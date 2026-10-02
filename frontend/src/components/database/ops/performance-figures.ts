import { duration } from "@/lib/format"
import type { Series } from "@/components/metrics/metric-chart"
import type { ChartUnit, ChartView } from "@/components/database/home/charts"
import {
  clickhouseReadings,
  compact,
  perSecond,
  sqlReadings,
  sqliteReadings,
  type Reading,
} from "@/components/database/home/readings"
import {
  TRANSACTIONS,
  counter,
  drawn,
  gauge,
  gauges,
  lastRate,
  rates,
  type Sample,
  type Source,
} from "@/components/database/home/samples"
import { THRESHOLDS } from "@/components/database/ops/performance-activity"
import type {
  DbServerStats,
  DbStatement,
  DbStatementSort,
} from "@/components/database/ops/performance-types"

/**
 * The Performance page's figures: its six readings, the charts of its
 * Overview, and how a statement's numbers are written.
 *
 * The readings and the charts are drawn from the same samples the database's
 * home keeps — one snapshot every few seconds, a rate the difference of two —
 * through the home's own arithmetic, so a figure here and the same figure
 * there are one calculation. What this page adds is the reading a home has no
 * room for: who is working, who is waiting on a lock, what has run longest.
 */

const SINCE_OPENED = "since this page was opened"

/** Sessions running a statement now. MySQL says it as threads where the session list is closed to the account. */
const WORKING = ["sessionsActive", "threadsRunning"] as const
/** Sessions waiting on a lock. SQL Server counts them itself. */
const WAITING = ["sessionsWaiting", "processesBlocked"] as const

function workingReading(samples: readonly Sample[]): Reading {
  const key = WORKING.find((name) => gauge(samples, name) !== undefined)
  const working = key ? gauge(samples, key) : undefined
  const inTransaction = gauge(samples, "sessionsIdleInTransaction")
  return {
    key: "working",
    label: "Working now",
    value: working === undefined ? undefined : compact(working),
    trailing: working === undefined ? undefined : working === 1 ? "session" : "sessions",
    trend: key
      ? {
          values: gauges(samples, key),
          color: "var(--chart-5)",
          label: `Sessions running a statement ${SINCE_OPENED}`,
        }
      : undefined,
    hint:
      working === undefined
        ? "Not reported to this account"
        : inTransaction === undefined
          ? "running a statement"
          : inTransaction > 0
            ? `${compact(inTransaction)} more idle in a transaction`
            : "none idle in a transaction",
  }
}

function waitingReading(samples: readonly Sample[]): Reading {
  const key = WAITING.find((name) => gauge(samples, name) !== undefined)
  const waiting = key ? gauge(samples, key) : undefined
  const deadlocks = counter(samples, "deadlocks")
  return {
    key: "waiting",
    label: "Waiting on a lock",
    value: waiting === undefined ? undefined : compact(waiting),
    tone: waiting !== undefined && waiting > 0 ? "warning" : "default",
    trend: key
      ? {
          values: gauges(samples, key),
          // The series colour a chart gives its trouble line, never a status
          // one: the tile's tone is what says something is wrong.
          color: "var(--chart-3)",
          label: `Sessions waiting on a lock ${SINCE_OPENED}`,
        }
      : undefined,
    hint:
      waiting === undefined
        ? "Not reported to this account"
        : deadlocks === undefined
          ? waiting > 0
            ? "see Locks for who holds them"
            : "nothing is held up"
          : `${compact(deadlocks)} ${deadlocks === 1 ? "deadlock" : "deadlocks"} since it started`,
  }
}

/**
 * The fourth figure is the engine's own: the statement that has run longest
 * where the server says (PostgreSQL), else statements a second where it
 * counts them, else rows read.
 */
