import type { GridColumn, GridColumnKind, GridLayout } from "./types"

/**
 * Which columns are drawn, in what order, how wide and where.
 *
 * A layout is what the reader did — dragged a column, hid one, pinned one —
 * stored by column key and applied to whatever columns exist now. Everything
 * here tolerates a layout that names columns the table no longer has, and
 * columns the layout has never heard of: a remembered arrangement outlives the
 * schema it was made for, and the alternative is a grid that opens broken.
 */

export const EMPTY_LAYOUT: GridLayout = { order: [], widths: {}, hidden: [], pinned: [] }

/** The header row's height in pixels. Fixed, like the rows under it. */
export const HEADER_HEIGHT = 36

export const MIN_COLUMN_WIDTH = 48
export const MAX_COLUMN_WIDTH = 960
/** The widest a column grows on its own — by default, or when fitted to its content. */
export const MAX_AUTO_WIDTH = 520

const KIND_WIDTH: Record<GridColumnKind, number> = {
  number: 112,
  boolean: 88,
  date: 116,
  time: 112,
  datetime: 208,
  uuid: 292,
  json: 260,
  array: 200,
  binary: 220,
  enum: 132,
  text: 208,
  unknown: 168,
}

/** Rough advance of one character of the header's type, in pixels. */
const HEADER_CHAR = 7
const TYPE_CHAR = 6

export function clampWidth(width: number): number {
  if (!Number.isFinite(width)) return MIN_COLUMN_WIDTH
  return Math.round(Math.min(Math.max(width, MIN_COLUMN_WIDTH), MAX_COLUMN_WIDTH))
}

/**
 * The width a column opens at: wide enough for its kind of value and for its
 * own name and type, so a header is never cut off before anything was dragged.
 */
export function defaultWidth(column: GridColumn): number {
  if (column.width) return clampWidth(column.width)
  const marks = (column.primaryKey ? 16 : 0) + (column.foreignKey ? 16 : 0)
  const header =
    column.name.length * HEADER_CHAR + Math.min(column.typeName.length, 18) * TYPE_CHAR + marks + 56
  return clampWidth(Math.min(Math.max(KIND_WIDTH[column.kind], header), MAX_AUTO_WIDTH))
}

export interface OrderedColumn {
  column: GridColumn
  /** Where the column's value sits in a row array. */
  source: number
  /** Position among the visible columns: the `col` of a cell reference. */
  index: number
  pinned: boolean
}

export interface ResolvedColumn extends OrderedColumn {
  width: number
  /** Distance from the first visible column's left edge to this one's. */
  offset: number
}

/**
 * The visible columns in the order they are drawn: pinned first, each group in
 * the layout's order, columns the layout does not mention after the ones it does.
 *
 * Widths are deliberately not part of this. Which columns exist and where they
 * stand changes rarely; a width changes on every frame of a drag, and a row
 * that was handed both in one object would be redrawn sixty times a second.
 */
export function orderColumns(
  columns: readonly GridColumn[],
  layout: Pick<GridLayout, "order" | "hidden" | "pinned">,
): OrderedColumn[] {
  const hidden = new Set(layout.hidden)
  const pinned = new Set(layout.pinned)
  const position = new Map<string, number>()
  layout.order.forEach((key, i) => {
    if (!position.has(key)) position.set(key, i)
  })

  const entries = columns
    .map((column, source) => ({ column, source }))
    .filter(({ column }) => !hidden.has(column.key))
  // Named columns by their place in the layout, the rest after them in the
  // order the table declares.
  const rank = (entry: { column: GridColumn; source: number }) =>
    position.get(entry.column.key) ?? layout.order.length + entry.source
  entries.sort((a, b) => {
    const pin = Number(pinned.has(b.column.key)) - Number(pinned.has(a.column.key))
    return pin !== 0 ? pin : rank(a) - rank(b)
  })
  return entries.map(({ column, source }, index) => ({
    column,
    source,
    index,
    pinned: pinned.has(column.key),
  }))
}

/** Each ordered column's width: what the reader dragged it to, or what it opens at. */
export function columnWidths(
  ordered: readonly OrderedColumn[],
  widths: Readonly<Record<string, number>>,
): number[] {
  return ordered.map(({ column }) => clampWidth(widths[column.key] ?? defaultWidth(column)))
}

/** Order and widths together, with each column's distance from the left. */
export function resolveColumns(
  columns: readonly GridColumn[],
  layout: GridLayout,
): ResolvedColumn[] {
  const ordered = orderColumns(columns, layout)
  const widths = columnWidths(ordered, layout.widths)
  let offset = 0
  return ordered.map((entry, i) => {
    const resolved = { ...entry, width: widths[i], offset }
    offset += widths[i]
    return resolved
  })
}

