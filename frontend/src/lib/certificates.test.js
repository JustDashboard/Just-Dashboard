import { describe, expect, test } from "bun:test"
import {
  afterReload,
  authorityName,
  certbotRunning,
  expiredAgo,
  lineageActivity,
  parseDomains,
  replacingTestCertificate,
  sameNames,
  stillServingTest,
  testCertificateReplaced,
  testRunPassed,
} from "./certificates"

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

  const app = { name: "app.example.com", domains: ["app.example.com", "www.app.example.com"] }
  const shop = { name: "shop.example.com", domains: ["shop.example.com"] }

  test("the lineage a job acts on says what is happening to it", () => {
    expect(lineageActivity(job(), app)).toBe("Renewing…")
    expect(lineageActivity(job({ title: "Dry run: renewing app.example.com" }), app)).toBe(
      "Testing renewal…",
    )
    expect(lineageActivity(job({ kind: "certbot.revoke" }), app)).toBe("Revoking…")
    // Another lineage, a renew-all, or a finished job says nothing.
    expect(lineageActivity(job(), shop)).toBeUndefined()
    expect(lineageActivity(job({ target: "every certificate due" }), app)).toBeUndefined()
    expect(lineageActivity(job({ status: "failed" }), app)).toBeUndefined()
  })

  // The issuance names the certificate by its names, not the lineage's.
  const replacement = (overrides) =>
    job({
      kind: "certbot.issue",
      title: "Replacing the test certificate for www.app.example.com, app.example.com",
      target: "www.app.example.com, app.example.com",
      ...overrides,
    })

  test("a lineage whose test certificate is being replaced says so", () => {
    expect(lineageActivity(replacement(), app)).toBe("Replacing…")
    expect(
      replacingTestCertificate(replacement(), ["APP.example.com", "www.app.example.com"]),
    ).toBe(true)
    // A subset of its names is another certificate to certbot.
    expect(replacingTestCertificate(replacement(), ["app.example.com"])).toBe(false)
    expect(lineageActivity(replacement(), shop)).toBeUndefined()
    // A plain issuance for the same names replaces nothing, and a finished one is done.
    expect(
      lineageActivity(
        replacement({ title: "Issuing a certificate for www.app.example.com, app.example.com" }),
        app,
      ),
    ).toBeUndefined()
    expect(lineageActivity(replacement({ status: "succeeded" }), app)).toBeUndefined()
  })
})

describe("what a finished issuance means for the page", () => {
  test("a test run that passed offers the real issuance for its names", () => {
    const passed = job({
      kind: "certbot.issue",
      title: "Test issuance for app.example.com, www.app.example.com",
      target: "app.example.com, www.app.example.com",
      status: "succeeded",
    })
    expect(testRunPassed(passed)).toBe("app.example.com, www.app.example.com")
    expect(parseDomains(testRunPassed(passed))).toEqual(["app.example.com", "www.app.example.com"])
    expect(testRunPassed({ ...passed, status: "failed" })).toBeUndefined()
    expect(testRunPassed({ ...passed, title: "Issuing a certificate for app.example.com" })).toBe(
      undefined,
    )
    expect(testRunPassed(null)).toBeUndefined()
  })

  test("a replaced test certificate is named, so the page can find the sites still serving it", () => {
    const replaced = job({
      kind: "certbot.issue",
      title: "Replacing the test certificate for app.example.com, www.app.example.com",
      target: "app.example.com, www.app.example.com",
      status: "succeeded",
    })
    expect(testCertificateReplaced(replaced)).toEqual(["app.example.com", "www.app.example.com"])
    expect(testCertificateReplaced({ ...replaced, status: "running" })).toBeUndefined()
    expect(testCertificateReplaced({ ...replaced, status: "failed" })).toBeUndefined()
    expect(
      testCertificateReplaced({ ...replaced, title: "Issuing a certificate for app.example.com" }),
    ).toBeUndefined()
  })

  test("only a site that answered with a test certificate is still serving one", () => {
    // What each site that names the certificate answered over a handshake:
    // a deploy hook may have reloaded nginx already, and a site that could
    // not be asked, or serves something else entirely, is claimed neither way.
    const served = [
      { site: "stale", name: "app.example.com", current: false, staging: true },
      { site: "reloaded", name: "app.example.com", current: true },
      { site: "down", name: "app.example.com", current: false, error: "connection refused" },
      { site: "elsewhere", name: "app.example.com", current: false, issuer: "Company CA" },
    ]
    expect(stillServingTest(served)).toEqual(["stale"])
    expect(stillServingTest(served.slice(1))).toEqual([])
    expect(stillServingTest(undefined)).toEqual([])
  })

  test("a reload is reported by what the sites answered afterwards", () => {
    const current = (site) => ({ site, name: "app.example.com", current: true })
    const test = (site) => ({ site, name: "app.example.com", current: false, staging: true })
    expect(afterReload(["app", "www"], [current("app"), current("www")])).toEqual({
      ok: true,
      description: "app, www serve the real certificate now.",
    })
    expect(afterReload(["app", "www"], [current("app"), test("www")])).toEqual({
      ok: false,
      description: "app serves the real certificate now. www still serves the test certificate.",
    })
    // A site that no longer answers, or is no longer asked, is not claimed.
    expect(
      afterReload(
        ["app", "www"],
        [{ site: "app", name: "app.example.com", current: false, error: "refused" }],
      ),
    ).toEqual({ ok: false, description: "What app, www serve could not be checked." })
  })

  test("names are the same set whatever their order or case", () => {
    expect(sameNames(["a.example.com", "B.example.com"], ["b.example.com", "a.example.com"])).toBe(
      true,
    )
    expect(sameNames(["a.example.com"], ["a.example.com", "b.example.com"])).toBe(false)
    expect(sameNames([], [])).toBe(true)
  })
})

describe("a configured ACME authority", () => {
  test("is named by its host", () => {
    expect(authorityName("https://ca.internal:9000/acme/acme/directory")).toBe("ca.internal:9000")
    expect(authorityName("https://acme.zerossl.com/v2/DV90")).toBe("acme.zerossl.com")
  })

  test("a value that is not a URL is shown as it is", () => {
    expect(authorityName("not a url")).toBe("not a url")
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
