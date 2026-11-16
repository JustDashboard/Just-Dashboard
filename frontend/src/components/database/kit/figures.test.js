import { describe, expect, test } from "bun:test"
import { capitalize, millis, shareWords } from "./figures"
import * as performance from "../ops/performance-figures"

describe("millis", () => {
  test("says a statement that ran under a hundredth of a millisecond as that, never as nought", () => {
    expect(millis(0.004)).toBe("<0.01 ms")
    expect(millis(0)).toBe("0.00 ms")
  })

  test("keeps two decimals under one, one under ten, none up to a second", () => {
    expect(millis(0.25)).toBe("0.25 ms")
    expect(millis(3.14)).toBe("3.1 ms")
    expect(millis(42.6)).toBe("43 ms")
  })

  test("says seconds from one, and a duration from a minute", () => {
    expect(millis(1500)).toBe("1.5 s")
    expect(millis(12_400)).toBe("12 s")
    expect(millis(75_000)).not.toBe("75 s")
    expect(millis(75_000)).toMatch(/1m/)
  })

  test("is the one millis: Performance's figures print through it", () => {
    for (const value of [0, 0.004, 0.25, 3.14, 42.6, 1500, 12_400, 75_000]) {
      expect(performance.millis(value)).toBe(millis(value))
    }
  })
})

describe("shareWords", () => {
  test("never says 0% for something that ran", () => {
    expect(shareWords(0)).toBe("0%")
    expect(shareWords(0.0004)).toBe("<0.1%")
    expect(shareWords(0.042)).toBe("4.2%")
    expect(shareWords(0.42)).toBe("42%")
  })
})

describe("capitalize", () => {
  test("raises the first letter and leaves the rest", () => {
    expect(capitalize("tables")).toBe("Tables")
    expect(capitalize("pgBouncer")).toBe("PgBouncer")
    expect(capitalize("")).toBe("")
  })
})
