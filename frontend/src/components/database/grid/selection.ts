import type { GridCellRef, GridRange, GridSelection } from "./types"

/**
 * The arithmetic of a selection: where the active cell goes for a key, what
 * rectangle a range covers, which rows a shift-click takes in.
 *
 * Positions are a display row and a visible column, the header being row -1 —
 * so one function moves the cell whether the reader is in the body or on a
 * column name, and none of it knows what a row or a column *is*.
 */

export const EMPTY_SELECTION: GridSelection = { active: null, anchor: null, rows: [] }

/** The header row's index. */
export const HEADER_ROW = -1

export interface GridBounds {
  rows: number
  cols: number
}

function clampNumber(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max)
}

/** Holds a cell inside the grid. Null when the grid has no columns to stand in. */
export function clampCell(cell: GridCellRef, bounds: GridBounds): GridCellRef | null {
  if (bounds.cols <= 0) return null
  return {
    row: clampNumber(cell.row, HEADER_ROW, bounds.rows - 1),
    col: clampNumber(cell.col, 0, bounds.cols - 1),
  }
}

/** Brings a selection back inside a grid that has changed shape under it. */
export function clampSelection(selection: GridSelection, bounds: GridBounds): GridSelection {
  const active = selection.active ? clampCell(selection.active, bounds) : null
  const anchor = active && selection.anchor ? clampCell(selection.anchor, bounds) : null
  const unchanged = sameCell(active, selection.active) && sameCell(anchor, selection.anchor)
  return unchanged ? selection : { ...selection, active, anchor }
}

export function sameCell(a: GridCellRef | null, b: GridCellRef | null): boolean {
  if (a === null || b === null) return a === b
  return a.row === b.row && a.col === b.col
}

/**
 * The rectangle a selection covers in the body. The header is never part of a
 * range: a selection that stands on it covers nothing.
 */
export function rangeOf(selection: GridSelection): GridRange | null {
  const { active, anchor } = selection
  if (!active || active.row < 0) return null
  const from = anchor && anchor.row >= 0 ? anchor : active
  return {
    top: Math.min(from.row, active.row),
    bottom: Math.max(from.row, active.row),
    left: Math.min(from.col, active.col),
    right: Math.max(from.col, active.col),
  }
}

export function rangeContains(range: GridRange | null, row: number, col: number): boolean {
  return (
    range !== null &&
    row >= range.top &&
    row <= range.bottom &&
    col >= range.left &&
    col <= range.right
  )
}

/** How many cells, rows and columns a range spans. */
export function rangeSize(range: GridRange | null): { cells: number; rows: number; cols: number } {
  if (!range) return { cells: 0, rows: 0, cols: 0 }
  const rows = range.bottom - range.top + 1
  const cols = range.right - range.left + 1
  return { cells: rows * cols, rows, cols }
}

/** Whether more than the one active cell is selected. */
export function isRange(selection: GridSelection): boolean {
  return selection.anchor !== null && !sameCell(selection.anchor, selection.active)
}

export type Move =
  | "up"
  | "down"
  | "left"
  | "right"
  | "rowStart"
  | "rowEnd"
  | "gridStart"
  | "gridEnd"
  | "columnStart"
  | "columnEnd"
  | "pageUp"
  | "pageDown"
  | "next"
  | "previous"

/**
 * Where a cell goes for a movement.
 *
 * `next` and `previous` are Tab's: across the row, then on to the next one —
 * and null past the last cell, which is how Tab gets out of the grid instead of
 * being trapped in it. Every other move stops at the edge.
 */
export function moveCell(
  cell: GridCellRef,
  move: Move,
  bounds: GridBounds,
  page = 10,
): GridCellRef | null {
  const lastRow = bounds.rows - 1
  const lastCol = bounds.cols - 1
  if (bounds.cols <= 0) return null
  const { row, col } = cell
  switch (move) {
    case "up":
      return { row: Math.max(row - 1, HEADER_ROW), col }
    case "down":
      return { row: Math.min(row + 1, lastRow), col }
    case "left":
      return { row, col: Math.max(col - 1, 0) }
    case "right":
      return { row, col: Math.min(col + 1, lastCol) }
    case "rowStart":
      return { row, col: 0 }
    case "rowEnd":
      return { row, col: lastCol }
    case "columnStart":
      return { row: Math.min(0, lastRow), col }
    case "columnEnd":
      return { row: lastRow, col }
    case "gridStart":
      return { row: Math.min(0, lastRow), col: 0 }
    case "gridEnd":
      return { row: lastRow, col: lastCol }
    case "pageUp":
      // A page never lands on the header: paging is through the rows.
      return { row: row <= 0 ? row : Math.max(row - page, 0), col }
    case "pageDown":
      return { row: Math.min(Math.max(row, 0) + page, lastRow), col }
    case "next":
      if (col < lastCol) return { row, col: col + 1 }
      if (row < lastRow) return { row: row + 1, col: 0 }
      return null
    case "previous":
      if (col > 0) return { row, col: col - 1 }
      if (row > 0) return { row: row - 1, col: lastCol }
      return null
  }
}

