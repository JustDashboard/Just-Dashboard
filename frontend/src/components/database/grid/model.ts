import type { CellEdit, ChangeSet, InsertedRow, RowOrigin } from "./change-set"
import type { OrderedColumn } from "./layout"
import { isRange, rangeContains, rangeOf } from "./selection"
import { isDefault, isTruncatedValue, type EditValue } from "./values"
import type { CellValue, GridCellRef, GridColumn, GridRow, GridSelection } from "./types"

/**
 * The grid as data: the rows in the order they are drawn, with the staged
 * changes laid over them.
 *
 * Everything that asks "what is in this cell" — the renderer, a copy, the
 * status line, an editor about to open — asks here, so a pending edit is seen
 * the same way by all of them. Rows from the server come first, in the order
 * the owner (or a client-side sort) put them; rows that only exist in the
 * change set follow.
 */
export interface GridModel {
  columns: readonly GridColumn[]
  rows: readonly GridRow[]
  /** Visible columns in display order. */
  ordered: readonly OrderedColumn[]
  /** Row ids in display order. */
  ids: readonly string[]
  /** For each display row, its index in `rows`; -1 for an inserted row. */
  sources: readonly number[]
  /** How many of the display rows came from the server. */
  serverCount: number
  changes: ChangeSet
  /** Row index in `rows` → column index → the whole value's size, for cells the server cut. */
  clipped: ReadonlyMap<number, ReadonlyMap<number, number>>
  editable: boolean
  /** Whether a row that already exists can be sent back to its column default. */
  defaultOnUpdate: boolean
  /** Where the primary-key columns sit in a row. Empty for a table without one. */
  keys: readonly number[]
}

/** The positions of the primary-key columns, for `GridModel.keys`. */
export function keySources(columns: readonly GridColumn[]): number[] {
  return columns.flatMap((column, i) => (column.primaryKey ? [i] : []))
}

export function insertedAt(model: GridModel, row: number): InsertedRow | undefined {
  return model.sources[row] < 0 ? model.changes.inserts[row - model.serverCount] : undefined
}

/** A cell as it is drawn. `undefined` is an inserted row's column nobody has set. */
export function valueAt(
  model: GridModel,
  row: number,
  entry: Pick<OrderedColumn, "column" | "source">,
): EditValue | undefined {
  const insert = insertedAt(model, row)
  if (insert) return insert.values[entry.column.key]
  const edited = model.changes.updates[model.ids[row]]?.values
  if (edited && entry.column.key in edited) return edited[entry.column.key]
  return model.rows[model.sources[row]]?.[entry.source] ?? null
}

/** Whether the server sent only the beginning of a value. */
export function isPreview(model: GridModel, row: number, source: number): boolean {
  const from = model.sources[row]
  if (from < 0) return false
  if (model.clipped.get(from)?.has(source)) return true
  const value = model.rows[from]?.[source]
  return typeof value === "string" && isTruncatedValue(value)
}

/**
 * Whether a row's primary key arrived cut. Such a row can be read and copied
 * but not changed: the key is how the change finds the row again, and the
 * first bytes of a key find nothing — or find another row.
 */
export function keyIsPreview(model: GridModel, row: number): boolean {
  if (model.sources[row] < 0) return false
  return model.keys.some((source) => isPreview(model, row, source))
}

/** A server row as it was read, in the shape the change set keeps. */
export function originOf(model: GridModel, row: number): RowOrigin | undefined {
  const from = model.sources[row]
  if (from < 0) return undefined
  const read = model.rows[from]
  const values: Record<string, CellValue> = {}
  const clipped: string[] = []
  model.columns.forEach((column, i) => {
    values[column.key] = read[i] ?? null
    if (isPreview(model, row, i)) clipped.push(column.key)
  })
  return { values, clipped: clipped.length > 0 ? clipped : undefined }
}

export type RowPending = "none" | "inserted" | "updated" | "deleted"

export function pendingOf(model: GridModel, row: number): RowPending {
  if (model.sources[row] < 0) return "inserted"
  const id = model.ids[row]
  if (model.changes.deletes[id]) return "deleted"
  return model.changes.updates[id] ? "updated" : "none"
}

/**
 * Why a cell cannot be edited, as a sentence for the reader — or null when it
 * can. A preview is the one that matters most: writing back the first 256
 * bytes of a value as if they were the value is how a row is silently
 * destroyed, so a cut cell is refused here whatever else is true.
 */
export function lockReason(model: GridModel, row: number, entry: OrderedColumn): string | null {
  if (!model.editable) return "These rows are read-only"
  if (row < 0 || row >= model.ids.length) return "Nothing to edit here"
  const { column } = entry
  if (column.generated) return `${column.name} is computed by the database`
  if (column.editable === false) return `${column.name} cannot be edited here`
  if (model.sources[row] < 0) return null
  if (model.changes.deletes[model.ids[row]]) return "This row is marked for deletion"
  if (isPreview(model, row, entry.source)) {
    return "Only the start of this value was loaded, so it cannot be edited here"
  }
  if (keyIsPreview(model, row)) return KEY_PREVIEW
  return null
}

