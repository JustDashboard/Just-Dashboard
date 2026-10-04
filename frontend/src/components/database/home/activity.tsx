"use client"

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

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
import { HISTORY_RANGES, SAMPLE_EVERY_MS } from "@/components/database/home/use-samples"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { tabClasses } from "@/components/tabs"
import { duration } from "@/lib/format"
import type { MetricEvent } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { cn } from "@/lib/utils"
import { memo, useMemo } from "react"

/** Recorded engine activity, available before this page is opened. */
export const Activity = memo(function Activity({
  id,
  views,
  samples,
  loading,
  error,
  events,
  hours,
  onHours,
}: {
  id: number
  hours: number
  onHours: (hours: number) => void
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
  // and a new array of the same ticks on every refresh would redraw it.
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
        className={cn(
          "[&>[data-slot=panel-header]>div:last-child]:max-w-full",
          several &&
            "[&>[data-slot=panel-header]>div:first-child]:pb-2.5 [&>[data-slot=panel-header][data-slot]]:items-end [&>[data-slot=panel-header][data-slot]]:pb-0",
        )}
        title="Activity"
        actions={
          <>
            <div className="pb-2.5">
              <Select value={String(hours)} onValueChange={(value) => onHours(Number(value))}>
                <SelectTrigger aria-label="Activity history range" size="sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {HISTORY_RANGES.map((range) => (
                    <SelectItem key={range.hours} value={String(range.hours)}>
                      {range.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {error && samples.length > 0 && (
              <span className={several ? "flex pb-2.5" : "flex"}>
                <NotUpdating error={error} />
              </span>
            )}
            {several && (
              <div
                role="tablist"
                aria-label="What the chart shows"
                className="-mb-px flex max-w-full min-w-0 overflow-x-auto"
              >
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
              : `The recorder is collecting activity; rates need two saved readings.`
        }
        footer={
          <p className="text-hint text-muted-foreground">
            {held > 0
              ? `Recorded activity over ${duration(held)}. Collected every ${SAMPLE_EVERY_MS / 1000} seconds, including while this page is closed; kept for 7 days.`
              : "Activity is recorded in the background every 30 seconds and kept for 7 days."}
          </p>
        }
      />
    </div>
  )
})
