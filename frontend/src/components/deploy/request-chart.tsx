"use client"

import { useState } from "react"
import { cn } from "@/lib/utils"
import { clock, timestamp } from "@/lib/format"
import type { RequestBucket } from "@/lib/types"
import { CLASS_DOT, CLASS_LABEL, STATUS_CLASSES, latency, type StatusClass } from "@/lib/requests"

// Worst last, which in a column packed to its end puts the alarming colours
// along the axis — the same order the log histogram next door stacks its
// levels in, so the two charts read as one instrument. The swatches are the
// ones the chips and the rows use, so a red column here is the same red as the
// requests it counts.
const STACK = [...STATUS_CLASSES].reverse()

function widthLabel(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`
  return `${Math.round(seconds / 86400)}d`
}

/**
 * When the requests happened, counted by status family, with the tail latency
 * drawn over them.
 *
 * A table of requests answers "what happened"; this answers "when did it start"
 * and "is it still happening", which is the question somebody looking at a
 * deployment during an incident actually has. The two readings are one chart
 * on purpose: a wall of red with a flat p95 is a deployment refusing requests,
 * and the same red with the p95 climbing off the top is one falling over. Those
 * are different afternoons, and two charts side by side make the reader
 * correlate them by eye.
 *
 * It is a row of the workspace, not a box in it: the hairline under it is the
 * only edge, and the columns sit on the same ground as the rows they count.
 */
/**
 * A moment worth a mark on the chart: a release going live, a container
 * exiting, being restarted or killed. A spike of red with a deploy mark at
 * its foot is a different afternoon from the same spike with none.
 */
export type ChartMarker = {
  at: string
  kind: "deploy" | "restart" | "failure"
  label: string
}

const MARKER: Record<ChartMarker["kind"], { line: string; word: string }> = {
  deploy: { line: "bg-brand", word: "deploy" },
  restart: { line: "bg-warning", word: "restart" },
  failure: { line: "bg-destructive", word: "exit" },
}

export function RequestChart({
  buckets,
  bucketSeconds,
  latencyKnown,
  markers = [],
  onZoom,
  className,
}: {
  buckets: RequestBucket[]
  bucketSeconds: number
  /** nginx's stock format carries no duration, so there is no line to draw. */
  latencyKnown: boolean
  markers?: ChartMarker[]
  onZoom: (since: Date, until: Date) => void
  className?: string
}) {
  const [hover, setHover] = useState<number | null>(null)
  if (buckets.length === 0) return null

  const peak = Math.max(...buckets.map((b) => b.total), 1)
  const slowest = Math.max(...buckets.map((b) => b.p95 ?? 0), 1)
  const active = hover === null ? null : buckets[hover]
  const showLatency = latencyKnown && buckets.some((b) => b.p95 !== undefined)

  // A window that crosses midnight has the same clock time at both ends, so
  // the axis read "23:00 … 23:00" over a full day of traffic.
  const first = new Date(buckets[0].start)
  const last = new Date(buckets[buckets.length - 1].start)
  const sameDay = first.toDateString() === last.toDateString()
  const edge = (iso: string) => (sameDay ? clock(iso) : timestamp(iso))
  // Where a marker sits, as a share of the chart's own span — the columns are
  // equal widths over equal time, so time maps to width directly.
  const spanStart = first.getTime()
  const spanEnd = last.getTime() + bucketSeconds * 1000
  const placed = markers
    .map((m) => ({ ...m, x: (Date.parse(m.at) - spanStart) / (spanEnd - spanStart) }))
    .filter((m) => Number.isFinite(m.x) && m.x >= 0 && m.x <= 1)

  return (
    <div className={cn("shrink-0 border-b border-hairline px-3 pt-2 pb-1.5", className)}>
      <div className="mb-1.5 flex items-baseline justify-between gap-3 text-hint">
        <span className="eyebrow">Requests over time · one column is {widthLabel(bucketSeconds)}</span>
        <span className="numeric truncate text-muted-foreground">
          {active
            ? [
                timestamp(active.start),
                `${active.total.toLocaleString()} ${active.total === 1 ? "request" : "requests"}`,
                active.p95 !== undefined ? `p95 ${latency(active.p95)}` : null,
              ]
                .filter(Boolean)
                .join(" · ")
            : `peak ${peak.toLocaleString()} · click a column to narrow`}
        </span>
      </div>

      <div
        className="relative flex h-20 items-end gap-px"
        onMouseLeave={() => setHover(null)}
        role="group"
        aria-label="Requests over time"
      >
        {buckets.map((bucket, i) => (
          <button
            key={bucket.start}
            onMouseEnter={() => setHover(i)}
            onClick={() => {
              const start = new Date(bucket.start)
              onZoom(start, new Date(start.getTime() + bucketSeconds * 1000))
            }}
            aria-label={`${timestamp(bucket.start)}, ${bucket.total} requests — narrow to this column`}
            className={cn(
              "flex h-full min-w-[3px] flex-1 flex-col justify-end rounded-sm focus-ring-inset transition-colors",
              hover === i ? "bg-row-hover" : "hover:bg-row-hover",
            )}
          >
            {STACK.map((klass) => {
              const count = bucket.counts[klass] ?? 0
              if (count === 0) return null
              return (
                <span
                  key={klass}
                  className={cn("w-full", CLASS_DOT[klass as StatusClass])}
                  style={{ height: `${Math.max((count / peak) * 100, 1.5)}%` }}
                />
              )
            })}
          </button>
        ))}

        {placed.map((m, i) => (
          <span
            key={`${m.at}:${i}`}
            title={`${m.label} · ${timestamp(m.at)}`}
            aria-label={`${m.label} at ${timestamp(m.at)}`}
            className={cn("pointer-events-auto absolute inset-y-0 w-px", MARKER[m.kind].line)}
            style={{ left: `${m.x * 100}%` }}
          >
            <span
              aria-hidden
              className={cn("absolute -top-0.5 -left-[3px] size-[7px] rounded-full", MARKER[m.kind].line)}
            />
          </span>
        ))}

        {/*
          The latency line rides over the columns rather than beside them, as a
          row of marks rather than a path: an SVG polyline through sixty points
          needs its own coordinate space, and the one thing a reader does with
          this line is see whether it is climbing.
        */}
        {showLatency && (
          <div aria-hidden className="pointer-events-none absolute inset-0 flex items-end gap-px">
            {buckets.map((bucket) => (
              <span key={bucket.start} className="relative h-full min-w-[3px] flex-1">
                {bucket.p95 !== undefined && (
                  <span
                    className="absolute right-0 left-0 h-px bg-warning/80"
                    style={{ bottom: `${Math.min((bucket.p95 / slowest) * 100, 100)}%` }}
                  />
                )}
              </span>
            ))}
          </div>
        )}
      </div>

      <div className="mt-1 flex items-center justify-between gap-3 text-micro text-muted-foreground">
        <span className="numeric">{edge(buckets[0].start)}</span>
        <span className="flex flex-wrap items-center justify-center gap-x-2.5 gap-y-1">
          {STATUS_CLASSES.filter((klass) => buckets.some((b) => (b.counts[klass] ?? 0) > 0)).map(
            (klass) => (
              <span key={klass} className="flex items-center gap-1">
                <span className={cn("size-1.5 rounded-full", CLASS_DOT[klass])} />
                {klass} {CLASS_LABEL[klass]}
              </span>
            ),
          )}
          {showLatency && (
            <span className="flex items-center gap-1">
              <span className="h-px w-2.5 bg-warning/80" />
              p95 to {latency(slowest)}
            </span>
          )}
          {(["deploy", "restart", "failure"] as const)
            .filter((kind) => placed.some((m) => m.kind === kind))
            .map((kind) => (
              <span key={kind} className="flex items-center gap-1">
                <span className={cn("size-1.5 rounded-full", MARKER[kind].line)} />
                {MARKER[kind].word}
              </span>
            ))}
        </span>
        <span className="numeric">{edge(buckets[buckets.length - 1].start)}</span>
      </div>
    </div>
  )
}