function busiestReading(samples: readonly Sample[]): Reading {
  const longest = gauge(samples, "longestQuerySeconds")
  if (longest !== undefined) {
    const oldest = gauge(samples, "oldestTransactionSeconds")
    return {
      key: "longest",
      label: "Longest statement",
      value: longest >= 1 ? duration(longest) : "None",
      trailing: longest >= 1 ? "so far" : undefined,
      tone: longest >= THRESHOLDS.active ? "warning" : "default",
      hint:
        oldest !== undefined && oldest >= 1
          ? `oldest transaction open for ${duration(oldest)}`
          : longest >= 1
            ? "still running"
            : "nothing has run for a second",
    }
  }
  const counted = counter(samples, "queries")
  if (counted !== undefined) {
    const rate = lastRate(samples, "queries")
    return {
      key: "statements",
      label: "Statements",
      value: rate === undefined ? undefined : perSecond(rate),
      trailing: rate === undefined ? undefined : "a second",
      trend: {
        values: drawn(rates(samples, "queries")),
        color: "var(--chart-2)",
        label: `Statements a second ${SINCE_OPENED}`,
      },
      hint:
        rate === undefined
          ? "The rate needs a second reading"
          : `${compact(counted)} since it started`,
    }
  }
  const read = lastRate(samples, "rowsRead")
  return {
    key: "rows",
    label: "Rows read",
    value: read === undefined ? undefined : perSecond(read),
    trailing: read === undefined ? undefined : "a second",
    trend: {
      values: drawn(rates(samples, "rowsRead")),
      color: "var(--chart-2)",
      label: `Rows read a second ${SINCE_OPENED}`,
    },
    hint:
      counter(samples, "rowsRead") === undefined
        ? "Not reported to this account"
        : read === undefined
          ? "The rate needs a second reading"
          : undefined,
  }
}

/** Which family of figures an engine is read by. */
export type FigureFamily = "server" | "analytic" | "file"

/**
 * The page's readings. A server is its sessions against the limit, who is
 * working, who is waiting, its own fourth figure, transactions and the cache;
 * an analytic engine and a file are read as their homes read them, since
 * what they are doing and what they are is the same six figures.
 */
export function performanceReadings(
  family: FigureFamily,
  samples: readonly Sample[],
  stats?: DbServerStats,
): Reading[] {
  if (family === "file") return sqliteReadings(samples, stats)
  if (family === "analytic") return clickhouseReadings(samples)
  const [sessions, transactions, cache] = sqlReadings(samples, stats)
  return [
    sessions,
    workingReading(samples),
    waitingReading(samples),
    busiestReading(samples),
    transactions,
    cache,
  ]
}

/** The labels of a family's tiles, for the bones drawn before the first sample. */
export const READING_LABELS: Record<FigureFamily, string[]> = {
  server: [
    "Sessions",
    "Working now",
    "Waiting on a lock",
    "Statements",
    "Transactions",
    "Cache hit",
  ],
  analytic: ["Queries", "Running queries", "Rows inserted", "Parts", "Memory", "Disk"],
  file: ["File size", "WAL size", "Pages", "Free pages", "Journal mode"],
}

type Line = Series & { from: Omit<Source, "key"> }

function view(id: string, label: string, unit: ChartUnit, lines: Line[]): ChartView {
  return {
    id,
    label,
    unit,
    series: lines.map(({ key, label: name, color, kind }) =>
      kind ? { key, label: name, color, kind } : { key, label: name, color },
    ),
    sources: lines.map((line) => ({ key: line.key, ...line.from }) as Source),
  }
}

/**
 * Every chart the Overview can draw, for every SQL engine, in the order they
 * are read. A chart is offered only where the newest sample carries one of
 * its lines (`offeredViews`), so this one list is each engine's own: a server
 * that counts no temporary files has no such chart, and a line an engine does
 * not report is left out of a chart it otherwise fills.
 *
 * Module constants throughout: `ChartPanel` is memoised on its props.
 */
