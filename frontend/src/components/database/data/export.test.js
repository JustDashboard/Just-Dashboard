import { describe, expect, test } from "bun:test"
import { exportEnd, exportMark, exportReport, exportStateEnd } from "./export"

describe("the closing mark of an exported file", () => {
  test("SQL always says how it went", () => {
    expect(exportMark("sql", "INSERT INTO t VALUES (1);\n-- export complete: 1204 rows\n")).toEqual(
      {
        status: "complete",
        rows: 1204,
      },
    )
    expect(
      exportMark("sql", "…;\n-- export truncated at 100000 rows: the limit was reached"),
    ).toEqual({
      status: "truncated",
      rows: 100000,
    })
    expect(exportMark("sql", "…;\n-- export failed after 3999 rows: connection reset")).toEqual({
      status: "failed",
      rows: 3999,
      error: "connection reset",
    })
  })
  test("a SQL file with no closing line was cut off", () => {
    expect(exportMark("sql", "INSERT INTO t VALUES (1);\nINSERT INTO t VAL").status).toBe("failed")
  })
  test("JSON is marked only when it is not complete", () => {
    expect(exportMark("json", '[{"id":1},{"id":2}]')).toEqual({ status: "complete" })
    expect(exportMark("json", '[{"id":1},{"__export":{"rows":2,"status":"truncated"}}]\n')).toEqual(
      {
        status: "truncated",
        rows: 2,
        error: undefined,
      },
    )
    expect(
      exportMark(
        "ndjson",
        '{"id":1}\n{"__export":{"error":"gone away","rows":3999,"status":"failed"}}\n',
      ),
    ).toEqual({ status: "failed", rows: 3999, error: "gone away" })
  })
  test("a row that merely mentions the word is not a mark", () => {
    expect(exportMark("ndjson", '{"note":"see __export"}\n')).toEqual({ status: "complete" })
  })
  test("CSV and TSV have nowhere to say it", () => {
    expect(exportMark("csv", "id,name\n1,a\n")).toBeNull()
    expect(exportMark("tsv", "id\tname\n")).toBeNull()
  })
})

describe("which sign is believed", () => {
  const done = { status: "truncated", rows: 100000, format: "csv", startedAt: "" }
  test("the server's record first", () => {
    expect(exportEnd(done, "csv", "")).toEqual({
      status: "truncated",
      rows: 100000,
      error: undefined,
    })
  })
  test("then the mark in the file", () => {
    expect(exportEnd(null, "sql", "-- export complete: 5 rows")).toEqual({
      status: "complete",
      rows: 5,
    })
  })
  test("a record still running is not an ending", () => {
    expect(exportStateEnd({ status: "running", rows: 10, format: "csv", startedAt: "" })).toBeNull()
  })
  test("a CSV with neither is not known, and is not called complete", () => {
    expect(exportEnd(null, "csv", "a,b\n")).toEqual({ status: "unknown" })
  })
})

describe("what the reader is told", () => {
  test("a whole export says how many rows", () => {
    expect(exportReport({ status: "complete", rows: 1204 }, "customers", 100000)).toEqual({
      tone: "success",
      title: "Exported 1,204 rows from customers",
    })
  })
  test("a truncated one says it is not the whole table, and why", () => {
    const report = exportReport({ status: "truncated", rows: 100000 }, "events", 100000)
    expect(report.tone).toBe("warning")
    expect(report.title).toBe("The export of events is not the whole table")
    expect(report.description).toContain("100,000-row limit")
  })
  test("a failed one says so and that nothing was saved", () => {
    const report = exportReport(
      { status: "failed", rows: 3999, error: "connection reset" },
      "events",
      100000,
    )
    expect(report.tone).toBe("danger")
    expect(report.description).toBe("connection reset — after 3,999 rows. No file was saved.")
  })
  test("an ending nobody recorded is said as that", () => {
    expect(exportReport({ status: "unknown" }, "t", 100000).tone).toBe("warning")
  })
})
