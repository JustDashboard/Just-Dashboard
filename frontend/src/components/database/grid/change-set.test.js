import { describe, expect, test } from "bun:test"
import {
  applyChange,
  buildChanges,
  changeCounts,
  changeSetReducer,
  duplicateValues,
  EMPTY_CHANGE_STATE,
  EMPTY_CHANGES,
  exceedsLimit,
  HISTORY_LIMIT,
  isEmpty,
  isNewRowId,
  keyProblem,
  MAX_CHANGES,
  rowKey,
  scopedState,
} from "./change-set"
import { DEFAULT_VALUE } from "./values"

const COLUMNS = [
  { key: "id", name: "id", kind: "number", primaryKey: true },
  { key: "email", name: "email", kind: "text" },
  { key: "balance", name: "balance", kind: "number" },
  { key: "active", name: "active", kind: "boolean" },
  { key: "note", name: "note", kind: "text", nullable: true },
]

const ANN = { id: "1", email: "ann@example.com", balance: "4193.40", active: true, note: null }
const BO = { id: "2", email: "bo@example.com", balance: "0", active: false, note: "" }

const origin = (values, clipped) => ({ values, clipped })
const edit = (rowId, column, value, values = ANN, kind) => ({
  rowId,
  column,
  value,
  kind: kind ?? COLUMNS.find((c) => c.key === column)?.kind ?? "text",
  origin: origin(values),
})

function run(...actions) {
  return actions.reduce(changeSetReducer, EMPTY_CHANGE_STATE)
}

describe("editing a cell", () => {
  test("the first edit to a row keeps the row as it was read beside the new value", () => {
    const { present } = run({ type: "edit", edits: [edit("1", "email", "ann@new.example")] })
    expect(present.updates["1"]).toEqual({
      original: ANN,
      clipped: undefined,
      values: { email: "ann@new.example" },
    })
  })

  test("a second edit to the same row adds a column and does not replace the original", () => {
    const later = { ...ANN, email: "someone else's read" }
    const { present } = run(
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
      { type: "edit", edits: [edit("1", "note", "hello", later)] },
    )
    expect(present.updates["1"].original).toBe(ANN)
    expect(present.updates["1"].values).toEqual({ email: "a@b.c", note: "hello" })
  })

  test("an edit back to the original value removes the cell, and the row with its last cell", () => {
    const state = run(
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
      { type: "edit", edits: [edit("1", "note", "x")] },
      { type: "edit", edits: [edit("1", "email", "ann@example.com")] },
    )
    expect(state.present.updates["1"].values).toEqual({ note: "x" })
    const cleared = changeSetReducer(state, { type: "edit", edits: [edit("1", "note", null)] })
    expect(isEmpty(cleared.present)).toBe(true)
  })

  test("a number spelled differently is the same number, so it is not an edit", () => {
    const state = run({ type: "edit", edits: [edit("1", "balance", "4193.4")] })
    expect(state).toBe(EMPTY_CHANGE_STATE)
    expect(run({ type: "edit", edits: [edit("1", "active", "t")] })).toBe(EMPTY_CHANGE_STATE)
  })

  test("NULL and the empty string are different values in both directions", () => {
    const toEmpty = run({ type: "edit", edits: [edit("1", "note", "")] })
    expect(toEmpty.present.updates["1"].values).toEqual({ note: "" })
    const toNull = run({ type: "edit", edits: [edit("2", "note", null, BO)] })
    expect(toNull.present.updates["2"].values).toEqual({ note: null })
  })

  test("a 64-bit integer stays the string it arrived as", () => {
    const big = { ...ANN, id: "9223372036854775806" }
    const { present } = run({
      type: "edit",
      edits: [edit("9223372036854775806", "balance", "9223372036854775807", big)],
    })
    const row = present.updates["9223372036854775806"]
    expect(row.values.balance).toBe("9223372036854775807")
    expect(typeof row.original.id).toBe("string")
  })

  test("the default marker is a value of its own and never collapses", () => {
    const { present } = run({ type: "edit", edits: [edit("1", "note", DEFAULT_VALUE)] })
    expect(present.updates["1"].values.note).toEqual({ $default: true })
  })

  test("an edit with no original row to stand on is ignored", () => {
    const state = run({
      type: "edit",
      edits: [{ rowId: "7", column: "email", value: "x", kind: "text" }],
    })
    expect(state).toBe(EMPTY_CHANGE_STATE)
  })

  test("a deleted row is not edited", () => {
    const state = run(
      { type: "delete", rows: [{ rowId: "1", origin: origin(ANN) }] },
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
    )
    expect(state.present.updates).toEqual({})
    expect(state.past).toHaveLength(1)
  })
})

