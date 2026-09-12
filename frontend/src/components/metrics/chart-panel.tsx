"use client"

import { memo, useMemo } from "react"
import { cn } from "@/lib/utils"
import type { MetricEvent } from "@/lib/types"
import type { ChartConfig } from "@/components/ui/chart"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { MetricChart, type ChartRowLike, type Series } from "@/components/metrics/metric-chart"
import { SeriesLegend } from "@/components/metrics/series-legend"

/**
 * A panel whose content is one chart and its numbers.
 *
 * Every metric panel on the dashboard is this shape — header, chart, legend —
 * and assembling it in one place is what stops the next chart from arriving
 * with its own tooltip format, its own empty state and its own idea of how
 * tall a chart is. Adding a measurement to the product should be a matter of
 * naming its series, not of rebuilding a recharts tree.
 */
/**
 * Memoised, and the call sites depend on it.
 *
 * The Overview page reads the live metrics socket, so it re-renders every two
 * seconds whether or not a recorded chart has anything new to draw. Without
 * this, that tick rebuilt the element tree of every chart on the page — ten
 * charts, ~38 series — to change a number in a stat tile.
 *
 * The bail-out is a shallow prop comparison, so it only works if the props are
 * referentially stable: formatters, domains and threshold arrays are module
 * constants at the call sites rather than inline literals, and `rows` comes
 * from a memo that does not depend on the live buffer. An inline
 * `format={(v) => rate(v)}` silently switches this off.
 */
export const ChartPanel = memo(function ChartPanel({
  title,
  icon,
  actions,
  rows,
  series,
  unit,
  format,
  axisFormat,
  domain,
  height = 180,
  events,
  onZoom,
  showPeaks = true,
  stacked = false,
  thresholds,
  note,
  legend = true,
  className,
  footer,
}: {
  title: string
  icon?: React.ComponentType<{ className?: string }>
  actions?: React.ReactNode
  rows: ChartRowLike[]
  series: Series[]
  unit?: string
  format?: (value: number) => string
  /** Shorter tick labels, where the full format is too wide for the gutter. */
  axisFormat?: (value: number) => string
  domain?: [number | string, number | string]
  height?: number
  events?: MetricEvent[]
  onZoom?: (from: number, to: number) => void
  showPeaks?: boolean
  stacked?: boolean
  thresholds?: { value: number; label: string; tone?: "warning" | "danger" }[]
  /** What to say when there is nothing to draw. */
  note?: string
  legend?: boolean
  className?: string
  footer?: React.ReactNode
}) {
  const config = useMemo<ChartConfig>(
    () => Object.fromEntries(series.map((s) => [s.key, { label: s.label, color: s.color }])),
    [series],
  )

  // A series with no numbers anywhere in the window is treated as absent
  // rather than drawn flat at zero. That distinction is the whole point on a
  // kernel without PSI, or a host whose disks report no latency counters.
  const present = useMemo(
    () => series.filter((s) => rows.some((row) => typeof row[s.key] === "number")),
    [series, rows],
  )
  const empty = rows.length === 0 || present.length === 0

  return (
    <Panel className={className}>
      <PanelHeader icon={icon} title={title} actions={actions} />
      <PanelBody className="flex flex-1 flex-col gap-3">
        {empty ? (
          <ChartPlaceholder note={note ?? "Nothing recorded in this window."} height={height} />
        ) : (
          <>
            <MetricChart
              rows={rows}
              series={present}
              config={config}
              height={height}
              domain={domain}
              unit={unit}
              format={format}
              axisFormat={axisFormat}
              events={events}
              onZoom={onZoom}
              showPeaks={showPeaks}
              stacked={stacked}
              thresholds={thresholds}
            />
            {legend && <SeriesLegend rows={rows} series={present} unit={unit} format={format} />}
          </>
        )}
        {footer}
      </PanelBody>
    </Panel>
  )
})

export function ChartPlaceholder({
  note,
  height = 180,
  className,
}: {
  note: string
  height?: number
  className?: string
}) {
  return (
    <div
      style={{ minHeight: height }}
      className={cn(
        "flex w-full flex-1 items-center justify-center rounded-lg border border-dashed border-hairline bg-surface-sunken px-4 text-center text-xs text-muted-foreground",
        className,
      )}
    >
      {note}
    </div>
  )
}
