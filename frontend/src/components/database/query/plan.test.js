import { describe, expect, test } from "bun:test"
import {
  estimateText,
  factorText,
  planFromRows,
  planOf,
  planRows,
  planText,
  readingsOf,
  rowsText,
  shareText,
  slowestFirst,
} from "./plan"

const POSTGRES = [
  {
    Plan: {
      "Node Type": "Limit",
      "Total Cost": 498.64,
      "Plan Rows": 10,
      "Actual Rows": 10,
      "Actual Total Time": 5.3,
      "Actual Loops": 1,
      "Shared Hit Blocks": 151,
      "Shared Read Blocks": 0,
      Plans: [
        {
          "Node Type": "Hash Join",
          "Parent Relationship": "Outer",
          "Join Type": "Inner",
          "Total Cost": 459.71,
          "Plan Rows": 1800,
          "Actual Rows": 1800,
          "Actual Total Time": 4.3,
          "Actual Loops": 1,
          "Hash Cond": "(o.customer_id = c.id)",
          Plans: [
            {
              "Node Type": "Seq Scan",
              "Relation Name": "orders",
              Alias: "o",
              "Parallel Aware": true,
              "Total Cost": 376,
              "Plan Rows": 9000,
              "Actual Rows": 4500,
              "Actual Total Time": 0.8,
              "Actual Loops": 2,
            },
            {
              "Node Type": "Index Scan",
              "Relation Name": "customers",
              Alias: "customers",
              "Index Name": "customers_pkey",
              "Total Cost": 57,
              "Plan Rows": 240,
              "Actual Rows": 240,
              "Actual Total Time": 0.3,
              "Actual Loops": 1,
              Filter: "(tier = 'pro'::customer_tier)",
              "Rows Removed by Filter": 960,
            },
          ],
        },
      ],
    },
    "Planning Time": 4.4,
    "Execution Time": 5.6,
  },
]

describe("a PostgreSQL plan", () => {
  const plan = planOf(POSTGRES)
  const rows = planRows(plan)
  test("is a tree of its steps, measured when the statement was run", () => {
    expect(rows.map((row) => `${"  ".repeat(row.depth)}${row.node.title}`)).toEqual([
      "Limit",
      "  Hash Join",
      "    Parallel Seq Scan",
      "    Index Scan",
    ])
    expect(plan.analyzed).toBe(true)
    expect(plan.planningMs).toBe(4.4)
    expect(plan.executionMs).toBe(5.6)
  })
  test("a step names what it acts on: the table, its alias where it differs, the index", () => {
    expect(rows[2].node.target).toBe("orders o")
    expect(rows[3].node.target).toBe("customers using customers_pkey")
  })
  test("conditions are the step's notes; the rest are facts, zeroes left out", () => {
    expect(rows[1].node.notes).toEqual(["Hash Cond: (o.customer_id = c.id)"])
    expect(rows[3].node.notes).toEqual(["Filter: (tier = 'pro'::customer_tier)"])
    expect(rows[3].node.facts).toEqual([["Rows Removed by Filter", "960"]])
    expect(rows[0].node.facts).toEqual([["Shared Hit Blocks", "151"]])
    expect(rows[1].node.facts).toEqual([["Join Type", "Inner"]])
  })
  test("rows and time count every loop", () => {
    expect(rows[2].node.actualRows).toBe(9000)
    expect(rows[2].node.timeMs).toBe(1.6)
    expect(rows[2].node.loops).toBe(2)
  })
  test("a bar is the step's own share: what is under it is left out", () => {
    // Hash Join: 459.71 − (376 + 57) of 498.64.
    expect(rows[1].costShare).toBeCloseTo(26.71 / 498.64, 5)
    expect(rows[2].costShare).toBeCloseTo(376 / 498.64, 5)
    // Time: 4.3 − (1.6 + 0.3) of 5.3.
    expect(rows[1].timeShare).toBeCloseTo(2.4 / 5.3, 5)
    expect(rows[2].rowsShare).toBe(1)
    expect(rows[0].rowsShare).toBeCloseTo(10 / 9000, 8)
  })
  test("the tree's rules: which levels above a step still have a line running down", () => {
    expect(rows.map((row) => [row.id, row.last, row.rails])).toEqual([
      ["0", true, []],
      ["0.0", true, []],
      ["0.0.0", false, [false]],
      ["0.0.1", true, [false]],
    ])
  })
  test("a plan that was not run carries no measurements", () => {
    const planned = planOf([{ Plan: { "Node Type": "Seq Scan", "Total Cost": 1, "Plan Rows": 1 } }])
    expect(planned.analyzed).toBe(false)
    expect(planRows(planned)[0].timeShare).toBeUndefined()
  })
})

