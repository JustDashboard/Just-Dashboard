import { describe, expect, test } from "bun:test"
import {
  describeCron,
  isValidCron,
  nextCronRun,
  nextCronRunsIn,
  parseCron,
  presetFor,
} from "./cron"

// The sentences the Scheduled page prints beside an expression have to say
// what cron will do, not what the builder meant: a wrong description is worse
// than none, because it is the one the reader trusts.
describe("describing a schedule", () => {
  test("names the common shapes", () => {
    expect(describeCron("* * * * *")).toBe("Every minute")
    expect(describeCron("*/5 * * * *")).toBe("Every 5 minutes")
    expect(describeCron("0 * * * *")).toBe("Every hour at :00")
    expect(describeCron("30 */6 * * *")).toBe("Every 6 hours at :30")
    expect(describeCron("0 3 * * *")).toBe("Every day at 03:00")
    expect(describeCron("15 7 * * 1")).toBe("At 07:15 on Monday")
    expect(describeCron("0 9 * * 1-5")).toBe("At 09:00 on weekdays")
    expect(describeCron("0 0 1 * *")).toBe("At 00:00 on the 1st of every month")
    expect(describeCron("0 0 1 1 *")).toBe("At 00:00 on the 1st in January")
    expect(describeCron("0 8,20 * * *")).toBe("Every day at 08:00 and 20:00")
    expect(describeCron("@daily")).toBe("Every day at 00:00")
    expect(describeCron("@reboot")).toBe("Once, when the server boots")
  })
  test("keeps cron's or-rule when both day fields are set", () => {
    expect(describeCron("0 0 15 * fri")).toBe("At 00:00 on the 15th of the month or on Friday")
  })
  test("refuses what cron would refuse", () => {
    expect(describeCron("0 3 * *")).toBe("Not a schedule cron understands")
    expect(isValidCron("61 * * * *")).toBe(false)
    expect(isValidCron("*/0 * * * *")).toBe(false)
    expect(isValidCron("0 3 * * mon")).toBe(true)
    expect(isValidCron("@reboot")).toBe(true)
    expect(isValidCron("@bogus")).toBe(false)
  })
})

describe("parsing fields", () => {
  test("reads names, ranges, lists and steps", () => {
    const cron = parseCron("0,30 9-17/4 1 jan,dec sun-mon")
    expect([...cron.minute]).toEqual([0, 30])
    expect([...cron.hour]).toEqual([9, 13, 17])
    expect([...cron.month]).toEqual([1, 12])
    expect([...cron.dayOfWeek]).toEqual([0, 1])
    // 7 is Sunday too.
    expect([...parseCron("* * * * 7").dayOfWeek]).toEqual([0])
  })
})

describe("the next run", () => {
  const from = new Date(2026, 8, 17, 14, 30, 45) // Thursday 17 September 2026
  test("finds the next matching minute, day and month", () => {
    expect(nextCronRun("*/15 * * * *", from)).toEqual(new Date(2026, 8, 17, 14, 45))
    expect(nextCronRun("0 3 * * *", from)).toEqual(new Date(2026, 8, 18, 3, 0))
    expect(nextCronRun("0 9 * * 1", from)).toEqual(new Date(2026, 8, 21, 9, 0))
    expect(nextCronRun("0 0 1 * *", from)).toEqual(new Date(2026, 9, 1, 0, 0))
    expect(nextCronRun("0 0 1 1 *", from)).toEqual(new Date(2027, 0, 1, 0, 0))
  })
  test("starts from the next whole minute, never the current one", () => {
    expect(nextCronRun("30 14 * * *", from)).toEqual(new Date(2026, 8, 18, 14, 30))
  })
  test("returns nothing for a schedule that cannot fire", () => {
    expect(nextCronRun("0 0 30 2 *", from)).toBeNull()
    expect(nextCronRun("@reboot", from)).toBeNull()
  })
})

describe("recognising a preset", () => {
  test("maps an expression back to the builder's choice", () => {
    expect(presetFor("* * * * *")).toBe("minute")
    expect(presetFor("*/5 * * * *")).toBe("5min")
    expect(presetFor("0 * * * *")).toBe("hourly")
    expect(presetFor("0 3 * * *")).toBe("daily")
    expect(presetFor("0 3 * * 1")).toBe("weekly")
    expect(presetFor("0 3 1 * *")).toBe("monthly")
    expect(presetFor("@reboot")).toBe("reboot")
    expect(presetFor("0 3 * * 1-5")).toBe("custom")
  })
})

