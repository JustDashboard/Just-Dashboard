import { describe, expect, test } from "bun:test"
import { dumpStrip, sessionsTrend, trendCeiling } from "./trend"

const AT = Date.parse("2026-10-11T12:00:00Z")
const snapshot = (index, stats, gap = false) => ({ at: AT + index * 30_000, stats, gap })

describe("sessionsTrend", () => {
  test("reads a SQL server's open sessions, oldest first", () => {
    const sql = (total) => ({
      at: "2026-10-11T12:00:00Z",
      supported: true,
      databaseBytes: 1,
      connections: { total, active: 1, idle: 0, idleInTransaction: 0, waiting: 0, max: 100 },
      counters: {},
      gauges: {},
    })
    expect(
      sessionsTrend("sql", [snapshot(0, sql(3)), snapshot(1, sql(5)), snapshot(2, sql(4))]),
    ).toEqual([3, 5, 4])
  })

  test("leaves out a reading that lists no session rather than draw nobody connected", () => {
    const silent = {
      at: "2026-10-11T12:00:00Z",
      supported: true,
      databaseBytes: 1,
      connections: { total: 0, active: 0, idle: 0, idleInTransaction: 0, waiting: 0, max: 0 },
      counters: {},
      gauges: {},
    }
    expect(sessionsTrend("sql", [snapshot(0, silent)])).toEqual([])
  })

  test("reads a key-value store's clients and a document store's connections", () => {
    const redis = (clients) => ({
      server: { sampledAtMs: AT, counters: { connected_clients: clients, keyspace_hits: 9 } },
    })
    expect(sessionsTrend("keyvalue", [snapshot(0, redis(2)), snapshot(1, redis(7))])).toEqual([
      2, 7,
    ])
    const mongo = (current) => ({ server: { timestamp: AT, connections: { current } } })
    expect(sessionsTrend("document", [snapshot(0, mongo(6)), snapshot(1, mongo(6))])).toEqual([
      6, 6,
    ])
  })

  test("an engine with no sessions, and a snapshot that is not one, draw nothing", () => {
    expect(sessionsTrend("search", [snapshot(0, { anything: 1 })])).toEqual([])
    expect(
      sessionsTrend("sql", [snapshot(0, null), { at: Number.NaN, stats: {}, gap: false }]),
    ).toEqual([])
  })
})

describe("trendCeiling", () => {
  test("leaves room over the highest reading, and never falls to nothing", () => {
    expect(trendCeiling([4, 8, 6])).toBe(10)
    expect(trendCeiling([0, 0])).toBe(1.25)
    expect(trendCeiling([])).toBe(1.25)
  })
})

describe("dumpStrip", () => {
  const recent = Array.from({ length: 9 }, (_, index) => ({
    takenAt: new Date(AT - index * 86_400_000).toISOString(),
    size: 1000 + index,
  }))

  test("draws the newest few, oldest first", () => {
    const strip = dumpStrip(recent, false, 7)
    expect(strip).toHaveLength(7)
    expect(strip[6].key).toBe(recent[0].takenAt)
    expect(strip[0].key).toBe(recent[6].takenAt)
    expect(strip.every((mark) => mark.tone === "success")).toBe(true)
  })

  test("a dump being taken is one more mark, in the same width", () => {
    const strip = dumpStrip(recent, true, 7)
    expect(strip).toHaveLength(7)
    expect(strip[6]).toMatchObject({ key: "running", tone: "running" })
    expect(strip[5].key).toBe(recent[0].takenAt)
  })

  test("a summary from before the strip, and a database never dumped, draw none", () => {
    expect(dumpStrip(undefined, false)).toEqual([])
    expect(dumpStrip([], false)).toEqual([])
    expect(dumpStrip([], true)).toHaveLength(1)
  })
})
