import { describe, expect, test } from "bun:test"
import {
  byUrgency,
  listenFamily,
  listenLabel,
  moduleMissing,
  moduleRemedy,
  saveBlocked,
  streamBody,
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