describe("inserting rows", () => {
  test("a blank row holds no values, so every column takes its default", () => {
    const { present } = run({ type: "insert", rows: [{ id: "new:1" }] })
    expect(present.inserts).toEqual([{ id: "new:1", values: {} }])
    expect(isNewRowId("new:1")).toBe(true)
    expect(isNewRowId("1")).toBe(false)
  })

  test("editing an inserted row sets the value outright, with nothing to collapse against", () => {
    const { present } = run(
      { type: "insert", rows: [{ id: "new:1" }] },
      { type: "edit", edits: [{ rowId: "new:1", column: "email", value: "", kind: "text" }] },
      { type: "edit", edits: [{ rowId: "new:1", column: "note", value: null, kind: "text" }] },
    )
    expect(present.inserts[0].values).toEqual({ email: "", note: null })
    expect(present.updates).toEqual({})
  })

  test("default in a new row is the absence of a value", () => {
    const { present } = run(
      { type: "insert", rows: [{ id: "new:1", values: { email: "a@b.c", note: DEFAULT_VALUE } }] },
      {
        type: "edit",
        edits: [{ rowId: "new:1", column: "email", value: DEFAULT_VALUE, kind: "text" }],
      },
    )
    expect(present.inserts[0].values).toEqual({})
  })

  test("the same temporary id is not inserted twice", () => {
    const state = run(
      { type: "insert", rows: [{ id: "new:1" }] },
      { type: "insert", rows: [{ id: "new:1", values: { email: "x" } }] },
    )
    expect(state.present.inserts).toHaveLength(1)
    expect(state.past).toHaveLength(1)
  })

  test("a duplicate carries NULL and the empty string, and leaves out the key, computed columns and previews", () => {
    const columns = [
      ...COLUMNS,
      { key: "total", name: "total", generated: true },
      { key: "blob", name: "blob" },
    ]
    const values = { ...BO, total: "5", blob: "\\x00ff… (9000 bytes)", note: null, email: "" }
    expect(duplicateValues(columns, values, ["blob"])).toEqual({
      email: "",
      balance: "0",
      active: false,
      note: null,
    })
  })
})

describe("deleting rows", () => {
  test("a server row is marked with the row as it was read", () => {
    const { present } = run({ type: "delete", rows: [{ rowId: "2", origin: origin(BO) }] })
    expect(present.deletes["2"]).toEqual({ original: BO, clipped: undefined })
  })

  test("deleting an edited row drops its edits and keeps the first original", () => {
    const { present } = run(
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
      { type: "delete", rows: [{ rowId: "1", origin: origin({ ...ANN, email: "a@b.c" }) }] },
    )
    expect(present.updates).toEqual({})
    expect(present.deletes["1"].original).toBe(ANN)
  })

  test("deleting an inserted row removes it, since there is nothing on the server to delete", () => {
    const { present } = run(
      { type: "insert", rows: [{ id: "new:1" }, { id: "new:2" }] },
      { type: "delete", rows: [{ rowId: "new:1" }] },
    )
    expect(present.inserts.map((r) => r.id)).toEqual(["new:2"])
    expect(present.deletes).toEqual({})
  })

  test("deleting the same row twice is one delete and one step of history", () => {
    const state = run(
      { type: "delete", rows: [{ rowId: "1", origin: origin(ANN) }] },
      { type: "delete", rows: [{ rowId: "1", origin: origin(ANN) }] },
    )
    expect(Object.keys(state.present.deletes)).toEqual(["1"])
    expect(state.past).toHaveLength(1)
  })
})