describe("a MySQL plan", () => {
  const BLOCK = {
    query_block: {
      select_id: 1,
      cost_info: { query_cost: "50.06" },
      ordering_operation: {
        using_filesort: true,
        nested_loop: [
          {
            table: {
              table_name: "i",
              access_type: "range",
              key: "PRIMARY",
              possible_keys: ["PRIMARY", "warehouse_id"],
              rows_examined_per_scan: 99,
              attached_condition: "(`i`.`id` < 100)",
              cost_info: { read_cost: "10.20", eval_cost: "9.90", prefix_cost: "20.10" },
            },
          },
          {
            table: {
              table_name: "w",
              access_type: "ALL",
              rows_examined_per_scan: 3,
              cost_info: { read_cost: "0.26", eval_cost: "9.90", prefix_cost: "50.06" },
            },
          },
        ],
      },
    },
  }
  test("the block form: operations named, tables by how they are read", () => {
    const rows = planRows(planOf(BLOCK))
    expect(rows.map((row) => `${"  ".repeat(row.depth)}${row.node.title}`)).toEqual([
      "Query block 1",
      "  Sort",
      "    Nested loop",
      "      Index range scan",
      "      Full table scan",
    ])
    expect(rows[3].node.target).toBe("i using PRIMARY")
    expect(rows[3].node.notes).toEqual(["Attached condition: (`i`.`id` < 100)"])
    expect(rows[3].node.rows).toBe(99)
    // A table's cost is its own reading and evaluating, not the running total.
    expect(rows[3].cost).toBeCloseTo(20.1, 5)
    expect(rows[4].cost).toBeCloseTo(10.16, 5)
    // A step with no cost of its own is the sum of what is under it.
    expect(rows[2].cost).toBeCloseTo(30.26, 5)
    expect(rows[0].cost).toBe(50.06)
    expect(rows[1].node.facts).toEqual([["Using filesort", "yes"]])
  })

  const MEASURED = {
    operation: "Limit: 5 row(s)",
    actual_rows: 5,
    actual_loops: 1,
    actual_last_row_ms: 5.8,
    inputs: [
      {
        operation: "Inner hash join (w.id = i.warehouse_id)",
        estimated_rows: 99.00000295,
        estimated_total_cost: 50.06,
        actual_rows: 99,
        actual_loops: 1,
        actual_last_row_ms: 3.7,
        hash_condition: ["(w.id = i.warehouse_id)"],
        inputs: [
          {
            operation: "Table scan on w",
            table_name: "warehouses",
            alias: "w",
            estimated_rows: 3,
            estimated_total_cost: 0.0036,
            actual_rows: 3,
            actual_loops: 1,
            actual_last_row_ms: 0.04,
          },
        ],
      },
    ],
    query: "/* select#1 */ select …",
    query_type: "select",
  }
  test("the measured form: a step is named by the start of its operation", () => {
    const plan = planOf(MEASURED)
    const rows = planRows(plan)
    expect(plan.analyzed).toBe(true)
    expect(rows.map((row) => row.node.title)).toEqual(["Limit", "Inner hash join", "Table scan"])
    expect(rows[0].node.notes).toEqual(["Limit: 5 row(s)"])
    // The condition is already in the operation: said once.
    expect(rows[1].node.notes).toEqual(["Inner hash join (w.id = i.warehouse_id)"])
    expect(rows[2].node.target).toBe("warehouses w")
    expect(rows[2].node.notes).toEqual([])
    expect(rows[0].node.facts).toEqual([])
  })
})

