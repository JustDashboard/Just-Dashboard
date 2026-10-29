import { describe, expect, test } from "bun:test"
import {
  availabilityPercent,
  availabilityReading,
  availabilityTone,
  coverageNote,
  downtimeLabel,
} from "./availability-model"

const DAY = 86400

function window(overrides) {
  return {
    window: "7d",
    seconds: 7 * DAY,
    observedSeconds: 7 * DAY,
    downSeconds: 0,
    incidents: 0,
    ratio: 1,
    coverageSeconds: 7 * DAY,
    ...overrides,
  }
}

describe("the percentage", () => {
  test("two decimals, and 100% only for no downtime at all", () => {
    expect(availabilityPercent(1)).toBe("100%")
    expect(availabilityPercent(0.9993)).toBe("99.93%")
    expect(availabilityPercent(0.95)).toBe("95.00%")
    // 99.996% rounds to 100.00 and must not claim it.
    expect(availabilityPercent(0.99996)).toBe("99.99%")
    expect(availabilityPercent(0)).toBe("0.00%")
  })
})

describe("downtime", () => {
  test("reads in the two largest units that say it", () => {
    expect(downtimeLabel(0)).toBe("none")
    expect(downtimeLabel(42)).toBe("< 1 min")
    expect(downtimeLabel(372)).toBe("6 min")
    expect(downtimeLabel(3600)).toBe("1 h")
    expect(downtimeLabel(4320)).toBe("1 h 12 min")
    expect(downtimeLabel(2 * DAY + 3 * 3600 + 600)).toBe("2 d 3 h")
    expect(downtimeLabel(3 * DAY)).toBe("3 d")
  })
})

describe("the tone", () => {
  test("is quiet while steady, warning under three nines, danger under two or while down now", () => {
    expect(availabilityTone(1, false)).toBe("default")
    expect(availabilityTone(0.9995, false)).toBe("default")
    expect(availabilityTone(0.995, false)).toBe("warning")
    expect(availabilityTone(0.98, false)).toBe("danger")
    expect(availabilityTone(1, true)).toBe("danger")
    expect(availabilityTone(undefined, false)).toBe("default")
  })
})

describe("a short record", () => {
  test("is said in the window's units, and a full one is not said at all", () => {
    expect(coverageNote(window())).toBeUndefined()
    expect(
      coverageNote(window({ window: "30d", seconds: 30 * DAY, coverageSeconds: 9 * DAY })),
    ).toBe("record covers 9 of 30 days")
    // Less than a day of record still counts as one day, never "0 of 30".
    expect(coverageNote(window({ window: "30d", seconds: 30 * DAY, coverageSeconds: 3600 }))).toBe(
      "record covers 1 of 30 days",
    )
    expect(coverageNote(window({ window: "24h", seconds: DAY, coverageSeconds: 3 * 3600 }))).toBe(
      "record covers 3 of 24 hours",
    )
    expect(coverageNote(window({ coverageSeconds: 0 }))).toBeUndefined()
  })
})

describe("a tile's reading", () => {
  test("names the downtime, its incidents and the record's reach", () => {
    expect(availabilityReading(window(), false)).toEqual({
      value: "100%",
      hint: "no downtime recorded",
      tone: "default",
    })
    expect(
      availabilityReading(
        window({
          window: "30d",
          seconds: 30 * DAY,
          observedSeconds: 9 * DAY,
          coverageSeconds: 9 * DAY,
          downSeconds: 372,
          incidents: 2,
          ratio: 1 - 372 / (9 * DAY),
        }),
        false,
      ),
    ).toEqual({
      value: "99.95%",
      hint: "6 min down · 2 incidents · record covers 9 of 30 days",
      tone: "default",
    })
  })

  test("says down now on the day's tile alone, in the bad tone", () => {
    const day = window({ window: "24h", seconds: DAY, observedSeconds: DAY, coverageSeconds: DAY })
    expect(availabilityReading(day, true).hint).toBe("no downtime recorded · down now")
    expect(availabilityReading(day, true).tone).toBe("danger")
    expect(availabilityReading(window(), true).hint).toBe("no downtime recorded")
  })

  test("has no figure without anything observed, and says which nothing it is", () => {
    expect(availabilityReading(window({ ratio: undefined, observedSeconds: 0 }), false)).toEqual({
      value: "—",
      hint: "no release was live",
      tone: "default",
    })
    expect(
      availabilityReading(
        window({ ratio: undefined, observedSeconds: 0, coverageSeconds: 0 }),
        false,
      ),
    ).toEqual({ value: "—", hint: "nothing recorded yet", tone: "default" })
  })
})
