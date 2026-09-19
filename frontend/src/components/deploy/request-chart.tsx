"use client"

import { useMemo } from "react"
import type { MetricEvent, RequestBucket } from "@/lib/types"
import { latency } from "@/lib/requests"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { ChartRowLike, Series } from "@/components/metrics/metric-chart"

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

/**
 * The request series. Volume is an area — the chart's ramp, the chart's
 * first colour, as every primary series on the Metrics page — and the two
 * families that mean something went wrong are lines over it, which sit on
 * the floor until they don't. Stacking the families into one solid block was
 * tried first: on a working deployment the ok share is ninety-five per cent
 * of every column, and a pale slab that size swallows the red sliver that is
 * the only thing anyone is looking for. Redirects fold into the volume; they
 * are traffic, not trouble.
 *
 * Module constants rather than inline literals: `ChartPanel` is memoised on
 * its props, and a series array rebuilt per render is a chart rebuilt per
 * render.
 */
const REQUEST_SERIES: Series[] = [
  { key: "total", label: "Requests", color: "var(--chart-1)", kind: "area" },
  { key: "refused", label: "4xx client error", color: "var(--warning)", kind: "line" },
  { key: "failed", label: "5xx server error", color: "var(--destructive)", kind: "line" },
]

const LATENCY_SERIES: Series[] = [{ key: "p95", label: "p95", color: "var(--warning)", kind: "line" }]

const count = (value: number) => Math.round(value).toLocaleString()
const millis = (value: number) => latency(value)

/**
 * When the requests happened, by status family, with the slow tenth under it
 * on the same axis.
 *
 * Two charts rather than one with two scales: requests are a volume and a
 * p95 is a level, and a line for one drawn over an area for the other lies
 * about whichever it was not scaled for. Stacked one above the other on one
 * time axis, a wall of red with a flat p95 is a deployment refusing requests,
 * and the same red with the p95 climbing is one falling over — different
 * afternoons, read without correlating by eye across the page.
 *
 * Both go through the house chart: numeric time axis (a hole in the record
 * is a gap, not a squeezed column), a synced crosshair readout of every
 * series at one instant, drag to narrow, and the marks — a release going
 * live in the deploy colour the Metrics page uses, an exit in danger, a
 * restart in warning.
 */
export function RequestChart({
  buckets,
  bucketSeconds,
  latencyKnown,
  showLatency = true,
  markers = [],
  onZoom,
}: {
  buckets: RequestBucket[]
  bucketSeconds: number
  /** nginx's stock format carries no duration, so there is no p95 to draw. */
  latencyKnown: boolean
  /**
   * Whether to draw the p95 under the volume. Insights does — the readings
   * live there; Requests does not, because two charts before the rows left
   * the rows, which are that view's point, a third of the pane.
   */
  showLatency?: boolean
  markers?: ChartMarker[]
  onZoom: (since: Date, until: Date) => void
}) {
  const rows = useMemo<ChartRowLike[]>(
    () =>
      buckets.map((bucket) => ({
        ts: Date.parse(bucket.start),
        total: bucket.total,
        refused: bucket.counts["4xx"] ?? 0,
        failed: bucket.counts["5xx"] ?? 0,
        ...(bucket.p95 !== undefined ? { p95: bucket.p95 } : {}),
      })),
    [buckets],
  )
  const events = useMemo<MetricEvent[]>(
    () =>
      markers.map((marker) => ({
        ts: marker.at,
        title: marker.label,
        kind: marker.kind === "deploy" ? "deploy" : marker.kind === "restart" ? "reboot" : "action",
        severity: marker.kind === "failure" ? "error" : marker.kind === "restart" ? "warning" : "info",
      })),
    [markers],
  )
  const zoom = (from: number, to: number) => onZoom(new Date(from), new Date(to))
  const hasLatency = showLatency && latencyKnown && rows.some((row) => typeof row.p95 === "number")

  if (rows.length === 0) return null
  return (
    <div className="shrink-0 border-b border-hairline px-3 pt-2 pb-2">
      <ChartPanel
        plain
        title="Requests"
        rows={rows}
        series={REQUEST_SERIES}
        height={120}
        format={count}
        events={events}
        onZoom={zoom}
        showPeaks={false}
        legend={false}
        note="No requests in this window."
        footer={<Legend series={REQUEST_SERIES} caption={`One point is ${widthLabel(bucketSeconds)}. Drag to narrow.`} />}
      />
      {hasLatency && (
        <ChartPanel
          plain
          title="Slowest tenth"
          rows={rows}
          series={LATENCY_SERIES}
          height={72}
          format={millis}
          events={events}
          onZoom={zoom}
          showPeaks={false}
          legend={false}
          className="mt-2"
          footer={<Legend series={LATENCY_SERIES} caption="p95 of each point's requests." />}
        />
      )}
    </div>
  )
}

/**
 * A one-line legend: the swatches and names, and the caption. The full
 * stats table the Metrics page hangs under a chart is the right thing there,
 * where the chart is the page; here the chart is one row of a workspace and
 * the rows under it are the point.
 */
function Legend({ series, caption }: { series: Series[]; caption: string }) {
  return (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
      {series.map((s) => (
        <span key={s.key} className="flex items-center gap-1.5">
          <span aria-hidden className="size-1.5 rounded-full" style={{ background: s.color }} />
          {s.label}
        </span>
      ))}
      <span className="ml-auto">{caption}</span>
    </span>
  )
}

function widthLabel(seconds: number) {
  if (seconds < 60) return `${seconds} seconds`
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`
  if (seconds < 86400) return `${Math.round(seconds / 3600)} h`
  return `${Math.round(seconds / 86400)} d`
}