export const OVERVIEW_CHARTS: ChartView[] = [
  view("sessions", "Sessions", "count", [
    {
      key: "open",
      label: "Open",
      color: "var(--chart-1)",
      kind: "area",
      from: { gauge: "sessions" },
    },
    { key: "active", label: "Active", color: "var(--chart-5)", from: { gauge: "sessionsActive" } },
    {
      key: "inTransaction",
      label: "Idle in a transaction",
      color: "var(--chart-4)",
      from: { gauge: "sessionsIdleInTransaction" },
    },
    {
      key: "waiting",
      label: "Waiting on a lock",
      color: "var(--chart-3)",
      from: { gauge: "sessionsWaiting" },
    },
  ]),
  view("transactions", "Transactions", "rate", [
    {
      key: "transactions",
      label: "Transactions",
      color: "var(--chart-1)",
      kind: "area",
      from: { rate: TRANSACTIONS },
    },
    {
      key: "rolledBack",
      label: "Rolled back",
      color: "var(--chart-3)",
      from: { rate: "transactionsRolledBack" },
    },
  ]),
  view("statements", "Statements", "rate", [
    {
      key: "statements",
      label: "Statements",
      color: "var(--chart-2)",
      kind: "area",
      from: { rate: "queries" },
    },
    { key: "selects", label: "Selects", color: "var(--chart-1)", from: { rate: "selects" } },
    { key: "inserts", label: "Inserts", color: "var(--chart-5)", from: { rate: "inserts" } },
    { key: "updates", label: "Updates", color: "var(--chart-4)", from: { rate: "updates" } },
    { key: "deletes", label: "Deletes", color: "var(--chart-3)", from: { rate: "deletes" } },
    // An analytic engine counts its queries by kind under names of its own.
    {
      key: "selectQueries",
      label: "Selects",
      color: "var(--chart-1)",
      from: { rate: "selectQueries" },
    },
    {
      key: "insertQueries",
      label: "Inserts",
      color: "var(--chart-5)",
      from: { rate: "insertQueries" },
    },
    {
      key: "failedQueries",
      label: "Failed",
      color: "var(--chart-3)",
      from: { rate: "failedQueries" },
    },
  ]),
  view("rows", "Rows", "rate", [
    {
      key: "read",
      label: "Read",
      color: "var(--chart-2)",
      kind: "area",
      from: { rate: "rowsRead" },
    },
    { key: "written", label: "Written", color: "var(--chart-5)", from: { rate: "rowsWritten" } },
  ]),
  view("writes", "Rows written by kind", "rate", [
    {
      key: "inserted",
      label: "Inserted",
      color: "var(--chart-5)",
      kind: "area",
      from: { rate: "rowsInserted" },
    },
    { key: "updated", label: "Updated", color: "var(--chart-4)", from: { rate: "rowsUpdated" } },
    { key: "deleted", label: "Deleted", color: "var(--chart-3)", from: { rate: "rowsDeleted" } },
  ]),
  view("cache", "Cache hit", "percent", [
    {
      key: "hit",
      label: "Hit rate",
      color: "var(--chart-2)",
      kind: "area",
      from: { share: ["blocksHit", "blocksRead"] },
    },
  ]),
  view("locks", "Lock waits and deadlocks", "rate", [
    {
      key: "rowWaits",
      label: "Row lock waits",
      color: "var(--chart-4)",
      kind: "area",
      from: { rate: "rowLockWaits" },
    },
    { key: "waits", label: "Lock waits", color: "var(--chart-4)", from: { rate: "lockWaits" } },
    { key: "deadlocks", label: "Deadlocks", color: "var(--chart-3)", from: { rate: "deadlocks" } },
    { key: "conflicts", label: "Conflicts", color: "var(--chart-2)", from: { rate: "conflicts" } },
  ]),
  view("log", "Log written", "bytesRate", [
    {
      key: "wal",
      label: "Write-ahead log",
      color: "var(--chart-4)",
      kind: "area",
      from: { rate: "walBytes" },
    },
    {
      key: "flushed",
      label: "Log flushed",
      color: "var(--chart-4)",
      kind: "area",
      from: { rate: "logBytesFlushed" },
    },
    {
      key: "redo",
      label: "Redo",
      color: "var(--chart-4)",
      kind: "area",
      from: { rate: "redoBytes" },
    },
  ]),
  view("network", "Network", "bytesRate", [
    {
      key: "received",
      label: "Received",
      color: "var(--chart-2)",
      kind: "area",
      from: { rate: "bytesReceived" },
    },
    { key: "sent", label: "Sent", color: "var(--chart-5)", from: { rate: "bytesSent" } },
  ]),
  view("temporary", "Temporary work", "rate", [
    {
      key: "files",
      label: "Files",
      color: "var(--chart-4)",
      kind: "area",
      from: { rate: "tempFiles" },
    },
    { key: "tables", label: "Tables", color: "var(--chart-2)", from: { rate: "tempTables" } },
    {
      key: "diskTables",
      label: "Tables on disk",
      color: "var(--chart-3)",
      from: { rate: "tempDiskTables" },
    },
  ]),
  view("disk", "Disk", "bytesRate", [
    {
      key: "read",
      label: "Read",
      color: "var(--chart-2)",
      kind: "area",
      from: { rate: "diskReadBytes" },
    },
    { key: "written", label: "Written", color: "var(--chart-5)", from: { rate: "diskWriteBytes" } },
  ]),
  view("memory", "Memory", "bytes", [
    {
      key: "resident",
      label: "Resident",
      color: "var(--chart-4)",
      kind: "area",
      from: { gauge: "memoryResident" },
    },
    {
      key: "tracked",
      label: "Tracked",
      color: "var(--chart-2)",
      from: { gauge: "memoryTracking" },
    },
    {
      key: "pool",
      label: "Buffer pool",
      color: "var(--chart-4)",
      kind: "area",
      from: { gauge: "bufferPoolBytes" },
    },
  ]),
  view("merges", "Merges", "rate", [
    {
      key: "merges",
      label: "Merges",
      color: "var(--chart-1)",
      kind: "area",
      from: { rate: "merges" },
    },
  ]),
  view("plans", "Plans compiled", "rate", [
    {
      key: "compilations",
      label: "Compiled",
      color: "var(--chart-1)",
      kind: "area",
      from: { rate: "compilations" },
    },
    {
      key: "recompilations",
      label: "Recompiled",
      color: "var(--chart-3)",
      from: { rate: "recompilations" },
    },
  ]),
]

