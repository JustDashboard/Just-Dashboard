import { aggregate, groupDigits, type Aggregate } from "./decimal"
import { rangeSize } from "./selection"
import type { EditValue } from "./values"
import type { GridColumnKind, GridRange } from "./types"

/**
 * What the status line says about the grid and the selection in it.
 *
 * Computed here, as data, so the owner can draw it somewhere else — or beside
 * its own pagination — instead of only wherever the grid would have put it.
 */

export interface GridStatus {
  /** Rows drawn, after a find. */
  rows: number
  /** Rows there are, before a find. Equal to `rows` when nothing is filtered. */
  totalRows: number
  /** Rows chosen through the selector column. */
  selectedRows: number
  /** Cells in the range; 0 or 1 means there is no range to speak of. */
  selectedCells: number
  /** Figures for the numbers in the range, when it holds at least two. */
  aggregate: Aggregate | null
}

/** Past this many cells the figures are skipped: a status line is not worth a stalled frame. */
export const AGGREGATE_CELL_LIMIT = 250_000

/**
 * Count, sum, average, minimum and maximum over the numeric columns a range
 * covers. `valueAt` reads a cell as it is drawn, pending edits included.
 */
export function rangeAggregate(
  range: GridRange | null,
  kinds: readonly GridColumnKind[],
  valueAt: (row: number, col: number) => EditValue | undefined,
): Aggregate | null {
  const size = rangeSize(range)
  if (!range || size.cells < 2 || size.cells > AGGREGATE_CELL_LIMIT) return null
  const numeric: number[] = []
  for (let col = range.left; col <= range.right; col++) {
    if (kinds[col] === "number") numeric.push(col)
  }
  if (numeric.length === 0) return null

  const { top, bottom } = range
  function* numbers() {
    for (let row = top; row <= bottom; row++) {
      for (const col of numeric) {
        const value = valueAt(row, col)
        if (typeof value === "string" || typeof value === "number") yield value
      }
    }
  }
  const result = aggregate(numbers())
  return result && result.count >= 2 ? result : null
}

/** The selection as one line of words, for the status strip and for a screen reader. */
export function describeStatus(status: GridStatus): string[] {
  const parts: string[] = []
  const count = (n: number, word: string) =>
    `${groupDigits(String(n))} ${word}${n === 1 ? "" : "s"}`
  parts.push(
    status.rows === status.totalRows
      ? count(status.rows, "row")
      : `${groupDigits(String(status.rows))} of ${count(status.totalRows, "row")}`,
  )
  if (status.selectedRows > 0) parts.push(`${count(status.selectedRows, "row")} selected`)
  if (status.selectedCells > 1) parts.push(`${count(status.selectedCells, "cell")} selected`)
  return parts
}

/** The aggregate as label/figure pairs, in the order a status line prints them. */
export function describeAggregate(result: Aggregate): [string, string][] {
  const figure = (text: string) => groupDigits(text)
  return [
    ["Sum", figure(result.sum)],
    ["Avg", figure(result.avg)],
    ["Min", figure(result.min)],
    ["Max", figure(result.max)],
  ]
}
