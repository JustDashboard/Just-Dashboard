"use client"

import { memo, useMemo } from "react"
import { duration } from "@/lib/format"
import type { MetricEvent } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { tabClasses } from "@/components/tabs"
import { NotUpdating } from "@/components/database/home/blocks"
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

/**
 * What the server has been doing while this page was open: one chart, and a
 * strip of views choosing which group of its readings is drawn.
 *
 * It is drawn from the samples the page itself has taken — at most the last
 * five minutes — and says so under the plot. Nothing here is recorded: a
 * reader who opens the page after an incident sees a chart that starts now,
 * and the line under it is why that is not a fault.
 *
 * The strip is the underlined one every switch between views of a page wears
 * (§8), set in the chart's own header so the block keeps one title and one
 * rule: the header gives up its bottom padding and the current view's
 * underline lies on the hairline, the way a strip's does anywhere else.
 *
 * A dump taken or a restart while the page is open is marked on the time
 * axis, so a step in a line is read next to what caused it.
 */
export const Activity = memo(function Activity({
  id,
  views,
  samples,
  loading,
  error,
  events,
}: {
  id: number
  views: readonly ChartView[]
  samples: readonly Sample[]
  /** No sample has landed yet. */
  loading: boolean
  /** Why the latest poll failed. The samples before it are still drawn. */
  error?: Error
  /** What happened to the server in the window, set on the time axis. */
  events?: MetricEvent[]
}) {
  const [chosen, setChosen] = useViewState(`databases.${id}.home.chart`, "")
  const offered = useMemo(() => offeredViews(views, samples[samples.length - 1]), [views, samples])
  const current = offered.find((entry) => entry.id === chosen) ?? offered[0]
  const rows = useMemo(
    () => (current ? drawnRows(chartRows(samples, current.sources), current.series) : []),
    [samples, current],
  )
  const unit = current ? CHART_UNITS[current.unit] : undefined
  // The axis of a count or a rate is stepped here, on round figures. Held by
  // what the ticks are, not by the rows: the chart is memoised on its props,
  // and a new array of the same ticks every five seconds would redraw it.
  const stepped = unit?.stepped
  const steps =
    stepped && current ? roundTicks(highest(rows, current.series), stepped === "whole") : undefined
  const stepKey = steps?.join(" ") ?? ""
  const axis = useMemo(() => {
    if (!stepKey) return undefined
    const ticks = stepKey.split(" ").map(Number)
    return { ticks, domain: [0, ticks[ticks.length - 1]] as [number, number] }
  }, [stepKey])
  if (!current || !unit) return null

  const held = heldSeconds(samples)
  const several = offered.length > 1
  return (
    // The chart's panel has a title and no name; the page's other blocks are
    // regions named by theirs, and this one is found the same way.
    <div role="region" aria-label="Activity" className="min-w-0">
      <ChartPanel
        plain
        // The selector names the header twice only to outweigh the panel's own
        // padding rule, which is as specific as a plain one here would be.
        className={
          several
            ? "[&>[data-slot=panel-header]>div:first-child]:pb-2.5 [&>[data-slot=panel-header][data-slot]]:items-end [&>[data-slot=panel-header][data-slot]]:pb-0"
            : undefined
        }
        title="Activity"
        actions={
          <>
            {error && samples.length > 0 && (
              <span className={several ? "flex pb-2.5" : "flex"}>
                <NotUpdating error={error} />
              </span>
            )}
            {several && (
              <div role="tablist" aria-label="What the chart shows" className="-mb-px flex">
                {offered.map((entry) => (
                  <button
                    key={entry.id}
                    type="button"
                    role="tab"
                    aria-selected={entry.id === current.id}
                    onClick={() => setChosen(entry.id)}
                    className={tabClasses(entry.id === current.id, "h-9 max-sm:px-2")}
                  >
                    {entry.label}
                  </button>
                ))}
              </div>
            )}
          </>
        }
        rows={rows}
        series={current.series}
        unit={unit.unit}
        format={unit.format}
        axisFormat={unit.axisFormat}
        domain={axis?.domain ?? unit.domain}
        yTicks={axis?.ticks ?? unit.yTicks}
        events={events}
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