/** Milliseconds at the precision a reader compares them at. */
export function millis(value: number): string {
  if (value >= 60_000) return duration(value / 1000)
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10_000 ? 0 : 1)} s`
  if (value >= 10) return `${Math.round(value)} ms`
  return `${value.toFixed(value >= 1 ? 1 : 2)} ms`
}

/** A share of the whole as a percentage: whole numbers from ten up, a decimal under it, never "0%" for something that ran. */
export function shareWords(share: number): string {
  const value = share * 100
  if (value <= 0) return "0%"
  if (value < 0.1) return "<0.1%"
  return `${value.toFixed(value >= 10 ? 0 : 1)}%`
}

const SORTS: { id: DbStatementSort; label: string }[] = [
  { id: "total", label: "Total time" },
  { id: "mean", label: "Mean" },
  { id: "max", label: "Slowest run" },
  { id: "calls", label: "Calls" },
  { id: "rows", label: "Rows" },
]

/**
 * The orders a statement list can be asked for. "Slowest run" is offered
 * only where the engine keeps one per statement: the server refuses the
 * order where it does not, and its rows then carry no such figure.
 */
export function statementSorts(
  statements: readonly DbStatement[] | undefined,
): { id: DbStatementSort; label: string }[] {
  const keepsSlowest = !statements || statements.some((s) => s.maxMs !== undefined)
  return SORTS.filter((sort) => sort.id !== "max" || keepsSlowest)
}

export function isStatementSort(value: string): value is DbStatementSort {
  return SORTS.some((sort) => sort.id === value)
}

/**
 * Whether a statement's text holds a placeholder where a value was. The
 * server hands statements over as shapes — `$1`, `?`, `:1`, `@p1` in place of
 * every constant — and a shape cannot be planned: the engine wants the value.
 * Quoted text and identifiers are skipped, since a `?` inside one is a
 * character.
 */
export function hasPlaceholder(text: string): boolean {
  const bare = text
    .replace(/'(?:[^']|'')*'/g, "''")
    .replace(/"(?:[^"]|"")*"/g, '""')
    .replace(/`[^`]*`/g, "``")
    .replace(/\[[^\]]*\]/g, "[]")
  return /\$\d+|\?|(?<![:\w]):\d+\b|@p\d+\b/i.test(bare)
}

/** The statements an engine draws a plan for: the ones that read or change rows. */
const PLANNED = new Set([
  "select",
  "with",
  "insert",
  "update",
  "delete",
  "merge",
  "values",
  "table",
])

/**
 * Whether a statement's plan can be asked for from its text: "plan" as it
 * stands, "shape" when it would plan but for the placeholders in it, "none"
 * for a statement that has no plan at all — a `VACUUM`, a `CREATE INDEX`.
 */
export function planFor(text: string): "plan" | "shape" | "none" {
  const verb = /^\s*([A-Za-z]+)/.exec(text)?.[1].toLowerCase()
  if (!verb || !PLANNED.has(verb)) return "none"
  return hasPlaceholder(text) ? "shape" : "plan"
}

/** Rows a call, for a statement that has been called. */
export function rowsPerCall(statement: DbStatement): number | undefined {
  return statement.calls > 0 ? statement.rows / statement.calls : undefined
}
