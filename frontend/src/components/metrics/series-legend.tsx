"use client"

import { useMemo, useSyncExternalStore } from "react"
import { cn } from "@/lib/utils"
import { getCrosshair, getServerCrosshair, subscribeCrosshair } from "@/lib/metrics-crosshair"
import type { ChartRowLike, Series } from "@/components/metrics/metric-chart"

/**
 * The numbers under a chart.
 *
 * A legend that only names its colours wastes the row. Grafana's answer — min,
 * mean, max and last per series — is the right one, because those four figures
 * answer most of what anyone asks a chart: how bad did it get, is that normal
 * for this window, and where is it now.
 *
 * The column that makes it more than a copy is **At cursor**: while the
 * pointer is over any chart on the page, every legend on the page switches to
 * the value its series held at that instant. So reading "what was memory doing
 * during that CPU spike" is a matter of hovering the spike and looking down,
 * rather than aligning two graphs by eye.
 */
export function SeriesLegend({
  rows,
  series,
  format,
  unit,
  className,
}: {
  rows: ChartRowLike[]
  series: Series[]
  format?: (value: number) => string
  unit?: string
  className?: string
}) {
  const { ts: hovered } = useSyncExternalStore(subscribeCrosshair, getCrosshair, getServerCrosshair)

  const stats = useMemo(() => summarise(rows, series), [rows, series])
  // The nearest row rather than an exact match: the charts on a page are
  // bucketed independently, so the instant the pointer is over in one of them
  // rarely lands exactly on another's bucket boundary.
  const cursor = useMemo(
    () => (hovered === null ? null : nearestRow(rows, hovered)),
    [rows, hovered],
  )

  if (rows.length === 0) return null

  return (
    <div className={cn("min-w-0 overflow-x-auto", className)}>
      <table className="w-full min-w-[22rem] text-hint">
        <thead>
          <tr className="text-muted-foreground">
            <th className="pb-1 text-left font-normal">Series</th>
            <th className="pb-1 pl-3 text-right font-normal">Min</th>
            <th className="pb-1 pl-3 text-right font-normal">Mean</th>
            <th className="pb-1 pl-3 text-right font-normal">Max</th>
            {/* The header changes with the pointer rather than a column
                appearing and disappearing, which would shift the other three
                sideways every time the mouse crossed a chart. */}
            <th className={cn("pb-1 pl-3 text-right font-normal", cursor && "text-foreground")}>
              {cursor ? "At cursor" : "Last"}
            </th>
          </tr>
        </thead>
        <tbody>
          {series.map((s) => {
            const stat = stats[s.key]
            if (!stat) return null
            const render =
              s.format ?? format ?? ((v: number) => (unit === "%" ? `${v}${unit}` : String(v)))
            const trailing = cursor ? numberAt(cursor, s.key) : stat.last
            return (
              <tr key={s.key} className="border-t border-hairline/60">
                <td className="py-1">
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span
                      aria-hidden
                      className="size-2 shrink-0 rounded-[2px]"
                      style={{ background: s.color }}
                    />
                    <span className="truncate">{s.label}</span>
                  </span>
                </td>
                <td className="numeric py-1 pl-3 text-right text-muted-foreground">
                  {render(stat.min)}
                </td>
                <td className="numeric py-1 pl-3 text-right text-muted-foreground">
                  {render(stat.mean)}
                </td>
                {/* The max is the figure the whole peaks apparatus exists to
                    preserve, so it is the one drawn at full contrast. */}
                <td className="numeric py-1 pl-3 text-right font-medium">{render(stat.max)}</td>
                <td
                  className={cn(
                    "numeric py-1 pl-3 text-right",
                    cursor ? "font-medium" : "text-muted-foreground",
                  )}
                >
                  {trailing === null ? "—" : render(trailing)}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

type Stat = { min: number; mean: number; max: number; last: number | null }

/**
 * Min, mean and max per series.
 *
 * The max reads the peak column where a series has one: on a downsampled
 * window the mean column's own maximum is the largest *average*, which is
 * exactly the figure that hides a spike. Reporting that as "Max" would undo
 * the reason the backend stores peaks at all.
 */
function summarise(rows: ChartRowLike[], series: Series[]): Record<string, Stat> {
  const out: Record<string, Stat> = {}
  for (const s of series) {
    let min = Infinity
    let max = -Infinity
    let sum = 0
    let count = 0
    let last: number | null = null

    for (const row of rows) {
      const value = numberAt(row, s.key)
      if (value === null) continue
      if (value < min) min = value
      sum += value
      count++
      last = value
      const peak = s.peakKey ? numberAt(row, s.peakKey) : null
      const high = peak !== null && peak > value ? peak : value
      if (high > max) max = high
    }
    if (count === 0) continue
    out[s.key] = { min, mean: round(sum / count), max, last }
  }
  return out
}

function numberAt(row: ChartRowLike, key: string): number | null {
  const value = row[key]
  return typeof value === "number" && Number.isFinite(value) ? value : null
}

function nearestRow(rows: ChartRowLike[], ts: number): ChartRowLike | null {
  if (rows.length === 0) return null

  // Binary search, not a scan: this runs for every legend on the page every
  // time the pointer moves to a new bucket, and a linear pass over ten
  // legends' worth of 240 rows is work done sixty times a second to find a
  // number that is already sorted.
  let lo = 0
  let hi = rows.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (rows[mid].ts < ts) lo = mid + 1
    else hi = mid
  }
  // The search lands on the first row at or after ts; its predecessor may be
  // closer.
  let best = rows[lo]
  let bestGap = Math.abs(best.ts - ts)
  if (lo > 0) {
    const before = rows[lo - 1]
    const gap = Math.abs(before.ts - ts)
    if (gap < bestGap) {
      best = before
      bestGap = gap
    }
  }
  // Tolerance is one bucket, not a share of the window. A seven-day chart's
  // window is 168 hours wide, so any fraction of it large enough to be useful
  // on a one-hour chart would here accept a row from the middle of yesterday
  // and print it as the value under the cursor. A bucket is the resolution
  // this series actually has, and beyond it there is nothing honest to show.
  return bestGap <= bucketOf(rows) ? best : null
}

function bucketOf(rows: ChartRowLike[]): number {
  if (rows.length < 2) return 60_000
  const span = rows[rows.length - 1].ts - rows[0].ts
  // A little over one bucket: the charts on a page are bucketed independently,
  // so the hovered instant rarely lands exactly on this series' boundary.
  return Math.max((span / (rows.length - 1)) * 1.5, 2_000)
}

function round(value: number): number {
  return Math.round(value * 100) / 100
}
