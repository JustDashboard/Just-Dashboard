import { describe, expect, test } from "bun:test"
import {
  activeCheck,
  checkCounts,
  enrollmentRequest,
  newEnrollmentDraft,
  stageReading,
} from "./network-external"
const check = (status) => ({
  id: "check",
  vantageId: "vantage",
  request: { vantageId: "vantage", scopeId: "service", family: "inet", port: 443, tls: true },
  status,
  createdAt: "2026-01-01",
  expiresAt: "2026-01-01",
  startedBy: "operator",
})
describe("controlled external evidence", () => {
  test("enrollment uses exact targets and ports, with no ranges or commands", () => {
    const draft = {
      ...newEnrollmentDraft(),
      name: "Region",
      target: "service.example",
      addresses: "192.0.2.8, 2001:db8::8",
      family: "both",
    }
    expect(enrollmentRequest(draft)?.scopes[0]).toEqual({
      id: "service",
      target: "service.example",
      addresses: ["192.0.2.8", "2001:db8::8"],
      ports: [443],
      families: ["inet", "inet6"],
    })
    for (const ports of ["1-1000", "443; command", "0", "65536"])
      expect(enrollmentRequest({ ...draft, ports })).toBeUndefined()
  })
  test("an expired, cancelled or failed upload is unknown without a result", () => {
    for (const state of ["expired", "cancelled", "failed"]) {
      expect(stageReading(check(state), "tcp").label).toBe("Unknown")
      expect(stageReading(check(state), "tls").good).toBe(false)
      expect(activeCheck(check(state))).toBe(false)
    }
  })
  test("inventory counts do not promote queue acceptance to connectivity", () => {
    const vantage = { enrolledAt: "2026-01-01" }
    expect(
      checkCounts(
        [vantage, { ...vantage, revokedAt: "2026-01-02" }],
        [check("queued"), check("expired")],
      ),
    ).toEqual({ enrolled: 1, pending: 1, retained: 0, unknown: 1 })
  })
})