// A deployment's schedule names its zone, and the server walks that zone's
// wall clock. The preview has to land on the same instants, or the timeline
// promises a run at a time the server never fires.
describe("the next runs in a named zone", () => {
  const utc = (iso) => new Date(iso)

  test("reads the fields as UTC's wall clock", () => {
    const from = utc("2026-09-17T14:30:45Z")
    expect(nextCronRunsIn("0 3 * * *", "UTC", 3, from)).toEqual([
      utc("2026-09-18T03:00:00Z"),
      utc("2026-09-19T03:00:00Z"),
      utc("2026-09-20T03:00:00Z"),
    ])
    expect(nextCronRunsIn("*/15 * * * *", "UTC", 2, from)).toEqual([
      utc("2026-09-17T14:45:00Z"),
      utc("2026-09-17T15:00:00Z"),
    ])
    // Cron's or-rule: the 15th, or any Friday.
    expect(nextCronRunsIn("0 0 15 * fri", "UTC", 2, from)).toEqual([
      utc("2026-09-18T00:00:00Z"),
      utc("2026-09-25T00:00:00Z"),
    ])
  })

  test("reads them in the zone, not in UTC", () => {
    // 09:00 in Bucharest is 06:00 UTC in summer and 07:00 once the clocks go
    // back on the last Sunday of October.
    expect(nextCronRunsIn("0 9 * * *", "Europe/Bucharest", 3, utc("2026-10-23T12:00:00Z"))).toEqual(
      [utc("2026-10-24T06:00:00Z"), utc("2026-10-25T07:00:00Z"), utc("2026-10-26T07:00:00Z")],
    )
  })

  test("a time the spring change skips does not fire that day", () => {
    // New York goes from 02:00 to 03:00 on 8 March 2026.
    expect(
      nextCronRunsIn("30 2 * * *", "America/New_York", 2, utc("2026-03-07T12:00:00Z")),
    ).toEqual([utc("2026-03-09T06:30:00Z"), utc("2026-03-10T06:30:00Z")])
  })

  test("a time the autumn change repeats fires on both sides of it", () => {
    // And back from 02:00 to 01:00 on 1 November 2026.
    expect(
      nextCronRunsIn("30 1 * * *", "America/New_York", 3, utc("2026-10-31T12:00:00Z")),
    ).toEqual([
      utc("2026-11-01T05:30:00Z"),
      utc("2026-11-01T06:30:00Z"),
      utc("2026-11-02T06:30:00Z"),
    ])
    // In time order, not wall order: 01:45 before the change is ahead of
    // 01:00 after it.
    expect(
      nextCronRunsIn("*/15 1 * * *", "America/New_York", 6, utc("2026-11-01T05:20:00Z")),
    ).toEqual([
      utc("2026-11-01T05:30:00Z"),
      utc("2026-11-01T05:45:00Z"),
      utc("2026-11-01T06:00:00Z"),
      utc("2026-11-01T06:15:00Z"),
      utc("2026-11-01T06:30:00Z"),
      utc("2026-11-01T06:45:00Z"),
    ])
  })

  test("understands the nicknames", () => {
    // 23:30 in Tokyo: midnight is half an hour away.
    const from = utc("2026-09-17T14:30:45Z")
    expect(nextCronRunsIn("@daily", "Asia/Tokyo", 1, from)).toEqual([utc("2026-09-17T15:00:00Z")])
    expect(nextCronRunsIn("@hourly", "UTC", 2, from)).toEqual([
      utc("2026-09-17T15:00:00Z"),
      utc("2026-09-17T16:00:00Z"),
    ])
    expect(nextCronRunsIn("@weekly", "UTC", 1, from)).toEqual([utc("2026-09-20T00:00:00Z")])
  })

  test("returns nothing it cannot honestly say", () => {
    const from = utc("2026-09-17T14:30:45Z")
    expect(nextCronRunsIn("@reboot", "UTC", 3, from)).toEqual([])
    expect(nextCronRunsIn("0 0 30 2 *", "UTC", 3, from)).toEqual([])
    expect(nextCronRunsIn("0 3 * * *", "Not/AZone", 3, from)).toEqual([])
  })

  test("agrees with the browser's own walk in the browser's zone", () => {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const from = new Date(2026, 8, 17, 14, 30, 45)
    for (const expression of ["*/15 * * * *", "0 3 * * *", "0 9 * * 1", "0 0 1 * *"]) {
      expect(nextCronRunsIn(expression, zone, 1, from)).toEqual([nextCronRun(expression, from)])
    }
  })
})
