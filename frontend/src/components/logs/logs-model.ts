import type { LogLine, LogSource } from "@/lib/types"
import { fieldsEqual, fieldsOf, highlightRanges, lineValue, type LogLevel } from "@/lib/log-filter"
import { logLineKey } from "@/lib/log-line-key"
import { READINGS_MINUTES, type ReadingFigure } from "@/lib/log-insights"
import { eventMeta, lensFor, type LensReading, type LensView, type LogLens } from "@/lib/log-lenses"
import { journalSource } from "@/lib/log-sources"
import type { LogFields, LogFilterState } from "@/components/logs/types"

/*
 * The service logs' decisions that are easy to get subtly wrong — which
 * reading a quick view already asks, what a page's narrowing leaves of the
 * filter, when a file's history is all in its rotated set, how wide the
 * event column is, which rows the lines are drawn as — as data, tested
 * without a browser.
 */

/** A question as a quick view, a reading and a filter each put it: its fields and its levels. */
export type LogQuestion = { fields?: LogFields; levels?: readonly LogLevel[] }

export function sameQuestion(a: LogQuestion, b: LogQuestion) {
  const levels = a.levels ?? []
  const others = b.levels ?? []
  return (
    fieldsEqual(a.fields ?? {}, b.fields ?? {}) &&
    levels.length === others.length &&
    levels.every((level) => others.includes(level))
  )
}

/**
 * A narrowing a page hands the pane — the minute around a failed request,
 * narrowed to the site's names; a reading pressed on the page's own grid.
 * Each part replaces the filter's own only when given: a window that names
 * no fields keeps the reader's.
 */
export type LogAsk = { fields?: LogFields; levels?: LogLevel[]; q?: string }

/** The narrowing a window or an ask carries, or nothing when it names none. */
export function askOf(at: LogAsk | undefined): LogAsk | undefined {
  if (!at || (at.fields === undefined && at.levels === undefined && at.q === undefined)) {
    return undefined
  }
  return { fields: at.fields, levels: at.levels, q: at.q }
}

export function withAsk(filter: LogFilterState, ask: LogAsk): LogFilterState {
  return {
    ...filter,
    fields: ask.fields ?? fieldsOf(filter),
    levels: ask.levels ?? filter.levels,
    q: ask.q ?? filter.q,
  }
}

/**
 * A file logrotate has just emptied: nothing in it yet, its history all in
 * the rotated set beside it. Size is left out of the description at zero,
 * so an absent size on a file with a path is an empty one.
 */
export function emptiedFile(source: Pick<LogSource, "path" | "size" | "archives">) {
  return Boolean(source.path) && !source.size && (source.archives ?? 0) > 0
}

/**
 * The lens's reading that asks exactly a quick view's question, whose figure
 * the view's chip then carries. A view that also searches for words asks
 * something no reading does.
 */
export function readingFor<T extends { reading: LensReading }>(
  view: LensView,
  tiles: readonly T[],
): T | undefined {
  if (view.q !== undefined) return undefined
  return tiles.find((tile) => sameQuestion(view, tile.reading))
}

/** The readings no quick view asks: chips of their own beside the views. */
export function unaskedReadings<T extends { reading: LensReading }>(
  views: readonly LensView[],
  tiles: readonly T[],
): T[] {
  return tiles.filter((tile) => !views.some((view) => readingFor(view, [tile]) !== undefined))
}

/** The stretch a reading's figure is over: its length, for a rate, and its words. */
export type ReadingsWindow = { minutes: number; short: string; long: string }

/** The windows a lens reads its readings over, by its own choice. */
export const READINGS_WINDOWS: Record<NonNullable<LogLens["readingsWindow"]>, ReadingsWindow> = {
  "1h": { minutes: READINGS_MINUTES["1h"], short: "in 1h", long: "the last hour" },
  "24h": { minutes: READINGS_MINUTES["24h"], short: "in 24h", long: "the last 24 hours" },
  "7d": { minutes: READINGS_MINUTES["7d"], short: "in 7d", long: "the last 7 days" },
}

/**
 * A reading's figure as it is drawn: a per-minute reading as its rate over
 * the window, a distinct count that stopped being exact with a "+". The
 * places are the ones the rounded figure needs, so a tile counting up to it
 * lands on the same text: 0.5 a minute, not 0.50.
 */
