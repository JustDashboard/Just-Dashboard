"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { isDefault, sameValue, type EditValue } from "./values"
import type { CellValue, GridColumn, GridColumnKind } from "./types"

/**
 * The edits nobody has sent yet.
 *
 * Typing in a cell changes nothing on the server. It records an intention
 * here — this row gains these values, that row goes, a new row arrives — and
 * the whole set is reviewed and applied in one transaction by
 * `POST /databases/{id}/changes`, or thrown away. That is what makes a grid
 * safe to type in: every edit can be seen before it happens, undone before it
 * happens, and either all of them land or none do.
 *
 * Three rules the rest of the grid leans on:
 *
 * - **A row is remembered as it was read.** The first edit to a row stores the
 *   whole original row beside it. The key that finds the row again is built
 *   from those values, never from the edited ones, so changing a primary key
 *   still updates the row that had the old one.
 * - **An edit back to the original is not an edit.** Values are compared by
 *   meaning (`4193.4` is `4193.40`), and a row whose last change was undone by
 *   hand leaves the set entirely.
 * - **Only what changed is written.** An update carries the columns that were
 *   edited and nothing else — not the primary key, not a binary preview, not a
 *   column the reader never touched.
 */

/** A row as it was read, by column key. */
export type RowSnapshot = Readonly<Record<string, CellValue>>

/** What an edit needs to know about the row it lands on, the first time it does. */
export interface RowOrigin {
  values: RowSnapshot
  /** Column keys whose value is only a preview. They are left out of a key. */
  clipped?: readonly string[]
}

export interface InsertedRow {
  /** A temporary id, unique for the life of the change set. */
  id: string
  /** Only the columns that were given a value; the rest take their defaults. */
  values: Readonly<Record<string, EditValue>>
}

export interface UpdatedRow {
  original: RowSnapshot
  clipped?: readonly string[]
  /** The edited columns alone. Never empty: an update with nothing in it is removed. */
  values: Readonly<Record<string, EditValue>>
}

export interface DeletedRow {
  original: RowSnapshot
  clipped?: readonly string[]
}

export interface ChangeSet {
  inserts: readonly InsertedRow[]
  /** By row id. */
  updates: Readonly<Record<string, UpdatedRow>>
  /** By row id. */
  deletes: Readonly<Record<string, DeletedRow>>
}

export interface ChangeSetState {
  present: ChangeSet
  past: readonly ChangeSet[]
  future: readonly ChangeSet[]
  /**
   * The table the set was staged against, as `useChangeSet` was told it. A
   * state read under any other scope is an empty one: row ids repeat from table
   * to table, and an edit to `customers` row 7 must never be found again as an
   * edit to `orders` row 7.
   */
  scope?: string
}

export const EMPTY_CHANGES: ChangeSet = { inserts: [], updates: {}, deletes: {} }
export const EMPTY_CHANGE_STATE: ChangeSetState = { present: EMPTY_CHANGES, past: [], future: [] }

/** How many steps back Ctrl+Z goes. Each step shares structure with the next, so this is cheap. */
export const HISTORY_LIMIT = 200

/**
 * The most rows one request to the changes route may change. The set is
 * applied in one transaction and cannot be split, so a set past this could
 * never be applied at all — which is why the grid stops staging at it rather
 * than letting the reader find out at the end.
 */
export const MAX_CHANGES = 1000

export interface CellEdit {
  rowId: string
  /** Column key. */
  column: string
  value: EditValue
  /** The column's kind, so an edit back to the original is recognised by meaning. */
  kind: GridColumnKind
  /** The row as read. Needed for a row from the server; ignored for an inserted one. */
  origin?: RowOrigin
}

export interface RowRemoval {
  rowId: string
  /** The row as read. Needed for a row from the server; ignored for an inserted one. */
  origin?: RowOrigin
}

export interface RowInsertion {
  id: string
  values?: Readonly<Record<string, EditValue>>
}

