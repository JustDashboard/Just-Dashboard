"use client"

import { memo, useMemo } from "react"
import { duration } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { Segments } from "@/components/deploy/settings/segments"
import { NotUpdating } from "@/components/database/home/blocks"
import { CHART_UNITS, offeredViews, type ChartView } from "@/components/database/home/charts"
import { chartRows, heldSeconds, type Sample } from "@/components/database/home/samples"
import { SAMPLE_EVERY_MS } from "@/components/database/home/use-samples"

/**
 * What the server has been doing while this page was open: one chart, and a
 * control choosing which group of its readings is drawn.
 *
 * It is drawn from the samples the page itself has taken — at most the last
 * five minutes — and says so under the plot. Nothing here is recorded: a
 * reader who opens the page after an incident sees a chart that starts now,
 * and the line under it is why that is not a fault.
 *
 * The control is one segmented choice of a few short words (§7), in the
 * panel's own header, so the block keeps one title and one hairline.
 */
export const Activity = memo(function Activity({
  id,
  views,
  samples,
  loading,
  error,
}: {
  id: number
  views: readonly ChartView[]
  samples: readonly Sample[]
  /** No sample has landed yet. */
  loading: boolean
  /** Why the latest poll failed. The samples before it are still drawn. */
  error?: Error
}) {
  const [chosen, setChosen] = useViewState(`databases.${id}.home.chart`, "")
  const offered = useMemo(() => offeredViews(views, samples[samples.length - 1]), [views, samples])
  const current = offered.find((entry) => entry.id === chosen) ?? offered[0]
  const rows = useMemo(
    () => (current ? chartRows(samples, current.sources) : []),
    [samples, current],
  )
  const options = useMemo(
    () => offered.map((entry) => ({ value: entry.id, label: entry.label })),
    [offered],
  )
  if (!current) return null

  const unit = CHART_UNITS[current.unit]
  const held = heldSeconds(samples)
  return (
    // The chart's panel has a title and no name; the page's other blocks are
    // regions named by theirs, and this one is found the same way.
    <div role="region" aria-label="Activity" className="min-w-0">
      <ChartPanel
        plain
        title="Activity"
        actions={
          <>
            {error && samples.length > 0 && <NotUpdating error={error} />}
            {options.length > 1 && (
              <Segments
                label="What the chart shows"
                value={current.id}
                options={options}
                onChange={setChosen}
              />
            )}
          </>
        }
        rows={rows}
        series={current.series}
        unit={unit.unit}
        format={unit.format}
        axisFormat={unit.axisFormat}
        domain={unit.domain}
        yTicks={unit.yTicks}
        height={200}
        showPeaks={false}
        note={
          loading
            ? "Reading the server…"
            : error && samples.length === 0
              ? `The server's statistics could not be read: ${error.message}`
              : `Readings arrive every ${SAMPLE_EVERY_MS / 1000} seconds; a rate is drawn from the second one on.`
        }
        footer={
          <p className="text-hint text-muted-foreground">
            {held > 0
              ? `Live readings over the last ${duration(held)}, one every ${SAMPLE_EVERY_MS / 1000} seconds since this page was opened. Nothing here is recorded.`
              : `Live readings, one every ${SAMPLE_EVERY_MS / 1000} seconds since this page was opened. Nothing here is recorded.`}
          </p>
        }
      />
    </div>
  )
})
