import { describe, expect, test } from "bun:test"
import {
  certInState,
  certUnused,
  certificateUsers,
  cleanupTicked,
  countCertificates,
  unusedDeletable,
} from "./certificates"

const cert = (overrides) => ({
  name: "app.example.com",
  path: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
  domains: ["app.example.com"],
  issuer: "R11",
  daysLeft: 60,
  expired: false,
  expiring: false,
  selfSigned: false,
  source: "certbot",
  usedBy: ["app.example.com"],
  ...overrides,
})
const unused = (overrides) => cert({ usedBy: [], ...overrides })

describe("the header's counts", () => {
  test("an expired certificate nothing serves is unused, not refused", () => {
    const counts = countCertificates([
      cert(),
      unused({ name: "old", expired: true, daysLeft: -4, source: "caddy" }),
      unused({ name: "trial", staging: true }),
    ])
    expect(counts.refused).toBe(0)
    expect(counts.refusedWhy).toBe("")
    expect(counts.unused).toBe(2)
    expect(counts.unusedRefused).toBe(2)
    expect(counts.all).toBe(3)
  })

  test("an expired or test certificate a site serves is refused", () => {
    const counts = countCertificates([
      cert({ expired: true, daysLeft: -1 }),
      cert({ name: "test", staging: true }),
      cert({ name: "broken", error: "no PEM", usedBy: [] }),
    ])
    expect(counts.refused).toBe(3)
    expect(counts.refusedWhy).toBe("1 expired · 1 test certificate · 1 unreadable")
    // An unreadable one is not known to be unused.
    expect(counts.unused).toBe(0)
  })

  test("only a served certificate is counted as expiring", () => {
    const counts = countCertificates([
      cert({ expiring: true, daysLeft: 12 }),
      cert({ name: "stream", expiring: true, daysLeft: 9, usedBy: [], usedByStreams: ["imap"] }),
      unused({ name: "left", expiring: true, daysLeft: 3, source: "caddy" }),
    ])
    expect(counts.expiring).toBe(2)
    expect(counts.soonest).toBe(9)
    expect(counts.unused).toBe(1)
    expect(counts.unusedRefused).toBe(0)
  })
})

describe("who serves a certificate", () => {
  test("a deployment's route reads as its project, a leftover as one", () => {
    const users = certificateUsers(
      cert({
        usedBy: ["just-dashboard-env-12.conf", "just-dashboard-env-40.conf", "blog"],
        usedByStreams: ["imap"],
        usedByLabels: [
          { site: "just-dashboard-env-12.conf", label: "shop · production", href: "/deploy/3" },
          {
            site: "just-dashboard-env-40.conf",
            label: "leftover route",
            href: "/proxy/sites/just-dashboard-env-40.conf",
            leftover: true,
          },
        ],
      }),
    )
    expect(users.map((user) => [user.label, user.href])).toEqual([
      ["shop · production", "/deploy/3"],
      ["leftover route", "/proxy/sites/just-dashboard-env-40.conf"],
      ["blog", "/proxy/sites/blog"],
      ["stream imap", "/proxy/streams/imap"],
    ])
    expect(users[1].leftover).toBe(true)
  })

  test("an older backend's answer keeps each site's name", () => {
    expect(certificateUsers(cert({ usedBy: ["a b"] }))).toEqual([
      { key: "site:a b", label: "a b", href: "/proxy/sites/a%20b", leftover: undefined },
    ])
  })
})

describe("what the cleanup offers", () => {
  test("every source this server can delete, used by nothing", () => {
    expect(unusedDeletable(unused({ source: "caddy" }))).toBe(true)
    expect(unusedDeletable(unused({ source: "imported" }))).toBe(true)
    expect(unusedDeletable(unused({ source: "certbot" }))).toBe(true)
    expect(unusedDeletable(cert({ source: "caddy" }))).toBe(false)
    expect(unusedDeletable(unused({ usedByStreams: ["imap"] }))).toBe(false)
    expect(unusedDeletable(unused({ error: "no PEM" }))).toBe(false)
    // A file a site names elsewhere is never "unused", and never this server's.
    expect(unusedDeletable(unused({ source: "blog.conf" }))).toBe(false)
  })

  test("a valid lineage or import is listed and left unticked", () => {
    expect(cleanupTicked({ source: "caddy", expired: false })).toBe(true)
    expect(cleanupTicked({ source: "imported", expired: true })).toBe(true)
    expect(cleanupTicked({ source: "imported", expired: false })).toBe(false)
    expect(cleanupTicked({ source: "certbot", expired: false })).toBe(false)
  })
})

test("the list narrows to unused and to expired", () => {
  const old = unused({ expired: true })
  expect(certUnused(old)).toBe(true)
  expect(certInState(old, "unused")).toBe(true)
  expect(certInState(old, "expired")).toBe(true)
  expect(certInState(cert(), "unused")).toBe(false)
  expect(certInState(cert({ expired: true }), "expired")).toBe(true)
  expect(certInState(cert({ error: "no PEM", expired: false }), "expired")).toBe(false)
  expect(certInState(cert(), "all")).toBe(true)
})
