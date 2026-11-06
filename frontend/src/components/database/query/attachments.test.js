import { describe, expect, test } from "bun:test"
import { ATTACHED_TEXT, boundAttached } from "../../assistant/agent/model"
import {
  TABLE_CELL,
  TABLE_COLUMNS,
  TABLE_ROWS,
  isStatementFence,
  planSource,
  queryAttachables,
  resultTable,
  statementTitle,
} from "./attachments"

const result = {
  columns: ["id", "email", "tier"],
  rows: [
    ["1", "ann@example.com", "pro"],
    ["2", "bo@example.com", null],
    ["3", "cy@example.com", "free"],
  ],
  rowCount: 3,
}

describe("a result attached to a question", () => {
  test("is a small table of text, NULL said, with how many rows there were", () => {
    const { text, rows } = resultTable(result)
    expect(rows).toBe(3)
    expect(text.split("\n")).toEqual([
      "id | email           | tier",
      "---+-----------------+-----",
      "1  | ann@example.com | pro",
      "2  | bo@example.com  | NULL",
      "3  | cy@example.com  | free",
      "(3 rows)",
    ])
  })

  test("carries its first rows and columns, each value on one line and cut short", () => {
    const columns = Array.from({ length: 40 }, (_, at) => `c${at}`)
    const rows = Array.from({ length: 120 }, (_, at) =>
      columns.map((_, column) => (column === 0 ? `line one\nline ${at}` : "é".repeat(200))),
    )
    const { text, rows: carried } = resultTable({ columns, rows, rowCount: 5000 })
    expect(carried).toBeLessThanOrEqual(TABLE_ROWS)
    expect(text.split("\n")[0].split(" | ")).toHaveLength(TABLE_COLUMNS)
    expect(text).not.toContain("line one\n")
    expect(text).toContain("line one line 0")
    expect(text).toContain(`${"é".repeat(TABLE_CELL - 1)}…`)
    expect(text).toContain("of 5,000)")
    expect(text).toContain("10 more columns left out")
    // However wide the values, it fits what a question may carry.
    expect([...text].length).toBeLessThanOrEqual(ATTACHED_TEXT)
  })

  test("a document in a cell is its JSON", () => {
    const { text } = resultTable({ columns: ["doc"], rows: [[{ a: 1 }]], rowCount: 1 })
    expect(text).toContain('{"a":1}')
  })
})

describe("what the editor's tab offers to attach", () => {
  test("only what it holds, labelled by kind and count, never by what is in it", () => {
    expect(queryAttachables({ statement: "  " })).toEqual([])
    const offered = queryAttachables({
      statement: "SELECT * FROM customers",
      result,
      failed: { sql: "SELECT * FROM nope", error: 'relation "nope" does not exist' },
      plan: { sql: "SELECT 1", text: "Result  (cost=0.00..0.01 rows=1 width=4)" },
    })
    expect(offered.map((one) => [one.id, one.label])).toEqual([
      ["statement", "statement"],
      ["result", "result · 3 rows"],
      ["error", "error"],
      ["plan", "plan"],
    ])
    expect(offered[1].rows).toBe(3)
    expect(offered[2].text).toContain("SELECT * FROM nope")
    expect(offered[2].text).toContain('relation "nope" does not exist')
    expect(offered[3].text).toContain("Result  (cost=0.00..0.01")
  })

  test("goes to the server within its bounds", () => {
    const sent = boundAttached([
      { label: "statement", text: "SELECT 1" },
      { label: "x".repeat(80), text: "y".repeat(30_000) },
      { label: "empty", text: "   " },
      { label: "c", text: "c" },
      { label: "d", text: "d" },
      { label: "e", text: "e" },
    ])
    expect(sent.map((one) => one.label.length)).toEqual([9, 60, 1, 1])
    expect(sent[1].label.endsWith("…")).toBe(true)
    expect([...sent[1].text].length).toBeLessThanOrEqual(ATTACHED_TEXT)
    expect(sent[1].text.endsWith("…")).toBe(true)
  })
})

describe("a plan as the assistant reads it", () => {
  test("is the engine's JSON, its printed plan, or its table of steps", () => {
    expect(planSource({ plan: [{ Plan: { "Node Type": "Seq Scan" } }], result: {} })).toContain(
      '"Node Type": "Seq Scan"',
    )
    expect(
      planSource({ result: { columns: ["QUERY PLAN"], rows: [["Seq Scan on orders"], [null]] } }),
    ).toBe("Seq Scan on orders\n")
    expect(
      planSource({
        result: {
          columns: ["id", "parent", "detail"],
          rows: [[2, 0, "SCAN orders"]],
        },
      }),
    ).toBe("id\tparent\tdetail\n2\t0\tSCAN orders")
  })
})

describe("a statement in an answer", () => {
  test("is a statement when it is fenced as SQL or as nothing", () => {
    expect(isStatementFence("")).toBe(true)
    expect(isStatementFence("SQL")).toBe(true)
    expect(isStatementFence("postgresql")).toBe(true)
    expect(isStatementFence("json")).toBe(false)
    expect(isStatementFence("bash")).toBe(false)
  })

  test("opens in a tab named for the question it answered", () => {
    expect(statementTitle("count  the\norders")).toBe("count the orders")
    expect(statementTitle("x".repeat(60))).toHaveLength(40)
    expect(statementTitle("")).toBe("From the assistant")
  })
})