describe("reverting", () => {
  test("a cell goes back alone; the row leaves with its last cell", () => {
    const state = run(
      { type: "edit", edits: [edit("1", "email", "a@b.c"), edit("1", "note", "x")] },
      { type: "revertCell", rowId: "1", column: "email" },
    )
    expect(state.present.updates["1"].values).toEqual({ note: "x" })
    const done = changeSetReducer(state, { type: "revertCell", rowId: "1", column: "note" })
    expect(isEmpty(done.present)).toBe(true)
  })

  test("a row goes back whole: its edits, its delete mark, or the inserted row itself", () => {
    const state = run(
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
      { type: "delete", rows: [{ rowId: "2", origin: origin(BO) }] },
      { type: "insert", rows: [{ id: "new:1" }] },
      { type: "revertRow", rowId: "1" },
      { type: "revertRow", rowId: "2" },
      { type: "revertRow", rowId: "new:1" },
    )
    expect(isEmpty(state.present)).toBe(true)
  })

  test("reverting something untouched changes nothing and costs no history", () => {
    const state = run({ type: "edit", edits: [edit("1", "email", "a@b.c")] })
    expect(changeSetReducer(state, { type: "revertRow", rowId: "9" })).toBe(state)
    expect(changeSetReducer(state, { type: "revertCell", rowId: "1", column: "note" })).toBe(state)
  })

  test("discard empties the set, and can itself be undone", () => {
    const state = run(
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
      { type: "insert", rows: [{ id: "new:1" }] },
      { type: "discard" },
    )
    expect(state.present).toBe(EMPTY_CHANGES)
    expect(changeSetReducer(state, { type: "discard" })).toBe(state)
    const back = changeSetReducer(state, { type: "undo" })
    expect(changeCounts(back.present).total).toBe(2)
  })
})

describe("undo and redo", () => {
  test("each change is one step back and one step forward again", () => {
    const edited = run(
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
      { type: "edit", edits: [edit("1", "note", "x")] },
    )
    const undone = changeSetReducer(edited, { type: "undo" })
    expect(undone.present.updates["1"].values).toEqual({ email: "a@b.c" })
    const twice = changeSetReducer(undone, { type: "undo" })
    expect(twice.present).toBe(EMPTY_CHANGES)
    expect(changeSetReducer(twice, { type: "undo" })).toBe(twice)
    const redone = changeSetReducer(changeSetReducer(twice, { type: "redo" }), { type: "redo" })
    expect(redone.present).toBe(edited.present)
    expect(changeSetReducer(redone, { type: "redo" })).toBe(redone)
  })

  test("a new change after an undo forgets what could have been redone", () => {
    const state = run(
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
      { type: "undo" },
      { type: "edit", edits: [edit("1", "note", "x")] },
    )
    expect(state.future).toEqual([])
    expect(state.present.updates["1"].values).toEqual({ note: "x" })
  })

  test("a batch is one step however much it holds", () => {
    const state = run({
      type: "batch",
      actions: [
        { type: "insert", rows: [{ id: "new:1" }] },
        {
          type: "edit",
          edits: [{ rowId: "new:1", column: "email", value: "p@q.r", kind: "text" }],
        },
        { type: "edit", edits: [edit("1", "note", "pasted"), edit("2", "note", "pasted", BO)] },
      ],
    })
    expect(state.past).toHaveLength(1)
    expect(changeCounts(state.present)).toEqual({
      inserts: 1,
      updates: 2,
      deletes: 0,
      cells: 2,
      total: 3,
    })
    expect(changeSetReducer(state, { type: "undo" }).present).toBe(EMPTY_CHANGES)
  })

  test("history is bounded, and reset forgets all of it", () => {
    let state = EMPTY_CHANGE_STATE
    for (let i = 0; i < HISTORY_LIMIT + 25; i++) {
      state = changeSetReducer(state, { type: "edit", edits: [edit("1", "note", `v${i}`)] })
    }
    expect(state.past).toHaveLength(HISTORY_LIMIT)
    expect(changeSetReducer(state, { type: "reset" })).toEqual(EMPTY_CHANGE_STATE)
    expect(changeSetReducer(EMPTY_CHANGE_STATE, { type: "reset" })).toBe(EMPTY_CHANGE_STATE)
  })

  test("applyChange alone has no history to move through", () => {
    const changes = applyChange(EMPTY_CHANGES, { type: "edit", edits: [edit("1", "note", "x")] })
    expect(applyChange(changes, { type: "undo" })).toBe(changes)
  })
})

