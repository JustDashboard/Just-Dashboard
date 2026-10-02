import { describe, expect, test } from "bun:test"
import { planFromRows, planOf, planRows, planText } from "./plan"

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
