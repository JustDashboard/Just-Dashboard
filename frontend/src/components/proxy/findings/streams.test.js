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

  test("the include is named when it sits in the wrong block", () => {
    const findings = streamFindings({ streams: status({ includedIn: "http" }) })
    expect(ids(findings)).toEqual(["warning streams.not-included"])
    expect(findings[0].detail).toBe(
      "/etc/nginx/stream.d is included inside http, where nginx does not read it as streams.",
    )
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
