import { describe, expect, test } from "bun:test"
import { offeredViews } from "../home/charts"
import {
  OVERVIEW_CHARTS,
  READING_LABELS,
  hasPlaceholder,
  isStatementSort,
  millis,
  performanceReadings,
  planFor,
  rowsPerCall,
  shareWords,
  statementSorts,
} from "./performance-figures"

const at = (seconds, counters = {}, gauges = {}) => ({ at: seconds * 1000, counters, gauges })
const byKey = (readings) => Object.fromEntries(readings.map((reading) => [reading.key, reading]))

describe("a server's six readings", () => {
  test("say who is working and who is waiting, from the session counts", () => {
    const samples = [
      at(
        0,
        { transactionsAll: 100 },
        { sessions: 12, sessionsActive: 2, sessionsWaiting: 0, sessionsMax: 100 },
      ),
      at(
        5,
        { transactionsAll: 150 },
        {
          sessions: 12,
          sessionsActive: 3,
          sessionsWaiting: 2,
          sessionsIdleInTransaction: 1,
          sessionsMax: 100,
        },
      ),
    ]
    const readings = byKey(performanceReadings("server", samples))
    expect(Object.keys(readings)).toEqual([
      "sessions",
      "working",
      "waiting",
      "rows",
      "transactions",
      "cache",
    ])
    expect(readings.working).toMatchObject({ value: "3", hint: "1 more idle in a transaction" })
    expect(readings.waiting).toMatchObject({ value: "2", tone: "warning" })
    expect(readings.transactions.value).toBe("10")
  })

  test("nothing waiting takes no tone", () => {
    const readings = byKey(
      performanceReadings("server", [
        at(0, {}, { sessions: 3, sessionsActive: 1, sessionsWaiting: 0 }),
      ]),
    )
    expect(readings.waiting).toMatchObject({ value: "0", tone: "default" })
  })

  test("a session list closed to the account is a dash, with the threads it does report", () => {
    const readings = byKey(
      performanceReadings("server", [at(0, { queries: 10 }, { threadsRunning: 2 })]),
    )
    expect(readings.sessions.value).toBeUndefined()
    expect(readings.working.value).toBe("2")
    expect(readings.waiting.value).toBeUndefined()
    expect(readings.waiting.hint).toBe("Not reported to this account")
  })

  test("the fourth figure is the engine's own", () => {
    const longest = byKey(
      performanceReadings("server", [
        at(0, {}, { longestQuerySeconds: 95, oldestTransactionSeconds: 300 }),
      ]),
    ).longest
    expect(longest).toMatchObject({ value: "1m 35s", tone: "warning" })
    expect(longest.hint).toBe("oldest transaction open for 5m")

    const idle = byKey(
      performanceReadings("server", [at(0, {}, { longestQuerySeconds: 0 })]),
    ).longest
    expect(idle).toMatchObject({ value: "None", tone: "default" })

    const counted = byKey(
      performanceReadings("server", [at(0, { queries: 100 }), at(5, { queries: 200 })]),
    ).statements
    expect(counted).toMatchObject({ value: "20", trailing: "a second" })
  })

  test("a file and an analytic engine are read as their homes read them", () => {
    expect(
      performanceReadings("file", [at(0, {}, { fileBytes: 4096 })]).map((r) => r.label),
    ).toEqual(READING_LABELS.file)
    expect(performanceReadings("analytic", [at(0)]).map((r) => r.label)).toEqual(
      READING_LABELS.analytic,
    )
    expect(READING_LABELS.server.length).toBe(6)
  })
})

