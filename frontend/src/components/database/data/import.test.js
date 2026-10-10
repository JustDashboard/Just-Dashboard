import { describe, expect, test } from "bun:test"
import { newColumns, tableNameFrom, uploadProgress, upsertKeys } from "./import"

describe("what an upsert can match rows by", () => {
  const index = (name, columns, more = {}) => ({
    name,
    columns,
    unique: true,
    primary: false,
    ...more,
  })
  test("the primary key first, then each unique index and constraint", () => {
    const keys = upsertKeys({
      primaryKey: ["id"],
      indexes: [
        index("customers_pkey", ["id"], { primary: true }),
        index("customers_email_key", ["email"]),
        index("customers_name_idx", ["full_name"], { unique: false }),
      ],
      constraints: [{ name: "one_slug", type: "unique", columns: ["tenant", "slug"] }],
    })
    expect(keys).toEqual([
      { label: "Primary key", columns: ["id"], primary: true },
      { label: "customers_email_key", columns: ["email"], primary: false },
      { label: "one_slug", columns: ["tenant", "slug"], primary: false },
    ])
  })
  test("a key is listed once, whatever says it", () => {
    const keys = upsertKeys({
      primaryKey: [],
      indexes: [index("u_email", ["email"])],
      constraints: [{ name: "u_email", type: "unique", columns: ["email"] }],
    })
    expect(keys).toHaveLength(1)
  })
  test("an index over an expression, over some rows only, or not valid is no key", () => {
    const keys = upsertKeys({
      primaryKey: [],
      indexes: [
        index("lower_email", ["email"], { expression: true }),
        index("live_email", ["email"], { predicate: "deleted_at IS NULL" }),
        index("broken", ["code"], { invalid: true }),
      ],
      constraints: [{ name: "positive", type: "check", columns: ["qty"] }],
    })
    expect(keys).toEqual([])
  })
})

describe("a table's name from a file's", () => {
  test("the extension goes, and what needs quoting is written plainly", () => {
    expect(tableNameFrom("Orders 2026-Q1.csv")).toBe("orders_2026_q1")
    expect(tableNameFrom("clienți.ndjson")).toBe("clienti")
    expect(tableNameFrom("a.b.tsv")).toBe("a_b")
  })
  test("a name cannot start with a digit, be empty, or run past what engines take", () => {
    expect(tableNameFrom("2026.csv")).toBe("t_2026")
    expect(tableNameFrom("???.csv")).toBe("imported")
    expect(tableNameFrom(`${"x".repeat(90)}.csv`)).toHaveLength(63)
  })
})

describe("the columns of a new table the reader has a say in", () => {
  const columns = [
    { source: "Id", target: "id" },
    { source: "Name", target: "full_name" },
    { source: "Note", target: "" },
  ]
  test("a key column is named as the table names it, NOT NULL, and its type left to the server", () => {
    expect(newColumns(columns, { Id: { key: true } })).toEqual([
      { name: "id", type: "", notNull: true, primaryKey: true },
    ])
  })
  test("a typed type is sent; an untouched or emptied one is not", () => {
    expect(newColumns(columns, { Name: { type: " varchar(80) " }, Id: { type: "  " } })).toEqual([
      { name: "full_name", type: "varchar(80)", notNull: false, primaryKey: false },
    ])
  })
  test("a column left out of the import is not described", () => {
    expect(newColumns(columns, { Note: { key: true, type: "text" } })).toEqual([])
  })
})

describe("how far an upload has got", () => {
  test("before the browser has said anything", () => {
    expect(uploadProgress(0, 0)).toEqual({ percent: 0, words: "Sending the file…", sending: true })
  })
  test("on the way", () => {
    const progress = uploadProgress(512 * 1024, 2 * 1024 * 1024)
    expect(progress.percent).toBe(25)
    expect(progress.sending).toBe(true)
    expect(progress.words).toContain("25%")
  })
  test("sent is not done: the rows are still being written", () => {
    const progress = uploadProgress(2048, 2048)
    expect(progress.percent).toBe(100)
    expect(progress.sending).toBe(false)
    expect(progress.words).toContain("being written")
  })
})
