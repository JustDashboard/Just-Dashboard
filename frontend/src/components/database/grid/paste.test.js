import { describe, expect, test } from "bun:test"
import { applyChange, changeCounts, EMPTY_CHANGES } from "./change-set"
import { orderColumns } from "./layout"
import { clipCells, planPaste } from "./paste"

const COLUMNS = [
  { key: "id", name: "id", typeName: "bigint", kind: "number", primaryKey: true },
  { key: "name", name: "name", typeName: "text", kind: "text", nullable: true },
  { key: "city", name: "city", typeName: "text", kind: "text" },
  { key: "qty", name: "qty", typeName: "integer", kind: "number", nullable: true },
  { key: "total", name: "total", typeName: "numeric(10,2)", kind: "number", generated: true },
]
const ROWS = [
  ["1", "ann", "Berlin", "3", "9.00"],
  ["2", "bo", "Paris", null, "0.00"],
  ["3", "cy", "", "7", "21.00"],
]

function model(changes = EMPTY_CHANGES) {
  return {
    columns: COLUMNS,
    rows: ROWS,
    ordered: orderColumns(COLUMNS, { order: [], hidden: [], pinned: [] }),
    ids: [...ROWS.map((row) => row[0]), ...changes.inserts.map((row) => row.id)],
    sources: [...ROWS.map((_, i) => i), ...changes.inserts.map(() => -1)],
    serverCount: ROWS.length,
    changes,
    clipped: new Map(),
    editable: true,
    defaultOnUpdate: true,
    keys: [0],
  }
}

const at = (row, col) => ({ row, col })
const cell = (row, col) => ({ active: at(row, col), anchor: null, rows: [] })
const text = (...rows) => rows.map((row) => row.map((field) => ({ text: field })))
const ids = () => {
  let n = 0
  return () => `new:${++n}`
}
const allow = () => ({ canInsert: true, newRowId: ids() })
const stage = (plan) => plan.actions.reduce(applyChange, EMPTY_CHANGES)

describe("pasting a block", () => {
  test("it is laid down from the active cell, each field read by its column", () => {
    const plan = planPaste(
      model(),
      cell(0, 1),
      text(["anna", "Bonn", "12"], ["bob", "Rome", ""]),
      allow(),
    )
    expect(plan).toMatchObject({
      pasted: 6,
      skipped: 0,
      block: { top: 0, left: 1, bottom: 1, right: 3 },
    })
    const changes = stage(plan)
    expect(changes.updates["1"].values).toEqual({ name: "anna", city: "Bonn", qty: "12" })
    // An empty field is NULL where the column allows it; `qty` was already NULL, so it is no edit.
    expect(changes.updates["2"].values).toEqual({ name: "bob", city: "Rome" })
  })

  test("an empty field is an empty string where NULL is not allowed", () => {
    const changes = stage(planPaste(model(), cell(0, 2), text([""]), allow()))
    expect(changes.updates["1"].values).toEqual({ city: "" })
  })

  test("a field its column cannot take is skipped, counted and explained; the rest still land", () => {
    const plan = planPaste(model(), cell(0, 2), text(["Oslo", "many"], ["Riga", "4"]), allow())
    expect(plan).toMatchObject({ pasted: 3, skipped: 1, reason: "qty: Whole numbers only" })
    const changes = stage(plan)
    expect(changes.updates["1"].values).toEqual({ city: "Oslo" })
    expect(changes.updates["2"].values).toEqual({ city: "Riga", qty: "4" })
  })

  test("a computed column and columns past the last are refused", () => {
    const plan = planPaste(model(), cell(0, 3), text(["5", "99.00", "extra"]), allow())
    expect(plan.pasted).toBe(1)
    expect(plan.skipped).toBe(2)
    expect(plan.reason).toBe("total is computed by the database")
    expect(plan.block).toEqual({ top: 0, left: 3, bottom: 0, right: 3 })
  })

  test("rows that run past the end become new rows, in one batch with the edits", () => {
    const plan = planPaste(
      model(),
      cell(2, 1),
      text(["cyd", "Kyiv"], ["dee", "Lviv"], ["eli", "Odesa"]),
      allow(),
    )
    expect(plan.actions.map((action) => action.type)).toEqual(["insert", "edit"])
    expect(plan.block).toEqual({ top: 2, left: 1, bottom: 4, right: 2 })
    const changes = stage(plan)
    expect(changeCounts(changes)).toMatchObject({ inserts: 2, updates: 1 })
    expect(changes.inserts.map((row) => row.values)).toEqual([
      { name: "dee", city: "Lviv" },
      { name: "eli", city: "Odesa" },
    ])
  })

  test("without leave to add rows, what runs past the end is refused and nothing is inserted", () => {
    const plan = planPaste(model(), cell(2, 1), text(["cyd"], ["dee"]), {
      canInsert: false,
      newRowId: ids(),
    })
    expect(plan).toMatchObject({ pasted: 1, skipped: 1, reason: "Rows cannot be added here" })
    expect(plan.block).toEqual({ top: 2, left: 1, bottom: 2, right: 1 })
    expect(stage(plan).inserts).toEqual([])
  })

  test("pasting into a staged row edits that row rather than adding another", () => {
    const withRow = applyChange(EMPTY_CHANGES, { type: "insert", rows: [{ id: "new:9" }] })
    const plan = planPaste(model(withRow), cell(3, 1), text(["zed", "Bern"]), allow())
    const changes = plan.actions.reduce(applyChange, withRow)
    expect(changes.inserts).toEqual([{ id: "new:9", values: { name: "zed", city: "Bern" } }])
  })

  test("a ragged block pastes what each line has", () => {
    const plan = planPaste(model(), cell(0, 1), text(["a", "b", "1"], ["c"]), allow())
    expect(plan.pasted).toBe(4)
    expect(plan.block).toEqual({ top: 0, left: 1, bottom: 1, right: 3 })
  })
})

