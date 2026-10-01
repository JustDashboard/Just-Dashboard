import type { CellEdit, ChangeSet, InsertedRow, RowOrigin } from "./change-set"
import type { OrderedColumn } from "./layout"
import { isRange, rangeOf } from "./selection"
import { isDefault, isTruncatedValue, type EditValue } from "./values"
import type { CellValue, GridColumn, GridRow, GridSelection } from "./types"

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
  return null
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

/**
 * The block an action applies to: the cell range when there is one, otherwise
 * the rows ticked in the selector column across every visible column,
 * otherwise the one active cell.
 */
export function selectedBlock(
  model: GridModel,
  selection: GridSelection,
): { rows: number[]; cols: OrderedColumn[] } | null {
  const range = rangeOf(selection)
  if (range && isRange(selection)) {
    const rows: number[] = []
    for (let row = range.top; row <= range.bottom; row++) rows.push(row)
    return { rows, cols: model.ordered.slice(range.left, range.right + 1) }
  }
  const ticked = tickedRows(model, selection)
  if (ticked.length > 0) return { rows: ticked, cols: [...model.ordered] }
  if (!range) return null
  return { rows: [range.top], cols: model.ordered.slice(range.left, range.left + 1) }
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
