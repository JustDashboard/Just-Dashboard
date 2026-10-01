import { describe, expect, test } from "bun:test"
import { applyChange, buildChanges, EMPTY_CHANGES } from "./change-set"
import { orderColumns } from "./layout"
import {
  editFor,
  insertedAt,
  isPreview,
  lockReason,
  originOf,
  pendingOf,
  previewKeys,
  rowRecord,
  rowValues,
  selectedBlock,
  targetRows,
  tickedRows,
  valueAt,
} from "./model"
import { DEFAULT_VALUE } from "./values"

const COLUMNS = [
  { key: "id", name: "id", typeName: "bigint", kind: "number", primaryKey: true },
  { key: "name", name: "name", typeName: "text", kind: "text", nullable: true },
  { key: "blob", name: "blob", typeName: "bytea", kind: "binary", nullable: true },
  { key: "body", name: "body", typeName: "text", kind: "text" },
  { key: "total", name: "total", typeName: "numeric(10,2)", kind: "number", generated: true },
  { key: "locked", name: "locked", typeName: "text", kind: "text", editable: false },
]
const ROWS = [
  ["1", "small", "\\x8950", "short", "10.00", "a"],
  ["2", "cut", "\\xabab… (4096 bytes)", "x".repeat(40), "20.00", "b"],
  ["3", null, null, "", "30.00", "c"],
]
const CLIPPED = new Map([[1, new Map([[3, 20000]])]])

function build(changes = EMPTY_CHANGES, extra = {}) {
  const sources = extra.sources ?? [0, 1, 2]
  return {
    columns: COLUMNS,
    rows: ROWS,
    ordered: orderColumns(COLUMNS, { order: [], hidden: [], pinned: [] }),
    ids: [...sources.map((i) => ROWS[i][0]), ...changes.inserts.map((row) => row.id)],
    sources: [...sources, ...changes.inserts.map(() => -1)],
    serverCount: sources.length,
    changes,
    clipped: CLIPPED,
    editable: true,
    ...extra.model,
  }
}
const col = (model, key) => model.ordered.find((entry) => entry.column.key === key)
const stage = (changes, model, ...actions) => actions.reduce(applyChange, changes)

describe("what is in a cell", () => {
  test("a row from the server reads as it arrived, with a staged edit laid over it", () => {
    const plain = build()
    expect(valueAt(plain, 0, col(plain, "name"))).toBe("small")
    expect(valueAt(plain, 2, col(plain, "name"))).toBeNull()
    const changes = stage(EMPTY_CHANGES, plain, {
      type: "edit",
      edits: [editFor(plain, 0, col(plain, "name"), "renamed")],
    })
    const edited = build(changes)
    expect(valueAt(edited, 0, col(edited, "name"))).toBe("renamed")
    expect(valueAt(edited, 0, col(edited, "body"))).toBe("short")
    expect(pendingOf(edited, 0)).toBe("updated")
    expect(pendingOf(edited, 1)).toBe("none")
  })

  test("an inserted row follows the server's rows and reads unset columns as undefined", () => {
    const changes = applyChange(EMPTY_CHANGES, {
      type: "insert",
      rows: [{ id: "new:1", values: { name: "fresh" } }],
    })
    const model = build(changes)
    expect(model.ids).toEqual(["1", "2", "3", "new:1"])
    expect(insertedAt(model, 3)?.id).toBe("new:1")
    expect(insertedAt(model, 0)).toBeUndefined()
    expect(valueAt(model, 3, col(model, "name"))).toBe("fresh")
    expect(valueAt(model, 3, col(model, "id"))).toBeUndefined()
    expect(pendingOf(model, 3)).toBe("inserted")
    expect(rowValues(model, 3)).toEqual([null, "fresh", null, null, null, null])
    expect(rowRecord(model, 3).id).toBeUndefined()
  })

  test("a sorted or filtered view still reads each row from where it really is", () => {
    const model = build(EMPTY_CHANGES, { sources: [2, 0] })
    expect(model.ids).toEqual(["3", "1"])
    expect(valueAt(model, 0, col(model, "total"))).toBe("30.00")
    expect(valueAt(model, 1, col(model, "total"))).toBe("10.00")
    expect(originOf(model, 0).values.id).toBe("3")
  })
})

