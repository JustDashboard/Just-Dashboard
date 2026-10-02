import type { DbHistoryEntry, DbQueryEntry, DbQueryLog } from "@/lib/types"
import type { Engine } from "@/components/database/engine"

/**
 * The Queries view's decisions, kept apart from its drawing: what each
 * engine calls the statements it records, where a list was read from in the
 * words the reader knows it by, the window either side of a moment, and the
 * dashboard's own statement history in the same shape as a server's.
 */

export type QueryNoun = {
  /** The view's name in the pane's strip. */
  view: string
  /** The list's title. */
  title: string
  /** One of them, for counts and sentences. */
  one: string
  many: string
}

/** As much of an engine as the vocabulary is read from: the registry's entry, or a part of it. */
export type QueryEngine = Pick<Engine, "kind" | "nouns" | "can">

/** Where a server says its list was read: a table that holds queries, not a log of statements. */
const QUERY_TABLES: ReadonlySet<DbQueryLog["source"]> = new Set([
  "slow_log",
  "statements_history",
  "query_log",
])

/**
 * Each engine in its own vocabulary: Redis runs commands and Mongo
 * operations, and a list called "Slow statements" over SLOWLOG reads as
 * someone else's page.
 *
 * The words come from the registry — what the engine calls a statement, and
 * whether it is a server at all — and from the server's own answer: a list
 * read from `mysql.slow_log` or `system.query_log` is a list of queries in
 * the engine's own name for it, and `query_log` holds every query, slow or
 * not. Until a list has answered, an engine is read by the registry's word.
 */
export function queryNoun(engine: QueryEngine, source?: DbQueryLog["source"]): QueryNoun {
  const queries = source !== undefined && QUERY_TABLES.has(source)
  const one = queries ? "query" : engine.nouns.statement
  const many = queries ? "queries" : engine.nouns.statements
  const capital = (word: string) => word[0].toUpperCase() + word.slice(1)
  return {
    // A key–value store has no queries: its view is named for what it runs.
    view: engine.kind === "keyvalue" ? capital(engine.nouns.statements) : "Queries",
    title: !engine.can("server")
      ? // A file has no server keeping a log: its list is what was run from here.
        "Run from here"
      : source === "query_log"
        ? "Queries"
        : `Slow ${many}`,
    one,
    many,
  }
}

const SOURCE_WORDS: Record<DbQueryLog["source"], string> = {
  log: "the server log",
  slowlog: "SLOWLOG",
  query_log: "system.query_log",
  slow_log: "mysql.slow_log",
  statements_history: "performance_schema's recent statements",
  "": "",
}

/** Where the rows were read, as the reader would look for them by hand. */
export function sourceWords(source: DbQueryLog["source"]): string {
  return SOURCE_WORDS[source] ?? source
}

/**
 * A statement on one line: a row is one line wide, and the line breaks in
 * SQL are layout the full text keeps.
 */
export function oneLine(sql: string): string {
  return sql.replace(/\s+/g, " ").trim()
}

/**
 * The statements this dashboard ran, as rows of the shape a server's log
 * gives: a SQLite file has no server to keep one, so this is the whole record.
 * A failure is said as one, since the history keeps no message.
 */
export function historyEntries(history: DbHistoryEntry[]): DbQueryEntry[] {
  return history.map((h) => ({
    at: h.ranAt,
    durationMs: h.durationMs,
    query: h.sql,
    rows: h.rowCount,
    error: h.success ? undefined : "failed",
  }))
}

/** A minute either side of a moment: the server log around one statement. */
export function around(at: string, seconds = 60): { since: string; until: string } {
  const t = Date.parse(at)
  return {
    since: new Date(t - seconds * 1000).toISOString(),
    until: new Date(t + seconds * 1000).toISOString(),
  }
}

export type QueryOrder = "latest" | "slowest"

export function sortEntries(entries: DbQueryEntry[], order: QueryOrder): DbQueryEntry[] {
  if (order === "latest") return entries
  return [...entries].sort((a, b) => b.durationMs - a.durationMs)
}

/** The windows the view offers, and how far back each reaches. */
export const QUERY_RANGES = [
  { id: "1h", label: "1h", minutes: 60 },
  { id: "24h", label: "24h", minutes: 24 * 60 },
  { id: "7d", label: "7d", minutes: 7 * 24 * 60 },
] as const

export type QueryRange = (typeof QUERY_RANGES)[number]["id"]

export function rangeSince(range: QueryRange, now = Date.now()): string {
  const minutes = QUERY_RANGES.find((r) => r.id === range)?.minutes ?? 24 * 60
  return new Date(now - minutes * 60_000).toISOString()
}

/**
 * How often a window is read again. The last hour moves while it is
 * watched; a day or a week read every half minute is the whole of it
 * searched again — the rotated files decompressed and all — for a row or
 * two at the top.
 */
export function refreshEvery(range: QueryRange): number {
  if (range === "1h") return 30_000
  return range === "24h" ? 120_000 : 300_000
}

/**
 * A key for each row that is the row's own rather than its place. A read
 * that finds a new statement puts it on top, and keys counted from the top
 * would move every row's by one and close the row being read. Only the same
 * statement recorded twice at the same moment for the same time shares one,
 * and those are told apart by their order among themselves.
 */
export function entryKeys(entries: DbQueryEntry[]): string[] {
  const seen = new Map<string, number>()
  return entries.map((entry) => {
    const key = `${entry.at}|${entry.durationMs}|${entry.query.slice(0, 200)}`
    const n = seen.get(key) ?? 0
    seen.set(key, n + 1)
    return n === 0 ? key : `${key}#${n}`
  })
}

/** How many of what, in the view's own noun: "1 slow statement", "12 commands". */
export function countWords(n: number, noun: QueryNoun): string {
  return `${n.toLocaleString()} ${n === 1 ? noun.one : noun.many}`
}