export type ChangeAction =
  | { type: "edit"; edits: readonly CellEdit[] }
  | { type: "insert"; rows: readonly RowInsertion[] }
  | { type: "delete"; rows: readonly RowRemoval[] }
  | { type: "revertCell"; rowId: string; column: string }
  | { type: "revertRow"; rowId: string }
  | { type: "discard" }
  /** Several actions as one step of history: a paste, a fill, a bulk delete. */
  | { type: "batch"; actions: readonly ChangeAction[] }
  | { type: "undo" }
  | { type: "redo" }
  /** Forget everything, history included: the set was applied. */
  | { type: "reset" }

function omit<T>(record: Readonly<Record<string, T>>, key: string): Record<string, T> {
  const next = { ...record }
  delete next[key]
  return next
}

function insertIndex(changes: ChangeSet, rowId: string): number {
  return changes.inserts.findIndex((row) => row.id === rowId)
}

function editOne(changes: ChangeSet, edit: CellEdit): ChangeSet {
  const at = insertIndex(changes, edit.rowId)
  if (at >= 0) {
    const row = changes.inserts[at]
    const has = edit.column in row.values
    // A new row has no original: DEFAULT is the absence of a value.
    if (isDefault(edit.value)) {
      if (!has) return changes
      const inserts = [...changes.inserts]
      inserts[at] = { ...row, values: omit(row.values, edit.column) }
      return { ...changes, inserts }
    }
    if (has && row.values[edit.column] === edit.value) return changes
    const inserts = [...changes.inserts]
    inserts[at] = { ...row, values: { ...row.values, [edit.column]: edit.value } }
    return { ...changes, inserts }
  }

  // A row on its way out is not edited; restoring it comes first.
  if (changes.deletes[edit.rowId]) return changes

  const existing = changes.updates[edit.rowId]
  const origin =
    existing ?? (edit.origin && { original: edit.origin.values, clipped: edit.origin.clipped })
  if (!origin) return changes
  const before = origin.original[edit.column] ?? null

  if (sameValue(edit.value, before, edit.kind)) {
    if (!existing || !(edit.column in existing.values)) return changes
    const values = omit(existing.values, edit.column)
    if (Object.keys(values).length === 0) {
      return { ...changes, updates: omit(changes.updates, edit.rowId) }
    }
    return { ...changes, updates: { ...changes.updates, [edit.rowId]: { ...existing, values } } }
  }

  if (existing && edit.column in existing.values && existing.values[edit.column] === edit.value) {
    return changes
  }
  const values = { ...(existing?.values ?? {}), [edit.column]: edit.value }
  return {
    ...changes,
    updates: {
      ...changes.updates,
      [edit.rowId]: { original: origin.original, clipped: origin.clipped, values },
    },
  }
}

function deleteOne(changes: ChangeSet, removal: RowRemoval): ChangeSet {
  const at = insertIndex(changes, removal.rowId)
  // A row that was never sent is simply dropped.
  if (at >= 0) return { ...changes, inserts: changes.inserts.filter((_, i) => i !== at) }
  if (changes.deletes[removal.rowId]) return changes
  const updated = changes.updates[removal.rowId]
  const origin = updated
    ? { original: updated.original, clipped: updated.clipped }
    : removal.origin && { original: removal.origin.values, clipped: removal.origin.clipped }
  if (!origin) return changes
  return {
    ...changes,
    // Its edits go with it: there is no point updating a row that is being deleted.
    updates: updated ? omit(changes.updates, removal.rowId) : changes.updates,
    deletes: { ...changes.deletes, [removal.rowId]: origin },
  }
}

function revertCell(changes: ChangeSet, rowId: string, column: string): ChangeSet {
  const at = insertIndex(changes, rowId)
  if (at >= 0) {
    const row = changes.inserts[at]
    if (!(column in row.values)) return changes
    const inserts = [...changes.inserts]
    inserts[at] = { ...row, values: omit(row.values, column) }
    return { ...changes, inserts }
  }
  const updated = changes.updates[rowId]
  if (!updated || !(column in updated.values)) return changes
  const values = omit(updated.values, column)
  if (Object.keys(values).length === 0) return { ...changes, updates: omit(changes.updates, rowId) }
  return { ...changes, updates: { ...changes.updates, [rowId]: { ...updated, values } } }
}