describe("previews", () => {
  test("a cell is a preview when the server lists it or its value carries the cut mark", () => {
    const model = build()
    expect(isPreview(model, 1, 2)).toBe(true)
    expect(isPreview(model, 1, 3)).toBe(true)
    expect(isPreview(model, 0, 2)).toBe(false)
    expect(isPreview(model, 0, 3)).toBe(false)
    expect(previewKeys(model, 1)).toEqual(["blob", "body"])
    expect(previewKeys(model, 0)).toEqual([])
  })

  test("the row as read records which columns were previews, so no key is built from one", () => {
    const model = build()
    expect(originOf(model, 1).clipped).toEqual(["blob", "body"])
    expect(originOf(model, 0).clipped).toBeUndefined()
    const changes = applyChange(EMPTY_CHANGES, {
      type: "edit",
      edits: [editFor(model, 1, col(model, "name"), "edited")],
    })
    const keyless = COLUMNS.map((column) => ({ ...column, primaryKey: false }))
    const { payload } = buildChanges(changes, { table: "t", columns: keyless })
    expect(payload.changes[0].key).toEqual({ id: "2", name: "cut", total: "20.00", locked: "b" })
  })

  test("a row already in the change set is not snapshotted a second time", () => {
    const model = build()
    const first = editFor(model, 0, col(model, "name"), "x")
    expect(first.origin.values.id).toBe("1")
    const next = build(applyChange(EMPTY_CHANGES, { type: "edit", edits: [first] }))
    expect(editFor(next, 0, col(next, "body"), "y").origin).toBeUndefined()
  })
})

describe("what can be edited", () => {
  test("a preview is refused, a whole value is not", () => {
    const model = build()
    expect(lockReason(model, 1, col(model, "blob"))).toMatch(/Only the start of this value/)
    expect(lockReason(model, 1, col(model, "body"))).toMatch(/Only the start of this value/)
    expect(lockReason(model, 0, col(model, "blob"))).toBeNull()
    expect(lockReason(model, 1, col(model, "name"))).toBeNull()
  })

  test("computed and locked columns are refused by name, on new rows too", () => {
    const changes = applyChange(EMPTY_CHANGES, { type: "insert", rows: [{ id: "new:1" }] })
    const model = build(changes)
    expect(lockReason(model, 0, col(model, "total"))).toBe("total is computed by the database")
    expect(lockReason(model, 3, col(model, "total"))).toBe("total is computed by the database")
    expect(lockReason(model, 0, col(model, "locked"))).toBe("locked cannot be edited here")
    expect(lockReason(model, 3, col(model, "name"))).toBeNull()
  })

  test("a row marked for deletion, a read-only grid and a row that is not there are refused", () => {
    const base = build()
    const changes = applyChange(EMPTY_CHANGES, {
      type: "delete",
      rows: [{ rowId: "1", origin: originOf(base, 0) }],
    })
    const model = build(changes)
    expect(lockReason(model, 0, col(model, "name"))).toBe("This row is marked for deletion")
    expect(pendingOf(model, 0)).toBe("deleted")
    const readOnly = build(EMPTY_CHANGES, { model: { editable: false } })
    expect(lockReason(readOnly, 0, col(readOnly, "name"))).toBe("These rows are read-only")
    expect(lockReason(base, 9, col(base, "name"))).toBe("Nothing to edit here")
  })
})

describe("what an action applies to", () => {
  const model = build()
  const at = (row, colIndex) => ({ row, col: colIndex })

  test("a range wins, then the ticked rows, then the active cell alone", () => {
    const range = { active: at(1, 2), anchor: at(0, 1), rows: ["3"] }
    const block = selectedBlock(model, range)
    expect(block.rows).toEqual([0, 1])
    expect(block.cols.map((entry) => entry.column.key)).toEqual(["name", "blob"])

    const ticked = selectedBlock(model, { active: at(0, 0), anchor: null, rows: ["3", "1"] })
    expect(ticked.rows).toEqual([0, 2])
    expect(ticked.cols).toHaveLength(COLUMNS.length)

    const single = selectedBlock(model, { active: at(2, 3), anchor: null, rows: [] })
    expect(single.rows).toEqual([2])
    expect(single.cols.map((entry) => entry.column.key)).toEqual(["body"])

    expect(selectedBlock(model, { active: null, anchor: null, rows: [] })).toBeNull()
    expect(selectedBlock(model, { active: at(-1, 0), anchor: null, rows: [] })).toBeNull()
  })

  test("ticked rows that are not on this page are not acted on", () => {
    expect(tickedRows(model, { active: null, anchor: null, rows: ["elsewhere", "2"] })).toEqual([1])
    expect(
      selectedBlock(model, { active: at(0, 0), anchor: null, rows: ["elsewhere"] }).rows,
    ).toEqual([0])
  })

  test("a row verb takes the ticked rows or the range only when invoked from inside them", () => {
    const ticked = { active: at(0, 0), anchor: null, rows: ["1", "3"] }
    expect(targetRows(model, ticked, 0)).toEqual([0, 2])
    expect(targetRows(model, ticked, 1)).toEqual([1])
    const range = { active: at(2, 1), anchor: at(1, 0), rows: [] }
    expect(targetRows(model, range, 2)).toEqual([1, 2])
    expect(targetRows(model, range, 0)).toEqual([0])
  })

  test("a pending default reads as nothing when the row is copied out", () => {
    const changes = applyChange(EMPTY_CHANGES, {
      type: "edit",
      edits: [editFor(model, 0, col(model, "name"), DEFAULT_VALUE)],
    })
    const edited = build(changes)
    expect(rowValues(edited, 0)[1]).toBeNull()
    expect(rowRecord(edited, 0).name).toEqual({ $default: true })
  })
})
