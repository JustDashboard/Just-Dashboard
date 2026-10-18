import { describe, expect, test } from "bun:test"
import { planRows } from "./plan"
import { planFromText } from "./plan-text"

const tree = (plan) => planRows(plan).map((row) => `${"  ".repeat(row.depth)}${row.node.title}`)

/* Each text is what the engine answered the Explain route on the shared test servers. */

const ORACLE = [
  "Plan hash value: 4178406387",
  " ",
  "------------------------------------------------------------------------------------------",
  "| Id  | Operation           | Name               | Rows  | Bytes | Cost (%CPU)| Time     |",
  "------------------------------------------------------------------------------------------",
  "|   0 | SELECT STATEMENT    |                    |    50 |  4350 |     9  (12)| 00:00:01 |",
  "|   1 |  SORT ORDER BY      |                    |    50 |  4350 |     9  (12)| 00:00:01 |",
  "|*  2 |   HASH JOIN         |                    |    50 |  4350 |     8   (0)| 00:00:01 |",
  "|*  3 |    TABLE ACCESS FULL| JDT_EXPLAIN_VIEW_O |    50 |  2600 |     5   (0)| 00:00:01 |",
  "|   4 |    TABLE ACCESS FULL| JDT_EXPLAIN_VIEW_C |   200 |  7000 |     3   (0)| 00:00:01 |",
  "------------------------------------------------------------------------------------------",
  " ",
  "Predicate Information (identified by operation id):",
  "---------------------------------------------------",
  " ",
  '   2 - access("C"."ID"="O"."CUSTOMER_ID")',
  '   3 - filter("O"."A"=1 AND "O"."B"=1)',
  " ",
  "Note",
  "-----",
  "   - dynamic statistics used: dynamic sampling (level=2)",
  "   - this is an adaptive plan",
].join("\n")

describe("Oracle's DBMS_XPLAN table", () => {
  const plan = planFromText(ORACLE)
  const rows = planRows(plan)
  test("is a tree by the Operation's indent", () => {
    expect(tree(plan)).toEqual([
      "SELECT STATEMENT",
      "  SORT ORDER BY",
      "    HASH JOIN",
      "      TABLE ACCESS FULL",
      "      TABLE ACCESS FULL",
    ])
    expect(plan.analyzed).toBe(false)
  })
  test("a step's object, rows and cost are its own columns; the rest are facts", () => {
    expect(rows[3].node.target).toBe("JDT_EXPLAIN_VIEW_O")
    expect(rows[3].node.rows).toBe(50)
    expect(rows[4].node.rows).toBe(200)
    expect(rows[2].cost).toBe(8)
    // The cost is the step's and everything under it, as everywhere else.
    expect(rows[2].costShare).toBeCloseTo(0 / 9, 6)
    expect(rows[3].costShare).toBeCloseTo(5 / 9, 6)
    expect(rows[3].node.facts).toEqual([
      ["Bytes", "2600"],
      ["Estimated time", "00:00:01"],
    ])
    expect(rows.every((row) => row.timeMs === undefined)).toBe(true)
  })
  test("the conditions go to the step they name, and the notes to the statement", () => {
    expect(rows[2].node.notes).toEqual(['access("C"."ID"="O"."CUSTOMER_ID")'])
    expect(rows[3].node.notes).toEqual(['filter("O"."A"=1 AND "O"."B"=1)'])
    expect(rows[4].node.notes).toEqual([])
    expect(rows[0].node.notes).toEqual([
      "dynamic statistics used: dynamic sampling (level=2)",
      "this is an adaptive plan",
    ])
  })
  test("a step with no estimate has none, a long condition is one note, and counts are scaled", () => {
    const plan = planFromText(
      [
        "| Id  | Operation          | Name | Rows  | Bytes | Cost (%CPU)| Time     |",
        "-----------------------------------------------------------------------------",
        "|   0 | SELECT STATEMENT   |      |  1200K|  30M  |   400   (2)| 00:00:01 |",
        "|*  1 |  COUNT STOPKEY     |      |       |       |            |          |",
        "|*  2 |   INDEX RANGE SCAN | T_IX |  1200K|  30M  |   400   (2)| 00:00:01 |",
        "-----------------------------------------------------------------------------",
        "",
        "Predicate Information (identified by operation id):",
        "---------------------------------------------------",
        "   1 - filter(ROWNUM<=5)",
        '   2 - access("A">1 AND',
        '              "B"<2)',
        '       filter("C" IS NOT NULL)',
      ].join("\n"),
    )
    const rows = planRows(plan)
    expect(rows[0].node.rows).toBe(1_200_000)
    expect(rows[1].node.rows).toBeUndefined()
    expect(rows[1].node.cost).toBeUndefined()
    expect(rows[2].node.notes).toEqual(['access("A">1 AND "B"<2)', 'filter("C" IS NOT NULL)'])
  })
})