function revertRow(changes: ChangeSet, rowId: string): ChangeSet {
  const at = insertIndex(changes, rowId)
  if (at >= 0) return { ...changes, inserts: changes.inserts.filter((_, i) => i !== at) }
  if (!changes.updates[rowId] && !changes.deletes[rowId]) return changes
  return {
    ...changes,
    updates: changes.updates[rowId] ? omit(changes.updates, rowId) : changes.updates,
    deletes: changes.deletes[rowId] ? omit(changes.deletes, rowId) : changes.deletes,
  }
}

/** One action applied to a change set. Returns the same object when nothing changed. */
export function applyChange(changes: ChangeSet, action: ChangeAction): ChangeSet {
  switch (action.type) {
    case "edit":
      return action.edits.reduce(editOne, changes)
    case "insert": {
      const taken = new Set(changes.inserts.map((row) => row.id))
      const fresh = action.rows.filter((row) => !taken.has(row.id))
      if (fresh.length === 0) return changes
      const rows = fresh.map((row) => {
        // DEFAULT in a new row is the same as saying nothing.
        const values = Object.fromEntries(
          Object.entries(row.values ?? {}).filter(([, value]) => !isDefault(value)),
        )
        return { id: row.id, values }
      })
      return { ...changes, inserts: [...changes.inserts, ...rows] }
    }
    case "delete":
      return action.rows.reduce(deleteOne, changes)
    case "revertCell":
      return revertCell(changes, action.rowId, action.column)
    case "revertRow":
      return revertRow(changes, action.rowId)
    case "discard":
      return isEmpty(changes) ? changes : EMPTY_CHANGES
    case "batch":
      return action.actions.reduce(applyChange, changes)
    // History is the reducer's; a change set alone has none.
    case "undo":
    case "redo":
    case "reset":
      return changes
  }
}

/**
 * The reducer: `applyChange` with a history around it.
 *
 * Every action that changes the set is one step back; one that changes nothing
 * (an edit to the value already there, a revert of an untouched row) leaves
 * the history alone, so Ctrl+Z never appears to do nothing.
 */
export function changeSetReducer(state: ChangeSetState, action: ChangeAction): ChangeSetState {
  switch (action.type) {
    case "undo": {
      if (state.past.length === 0) return state
      return {
        ...state,
        present: state.past[state.past.length - 1],
        past: state.past.slice(0, -1),
        future: [state.present, ...state.future],
      }
    }
    case "redo": {
      if (state.future.length === 0) return state
      return {
        ...state,
        present: state.future[0],
        past: [...state.past, state.present],
        future: state.future.slice(1),
      }
    }
    case "reset":
      return isEmpty(state.present) && !state.past.length && !state.future.length
        ? state
        : { ...state, ...EMPTY_CHANGE_STATE }
    default: {
      const present = applyChange(state.present, action)
      if (present === state.present) return state
      return {
        ...state,
        present,
        past: [...state.past, state.present].slice(-HISTORY_LIMIT),
        future: [],
      }
    }
  }
}

/* ---------------------------------------------------------------- reading */

export function isEmpty(changes: ChangeSet): boolean {
  return (
    changes.inserts.length === 0 &&
    Object.keys(changes.updates).length === 0 &&
    Object.keys(changes.deletes).length === 0
  )
}

export interface ChangeCounts {
  inserts: number
  updates: number
  deletes: number
  /** Edited cells across every updated row. */
  cells: number
  total: number
}

export function changeCounts(changes: ChangeSet): ChangeCounts {
  const inserts = changes.inserts.length
  const updates = Object.keys(changes.updates).length
  const deletes = Object.keys(changes.deletes).length
  let cells = 0
  for (const row of Object.values(changes.updates)) cells += Object.keys(row.values).length
  return { inserts, updates, deletes, cells, total: inserts + updates + deletes }
}

