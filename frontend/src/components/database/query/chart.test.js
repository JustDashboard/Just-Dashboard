import { describe, expect, test } from "bun:test"
import { chartOf } from "./chart"

const result = (columns, kinds, rows) => ({
  columns,
  types: columns.map(() => ""),
  kinds,
  rows,
  rowCount: rows.length,
  rowsAffected: 0,
  duration: "1ms",
  truncated: false,
  statement: "",
})

describe("whether a result can be drawn", () => {
  test("instants beside numbers are lines over time, in time's order", () => {
    const read = chartOf(
      result(
        ["day", "orders", "revenue"],
        ["date", "integer", "decimal"],
        [
          ["2026-10-02", "12", "40.50"],
          ["2026-10-01", "9", "31.25"],
        ],
      ),
    )
    expect(read.kind).toBe("series")
    expect(read.time).toBe("day")
    expect(read.series).toEqual([
      { key: "c1", label: "orders" },
      { key: "c2", label: "revenue" },
    ])
    expect(read.rows).toEqual([
      { ts: Date.UTC(2026, 9, 1), c1: 9, c2: 31.25 },
      { ts: Date.UTC(2026, 9, 2), c1: 12, c2: 40.5 },
    ])
  })
  test("a timestamp is read with its zone, and without one as UTC", () => {
    const read = chartOf(
      result(
        ["at", "n"],
        ["datetime", "integer"],
        [
          ["2026-10-01T10:00:00Z", "1"],
          ["2026-10-01 12:00:00", "2"],
          ["2026-10-01T15:00:00+02:00", "3"],
        ],
      ),
    )
    expect(read.rows.map((row) => new Date(row.ts).toISOString())).toEqual([
      "2026-10-01T10:00:00.000Z",
      "2026-10-01T12:00:00.000Z",
      "2026-10-01T13:00:00.000Z",
    ])
  })
  test("a NULL is a gap in its series, not a zero", () => {
    const read = chartOf(
      result(
        ["at", "n"],
        ["datetime", "integer"],
        [
          ["2026-10-01T10:00:00Z", null],
          ["2026-10-01T11:00:00Z", "2"],
        ],
      ),
    )
    expect(read.rows[0]).toEqual({ ts: Date.UTC(2026, 9, 1, 10) })
  })
  test("no more series than the palette has colours", () => {
    const columns = ["at", "a", "b", "c", "d", "e", "f"]
    const kinds = ["datetime", ...Array(6).fill("integer")]
    const rows = [
      ["2026-10-01T10:00:00Z", 1, 2, 3, 4, 5, 6],
      ["2026-10-01T11:00:00Z", 1, 2, 3, 4, 5, 6],
    ]
    expect(chartOf(result(columns, kinds, rows)).series).toHaveLength(5)
  })
  test("a name beside a number is a ranking, in the result's own order", () => {
    const read = chartOf(
      result(
        ["table", "bytes", "rows"],
        ["text", "integer", "integer"],
        [
          ["orders", "9000", "3"],
          [null, "10", "1"],
        ],
      ),
    )
    expect(read).toEqual({
      kind: "bars",
      label: "table",
      value: "bytes",
      items: [
        { label: "orders", value: 9000 },
        { label: "NULL", value: 10 },
      ],
      more: 0,
    })
  })
  test("a column of digits the server calls text is a name, not a number", () => {
    const read = chartOf(result(["zip", "n"], ["text", "integer"], [["01234", "5"]]))
    expect(read.kind).toBe("bars")
    expect(read.items[0].label).toBe("01234")
  })
  test("a column the server has no word for is read from its values", () => {
    const read = chartOf(
      result(["at", "n"], undefined, [
        ["2026-10-01", "1.5"],
        ["2026-10-02", "2"],
      ]),
    )
    expect(read.kind).toBe("series")
  })
  test("what cannot be drawn says why", () => {
    expect(chartOf(undefined).kind).toBe("none")
    expect(chartOf(result(["a"], ["text"], [])).reason).toBe("The statement returned no rows.")
    expect(chartOf(result(["a", "b"], ["text", "text"], [["x", "y"]])).reason).toContain(
      "Nothing here is a number",
    )
    expect(chartOf(result(["n"], ["integer"], [["1"], ["2"]])).reason).toContain(
      "a column of instants or of names",
    )
    expect(
      chartOf(result(["at", "n"], ["datetime", "integer"], [["2026-10-01T10:00:00Z", "1"]])).reason,
    ).toContain("at least two rows")
  })
  test("a true-or-false column is not a number", () => {
    expect(chartOf(result(["name", "ok"], ["text", "boolean"], [["a", true]])).kind).toBe("none")
  })
})