const MYSQL = [
  "-> Nested loop inner join  (cost=10040 rows=100000) (actual time=1.1..1.2 rows=2 loops=1)",
  "    -> Table scan on c  (cost=20.2 rows=200) (actual time=0.0266..0.0656 rows=200 loops=1)",
  "    -> Single-row index lookup on <subquery2> using <auto_distinct_key> (customer_id=c.id)  (cost=554..554 rows=1) (actual time=0.00556..0.00557 rows=0.01 loops=200)",
  "        -> Materialize with deduplication  (cost=554..554 rows=500) (actual time=1.05..1.05 rows=2 loops=1)",
  "            -> Filter: (o.customer_id is not null)  (cost=504 rows=500) (actual time=0.0681..1.02 rows=50 loops=1)",
  "                -> Filter: (o.a = 3)  (cost=504 rows=500) (actual time=0.0673..1.01 rows=50 loops=1)",
  "                    -> Table scan on o  (cost=504 rows=5000) (actual time=0.0589..0.802 rows=5000 loops=1)",
  "",
].join("\n")

describe("MySQL's measured tree, as text", () => {
  const plan = planFromText(MYSQL)
  const rows = planRows(plan)
  test("is a tree by its four-space indent, measured", () => {
    expect(tree(plan)).toEqual([
      "Nested loop inner join",
      "  Table scan",
      "  Single-row index lookup",
      "    Materialize with deduplication",
      "      Filter",
      "        Filter",
      "          Table scan",
    ])
    expect(plan.analyzed).toBe(true)
    expect(plan.executionMs).toBeCloseTo(1.2, 6)
  })
  test("a step names its table, keeps its detail, and counts every loop", () => {
    expect(rows[1].node.target).toBe("c")
    expect(rows[2].node.target).toBe("<subquery2> using <auto_distinct_key>")
    expect(rows[2].node.notes).toEqual([
      "Single-row index lookup on <subquery2> using <auto_distinct_key> (customer_id=c.id)",
    ])
    expect(rows[4].node.notes).toEqual(["Filter: (o.customer_id is not null)"])
    expect(rows[2].node.cost).toBe(554)
    expect(rows[2].node.rows).toBe(1)
    expect(rows[2].node.loops).toBe(200)
    expect(rows[2].node.actualRows).toBeCloseTo(2, 6)
    expect(rows[2].node.timeMs).toBeCloseTo(0.00557 * 200, 6)
  })
  test("far-off steps are said as they are of the measured JSON", () => {
    expect(rows.filter((row) => row.estimate?.farOff).map((row) => row.node.title)).toEqual([
      "Nested loop inner join",
      "Materialize with deduplication",
      "Filter",
      "Filter",
    ])
  })
  test("a limit stops what is under it, and a step that never ran says so", () => {
    const rows = planRows(
      planFromText(
        [
          "-> Limit: 5 row(s)  (cost=504 rows=5) (actual time=0.15..0.152 rows=5 loops=1)",
          "    -> Nested loop antijoin  (cost=504 rows=1666) (actual time=0.144..0.146 rows=5 loops=1)",
          "        -> Table scan on t  (cost=504 rows=5000) (actual time=0.141..0.143 rows=5 loops=1)",
          "        -> Index lookup on u using fk (t_id=t.id)  (cost=0.25 rows=1) (never executed)",
        ].join("\n"),
      ),
    )
    expect(rows[0].node.stops).toBe("inputs")
    expect(rows[1].node.stops).toBe("inner")
    expect(rows[2].estimate.farOff).toBe(false)
    expect(rows[3].never).toBe(true)
  })
})

describe("ClickHouse's steps", () => {
  const plan = planFromText(
    [
      "Expression (Project names)",
      "  Sorting (Sorting for ORDER BY)",
      "    Expression ((Before ORDER BY + (Projection + )))",
      "      Expression",
      "        Join",
      "          Expression ((WHERE + Change column names to column identifiers))",
      "            ReadFromMergeTree (default.jdt_explain_view_o)",
      "          Expression (Change column names to column identifiers)",
      "            ReadFromMergeTree (default.jdt_explain_view_c)",
    ].join("\n"),
  )
  const rows = planRows(plan)
  test("are a tree by their two-space indent, with nothing to measure", () => {
    expect(tree(plan)).toEqual([
      "Expression",
      "  Sorting",
      "    Expression",
      "      Expression",
      "        Join",
      "          Expression",
      "            ReadFromMergeTree",
      "          Expression",
      "            ReadFromMergeTree",
    ])
    expect(plan.analyzed).toBe(false)
    expect(rows.every((row) => row.cost === undefined && row.node.rows === undefined)).toBe(true)
  })
  test("what a step is for is its note, the table it reads its target", () => {
    expect(rows[1].node.notes).toEqual(["Sorting for ORDER BY"])
    expect(rows[2].node.notes).toEqual(["Before ORDER BY + (Projection + )"])
    expect(rows[3].node.notes).toEqual([])
    expect(rows[6].node.target).toBe("default.jdt_explain_view_o")
    expect(rows[6].node.notes).toEqual([])
    expect(
      planRows(planFromText("Limit (preliminary LIMIT (without OFFSET))"))[0].node.notes,
    ).toEqual(["preliminary LIMIT (without OFFSET)"])
  })
})

describe("text nobody has described", () => {
  test("is not guessed at", () => {
    expect(planFromText("")).toBeNull()
    expect(
      planFromText("distribution: local\nvectorized: true\n\n• scan\n  table: t@t_pkey"),
    ).toBeNull()
    expect(planFromText("Expression\n    ReadFromMergeTree (t)")).toBeNull()
    expect(planFromText("Expression\nExpression")).toBeNull()
    expect(planFromText("-> Table scan on t\n  -> Filter: (a = 1)")).toBeNull()
  })
})
