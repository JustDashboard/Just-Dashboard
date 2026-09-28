import { describe, expect, test } from "bun:test"
import { ApiError } from "@/lib/api"
import { foldProxyFindings } from "@/components/proxy/attention"
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
      "Install libnginx-mod-stream from the Streams page, then connect /etc/nginx/stream.d there.",
    )
    // No package the host's manager has: the remedy in words, then the page.
    const unnamed = streamFindings({
      streams: status({ module: { state: "not-installed", usable: false } }),
    })
    expect(unnamed[0].advice).toBe(
      "Install your distribution's package for nginx's stream module. Then connect /etc/nginx/stream.d on the Streams page.",
    )
  })

  test("streams nginx does not read lead to the page's connect", () => {
    const findings = streamFindings({ streams: status() })
    expect(ids(findings)).toEqual(["warning streams.not-included"])
    expect(findings[0].advice).toBe(
      "Connect the directory on the Streams page, which shows the change to nginx.conf before it makes it.",
    )
    const unread = streamFindings({ streams: status({ includeError: "permission denied" }) })
    expect(unread[0].detail).toBe(
      "Whether nginx.conf includes /etc/nginx/stream.d could not be read: permission denied.",
    )
  })

  test("a stream block nginx cannot read is critical: every reload fails", () => {
    const findings = streamFindings({ streams: status({ included: true, module: notInstalled }) })
    expect(ids(findings)).toEqual(["critical streams.module-missing"])
    expect(findings[0].detail).toContain("every reload is refused")
    expect(findings[0].advice).toBe(
      "Install libnginx-mod-stream, the package with nginx's stream module. Or take the stream block out of nginx.conf.",
    )
    // The dashboard's own include can be taken out from the page.
    const ours = streamFindings({
      streams: status({
        included: true,
        module: notInstalled,
        connection: {
          mode: "dropin",
          path: "/etc/nginx/modules-enabled/zz-just-dashboard-stream.conf",
        },
      }),
    })
    expect(ours[0].advice).toBe(
      "Install libnginx-mod-stream, the package with nginx's stream module. Or disconnect the stream directory on the Streams page.",
    )
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

  test("a stream of both protocols is named as such", () => {
    const findings = streamFindings({
      streams: status({
        included: true,
        streams: [stream({ name: "dns", listen: 53, protocol: "both" })],
      }),
    })
    expect(findings.map((f) => f.detail)).toEqual([
      "TCP+UDP 53 → 10.0.0.5:5432 with no allow list.",
    ])
  })

  // The overview gave no finding for a directory it could not read, and so
  // said streams were within limits over four open forwards it could not see.
  describe("a directory that could not be read", () => {
    const unreadable = new ApiError(
      500,
      "stream_dir_unreadable",
      "open /etc/nginx/stream.d: permission denied",
    )

    test("is a warning of its own, not nothing to report", () => {
      const findings = streamFindings({ streamsError: unreadable })
      expect(ids(findings)).toEqual(["warning streams.unreadable"])
      expect(findings[0]).toMatchObject({
        title: "Could not read the stream directory",
        detail: "open /etc/nginx/stream.d: permission denied",
        advice:
          "Until it can be read, no stream is checked for an open port or for a reload it would stop.",
        href: "/proxy/streams",
      })
    })

    test("keeps what the last read found, and says that is what it is", () => {
      const findings = streamFindings({
        streams: status({ included: true, streams: [stream({ name: "db" })] }),
        streamsError: unreadable,
      })
      expect(ids(findings)).toEqual(["warning streams.unreadable", "warning stream.open.db"])
      expect(findings[0].advice).toBe(
        "What this list says about streams is from the last read that worked.",
      )
    })

    test("a request that failed on the way is not blamed on the directory", () => {
      const findings = streamFindings({ streamsError: new TypeError("Failed to fetch") })
      expect(findings[0]).toMatchObject({
        title: "Could not read the streams",
        detail: "Failed to fetch",
      })
    })

    test("reaches the overview's list, so it is not empty", () => {
      expect(foldProxyFindings({ streamsError: unreadable }).map((f) => f.id)).toEqual([
        "streams.unreadable",
      ])
    })
  })

  // Each stream's own state, once nginx reads the directory: a port it
  // cannot bind stops every reload on the host, and a stream shadowed or not
  // listening forwards nothing — where the page said "configured".
  describe("each stream's state", () => {
    const restricted = { open: false, allowFrom: ["10.0.0.0/8"] }
    const held = {
      ...restricted,
      name: "held",
      listen: 7000,
      state: "not-listening",
      stateReason:
        "Port 7000/tcp is held by postgres (pid 900), so nginx cannot bind it, and every reload fails until it is free.",
      blocker: { port: 7000, proto: "tcp", kind: "program", name: "postgres", pid: 900 },
      bindError: "2026/09/28 03:29:05 bind() to 0.0.0.0:7000 failed (98: Address in use)",
    }

    test("a port nginx cannot bind is critical, named with what holds it", () => {
      const findings = streamFindings({
        streams: status({ included: true, streams: [stream(held)] }),
      })
      expect(ids(findings)).toEqual(["critical stream.unbindable.held"])
      expect(findings[0]).toMatchObject({
        title:
          "nginx refuses every reload: stream held asks for port 7000/tcp, which postgres (pid 900) holds",
        detail: `${held.stateReason} nginx logged: ${held.bindError}`,
        href: "/proxy/streams",
      })
      const site = stream({
        ...held,
        name: "web",
        blocker: { port: 80, proto: "tcp", kind: "site", name: "app.example.com", site: "app" },
        bindError: undefined,
      })
      expect(
        streamFindings({ streams: status({ included: true, streams: [site] }) })[0].title,
      ).toBe(
        "nginx refuses every reload: stream web asks for port 80/tcp, which the site app.example.com holds",
      )
    })

    test("a stream file that does not parse is critical", () => {
      const broken = stream({
        ...restricted,
        name: "broken",
        state: "not-read",
        unsupported: ["a syntax error (unexpected end of file)"],
      })
      expect(
        ids(streamFindings({ streams: status({ included: true, streams: [broken] }) })),
      ).toEqual(["critical stream.unparsable.broken"])
    })

    test("not listening and shadowed are warnings", () => {
      const findings = streamFindings({
        streams: status({
          included: true,
          streams: [
            stream({
              ...restricted,
              name: "quiet",
              state: "not-listening",
              stateReason: "nginx holds no socket for port 7100/tcp.",
            }),
            stream({
              ...restricted,
              name: "copy",
              state: "shadowed",
              blocker: { port: 5432, proto: "tcp", kind: "stream", name: "db" },
            }),
            stream({ ...restricted, name: "fine", state: "live" }),
          ],
        }),
      })
      expect(ids(findings)).toEqual([
        "warning stream.not-listening.quiet",
        "warning stream.shadowed.copy",
      ])
      expect(findings[1].title).toBe("Stream copy gets no connection: the stream db has its port")
    })

    // While nginx reads none of the directory, or cannot, the directory's own
    // finding says so, once.
    test("say nothing of their own while the directory is not read", () => {
      for (const streams of [
        status({ included: false, streams: [stream(held)] }),
        status({ included: true, module: notInstalled, streams: [stream(held)] }),
      ]) {
        expect(
          ids(streamFindings({ streams })).some((id) => id.includes("stream.unbindable")),
        ).toBe(false)
      }
    })
  })
})