/** Why a row whose key was cut is left alone. */
export const KEY_PREVIEW =
  "Only the start of this row's key was loaded, so the row cannot be changed here"

/**
 * Whether "use the column's default" can be staged for a cell. A new row can
 * always leave a column to its default; an existing one only where the engine
 * has `SET column = DEFAULT`, which SQLite does not.
 */
export function defaultAllowed(model: GridModel, row: number, column: GridColumn): boolean {
  return column.defaultExpr !== undefined && (model.defaultOnUpdate || model.sources[row] < 0)
}

/** One staged edit for a cell, carrying the row as read when the set does not hold it yet. */
export function editFor(
  model: GridModel,
  row: number,
  entry: OrderedColumn,
  value: EditValue,
): CellEdit {
  const rowId = model.ids[row]
  const known = model.sources[row] < 0 || model.changes.updates[rowId] !== undefined
  return {
    rowId,
    column: entry.column.key,
    value,
    kind: entry.column.kind,
    origin: known ? undefined : originOf(model, row),
  }
}

/** A whole row as it is drawn, aligned to `model.columns`. Unset and default read as NULL. */
export function rowValues(model: GridModel, row: number): CellValue[] {
  return model.columns.map((column, source) => {
    const value = valueAt(model, row, { column, source })
    return value === undefined || isDefault(value) ? null : value
  })
}

/** The same row by column key, with unset columns left out: what a duplicate starts from. */
export function rowRecord(model: GridModel, row: number): Record<string, EditValue | undefined> {
  const record: Record<string, EditValue | undefined> = {}
  model.columns.forEach((column, source) => {
    record[column.key] = valueAt(model, row, { column, source })
  })
  return record
}

/** Column keys of a row whose values are previews. */
export function previewKeys(model: GridModel, row: number): string[] {
  return model.columns.filter((_, i) => isPreview(model, row, i)).map((column) => column.key)
}

export interface GridBlock {
  rows: number[]
  cols: OrderedColumn[]
}

/**
 * The cells a verb acts on, given the cell it was invoked from — the one under
 * the menu, or the active one for a key.
 *
 * The range when that cell is inside it; every visible column of the ticked
 * rows when that cell's row is one of them; and otherwise that cell alone.
 * What is selected somewhere else is never the target of something done here:
 * "Set NULL" on a cell in the fourth row does not empty three ticked rows
 * above it. With no cell to go by (the owner's toolbar asking for a copy) it
 * is the range, or failing that the ticked rows.
 */
export function actionBlock(
  model: GridModel,
  selection: GridSelection,
  at: GridCellRef | null,
): GridBlock | null {
  const range = isRange(selection) ? rangeOf(selection) : null
  const span = (top: number, bottom: number) => {
    const rows: number[] = []
    for (let row = top; row <= bottom; row++) rows.push(row)
    return rows
  }
  if (!at) {
    if (range) {
      return {
        rows: span(range.top, range.bottom),
        cols: model.ordered.slice(range.left, range.right + 1),
      }
    }
    const ticked = tickedRows(model, selection)
    return ticked.length > 0 ? { rows: ticked, cols: [...model.ordered] } : null
  }
  if (at.row < 0 || at.row >= model.ids.length || !model.ordered[at.col]) return null
  if (range && rangeContains(range, at.row, at.col)) {
    return {
      rows: span(range.top, range.bottom),
      cols: model.ordered.slice(range.left, range.right + 1),
    }
  }
  if (selection.rows.includes(model.ids[at.row])) {
    return { rows: tickedRows(model, selection), cols: [...model.ordered] }
  }
  return { rows: [at.row], cols: [model.ordered[at.col]] }
}

/**
 * The rows a row verb acts on when it is asked for from outside the grid — the
 * owner's own Delete or Duplicate button, which has no cell to go by: the
 * ticked rows, which is what ticking is for; failing that the rows the range
 * crosses, or the active one.
 */
export function selectedRows(model: GridModel, selection: GridSelection): number[] {
  const ticked = tickedRows(model, selection)
  if (ticked.length > 0) return ticked
  const range = rangeOf(selection)
  if (!range) return []
  const rows: number[] = []
  for (let row = range.top; row <= range.bottom; row++) rows.push(row)
  return rows
}

/** The rows ticked in the selector column, as display indexes in display order. */
export function tickedRows(model: GridModel, selection: GridSelection): number[] {
  if (selection.rows.length === 0) return []
  const ticked = new Set(selection.rows)
  return model.ids.flatMap((id, row) => (ticked.has(id) ? [row] : []))
}

/**
 * The rows a row verb acts on, given the row it was invoked from: every ticked
 * row when that row is one of them, every row of the range when it is inside
 * it, and otherwise that row alone.
 */
export function targetRows(model: GridModel, selection: GridSelection, row: number): number[] {
  if (selection.rows.includes(model.ids[row])) return tickedRows(model, selection)
  const range = rangeOf(selection)
  if (range && isRange(selection) && row >= range.top && row <= range.bottom) {
    const rows: number[] = []
    for (let index = range.top; index <= range.bottom; index++) rows.push(index)
    return rows
  }
  return [row]
}
