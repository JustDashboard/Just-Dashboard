"use client"

import { useMemo, useState } from "react"
import { cn } from "@/lib/utils"
import { clock, timestamp } from "@/lib/format"
import type { LogBucket } from "@/lib/types"
import { LEVEL_DOT, LEVEL_LABEL, LOG_LEVELS, type LogLevel } from "@/lib/log-filter"
import { eventLabel, eventMeta } from "@/lib/log-lenses"
import { fieldOf } from "@/lib/log-fields"
import { CLASS_DOT, type StatusClass } from "@/lib/requests"
import type { Tone } from "@/components/tone"

// Worst last, so the alarming colours sit at the top of a stacked column where
// the eye lands, rather than being buried under a block of debug. The swatches
// are the ones the level chips and the level column use, so a red column here
// is the same red as the lines it counts.
const STACK = [...LOG_LEVELS].reverse()

/** The server's name for "every value past the ones it kept". */
const REST = "*"

/**
 * The lines that carry no value for the key at all: counted in the column's
 * total and in no series. Drawn, faintly, because a column of forty lines
 * with three named ones is not a column of three.
 */
const UNKEYED = "\u0000unkeyed"

const TONE_DOT: Partial<Record<Tone, string>> = {
  danger: "bg-destructive",
  warning: "bg-warning",
  success: "bg-success",
}

/**
 * The hues a series with no verdict takes, in the order the series rank —
 * the `--tag-*` hues, which sit at one lightness, without red and amber,
 * which on this chart are the errors' and the warnings'. Assigned by rank
 * rather than hashed by name: a chart of five events needs five different
 * colours, and a hash only promises the same one per name.
 */
const NEUTRAL = [
  "var(--tag-blue)",
  "var(--tag-violet)",
  "var(--tag-cyan)",
  "var(--tag-pink)",
  "var(--tag-green)",
  "var(--tag-slate)",
]

type Series = { key: string; label: string; className?: string; color?: string }