describe("a MariaDB plan", () => {
  const MARIA = {
    query_block: {
      select_id: 1,
      cost: 0.32,
      r_loops: 1,
      r_total_time_ms: 2.2,
      filesort: {
        sort_key: "p.views",
        r_total_time_ms: 0.3,
        temporary_table: {
          nested_loop: [
            {
              table: {
                table_name: "a",
                access_type: "range",
                key: "PRIMARY",
                cost: 0.004,
                rows: 9,
                r_rows: 9,
                r_loops: 1,
                r_table_time_ms: 0.8,
                r_other_time_ms: 0.02,
                r_engine_stats: { pages_accessed: 3 },
              },
            },
            {
              table: {
                table_name: "p",
                access_type: "ref",
                key: "fk_posts_author",
                cost: 0.2,
                rows: 19,
                r_rows: 20,
                r_loops: 9,
                r_table_time_ms: 1.0,
              },
            },
          ],
        },
      },
    },
    query_optimization: { r_total_time_ms: 0.18 },
  }
  test("a sort's and a table's time is their own; a block's is the whole", () => {
    const plan = planOf(MARIA)
    const rows = planRows(plan)
    expect(plan.analyzed).toBe(true)
    expect(plan.planningMs).toBe(0.18)
    expect(plan.executionMs).toBe(2.2)
    const by = Object.fromEntries(rows.map((row) => [row.node.title, row]))
    expect(by["Sort"].timeMs).toBeCloseTo(0.3 + 0.82 + 1.0, 5)
    expect(by["Sort"].timeShare).toBeCloseTo(0.3 / 2.2, 5)
    expect(by["Index lookup"].timeShare).toBeCloseTo(1.0 / 2.2, 5)
    // Rows per loop, times the loops.
    expect(by["Index lookup"].node.actualRows).toBe(180)
    expect(by["Index range scan"].node.facts).toContainEqual(["Pages accessed", "3"])
  })
  test("no step takes longer than the statement it ran inside", () => {
    // As MariaDB 11.8 answered: the sort is timed with the table read it
    // drives, and the table is timed as well — 0.38 + 0.38 under a statement
    // that took 0.42. The steps came to 0.73 ms inside 0.42 ms.
    const plan = planOf({
      query_block: {
        select_id: 1,
        cost: 0.16,
        r_loops: 1,
        r_total_time_ms: 0.41537062,
        nested_loop: [
          {
            read_sorted_file: {
              r_rows: 5,
              filesort: {
                sort_key: "posts.views desc",
                r_loops: 1,
                r_total_time_ms: 0.376793962,
                table: {
                  table_name: "posts",
                  access_type: "ALL",
                  rows: 774,
                  r_rows: 800,
                  r_loops: 1,
                  r_table_time_ms: 0.256213334,
                  r_other_time_ms: 0.128580787,
                },
              },
            },
          },
        ],
      },
    })
    const rows = planRows(plan)
    const whole = rows[0].timeMs
    expect(whole).toBeCloseTo(0.41537062, 6)
    for (const row of rows) expect(row.timeMs ?? 0).toBeLessThanOrEqual(whole + 1e-9)
    const by = Object.fromEntries(rows.map((row) => [row.node.title, row]))
    // What is left of the sort once its read is taken out is the sort's own.
    expect(by["Sort"].timeMs).toBeCloseTo(0.41537062, 6)
    expect(by["Full table scan"].timeMs).toBeCloseTo(0.384794121, 6)
    expect(by["Sort"].timeShare).toBeCloseTo((0.41537062 - 0.384794121) / 0.41537062, 5)
    // The shares of the steps' own time come to the whole, not to more than it.
    const shares = rows.reduce((total, row) => total + (row.timeShare ?? 0), 0)
    expect(shares).toBeCloseTo(1, 5)
  })
})

