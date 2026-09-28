import { describe, expect, test } from "bun:test"
import {
  STREAM_STATES,
  blocksReloads,
  byUrgency,
  carries,
  connectChange,
  disconnectChange,
  moduleLabel,
  durationError,
  formatDuration,
  includedPlace,
  listenFamily,
  listenLabel,
  moduleMissing,
  moduleRemedy,
  parseDuration,
  portOwnerName,
  protocolLabel,
  runningInstall,
  saveBlocked,
  saveOutcome,
  stateCounts,
  stateStatus,
  stateTitle,
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
  state: "live",
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

describe("connecting the directory", () => {
  test("names each module state in a word or two", () => {
    expect(moduleLabel({ state: "static", usable: true })).toBe("built in")
    expect(moduleLabel({ state: "not-installed", usable: false })).toBe("not installed")
    expect(moduleLabel({ state: "absent", usable: false })).toBe("not in this build")
    expect(moduleLabel({ state: "unknown", usable: false })).toBe("could not check")
  })

  test("says what each change touches, and that disconnecting takes out only that", () => {
    const dropIn = "/etc/nginx/modules-enabled/zz-just-dashboard-stream.conf"
    expect(connectChange({ mode: "dropin", path: dropIn, keepsCopy: false })).toMatch(
      /^Creates zz-just-dashboard-stream\.conf in \/etc\/nginx\/modules-enabled\. .*nginx\.conf stays/,
    )
    expect(
      connectChange({
        mode: "nginx.conf",
        path: "/etc/nginx/nginx.conf",
        reason: "no-directory",
        keepsCopy: true,
      }),
    ).toBe(
      "Adds a stream block to the end of nginx.conf, after every module it loads: it includes no directory at its top level where a file of its own could go. The file as it is now is kept beside it.",
    )
    expect(
      connectChange({ mode: "stream-block", path: "/etc/nginx/streams.conf", keepsCopy: true }),
    ).toBe(
      "Adds one include line to the stream block in /etc/nginx/streams.conf. nginx refuses a second stream block, so the directory goes into the one that is there. The file as it is now is kept beside it.",
    )
    expect(disconnectChange("dropin", dropIn)).toBe(
      `It removes ${dropIn}, the file the dashboard added.`,
    )
    expect(disconnectChange("nginx.conf", "/etc/nginx/nginx.conf")).toContain(
      "the stream block the dashboard added to the end of nginx.conf",
    )
  })
})

describe("connectChange", () => {
  const dropIn = "/etc/nginx/modules-enabled/zz-just-dashboard-stream.conf"
  const edit = (reason, keepsCopy = true) =>
    connectChange({ mode: "nginx.conf", path: "/etc/nginx/nginx.conf", reason, dropIn, keepsCopy })

  test("gives the reason nginx.conf is edited that the plan gives, and only that one", () => {
    expect(edit("load-module-after")).toContain(
      ": it includes /etc/nginx/modules-enabled at its top level, but a module is loaded after that directory, and nginx refuses a module loaded after a stream block.",
    )
    expect(edit("directory-elsewhere")).toContain(
      ": the directory it includes at its top level, /etc/nginx/modules-enabled, leads outside the nginx directory",
    )
    expect(edit("name-taken")).toContain(
      ": /etc/nginx/modules-enabled, which it includes at its top level, already holds a zz-just-dashboard-stream.conf that is not the dashboard's.",
    )
    for (const reason of ["load-module-after", "directory-elsewhere", "name-taken"]) {
      expect(edit(reason)).not.toContain("includes no directory")
    }
  })

  test("promises a copy only where the plan keeps one", () => {
    expect(edit("no-directory", false)).not.toContain("kept")
    expect(edit("no-directory", true)).toMatch(/kept beside it\.$/)
    expect(
      connectChange({ mode: "stream-block", path: "/etc/nginx/main.d/streams", keepsCopy: false }),
    ).not.toContain("kept")
  })
})

describe("runningInstall", () => {
  const job = (over) => ({
    id: "job-1",
    kind: "packages.install",
    title: "Installing libnginx-mod-stream",
    target: "libnginx-mod-stream",
    status: "running",
    exitCode: 0,
    startedAt: "",
    lines: 0,
    ...over,
  })

  test("finds a running install of the package, alone or among others", () => {
    expect(runningInstall([job()], "libnginx-mod-stream")?.id).toBe("job-1")
    expect(
      runningInstall([job({ target: "htop, libnginx-mod-stream" })], "libnginx-mod-stream")?.id,
    ).toBe("job-1")
  })

  test("passes over a finished install, another package, and other kinds of job", () => {
    for (const other of [
      job({ status: "succeeded" }),
      job({ status: "failed" }),
      job({ target: "libnginx-mod-stream-geoip2" }),
      job({ kind: "packages.remove" }),
      job({ target: undefined }),
    ]) {
      expect(runningInstall([other], "libnginx-mod-stream")).toBeUndefined()
    }
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

  // A stream nginx cannot bind stops every reload on the host; one that
  // forwards nothing, or that another shadows, comes before any live one.
  test("a stream stopping every reload, then not listening, then shadowed, then the rest", () => {
    const held = { port: 7000, proto: "tcp", kind: "program", name: "postgres", pid: 900 }
    const streams = [
      entry({ name: "open-db", listen: 5432 }),
      entry({ name: "shadowed", listen: 6000, state: "shadowed", open: false }),
      entry({ name: "quiet", listen: 7100, state: "not-listening", open: false }),
      entry({ name: "held", listen: 7000, state: "not-listening", blocker: held, open: false }),
      entry({ name: "fine", listen: 1000, open: false }),
    ]
    expect(streams.sort(byUrgency).map((s) => s.name)).toEqual([
      "held",
      "quiet",
      "shadowed",
      "open-db",
      "fine",
    ])
  })
})

describe("stream states", () => {
  test("each state reads as a verdict on the card, and an unreadable file as unreadable", () => {
    expect(stateStatus(entry({}))).toEqual({ verdict: "ok", label: "live" })
    expect(stateStatus(entry({ state: "not-listening" }))).toEqual({
      verdict: "critical",
      label: "not listening",
    })
    expect(stateStatus(entry({ state: "shadowed" }))).toEqual({
      verdict: "warning",
      label: "shadowed",
    })
    expect(stateStatus(entry({ state: "not-read" }))).toEqual({
      verdict: "warning",
      label: "not read",
    })
    // Never checked is not a verdict either way.
    expect(stateStatus(entry({ state: "unknown" }))).toEqual({ tone: "unknown", label: "unknown" })
    expect(stateStatus(entry({ state: "unknown", error: "permission denied" }))).toEqual({
      verdict: "critical",
      label: "unreadable",
    })
    expect(STREAM_STATES.map(stateTitle)).toEqual([
      "Live",
      "Not listening",
      "Shadowed",
      "Not read",
      "Unknown",
      "Paused",
    ])
  })

  test("counts every stream once, under its state", () => {
    const counts = stateCounts([
      entry({}),
      entry({ name: "b" }),
      entry({ name: "c", state: "shadowed" }),
      entry({ name: "d", state: "unknown", error: "gone" }),
    ])
    expect(counts).toEqual({
      all: 4,
      live: 2,
      "not-listening": 0,
      shadowed: 1,
      "not-read": 0,
      unknown: 1,
      paused: 0,
    })
  })

  test("only a stream something else holds the port of stops every reload", () => {
    const site = { port: 80, proto: "tcp", kind: "site", name: "example.com", site: "example.com" }
    expect(blocksReloads(entry({ state: "not-listening", blocker: site }))).toBe(true)
    // Not taken up yet, or its last bind failed and the port is free now.
    expect(blocksReloads(entry({ state: "not-listening" }))).toBe(false)
    expect(blocksReloads(entry({ state: "shadowed", blocker: { ...site, kind: "stream" } }))).toBe(
      false,
    )
  })

  test("what holds a port is named as the backend's sentences name it", () => {
    const at = { port: 5432, proto: "tcp" }
    expect(portOwnerName({ ...at, kind: "stream", name: "replica" })).toBe("the stream replica")
    expect(portOwnerName({ ...at, kind: "stream", file: "/etc/nginx/nginx.conf" })).toBe(
      "a stream server in /etc/nginx/nginx.conf",
    )
    expect(portOwnerName({ ...at, kind: "site", name: "app.example.com" })).toBe(
      "the site app.example.com",
    )
    expect(portOwnerName({ ...at, kind: "site", file: "/etc/nginx/nginx.conf" })).toBe(
      "an http server in /etc/nginx/nginx.conf",
    )
    expect(portOwnerName({ ...at, kind: "program", name: "postgres", pid: 900 })).toBe(
      "postgres (pid 900)",
    )
    expect(portOwnerName({ ...at, kind: "program" })).toBe("another program")
  })
})

describe("saveOutcome", () => {
  const res = (overrides) => ({
    name: "replica",
    path: "/etc/nginx/stream.d/replica.conf",
    content: "",
    warnings: [],
    reloaded: true,
    ...overrides,
  })

  test("listening is said only when nginx was watched holding the port", () => {
    expect(saveOutcome(res({ listening: true }), true)).toEqual({
      tone: "success",
      title: "replica saved and listening",
      description: undefined,
    })
  })

  // "Forwarding" was what the page said while nginx had failed to bind.
  test("a reload nginx had not taken up is a warning with a way to ask again", () => {
    expect(
      saveOutcome(
        res({
          listening: false,
          listenNote: "nginx had not taken the reload up 3s after it was sent.",
        }),
        true,
      ),
    ).toEqual({
      tone: "warning",
      title: "replica saved, not listening yet",
      description: "nginx had not taken the reload up 3s after it was sent.",
      recheck: true,
    })
  })

  test("a reload nobody could watch says so, and is not called listening", () => {
    const outcome = saveOutcome(
      res({
        listenNote:
          "Whether nginx took it up could not be checked: no running nginx reads /etc/nginx/nginx.conf.",
      }),
      true,
    )
    expect(outcome.tone).toBe("success")
    expect(outcome.title).toBe("replica saved and reloaded")
    expect(outcome.description).toContain("could not be checked")
  })

  test("a failed reload, a rename and a directory nginx does not read keep their own words", () => {
    expect(
      saveOutcome(
        res({
          reloaded: false,
          reloadError:
            "nginx did not take the reload up: bind() to 0.0.0.0:9999 failed (98: Address in use)",
        }),
        true,
      ),
    ).toEqual({
      tone: "warning",
      title: "replica saved, reload failed",
      description:
        "The file passed nginx's test and is on disk, but nginx did not reload, so it is not forwarding yet: nginx did not take the reload up: bind() to 0.0.0.0:9999 failed (98: Address in use)",
    })
    expect(
      saveOutcome(res({ renamed: "old", listening: true, warnings: ["No authentication."] }), true),
    ).toEqual({
      tone: "success",
      title: "old renamed to replica saved and listening",
      description: "No authentication. old.conf is kept as old.conf.bak.",
    })
    expect(saveOutcome(res({ reloaded: false }), false).title).toBe("replica saved, not yet live")
  })
})
