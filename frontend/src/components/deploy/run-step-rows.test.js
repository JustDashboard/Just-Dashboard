import { describe, expect, test } from "bun:test"
import { ranStep, skippedWithEffect } from "./run-step-rows"

const step = (state, evidence) => ({ id: 1, key: "retire_previous", state, attempt: 1, evidence })

describe("which steps are rows", () => {
  test("a step that ran or is running is a row, one not reached is not", () => {
    expect(ranStep(step("passed", {}))).toBe(true)
    expect(ranStep(step("running"))).toBe(true)
    expect(ranStep(step("failed", {}))).toBe(true)
    expect(ranStep(step("pending"))).toBe(false)
  })

  test("a bare skip is its reason, not a row", () => {
    expect(ranStep(step("skipped", { reason: "no backup gate configured" }))).toBe(false)
    expect(ranStep(step("skipped"))).toBe(false)
    // A route flag that did not happen is no record either.
    expect(ranStep(step("skipped", { reason: "x", routeRemoved: false }))).toBe(false)
  })

  test("a skip that removed a preview's route and address first is a row to read", () => {
    const cleanup = step("skipped", {
      reason: "preview had no live release",
      routeRemoved: true,
      addressWithdrawn: false,
    })
    expect(skippedWithEffect(cleanup)).toBe(true)
    expect(ranStep(cleanup)).toBe(true)
    expect(skippedWithEffect({ ...cleanup, state: "passed" })).toBe(false)
  })
})