describe("the table a set was staged for", () => {
  const customers = scopedState(EMPTY_CHANGE_STATE, "1.public.customers")
  const staged = [
    { type: "edit", edits: [edit("1", "email", "a@b.c")] },
    { type: "delete", rows: [{ rowId: "2", origin: origin(BO) }] },
  ].reduce(changeSetReducer, customers)

  test("every step of the reducer keeps the scope the state was staged under", () => {
    expect(staged.scope).toBe("1.public.customers")
    const steps = [
      { type: "undo" },
      { type: "redo" },
      { type: "discard" },
      { type: "insert", rows: [{ id: "new:1" }] },
      { type: "reset" },
    ]
    let state = staged
    for (const action of steps) {
      state = changeSetReducer(state, action)
      expect(state.scope).toBe("1.public.customers")
    }
  })

  test("the same scope reads the state as it is", () => {
    expect(scopedState(staged, "1.public.customers")).toBe(staged)
  })

  test("another table reads it as empty, with no history to undo into", () => {
    const orders = scopedState(staged, "1.public.orders")
    expect(isEmpty(orders.present)).toBe(true)
    expect(orders.past).toEqual([])
    expect(orders.scope).toBe("1.public.orders")
    // Ctrl+Z in the next table has nothing of the last one to bring back.
    expect(changeSetReducer(orders, { type: "undo" })).toBe(orders)
  })

  test("a discard that was undoable in its own table is not undoable from another", () => {
    const discarded = changeSetReducer(staged, { type: "discard" })
    expect(changeSetReducer(discarded, { type: "undo" }).present).toBe(staged.present)
    const orders = scopedState(discarded, "1.public.orders")
    expect(isEmpty(changeSetReducer(orders, { type: "undo" }).present)).toBe(true)
  })

  test("a state that was never given a scope belongs to none", () => {
    const loose = run({ type: "edit", edits: [edit("1", "email", "a@b.c")] })
    expect(isEmpty(scopedState(loose, "1.public.customers").present)).toBe(true)
  })
})

describe("how much one apply can carry", () => {
  const rows = (count, from = 0) =>
    Array.from({ length: count }, (_, i) => ({ id: `new:${from + i}` }))
  const full = applyChange(EMPTY_CHANGES, { type: "insert", rows: rows(MAX_CHANGES) })

  test("an action that stays within the limit is not refused, on it included", () => {
    expect(exceedsLimit(EMPTY_CHANGES, { type: "insert", rows: rows(MAX_CHANGES) })).toBe(false)
    expect(exceedsLimit(EMPTY_CHANGES, { type: "insert", rows: rows(3) })).toBe(false)
  })

  test("one row past it is refused, however it gets there", () => {
    expect(exceedsLimit(EMPTY_CHANGES, { type: "insert", rows: rows(MAX_CHANGES + 1) })).toBe(true)
    expect(exceedsLimit(full, { type: "insert", rows: rows(1, MAX_CHANGES) })).toBe(true)
    expect(exceedsLimit(full, { type: "edit", edits: [edit("1", "email", "a@b.c")] })).toBe(true)
    expect(exceedsLimit(full, { type: "delete", rows: [{ rowId: "2", origin: origin(BO) }] })).toBe(
      true,
    )
    const paste = {
      type: "batch",
      actions: [
        { type: "insert", rows: rows(600) },
        { type: "insert", rows: rows(600, 600) },
      ],
    }
    expect(exceedsLimit(EMPTY_CHANGES, paste)).toBe(true)
  })

  test("what does not add a row is never refused, at the limit or past it", () => {
    // Many cells of one row are one change.
    const cells = { type: "edit", edits: COLUMNS.map((c) => edit("1", c.key, "x")) }
    const nearly = applyChange(EMPTY_CHANGES, { type: "insert", rows: rows(MAX_CHANGES - 1) })
    expect(exceedsLimit(nearly, cells)).toBe(false)
    expect(
      exceedsLimit(full, {
        type: "edit",
        edits: [{ rowId: "new:5", column: "email", value: "x" }],
      }),
    ).toBe(false)
    expect(exceedsLimit(full, { type: "revertRow", rowId: "new:5" })).toBe(false)
    expect(exceedsLimit(full, { type: "discard" })).toBe(false)
    // An edit that puts back the original adds nothing, though it names a new row.
    expect(
      exceedsLimit(full, { type: "edit", edits: [edit("1", "email", "ann@example.com")] }),
    ).toBe(false)
    const over = applyChange(full, { type: "insert", rows: rows(5, MAX_CHANGES) })
    expect(exceedsLimit(over, { type: "revertRow", rowId: "new:5" })).toBe(false)
  })

  test("a set past the limit has no request, and says why", () => {
    const over = applyChange(full, { type: "insert", rows: rows(1, MAX_CHANGES) })
    const built = buildChanges(over, { table: "customers", columns: COLUMNS })
    expect(built.payload).toBeNull()
    expect(built.problems).toEqual([
      { rowId: null, reason: "1,001 rows are changed, and one apply takes at most 1,000" },
    ])
    expect(
      buildChanges(full, { table: "customers", columns: COLUMNS }).payload.changes,
    ).toHaveLength(MAX_CHANGES)
  })
})

