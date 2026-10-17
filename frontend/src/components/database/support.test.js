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
    expect(supportWords(eol("2028-11-09", false))).toBe("Supported until Nov 2028")
    expect(supportWords(eol("2026-04-30", true))).toBe("End of life since Apr 30, 2026")
  })

  test("is the day on the calendar it was published on, whatever the reader's zone", () => {
    // Midnight UTC is the evening before in the Americas.
    expect(supportWords(eol("2026-07-01", false))).toBe("Supported until Jul 2026")
    expect(supportWords(eol("2025-07-31", true))).toBe("End of life since Jul 31, 2025")
  })

  test("reads as one fact in a line of them", () => {
    expect(supportFact(eol("2030-11-14", false))).toBe("supported until Nov 2030")
    expect(supportFact(eol("2025-11-13", true))).toBe("end of life since Nov 13, 2025")
  })
})
