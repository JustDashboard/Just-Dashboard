import { describe, expect, test } from "bun:test"
import {
  basisText,
  confidenceText,
  daysLabel,
  growthText,
  mountTone,
  mountVerdict,
} from "./capacity"

const mount = (overrides) => ({
  mountpoint: "/",
  usedBytes: 48 * 2 ** 30,
  totalBytes: 100 * 2 ** 30,
  usedPercent: 48,
  status: "filling",
  growthBytesPerDay: 2 * 2 ** 30,
  daysToFull: 26,
  fullAt: "2026-11-24T00:00:00Z",
  r2: 0.99,
  confidence: "high",
  from: "2026-10-25T00:30:00Z",
  to: "2026-10-29T00:00:00Z",
  points: 96,
  ...overrides,
})

describe("capacity forecast words", () => {
  test("days to full reads in the unit that matters", () => {
    expect(daysLabel(0.4)).toBe("within a day")
    expect(daysLabel(1)).toBe("in 1 day")
    expect(daysLabel(6.6)).toBe("in 7 days")
    expect(daysLabel(26)).toBe("in 4 weeks")
    expect(daysLabel(120)).toBe("in 4 months")
    expect(daysLabel(900)).toBe("in more than a year")
  })

  test("a filesystem says when it fills, or that it does not", () => {
    expect(mountVerdict(mount())).toBe("Full in 4 weeks")
    expect(mountVerdict(mount({ status: "steady", daysToFull: null }))).toBe("Not filling")
    expect(mountVerdict(mount({ status: "shrinking", daysToFull: null }))).toBe("Shrinking")
    expect(mountVerdict(mount({ status: "insufficient_history", daysToFull: null }))).toBe(
      "Gathering history",
    )
  })

  test("under a week is red and under a month amber", () => {
    expect(mountTone(mount({ daysToFull: 3 }))).toBe("danger")
    expect(mountTone(mount({ daysToFull: 26 }))).toBe("warning")
    expect(mountTone(mount({ daysToFull: 90 }))).toBe("default")
    expect(mountTone(mount({ daysToFull: null }))).toBe("default")
  })

  test("growth, confidence and basis in words", () => {
    expect(growthText(2 * 2 ** 30)).toBe("+2.0 GB a day")
    expect(growthText(-512 * 2 ** 20)).toBe("−512.0 MB a day")
    expect(growthText(1000)).toBe("steady")
    expect(growthText(null)).toBe("—")
    expect(confidenceText("high", 0.987)).toBe("high confidence (R² 0.99)")
    expect(confidenceText(undefined, null)).toBe("")
    expect(basisText(96, "2026-10-25T00:30:00Z", "2026-10-29T00:00:00Z")).toBe(
      "fitted to 96 hourly means over 3 days",
    )
    expect(basisText(6, "2026-10-29T00:00:00Z", "2026-10-29T05:00:00Z")).toBe(
      "fitted to 6 hourly means over 5 hours",
    )
    expect(basisText(0, null, null)).toBe("no samples yet")
  })
})