function widthLabel(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${seconds / 60}m`
  if (seconds < 86400) return `${seconds / 3600}h`
  return `${seconds / 86400}d`
}

/**
 * The series a histogram keyed by `by` draws, worst last. Levels are the
 * level chips' swatches; a status class is the request log's family colour;
 * an event takes its tone where it has one; anything else a neutral hue.
 */
function seriesFor(buckets: LogBucket[], by: string | undefined, lens: string | undefined) {
  if (!by || by === "level") {
    return STACK.map<Series>((level) => ({
      key: level,
      label: LEVEL_LABEL[level],
      className: LEVEL_DOT[level],
    }))
  }
  const totals = new Map<string, number>()
  for (const bucket of buckets) {
    for (const [key, n] of Object.entries(bucket.counts))
      totals.set(key, (totals.get(key) ?? 0) + n)
  }
  const tone = (key: string): Tone | undefined =>
    by === "event" ? eventMeta(lens, key)?.tone : undefined
  const rank = (key: string) => {
    if (key === REST) return -1
    const t = tone(key)
    return t === "danger" ? 3 : t === "warning" ? 2 : t === "success" ? 1 : 0
  }
  const keys = [...totals.keys()].sort(
    (a, b) => rank(a) - rank(b) || (totals.get(a) ?? 0) - (totals.get(b) ?? 0),
  )
  const neutral = keys.filter(
    (key) =>
      key !== REST &&
      !(by === "class" ? CLASS_DOT[key as StatusClass] : TONE_DOT[tone(key) ?? "default"]),
  )
  const unkeyed: Series[] = buckets.some((bucket) => unkeyedOf(bucket) > 0)
    ? [{ key: UNKEYED, label: `no ${fieldOf(by).label}`, className: "bg-muted-foreground/15" }]
    : []
  return [
    ...unkeyed,
    ...keys.map<Series>((key) => {
      if (key === REST) return { key, label: "other", className: "bg-muted-foreground/30" }
      const label = by === "event" ? eventLabel(lens, key) : key
      const fixed =
        by === "class" ? CLASS_DOT[key as StatusClass] : TONE_DOT[tone(key) ?? "default"]
      if (fixed) return { key, label, className: fixed }
      // Largest neutral series first in the hue order, so the busiest keeps blue.
      const at = neutral.length - 1 - neutral.indexOf(key)
      return { key, label, color: NEUTRAL[at % NEUTRAL.length] }
    }),
  ]
}

function unkeyedOf(bucket: LogBucket) {
  let named = 0
  for (const n of Object.values(bucket.counts)) named += n
  return Math.max(bucket.total - named, 0)
}

function countIn(bucket: LogBucket, key: string) {
  return key === UNKEYED ? unkeyedOf(bucket) : (bucket.counts[key] ?? 0)
}

/**
 * When the matches happened, counted by level — or by what the lens names.
 *
 * A list of results answers "did it happen"; this answers "when did it start"
 * and "is it still happening", which is the question somebody searching a log
 * during an incident actually has. Counting by level rather than only in total
 * is what turns "something happened at 03:12" into "the errors started at 03:12
 * while the traffic stayed flat", and counting by event turns it into "the
 * deadlocks started at 03:12".
 *
 * Clicking a column narrows the search to it, which is the fast path from a
 * spike on the chart to the lines inside it.
 *
 * It is a row of the workspace, not a box in it: the hairline under it is the
 * only edge, and the columns sit on the same ground as the lines they count.
 */
export function Histogram({
  buckets,
  bucketSeconds,
  onZoom,
  className,
  by,
  lens,
  title = "Matches over time",
}: {
  buckets: LogBucket[]
  bucketSeconds: number
  onZoom: (since: Date, until: Date) => void
  className?: string
  /** What the counts are keyed by (`histogramBy`); the level when absent. */
  by?: string
  /** The lens that names an `event` series. */
  lens?: string
  /** What the chart counts, as its eyebrow and its accessible name. */
  title?: string
}) {
  const [hover, setHover] = useState<number | null>(null)
  const series = useMemo(() => seriesFor(buckets, by, lens), [buckets, by, lens])
  if (buckets.length === 0) return null

  const peak = Math.max(...buckets.map((b) => b.total), 1)
  const active = hover === null ? null : buckets[hover]
  // A window that crosses midnight has the same clock time at both ends, so
  // the axis read "23:00:00 … 23:00:00" over a full day of data.
  const first = new Date(buckets[0].start)
  const last = new Date(buckets[buckets.length - 1].start)
  const sameDay = first.toDateString() === last.toDateString()
  const edge = (iso: string) => (sameDay ? clock(iso) : timestamp(iso))
  const legend = [...series].reverse().filter((s) => buckets.some((b) => countIn(b, s.key) > 0))
  // Levels keep their own reading order in the legend, worst first.
  if (!by || by === "level") {
    legend.sort(
      (a, b) => LOG_LEVELS.indexOf(a.key as LogLevel) - LOG_LEVELS.indexOf(b.key as LogLevel),
    )
  }

  return (
    <div className={cn("shrink-0 border-b border-hairline px-3 pt-2 pb-1.5", className)}>
      <div className="mb-1.5 flex items-baseline justify-between gap-3 text-hint">
        <span className="eyebrow">
          {title} · one column is {widthLabel(bucketSeconds)}
        </span>
        <span className="numeric truncate text-muted-foreground">
          {active
            ? `${timestamp(active.start)} · ${active.total.toLocaleString()} ${active.total === 1 ? "line" : "lines"}`
            : `peak ${peak.toLocaleString()} · click a column to narrow to it`}
        </span>
      </div>
      <div
        className="flex h-14 items-end gap-px"
        onMouseLeave={() => setHover(null)}
        role="group"
        aria-label={title}
      >
        {buckets.map((bucket, i) => (
          <button
            key={bucket.start}
            onMouseEnter={() => setHover(i)}
            onClick={() => {
              const start = new Date(bucket.start)
              onZoom(start, new Date(start.getTime() + bucketSeconds * 1000))
            }}
            aria-label={`${timestamp(bucket.start)}, ${bucket.total} lines — narrow to this column`}
            className={cn(
              "flex h-full min-w-[3px] flex-1 flex-col justify-end rounded-sm focus-ring-inset transition-colors",
              hover === i ? "bg-row-hover" : "hover:bg-row-hover",
            )}
          >
            {series.map((s) => {
              const count = countIn(bucket, s.key)
              if (count === 0) return null
              return (
                <span
                  key={s.key}
                  className={cn("w-full", s.className)}
                  style={{
                    height: `${Math.max((count / peak) * 100, 1.5)}%`,
                    backgroundColor: s.color,
                  }}
                />
              )
            })}
          </button>
        ))}
      </div>
      <div className="mt-1 flex items-center justify-between gap-3 text-micro text-muted-foreground">
        <span className="numeric">{edge(buckets[0].start)}</span>
        <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
          {legend.map((s) => (
            <span key={s.key} className="flex items-center gap-1">
              <span
                className={cn("size-1.5 rounded-full", s.className)}
                style={{ backgroundColor: s.color }}
              />
              {s.label}
            </span>
          ))}
        </span>
        <span className="numeric">{edge(buckets[buckets.length - 1].start)}</span>
      </div>
    </div>
  )
}
