import { duration } from "@/lib/format"
import type { Series } from "@/components/metrics/metric-chart"
import type { ChartUnit, ChartView } from "@/components/database/home/charts"
import { TRANSACTIONS, type Sample, type Source } from "@/components/database/home/samples"
import type { SessionTally } from "@/components/database/ops/performance-activity"
import type { DbStatement, DbStatementSort } from "@/components/database/ops/performance-types"

/**
 * The Performance page's figures: the charts of its Overview, and how a
 * statement's numbers are written.
 *
 * The charts are drawn from the same samples the database's home keeps — one
 * snapshot every few seconds, a rate the difference of two — through the
 * home's own arithmetic, so a figure here and the same figure there are one
 * calculation. What this page adds is the reading a home has no
 * room for: who is working, who is waiting on a lock, what has run longest.
 */

/**
 * The session list as the page last read it: what the tiles about sessions
 * say where the list can be read.
 */
export type LiveSessions = { tally: SessionTally }

type Line = Series & { from: Omit<Source, "key"> }

function view(
  id: string,
  label: string,
  unit: ChartUnit,
  lines: Line[],
  ceiling?: string,
): ChartView {
  return {
    id,
    label,
    unit,
    ceiling,
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
 * Each line takes its measurement's hue (§10), the one the database's home
 * draws it in: sessions blue, transactions violet, rows read green against
 * written magenta, the cache the brand's pale line.
 *
 * Module constants throughout: `ChartPanel` is memoised on its props.
 */
export const OVERVIEW_CHARTS: ChartView[] = [
  view(
    "sessions",
    "Sessions",
    "count",
    [
      {
        key: "open",
        label: "Open",
        color: "var(--chart-2)",
        kind: "area",
        from: { gauge: "sessions" },
      },
      // Working, not the engine's "active": a session waiting on a lock is the
      // line under this one, and is not counted in both.
      {
        key: "active",
        label: "Working",
        color: "var(--chart-5)",
        from: { gauge: "sessionsWorking" },
      },
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
    ],
    "sessionsMax",
  ),
  view("transactions", "Transactions", "rate", [
    {
      key: "transactions",
      label: "Transactions",
      color: "var(--chart-4)",
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
      color: "var(--chart-5)",
      kind: "area",
      from: { rate: "rowsRead" },
    },
    { key: "written", label: "Written", color: "var(--chart-3)", from: { rate: "rowsWritten" } },
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
  // A line rather than an area: a cache that hits 99% of the time filled the
  // plot to its top rule with one slab, and the dip worth seeing was a notch.
  view("cache", "Cache hit", "percent", [
    {
      key: "hit",
      label: "Hit rate",
      color: "var(--chart-1)",
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
      color: "var(--chart-4)",
      kind: "area",
      from: { rate: "merges" },
    },
  ]),
  view("plans", "Plans compiled", "rate", [
    {
      key: "compilations",
      label: "Compiled",
      color: "var(--chart-2)",
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

/**
 * The Overview's charts for one engine: a line the engine has no notion of is
 * left out of a chart it otherwise fills. An engine without transactions has
 * no session idle inside one, and one without lock waits has none waiting —
 * its counters say zero for both, and a line drawn flat at zero for ever is
 * a claim that it could be anything else.
 */
export function overviewCharts(has: { transactions: boolean; locks: boolean }): ChartView[] {
  const absent = new Set<string>([
    ...(has.transactions ? [] : ["inTransaction"]),
    ...(has.locks ? [] : ["waiting"]),
  ])
  if (absent.size === 0) return OVERVIEW_CHARTS
  return OVERVIEW_CHARTS.map((chart) =>
    chart.id !== "sessions"
      ? chart
      : {
          ...chart,
          series: chart.series.filter((line) => !absent.has(line.key)),
          sources: chart.sources.filter((source) => !absent.has(source.key)),
        },
  )
}

/**
 * Whether a chart has had nothing above zero in the window: no row written,
 * no lock waited on. Such a chart is one flat line, and several of them are
 * a wall of flat lines; the Overview names them in a sentence instead and
 * draws each when it moves. A chart of gauges that are simply low — two
 * sessions — is not quiet, and neither is one whose rates have not had their
 * second reading yet unless the counters under them have never moved at all.
 */
export function quietChart(chart: ChartView, samples: readonly Sample[]): boolean {
  const newest = samples[samples.length - 1]
  if (!newest) return false
  const never = chart.sources.every((source) => {
    if ("gauge" in source) return (newest.gauges[source.gauge] ?? 0) === 0
    const keys = "rate" in source ? source.rate : source.share
    return (typeof keys === "string" ? [keys] : keys).every(
      (key) => (newest.counters[key] ?? 0) === 0,
    )
  })
  if (never) return true
  if (samples.length < 2) return false
  const oldest = samples[0]
  return chart.sources.every((source) => {
    if ("gauge" in source) {
      return samples.every((sample) => (sample.gauges[source.gauge] ?? 0) === 0)
    }
    const keys = "rate" in source ? source.rate : source.share
    return (typeof keys === "string" ? [keys] : keys).every(
      (key) => (newest.counters[key] ?? 0) === (oldest.counters[key] ?? 0),
    )
  })
}

/** Milliseconds at the precision a reader compares them at. */
export function millis(value: number): string {
  if (value >= 60_000) return duration(value / 1000)
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10_000 ? 0 : 1)} s`
  if (value >= 10) return `${Math.round(value)} ms`
  // Something that ran took some time: under a hundredth is said as that,
  // never as a round nought.
  if (value > 0 && value < 0.005) return "<0.01 ms"
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

/** One placeholder of a statement's shape, where it stands in the text. */
export type ShapeSlot = {
  /** As written: `$1`, `?`, `?..`, `@p2`, `:3`. */
  token: string
  /**
   * Which value it takes. A numbered placeholder is one value wherever it
   * repeats; a `?` is a value of its own each time.
   */
  key: string
  start: number
  end: number
  /** The words before it, for the label of the field that fills it: "WHERE id =". */
  before: string
}

const NUMBERED = /^(?:\$\d+|:\d+|@p\d+)/i
/**
 * A name in square brackets, as SQL Server quotes one: letters in it and no
 * operator. `[is?]` is a column; `[?..]` and `[$1, $2]` are arrays of values.
 */
const BRACKETED = /^\[(?=[^\]]*[A-Za-z_])[^\],+*/%=<>()$]*\]/

/**
 * The placeholders of a statement, in the order they stand. The server hands
 * statements over as shapes — `$1`, `?`, `:1`, `@p1` where each constant was,
 * `?..` where a list of them was. Quoted text, quoted names and comments are
 * skipped, since a `?` inside one is a character; so is `::`, a cast.
 */
export function shapeSlots(text: string): ShapeSlot[] {
  const slots: ShapeSlot[] = []
  let anonymous = 0
  let i = 0
  const skipTo = (close: string, doubled = false) => {
    let at = i + 1
    for (;;) {
      const end = text.indexOf(close, at)
      if (end < 0) return text.length
      if (doubled && text[end + 1] === close) at = end + 2
      else return end + close.length
    }
  }
  while (i < text.length) {
    const char = text[i]
    if (char === "'" || char === '"') i = skipTo(char, true)
    else if (char === "`") i = skipTo("`")
    else if (char === "[" && BRACKETED.test(text.slice(i, i + 130))) i = skipTo("]")
    else if (text.startsWith("--", i)) i = skipTo("\n")
    else if (text.startsWith("/*", i)) i = skipTo("*/")
    else if (char === "$" && /^\$(?:[A-Za-z_]\w*)?\$/.test(text.slice(i, i + 64))) {
      // A dollar-quoted body: everything up to the same tag again.
      const tag = /^\$(?:[A-Za-z_]\w*)?\$/.exec(text.slice(i, i + 64))?.[0] ?? "$$"
      const end = text.indexOf(tag, i + tag.length)
      i = end < 0 ? text.length : end + tag.length
    } else {
      const prior = i > 0 ? text[i - 1] : ""
      const numbered = /[\w:@$]/.test(prior) ? null : NUMBERED.exec(text.slice(i, i + 16))
      const token = numbered
        ? numbered[0]
        : char === "?"
          ? text.startsWith("?..", i)
            ? "?.."
            : "?"
          : ""
      if (!token) {
        i += 1
        continue
      }
      slots.push({
        token,
        key: numbered ? token.toLowerCase() : `?${++anonymous}`,
        start: i,
        end: i + token.length,
        before: wordsBefore(text, i, slots[slots.length - 1]?.end ?? 0),
      })
      i += token.length
    }
  }
  return slots
}

/**
 * The last few words before a place in a statement, on one line: what the
 * value there is compared with or assigned to. Cut at the clause it belongs
 * to, and never reaching back past the placeholder before it.
 */
function wordsBefore(text: string, at: number, from: number): string {
  const line = text
    .slice(Math.max(from, at - 60), at)
    .replace(/\s+/g, " ")
    .trim()
  const clause =
    line
      .split(/,|\b(?:and|or|where|set|values|on|when|then|else)\b/i)
      .pop()
      ?.trim() ?? ""
  const words = clause || line
  return words.length <= 28 ? words : `…${words.slice(-27)}`
}

/** The values a shape asks for, each once: a numbered placeholder repeated is one field. */
export function shapeFields(slots: readonly ShapeSlot[]): ShapeSlot[] {
  const seen = new Set<string>()
  return slots.filter((slot) => !seen.has(slot.key) && Boolean(seen.add(slot.key)))
}

/**
 * A shape with values put where its placeholders are, as the reader typed
 * them — `42`, `'paid'`, `now()` — so that it is a statement an engine can
 * plan. A value left empty is `NULL`.
 */
export function fillShape(text: string, values: Readonly<Record<string, string>>): string {
  let filled = ""
  let from = 0
  for (const slot of shapeSlots(text)) {
    const value = Object.hasOwn(values, slot.key) ? values[slot.key].trim() : ""
    filled += text.slice(from, slot.start) + (value || "NULL")
    from = slot.end
  }
  return filled + text.slice(from)
}

/** Whether a statement's text holds a placeholder where a value was. */
export function hasPlaceholder(text: string): boolean {
  return shapeSlots(text).length > 0
}

/**
 * A statement's key in the list. The server's id is the engine's digest of
 * the text, and one digest can be listed twice — once per schema it ran in —
 * so the id alone is not one row: the second is told apart by its place.
 */
export function statementKeys(statements: readonly Pick<DbStatement, "id">[]): string[] {
  const seen = new Map<string, number>()
  return statements.map((statement) => {
    const n = seen.get(statement.id) ?? 0
    seen.set(statement.id, n + 1)
    return n === 0 ? statement.id : `${statement.id}~${n}`
  })
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
