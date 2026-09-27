import { describe, expect, test } from "bun:test"
import { certificateFindings } from "./certificates"

const expired = (hoursAgo) => ({
  name: "gone.example.com",
  path: "/etc/ssl/just-dashboard/gone/fullchain.pem",
  domains: ["gone.example.com"],
  issuer: "R11",
  notBefore: new Date(Date.now() - 90 * 86_400_000).toISOString(),
  notAfter: new Date(Date.now() - hoursAgo * 3_600_000).toISOString(),
  // What the backend sends for a certificate that ran out this morning:
  // days left truncate toward zero.
  daysLeft: 0,
  expired: true,
  expiring: false,
  selfSigned: false,
  source: "imported",
  usedBy: [],
})

describe("an expired certificate's finding", () => {
  test("hours after expiry it says today, not 0 days ago", () => {
    const [finding] = certificateFindings({ certs: [expired(3)] })
    expect(finding.title).toBe("gone.example.com has expired")
    expect(finding.detail).toBe("Expired today; every browser refuses it now.")
  })

  test("days after, it counts them", () => {
    const [finding] = certificateFindings({ certs: [{ ...expired(5 * 24 + 2), daysLeft: -5 }] })
    expect(finding.detail).toBe("Expired 5 days ago; every browser refuses it now.")
  })
})
