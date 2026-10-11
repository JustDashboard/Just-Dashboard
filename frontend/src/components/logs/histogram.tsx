"use client"

import { useMemo, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import { clock, plural, timestamp } from "@/lib/format"
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
  if (seconds < 60) return `${seconds} seconds`
  if (seconds < 3600) return seconds === 60 ? "a minute" : `${seconds / 60} minutes`
  if (seconds < 86400) return seconds === 3600 ? "an hour" : `${seconds / 3600} hours`
  return seconds === 86400 ? "a day" : `${seconds / 86400} days`
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
 * A press on a column narrows the window to it, and a drag across several
 * narrows it to the span — the fast path from a spike to the lines inside it.
 * Under the pointer the head reads the column out by series, in the series'
 * colours, so a red column is "3 errors of 124" before anything is pressed;
 * at rest it names the busiest column. "One column is 30s" used to be the
 * chart's title, which was the one thing on it nobody needed first.
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
  const [drag, setDrag] = useState<{ from: number; to: number } | null>(null)
  // A finger is a tap or a scroll of the page, never a drag across columns:
  // a touch captures the pointer to the column it began on, and a chart that
  // took the swipe kept the page from scrolling past it.
  const touched = useRef(false)
  const series = useMemo(() => seriesFor(buckets, by, lens), [buckets, by, lens])
  if (buckets.length === 0) return null

  const peak = Math.max(...buckets.map((b) => b.total), 1)
  const peakAt = buckets.findIndex((b) => b.total === peak)
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
  const lo = drag ? Math.min(drag.from, drag.to) : -1
  const hi = drag ? Math.max(drag.from, drag.to) : -1
  const finish = () => {
    if (!drag) return
    const start = new Date(buckets[lo].start)
    const end = new Date(new Date(buckets[hi].start).getTime() + bucketSeconds * 1000)
    setDrag(null)
    onZoom(start, end)
  }

  return (
    <div className={cn("shrink-0 border-b border-hairline px-3 pt-2.5 pb-2", className)}>
      <div className="mb-2 flex min-h-4 items-baseline justify-between gap-3 text-hint">
        <span className="eyebrow shrink-0">{title}</span>
        <span className="numeric flex min-w-0 items-center justify-end gap-x-2.5 truncate text-muted-foreground">
          {drag && lo !== hi ? (
            <span>
              {clock(buckets[lo].start)} – {clock(buckets[hi].start)} · release to zoom in
            </span>
          ) : active ? (
            <>
              <span className="text-foreground">{timestamp(active.start)}</span>
              <span>{plural(active.total, "line")}</span>
              {legend
                .filter((s) => countIn(active, s.key) > 0)
                .slice(0, 4)
                .map((s) => (
                  <span key={s.key} className="flex items-center gap-1">
                    <span
                      className={cn("size-1.5 rounded-full", s.className)}
                      style={{ backgroundColor: s.color }}
                    />
                    {countIn(active, s.key).toLocaleString()} {s.label}
                  </span>
                ))}
            </>
          ) : (
            <span className="max-sm:hidden">
              busiest {clock(buckets[peakAt].start)} · {plural(peak, "line")} in{" "}
              {widthLabel(bucketSeconds)} · press or drag to zoom
            </span>
          )}
        </span>
      </div>
      <div
        className="flex h-20 items-end gap-px select-none"
        onMouseLeave={() => setHover(null)}
        onPointerUp={finish}
        onPointerLeave={() => setDrag(null)}
        role="group"
        aria-label={title}
      >
        {buckets.map((bucket, i) => (
          <button
            key={bucket.start}
            onPointerEnter={() => {
              setHover(i)
              if (drag) setDrag({ ...drag, to: i })
            }}
            onPointerDown={(e) => {
              touched.current = e.pointerType === "touch"
              if (!touched.current && e.button === 0) setDrag({ from: i, to: i })
            }}
            onClick={() => {
              if (!touched.current) return
              const start = new Date(bucket.start)
              onZoom(start, new Date(start.getTime() + bucketSeconds * 1000))
            }}
            onKeyDown={(e) => {
              if (e.key !== "Enter" && e.key !== " ") return
              e.preventDefault()
              const start = new Date(bucket.start)
              onZoom(start, new Date(start.getTime() + bucketSeconds * 1000))
            }}
            aria-label={`${timestamp(bucket.start)}, ${bucket.total} lines — narrow to this column`}
            className={cn(
              "flex h-full min-w-[3px] flex-1 flex-col justify-end rounded-sm focus-ring-inset transition-colors",
              i >= lo && i <= hi
                ? "bg-accent"
                : hover === i
                  ? "bg-row-hover"
                  : "hover:bg-row-hover",
            )}
          >
            {series.map((s) => {
              const count = countIn(bucket, s.key)
              if (count === 0) return null
              return (
                <span
                  key={s.key}
                  className={cn(
                    "w-full transition-[height] duration-300 ease-out first:rounded-t-[2px]",
                    s.className,
                  )}
                  style={{
                    height: `${Math.max((count / peak) * 100, 2)}%`,
                    backgroundColor: s.color,
                  }}
                />
              )
            })}
          </button>
        ))}
      </div>
      <div className="mt-1.5 flex items-center justify-between gap-3 text-micro text-muted-foreground">
        <span className="numeric">{edge(buckets[0].start)}</span>
        <span className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1">
          {legend.map((s) => (
            <span key={s.key} className="flex items-center gap-1.5">
              <span
                className={cn("size-2 rounded-[2px]", s.className)}
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
