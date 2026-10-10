/**
 * The vocabulary every file in `grid/` shares.
 *
 * Kept free of React and of anything outside this directory, so the pure
 * modules (change set, clipboard, selection, sizing, windowing) can be loaded
 * by `bun test` without a DOM.
 */

/**
 * One value exactly as the API sent it.
 *
 * 64-bit integers and exact decimals arrive as strings and stay strings from
 * here to the request that writes them back: a `number` is a float column and
 * nothing else. Timestamps are RFC 3339 text, binary is `\x` + hex (possibly a
 * preview), and only ClickHouse hands over real arrays and maps.
 */
export type CellValue =
  null | string | number | boolean | CellValue[] | { [key: string]: CellValue }

/** A row as the API sent it: one value per column, in the columns' own order. */
export type GridRow = readonly CellValue[]

/** What a column holds, in the grid's vocabulary. See `kinds.ts` for the mapping. */
export type GridColumnKind =
  | "number"
  | "text"
  | "boolean"
  | "datetime"
  | "date"
  | "time"
  | "json"
  | "binary"
  | "uuid"
  | "enum"
  | "array"
  | "unknown"

export interface GridForeignKey {
  schema?: string
  table: string
  column: string
}

export interface GridColumn {
  /**
   * What identifies the column everywhere a column is named: layout, sort,
   * the change set. A table uses the column's name; a query result uses the
   * column's index, because `SELECT a.id, b.id` has two columns called `id`.
   */
  key: string
  name: string
  /** The engine's own type name, drawn in the header and used to validate a number. */
  typeName: string
  kind: GridColumnKind
  nullable?: boolean
  primaryKey?: boolean
  foreignKey?: GridForeignKey
  /** Labels in order, when the column is an enum. */
  enumValues?: readonly string[]
  /** Computed by the engine: never editable, never part of an insert. */
  generated?: boolean
  /** The SQL that follows DEFAULT. Its presence is what offers "Set default". */
  defaultExpr?: string
  comment?: string
  /** Starting width in pixels, before the reader has dragged anything. */
  width?: number
  /** `false` locks one column of an otherwise editable grid. */
  editable?: boolean
}

/** A cell the server cut for display. Indexes are into `rows` and `columns` as given. */
export interface ClippedCell {
  row: number
  column: number
  /** The whole value's size in bytes. */
  size: number
}

/** One key of a multi-column sort, in priority order. */
export interface GridSortKey {
  column: string
  desc: boolean
}

export type GridSort = readonly GridSortKey[]

/**
 * How the reader arranged the columns. Everything is by column key, and every
 * list tolerates keys that no longer exist — a remembered layout outlives the
 * table it was made for.
 */
export interface GridLayout {
  /** Display order. Columns it does not mention follow in their natural order. */
  order: string[]
  widths: Record<string, number>
  hidden: string[]
  /** Pinned to the left edge, drawn in display order. */
  pinned: string[]
}

/** A position in the grid as drawn: display row, visible column. Row -1 is the header. */
export interface GridCellRef {
  row: number
  col: number
}

export interface GridSelection {
  /** The one cell keys act on. */
  active: GridCellRef | null
  /** The corner a range was started from; null when only the active cell is selected. */
  anchor: GridCellRef | null
  /** Rows chosen through the selector column, by row id. */
  rows: readonly string[]
}

/** An inclusive rectangle of cells, already normalised. */
export interface GridRange {
  top: number
  left: number
  bottom: number
  right: number
}

/** What a quick filter from a cell asks the owner for. */
export interface GridFilterRequest {
  column: GridColumn
  op: "eq" | "ne" | "is_null" | "not_null"
  /** Absent for the two NULL tests. */
  value?: CellValue
}

/** A cell of a copied block, by its place in the block. */
export interface GridBlockCell {
  row: number
  column: number
}

/** The rows and columns an action was taken on, as raw values. */
export interface GridSelectionData {
  columns: GridColumn[]
  /** One entry per row, values aligned to `columns`. Pending edits are included. */
  rows: CellValue[][]
  /** The ids of those rows, aligned to `rows`. */
  rowIds: string[]
  /**
   * The cells of `rows` that hold only the start of their value, because the
   * server cut it. What is in them must not be written anywhere as if it were
   * the value.
   */
  previews: GridBlockCell[]
}

/** A row handed to the owner: its id, where it is drawn, and its values as drawn. */
export interface GridRowRef {
  id: string
  index: number
  /** Aligned to `columns`, with staged edits applied. */
  values: CellValue[]
  /** The row as the server sent it; null for a row that is only staged. */
  original: GridRow | null
  /** Keys of the columns whose value here is only its start; fetch those whole before showing them. */
  previews: string[]
}

export interface GridForeignKeyTarget {
  column: GridColumn
  foreignKey: GridForeignKey
  value: CellValue
  row: GridRowRef
}
