import { describe, expect, test } from "bun:test"
import { certbotRunning, expiredAgo, lineageActivity, parseDomains } from "./certificates"

const now = Date.parse("2026-09-27T12:00:00Z")
const job = (overrides) => ({
  id: "j1",
  kind: "certbot.renew",
  title: "Renewing app.example.com",
  target: "app.example.com",
  status: "running",
  exitCode: 0,
  startedAt: "2026-09-27T11:59:00Z",
  lines: 0,
  ...overrides,
})

describe("how long ago a certificate expired", () => {
  test("hours ago is today, not 0 days ago", () => {
    expect(expiredAgo("2026-09-27T09:00:00Z", now)).toBe("today")
    expect(expiredAgo("2026-09-27T11:59:59Z", now)).toBe("today")
  })

  test("a day or more is counted in whole days", () => {
    expect(expiredAgo("2026-09-26T11:00:00Z", now)).toBe("yesterday")
    expect(expiredAgo("2026-09-24T12:00:00Z", now)).toBe("3 days ago")
  })
})

describe("a certbot run in the console", () => {
  test("only a running certbot job holds certbot", () => {
    expect(certbotRunning(job())).toBe(true)
    expect(certbotRunning(job({ status: "succeeded" }))).toBe(false)
    expect(certbotRunning(job({ kind: "packages.install" }))).toBe(false)
    expect(certbotRunning(null)).toBe(false)
  })

  test("the lineage a job acts on says what is happening to it", () => {
    expect(lineageActivity(job(), "app.example.com")).toBe("Renewing…")
    expect(
      lineageActivity(job({ title: "Dry run: renewing app.example.com" }), "app.example.com"),
    ).toBe("Testing renewal…")
    expect(lineageActivity(job({ kind: "certbot.revoke" }), "app.example.com")).toBe("Revoking…")
    // Another lineage, a renew-all, or a finished job says nothing.
    expect(lineageActivity(job(), "shop.example.com")).toBeUndefined()
    expect(
      lineageActivity(job({ target: "every certificate due" }), "app.example.com"),
    ).toBeUndefined()
    expect(lineageActivity(job({ status: "failed" }), "app.example.com")).toBeUndefined()
  })
})

describe("the names typed into the issue form", () => {
  test("any mix of spaces, commas and new lines separates them", () => {
    expect(parseDomains(" app.example.com,www.app.example.com\n*.example.com  ")).toEqual([
      "app.example.com",
      "www.app.example.com",
      "*.example.com",
    ])
    expect(parseDomains("   ")).toEqual([])
  })
})
