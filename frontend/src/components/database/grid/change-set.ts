"use client"

import { useCallback, useMemo, useState } from "react"
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
}

export const EMPTY_CHANGES: ChangeSet = { inserts: [], updates: {}, deletes: {} }
export const EMPTY_CHANGE_STATE: ChangeSetState = { present: EMPTY_CHANGES, past: [], future: [] }

/** How many steps back Ctrl+Z goes. Each step shares structure with the next, so this is cheap. */
export const HISTORY_LIMIT = 200

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
        present: state.past[state.past.length - 1],
        past: state.past.slice(0, -1),
        future: [state.present, ...state.future],
      }
    }
    case "redo": {
      if (state.future.length === 0) return state
      return {
        present: state.future[0],
        past: [...state.past, state.present],
        future: state.future.slice(1),
      }
    }
    case "reset":
      return state === EMPTY_CHANGE_STATE ||
        (isEmpty(state.present) && !state.past.length && !state.future.length)
        ? state
        : EMPTY_CHANGE_STATE
    default: {
      const present = applyChange(state.present, action)
      if (present === state.present) return state
      return {
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
 * The staged set as the request that applies it.
 *
 * Deletes go first, then updates, then inserts: a unique value freed by a
 * delete or an update is then free by the time a later row wants it, which is
 * the order a person doing it by hand would choose. Column keys become column
 * names here and nowhere else.
 */
export function buildChanges(
  changes: ChangeSet,
  target: ChangesTarget,
): { payload: ChangesPayload; refs: ChangeRef[] } {
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
  const push = (change: Change, rowId: string) => {
    refs.push({ index: list.length, op: change.op, rowId })
    list.push(change)
  }

  for (const [rowId, row] of Object.entries(changes.deletes)) {
    push({ op: "delete", key: rowKey(target.columns, row.original, row.clipped) }, rowId)
  }
  for (const [rowId, row] of Object.entries(changes.updates)) {
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

  return { payload: { schema: target.schema ?? "", table: target.table, changes: list }, refs }
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

/**
 * The change set as a hook.
 *
 * Holds its own state unless handed a `store` — pass one built on
 * `useSessionState` and a half-finished edit survives leaving the page, which
 * is what staged work should do.
 */
export function useChangeSet(store?: ChangeSetStore): ChangeSetController {
  const [inner, setInner] = useState(EMPTY_CHANGE_STATE)
  const state = store?.state ?? inner
  const setState = store?.setState ?? setInner

  const dispatch = useCallback(
    (action: ChangeAction) => setState((previous) => changeSetReducer(previous, action)),
    [setState],
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
