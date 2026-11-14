import { describe, expect, test } from "bun:test"
import { boxToDays, countChanged, daysToBox, validDays, validLimit, validWhole } from "./fields"

describe("days", () => {
  test("no rule is an empty box, and an empty box is the host's -1", () => {
    expect(daysToBox(-1)).toBe("")
    expect(daysToBox(undefined)).toBe("")
    expect(daysToBox(0)).toBe("0")
    expect(daysToBox(180)).toBe("180")
    expect(boxToDays("")).toBe(-1)
    expect(boxToDays("  ")).toBe(-1)
    expect(boxToDays("180")).toBe(180)
  })

  test("a box of days is empty or a whole number the host takes", () => {
    expect(validDays("")).toBe(true)
    expect(validDays("0")).toBe(true)
    expect(validDays("99999")).toBe(true)
    expect(validDays("100000")).toBe(false)
    expect(validDays("-1")).toBe(false)
    expect(validDays("1.5")).toBe(false)
    expect(validDays("0", 1)).toBe(false)
  })
})

describe("validWhole", () => {
  test("an empty box is not zero", () => {
    expect(validWhole("", 0, 4)).toBe(false)
    expect(validWhole("8", 6, 128)).toBe(true)
    expect(validWhole("5", 6, 128)).toBe(false)
    expect(validWhole("129", 6, 128)).toBe(false)
    expect(validWhole("1e2", 6, 128)).toBe(false)
  })
})

describe("validLimit", () => {
  test("empty is no limit; a number has a floor", () => {
    expect(validLimit("", 5)).toBe(true)
    expect(validLimit("200", 5)).toBe(true)
    expect(validLimit("4", 5)).toBe(false)
    expect(validLimit("0", 5)).toBe(true)
    expect(validLimit("1.5", 0)).toBe(false)
    expect(validLimit("1.5", 0, false)).toBe(true)
    expect(validLimit("-1", 0, false)).toBe(false)
  })
})

describe("countChanged", () => {
  test("counts the boxes that differ", () => {
    expect(
      countChanged([
        ["a", "a"],
        ["7", ""],
        [true, false],
      ]),
    ).toBe(2)
  })
})