describe("pasting one value", () => {
  test("over a range it fills the range, and the range stays selected", () => {
    const range = { active: at(2, 2), anchor: at(0, 1), rows: [] }
    const plan = planPaste(model(), range, text(["x"]), allow())
    expect(plan).toMatchObject({ pasted: 6, skipped: 0, block: null })
    const changes = stage(plan)
    expect(changes.updates["3"].values).toEqual({ name: "x", city: "x" })
  })

  test("over a single cell it is a block of one", () => {
    const plan = planPaste(model(), cell(1, 3), text(["42"]), allow())
    expect(plan.block).toEqual({ top: 1, left: 3, bottom: 1, right: 3 })
    expect(stage(plan).updates["2"].values).toEqual({ qty: "42" })
  })

  test("a fill skips the cells that cannot take the value", () => {
    const range = { active: at(1, 4), anchor: at(0, 3), rows: [] }
    const plan = planPaste(model(), range, text(["8"]), allow())
    expect(plan).toMatchObject({ pasted: 2, skipped: 2 })
  })
})

describe("pasting from another grid", () => {
  const exact = (...rows) => rows.map((row) => row.map((value) => ({ value })))

  test("NULL stays NULL and the empty string stays empty, in the same column", () => {
    const plan = planPaste(model(), cell(0, 1), exact([null], [""]), allow())
    const changes = stage(plan)
    expect(changes.updates["1"].values).toEqual({ name: null })
    expect(changes.updates["2"].values).toEqual({ name: "" })
  })

  test("NULL is refused where the column cannot hold it", () => {
    const plan = planPaste(model(), cell(0, 2), exact([null]), allow())
    expect(plan).toMatchObject({
      pasted: 0,
      skipped: 1,
      reason: "city: cannot be NULL",
      block: null,
    })
    expect(plan.actions).toEqual([])
  })

  test("a value of another type is read as its text by the column it lands in", () => {
    const changes = stage(planPaste(model(), cell(0, 1), exact([12, true]), allow()))
    expect(changes.updates["1"].values).toEqual({ name: "12", city: "true" })
    const bad = planPaste(model(), cell(0, 3), exact([{ a: 1 }]), allow())
    expect(bad.skipped).toBe(1)
  })
})

describe("pasting what was only a preview", () => {
  test("a cell the other grid held the start of is refused, and its neighbours still land", () => {
    const clip = { cells: [["first 4096 bytes", "Lyon"]], previews: [{ row: 0, column: 0 }] }
    const plan = planPaste(model(), cell(0, 1), clipCells(clip), allow())
    expect(plan.pasted).toBe(1)
    expect(plan.skipped).toBe(1)
    expect(plan.reason).toBe("name: only the start of the copied value was loaded")
    expect(stage(plan).updates["1"].values).toEqual({ city: "Lyon" })
  })

  test("a block with no previews is its values, NULL included", () => {
    expect(clipCells({ cells: [[null, "a"]], previews: [] })).toEqual([
      [{ value: null }, { value: "a" }],
    ])
  })
})

describe("when there is nowhere to paste", () => {
  test("no active cell, the header, or an empty clipboard is no plan at all", () => {
    expect(
      planPaste(model(), { active: null, anchor: null, rows: [] }, text(["x"]), allow()),
    ).toBeNull()
    expect(planPaste(model(), cell(-1, 1), text(["x"]), allow())).toBeNull()
    expect(planPaste(model(), cell(0, 1), [], allow())).toBeNull()
  })

  test("a read-only grid refuses every cell and stages nothing", () => {
    const readOnly = { ...model(), editable: false }
    const plan = planPaste(readOnly, cell(0, 1), text(["x", "y"]), allow())
    expect(plan).toMatchObject({ pasted: 0, skipped: 2, reason: "These rows are read-only" })
    expect(plan.actions).toEqual([])
  })
})