describe("a plan that came as rows", () => {
  test("SQL Server's SHOWPLAN is a tree by NodeId and Parent", () => {
    const plan = planFromRows({
      columns: [
        "StmtText",
        "StmtId",
        "NodeId",
        "Parent",
        "PhysicalOp",
        "LogicalOp",
        "Argument",
        "EstimateRows",
        "TotalSubtreeCost",
        "Type",
      ],
      types: [],
      rows: [
        ["select * from t order by a", "1", "1", "0", null, null, "1", 100.4, 0.35, "SELECT"],
        [
          "  |--Sort(ORDER BY:([a] ASC))",
          "1",
          "2",
          "1",
          "Sort",
          "Sort",
          "ORDER BY:([a] ASC)",
          100.4,
          0.35,
          "PLAN_ROW",
        ],
        [
          "       |--Clustered Index Scan",
          "1",
          "3",
          "2",
          "Clustered Index Scan",
          "Clustered Index Scan",
          "OBJECT:([t].[pk])",
          100.4,
          0.04,
          "PLAN_ROW",
        ],
      ],
      rowCount: 3,
      rowsAffected: 0,
      duration: "1ms",
      truncated: false,
      statement: "",
    })
    const rows = planRows(plan)
    expect(rows.map((row) => `${"  ".repeat(row.depth)}${row.node.title}`)).toEqual([
      "SELECT",
      "  Sort",
      "    Clustered Index Scan",
    ])
    expect(rows[0].node.notes).toEqual([])
    expect(rows[1].node.notes).toEqual(["ORDER BY:([a] ASC)"])
    expect(rows[2].cost).toBe(0.04)
    expect(rows[1].costShare).toBeCloseTo(0.31 / 0.35, 5)
    expect(plan.analyzed).toBe(false)
  })
  test("SQLite's query plan is a tree by id and parent, under one root", () => {
    const plan = planFromRows({
      columns: ["id", "parent", "notused", "detail"],
      types: [],
      rows: [
        ["3", "0", "0", "SCAN n"],
        ["5", "0", "0", "SEARCH b USING INTEGER PRIMARY KEY (rowid=?)"],
        ["9", "5", "0", "USE TEMP B-TREE FOR ORDER BY"],
      ],
      rowCount: 3,
      rowsAffected: 0,
      duration: "1ms",
      truncated: false,
      statement: "",
    })
    expect(planRows(plan).map((row) => `${"  ".repeat(row.depth)}${row.node.title}`)).toEqual([
      "Query plan",
      "  SCAN n",
      "  SEARCH b USING INTEGER PRIMARY KEY (rowid=?)",
      "    USE TEMP B-TREE FOR ORDER BY",
    ])
  })
  test("rows that name no parent are not a tree", () => {
    const result = {
      columns: ["explain"],
      types: [],
      rows: [["Expression"], ["  ReadFromMergeTree (t)"]],
      rowCount: 2,
      rowsAffected: 0,
      duration: "1ms",
      truncated: false,
      statement: "",
    }
    expect(planFromRows(result)).toBeNull()
    expect(planText(result)).toBe("Expression\n  ReadFromMergeTree (t)")
  })
  test("a table of several columns is not lines of text", () => {
    expect(
      planText({
        columns: ["id", "select_type"],
        types: [],
        rows: [["1", "SIMPLE"]],
        rowCount: 1,
        rowsAffected: 0,
        duration: "",
        truncated: false,
        statement: "",
      }),
    ).toBeNull()
  })
})

describe("a shape nobody has described", () => {
  test("is not guessed at", () => {
    expect(planOf({ something: "else" })).toBeNull()
    expect(planOf("text")).toBeNull()
    expect(planOf(null)).toBeNull()
    expect(planOf([])).toBeNull()
  })
})

/* What PostgreSQL 17 answered for a join whose two conditions are one condition twice. */
const MISJUDGED = [
  {
    Plan: {
      "Node Type": "Sort",
      "Total Cost": 107.79,
      "Plan Rows": 1,
      "Actual Rows": 50,
      "Actual Total Time": 0.76,
      "Actual Loops": 1,
      "Sort Key": ["o.id"],
      Plans: [
        {
          "Node Type": "Hash Join",
          "Join Type": "Inner",
          "Total Cost": 107.77,
          "Plan Rows": 1,
          "Actual Rows": 50,
          "Actual Total Time": 0.713,
          "Actual Loops": 1,
          "Hash Cond": "(c.id = o.customer_id)",
          Plans: [
            {
              "Node Type": "Seq Scan",
              "Relation Name": "jdt_explain_view_c",
              Alias: "c",
              "Total Cost": 4,
              "Plan Rows": 200,
              "Actual Rows": 200,
              "Actual Total Time": 0.036,
              "Actual Loops": 1,
            },
            {
              "Node Type": "Hash",
              "Total Cost": 103,
              "Plan Rows": 1,
              "Actual Rows": 50,
              "Actual Total Time": 0.629,
              "Actual Loops": 1,
              Plans: [
                {
                  "Node Type": "Seq Scan",
                  "Relation Name": "jdt_explain_view_o",
                  Alias: "o",
                  "Total Cost": 103,
                  "Plan Rows": 1,
                  "Actual Rows": 50,
                  "Actual Total Time": 0.575,
                  "Actual Loops": 1,
                  Filter: "((a = 1) AND (b = 1))",
                  "Rows Removed by Filter": 4950,
                },
              ],
            },
          ],
        },
      ],
    },
    "Planning Time": 0.448,
    "Execution Time": 0.839,
  },
]

