import { describe, expect, test } from "bun:test"
import {
  byUrgency,
  carries,
  durationError,
  formatDuration,
  includedPlace,
  listenFamily,
  listenLabel,
  moduleMissing,
  moduleRemedy,
  parseDuration,
  protocolLabel,
  saveBlocked,
  streamBody,
  streamOutage,
  streamSpecOf,
  streamsLive,
} from "./streams"

const entry = (overrides) => ({
  name: "db",
  listen: 5432,
  protocol: "tcp",
  upstream: "10.0.0.5:5432",
  proxyProtocol: false,
  allowFrom: [],
  path: "/etc/nginx/stream.d/db.conf",
  managed: true,
  open: true,
  unsupported: [],
  ...overrides,
})

describe("streamSpecOf", () => {
  // The save refuses unknown fields, so the listing's own must not ride along
  // when the form posts a stream it opened from the list.
  test("keeps the spec and drops what the listing added", () => {
    const spec = streamSpecOf(entry({ address: "127.0.0.1", timeout: 600, connectTimeout: 5 }))
    expect(Object.keys(spec).sort()).toEqual(
      [
        "address",
        "allowFrom",
        "connectTimeout",
        "listen",
        "name",
        "protocol",
        "proxyProtocol",
        "timeout",
        "udpMode",
        "upstream",
      ].sort(),
    )
    expect(JSON.parse(JSON.stringify(spec))).toEqual({
      name: "db",
      listen: 5432,
      address: "127.0.0.1",
      protocol: "tcp",
      upstream: "10.0.0.5:5432",
      proxyProtocol: false,
      timeout: 600,
      connectTimeout: 5,
      allowFrom: [],
    })
  })
})

describe("streamBody", () => {
  test("splits the allow list on commas and spaces", () => {
    expect(streamBody(entry({}), " 10.0.0.0/8,  203.0.113.9 ").allowFrom).toEqual([
      "10.0.0.0/8",
      "203.0.113.9",
    ])
  })

  test("sends a UDP mode only for UDP, long-lived unless chosen otherwise", () => {
    expect(streamBody(entry({ udpMode: "request" }), "").udpMode).toBeUndefined()
    expect(streamBody(entry({ protocol: "udp" }), "").udpMode).toBe("session")
    expect(streamBody(entry({ protocol: "udp", udpMode: "request" }), "").udpMode).toBe("request")
  })

  test("a stream of both protocols has UDP, so it carries the mode too", () => {
    expect(streamBody(entry({ protocol: "both" }), "").udpMode).toBe("session")
    expect(streamBody(entry({ protocol: "both", udpMode: "request" }), "").udpMode).toBe("request")
  })
})

describe("protocols", () => {
  test("both reads as TCP+UDP and counts as each", () => {
    expect(["tcp", "udp", "both"].map(protocolLabel)).toEqual(["TCP", "UDP", "TCP+UDP"])
    const both = entry({ protocol: "both" })
    expect([carries(both, "tcp"), carries(both, "udp")]).toEqual([true, true])
    expect([carries(entry({}), "tcp"), carries(entry({}), "udp")]).toEqual([true, false])
  })
})

