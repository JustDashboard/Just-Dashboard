import type {
  DbStatementHistoryFacts,
  DbStatementHistoryPoint,
  DbStatementHistorySort,
} from "@/components/database/ops/performance-types"
import type { MetricEvent } from "@/lib/types"

/**
 * What the Statement history view works out from the server's answer: which
 * orders and columns an engine has, a row's trend as its sparkline draws it,
 * a statement's points as chart rows, and the sentences that say what each
 * figure was made from.
 */

const SORTS: { id: DbStatementHistorySort; label: string }[] = [
  { id: "total", label: "Of runtime" },
  { id: "calls", label: "Calls" },
  { id: "mean", label: "Mean" },
  { id: "p95", label: "p95" },
  { id: "rows", label: "Rows" },
  { id: "rowsRead", label: "Rows read" },
]

/**
 * The orders a history can be listed in. p95 and rows read are offered only
 * where the engine keeps what they are made from: elsewhere the server would
 * rank every row as having none.
 */
export function historySorts(
  facts: Pick<DbStatementHistoryFacts, "percentiles" | "rowsRead"> | undefined,
): { id: DbStatementHistorySort; label: string }[] {
  return SORTS.filter(
    (sort) =>
      (sort.id !== "p95" || Boolean(facts?.percentiles)) &&
      (sort.id !== "rowsRead" || Boolean(facts?.rowsRead)),
  )
}

export function isHistorySort(value: string): value is DbStatementHistorySort {
  return SORTS.some((sort) => sort.id === value)
}

/**
 * A row's trend as its sparkline draws it: from the first point anything was
 * recorded at, with a point nothing was recorded at drawn as a point nothing
 * ran in. A sparkline answers "did anything happen here"; the statement's own
 * charts are where a gap is drawn as one.
 */
export function trendValues(trend: readonly (number | null)[] | undefined): number[] {
  const values = trend ?? []
  const start = values.findIndex((value) => value !== null)
  return start < 0 ? [] : values.slice(start).map((value) => value ?? 0)
}

/** "the last hour", "the last 24 hours", "the last 7 days". */
export function windowWords(hours: number): string {
  if (hours === 1) return "the last hour"
  if (hours % 24 === 0 && hours > 24) return `the last ${hours / 24} days`
  return `the last ${hours} hours`
}

/** A span in the words a sentence takes after "every": "5 minutes", "hour", "7 days". */
export function spanWords(seconds: number): string {
  if (seconds >= 86_400 && seconds % 86_400 === 0) {
    return seconds === 86_400 ? "day" : `${seconds / 86_400} days`
  }
  if (seconds >= 3600 && seconds % 3600 === 0) {
    return seconds === 3600 ? "hour" : `${seconds / 3600} hours`
  }
  if (seconds >= 60 && seconds % 60 === 0) {
    return seconds === 60 ? "minute" : `${seconds / 60} minutes`
  }
  return `${Math.round(seconds)} seconds`
}

/**
 * Whether a window is recorded only in part: the recorder started inside it,
 * or missed readings, so "the last 7 days" holds less than seven days.
 */
export function partlyRecorded(facts: DbStatementHistoryFacts): boolean {
  return facts.intervals > 0 && facts.recordedSeconds < facts.hours * 3600 * 0.9
}

type EngineWords = { driver: string; label: string }

/**
 * How the figures were made from what the engine keeps — which is the
 * difference between a mean that is a mean of every run and one worked out
 * from two totals — and what was left out of each reading.
 */
export function historyMethod(engine: EngineWords, facts: DbStatementHistoryFacts): string[] {
  const every = spanWords(facts.everySeconds)
  const reading = `Every ${every} the dashboard reads them, and a point is what a statement added since the reading before`
  const kept = `Each reading keeps the ${facts.keptPerInterval} statements that took the most time in it; the rest count toward the total.`
  switch (engine.driver) {
    case "postgres":
      return [
        `pg_stat_statements keeps running totals for each statement. ${reading}: its calls, time and rows. Its mean is that time divided by those calls. The extension keeps no record of single runs, so there are no percentiles.`,
        kept,
      ]
    case "mysql":
      return [
        facts.percentiles
          ? `performance_schema keeps running totals for each digest, and a histogram of how long its runs took. ${reading}. p50, p95 and p99 are read from the histogram over the same readings: never under the true figure, and over it by less than 5%. Rows read is what the server examined to answer.`
          : `performance_schema keeps running totals for each digest. ${reading}. Rows read is what the server examined to answer. ${engine.label} keeps no histogram of run times, so there are no percentiles.`,
        kept,
      ]
    case "clickhouse":
      return [
        `ClickHouse's query log holds every finished query. Every ${every} the dashboard sums the runs that finished since the reading before, by the query's shape. p50, p95 and p99 are read from those runs' own durations: never under the true figure, and over it by less than 5%.`,
        kept,
      ]
    case "sqlserver":
      return [
        `SQL Server keeps running totals beside each cached plan. ${reading}. A plan that leaves the cache takes its totals with it, and the statement counts again from when its plan is compiled again. There are no percentiles.`,
        kept,
      ]
    case "oracle":
      return [
        `Oracle keeps running totals for each statement in the shared pool (v$sqlstats). ${reading}. A statement aged out of the pool counts again from when it is parsed again. There are no percentiles.`,
        kept,
      ]
    default:
      return [`${reading}.`, kept]
  }
}

/** One point of a statement's charts. A point nothing was recorded at holds only its time: a gap. */
export type HistoryRow = {
  ts: number
  time?: number
  calls?: number
  mean?: number
  p50?: number
  p95?: number
  p99?: number
  returned?: number
  read?: number
}

export function historyRows(points: readonly DbStatementHistoryPoint[]): HistoryRow[] {
  return points.map((point) => {
    if (!point.recorded) return { ts: point.at }
    const row: HistoryRow = {
      ts: point.at,
      time: point.totalMs,
      calls: point.calls,
      returned: point.rows,
    }
    if (point.meanMs !== undefined) row.mean = point.meanMs
    if (point.p50Ms !== undefined) row.p50 = point.p50Ms
    if (point.p95Ms !== undefined) row.p95 = point.p95Ms
    if (point.p99Ms !== undefined) row.p99 = point.p99Ms
    if (point.rowsRead !== undefined) row.read = point.rowsRead
    return row
  })
}

/** The readings across which the counters started again, as marks on the charts' time axis. */
export function historyEvents(resets: readonly string[]): MetricEvent[] {
  return resets.map((ts) => ({
    ts,
    kind: "action",
    title: "Statement statistics were reset",
    detail: "The figures after this count from the reset.",
    severity: "info",
  }))
}

/**
 * When the history will have its first point, for a window with none yet.
 * The recorder's first reading of a server is what the second is taken
 * against, so a point needs two.
 */
export function firstPointWords(facts: DbStatementHistoryFacts, now: number): string {
  const every = spanWords(facts.everySeconds)
  const due = facts.nextAt ? Date.parse(facts.nextAt) : Number.NaN
  if (!facts.reading || Number.isNaN(due)) {
    return `The dashboard reads this server's statements every ${every}, from when it starts, and a statement's history begins with its second reading.`
  }
  const left = Math.max(0, (due - now) / 1000)
  const when = left < 90 ? "within a minute" : `in about ${spanWords(Math.round(left / 60) * 60)}`
  return `The dashboard reads this server's statements every ${every}, and a statement's history begins with the second reading: the next is ${when}.`
}