describe("the charts an engine is offered", () => {
  const ids = (sample) => offeredViews(OVERVIEW_CHARTS, sample).map((chart) => chart.id)

  test("are the ones its newest sample can fill", () => {
    const postgres = at(
      5,
      {
        transactionsAll: 1,
        rowsRead: 1,
        rowsWritten: 1,
        rowsInserted: 1,
        blocksHit: 1,
        blocksRead: 1,
        walBytes: 1,
        deadlocks: 0,
        tempFiles: 0,
      },
      { sessions: 3, sessionsActive: 1 },
    )
    expect(ids(postgres)).toEqual([
      "sessions",
      "transactions",
      "rows",
      "writes",
      "cache",
      "locks",
      "log",
      "temporary",
    ])
    const sqlite = at(5, {}, { fileBytes: 1 })
    expect(ids(sqlite)).toEqual([])
  })

  test("every chart has a line and every line a place to come from", () => {
    for (const chart of OVERVIEW_CHARTS) {
      expect(chart.series.length).toBeGreaterThan(0)
      expect(chart.sources.map((source) => source.key)).toEqual(chart.series.map((s) => s.key))
      expect(new Set(chart.series.map((s) => s.key)).size).toBe(chart.series.length)
    }
    expect(new Set(OVERVIEW_CHARTS.map((chart) => chart.id)).size).toBe(OVERVIEW_CHARTS.length)
  })
})

describe("a statement's figures", () => {
  test("milliseconds are written at the precision they are compared at", () => {
    expect(millis(0.874)).toBe("0.87 ms")
    expect(millis(4.44)).toBe("4.4 ms")
    expect(millis(321)).toBe("321 ms")
    expect(millis(1840)).toBe("1.8 s")
    expect(millis(42_000)).toBe("42 s")
    expect(millis(125_000)).toBe("2m 5s")
  })

  test("a share is never zero for something that ran", () => {
    expect(shareWords(0.4717)).toBe("47%")
    expect(shareWords(0.05)).toBe("5.0%")
    expect(shareWords(0.0004)).toBe("<0.1%")
    expect(shareWords(0)).toBe("0%")
  })

  test("the slowest-run order is offered only where the engine keeps one", () => {
    const withMax = [{ id: "1", maxMs: 9 }]
    const without = [{ id: "1" }]
    expect(statementSorts(withMax).map((sort) => sort.id)).toContain("max")
    expect(statementSorts(without).map((sort) => sort.id)).not.toContain("max")
    expect(statementSorts(without).map((sort) => sort.id)).toEqual([
      "total",
      "mean",
      "calls",
      "rows",
    ])
    expect(isStatementSort("mean")).toBe(true)
    expect(isStatementSort("p95")).toBe(false)
  })

  test("rows a call needs a call", () => {
    expect(rowsPerCall({ rows: 30, calls: 3 })).toBe(10)
    expect(rowsPerCall({ rows: 0, calls: 0 })).toBeUndefined()
  })
})

describe("whether a statement has a plan to ask for", () => {
  test("one that reads or changes rows does, as it stands or once it has values", () => {
    expect(planFor("SELECT count(*) FROM orders")).toBe("plan")
    expect(planFor("  with t as (select 1) select * from t")).toBe("plan")
    expect(planFor("UPDATE orders SET note = $1 WHERE id = $2")).toBe("shape")
  })

  test("one that does neither has none", () => {
    expect(planFor("VACUUM (VERBOSE) orders")).toBe("none")
    expect(planFor("CREATE INDEX i ON t (x)")).toBe("none")
    expect(planFor("")).toBe("none")
  })
})

describe("a statement that is a shape", () => {
  test("is told by the placeholder where a value was", () => {
    expect(hasPlaceholder("SELECT * FROM orders WHERE id = $1")).toBe(true)
    expect(hasPlaceholder("SELECT * FROM `orders` WHERE `id` = ?")).toBe(true)
    expect(hasPlaceholder("SELECT * FROM orders WHERE id = :1")).toBe(true)
    expect(hasPlaceholder("SELECT * FROM orders WHERE id = @p1")).toBe(true)
  })

  test("a statement with no values in it can be planned as it stands", () => {
    expect(hasPlaceholder("SELECT count(*) FROM orders")).toBe(false)
    expect(hasPlaceholder("ANALYZE public.sessions_log")).toBe(false)
  })

  test("a question mark inside quotes or a cast is not a placeholder", () => {
    expect(hasPlaceholder("SELECT 'why?' FROM t")).toBe(false)
    expect(hasPlaceholder('SELECT "odd?name" FROM t')).toBe(false)
    expect(hasPlaceholder("SELECT a::text FROM t")).toBe(false)
    expect(hasPlaceholder("SELECT [is?] FROM t")).toBe(false)
  })
})
