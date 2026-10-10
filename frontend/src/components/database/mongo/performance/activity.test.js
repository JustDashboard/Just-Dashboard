import { describe, expect, test } from "bun:test"
import { activityOf, countersOf, watched } from "./activity"

const stats = (reads, writes, at = 1790921697400, readMicros = 1000) =>
  `{"ns":"app.users","localTime":{"$date":{"$numberLong":"${at}"}},"latencyStats":{` +
  `"reads":{"latency":{"$numberLong":"${readMicros}"},"ops":{"$numberLong":"${reads}"}},` +
  `"writes":{"latency":{"$numberLong":"500"},"ops":{"$numberLong":"${writes}"}},` +
  `"commands":{"latency":{"$numberLong":"9"},"ops":{"$numberLong":"3"}}}}`

describe("a collection's counters", () => {
  test("are read off the document $collStats answers with", () => {
    expect(countersOf(stats(622, 10))).toEqual({
      at: 1790921697400,
      reads: 622,
      writes: 10,
      commands: 3,
      readMicros: 1000,
      writeMicros: 500,
    })
  })

  test("a document without them, or none at all, is no reading", () => {
    expect(countersOf('{"ns":"app.users","storageStats":{}}')).toBeNull()
    expect(countersOf(undefined)).toBeNull()
    expect(countersOf("{ not json")).toBeNull()
  })
})

describe("activity between two readings", () => {
  test("one reading has a total and no rate", () => {
    expect(activityOf(undefined, countersOf(stats(600, 10)))).toEqual({
      total: 610,
      perSecond: null,
      readsPerSecond: null,
      writesPerSecond: null,
      readMicros: null,
    })
  })

  test("a rate is the difference over the server's own clock", () => {
    const before = countersOf(stats(600, 10, 1_000_000, 1000))
    const now = countersOf(stats(650, 20, 1_010_000, 6000))
    expect(activityOf(before, now)).toEqual({
      total: 670,
      perSecond: 6,
      readsPerSecond: 5,
      writesPerSecond: 1,
      // 5,000 µs over the 50 reads in between.
      readMicros: 100,
    })
  })

  test("the watcher's own read is not counted as the collection's activity", () => {
    const before = countersOf(stats(600, 10, 1_000_000))
    const now = countersOf(stats(601, 10, 1_010_000))
    expect(activityOf(before, now, 1)).toMatchObject({ perSecond: 0, readMicros: null })
    expect(activityOf(before, countersOf(stats(611, 10, 1_010_000)), 1).readsPerSecond).toBe(1)
  })

  test("no read in between is no latency, not a zero", () => {
    const before = countersOf(stats(600, 10, 1_000_000))
    const now = countersOf(stats(600, 12, 1_010_000))
    expect(activityOf(before, now).readMicros).toBeNull()
  })

  test("counters that went down are a restart: the rate starts again", () => {
    const before = countersOf(stats(600, 10, 1_000_000))
    const now = countersOf(stats(5, 0, 1_010_000))
    expect(activityOf(before, now)).toMatchObject({ total: 5, perSecond: null })
  })
})

test("the collections watched are the reader's own, largest first, and no more than asked for", () => {
  const list = [
    { name: "small", type: "collection", system: false, size: 1 },
    { name: "view", type: "view", system: false, size: 0 },
    { name: "system.views", type: "collection", system: true, size: 900 },
    { name: "big", type: "collection", system: false, size: 50 },
    { name: "mid", type: "collection", system: false, size: 20 },
  ]
  expect(watched(list, 2).map((entry) => entry.name)).toEqual(["big", "mid"])
})
