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
  /**
   * By table (`tableKey`): whether it holds more matching rows than are
   * here. Known only where each table was read by itself; a search of a whole
   * schema says nothing per table, and a table that returned as many rows as
   * one table may is then taken to hold more.
   */
  more?: Record<string, boolean>
  /** By table: its primary key, where the read that found the rows also said it. */
  keys?: Record<string, string[]>
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
  /** The table holds more matching rows than these — or may, where nobody counted. */
  more: boolean
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
      group = { key, schema: match.schema, table: match.table, hits: [], columns: [], more: false }
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
  for (const group of groups.values()) {
    group.more = result.more?.[group.key] ?? group.hits.length >= PER_TABLE
  }
  return [...groups.values()]
}

/** A table's count as its chip and its head say it: "3", or "5+" where there are more than are shown. */
export const countText = (group: TableHits) =>
  group.more ? `${group.hits.length}+` : String(group.hits.length)

/** What a cell is, as far as its text says: enough to order a row's cells and to colour them. */
export type CellKind = "text" | "number" | "instant" | "flag" | "token"

const INSTANT = /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}/
const DAY = /^\d{4}-\d{2}-\d{2}$/
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const BYTES = /^\\x[0-9a-f]*|… \(\d+ bytes\)$/i
const NUMBER = /^-?\d+(?:\.\d+)?$/

/** A machine's value — a uuid, bytes, a long run of hex — that nobody recognises a row by. */
const isToken = (text: string) =>
  UUID.test(text) || BYTES.test(text) || /^[0-9a-f]{24,}$/i.test(text)

export function cellKind(value: CellValue): CellKind {
  if (typeof value === "boolean") return "flag"
  if (typeof value === "number") return "number"
  const text = cellText(value) ?? ""
  if (INSTANT.test(text) || DAY.test(text)) return "instant"
  if (isToken(text)) return "token"
  return NUMBER.test(text) ? "number" : "text"
}

export interface OtherCell {
  column: string
  text: string
  kind: CellKind
  /** One of the table's key columns. */
  key: boolean
}

/**
 * A few of a row's other cells, to recognise it by: its key first, then what
 * a person reads — short texts — then numbers, then instants. A uuid or a run
 * of bytes names a row to a machine and to nobody else, and is drawn only
 * when the row has nothing else to show. The cells already drawn as matches,
 * NULLs, documents and long texts are left out.
 */
