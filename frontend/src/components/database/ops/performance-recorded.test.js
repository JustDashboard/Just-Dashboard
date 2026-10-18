import { describe, expect, test } from "bun:test"
import {
  firstPointWords,
  historyEvents,
  historyMethod,
  historyRows,
  historySorts,
  isHistorySort,
  partlyRecorded,
  spanWords,
  trendValues,
  windowWords,
} from "./performance-recorded"

const facts = (over = {}) => ({
  supported: true,
  hours: 24,
  everySeconds: 300,
  retentionHours: 168,
  keptPerInterval: 50,
  bucketSeconds: 3600,
  from: "2026-10-16T09:00:00Z",
  until: "2026-10-17T09:00:00Z",
  intervals: 288,
  recordedSeconds: 86_400,
  totalMs: 1000,
  rowsRead: false,
  percentiles: false,
  resets: [],
  ...over,
})

describe("the orders a history is listed in", () => {
  test("are the engine's: p95 and rows read only where it keeps what they are made from", () => {
    expect(historySorts(facts()).map((sort) => sort.id)).toEqual(["total", "calls", "mean", "rows"])
    expect(historySorts(facts({ percentiles: true, rowsRead: true })).map((s) => s.id)).toEqual([
      "total",
      "calls",
      "mean",
      "p95",
      "rows",
      "rowsRead",
    ])
    expect(historySorts(undefined).map((sort) => sort.id)).not.toContain("p95")
    expect(isHistorySort("p95")).toBe(true)
    expect(isHistorySort("max")).toBe(false)
  })
})

describe("a row's trend", () => {
  test("starts where recording did, and draws a point not recorded as nothing run", () => {
    expect(trendValues([null, null, 4, 0, null, 2])).toEqual([4, 0, 0, 2])
    expect(trendValues([null, null])).toEqual([])
    expect(trendValues(undefined)).toEqual([])
  })
})

describe("the words of a window", () => {
  test("say its length and the length of a point", () => {
    expect(windowWords(1)).toBe("the last hour")
    expect(windowWords(6)).toBe("the last 6 hours")
    expect(windowWords(24)).toBe("the last 24 hours")
    expect(windowWords(168)).toBe("the last 7 days")
    expect(spanWords(300)).toBe("5 minutes")
    expect(spanWords(60)).toBe("minute")
    expect(spanWords(3600)).toBe("hour")
    expect(spanWords(25_200)).toBe("7 hours")
    expect(spanWords(604_800)).toBe("7 days")
    expect(spanWords(45)).toBe("45 seconds")
  })

  test("know a window the recorder holds only part of", () => {
    expect(partlyRecorded(facts())).toBe(false)
    expect(partlyRecorded(facts({ recordedSeconds: 3600, intervals: 12 }))).toBe(true)
    expect(partlyRecorded(facts({ recordedSeconds: 0, intervals: 0 }))).toBe(false)
  })
})

describe("what the figures were made from", () => {
  test("says on PostgreSQL that a mean is two totals divided and there are no percentiles", () => {
    const [method, kept] = historyMethod({ driver: "postgres", label: "PostgreSQL" }, facts())
    expect(method).toContain("pg_stat_statements keeps running totals")
    expect(method).toContain("Every 5 minutes")
    expect(method).toContain("that time divided by those calls")
    expect(method).toContain("no percentiles")
    expect(kept).toBe(
      "Each reading keeps the 50 statements that took the most time in it; the rest count toward the total.",
    )
  })

  test("says where percentiles come from, and names the engine that keeps none", () => {
    const mysql = historyMethod({ driver: "mysql", label: "MySQL" }, facts({ percentiles: true }))
    expect(mysql[0]).toContain("histogram")
    expect(mysql[0]).toContain("never under the true figure")
    const maria = historyMethod({ driver: "mysql", label: "MariaDB" }, facts())
    expect(maria[0]).toContain("MariaDB keeps no histogram of run times")
    const ch = historyMethod(
      { driver: "clickhouse", label: "ClickHouse" },
      facts({ percentiles: true }),
    )
    expect(ch[0]).toContain("sums the runs that finished since the reading before")
    for (const driver of ["sqlserver", "oracle"]) {
      expect(historyMethod({ driver, label: driver }, facts())[0]).toContain("no percentiles")
    }
  })
})

describe("a statement's charts", () => {
  test("are its points, with a point nothing was recorded at left a gap", () => {
    const rows = historyRows([
      { at: 1000, recorded: false, calls: 0, totalMs: 0, rows: 0 },
      { at: 2000, recorded: true, calls: 0, totalMs: 0, rows: 0 },
      {
        at: 3000,
        recorded: true,
        calls: 4,
        totalMs: 8,
        meanMs: 2,
        rows: 4,
        rowsRead: 400,
        p50Ms: 1,
        p95Ms: 4,
        p99Ms: 4,
      },
    ])
    expect(rows).toEqual([
      { ts: 1000 },
      { ts: 2000, time: 0, calls: 0, returned: 0 },
      { ts: 3000, time: 8, calls: 4, returned: 4, mean: 2, p50: 1, p95: 4, p99: 4, read: 400 },
    ])
  })

  test("mark each reset on the time axis", () => {
    expect(historyEvents(["2026-10-17T08:00:00Z"])).toEqual([
      {
        ts: "2026-10-17T08:00:00Z",
        kind: "action",
        title: "Statement statistics were reset",
        detail: "The figures after this count from the reset.",
        severity: "info",
      },
    ])
  })
})

describe("before the first point", () => {
  test("says when the next reading is, once the recorder has read the server", () => {
    const now = Date.parse("2026-10-17T09:00:00Z")
    expect(
      firstPointWords(
        facts({
          reading: { at: "2026-10-17T08:59:00Z", supported: true },
          nextAt: "2026-10-17T09:04:00Z",
        }),
        now,
      ),
    ).toBe(
      "The dashboard reads this server's statements every 5 minutes, and a statement's history begins with the second reading: the next is in about 4 minutes.",
    )
    expect(
      firstPointWords(
        facts({ reading: { at: "x", supported: true }, nextAt: "2026-10-17T09:00:30Z" }),
        now,
      ),
    ).toContain("within a minute")
    expect(firstPointWords(facts(), now)).toBe(
      "The dashboard reads this server's statements every 5 minutes, from when it starts, and a statement's history begins with its second reading.",
    )
  })
})
