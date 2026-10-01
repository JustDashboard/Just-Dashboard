import { describe, expect, test } from "bun:test"
import { parseScore, scoreText } from "./score"

describe("a score as typed", () => {
  test("numbers in the forms people write them", () => {
    expect(parseScore("42")).toBe(42)
    expect(parseScore(" -1.5 ")).toBe(-1.5)
    expect(parseScore(".5")).toBe(0.5)
    expect(parseScore("1e3")).toBe(1000)
    expect(parseScore("0")).toBe(0)
  })

  test("the two infinities Redis allows", () => {
    expect(parseScore("inf")).toBe("inf")
    expect(parseScore("+INF")).toBe("inf")
    expect(parseScore("-inf")).toBe("-inf")
    expect(parseScore("Infinity")).toBe("inf")
  })

  test("nothing, a word, NaN and an overflow are not scores", () => {
    for (const typed of ["", " ", "abc", "nan", "1,5", "1e999", "0x10", "1 2"]) {
      expect(parseScore(typed)).toBeNull()
    }
  })

  test("a score is written as it is", () => {
    expect(scoreText(1.5)).toBe("1.5")
    expect(scoreText("inf")).toBe("inf")
    expect(scoreText(undefined)).toBe("")
  })
})