describe("a row that cannot be found again", () => {
  const columns = [
    { key: "slug", name: "slug", primaryKey: true },
    { key: "title", name: "title" },
  ]
  const row = { slug: "the-first-four-thousand-bytes", title: "A long story" }

  test("a primary key that arrived cut is not a key", () => {
    expect(keyProblem(columns, row, ["slug"])).toBe(
      "Only the start of this row's key (slug) was loaded, so the row cannot be found again",
    )
    expect(keyProblem(columns, row, ["title"])).toBeNull()
    expect(keyProblem(columns, row)).toBeNull()
  })

  test("an update or a delete of such a row leaves no request to send", () => {
    const { present } = run(
      {
        type: "edit",
        edits: [
          { rowId: "a", column: "title", value: "x", kind: "text", origin: origin(row, ["slug"]) },
        ],
      },
      { type: "delete", rows: [{ rowId: "b", origin: origin({ ...row, slug: "other" }) }] },
    )
    const built = buildChanges(present, { table: "posts", columns })
    expect(built.payload).toBeNull()
    expect(built.problems).toHaveLength(1)
    expect(built.problems[0].rowId).toBe("a")
    // Which staged row each change came from is still told, to mark the row.
    expect(built.refs.map((ref) => ref.rowId)).toEqual(["b", "a"])
  })

  test("a keyless row whose every value was cut has nothing to be matched on", () => {
    const keyless = columns.map((column) => ({ ...column, primaryKey: false }))
    expect(keyProblem(keyless, row, ["slug", "title"])).toMatch(/Nothing identifies this row/)
    expect(keyProblem(keyless, row, ["slug"])).toBeNull()
  })

  test("a row staged against a table with other columns has no key in this one", () => {
    const { present } = run({ type: "edit", edits: [edit("1", "email", "a@b.c")] })
    const built = buildChanges(present, { table: "posts", columns })
    expect(built.payload).toBeNull()
    expect(built.problems[0].reason).toBe("This row was staged against a table with other columns")
  })

  test("a set with nothing wrong in it has no problems", () => {
    const { present } = run({ type: "edit", edits: [edit("1", "email", "a@b.c")] })
    expect(buildChanges(present, { table: "customers", columns: COLUMNS }).problems).toEqual([])
  })
})

