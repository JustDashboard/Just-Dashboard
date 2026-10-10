import { describe, expect, test } from "bun:test"
import { accessVerdict, layerVerdict, validSource } from "./access-explain"

describe("access explanations", () => {
  test("each verdict is worded and toned, unknown never as a pass", () => {
    expect(accessVerdict({ verdict: "credentials" })).toEqual({
      label: "Asked to sign in",
      tone: "warning",
    })
    expect(accessVerdict({ verdict: "unknown" }).tone).toBe("unknown")
    expect(layerVerdict({ verdict: "refuses" })).toEqual({ label: "refuses", tone: "danger" })
    expect(layerVerdict({ verdict: "skipped" }).label).toBe("not applied")
  })

  test("only one address is a source", () => {
    for (const ok of ["192.0.2.7", "10.0.0.1", "2001:db8::1", "::1"])
      expect(validSource(ok)).toBe(true)
    for (const bad of ["", "10.0.0.0/8", "256.1.1.1", "office", "fe80::1%eth0", "1.2.3"])
      expect(validSource(bad)).toBe(false)
  })
})
