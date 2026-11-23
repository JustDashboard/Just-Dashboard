import { expect, test } from "bun:test"
import { ApiError } from "./api"
import {
  addressIn,
  appDomainStateOf,
  certificateReading,
  handleChecks,
  handleValid,
  normalizeHandle,
  refusalWords,
} from "./app-domain"

const met = (value) =>
  Object.fromEntries(handleChecks(value).map((check) => [check.key, check.met]))

test("a handle meets the site's four rules, each on its own", () => {
  expect(met("")).toEqual({ length: false, characters: false, ends: false, double: false })
  expect(met("ac")).toEqual({ length: false, characters: true, ends: true, double: true })
  expect(met("acme")).toEqual({ length: true, characters: true, ends: true, double: true })
  expect(met("a".repeat(21)).length).toBe(false)
  expect(met("a".repeat(20)).length).toBe(true)
  expect(met("ac_me").characters).toBe(false)
  expect(met("-acme").ends).toBe(false)
  expect(met("acme-").ends).toBe(false)
  expect(met("xn--acme").double).toBe(false)
  expect(handleValid("acme-shop")).toBe(true)
  expect(handleValid("xn--acme")).toBe(false)
  // The site lowercases before it checks, and so does the field.
  expect(normalizeHandle("  ACME ")).toBe("acme")
  expect(handleValid("ACME")).toBe(true)
})

const status = (domain, zone = "just-dashboard.com") => ({
  enabled: true,
  linked: true,
  available: true,
  zone,
  reason: null,
  publicAddress: "203.0.113.10",
  autoAssign: true,
  domain,
  certificate: null,
  usedBy: [],
})
const held = (status, handle = "acme") => ({
  handle,
  hostname: `${handle}.just-dashboard.com`,
  wildcard: `*.${handle}.just-dashboard.com`,
  address: "203.0.113.10",
  status,
  reason: null,
  updatedAt: 1795000000,
})

test("a name is the server's, detached, or not an app name at all", () => {
  const active = status(held("active"))
  expect(appDomainStateOf("shop.acme.just-dashboard.com", undefined, active)).toBe("active")
  expect(appDomainStateOf("SHOP.Acme.just-dashboard.com", undefined, active)).toBe("active")
  // Another handle was never this server's: an ordinary name, as the backend judges it.
  expect(appDomainStateOf("shop.other.just-dashboard.com", undefined, active)).toBeUndefined()
  expect(
    appDomainStateOf("shop.acme.just-dashboard.com", undefined, status(held("suspended"))),
  ).toBe("detached")
  expect(appDomainStateOf("shop.acme.just-dashboard.com", undefined, status(null))).toBeUndefined()
  // The zone's own names: the site that runs it may well be deployed from this dashboard.
  expect(appDomainStateOf("just-dashboard.com", undefined, status(null))).toBeUndefined()
  expect(appDomainStateOf("www.just-dashboard.com", undefined, status(null))).toBeUndefined()
  // The server name and other names in the zone are not app names.
  expect(appDomainStateOf("acme.just-dashboard.com", undefined, active)).toBeUndefined()
  expect(appDomainStateOf("a.b.acme.just-dashboard.com", undefined, active)).toBeUndefined()
  expect(appDomainStateOf("shop.example.com", undefined, active)).toBeUndefined()
  // Nothing known about the zone, nothing said.
  expect(appDomainStateOf("shop.acme.just-dashboard.com", undefined, undefined)).toBeUndefined()
  // The backend's own mark wins over the reading.
  expect(
    appDomainStateOf(
      "shop.acme.just-dashboard.com",
      { hostname: "shop.acme.just-dashboard.com", appDomain: { status: "detached" } },
      active,
    ),
  ).toBe("detached")
})

test("the certificate reads as a dot, a word and the one thing to do", () => {
  expect(certificateReading(null)).toMatchObject({ label: "No certificate yet", action: "Issue" })
  expect(certificateReading({ name: "x", state: "issuing", expiresAt: null, error: null })).toEqual(
    { tone: "notice", label: "Issuing", issuing: true },
  )
  expect(
    certificateReading({ name: "x", state: "valid", expiresAt: 1802000000, error: null }),
  ).toMatchObject({
    tone: "running",
    action: "Renew",
    label: expect.stringMatching(/^Valid until /),
  })
  expect(
    certificateReading({ name: "x", state: "expiring", expiresAt: 1802000000, error: null }),
  ).toMatchObject({ tone: "warning", action: "Renew" })
  expect(
    certificateReading({ name: "x", state: "failed", expiresAt: null, error: "boom" }),
  ).toMatchObject({ tone: "danger", action: "Issue", label: "Issuing failed" })
})

const refused = (code, message = "refused", extra) =>
  new ApiError(409, code, message, undefined, extra)

test("every refusal is said as what happened and what to do", () => {
  const known = status(held("active"))
  const codes = [
    "not_linked",
    "session_required",
    "domain_in_use",
    "invalid_handle",
    "invalid_address",
    "limit_reached",
    "account_suspended",
    "domain_suspended",
    "no_domain",
    "handle_reserved",
    "handle_taken",
    "handle_quarantined",
    "address_mismatch",
    "too_many_challenges",
    "rate_limited",
    "dns_failed",
    "domains_unavailable",
    "unknown_server",
    "server_removed",
    "stale_time",
  ]
  for (const code of codes) {
    const words = refusalWords(refused(code, "the site's words"), known)
    expect(words.length, code).toBeGreaterThan(20)
    // Our sentence, not the server's code.
    expect(words, code).not.toContain(code)
  }
  expect(refusalWords(refused("handle_reserved"), known)).toContain("just-dashboard.com")
  expect(refusalWords(refused("invalid_address"), known)).toContain("203.0.113.10")
  // The quarantine's day is in the site's message, so it is kept.
  expect(
    refusalWords(refused("handle_quarantined", "It frees up on 22 December 2026."), known),
  ).toContain("22 December 2026")
  expect(refusalWords(refused("rate_limited", "", { retryAfterMs: 600_000 }), known)).toContain(
    "10 minutes",
  )
  // An unknown code is the server's own sentence.
  expect(refusalWords(refused("brand_new", "Something new."), known)).toBe("Something new.")
  expect(refusalWords(new Error("offline"), known)).toBe("offline")
})

test("address_mismatch names the address the site saw", () => {
  const words = refusalWords(
    refused(
      "address_mismatch",
      "The address is not the one this request came from: the site saw 198.51.100.7.",
    ),
    status(null),
  )
  expect(words).toContain("198.51.100.7")
  expect(words).toContain("not 203.0.113.10")
  expect(addressIn("seen from 198.51.100.7.")).toBe("198.51.100.7")
  expect(addressIn("no address here")).toBeUndefined()
})
