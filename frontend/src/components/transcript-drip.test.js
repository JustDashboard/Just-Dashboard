import { describe, expect, test } from "bun:test"
import { DRIP_LAG_MS, dripRemaining } from "./transcript-line"

describe("dripRemaining", () => {
  test("held lines are let out evenly and all of them by the lag bound", () => {
    expect(dripRemaining(64, 0)).toBe(64)
    expect(dripRemaining(64, DRIP_LAG_MS / 2)).toBe(32)
    expect(dripRemaining(64, DRIP_LAG_MS)).toBe(0)
    expect(dripRemaining(400, DRIP_LAG_MS + 50)).toBe(0)
  })

  test("a line is never let out early by rounding, and nothing held is nothing", () => {
    expect(dripRemaining(3, 1)).toBe(3)
    expect(dripRemaining(0, 100)).toBe(0)
  })

  test("the bound is about a third of a second", () => {
    expect(DRIP_LAG_MS).toBeLessThanOrEqual(300)
  })
})
