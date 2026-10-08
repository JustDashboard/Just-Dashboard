import { describe, expect, test } from "bun:test"
import { dnsEvidenceLabel } from "./network-dns-evidence"

describe("DNS evidence provenance", () => {
  test("encryption and validation labels preserve native reporting", () => {
    expect(dnsEvidenceLabel({ state: "encrypted" })).toBe("Native reports encryption")
    expect(dnsEvidenceLabel({ state: "validated" })).toBe("Native reports DNSSEC validation")
    expect(dnsEvidenceLabel({ state: "native_policy_validated" })).toBe("Native strict TLS policy")
  })
  test("unknown and configuration are separate from measured validation", () => {
    expect(dnsEvidenceLabel({ state: "unknown" })).toBe("Unknown")
    expect(dnsEvidenceLabel({ state: "modeled" })).toBe("Policy candidates")
    expect(dnsEvidenceLabel({ state: "not_authenticated" })).toBe(
      "Native did not authenticate data",
    )
  })
})