describe("durations", () => {
  test("seconds, minutes and hours as nginx writes them", () => {
    for (const [text, seconds] of [
      ["", 0],
      ["  ", 0],
      ["90", 90],
      ["90s", 90],
      ["10m", 600],
      ["1h30m", 5400],
      ["1h 30m", 5400],
      ["1m30s", 90],
      ["1d", 86400],
      ["24h", 86400],
    ]) {
      expect(parseDuration(text)).toBe(seconds)
    }
  })

  test("anything nginx would not read, or longer than a day, is refused", () => {
    for (const text of ["10x", "m", "30m1h", "1.5h", "-5s", "10 5", "25h", "2d", "1M", "10ms"]) {
      expect(parseDuration(text)).toBeNull()
      expect(durationError(text, "10m")).toBe("Write it as 90s, 10m or 1h30m, up to 24h.")
    }
  })

  // A typed 0 read as empty: the form sent no timeout, nginx's default was
  // saved, and the field went on saying 0. To nginx a 0 is no time at all —
  // every connection is dropped at once — so it is refused, and said why.
  test("a written zero is refused rather than read as empty", () => {
    for (const text of ["0", "0s", " 0m ", "0h0m", "00"]) {
      expect(parseDuration(text)).toBeNull()
      expect(durationError(text, "60s")).toBe(
        "0 makes nginx drop every connection at once. Leave it empty for nginx's 60s.",
      )
    }
  })

  test("a stored timeout is shown the way it would be typed, and reads back", () => {
    for (const [seconds, text] of [
      [0, ""],
      [undefined, ""],
      [5, "5s"],
      [90, "1m30s"],
      [600, "10m"],
      [3600, "1h"],
      [3661, "1h1m1s"],
      [86400, "24h"],
    ]) {
      expect(formatDuration(seconds)).toBe(text)
      expect(parseDuration(text)).toBe(seconds ?? 0)
    }
  })
})

describe("module and include", () => {
  const status = (included, module) => ({
    included,
    module,
    snippet: "",
    dir: "/etc/nginx/stream.d",
    streams: [],
  })

  test("a stream is live only where nginx reads the directory and has the module", () => {
    expect(streamsLive(status(true, { state: "loaded", usable: true }))).toBe(true)
    expect(streamsLive(status(false, { state: "static", usable: true }))).toBe(false)
    expect(streamsLive(status(true, { state: "not-installed", usable: false }))).toBe(false)
    // nginx that could not be asked is not held against the stream.
    expect(streamsLive(status(true, { state: "unknown", usable: false }))).toBe(true)
  })

  test("unknown is never reported as missing", () => {
    expect(moduleMissing({ state: "unknown", usable: false })).toBe(false)
    expect(moduleMissing({ state: "absent", usable: false })).toBe(true)
  })

  test("says what gets the module in", () => {
    expect(
      moduleRemedy({ state: "not-installed", usable: false, package: "libnginx-mod-stream" }),
    ).toBe("Install libnginx-mod-stream, the package with nginx's stream module.")
    expect(moduleRemedy({ state: "not-installed", usable: false })).toContain(
      "your distribution's package",
    )
    expect(
      moduleRemedy({
        state: "not-loaded",
        usable: false,
        path: "/usr/lib/nginx/modules/ngx_stream_module.so",
      }),
    ).toBe(
      "Load it: add load_module /usr/lib/nginx/modules/ngx_stream_module.so; at the top of nginx.conf.",
    )
    expect(moduleRemedy({ state: "absent", usable: false })).toContain("built without it")
    expect(moduleRemedy({ state: "loaded", usable: true })).toBe("")
  })
})

describe("listenLabel", () => {
  test("names the address only when the stream is bound to one", () => {
    expect(listenLabel({ listen: 5432 })).toBe("5432")
    expect(listenLabel({ listen: 6000, address: "127.0.0.1" })).toBe("127.0.0.1:6000")
    expect(listenLabel({ listen: 6000, address: "::1" })).toBe("[::1]:6000")
  })

  // `listen 20003;` is 0.0.0.0: every IPv4 address, not one address to dial.
  test("a family's wildcard is the port, with the family said beside it", () => {
    expect(listenLabel({ listen: 20003, address: "0.0.0.0" })).toBe("20003")
    expect(listenLabel({ listen: 20003, address: "::" })).toBe("20003")
    expect(listenFamily("0.0.0.0")).toBe("IPv4")
    expect(listenFamily("::")).toBe("IPv6")
    expect(listenFamily(undefined)).toBeNull()
    expect(listenFamily("127.0.0.1")).toBeNull()
    expect(listenFamily("::1")).toBeNull()
  })
})

