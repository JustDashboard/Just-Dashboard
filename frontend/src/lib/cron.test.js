import { describe, expect, test } from "bun:test"
import { describeCron, isValidCron, nextCronRun, parseCron, presetFor } from "./cron"

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