export function readingShown(
  reading: LensReading,
  figure: ReadingFigure | undefined,
  window: Pick<ReadingsWindow, "minutes">,
): { value: number; text: string; decimals: number } {
  if (!figure) return { value: 0, text: "—", decimals: 0 }
  if (reading.figure === "per_minute") {
    const value = figure.value / window.minutes
    const places = value < 10 ? 2 : value < 100 ? 1 : 0
    return {
      value,
      text: value.toLocaleString(undefined, { maximumFractionDigits: places }),
      decimals: (String(Number(value.toFixed(places))).split(".")[1] ?? "").length,
    }
  }
  return {
    value: figure.value,
    text: `${figure.value.toLocaleString()}${figure.capped ? "+" : ""}`,
    decimals: 0,
  }
}

/**
 * Whether a source is one service's stream — a container, a process, a
 * stack, an application's file, one unit's journal — whose lifecycle lines
 * mark its runs. A whole host's (the journal, the kernel, syslog) is every
 * service's at once, and a rule for each timer that fired was most of it.
 */
export function oneService(kind: LogSource["kind"], sourceId: string) {
  return (
    kind === "docker" ||
    kind === "pm2" ||
    kind === "stack" ||
    kind === "app" ||
    (kind === "journal" && sourceId !== journalSource())
  )
}

/**
 * The lens a unit's journal offers its quick views from: the unit's own, with
 * the manager's Failures beside them. Most of a failing unit's journal is
 * systemd's lines about it — failed, restart scheduled, start limit hit —
 * which the unit's lens (Postgres's Lifecycle, an app's Startup) never asks
 * for. Only the views: the facets, defaults and readings stay the unit's.
 */
export function unitJournalLens(
  lens: LogLens | undefined,
  kind: LogSource["kind"],
  sourceId: string,
): LogLens | undefined {
  if (!lens || lens.id === "systemd" || kind !== "journal" || sourceId === journalSource()) {
    return lens
  }
  const failures = lensFor("systemd")?.views.find((view) => view.id === "failures")
  if (!failures || lens.views.some((view) => view.id === failures.id)) return lens
  return { ...lens, views: [...lens.views, failures] }
}

/** The event words the column takes: long enough for "upstream refused", no wider. */
const EVENT_MIN = 6
const EVENT_MAX = 17

/**
 * How wide the level column is while the lines on screen carry event words,
 * in characters of the console's font: the longest word drawn, within
 * bounds — a fixed width either cut "restart scheduled" to "restart sch…"
 * or gave "ban" a column of blanks. Zero when no line has one, and the
 * column is the level's own.
 */
export function eventColumnFor(lines: readonly LogLine[], lens: string | undefined): number {
  let longest = 0
  for (const line of lines) {
    if (!line.event || line.cont) continue
    const meta = eventMeta(lens, line.event, line.lens)
    if (meta && meta.mark !== false) longest = Math.max(longest, meta.label.length)
  }
  return longest === 0 ? 0 : Math.min(Math.max(longest, EVENT_MIN), EVENT_MAX)
}

/**
 * The run of a stack trace shown before the rest folds away. A run one line
 * longer is shown whole: a fold that hides one line costs a line to say so.
 */
const FOLD_AFTER = 3

export type Row =
  | {
      kind: "line"
      key: number
      line: LogLine
      /** The stamp of the row drawn above, for the time column's `delta`. */
      prev?: string
      /** The record this line continues, when it is part of one. */
      head?: LogLine
      /** How many identical lines this row stands for, and when the first was. */
      repeat?: number
      since?: string
    }
  | { kind: "fold"; key: string; head: number; hidden: number; expanded: boolean }
  | { kind: "divider"; key: string; line: LogLine; label: string }

/**
 * What a line is compared on to call it a repeat: who said it, how loudly,
 * what it records, and the words — without the time the line itself leads
 * with, which is the one part a repeat never repeats.
 */
const LEADING_TIME =
  /^\[?(?:\d{4}[-/.]\d{2}[-/.]\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?|[A-Z][a-z]{2} +\d{1,2} \d{2}:\d{2}:\d{2}(?:\.\d+)?)\]?\s*/