export function otherCells(
  hit: Hit,
  primaryKey: readonly string[] | undefined,
  most = OTHERS,
): OtherCell[] {
  const row = hit.match.row ?? {}
  const matched = new Set(hit.cells.map((cell) => cell.column))
  const key = (primaryKey ?? []).filter((column) => column in row)
  const cells: OtherCell[] = []
  for (const column of Object.keys(row)) {
    if (matched.has(column)) continue
    const value = row[column]
    const text = cellText(value)
    if (text === null || text === "" || text.length > 48 || /^[[{]/.test(text)) continue
    cells.push({ column, text, kind: cellKind(value), key: key.includes(column) })
  }
  const rank = (cell: OtherCell) =>
    cell.key
      ? key.indexOf(cell.column) - key.length
      : { text: 0, number: 1, instant: 2, flag: 3, token: 4 }[cell.kind]
  // A stable sort: within a rank the row's own order stands.
  const ordered = cells
    .map((cell, at) => ({ cell, at }))
    .sort((a, b) => rank(a.cell) - rank(b.cell) || a.at - b.at)
    .map((entry) => entry.cell)
  const readable = ordered.filter((cell) => cell.key || cell.kind !== "token")
  return (readable.length > 0 ? readable : ordered).slice(0, most)
}

/** The most conditions one address carries (the server's own limit). */
const MAX_CONDITIONS = 12

/** A value as a condition can state it; null for one that cannot be compared as shown. */
function comparable(value: CellValue): string | null {
  if (typeof value === "boolean") return value ? "1" : "0"
  if (typeof value === "number") return Number.isFinite(value) ? String(value) : null
  if (typeof value !== "string") return null
  // A long text, a document, an array and the start of a cut value are not compared by equality.
  if (value.length > 128 || /^[[{]/.test(value) || /… \(\d+ bytes\)$/.test(value)) return null
  return value
}

/** Whether a row can be found again exactly: its table has a key, and the row's key can be stated. */
export function isKeyed(match: SearchMatch, primaryKey: readonly string[] | undefined): boolean {
  const row = match.row ?? {}
  return (
    primaryKey !== undefined &&
    primaryKey.length > 0 &&
    primaryKey.every((column) => comparable(row[column] ?? null) !== null)
  )
}

/**
 * The conditions that find a matched row again in the table editor.
 *
 * With the table's key in hand, the key: that is the row and no other. With
 * no key known — a table that has none, or an engine whose key repeats — the
 * row's own short values: the matched cell first, then its texts, then its
 * numbers. An instant is left out: each engine reads a moment written as text
 * its own way (ClickHouse refuses the form the row was handed over in), and a
 * condition the engine cannot read opens the table on a refusal instead of on
 * the row. What these conditions find is the rows like this one — which is
 * all a table with nothing to tell its rows apart allows.
 */
export function rowFilters(
  match: SearchMatch,
  primaryKey: readonly string[] | undefined,
): Filter[] {
  const row = match.row ?? {}
  if (primaryKey && isKeyed(match, primaryKey)) {
    return primaryKey.map((column) => ({
      column,
      op: "eq",
      value: comparable(row[column] ?? null)!,
    }))
  }
  const stated: { column: string; value: string; rank: number }[] = []
  for (const column of Object.keys(row)) {
    const value = row[column]
    if (value === null) continue
    const text = comparable(value)
    if (text === null) continue
    const kind = cellKind(value)
    // A day alone is read the same everywhere; a moment is not.
    if (kind === "instant" && !DAY.test(text)) continue
    stated.push({
      column,
      value: text,
      rank: column === match.column ? 0 : kind === "text" ? 1 : kind === "number" ? 2 : 3,
    })
  }
  return stated
    .map((entry, at) => ({ entry, at }))
    .sort((a, b) => a.entry.rank - b.entry.rank || a.at - b.at)
    .slice(0, MAX_CONDITIONS)
    .map(({ entry }) => ({ column: entry.column, op: "eq" as const, value: entry.value }))
}

/** Every row of a table that holds the value in one of the columns it was found in. */
export function tableFilters(group: TableHits, needle: string): Filter[] {
  return group.columns
    .slice(0, MAX_CONDITIONS)
    .map((column) => ({ column, op: "icontains", value: needle }))
}

/* ------------------------------------------------- one table at a time */

/** The part of `GET /browse` a scan of one table reads. */
export interface BrowseAnswer {
  columns?: string[]
  rows?: CellValue[][]
  truncated?: boolean
  primaryKey?: string[]
}

/** The conditions that find a value anywhere in a table: any of its columns holds it, any case. */
export function scanFilters(columns: readonly string[], needle: string): Filter[][] {
  const chunks: Filter[][] = []
  for (let at = 0; at < columns.length; at += MAX_CONDITIONS) {
    chunks.push(
      columns
        .slice(at, at + MAX_CONDITIONS)
        .map((column) => ({ column, op: "icontains" as const, value: needle })),
    )
  }
  return chunks
}

/**
 * The matches of one table, from the reads that scanned it: a table with more
 * columns than one read may name is read in several, and a row found by two
 * of them is one row. No more than a table's share is kept, and whether there
 * were more is said.
 */
export function tableMatches(
  schema: string,
  table: string,
  answers: readonly BrowseAnswer[],
  needle: string,
): { matches: SearchMatch[]; more: boolean; key: string[] } {
  const seen = new Set<string>()
  const matches: SearchMatch[] = []
  let more = false
  let key: string[] = []
  const find = needle.toLowerCase()
  for (const answer of answers) {
    const columns = Array.isArray(answer.columns) ? answer.columns : []
    if (answer.truncated) more = true
    if (Array.isArray(answer.primaryKey) && answer.primaryKey.length > 0) key = answer.primaryKey
    for (const cells of Array.isArray(answer.rows) ? answer.rows : []) {
      const row: Record<string, CellValue> = {}
      columns.forEach((column, at) => (row[column] = cells[at] ?? null))
      const id = JSON.stringify(cells)
      if (seen.has(id)) continue
      seen.add(id)
      if (matches.length >= PER_TABLE) {
        more = true
        continue
      }
      const column =
        columns.find((name) => (cellText(row[name]) ?? "").toLowerCase().includes(find)) ??
        columns[0] ??
        ""
      matches.push({
        schema,
        table,
        column,
        value: excerpt(cellText(row[column]) ?? "", needle),
        row,
      })
    }
  }
  return { matches, more, key }
}
