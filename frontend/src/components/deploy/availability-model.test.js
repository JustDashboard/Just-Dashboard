import { describe, expect, test } from "bun:test"
import {
  availabilityLead,
  dayTone,
  dayWords,
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

describe("availabilityLead", () => {
  const wins = (
    seven,
    thirty,
    day = window({ window: "24h", seconds: DAY, observedSeconds: DAY, coverageSeconds: DAY }),
  ) => ({
    down: false,
    windows: [day, seven, thirty],
  })
  const short = {
    downSeconds: 1200,
    incidents: 3,
    ratio: 0.9941,
    coverageSeconds: 2 * DAY,
    observedSeconds: 2 * DAY,
  }

  test("names how far the record reaches, not the window it was asked over", () => {
    const lead = availabilityLead(
      wins(window(short), window({ ...short, window: "30d", seconds: 30 * DAY })),
    )
    expect(lead.title).toBe("Up 99.41% of the last 2 days")
    expect(lead.hint).toBe("20 min down across 3 incidents, none in the last 24 hours")
    expect(lead.tone).toBe("warning")
  })

  test("a steady record says there was none", () => {
    expect(
      availabilityLead(
        wins(
          window({}),
          window({
            window: "30d",
            seconds: 30 * DAY,
            coverageSeconds: 30 * DAY,
            observedSeconds: 30 * DAY,
          }),
        ),
      ),
    ).toEqual({ title: "No downtime in the last 30 days", tone: "running" })
  })

  test("down now outranks every figure", () => {
    const lead = availabilityLead({ ...wins(window(short), window(short)), down: true })
    expect(lead.title).toBe("The live release is down right now")
    expect(lead.tone).toBe("danger")
  })

  test("no figure yet says so rather than claiming a clean record", () => {
    const empty = window({ ratio: undefined, observedSeconds: 0 })
    expect(availabilityLead({ down: false, windows: [empty] }).title).toBe("Nothing recorded yet")
  })
})

describe("a day on the ribbon", () => {
  const day = (observedSeconds, downSeconds, incidents = 0) => ({
    from: "2026-10-30T00:00:00Z",
    observedSeconds,
    downSeconds,
    incidents,
  })

  test("unrecorded time is unknown, not up", () => {
    expect(dayTone(day(0, 0))).toBe("unknown")
    expect(dayWords(day(0, 0))).toBe("nothing recorded")
  })

  test("any downtime shows, and a bad day is the red one", () => {
    expect(dayTone(day(DAY, 0))).toBe("up")
    expect(dayTone(day(DAY, 60, 1))).toBe("dented")
    expect(dayTone(day(DAY, 1200, 3))).toBe("down")
    expect(dayWords(day(DAY, 1200, 3))).toBe("20 min down · 3 incidents")
    expect(dayWords(day(DAY, 60, 1))).toBe("1 min down · 1 incident")
  })
})