/** Moves the active cell alone: the range collapses onto it. */
export function selectCell(selection: GridSelection, cell: GridCellRef): GridSelection {
  return { ...selection, active: cell, anchor: null }
}

/**
 * Grows the range to a cell, keeping the corner it was started from. The
 * header is not part of a range, so extending stops at the first row.
 */
export function extendTo(selection: GridSelection, cell: GridCellRef): GridSelection {
  if (!selection.active || selection.active.row < 0) return selectCell(selection, cell)
  const target = { row: Math.max(cell.row, 0), col: cell.col }
  const anchor = selection.anchor ?? selection.active
  return { ...selection, active: target, anchor: sameCell(anchor, target) ? null : anchor }
}

/** Every cell of the body. The active cell stays where it was, as the range's far corner. */
export function selectAll(selection: GridSelection, bounds: GridBounds): GridSelection {
  if (bounds.rows <= 0 || bounds.cols <= 0) return selection
  return {
    ...selection,
    anchor: { row: 0, col: 0 },
    active: { row: bounds.rows - 1, col: bounds.cols - 1 },
  }
}

/** A whole column as a cell range. */
export function selectColumn(
  selection: GridSelection,
  col: number,
  bounds: GridBounds,
): GridSelection {
  if (bounds.rows <= 0) return selection
  return { ...selection, anchor: { row: 0, col }, active: { row: bounds.rows - 1, col } }
}

/** Escape's first press: the range falls back to the active cell. */
export function collapse(selection: GridSelection): GridSelection {
  return selection.anchor ? { ...selection, anchor: null } : selection
}

/* ------------------------------------------------------- the selector column */

/** Adds or removes one row id. */
export function toggleRow(rows: readonly string[], id: string): string[] {
  return rows.includes(id) ? rows.filter((row) => row !== id) : [...rows, id]
}

/**
 * Shift-click in the selector column: every row between the last one clicked
 * and this one takes the state the click gives this one. `ids` is the page in
 * display order.
 */
export function toggleRowRange(
  rows: readonly string[],
  ids: readonly string[],
  from: number,
  to: number,
  select: boolean,
): string[] {
  const start = clampNumber(Math.min(from, to), 0, ids.length - 1)
  const end = clampNumber(Math.max(from, to), 0, ids.length - 1)
  const span = ids.slice(start, end + 1)
  if (!select) {
    const drop = new Set(span)
    return rows.filter((row) => !drop.has(row))
  }
  const have = new Set(rows)
  return [...rows, ...span.filter((id) => !have.has(id))]
}

/** The header checkbox: every row of the page, or none of them. */
export function toggleAllRows(rows: readonly string[], ids: readonly string[]): string[] {
  if (ids.length === 0) return []
  const have = new Set(rows)
  if (ids.every((id) => have.has(id))) {
    const drop = new Set(ids)
    return rows.filter((row) => !drop.has(row))
  }
  return [...rows, ...ids.filter((id) => !have.has(id))]
}

/** `true` when every row of the page is selected, `"mixed"` when some are. */
export function allRowsState(rows: readonly string[], ids: readonly string[]): boolean | "mixed" {
  if (rows.length === 0 || ids.length === 0) return false
  const have = new Set(rows)
  let count = 0
  for (const id of ids) if (have.has(id)) count++
  return count === 0 ? false : count === ids.length ? true : "mixed"
}

/**
 * The slice of a row's columns a range paints, for a row component that only
 * wants two numbers: `[-1, -1]` when the row is outside the range.
 */
export function rowSpan(range: GridRange | null, row: number): [number, number] {
  if (!range || row < range.top || row > range.bottom) return [-1, -1]
  return [range.left, range.right]
}
