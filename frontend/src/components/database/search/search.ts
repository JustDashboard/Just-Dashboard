import type { CellValue } from "@/components/database/grid"
import type { Filter } from "@/components/database/data/types"

/**
 * What a search of every table came back with, arranged to be read: the
 * matches of one table together, each row with the cells that hold the value
 * marked and enough of its other cells to recognise it — and, for each row,
 * the conditions that find it again in the table editor.
 */

export interface SearchMatch {
  schema: string
  table: string
  /** The first column of the row that holds the value. */
  column: string
  /** That cell, cut for display. */
  value: string
  row: Record<string, CellValue>
}

export interface SearchResult {
  matches: SearchMatch[]
  tablesScanned: number
  tablesSkipped?: string[]
  /** The search stopped at one of its bounds: there may be more. */
  truncated: boolean
}

/** A server without the route answers with a list; that is "nothing found", not a result to read. */
export function readResult(answer: unknown): SearchResult {
  const given = (
    answer && typeof answer === "object" && !Array.isArray(answer) ? answer : {}
  ) as Partial<SearchResult>
  return {
    matches: Array.isArray(given.matches) ? given.matches : [],
    tablesScanned: typeof given.tablesScanned === "number" ? given.tablesScanned : 0,
    tablesSkipped: Array.isArray(given.tablesSkipped) ? given.tablesSkipped : [],
    truncated: given.truncated === true,
  }
}

export interface Part {
  text: string
  hit: boolean
}

/** A text cut where the needle occurs, any case, so the occurrences can be marked. */
export function markParts(text: string, needle: string): Part[] {
  const find = needle.toLowerCase()
  if (!find) return [{ text, hit: false }]
  const lower = text.toLowerCase()
  const parts: Part[] = []
  let at = 0
  for (;;) {
    const found = lower.indexOf(find, at)
    if (found < 0) break
    if (found > at) parts.push({ text: text.slice(at, found), hit: false })
    parts.push({ text: text.slice(found, found + find.length), hit: true })
    at = found + find.length
  }
  if (at < text.length) parts.push({ text: text.slice(at), hit: false })
  return parts.length > 0 ? parts : [{ text, hit: false }]
}

/** A cell as the text the search compared: NULL has none. */
export function cellText(value: CellValue): string | null {
  if (value === null || value === undefined) return null
  if (typeof value === "string") return value
  if (typeof value === "number" || typeof value === "boolean") return String(value)
  return JSON.stringify(value)
}

/** How much of a long cell is drawn around its first match. */
const WINDOW = 96

/** A long text cut to a window around its first match, with what was cut said by an ellipsis. */
export function excerpt(text: string, needle: string, width = WINDOW): string {
  if (text.length <= width) return text
  const found = text.toLowerCase().indexOf(needle.toLowerCase())
  if (found < 0) return `${text.slice(0, width - 1)}…`
  const start = Math.max(0, Math.min(found - Math.floor(width / 3), text.length - width))
  const end = Math.min(text.length, start + width)
  return `${start > 0 ? "…" : ""}${text.slice(start, end)}${end < text.length ? "…" : ""}`
}

export interface HitCell {
  column: string
  parts: Part[]
}

export interface Hit {
  match: SearchMatch
  /** The cells that hold the value, in the row's own order. */
  cells: HitCell[]
}

export interface TableHits {
  key: string
  schema: string
  table: string
  hits: Hit[]
  /** Every column of this table in which the value was found. */
  columns: string[]
}

/** The most rows the server hands back from one table: a table showing this many may hold more. */
export const PER_TABLE = 5

/** How many of a row's other cells are drawn beside its matches. */
const OTHERS = 5

export const tableKey = (schema: string, table: string) => `${schema}\u0000${table}`