/** The most rows an action could add to the set, without working out what it would do. */
function rowsTouched(action: ChangeAction): number {
  switch (action.type) {
    case "edit":
      return new Set(action.edits.map((edit) => edit.rowId)).size
    case "insert":
    case "delete":
      return action.rows.length
    case "batch":
      return action.actions.reduce((sum, inner) => sum + rowsTouched(inner), 0)
    default:
      return 0
  }
}

/**
 * Whether staging an action would leave more rows changed than one request can
 * carry. An action that does not grow the set is never refused, so a set that
 * is somehow already too large can still be edited down.
 */
export function exceedsLimit(
  changes: ChangeSet,
  action: ChangeAction,
  limit = MAX_CHANGES,
): boolean {
  const before = changeCounts(changes).total
  // Most actions are nowhere near the limit; only one that might cross it is
  // worth applying to find out.
  if (before + rowsTouched(action) <= limit) return false
  const after = changeCounts(applyChange(changes, action)).total
  return after > limit && after > before
}

/** A new row's values from an existing one: "the same as that, but different". */
export function duplicateValues(
  columns: readonly Pick<GridColumn, "key" | "primaryKey" | "generated">[],
  values: Readonly<Record<string, EditValue | undefined>>,
  clipped: readonly string[] = [],
): Record<string, EditValue> {
  const skip = new Set(clipped)
  const copy: Record<string, EditValue> = {}
  for (const column of columns) {
    // The key is what makes the new row a different row, a computed column is
    // the engine's to fill, and a preview is not the value.
    if (column.primaryKey || column.generated || skip.has(column.key)) continue
    const value = values[column.key]
    if (value === undefined || isDefault(value)) continue
    copy[column.key] = value
  }
  return copy
}

/* ---------------------------------------------------------------- payload */

/** What the payload builder needs to know about a column. */
export type ChangeColumn = Pick<GridColumn, "key" | "name" | "primaryKey">

export type Change =
  | { op: "insert"; values: Record<string, EditValue> }
  | { op: "update"; key: Record<string, CellValue>; values: Record<string, EditValue> }
  | { op: "delete"; key: Record<string, CellValue> }

/** The body of `POST /databases/{id}/changes`. */
export interface ChangesPayload {
  schema: string
  table: string
  changes: Change[]
}

/** Which staged row each entry of `changes` came from, so a refusal can be shown on it. */
export interface ChangeRef {
  index: number
  op: Change["op"]
  rowId: string
}

/** Why a staged set cannot be sent as it stands. */
export interface ChangeProblem {
  /** The staged row it is about, or null when it is about the set as a whole. */
  rowId: string | null
  reason: string
}

export interface ChangesTarget {
  schema?: string
  table: string
  columns: readonly ChangeColumn[]
  /**
   * Also match on the original value of every edited column, so an update to a
   * row somebody else changed in the meantime matches nothing and is refused,
   * instead of overwriting their value with one decided from a stale read.
   */
  guard?: boolean
}

/**
 * The key that finds a row again: its primary-key columns as they were read,
 * or — for a table with no primary key — the whole row as it was read, minus
 * the columns that only held a preview.
 */
export function rowKey(
  columns: readonly ChangeColumn[],
  original: RowSnapshot,
  clipped: readonly string[] = [],
): Record<string, CellValue> {
  const primary = columns.filter((column) => column.primaryKey)
  const skip = new Set(clipped)
  const from = primary.length > 0 ? primary : columns.filter((column) => !skip.has(column.key))
  const key: Record<string, CellValue> = {}
  for (const column of from) key[column.name] = original[column.key] ?? null
  return key
}

/**
 * Why a row as it was read cannot be found again, or null when it can.
 *
 * A key built from the first bytes of a value is worse than no key: it matches
 * nothing, or it matches the one other row whose whole key is those bytes.
 * And a row read from a table with other columns has no key in this one.
 */
