import { compareValues, searchText } from "./values"
import type { GridColumn, GridRow, GridSort, GridSortKey } from "./types"

/**
 * Sort keys, and the two things a grid can do with a result it already holds:
 * order it and search it. A table that is paged from the server does neither
 * here — sorting one page of nine thousand rows misleads — so these are for
 * query results, where every row there is has already arrived.
 */

/**
 * A click on a column name: ascending, then descending, then not sorted.
 *
 * Plain, it replaces the sort with that one column. With Shift it adds the
 * column to the keys already there, or cycles it where it stands, which is how
 * a second and third key are built up.
 */
export function cycleSort(sort: GridSort, column: string, additive: boolean): GridSortKey[] {
  const current = sort.find((key) => key.column === column)
  const next: GridSortKey | null = !current
    ? { column, desc: false }
    : current.desc
      ? null
      : { column, desc: true }
  if (!additive) return next ? [next] : []
  if (!current) return next ? [...sort, next] : [...sort]
  return sort.flatMap((key) => (key.column === column ? (next ? [next] : []) : [key]))
}

/** Sets one column's direction outright, from a menu rather than a click. */
export function setSort(
  sort: GridSort,
  column: string,
  desc: boolean | null,
  additive: boolean,
): GridSortKey[] {
  const rest = sort.filter((key) => key.column !== column)
  if (desc === null) return rest
  if (!additive) return [{ column, desc }]
  const at = sort.findIndex((key) => key.column === column)
  if (at < 0) return [...rest, { column, desc }]
  return sort.map((key) => (key.column === column ? { column, desc } : key))
}

/** A column's place in the sort: its direction and, with several keys, its rank from 1. */
export function sortState(
  sort: GridSort,
  column: string,
): { desc: boolean; order: number | null } | null {
  const at = sort.findIndex((key) => key.column === column)
  if (at < 0) return null
  return { desc: sort[at].desc, order: sort.length > 1 ? at + 1 : null }
}

/**
 * The order to draw rows in, as indexes into `rows`. Stable: rows that compare
 * equal keep the order the result arrived in, which is the engine's.
 */
export function sortedIndexes(
  rows: readonly GridRow[],
  columns: readonly GridColumn[],
  sort: GridSort,
  indexes?: readonly number[],
): number[] {
  const order = indexes ? [...indexes] : rows.map((_, i) => i)
  const keys = sort
    .map((key) => {
      const source = columns.findIndex((column) => column.key === key.column)
      return source < 0 ? null : { source, desc: key.desc, kind: columns[source].kind }
    })
    .filter((key) => key !== null)
  if (keys.length === 0) return order
  return order.sort((a, b) => {
    for (const key of keys) {
      const x = rows[a][key.source] ?? null
      const y = rows[b][key.source] ?? null
      // NULL stays last in both directions rather than flipping to the top.
      if (x === null || y === null) {
        if (x === y) continue
        return x === null ? 1 : -1
      }
      const result = compareValues(x, y, key.kind)
      if (result !== 0) return key.desc ? -result : result
    }
    return a - b
  })
}

/**
 * The rows a find keeps: those with the text somewhere in them, in any column,
 * whatever its case. Returns indexes into `rows`, in their original order.
 */
export function findIndexes(rows: readonly GridRow[], query: string): number[] {
  const needle = query.trim().toLowerCase()
  const all = rows.map((_, i) => i)
  if (!needle) return all
  return all.filter((i) =>
    rows[i].some((value) => searchText(value).toLowerCase().includes(needle)),
  )
}
