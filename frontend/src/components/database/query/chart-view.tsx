"use client"

import { useMemo } from "react"
import { LineChart } from "@/components/icons"
import { BarList } from "@/components/bar-list"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { Series } from "@/components/metrics/metric-chart"
import { EmptyState } from "@/components/state"
import { chartOf } from "@/components/database/query/chart"
import type { QueryResult } from "@/components/database/query/types"

const COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
]

const number = (value: number) =>
  value.toLocaleString("en-US", { maximumFractionDigits: Math.abs(value) >= 100 ? 0 : 2 })

/**
 * A result drawn, when it is one that can be: instants beside numbers as
 * lines over time through the dashboard's own chart, a name beside a number
 * as a ranking. Anything else says what a chartable result is rather than
 * drawing a picture of a table.
 */
export function ChartView({ result }: { result: QueryResult | undefined }) {
  const read = useMemo(() => chartOf(result), [result])
  const series = useMemo<Series[]>(
    () =>
      read.kind === "series"
        ? read.series.map((entry, index) => ({
            key: entry.key,
            label: entry.label,
            color: COLORS[index % COLORS.length],
            kind: "line",
          }))
        : [],
    [read],
  )

  if (read.kind === "none") {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-6">
        <EmptyState
          className="border-0 py-6"
          icon={LineChart}
          title="Nothing to draw"
          description={`${read.reason} A column of dates with a count or a sum beside it draws as lines over time; a name with a number draws as a ranking.`}
        />
      </div>
    )
  }
  if (read.kind === "series") {
    return (
      <div className="min-h-0 flex-1 overflow-auto p-4">
        <ChartPanel
          plain
          title={`${read.series.map((entry) => entry.label).join(", ")} by ${read.time}`}
          rows={read.rows}
          series={series}
          format={number}
          height={220}
          showPeaks={false}
        />
      </div>
    )
  }
  const most = Math.max(...read.items.map((item) => Math.abs(item.value)), 0)
  return (
    <div className="min-h-0 flex-1 overflow-auto p-4">
      <p className="eyebrow pb-2">
        {read.value} by {read.label}
      </p>
      <BarList
        items={read.items.map((item, index) => ({
          key: String(index),
          label: item.label,
          value: number(item.value),
          share: most > 0 ? Math.abs(item.value) / most : 0,
        }))}
      />
      {read.more > 0 && (
        <p className="pt-2 text-hint text-muted-foreground">
          And {read.more.toLocaleString("en-US")} more rows not drawn.
        </p>
      )}
    </div>
  )
}