export function keyProblem(
  columns: readonly ChangeColumn[],
  original: RowSnapshot,
  clipped: readonly string[] = [],
): string | null {
  const primary = columns.filter((column) => column.primaryKey)
  const skip = new Set(clipped)
  if (primary.length === 0) {
    const whole = columns.filter((column) => !skip.has(column.key) && column.key in original)
    return whole.length > 0
      ? null
      : "Nothing identifies this row: none of its values was loaded whole"
  }
  const missing = primary.filter((column) => !(column.key in original))
  if (missing.length > 0) return "This row was staged against a table with other columns"
  const cut = primary.filter((column) => skip.has(column.key))
  if (cut.length === 0) return null
  const names = cut.map((column) => column.name).join(", ")
  return `Only the start of this row's key (${names}) was loaded, so the row cannot be found again`
}

/**
 * The staged set as the request that applies it.
 *
 * Deletes go first, then updates, then inserts: a unique value freed by a
 * delete or an update is then free by the time a later row wants it, which is
 * the order a person doing it by hand would choose. Column keys become column
 * names here and nowhere else.
 *
 * `payload` is null when the set cannot be sent as it stands, and `problems`
 * says why: more rows than one request takes, or a row that cannot be found
 * again. There is no request to send in that case, on purpose — the set is
 * applied whole or not at all, and a body with the awkward rows left out would
 * be a different set from the one the reader reviewed.
 */
export function buildChanges(
  changes: ChangeSet,
  target: ChangesTarget,
): { payload: ChangesPayload | null; refs: ChangeRef[]; problems: ChangeProblem[] } {
  const names = new Map(target.columns.map((column) => [column.key, column.name]))
  const named = (values: Readonly<Record<string, EditValue>>) => {
    const out: Record<string, EditValue> = {}
    for (const [key, value] of Object.entries(values)) {
      const name = names.get(key)
      if (name !== undefined) out[name] = isDefault(value) ? { $default: true } : value
    }
    return out
  }

  const list: Change[] = []
  const refs: ChangeRef[] = []
  const problems: ChangeProblem[] = []
  const push = (change: Change, rowId: string) => {
    refs.push({ index: list.length, op: change.op, rowId })
    list.push(change)
  }
  const findable = (rowId: string, row: DeletedRow) => {
    const reason = keyProblem(target.columns, row.original, row.clipped)
    if (reason) problems.push({ rowId, reason })
  }

  for (const [rowId, row] of Object.entries(changes.deletes)) {
    findable(rowId, row)
    push({ op: "delete", key: rowKey(target.columns, row.original, row.clipped) }, rowId)
  }
  for (const [rowId, row] of Object.entries(changes.updates)) {
    findable(rowId, row)
    const key = rowKey(target.columns, row.original, row.clipped)
    if (target.guard) {
      const skip = new Set(row.clipped ?? [])
      for (const column of Object.keys(row.values)) {
        const name = names.get(column)
        if (name !== undefined && !skip.has(column)) key[name] = row.original[column] ?? null
      }
    }
    push({ op: "update", key, values: named(row.values) }, rowId)
  }
  for (const row of changes.inserts) {
    push({ op: "insert", values: named(row.values) }, row.id)
  }

  if (list.length > MAX_CHANGES) {
    problems.unshift({
      rowId: null,
      reason: `${list.length.toLocaleString("en-US")} rows are changed, and one apply takes at most ${MAX_CHANGES.toLocaleString("en-US")}`,
    })
  }

  return {
    payload:
      problems.length > 0
        ? null
        : { schema: target.schema ?? "", table: target.table, changes: list },
    refs,
    problems,
  }
}

/* ------------------------------------------------------------------- hook */

/** Somewhere to keep the state other than this component: session storage, a parent. */
export interface ChangeSetStore {
  state: ChangeSetState
  setState: (next: ChangeSetState | ((previous: ChangeSetState) => ChangeSetState)) => void
}

export interface ChangeSetController {
  changes: ChangeSet
  counts: ChangeCounts
  dirty: boolean
  canUndo: boolean
  canRedo: boolean
  dispatch: (action: ChangeAction) => void
  /** A fresh temporary row id, for building an insert inside a batch. */
  newRowId: () => string
  editCell: (edit: CellEdit) => void
  /** Adds one new row and returns its temporary id. */
  insertRow: (values?: Readonly<Record<string, EditValue>>) => string
  deleteRows: (rows: readonly RowRemoval[]) => void
  revertCell: (rowId: string, column: string) => void
  revertRow: (rowId: string) => void
  /**
   * Empties the set as one more step of history, so Ctrl+Z brings it back.
   * For "throw these edits away"; leaving the table for another is `scope`'s job.
   */
  discardAll: () => void
  undo: () => void
  redo: () => void
  /** Empties the set and its history, after it was applied. */
  reset: () => void
}