describe("a measured step far from its estimate", () => {
  const rows = planRows(planOf(MISJUDGED))
  const by = Object.fromEntries(rows.map((row) => [row.node.target ?? row.node.title, row]))
  test("is said where the rows that came are ten times the expected or more", () => {
    expect(by["jdt_explain_view_o o"].estimate).toEqual({
      expected: 1,
      actual: 50,
      factor: 50,
      farOff: true,
    })
    expect(estimateText(by["jdt_explain_view_o o"].estimate)).toBe("est 1 · actual 50 (×50)")
    expect(by["jdt_explain_view_c c"].estimate.farOff).toBe(false)
    expect(rows.filter((row) => row.estimate?.farOff).map((row) => row.node.title)).toEqual([
      "Sort",
      "Hash Join",
      "Hash",
      "Seq Scan",
    ])
  })
  test("either way: fewer rows than expected is as far off as more", () => {
    const [row] = planRows(
      planOf([
        {
          Plan: {
            "Node Type": "Seq Scan",
            "Plan Rows": 48_000,
            "Actual Rows": 12,
            "Actual Loops": 1,
            "Actual Total Time": 4,
          },
        },
      ]),
    )
    expect(row.estimate.farOff).toBe(true)
    expect(estimateText(row.estimate)).toBe("est 48,000 · actual 12 (×4,000)")
  })
  test("is not said of a plan that was only planned", () => {
    const [row] = planRows(
      planOf([{ Plan: { "Node Type": "Seq Scan", "Plan Rows": 1, "Total Cost": 3 } }]),
    )
    expect(row.estimate).toBeUndefined()
    expect(row.never).toBeUndefined()
  })
  test("counts the whole run: a loop's estimate times its loops against every row that came", () => {
    const [, , inner] = planRows(
      planOf([
        {
          Plan: {
            "Node Type": "Nested Loop",
            "Join Type": "Inner",
            "Plan Rows": 200,
            "Actual Rows": 9_000,
            "Actual Loops": 1,
            Plans: [
              { "Node Type": "Seq Scan", "Plan Rows": 200, "Actual Rows": 200, "Actual Loops": 1 },
              { "Node Type": "Index Scan", "Plan Rows": 1, "Actual Rows": 45, "Actual Loops": 200 },
            ],
          },
        },
      ]),
    )
    expect(inner.estimate).toMatchObject({ expected: 200, actual: 9_000, farOff: true })
    expect(estimateText(inner.estimate)).toBe("est 200 · actual 9,000 (×45)")
  })
  test("a lookup that mostly finds nothing is the planner's one-row floor, not a misjudgement", () => {
    // PostgreSQL before 18 rounds a loop's rows to whole rows: 0 of the 1 it always plans for.
    const [, , inner] = planRows(
      planOf([
        {
          Plan: {
            "Node Type": "Nested Loop",
            "Plan Rows": 10_000,
            "Actual Rows": 30,
            "Actual Loops": 1,
            Plans: [
              {
                "Node Type": "Seq Scan",
                "Plan Rows": 10_000,
                "Actual Rows": 10_000,
                "Actual Loops": 1,
              },
              {
                "Node Type": "Index Scan",
                "Plan Rows": 1,
                "Actual Rows": 0,
                "Actual Loops": 10_000,
              },
            ],
          },
        },
      ]),
    )
    expect(inner.estimate.farOff).toBe(false)
  })
  test("fewer rows under a limit is the limit stopping, not a misjudgement", () => {
    const [limit, scan] = planRows(
      planOf([
        {
          Plan: {
            "Node Type": "Limit",
            "Plan Rows": 5,
            "Actual Rows": 5,
            "Actual Loops": 1,
            Plans: [
              {
                "Node Type": "Seq Scan",
                "Plan Rows": 2500,
                "Actual Rows": 5,
                "Actual Loops": 1,
                Filter: "(a < 50)",
              },
            ],
          },
        },
      ]),
    )
    expect(limit.node.stops).toBe("inputs")
    expect(scan.estimate).toMatchObject({ expected: 2500, actual: 5, farOff: false })
    // More rows than planned has no such reason.
    const [, more] = planRows(
      planOf([
        {
          Plan: {
            "Node Type": "Limit",
            "Plan Rows": 5,
            "Actual Rows": 5,
            "Actual Loops": 1,
            Plans: [
              { "Node Type": "Seq Scan", "Plan Rows": 1, "Actual Rows": 50, "Actual Loops": 1 },
            ],
          },
        },
      ]),
    )
    expect(more.estimate.farOff).toBe(true)
  })
  test("a semi-join stops its inner input at the first match, and only that input", () => {
    const [, outer, inner] = planRows(
      planOf([
        {
          Plan: {
            "Node Type": "Nested Loop",
            "Join Type": "Semi",
            "Plan Rows": 200,
            "Actual Rows": 2,
            "Actual Loops": 1,
            Plans: [
              { "Node Type": "Seq Scan", "Plan Rows": 2000, "Actual Rows": 200, "Actual Loops": 1 },
              { "Node Type": "Index Scan", "Plan Rows": 50, "Actual Rows": 2, "Actual Loops": 200 },
            ],
          },
        },
      ]),
    )
    expect(outer.estimate.farOff).toBe(true)
    expect(inner.estimate.farOff).toBe(false)
  })
  test("a step that never ran says so and is not compared", () => {
    const [, , never] = planRows(
      planOf([
        {
          Plan: {
            "Node Type": "Hash Join",
            "Plan Rows": 10,
            "Actual Rows": 0,
            "Actual Loops": 1,
            Plans: [
              { "Node Type": "Seq Scan", "Plan Rows": 10, "Actual Rows": 0, "Actual Loops": 1 },
              { "Node Type": "Hash", "Plan Rows": 900, "Actual Rows": 0, "Actual Loops": 0 },
            ],
          },
        },
      ]),
    )
    expect(never.never).toBe(true)
    expect(never.estimate).toBeUndefined()
  })
})