describe("saveBlocked", () => {
  const status = (overrides) => ({
    included: false,
    module: { state: "loaded", usable: true },
    snippet: "",
    dir: "/etc/nginx/stream.d",
    streams: [],
    ...overrides,
  })

  // nginx's test refuses every stream file in both, so no save can pass.
  test("a stream block without the module, and an include in the wrong block", () => {
    const missing = { state: "not-installed", usable: false }
    expect(saveBlocked(status({ included: true, module: missing }))).toBe("module")
    expect(saveBlocked(status({ includedIn: "http" }))).toBe("misplaced")
    expect(saveBlocked(status({ includedIn: "http", module: missing }))).toBe("misplaced")
  })

  test("not while a save can pass, whether or not nginx reads it yet", () => {
    expect(saveBlocked(status({ included: true }))).toBeNull()
    expect(saveBlocked(status({}))).toBeNull()
    expect(saveBlocked(status({ module: { state: "not-installed", usable: false } }))).toBeNull()
    expect(
      saveBlocked(status({ included: true, module: { state: "unknown", usable: false } })),
    ).toBeNull()
  })
})

describe("streamOutage", () => {
  const status = (overrides) => ({
    included: false,
    module: { state: "loaded", usable: true },
    snippet: "",
    dir: "/etc/nginx/stream.d",
    streams: [],
    ...overrides,
  })
  const missing = { state: "not-installed", usable: false }
  const file = entry({})

  // nginx reads a file included inside http as http, and its test refuses
  // proxy_pass there: every reload on the host fails, not only the streams'.
  test("a file included in the wrong block stops every reload, with or without the module", () => {
    expect(streamOutage(status({ includedIn: "http", streams: [file] }))).toBe("misplaced")
    expect(streamOutage(status({ includedIn: "http", module: missing, streams: [file] }))).toBe(
      "misplaced",
    )
    // An unreadable file is still a file nginx reads.
    expect(
      streamOutage(
        status({ includedIn: "http", streams: [entry({ error: "permission denied" })] }),
      ),
    ).toBe("misplaced")
  })

  test("a stream block with no module stops every reload, files or not", () => {
    expect(streamOutage(status({ included: true, module: missing }))).toBe("module")
    expect(streamOutage(status({ included: true, module: missing, streams: [file] }))).toBe(
      "module",
    )
  })

  // An empty directory included anywhere passes nginx's test: saves are
  // blocked, reloads are not.
  test("nothing stops a reload while the misplaced directory is empty", () => {
    expect(streamOutage(status({ includedIn: "http" }))).toBeNull()
    expect(saveBlocked(status({ includedIn: "http" }))).toBe("misplaced")
    expect(streamOutage(status({ included: true, streams: [file] }))).toBeNull()
    expect(streamOutage(status({ streams: [file] }))).toBeNull()
    expect(streamOutage(status({ module: missing, streams: [file] }))).toBeNull()
    expect(
      streamOutage(status({ included: true, module: { state: "unknown", usable: false } })),
    ).toBeNull()
  })
})

describe("includedPlace", () => {
  test("a block is inside, the top level is at", () => {
    expect(includedPlace("http")).toBe("inside http")
    expect(includedPlace("stream › server")).toBe("inside stream › server")
    expect(includedPlace("the top level, outside any block")).toBe(
      "at the top level, outside any block",
    )
  })
})

describe("byUrgency", () => {
  test("unreadable, then open database ports, then open, then restricted", () => {
    const streams = [
      entry({ name: "restricted", listen: 1000, open: false, allowFrom: ["10.0.0.0/8"] }),
      entry({ name: "game", listen: 25565 }),
      entry({ name: "pg", listen: 5432 }),
      entry({ name: "broken", listen: 0, error: "permission denied", open: false }),
      // allow all restricts nothing, so it sorts with the open ones.
      entry({ name: "all", listen: 7000, allowFrom: ["all"], open: true }),
    ]
    expect(streams.sort(byUrgency).map((s) => s.name)).toEqual([
      "broken",
      "pg",
      "all",
      "game",
      "restricted",
    ])
  })
})
