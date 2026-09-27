import type { DbDriver, DbHistoryEntry, DbQueryEntry, DbQueryLog } from "@/lib/types"

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

/**
 * Each engine in its own vocabulary: Redis runs commands and Mongo
 * operations, and a list called "Slow statements" over SLOWLOG reads as
 * someone else's page.
 */
export function queryNoun(driver: DbDriver): QueryNoun {
  switch (driver) {
    case "redis":
      return { view: "Commands", title: "Slow commands", one: "command", many: "commands" }
    case "mongodb":
      return { view: "Queries", title: "Slow operations", one: "operation", many: "operations" }
    case "mysql":
      return { view: "Queries", title: "Slow queries", one: "query", many: "queries" }
    case "clickhouse":
      // system.query_log holds every query, slow or not.
      return { view: "Queries", title: "Queries", one: "query", many: "queries" }
    case "sqlite":
      return { view: "Queries", title: "Run from here", one: "statement", many: "statements" }
  }
  return { view: "Queries", title: "Slow statements", one: "statement", many: "statements" }
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

/** How many of what, in the view's own noun: "1 slow statement", "12 commands". */
export function countWords(n: number, noun: QueryNoun): string {
  return `${n.toLocaleString()} ${n === 1 ? noun.one : noun.many}`
}