describe("a measured step's time", () => {
  const rows = planRows(planOf(MISJUDGED))
  const by = Object.fromEntries(rows.map((row) => [row.node.target ?? row.node.title, row]))
  test("is its own and, with the steps under it, the whole of its subtree, both against the plan's", () => {
    expect(by["Hash"].selfMs).toBeCloseTo(0.054, 6)
    expect(by["Hash"].timeMs).toBeCloseTo(0.629, 6)
    expect(by["Hash"].timeShare).toBeCloseTo(0.054 / 0.76, 6)
    expect(by["Hash"].totalShare).toBeCloseTo(0.629 / 0.76, 6)
    expect(by["Sort"].totalShare).toBe(1)
    expect(by["Hash Join"].selfMs).toBeCloseTo(0.713 - 0.036 - 0.629, 6)
    // The steps' own shares make up the whole.
    expect(rows.reduce((total, row) => total + row.timeShare, 0)).toBeCloseTo(1, 6)
  })
  test("orders the steps slowest first, out of the tree, a step's place kept as its id", () => {
    const sorted = slowestFirst(rows)
    expect(sorted.map((row) => row.node.target ?? row.node.title)).toEqual([
      "jdt_explain_view_o o",
      "Hash",
      "Hash Join",
      "Sort",
      "jdt_explain_view_c c",
    ])
    expect(sorted[0].id).toBe("0.0.1.0")
    expect(sorted.every((row) => row.depth === 0 && row.rails.length === 0)).toBe(true)
    // The plan's own order is left as it was.
    expect(rows[0].node.title).toBe("Sort")
  })
  test("a step the plan did not time keeps its place after the timed ones", () => {
    const sorted = slowestFirst([
      { id: "0", depth: 0, rails: [], last: true, node: { title: "Group" } },
      { id: "0.0", depth: 1, rails: [], last: false, node: { title: "Quick" }, selfMs: 1 },
      { id: "0.1", depth: 1, rails: [], last: true, node: { title: "Slow" }, selfMs: 9 },
    ])
    expect(sorted.map((row) => row.node.title)).toEqual(["Slow", "Quick", "Group"])
  })
  test("reads under the step as facts, in words", () => {
    expect(readingsOf(by["Hash"])).toEqual([
      ["Time in this step", "0.05 ms · 7%"],
      ["With the steps under it", "0.63 ms · 83%"],
      ["Rows expected", "1"],
    ])
    // A leaf has nothing under it to add; a loop count is said once there are loops.
    expect(readingsOf(by["jdt_explain_view_c c"]).map(([label]) => label)).toEqual([
      "Time in this step",
      "Rows expected",
    ])
  })
})

