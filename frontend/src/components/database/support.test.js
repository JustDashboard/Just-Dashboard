import { describe, expect, test } from "bun:test"
import { supportFact, supportWords } from "./support"

const eol = (date, past) => ({
  product: "PostgreSQL",
  release: "16",
  date: `${date}T00:00:00Z`,
  past,
  daysLeft: past ? -10 : 900,
})

describe("a release's support status", () => {
  test("is the month while it is ahead and the day once it has gone by", () => {
    expect(supportWords(eol("2028-11-09", false))).toBe("Supported until 2028-11")
    expect(supportWords(eol("2026-04-30", true))).toBe("End of life since 2026-04-30")
  })

  test("reads as one fact in a line of them", () => {
    expect(supportFact(eol("2030-11-14", false))).toBe("supported until 2030-11")
    expect(supportFact(eol("2025-11-13", true))).toBe("end of life since 2025-11-13")
  })
})
