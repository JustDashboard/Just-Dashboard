"use client"

import { memo, useMemo } from "react"
import { duration } from "@/lib/format"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { Metric, MetricStrip } from "@/components/page"
import {
  CHART_UNITS,
  drawnRows,
  highest,
  offeredViews,
  roundTicks,
  type ChartView,
} from "@/components/database/home/charts"
import { chartRows, heldSeconds, type Sample } from "@/components/database/home/samples"
import { SAMPLE_EVERY_MS } from "@/components/database/home/use-samples"
import { OVERVIEW_CHARTS } from "@/components/database/ops/performance-figures"
import { Notes } from "@/components/database/ops/performance-parts"
import type { DbServerStats } from "@/components/database/ops/performance-types"

const QUIET_CACHE = "No block was read in this window, so there is no hit rate to draw."

/**
 * What the server has been doing since this page was opened, as every chart
 * its engine can fill: sessions by what they are doing, transactions,
 * statements, rows, the cache, lock trouble, what it writes to its log and
 * sends over the network.
 *
 * The charts are drawn from the page's own samples — one snapshot every few
 * seconds, a rate the difference of two — so a chart of rates is empty for
 * the first seconds and nothing here is recorded history: the charts start
 * again when the page does. The line under them says so.
 *
 * Under the charts is the one thing here that is about the dashboard and not
 * the server: the connections it holds to it for its own reads.
 */
export function OverviewView({
  samples,
  stats,
  loading,
  error,
}: {
  samples: readonly Sample[]
  stats: DbServerStats | undefined
  /** No sample has landed yet. */
  loading: boolean
  /** Why the latest poll failed. What was sampled before it is still drawn. */
  error?: Error
}) {
  const newest = samples[samples.length - 1]
  // Until a sample says which charts this engine fills, the first four hold
  // the page's shape. After that the list is held by which charts are
  // offered, not by the samples: it is the same from one sample to the next.
  const offeredKey = newest
    ? offeredViews(OVERVIEW_CHARTS, newest)
        .map((entry) => entry.id)
        .join(" ")
    : ""
  const offered = useMemo(
    () =>
      offeredKey
        ? OVERVIEW_CHARTS.filter((entry) => offeredKey.split(" ").includes(entry.id))
        : OVERVIEW_CHARTS.slice(0, 4),
    [offeredKey],
  )
  const held = heldSeconds(samples)
  const every = SAMPLE_EVERY_MS / 1000
  const note = loading
    ? "Reading the server…"
    : error && samples.length === 0
      ? `The server's statistics could not be read: ${error.message}`
      : `A rate is the difference between two readings: the first appears ${every * 2} seconds after the page opens.`
  const pool = stats?.pool

  return (
    <div className="space-y-6">
      <div className="grid gap-x-8 gap-y-6 lg:grid-cols-2 2xl:grid-cols-3 [&>*]:min-w-0">
        {offered.map((chart) => (
          <OverviewChart
            key={chart.id}
            chart={chart}
            samples={samples}
            // A share has nothing to say about a window in which nothing was
            // asked of the cache; that is not a rate still on its way.
            note={chart.unit === "percent" && samples.length > 1 ? QUIET_CACHE : note}
          />
        ))}
      </div>
      <p className="text-hint text-muted-foreground">
        {held > 0
          ? `Live readings over the last ${duration(held)}, one every ${every} seconds since this page was opened. Nothing here is recorded: the charts start again when the page does.`
          : `Live readings, one every ${every} seconds since this page was opened. Nothing here is recorded: the charts start again when the page does.`}
      </p>
      <Notes notes={stats?.notes} />
      {pool && (
        <section aria-label="This dashboard's connections" className="space-y-2">
          <p className="eyebrow">This dashboard&apos;s own connections to it</p>
          <MetricStrip>
            <Metric label="Open" value={`${pool.open} of ${pool.maxOpen}`} />
            <Metric label="In use" value={pool.inUse.toLocaleString()} />
            <Metric label="Idle" value={pool.idle.toLocaleString()} />
            <Metric
              label="Waited for one"
              value={pool.waitCount.toLocaleString()}
              hint={pool.waitCount > 0 ? `${pool.waitDuration} in all` : undefined}
            />
          </MetricStrip>
        </section>
      )}
    </div>
  )
}

/**
 * One chart of the Overview. Its rows and its axis are worked out here and
 * held by what they are, so a new sample redraws the charts whose lines moved
 * and none of the others: `ChartPanel` is memoised on its props.
 */
const OverviewChart = memo(function OverviewChart({
  chart,
  samples,
  note,
}: {
  chart: ChartView
  samples: readonly Sample[]
  note: string
}) {
  const rows = useMemo(
    () => drawnRows(chartRows(samples, chart.sources), chart.series),
    [samples, chart],
  )
  const unit = CHART_UNITS[chart.unit]
  // The axis of a count or a rate is stepped on round figures here: a scale
  // fitted to three sessions puts its ticks at 2.25 and 1.5.
  const steps = unit.stepped
    ? roundTicks(highest(rows, chart.series), unit.stepped === "whole")
    : undefined
  const stepKey = steps?.join(" ") ?? ""
  const axis = useMemo(() => {
    if (!stepKey) return undefined
    const ticks = stepKey.split(" ").map(Number)
    return { ticks, domain: [0, ticks[ticks.length - 1]] as [number, number] }
  }, [stepKey])
  return (
    <ChartPanel
      plain
      title={chart.label}
      rows={rows}
      series={chart.series}
      unit={unit.unit}
      format={unit.format}
      axisFormat={unit.axisFormat}
      domain={axis?.domain ?? unit.domain}
      yTicks={axis?.ticks ?? unit.yTicks}
      height={160}
      showPeaks={false}
      note={note}
    />
  )
})
