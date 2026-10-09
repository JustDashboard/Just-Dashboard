import { expect, test } from "bun:test"
import { ENFORCEMENT, enforcementOf, goDuration, pullAge } from "./enforcement"

const view = (overrides) => ({
  installed: true,
  active: true,
  decisions: [],
  alerts: [],
  bouncers: [],
  ...overrides,
})

test("protection is claimed only by the enforcing and proxy-only verdicts", () => {
  const protecting = Object.entries(ENFORCEMENT)
    .filter(([, words]) => words.protects)
    .map(([state]) => state)
  expect(protecting.sort()).toEqual(["enforcing", "partial"])
  expect(ENFORCEMENT.partial.label).toBe("HTTP only")
})

test("a view without a verdict is unverified, and a stopped engine is stopped", () => {
  expect(enforcementOf(view({}))).toBe("unverified")
  expect(enforcementOf(view({ active: false, enforcement: { state: "enforcing" } }))).toBe(
    "stopped",
  )
  expect(enforcementOf(view({ enforcement: { state: "stale" } }))).toBe("stale")
})

test("pull ages and the freshness window read as a person says them", () => {
  expect(pullAge(-1)).toBe("never")
  expect(pullAge(8)).toBe("8 s")
  expect(pullAge(240)).toBe("4 min")
  expect(pullAge(3 * 3600 + 5)).toBe("3 h")
  expect(pullAge(5 * 86400)).toBe("5 days")
  expect(goDuration("3m0s")).toBe("3 min")
  expect(goDuration("1h30m0s")).toBe("1 h")
  expect(goDuration("soon")).toBe("soon")
})
