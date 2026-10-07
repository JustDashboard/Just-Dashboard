import { expect, test } from "bun:test"
import { formatKbit, kbitToMbit, limitProblem, mbitToKbit } from "./shaping-math"

test("limits read as people say them", () => {
  expect(formatKbit(0)).toBe("No limit")
  expect(formatKbit(500)).toBe("500 kbit/s")
  expect(formatKbit(50_000)).toBe("50 Mbit/s")
  expect(formatKbit(2500)).toBe("2.5 Mbit/s")
  expect(formatKbit(1_500_000)).toBe("1.5 Gbit/s")
})

test("megabits typed become the kbit the route takes", () => {
  expect(mbitToKbit("50")).toBe(50_000)
  expect(mbitToKbit("2,5")).toBe(2500)
  expect(mbitToKbit("")).toBe(0)
  expect(mbitToKbit("0")).toBe(0)
  expect(Number.isNaN(mbitToKbit("fast"))).toBe(true)
  expect(kbitToMbit(50_000)).toBe("50")
  expect(kbitToMbit(0)).toBe("")
})

test("the uplink and the client path are held to a megabit, other devices are not", () => {
  expect(limitProblem("0.5", { uplink: true, clientPath: false })).toBeDefined()
  expect(limitProblem("0.5", { uplink: false, clientPath: true })).toBeDefined()
  expect(limitProblem("0.5", { uplink: false, clientPath: false })).toBeUndefined()
  expect(limitProblem("", { uplink: true, clientPath: false })).toBeUndefined()
  expect(limitProblem("1", { uplink: true, clientPath: false })).toBeUndefined()
  expect(limitProblem("x", { uplink: false, clientPath: false })).toBeDefined()
})