describe("the request that applies a change set", () => {
  const target = { schema: "public", table: "customers", columns: COLUMNS }

  test("deletes, then updates, then inserts, each tied back to its staged row", () => {
    const { present } = run(
      { type: "insert", rows: [{ id: "new:1", values: { email: "new@example.com" } }] },
      { type: "edit", edits: [edit("1", "email", "a@b.c")] },
      { type: "delete", rows: [{ rowId: "2", origin: origin(BO) }] },
    )
    const { payload, refs } = buildChanges(present, target)
    expect(payload).toEqual({
      schema: "public",
      table: "customers",
      changes: [
        { op: "delete", key: { id: "2" } },
        { op: "update", key: { id: "1" }, values: { email: "a@b.c" } },
        { op: "insert", values: { email: "new@example.com" } },
      ],
    })
    expect(refs).toEqual([
      { index: 0, op: "delete", rowId: "2" },
      { index: 1, op: "update", rowId: "1" },
      { index: 2, op: "insert", rowId: "new:1" },
    ])
  })

  test("the key is the primary key as it was read, even when the key itself was edited", () => {
    const { present } = run({ type: "edit", edits: [edit("1", "id", "100")] })
    const { payload } = buildChanges(present, target)
    expect(payload.changes).toEqual([{ op: "update", key: { id: "1" }, values: { id: "100" } }])
  })

  test("a composite key sends every part of it", () => {
    const columns = [
      { key: "order_id", name: "order_id", primaryKey: true },
      { key: "line", name: "line", primaryKey: true },
      { key: "qty", name: "qty" },
    ]
    const row = { order_id: "9007199254740993", line: "2", qty: "1" }
    const { present } = run({
      type: "edit",
      edits: [
        {
          rowId: "9007199254740993:2",
          column: "qty",
          value: "3",
          kind: "number",
          origin: origin(row),
        },
      ],
    })
    const { payload } = buildChanges(present, { table: "order_items", columns })
    expect(payload.schema).toBe("")
    expect(payload.changes).toEqual([
      { op: "update", key: { order_id: "9007199254740993", line: "2" }, values: { qty: "3" } },
    ])
  })

  test("a table with no primary key is matched on the whole row as read, NULLs included", () => {
    const columns = [
      { key: "a", name: "a" },
      { key: "b", name: "b" },
      { key: "c", name: "c" },
    ]
    const row = { a: "x", b: null, c: "" }
    const { present } = run(
      {
        type: "edit",
        edits: [{ rowId: "0", column: "c", value: "y", kind: "text", origin: origin(row) }],
      },
      { type: "delete", rows: [{ rowId: "1", origin: origin({ a: "p", b: "q", c: null }) }] },
    )
    const { payload } = buildChanges(present, { schema: "main", table: "keyless", columns })
    expect(payload.changes).toEqual([
      { op: "delete", key: { a: "p", b: "q", c: null } },
      { op: "update", key: { a: "x", b: null, c: "" }, values: { c: "y" } },
    ])
  })

  test("a value that was only a preview is never part of a key", () => {
    const columns = [
      { key: "name", name: "name" },
      { key: "blob", name: "blob" },
    ]
    const row = { name: "logo", blob: "\\x8950… (4096 bytes)" }
    expect(rowKey(columns, row, ["blob"])).toEqual({ name: "logo" })
    const { present } = run({
      type: "edit",
      edits: [
        { rowId: "0", column: "name", value: "mark", kind: "text", origin: origin(row, ["blob"]) },
      ],
    })
    const { payload } = buildChanges(present, { table: "files", columns })
    expect(payload.changes[0].key).toEqual({ name: "logo" })
  })

  test("with the guard on, an update also matches the original of every edited column", () => {
    const { present } = run({
      type: "edit",
      edits: [edit("1", "email", "a@b.c"), edit("1", "note", "x")],
    })
    const { payload } = buildChanges(present, { ...target, guard: true })
    expect(payload.changes[0].key).toEqual({ id: "1", email: "ann@example.com", note: null })
  })

  test("the default marker travels as the object the route expects, and is absent from an insert", () => {
    const { present } = run(
      { type: "edit", edits: [edit("1", "note", DEFAULT_VALUE)] },
      { type: "insert", rows: [{ id: "new:1", values: { email: "x@y.z", note: DEFAULT_VALUE } }] },
    )
    const { payload } = buildChanges(present, target)
    expect(payload.changes).toEqual([
      { op: "update", key: { id: "1" }, values: { note: { $default: true } } },
      { op: "insert", values: { email: "x@y.z" } },
    ])
  })

  test("a column is sent under its name, not its key, and one the table no longer has is left out", () => {
    const columns = [
      { key: "0", name: "Id", primaryKey: true },
      { key: "1", name: "weird column" },
    ]
    const row = { 0: "5", 1: "spaces" }
    const { present } = run({
      type: "edit",
      edits: [
        { rowId: "5", column: "1", value: "tabs", kind: "text", origin: origin(row) },
        { rowId: "5", column: "gone", value: "x", kind: "text", origin: origin(row) },
      ],
    })
    const { payload } = buildChanges(present, { table: "Mixed Case Table", columns })
    expect(payload.changes).toEqual([
      { op: "update", key: { Id: "5" }, values: { "weird column": "tabs" } },
    ])
  })

  test("a set that survived JSON — session storage — builds the same request", () => {
    const { present } = run(
      { type: "edit", edits: [edit("1", "note", DEFAULT_VALUE)] },
      { type: "delete", rows: [{ rowId: "2", origin: origin(BO) }] },
    )
    const revived = JSON.parse(JSON.stringify(present))
    expect(buildChanges(revived, target)).toEqual(buildChanges(present, target))
  })

  test("an empty set is an empty request", () => {
    expect(buildChanges(EMPTY_CHANGES, target).payload.changes).toEqual([])
    expect(changeCounts(EMPTY_CHANGES).total).toBe(0)
  })
})
