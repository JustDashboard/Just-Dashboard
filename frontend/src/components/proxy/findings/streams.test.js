import { describe, expect, test } from "bun:test"
import { streamFindings } from "./streams"

const stream = (overrides) => ({
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
const status = (overrides) => ({
  included: false,
  module: { state: "static", usable: true },
  snippet: "",
  dir: "/etc/nginx/stream.d",
  streams: [stream({ open: false, allowFrom: ["10.0.0.0/8"] })],
  ...overrides,
})
const ids = (findings) => findings.map((f) => `${f.level} ${f.id}`)

const notInstalled = { state: "not-installed", usable: false, package: "libnginx-mod-stream" }

describe("streamFindings", () => {
  // Telling this Ubuntu host to "add the include" would have stopped nginx
  // reloading: the module comes first.
  test("without the module, the advice is the module, not the include", () => {
    const findings = streamFindings({ streams: status({ module: notInstalled }) })
    expect(ids(findings)).toEqual(["warning streams.module-missing"])
    expect(findings[0].advice).toBe(
      "Install libnginx-mod-stream, the package with nginx's stream module. Then include /etc/nginx/stream.d from a top-level stream block.",
    )
  })

  test("a stream block nginx cannot read is critical: every reload fails", () => {
    const findings = streamFindings({ streams: status({ included: true, module: notInstalled }) })
    expect(ids(findings)).toEqual(["critical streams.module-missing"])
    expect(findings[0].detail).toContain("every reload is refused")
  })

  // nginx reads a file included inside http — as http — and its test refuses
  // proxy_pass there, so this is the module outage's equal, not "not reading".
  test("a stream file included in the wrong block is critical: every reload fails", () => {
    const findings = streamFindings({ streams: status({ includedIn: "http" }) })
    expect(ids(findings)).toEqual(["critical streams.misplaced"])
    expect(findings[0].title).toBe(
      "nginx refuses every reload: a stream file is included inside http",
    )
    expect(findings[0].detail).toBe(
      "/etc/nginx/stream.d is included inside http, where a stream is not allowed, so nginx's configuration test fails and every reload is refused — for every site on this host, not only the streams.",
    )
    expect(findings[0].advice).toBe(
      "Move the include into a top-level stream block beside the http block, or delete the stream files.",
    )
    expect(findings.map((f) => f.title).join(" ")).not.toContain("not reading")
  })

  test("the place is said as nginx has it, and files are counted", () => {
    const two = [stream({ name: "a" }), stream({ name: "b", open: false, allowFrom: ["10.0.0.1"] })]
    const findings = streamFindings({
      streams: status({ includedIn: "the top level, outside any block", streams: two }),
    })
    expect(findings[0].title).toBe(
      "nginx refuses every reload: 2 stream files are included at the top level, outside any block",
    )
  })

  // Moving the include into a stream block on a host without the module
  // swaps one outage for another: the include comes out, then the module.
  test("without the module, the include comes out first and the module before the stream block", () => {
    const findings = streamFindings({
      streams: status({ includedIn: "http", module: notInstalled }),
    })
    expect(ids(findings)).toEqual(["critical streams.misplaced"])
    expect(findings[0].advice).toBe(
      "Take out the include that puts /etc/nginx/stream.d there, which ends the refusals. Install libnginx-mod-stream, the package with nginx's stream module. Then include it from a top-level stream block.",
    )
    expect(findings[0].advice).not.toContain("Move the include")
  })

  // An empty directory included inside http passes nginx's test.
  test("nothing to say about an empty directory included in the wrong block", () => {
    expect(streamFindings({ streams: status({ includedIn: "http", streams: [] }) })).toEqual([])
  })

  test("nothing to say about an empty directory with no module", () => {
    expect(streamFindings({ streams: status({ module: notInstalled, streams: [] }) })).toEqual([])
  })

  // `allow all` restricts nothing: the backend says open, and so does this.
  test("open is the backend's reading, allow list or not", () => {
    const findings = streamFindings({
      streams: status({
        included: true,
        streams: [
          stream({ name: "everyone", allowFrom: ["all"], open: true }),
          stream({ name: "restricted", allowFrom: ["10.0.0.0/8"], open: false }),
          stream({ name: "broken", error: "permission denied", open: false }),
        ],
      }),
    })
    expect(ids(findings)).toEqual(["warning stream.open.everyone"])
  })
})
