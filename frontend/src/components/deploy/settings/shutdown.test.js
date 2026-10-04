import { describe, expect, test } from "bun:test"
import { shutdownSeconds, shutdownSummary } from "./shutdown"

describe("shutdownSeconds", () => {
  test("blank, zero and text leave the field out, which the server reads as its default", () => {
    expect(shutdownSeconds("")).toBeUndefined()
    expect(shutdownSeconds("0")).toBeUndefined()
    expect(shutdownSeconds("abc")).toBeUndefined()
    expect(shutdownSeconds("-5")).toBeUndefined()
  })

  test("keeps a whole number inside the planner's 0 to 300 range", () => {
    expect(shutdownSeconds("30")).toBe(30)
    expect(shutdownSeconds("12.9")).toBe(12)
    expect(shutdownSeconds("301")).toBe(300)
    expect(shutdownSeconds("99999")).toBe(300)
  })
})

describe("shutdownSummary", () => {
  test("a plan that sets nothing says it runs the defaults and what they are", () => {
    expect(shutdownSummary({})).toBe("Defaults · SIGTERM · 10 s grace")
  })

  test("names the signal, the grace and the drain that are set", () => {
    expect(shutdownSummary({ stopSignal: "SIGINT", gracePeriodSeconds: 30, drainSeconds: 5 })).toBe(
      "SIGINT · 30 s grace · 5 s drain",
    )
  })

  test("fills what is unset with the default beside what is set, and omits an absent drain", () => {
    expect(shutdownSummary({ drainSeconds: 5 })).toBe("SIGTERM · 10 s grace · 5 s drain")
    expect(shutdownSummary({ stopSignal: "SIGQUIT" })).toBe("SIGQUIT · 10 s grace")
  })
})