/** Every column key in display order, hidden ones included where they would stand. */
export function displayOrder(columns: readonly GridColumn[], layout: GridLayout): string[] {
  const known = new Set(columns.map((column) => column.key))
  const named = layout.order.filter((key, i) => known.has(key) && layout.order.indexOf(key) === i)
  const seen = new Set(named)
  return [...named, ...columns.map((column) => column.key).filter((key) => !seen.has(key))]
}

export function setColumnWidth(layout: GridLayout, key: string, width: number): GridLayout {
  return { ...layout, widths: { ...layout.widths, [key]: clampWidth(width) } }
}

export function resetColumnWidth(layout: GridLayout, key: string): GridLayout {
  if (!(key in layout.widths)) return layout
  const widths = { ...layout.widths }
  delete widths[key]
  return { ...layout, widths }
}

/**
 * Moves a column to stand before another (or last, with `before` null).
 *
 * A column dropped among the pinned ones becomes pinned and one dragged out of
 * them is released, so the drop lands where the reader put it instead of
 * snapping back to its own group.
 */
export function moveColumn(
  columns: readonly GridColumn[],
  layout: GridLayout,
  key: string,
  before: string | null,
): GridLayout {
  if (key === before) return layout
  const order = displayOrder(columns, layout).filter((entry) => entry !== key)
  const at = before === null ? -1 : order.indexOf(before)
  if (at < 0) order.push(key)
  else order.splice(at, 0, key)

  const pinned = new Set(layout.pinned)
  const target = before === null ? false : pinned.has(before)
  if (target) pinned.add(key)
  else pinned.delete(key)
  return { ...layout, order, pinned: order.filter((entry) => pinned.has(entry)) }
}

export function setColumnHidden(layout: GridLayout, key: string, hidden: boolean): GridLayout {
  const has = layout.hidden.includes(key)
  if (has === hidden) return layout
  return {
    ...layout,
    hidden: hidden ? [...layout.hidden, key] : layout.hidden.filter((entry) => entry !== key),
  }
}

export function setColumnPinned(layout: GridLayout, key: string, pinned: boolean): GridLayout {
  const has = layout.pinned.includes(key)
  if (has === pinned) return layout
  return {
    ...layout,
    pinned: pinned ? [...layout.pinned, key] : layout.pinned.filter((entry) => entry !== key),
  }
}

/**
 * Drops what a layout says about columns that no longer exist. Worth doing
 * before storing one, so a table that is altered every week does not grow a
 * layout that names every column it ever had.
 */
export function pruneLayout(columns: readonly GridColumn[], layout: GridLayout): GridLayout {
  const known = new Set(columns.map((column) => column.key))
  return {
    order: layout.order.filter((key) => known.has(key)),
    hidden: layout.hidden.filter((key) => known.has(key)),
    pinned: layout.pinned.filter((key) => known.has(key)),
    widths: Object.fromEntries(Object.entries(layout.widths).filter(([key]) => known.has(key))),
  }
}

/** Whether a stored value is a layout at all — it comes back from localStorage. */
export function isLayout(value: unknown): value is GridLayout {
  if (typeof value !== "object" || value === null) return false
  const layout = value as Partial<GridLayout>
  const strings = (list: unknown) =>
    Array.isArray(list) && list.every((entry) => typeof entry === "string")
  return (
    strings(layout.order) &&
    strings(layout.hidden) &&
    strings(layout.pinned) &&
    typeof layout.widths === "object" &&
    layout.widths !== null &&
    Object.values(layout.widths).every((width) => typeof width === "number")
  )
}

/**
 * The width that fits a column's content: the widest of its header and the
 * texts it currently shows, plus the cell's own padding, held to the range a
 * column may take on its own. `measure` returns a text's width in pixels —
 * a canvas in the browser, anything at all in a test.
 */
export function fitWidth(
  texts: readonly string[],
  measure: (text: string) => number,
  options: { padding: number; header: number },
): number {
  let widest = options.header
  for (const text of texts) {
    const width = measure(text) + options.padding
    if (width > widest) widest = width
  }
  return Math.round(Math.min(Math.max(widest, MIN_COLUMN_WIDTH), MAX_AUTO_WIDTH))
}

/**
 * Where a dragged column would land: the key of the column it goes before, or
 * null for the end. `x` is the pointer in the grid's own content coordinates
 * (the gutter excluded); the drop goes before whichever column's middle it has
 * not yet passed.
 */
export function dropTarget(columns: readonly ResolvedColumn[], x: number): string | null {
  for (const entry of columns) {
    if (x < entry.offset + entry.width / 2) return entry.column.key
  }
  return null
}
