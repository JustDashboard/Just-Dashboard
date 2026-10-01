import { describe, expect, test } from "bun:test"
import { ApiError } from "@/lib/api"
import {
  cellKey,
  changeSummary,
  guardable,
  inspectRow,
  refusal,
  relaxGuards,
  rowIdentity,
  rowOrigin,
  rowRecord,
} from "./rows"

const COLUMNS = [
  { key: "id", name: "id", typeName: "bigint", kind: "number", primaryKey: true },
  { key: "name", name: "name", typeName: "text", kind: "text", nullable: true },
  { key: "body", name: "body", typeName: "text", kind: "text", nullable: true },
]
const ROWS = [
  ["9007199254740993", "Ann", "short"],
  ["2", null, "x".repeat(10)],
]
const NONE = { inserts: [], updates: {}, deletes: {} }

describe("row identity", () => {
  test("a keyed table is told apart by its key, digit for digit", () => {
    const id = rowIdentity(COLUMNS, true)
    expect(id(ROWS[0])).toBe("9007199254740993")
  })
  test("a composite key joins its parts", () => {
    const id = rowIdentity(
      [
        { key: "a", name: "a", typeName: "int", kind: "number", primaryKey: true },
        { key: "b", name: "b", typeName: "int", kind: "number", primaryKey: true },
      ],
      true,
    )
    expect(id(["1", "2"])).toBe("1\u00002")
    expect(id(["12", ""])).not.toBe(id(["1", "2"]))
  })
  test("no key, or a key that repeats, leaves the grid to count positions", () => {
    expect(rowIdentity(COLUMNS, false)).toBeUndefined()
    expect(
      rowIdentity([{ key: "a", name: "a", typeName: "text", kind: "text" }], true),
    ).toBeUndefined()
  })
})

describe("a row as the inspector reads it", () => {
  test("a clean row is what the server sent, NULL kept as NULL", () => {
    const row = inspectRow(
      { columns: COLUMNS, rows: ROWS, changes: NONE, rowId: rowIdentity(COLUMNS, true) },
      1,
    )
    expect(row.id).toBe("2")
    expect(row.state).toBe("clean")
    expect(row.cells[1].value).toBeNull()
    expect(row.cells[1].edited).toBe(false)
    expect(row.origin.values).toEqual({ id: "2", name: null, body: "xxxxxxxxxx" })
  })

  test("a staged edit stands over what was read, and only where it was made", () => {
    const changes = {
      ...NONE,
      updates: { 2: { original: { id: "2", name: null, body: "x" }, values: { name: "" } } },
    }
    const row = inspectRow(
      { columns: COLUMNS, rows: ROWS, changes, rowId: rowIdentity(COLUMNS, true) },
      1,
    )
    expect(row.state).toBe("updated")
    expect(row.cells[1]).toMatchObject({ value: "", original: null, edited: true })
    expect(row.cells[2].edited).toBe(false)
  })

  test("a cut value is a preview until a whole one is staged over it", () => {
    const clipped = [{ row: 0, column: 2, size: 9000 }]
    const source = {
      columns: COLUMNS,
      rows: ROWS,
      clipped,
      changes: NONE,
      rowId: rowIdentity(COLUMNS, true),
    }
    const cut = inspectRow(source, 0).cells[2]
    expect(cut).toMatchObject({ preview: true, size: 9000 })
    expect(inspectRow(source, 0).origin.clipped).toEqual(["body"])
    const staged = inspectRow(
      {
        ...source,
        changes: {
          ...NONE,
          updates: {
            "9007199254740993": { original: {}, clipped: ["body"], values: { body: "whole" } },
          },
        },
      },
      0,
    ).cells[2]
    expect(staged).toMatchObject({ preview: false, value: "whole", edited: true })
  })

  test("a staged new row follows the page, and an unset column is left to its default", () => {
    const changes = { ...NONE, inserts: [{ id: "new:1", values: { name: "Zed" } }] }
    const row = inspectRow({ columns: COLUMNS, rows: ROWS, changes }, 2)
    expect(row).toMatchObject({ id: "new:1", state: "inserted" })
    expect(row.origin).toBeUndefined()
    expect(row.cells[0].value).toBeUndefined()
    expect(rowRecord(row)).toEqual({ name: "Zed" })
    expect(inspectRow({ columns: COLUMNS, rows: ROWS, changes }, 3)).toBeNull()
  })

  test("a keyless row is known by its position, and a deleted one says so", () => {
    const changes = { ...NONE, deletes: { 0: { original: {} } } }
    const row = inspectRow({ columns: COLUMNS, rows: ROWS, changes }, 0)
    expect(row).toMatchObject({ id: "0", state: "deleted" })
  })
})

