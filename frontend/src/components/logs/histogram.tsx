"use client"

import { useState } from "react"
import { cn } from "@/lib/utils"
import { clock, timestamp } from "@/lib/format"
import type { LogBucket } from "@/lib/types"
import { LEVEL_DOT, LEVEL_LABEL, LOG_LEVELS } from "@/lib/log-filter"

// Worst last, so the alarming colours sit at the top of a stacked column where
// the eye lands, rather than being buried under a block of debug. The swatches
// are the ones the level chips and the level column use, so a red column here
// is the same red as the lines it counts.
const STACK = [...LOG_LEVELS].reverse()

function widthLabel(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${seconds / 60}m`
  if (seconds < 86400) return `${seconds / 3600}h`
  return `${seconds / 86400}d`
}

/**
 * When the matches happened, counted by level.
 *
 * A list of results answers "did it happen"; this answers "when did it start"
 * and "is it still happening", which is the question somebody searching a log
 * during an incident actually has. Counting by level rather than only in total
 * is what turns "something happened at 03:12" into "the errors started at 03:12
 * while the traffic stayed flat".
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
}: {
  buckets: LogBucket[]
  bucketSeconds: number
  onZoom: (since: Date, until: Date) => void
  className?: string
}) {
  const [hover, setHover] = useState<number | null>(null)
  if (buckets.length === 0) return null

  const peak = Math.max(...buckets.map((b) => b.total), 1)
  const active = hover === null ? null : buckets[hover]
  // A window that crosses midnight has the same clock time at both ends, so
  // the axis read "23:00:00 … 23:00:00" over a full day of data.
  const first = new Date(buckets[0].start)
  const last = new Date(buckets[buckets.length - 1].start)
  const sameDay = first.toDateString() === last.toDateString()
  const edge = (iso: string) => (sameDay ? clock(iso) : timestamp(iso))

  return (
    <div className={cn("shrink-0 border-b border-hairline px-3 pt-2 pb-1.5", className)}>
      <div className="mb-1.5 flex items-baseline justify-between gap-3 text-hint">
        <span className="eyebrow">
          Matches over time · one column is {widthLabel(bucketSeconds)}
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
        aria-label="Matches over time"
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
            {STACK.map((level) => {
              const count = bucket.counts[level] ?? 0
              if (count === 0) return null
              return (
                <span
                  key={level}
                  className={cn("w-full", LEVEL_DOT[level])}
                  style={{ height: `${Math.max((count / peak) * 100, 1.5)}%` }}
                />
              )
            })}
          </button>
        ))}
      </div>
      <div className="mt-1 flex items-center justify-between gap-3 text-micro text-muted-foreground">
        <span className="numeric">{edge(buckets[0].start)}</span>
        <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
          {LOG_LEVELS.filter((level) => buckets.some((b) => (b.counts[level] ?? 0) > 0)).map(
            (level) => (
              <span key={level} className="flex items-center gap-1">
                <span className={cn("size-1.5 rounded-full", LEVEL_DOT[level])} />
                {LEVEL_LABEL[level]}
              </span>
            ),
          )}
        </span>
        <span className="numeric">{edge(buckets[buckets.length - 1].start)}</span>
      </div>
    </div>
  )
}