const NEW_ROW_PREFIX = "new:"

// Temporary ids only have to be unique within one change set. The page-load
// stamp keeps them apart from a set that came back out of session storage.
const SESSION = Date.now().toString(36)
let sequence = 0

export function isNewRowId(rowId: string): boolean {
  return rowId.startsWith(NEW_ROW_PREFIX)
}

export interface ChangeSetOptions {
  /**
   * What the edits are for: one table of one database, spelled however the
   * owner likes as long as two tables never share it
   * (`${id}.${schema}.${table}`). When it changes, the set and its history
   * are gone — not discarded, which could be undone into the next table, but
   * never there as far as the new scope is concerned.
   */
  scope: string
  /** Somewhere else to keep the state. It is stamped with the scope it was staged under. */
  store?: ChangeSetStore
}

/**
 * A state as one scope sees it: itself when it was staged under that scope,
 * and otherwise empty — history included, so nothing staged for one table can
 * be undone back into another.
 */
export function scopedState(state: ChangeSetState, scope: string): ChangeSetState {
  return state.scope === scope ? state : { ...EMPTY_CHANGE_STATE, scope }
}

function untouched(state: ChangeSetState): boolean {
  return isEmpty(state.present) && state.past.length === 0 && state.future.length === 0
}

/**
 * The change set as a hook.
 *
 * Holds its own state unless handed a `store` — pass one built on
 * `useSessionState` and a half-finished edit survives leaving the page, which
 * is what staged work should do.
 */
export function useChangeSet({ scope, store }: ChangeSetOptions): ChangeSetController {
  const [inner, setInner] = useState(EMPTY_CHANGE_STATE)
  const stored = store?.state ?? inner
  const setState = store?.setState ?? setInner
  const state = useMemo(() => scopedState(stored, scope), [stored, scope])

  // What another table left behind is erased, not merely hidden: coming back
  // to that table must not bring back edits the reader was told were gone.
  const stale = stored.scope !== scope && !untouched(stored)
  useEffect(() => {
    if (stale) setState((previous) => scopedState(previous, scope))
  }, [stale, setState, scope])

  const dispatch = useCallback(
    (action: ChangeAction) =>
      setState((previous) => changeSetReducer(scopedState(previous, scope), action)),
    [setState, scope],
  )

  const newRowId = useCallback(() => `${NEW_ROW_PREFIX}${SESSION}-${++sequence}`, [])

  const actions = useMemo(
    () => ({
      editCell: (edit: CellEdit) => dispatch({ type: "edit", edits: [edit] }),
      insertRow: (values?: Readonly<Record<string, EditValue>>) => {
        const id = newRowId()
        dispatch({ type: "insert", rows: [{ id, values }] })
        return id
      },
      deleteRows: (rows: readonly RowRemoval[]) => dispatch({ type: "delete", rows }),
      revertCell: (rowId: string, column: string) =>
        dispatch({ type: "revertCell", rowId, column }),
      revertRow: (rowId: string) => dispatch({ type: "revertRow", rowId }),
      discardAll: () => dispatch({ type: "discard" }),
      undo: () => dispatch({ type: "undo" }),
      redo: () => dispatch({ type: "redo" }),
      reset: () => dispatch({ type: "reset" }),
    }),
    [dispatch, newRowId],
  )

  const counts = useMemo(() => changeCounts(state.present), [state.present])

  return useMemo(
    () => ({
      changes: state.present,
      counts,
      dirty: counts.total > 0,
      canUndo: state.past.length > 0,
      canRedo: state.future.length > 0,
      dispatch,
      newRowId,
      ...actions,
    }),
    [state, counts, dispatch, newRowId, actions],
  )
}
