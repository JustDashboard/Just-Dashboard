import type { LogLine, LogSource } from "@/lib/types"
import { fieldsEqual, fieldsOf, type LogLevel } from "@/lib/log-filter"
import { READINGS_MINUTES, type ReadingFigure } from "@/lib/log-insights"
import { eventMeta, type LensReading, type LensView, type LogLens } from "@/lib/log-lenses"
import type { LogFields, LogFilterState } from "@/components/logs/types"

/*
 * The service logs' decisions that are easy to get subtly wrong — which
 * reading a quick view already asks, what a page's narrowing leaves of the
 * filter, when a file's history is all in its rotated set, how wide the
 * event column is — as data, tested without a browser.
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

/**
 * A reading's figure as it is drawn: a per-minute reading as its rate over
 * the window, a distinct count that stopped being exact with a "+".
 */
export function readingShown(
  reading: LensReading,
  figure: ReadingFigure | undefined,
  window: NonNullable<LogLens["readingsWindow"]>,
): { value: number; text: string } {
  if (!figure) return { value: 0, text: "—" }
  if (reading.figure === "per_minute") {
    const value = figure.value / READINGS_MINUTES[window]
    return {
      value,
      text: value.toLocaleString(undefined, {
        maximumFractionDigits: value < 10 ? 2 : value < 100 ? 1 : 0,
      }),
    }
  }
  return {
    value: figure.value,
    text: `${figure.value.toLocaleString()}${figure.capped ? "+" : ""}`,
  }
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
