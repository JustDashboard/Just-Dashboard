"use client"

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
import { overviewCharts, quietChart } from "@/components/database/ops/performance-figures"
import { Notes } from "@/components/database/ops/performance-parts"
import type { DbServerStats } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { Metric, MetricStrip } from "@/components/page"
import { duration } from "@/lib/format"
import { cn } from "@/lib/utils"
import { memo, useMemo } from "react"

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
  const { engine } = useDatabase()
  const transactions = engine.can("transactions")
  const locks = engine.can("locks")
  const charts = useMemo(() => overviewCharts({ transactions, locks }), [transactions, locks])
  const newest = samples[samples.length - 1]
  // Until a sample says which charts this engine fills, the first four hold
  // the page's shape. After that the two lists are held by which charts are
  // in them, not by the samples: they are the same from one sample to the
  // next, and `ChartPanel` is memoised on what it is handed.
  const offered = newest ? offeredViews(charts, newest) : charts.slice(0, 4)
  const drawnKey = offered
    .filter((chart) => !quietChart(chart, samples))
    .map((chart) => chart.id)
    .join(" ")
  const offeredKey = offered.map((chart) => chart.id).join(" ")
  const { drawn, quiet } = useMemo(() => {
    const ids = new Set(drawnKey.split(" "))
    const all = charts.filter((chart) => offeredKey.split(" ").includes(chart.id))
    return {
      drawn: all.filter((chart) => ids.has(chart.id)),
      quiet: all.filter((chart) => !ids.has(chart.id)),
    }
  }, [charts, drawnKey, offeredKey])
  const held = heldSeconds(samples)
  const every = SAMPLE_EVERY_MS / 1000
  const note = loading
    ? "Reading the server…"
    : error && samples.length === 0
      ? `The server's statistics could not be read: ${error.message}`
      : "The recorder is collecting activity; rates need two saved readings."
  const pool = stats?.pool

  return (
    <div className="space-y-6">
      {drawn.length > 0 && (
        <div className="grid gap-x-8 gap-y-6 lg:grid-cols-2 2xl:grid-cols-6 [&>*]:min-w-0">
          {drawn.map((chart, index) => (
            <OverviewChart
              key={chart.id}
              chart={chart}
              samples={samples}
              note={note}
              className={spanOf(index, drawn.length)}
            />
          ))}
        </div>
      )}
      {quiet.length > 0 && (
        <p className="text-body leading-relaxed text-muted-foreground">
          <span className="font-medium text-foreground">
            {drawn.length > 0 ? "Nothing else has moved." : "Nothing has moved."}
          </span>{" "}
          {sentence(quiet.map((chart) => chart.label.toLowerCase()))}{" "}
          {quiet.length === 1 ? "has" : "have"} been at zero throughout the recorded window. Each is
          drawn when it moves.
        </p>
      )}
      <p className="text-hint text-muted-foreground">
        {held > 0
          ? `Recorded activity over ${duration(held)}, collected every ${every} seconds and kept for 7 days.`
          : "Activity is recorded in the background and kept for 7 days."}
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

/** "a", "a and b", "a, b and c". */
function sentence(words: string[]): string {
  const text =
    words.length <= 1
      ? (words[0] ?? "")
      : `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/**
 * How many columns a chart takes, so that every row of charts is full. Two
 * across, a chart left alone on the last row takes the row. Three across the
 * grid has six tracks: a chart takes two, and the charts of a last row that
 * would be short take three each — one left over joins the three before it
 * as two rows of two, since a single chart across the whole page is a line
 * six times wider than it is tall.
 */
function spanOf(index: number, count: number): string {
  const lone = count % 2 === 1 && index === count - 1 ? "lg:max-2xl:col-span-2" : ""
  if (count === 1) return cn(lone, "2xl:col-span-6")
  const over = count % 3
  const halves = over === 1 ? 4 : over === 2 ? 2 : 0
  return cn(lone, index >= count - halves ? "2xl:col-span-3" : "2xl:col-span-2")
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
  className,
}: {
  chart: ChartView
  samples: readonly Sample[]
  note: string
  className: string
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
      className={className}
    />
  )
})