function sameAs(a: LogLine, b: LogLine) {
  return (
    a.source === b.source &&
    a.level === b.level &&
    a.event === b.event &&
    (a.message ?? a.text.replace(LEADING_TIME, "")) ===
      (b.message ?? b.text.replace(LEADING_TIME, ""))
  )
}

function hasHit(line: LogLine, filter: LogFilterState | undefined) {
  if (line.match?.length) return true
  return filter ? highlightRanges(line.text, filter).length > 0 : false
}

/**
 * The rows the lines are drawn as. A record's continuation lines stay under
 * their head — the first few inline, the rest behind a fold unless one of
 * them is what the search found — a lens's lifecycle events get a rule across
 * the pane where the pane is one service's runs (`dividers`), and in the live
 * tail a run of identical lines is one row with its count. An orphan
 * continuation at the top of the window (its head scrolled out of the buffer)
 * is drawn as the line it is.
 */
export function buildRows(
  lines: readonly LogLine[],
  opts: {
    dedupe: boolean
    /** Rules for the runs: a stream of one service's, never a whole host's. */
    dividers?: boolean
    lens?: string
    folds: ReadonlySet<number>
    filter?: LogFilterState
  },
): Row[] {
  const rows: Row[] = []
  let prev: string | undefined
  let head: { line: LogLine; key: number } | undefined
  let last: Extract<Row, { kind: "line" }> | undefined
  let lastHasRun = false

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (line.cont && head) {
      let end = i
      while (end < lines.length && lines[end].cont) end++
      const run = lines.slice(i, end)
      const long = run.length > FOLD_AFTER + 1
      const expanded = long && opts.folds.has(head.key)
      // A fold that hides what the search found is a result nobody sees.
      const open = !long || expanded || run.slice(FOLD_AFTER).some((l) => hasHit(l, opts.filter))
      const shown = open ? run : run.slice(0, FOLD_AFTER)
      for (const cont of shown) {
        rows.push({ kind: "line", key: logLineKey(cont), line: cont, prev, head: head.line })
        prev = cont.timestamp ?? prev
      }
      if (long && (expanded || !open)) {
        rows.push({
          kind: "fold",
          key: `fold:${head.key}`,
          head: head.key,
          hidden: run.length - FOLD_AFTER,
          expanded,
        })
      }
      lastHasRun = true
      last = undefined
      i = end - 1
      continue
    }

    const key = logLineKey(line)
    const startsRun = Boolean(lines[i + 1]?.cont)
    if (opts.dedupe && last && !lastHasRun && !startsRun && sameAs(last.line, line)) {
      // The row keeps its first line's key and shows the newest line: an
      // opened row, the one j and k are on, and React's own identity all
      // hold it, and a new key per repeat closed it every time the line came
      // round again — which, for a line worth collapsing, is every second.
      last.repeat = (last.repeat ?? 1) + 1
      last.since ??= last.line.timestamp
      last.line = line
      head = { line, key }
      continue
    }

    const meta =
      opts.dividers && line.event ? eventMeta(opts.lens, line.event, line.lens) : undefined
    if (meta?.divider) {
      rows.push({ kind: "divider", key: `divider:${key}`, line, label: meta.label })
    }
    last = { kind: "line", key, line, prev }
    rows.push(last)
    prev = line.timestamp ?? prev
    head = { line, key }
    lastHasRun = false
  }
  return rows
}

/** The widths a lens column drawn as its text takes, in characters. */
const COLUMN_MIN = 6
const COLUMN_MAX = 24

/**
 * How wide a lens column drawn as its own text is: its longest value on
 * screen, within bounds. A width by kind cut an upstream to "http://1…" and
 * a jail to "nginx-ht…" — the prefix every value shares — and gave a
 * three-letter code a column of blanks. Zero when no line has a value.
 */
export function columnWidthFor(lines: readonly LogLine[], key: string): number {
  let longest = 0
  for (const line of lines) {
    if (line.cont) continue
    const value = lineValue(line, key)
    if (value) longest = Math.max(longest, value.length)
  }
  return longest === 0 ? 0 : Math.min(Math.max(longest, COLUMN_MIN), COLUMN_MAX)
}
