import { describe, expect, test } from "bun:test"
import { offeredViews } from "../home/charts"
import {
  OVERVIEW_CHARTS,
  fillShape,
  hasPlaceholder,
  isStatementSort,
  millis,
  overviewCharts,
  planFor,
  quietChart,
  rowsPerCall,
  shapeFields,
  shapeSlots,
  shareWords,
  statementKeys,
  statementSorts,
} from "./performance-figures"

const at = (seconds, counters = {}, gauges = {}) => ({ at: seconds * 1000, counters, gauges })

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

describe("the charts drawn", () => {
  const sessions = (charts) => charts.find((chart) => chart.id === "sessions")

  test("leave out the lines an engine has no notion of", () => {
    const all = sessions(overviewCharts({ transactions: true, locks: true }))
    expect(all.series.map((line) => line.key)).toEqual([
      "open",
      "active",
      "inTransaction",
      "waiting",
    ])
    const analytic = sessions(overviewCharts({ transactions: false, locks: false }))
    expect(analytic.series.map((line) => line.key)).toEqual(["open", "active"])
    expect(analytic.sources.map((source) => source.key)).toEqual(["open", "active"])
    // The list handed to a memoised chart is the same one every time.
    expect(overviewCharts({ transactions: true, locks: true })).toBe(OVERVIEW_CHARTS)
  })

  test("a chart with nothing above zero in the window is quiet", () => {
    const chart = (id) => OVERVIEW_CHARTS.find((entry) => entry.id === id)
    const samples = [
      at(
        0,
        { rowsInserted: 40, deadlocks: 0, lockWaits: 0, blocksHit: 9, blocksRead: 1 },
        { sessions: 2 },
      ),
      at(
        5,
        { rowsInserted: 40, deadlocks: 0, lockWaits: 0, blocksHit: 9, blocksRead: 1 },
        { sessions: 2 },
      ),
    ]
    // Never moved since the server started: quiet from the first sample.
    expect(quietChart(chart("locks"), samples.slice(0, 1))).toBe(true)
    // Has moved before, and not in this window: quiet once there is a window.
    expect(quietChart(chart("writes"), samples.slice(0, 1))).toBe(false)
    expect(quietChart(chart("writes"), samples)).toBe(true)
    // No block read in the window: no hit rate to draw.
    expect(quietChart(chart("cache"), samples)).toBe(true)
    // A gauge that is merely low is not quiet.
    expect(quietChart(chart("sessions"), samples)).toBe(false)
    const moved = [
      ...samples,
      at(10, { rowsInserted: 41, deadlocks: 0, lockWaits: 0 }, { sessions: 2 }),
    ]
    expect(quietChart(chart("writes"), moved)).toBe(false)
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

  test("something that ran is never printed as taking nothing", () => {
    expect(millis(0.0004)).toBe("<0.01 ms")
    expect(millis(0.006)).toBe("0.01 ms")
    expect(millis(0)).toBe("0.00 ms")
  })

  test("a digest listed twice is two rows with two keys", () => {
    expect(statementKeys([{ id: "a1" }, { id: "b2" }, { id: "a1" }, { id: "a1" }])).toEqual([
      "a1",
      "b2",
      "a1~1",
      "a1~2",
    ])
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
    expect(hasPlaceholder("SELECT 1 -- why?\n FROM t /* $1 */")).toBe(false)
    expect(hasPlaceholder("SELECT $tag$ what? $tag$")).toBe(false)
  })

  test("its placeholders are found where they stand, each with the words before it", () => {
    const slots = shapeSlots("UPDATE orders SET note = $1 WHERE id = $2 AND owner = $1")
    expect(slots.map((slot) => [slot.token, slot.key, slot.before])).toEqual([
      ["$1", "$1", "note ="],
      ["$2", "$2", "id ="],
      ["$1", "$1", "owner ="],
    ])
    // A numbered placeholder repeated is one value; each ? is its own.
    expect(shapeFields(slots).map((slot) => slot.key)).toEqual(["$1", "$2"])
    const anonymous = shapeSlots("SELECT * FROM `t` WHERE `a` = ? AND `b` IN (?..) AND c = ?")
    expect(anonymous.map((slot) => [slot.token, slot.key])).toEqual([
      ["?", "?1"],
      ["?..", "?2"],
      ["?", "?3"],
    ])
    // An array of values is not a bracketed name.
    expect(shapeSlots("SELECT [?..][? + number % ?]").length).toBe(3)
    expect(
      shapeSlots("SELECT TOP (@p1) [id] FROM [dbo].[t] WHERE [n] = :2").map((s) => s.token),
    ).toEqual(["@p1", ":2"])
  })

  test("a PostgreSQL shape asks for the extension's markers and for each ? beside them", () => {
    // pg_stat_statements writes $n where it took a constant out; a literal it
    // left is a ? by the time the list has it. Each is a value of its own.
    const slots = shapeSlots("SELECT id FROM orders WHERE customer_id = $1 ORDER BY ?")
    expect(slots.map((slot) => [slot.token, slot.key, slot.before])).toEqual([
      ["$1", "$1", "customer_id ="],
      ["?", "?1", "ORDER BY"],
    ])
    expect(
      fillShape("SELECT id FROM orders WHERE customer_id = $1 ORDER BY ?", { $1: "42", "?1": "1" }),
    ).toBe("SELECT id FROM orders WHERE customer_id = 42 ORDER BY 1")
    // A statement the extension kept as it was sent is a shape with no plan.
    for (const utility of ["CREATE ROLE app_reader WITH LOGIN PASSWORD ?", "SET work_mem = ?"]) {
      expect(hasPlaceholder(utility)).toBe(true)
      expect(planFor(utility)).toBe("none")
    }
  })

  test("is a statement again once values stand where the placeholders were", () => {
    const shape = "SELECT * FROM t WHERE a = $1 AND b = $2 AND c = $1"
    expect(fillShape(shape, { $1: " 42 ", $2: "'paid'" })).toBe(
      "SELECT * FROM t WHERE a = 42 AND b = 'paid' AND c = 42",
    )
    // A value left empty is NULL: the statement still plans.
    expect(fillShape(shape, { $1: "42" })).toBe(
      "SELECT * FROM t WHERE a = 42 AND b = NULL AND c = 42",
    )
    expect(fillShape("SELECT ? || x, y IN (?..)", { "?1": "'a'", "?2": "1, 2" })).toBe(
      "SELECT 'a' || x, y IN (1, 2)",
    )
    expect(planFor(fillShape(shape, {}))).toBe("plan")
    // A key that is not the shape's own is never read off the prototype.
    expect(fillShape("SELECT ?", { constructor: "1" })).toBe("SELECT NULL")
  })
})