describe("the figures a plan prints", () => {
  test("a factor to two figures", () => {
    expect(factorText(4017.5)).toBe("×4,000")
    expect(factorText(12.4)).toBe("×12")
    expect(factorText(125)).toBe("×130")
    expect(factorText(50)).toBe("×50")
  })
  test("rows whole from ten, a fraction of a row below", () => {
    expect(rowsText(50.00000353902578)).toBe("50")
    expect(rowsText(1666.4999)).toBe("1,666")
    expect(rowsText(2.5)).toBe("2.5")
    expect(rowsText(0.01)).toBe("0.01")
  })
  test("a share as a percentage, and a sliver as under one", () => {
    expect(shareText(0.45)).toBe("45%")
    expect(shareText(0.003)).toBe("under 1%")
    expect(shareText(0)).toBe("0%")
  })
})

describe("a measured MySQL and MariaDB plan", () => {
  test("MySQL's limit stops the steps under it", () => {
    // As MySQL 8.4 measured `… WHERE a < 50 LIMIT 5`.
    const plan = planOf({
      operation: "Limit: 5 row(s)",
      access_type: "limit",
      limit: 5,
      estimated_rows: 5,
      actual_rows: 5,
      actual_loops: 1,
      actual_last_row_ms: 0.12,
      inputs: [
        {
          operation: "Filter: (jdt_explain_view_o.a < 50)",
          access_type: "filter",
          estimated_rows: 1666.4999723434448,
          actual_rows: 5,
          actual_loops: 1,
          actual_last_row_ms: 0.118,
          inputs: [
            {
              operation: "Table scan on jdt_explain_view_o",
              access_type: "table",
              table_name: "jdt_explain_view_o",
              estimated_rows: 5000,
              actual_rows: 5,
              actual_loops: 1,
              actual_last_row_ms: 0.115,
            },
          ],
        },
      ],
    })
    const rows = planRows(plan)
    expect(rows[0].node.stops).toBe("inputs")
    expect(rows.map((row) => row.estimate.farOff)).toEqual([false, false, false])
    expect(rows[2].estimate.factor).toBe(1000)
  })
  test("MySQL's nested semi-join stops its inner input", () => {
    const [row] = planRows(
      planOf({
        operation: "Nested loop semijoin",
        join_type: "semijoin",
        join_algorithm: "nested_loop",
        inputs: [],
      }),
    )
    expect(row.node.stops).toBe("inner")
  })
  test("a MariaDB table's rows are the ones its condition kept, so a misjudged condition shows", () => {
    // As MariaDB 11.4 measured it: the scan read the 5,000 rows it planned to
    // and kept 1% of them, where it had planned to keep them all.
    const rows = planRows(
      planOf({
        query_block: {
          select_id: 1,
          cost: 8.61,
          r_loops: 1,
          r_total_time_ms: 1.0,
          nested_loop: [
            {
              table: {
                table_name: "o",
                access_type: "index",
                key: "PRIMARY",
                attached_condition: "o.a = 1 and o.b = 1",
                rows: 5000,
                filtered: 100,
                r_rows: 5000,
                r_filtered: 1,
                r_loops: 1,
                r_table_time_ms: 0.76,
                r_other_time_ms: 0.19,
              },
            },
            {
              table: {
                table_name: "c",
                access_type: "eq_ref",
                key: "PRIMARY",
                rows: 1,
                filtered: 100,
                r_rows: 1,
                r_filtered: 100,
                r_loops: 50,
                r_table_time_ms: 0.035,
              },
            },
          ],
        },
      }),
    )
    const by = Object.fromEntries(rows.map((row) => [row.node.target, row]))
    expect(by["o using PRIMARY"].node.rows).toBe(5000)
    expect(by["o using PRIMARY"].node.actualRows).toBe(50)
    expect(by["o using PRIMARY"].node.facts).toContainEqual(["Rows read", "5,000"])
    expect(estimateText(by["o using PRIMARY"].estimate)).toBe("est 5,000 · actual 50 (×100)")
    expect(by["c using PRIMARY"].node.actualRows).toBe(50)
    expect(by["c using PRIMARY"].estimate.farOff).toBe(false)
    expect(by["c using PRIMARY"].node.facts.map(([label]) => label)).not.toContain("Rows read")
  })
})
