import { describe, expect, test } from "bun:test"
import cases from "./scan-target-cases.json"
import {
  RECENT_TARGETS,
  parseScanQuery,
  parseScanTarget,
  scanSuggestions,
  targetHint,
  targetLabel,
  tlsListenPorts,
  tlsReportHref,
  withRecent,
} from "./scan-target"

// The backend parses the same field again (proxysvc/scantarget.go) and
// TestParseScanTarget reads this same table, so the page never promises a
// target the server then reads differently.
describe("a scan target", () => {
  test("reads every case in the shared table as the backend does", () => {
    expect(cases.length).toBeGreaterThanOrEqual(20)
    for (const c of cases) {
      // A case with portText is a port given as text — ?port= or the port
      // field — and goes through parseScanQuery, as the backend's does.
      const parsed =
        c.portText === undefined
          ? parseScanTarget(c.input, c.port ?? 0)
          : parseScanQuery(c.input, c.portText)
      if (c.error) {
        expect({ input: c.input, error: typeof parsed.error }).toEqual({
          input: c.input,
          error: "string",
        })
      } else {
        expect({ input: c.input, target: parsed.target }).toEqual({
          input: c.input,
          target: { host: c.host, port: c.want },
        })
      }
    }
  })

  test("says why a pasted address is refused", () => {
    expect(parseScanTarget("user@app.example.com").error).toContain("user name")
    expect(parseScanTarget("mail.example.com:993", 443).error).toContain("993")
    expect(parseScanTarget("ftp://files.example.com").error).toContain("ftp://")
  })
})

describe("a report link", () => {
  test("takes its port in the name or beside it, and refuses one that is not a number", () => {
    const mail = { host: "mail.example.com", port: 993 }
    expect(parseScanQuery("mail.example.com:993", null).target).toEqual(mail)
    expect(parseScanQuery("mail.example.com", "993").target).toEqual(mail)
    expect(parseScanQuery("mail.example.com", "").target).toEqual({ ...mail, port: 443 })
    expect(parseScanQuery("mail.example.com", "imap").error).toContain("not a number")
    // "0" is a port someone typed, not the absence of one.
    expect(parseScanQuery("mail.example.com", "0").error).toContain("outside 1–65535")
  })
})

describe("the hint under the field", () => {
  test("names the host and the port the scan will reach", () => {
    const parsed = parseScanQuery("imaps://Mail.Example.com/", "")
    expect(targetHint(parsed.target)).toBe("mail.example.com, port 993")
    expect(targetHint(parseScanTarget("bücher.example").target)).toBe(
      "xn--bcher-kva.example, port 443",
    )
  })
})

describe("a target as text", () => {
  test("keeps a port off 443 and brackets an IPv6 address that has one", () => {
    expect(targetLabel({ host: "app.example.com", port: 443 })).toBe("app.example.com")
    expect(targetLabel({ host: "mail.example.com", port: 993 })).toBe("mail.example.com:993")
    expect(targetLabel({ host: "2001:db8::1", port: 443 })).toBe("2001:db8::1")
    expect(targetLabel({ host: "2001:db8::1", port: 993 })).toBe("[2001:db8::1]:993")
  })

  test("round-trips through the report link", () => {
    for (const target of [
      { host: "mail.example.com", port: 993 },
      { host: "2001:db8::1", port: 8443 },
      { host: "xn--bcher-kva.example", port: 443 },
    ]) {
      const domain = new URL(tlsReportHref(target), "http://x").searchParams.get("domain")
      expect(parseScanTarget(domain).target).toEqual(target)
    }
    expect(tlsReportHref({ host: "mail.example.com", port: 993 })).toBe(
      "/proxy/tls?domain=mail.example.com%3A993",
    )
  })
})

describe("the targets the field offers", () => {
  const site = (name, serverNames, listen, extra = {}) => ({
    name,
    kind: "nginx",
    enabled: true,
    serverNames,
    listen,
    ...extra,
  })

  test("reads the TLS ports off a site's listen lines", () => {
    expect(tlsListenPorts(["443 ssl", "[::]:443 ssl http2", "80"])).toEqual([443])
    expect(tlsListenPorts(["127.0.0.1:8443 ssl", "443 quic reuseport"])).toEqual([8443, 443])
    expect(tlsListenPorts(["unix:/run/app.sock ssl", "80", "[::]:80"])).toEqual([])
    // A bare address listens on 80, as nginx reads it.
    expect(tlsListenPorts(["127.0.0.1 ssl"])).toEqual([80])
  })

  test("offers recent scans first, then sites, the watch list and certificates", () => {
    const got = scanSuggestions({
      recent: ["mail.example.com:993", "app.example.com"],
      sites: [
        site("app.conf", ["app.example.com", "www.example.com"], ["443 ssl", "80"]),
        site("admin.conf", ["admin.example.com"], ["8443 ssl", "443 ssl"]),
        site("plain.conf", ["plain.example.com"], ["80"]),
        site("off.conf", ["off.example.com"], ["443 ssl"], { enabled: false }),
        site(
          "odd.conf",
          ["_", "~^(?<sub>.+)\\.example\\.com$", "*.example.com", "$hostname"],
          ["443 ssl"],
        ),
        site("apex.conf", [".example.net"], ["443 ssl"]),
        site("Caddyfile", ["https://shop.example.com:8443", ":80"], [], { kind: "caddy" }),
      ],
      watched: [
        { domain: "mail.example.com", port: 993 },
        { domain: "status.example.com", port: 443 },
      ],
      certificates: [
        { name: "example.com", domains: ["example.com", "*.example.com", "www.example.com"] },
      ],
    })
    expect(got).toEqual([
      { value: "mail.example.com:993", source: "Scanned recently" },
      { value: "app.example.com", source: "Scanned recently" },
      { value: "admin.example.com", source: "Site admin.conf" },
      { value: "admin.example.com:8443", source: "Site admin.conf" },
      { value: "example.net", source: "Site apex.conf" },
      { value: "plain.example.com", source: "Site plain.conf" },
      { value: "shop.example.com:8443", source: "Site Caddyfile" },
      { value: "www.example.com", source: "Site app.conf" },
      { value: "status.example.com", source: "Watched" },
      { value: "example.com", source: "Certificate" },
    ])
  })

  test("skips a remembered entry that no longer reads as a target", () => {
    expect(scanSuggestions({ recent: ["exa mple.com", "app.example.com"] })).toEqual([
      { value: "app.example.com", source: "Scanned recently" },
    ])
  })

  test("keeps the newest scans, each once", () => {
    let recent = []
    for (const label of ["a.example", "b.example", "a.example"]) recent = withRecent(recent, label)
    expect(recent).toEqual(["a.example", "b.example"])
    for (let i = 0; i < 20; i++) recent = withRecent(recent, `host${i}.example`)
    expect(recent).toHaveLength(RECENT_TARGETS)
    expect(recent[0]).toBe("host19.example")
  })
})
