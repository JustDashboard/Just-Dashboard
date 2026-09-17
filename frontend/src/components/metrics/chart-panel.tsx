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
  plain,
  className,
  footer,
}: {
  title: string
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
  /** No frame: title, hairline, plot — for a chart that is its own block on the page. */
  plain?: boolean
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
    <Panel plain={plain} className={className}>
      <PanelHeader title={title} actions={actions} />
      {/* Tighter than a panel's default padding, and deliberately so: a chart
          brings its own margins — recharts reserves a gutter for the axis and
          a strip under it for the ticks — so the body's `p-4` was drawing a
          second inset around one that already existed. Ten of these on a page
          is most of a screen of nothing.

          The body remounts when the first rows land so the plot rises once
          on arrival (§11), and never again while the window refreshes. */}
      <PanelBody
        key={empty ? "empty" : "data"}
        className={cn(
          "flex flex-1 flex-col gap-2.5 px-4 pt-3 pb-4 group-data-[plain]/panel:px-0 group-data-[plain]/panel:pt-2",
          !empty && "animate-rise",
        )}
      >
        {empty ? (
          <ChartPlaceholder
            note={note ?? "Nothing recorded in this window."}
            height={height}
            plain={plain}
          />
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
            {legend && (
              <SeriesLegend
                rows={rows}
                series={present}
                unit={unit}
                format={format}
                // A hairline, not a gap: the numbers belong to the plot above
                // them, and a rule says "same object, second part" where more
                // empty space would only say "two things".
                className="border-t border-hairline pt-2"
              />
            )}
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
  plain,
  className,
}: {
  note: string
  height?: number
  /** Inside a plain panel the sentence sits on the page's ground: a dashed box would be the only frame on it. */
  plain?: boolean
  className?: string
}) {
  return (
    <div
      style={{ minHeight: height }}
      className={cn(
        "flex w-full flex-1 items-center justify-center px-4 text-center text-xs text-muted-foreground",
        !plain && "rounded-lg border border-dashed border-hairline bg-surface-sunken",
        className,
      )}
    >
      {note}
    </div>
  )
}