describe("the key one whole value is read by", () => {
  test("the primary key when there is one", () => {
    const origin = rowOrigin(COLUMNS, ROWS[0], 0, undefined)
    expect(cellKey(COLUMNS, origin)).toEqual({ id: "9007199254740993" })
  })
  test("otherwise the row's short values, never a preview", () => {
    const columns = COLUMNS.map((column) => ({ ...column, primaryKey: false }))
    const origin = rowOrigin(columns, ["1", "y".repeat(300), "cut"], 0, [
      { row: 0, column: 2, size: 5000 },
    ])
    expect(cellKey(columns, origin)).toEqual({ id: "1" })
  })
})

describe("the change bar's sentence", () => {
  test("says what the set is made of", () => {
    expect(changeSummary({ inserts: 1, updates: 2, deletes: 0, cells: 3, total: 3 })).toBe(
      "3 changes — 2 edited, 1 new",
    )
    expect(changeSummary({ inserts: 0, updates: 0, deletes: 1, cells: 0, total: 1 })).toBe(
      "1 change — 1 deleted",
    )
  })
})

describe("the guard on an edit", () => {
  test("a single-precision float is not compared", () => {
    expect(guardable("float")).toBe(false)
    expect(guardable("FLOAT(7,3) unsigned")).toBe(false)
    expect(guardable("double precision")).toBe(true)
    expect(guardable("numeric(10,2)")).toBe(true)
  })
  test("it is taken out of an update's key and the primary key stays", () => {
    const columns = [...COLUMNS, { key: "w", name: "w", typeName: "float", kind: "number" }]
    const payload = {
      schema: "s",
      table: "t",
      changes: [
        { op: "update", key: { id: "1", w: 1.1, name: "a" }, values: { w: 2, name: "b" } },
        { op: "delete", key: { id: "2" } },
      ],
    }
    expect(relaxGuards(payload, columns).changes[0].key).toEqual({ id: "1", name: "a" })
    expect(relaxGuards(payload, columns).changes[1]).toBe(payload.changes[1])
  })
  test("a keyless table keeps the whole row: there is nothing else to find it by", () => {
    const columns = [{ key: "w", name: "w", typeName: "float", kind: "number" }]
    const payload = {
      schema: "",
      table: "t",
      changes: [{ op: "update", key: { w: 1.5 }, values: { w: 2 } }],
    }
    expect(relaxGuards(payload, columns)).toBe(payload)
  })
})

describe("a refused set", () => {
  const refs = [
    { index: 0, op: "delete", rowId: "7" },
    { index: 1, op: "update", rowId: "9" },
    { index: 2, op: "insert", rowId: "new:1" },
  ]
  test("a conflict names the staged row and says nothing was matched", () => {
    const error = new ApiError(409, "change_conflict", "change 2 matched 0 rows", undefined, {
      field: "changes[1]",
      operation: "update",
      reason: "matched 0 rows",
    })
    expect(refusal(error, refs)).toMatchObject({
      rowId: "9",
      position: 2,
      total: 3,
      op: "update",
      matched: 0,
      conflict: true,
      message: "The edit of this row was refused: the row was changed or deleted after it was read",
    })
  })
  test("a row that matched several says how many", () => {
    const error = new ApiError(409, "change_conflict", "x", undefined, {
      field: "changes[0]",
      reason: "matched 2 rows",
    })
    expect(refusal(error, refs).message).toBe(
      "The delete of this row was refused: it matches 2 rows, and a change has to match exactly one",
    )
  })
  test("the engine's own refusal is kept in its words", () => {
    const error = new ApiError(
      400,
      "change_failed",
      "duplicate key value violates unique constraint",
      undefined,
      {
        field: "changes[2]",
      },
    )
    expect(refusal(error, refs)).toMatchObject({
      rowId: "new:1",
      conflict: false,
      message: "duplicate key value violates unique constraint",
    })
  })
  test("anything else is just what went wrong", () => {
    expect(refusal(new Error("network"), refs)).toEqual({
      total: 3,
      conflict: false,
      message: "network",
    })
  })
})
