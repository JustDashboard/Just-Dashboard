import { describe, expect, test } from "bun:test"
import cases from "./scan-target-cases.json"
import { parseScanQuery, parseScanTarget, targetLabel, tlsReportHref } from "./scan-target"

// The backend parses the same field again (proxysvc/scantarget.go) and
// TestParseScanTarget reads this same table, so the page never promises a
// target the server then reads differently.
describe("a scan target", () => {
  test("reads every case in the shared table as the backend does", () => {
    expect(cases.length).toBeGreaterThanOrEqual(20)
    for (const c of cases) {
      const parsed = parseScanTarget(c.input, c.port ?? 0)
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
