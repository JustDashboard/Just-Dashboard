"use client"

import { useSyncExternalStore } from "react"

/**
 * How the time column reads. `clock` is the local time of day, `date` adds the
 * day for a window that spans several, `utc` is the clock the server and
 * every other machine's log agree on, and `delta` is the gap since the line
 * above — the question a slow request or a retry loop actually asks, and a
 * static label, unlike "2 minutes ago", which would go stale in a memoised row
 * or redraw four thousand of them every tick.
 */
export type LogTime = "off" | "clock" | "date" | "delta" | "utc"

export const LOG_TIMES: { id: LogTime; label: string }[] = [
  { id: "clock", label: "Local time" },
  { id: "date", label: "Date and time" },
  { id: "utc", label: "UTC" },
  { id: "delta", label: "Since the line above" },
  { id: "off", label: "Hidden" },
]

/**
 * How the log pane is drawn, kept in the browser.
 *
 * On the screen and not on the account, for the same reason the theme and the
 * terminal's font are: whether lines wrap is a property of the display you are
 * sitting at. One store rather than per-pane state so every pane on the page
 * agrees, including one that mounts after the setting was changed.
 */
export type LogViewSettings = {
  /** Wrap long lines instead of scrolling sideways. */
  wrap: boolean
  /** How the time each line was parsed out of is shown, if at all. */
  time: LogTime
  /**
   * Draw each line by its shapes (`components/logs/log-text.tsx`) and a
   * structured line as its message and fields. Off is the line exactly as it
   * was written, which is one click away for the moment the colouring guessed
   * wrong or the JSON itself is the question.
   */
  highlight: boolean
  /**
   * Collapse a run of identical lines into one with its count, in the live
   * tail. A health check logging the same sentence every second is one fact,
   * not a screen of it; History never collapses, so its line numbers stay
   * contiguous and a search result is every line that matched.
   */
  dedupe: boolean
}

const KEY = "jd.logs.view"

const DEFAULTS: LogViewSettings = { wrap: false, time: "clock", highlight: true, dedupe: true }

const TIMES = new Set<string>(LOG_TIMES.map((t) => t.id))

/**
 * A stored value, whatever version of this type wrote it. The first version
 * had a `timestamps` switch; merging the new defaults over it would have
 * turned an operator's "no times" back on, so an old `false` becomes `off`.
 */
export function readLogView(raw: unknown): LogViewSettings {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return DEFAULTS
  const stored = raw as Partial<Record<keyof LogViewSettings | "timestamps", unknown>>
  const time =
    typeof stored.time === "string"
      ? TIMES.has(stored.time)
        ? (stored.time as LogTime)
        : "clock"
      : stored.timestamps === false
        ? "off"
        : "clock"
  const flag = (value: unknown, fallback: boolean) =>
    typeof value === "boolean" ? value : fallback
  return {
    wrap: flag(stored.wrap, DEFAULTS.wrap),
    time,
    highlight: flag(stored.highlight, DEFAULTS.highlight),
    dedupe: flag(stored.dedupe, DEFAULTS.dedupe),
  }
}

let current: LogViewSettings | null = null
const listeners = new Set<() => void>()

function load(): LogViewSettings {
  if (current) return current
  try {
    const raw = window.localStorage.getItem(KEY)
    current = raw ? readLogView(JSON.parse(raw)) : DEFAULTS
  } catch {
    current = DEFAULTS
  }
  return current
}

export function setLogView(patch: Partial<LogViewSettings>) {
  current = { ...load(), ...patch }
  try {
    window.localStorage.setItem(KEY, JSON.stringify(current))
  } catch {
    // Private browsing, or storage that is full. The setting still applies for
    // this session, which is better than refusing to change it.
  }
  for (const listener of listeners) listener()
}

export function useLogView(): LogViewSettings {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    () => load(),
    () => DEFAULTS,
  )
}

// One formatter per mode, built once: `toLocaleTimeString` per row builds a
// formatter per call, which on a four-thousand-line pane was the most
// expensive thing in the row after the tokenizer.
const FORMATS = {
  clock: new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }),
  date: new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }),
  utc: new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
    timeZone: "UTC",
  }),
}

/**
 * A line's time in the column's mode. `previous` is the stamp of the line
 * drawn above it, for `delta`; the first line of a pane, or one after a line
 * with no stamp, has nothing to measure from and shows its clock.
 */
export function formatLogTime(iso: string | undefined, mode: LogTime, previous?: string): string {
  if (!iso) return "—"
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return "—"
  switch (mode) {
    case "off":
      return ""
    case "utc":
      return `${FORMATS.utc.format(at)}Z`
    case "date":
      return FORMATS.date.format(at)
    case "delta": {
      const before = previous ? Date.parse(previous) : NaN
      if (Number.isNaN(before)) return FORMATS.clock.format(at)
      return delta(at.getTime() - before)
    }
    default:
      return FORMATS.clock.format(at)
  }
}

function delta(ms: number): string {
  const sign = ms < 0 ? "−" : "+"
  const abs = Math.abs(ms)
  if (abs < 10_000) return `${sign}${(abs / 1000).toFixed(3)}s`
  if (abs < 60_000) return `${sign}${(abs / 1000).toFixed(1)}s`
  if (abs < 3_600_000)
    return `${sign}${Math.floor(abs / 60_000)}m ${Math.floor((abs % 60_000) / 1000)}s`
  return `${sign}${Math.floor(abs / 3_600_000)}h ${Math.floor((abs % 3_600_000) / 60_000)}m`
}

/** How wide the column is for each mode, so a pane of lines does not jitter. */
export const TIME_WIDTH: Record<LogTime, string> = {
  off: "",
  clock: "w-16",
  utc: "w-18",
  delta: "w-16",
  date: "w-28",
}