/** The matches by table, in the order the tables were first found. */
export function groupHits(result: SearchResult, needle: string): TableHits[] {
  const groups = new Map<string, TableHits>()
  for (const match of result.matches) {
    const key = tableKey(match.schema, match.table)
    let group = groups.get(key)
    if (!group) {
      group = { key, schema: match.schema, table: match.table, hits: [], columns: [] }
      groups.set(key, group)
    }
    const cells: HitCell[] = []
    for (const [column, value] of Object.entries(match.row ?? {})) {
      const text = cellText(value)
      if (text === null) continue
      if (needle && text.toLowerCase().includes(needle.toLowerCase())) {
        cells.push({ column, parts: markParts(excerpt(text, needle), needle) })
        if (!group.columns.includes(column)) group.columns.push(column)
      }
    }
    // The server found the row by a text form the page does not have (a date
    // as the engine casts it): its own word on which cell matched stands.
    if (cells.length === 0) {
      cells.push({ column: match.column, parts: markParts(match.value, needle) })
      if (match.column && !group.columns.includes(match.column)) group.columns.push(match.column)
    }
    group.hits.push({ match, cells })
  }
  return [...groups.values()]
}

/**
 * A few of a row's other cells, to recognise it by: its key first, then its
 * short values in the row's own order. The cells already drawn as matches,
 * NULLs and long texts are left out.
 */
export function otherCells(
  hit: Hit,
  primaryKey: readonly string[] | undefined,
  most = OTHERS,
): { column: string; text: string }[] {
  const row = hit.match.row ?? {}
  const matched = new Set(hit.cells.map((cell) => cell.column))
  const key = (primaryKey ?? []).filter((column) => column in row)
  const order = [...key, ...Object.keys(row).filter((column) => !key.includes(column))]
  const cells: { column: string; text: string }[] = []
  for (const column of order) {
    if (cells.length >= most) break
    if (matched.has(column)) continue
    const text = cellText(row[column])
    if (text === null || text === "" || text.length > 48) continue
    cells.push({ column, text })
  }
  return cells
}

/** The most conditions one address carries (the server's own limit). */
const MAX_CONDITIONS = 12

/** A value as a condition can state it; null for one that cannot be compared as shown. */
function comparable(value: CellValue): string | null {
  if (typeof value === "boolean") return value ? "1" : "0"
  if (typeof value !== "string") return null
  // A long text, a document, an array and the start of a cut value are not compared by equality.
  if (value.length > 128 || /^[[{]/.test(value) || /… \(\d+ bytes\)$/.test(value)) return null
  return value
}

/**
 * The conditions that find a matched row again in the table editor.
 *
 * With the table's key in hand, the key: that is the row and no other. With
 * no key known — a table that has none, or an engine whose key repeats — the
 * row's own short values, the matched cell first: as close to "this row" as a
 * table with nothing to tell its rows apart allows.
 */
export function rowFilters(
  match: SearchMatch,
  primaryKey: readonly string[] | undefined,
): Filter[] {
  const row = match.row ?? {}
  if (primaryKey && primaryKey.length > 0) {
    const key = primaryKey.map((column) => ({ column, value: comparable(row[column] ?? null) }))
    if (key.every((entry) => entry.value !== null)) {
      return key.map((entry) => ({ column: entry.column, op: "eq", value: entry.value! }))
    }
  }
  const filters: Filter[] = []
  const columns = [match.column, ...Object.keys(row).filter((column) => column !== match.column)]
  for (const column of columns) {
    if (filters.length >= MAX_CONDITIONS || !(column in row)) continue
    const value = row[column]
    if (value === null) continue
    const text = comparable(value)
    if (text !== null) filters.push({ column, op: "eq", value: text })
  }
  return filters
}

/** Every row of a table that holds the value in one of the columns it was found in. */
export function tableFilters(group: TableHits, needle: string): Filter[] {
  return group.columns
    .slice(0, MAX_CONDITIONS)
    .map((column) => ({ column, op: "icontains", value: needle }))
}
