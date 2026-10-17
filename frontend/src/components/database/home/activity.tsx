"use client"

import { NotUpdating } from "@/components/database/home/blocks"
import {
  CHART_UNITS,
  drawnRows,
  highest,
  offeredViews,
  roundTicks,
  type ChartView,
} from "@/components/database/home/charts"
import { chartRows, gauge, heldSeconds, type Sample } from "@/components/database/home/samples"
import { HISTORY_RANGES, SAMPLE_EVERY_MS } from "@/components/database/home/use-samples"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { ChartReading } from "@/components/metrics/chart-reading"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { duration } from "@/lib/format"
import type { MetricEvent } from "@/lib/types"
import { cn } from "@/lib/utils"
import { memo, useMemo } from "react"

/**
 * Recorded engine activity, available before this page is opened.
 *
 * Every view the engine can fill is a chart of its own, two to a row from
 * `lg`, each headed by its reading now — the way a project's Runtime draws a
 * container — so sessions, throughput, rows and the cache are read at once
 * rather than one behind a strip of tabs. Each line is in its measurement's
 * hue (§10), so the four tell apart before their titles are read. The range
 * is one row of presses over all of them.
 *
 * "Now" is the newest recorded reading, at most one interval old: the record
 * is polled, not a socket, so nothing here says Live (§11).
 */
export const Activity = memo(function Activity({
  views,
  samples,
  loading,
  error,
  events,
  hours,
  onHours,
}: {
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
  const newest = samples[samples.length - 1]
  const offered = useMemo(() => offeredViews(views, newest), [views, newest])
  if (offered.length === 0) return null

  const held = heldSeconds(samples)
  const note = loading
    ? "Reading the server…"
    : error && samples.length === 0
      ? `The server's statistics could not be read: ${error.message}`
      : "The recorder is collecting activity; rates need two saved readings."
  return (
    // The block is found by its name, as the page's other blocks are.
    <Panel plain aria-label="Activity" className="min-w-0">
      <PanelHeader
        title="Activity"
        actions={
          <>
            {error && samples.length > 0 && <NotUpdating error={error} />}
            <HistoryRange hours={hours} onHours={onHours} />
          </>
        }
      />
      <PanelBody className="space-y-4">
        <div className="grid gap-x-8 gap-y-6 lg:grid-cols-2 [&>*]:min-w-0">
          {offered.map((view, index) => (
            <ViewChart
              key={view.id}
              view={view}
              samples={samples}
              events={events}
              note={note}
              // An odd chart out takes the row rather than sit beside a hole.
              wide={offered.length % 2 === 1 && index === offered.length - 1}
            />
          ))}
        </div>
        <p className="text-hint text-muted-foreground">
          {held > 0
            ? `Recorded activity over ${duration(held)}. Collected every ${SAMPLE_EVERY_MS / 1000} seconds, including while this page is closed; kept for 7 days.`
            : "Activity is recorded in the background every 30 seconds and kept for 7 days."}
        </p>
      </PanelBody>
    </Panel>
  )
})

/** The recorded window the charts cover, as one row of presses. */
export function HistoryRange({
  hours,
  onHours,
  label = "Activity history range",
}: {
  hours: number
  onHours: (hours: number) => void
  /** What the window is of, for a screen reader. */
  label?: string
}) {
  return (
    <ToggleGroup
      type="single"
      value={String(hours)}
      // Radix reports "" when the pressed range is pressed again: it stays.
      onValueChange={(next) => next && onHours(Number(next))}
      variant="outline"
      size="sm"
      aria-label={label}
    >
      {HISTORY_RANGES.map((range) => (
        <ToggleGroupItem
          key={range.hours}
          value={String(range.hours)}
          aria-label={range.label}
          className="px-2.5 text-hint"
        >
          {range.short}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

/**
 * One view as its own chart, headed by the newest reading of its lines. The
 * Performance Overview draws its charts with it too, so a line reads the
 * same on both pages.
 */
export const ViewChart = memo(function ViewChart({
  view,
  samples,
  events,
  note,
  wide,
  className,
}: {
  view: ChartView
  samples: readonly Sample[]
  events?: MetricEvent[]
  note: string
  wide?: boolean
  className?: string
}) {
  const rows = useMemo(
    () => drawnRows(chartRows(samples, view.sources), view.series),
    [samples, view],
  )
  const unit = CHART_UNITS[view.unit]
  // The axis of a count or a rate is stepped here, on round figures. Held by
  // what the ticks are, not by the rows: the chart is memoised on its props,
  // and a new array of the same ticks on every refresh would redraw it.
  const steps = unit.stepped
    ? roundTicks(highest(rows, view.series), unit.stepped === "whole")
    : undefined
  const stepKey = steps?.join(" ") ?? ""
  const axis = useMemo(() => {
    if (!stepKey) return undefined
    const ticks = stepKey.split(" ").map(Number)
    return { ticks, domain: [0, ticks[ticks.length - 1]] as [number, number] }
  }, [stepKey])

  const ceiling = view.ceiling ? gauge(samples, view.ceiling) : undefined
  // The lines that have a reading: a line the engine does not count — a
  // statement total on PostgreSQL — is left out of the head rather than
  // drawn as a dash beside the one that does.
  const read = view.series
    .map((line) => ({ line, value: latest(rows, line.key) }))
    .filter((entry, index) => entry.value !== undefined || index === 0)
  const reading = (
    <span className="flex flex-wrap items-baseline justify-end gap-x-4 gap-y-1">
      {read.slice(0, 2).map(({ line, value }, index) => {
        const shown =
          value === undefined
            ? "—"
            : view.unit === "percent"
              ? `${value.toFixed(1)}%`
              : (unit.format?.(value) ?? String(value))
        const capped = index === 0 && ceiling !== undefined && ceiling > 0
        return (
          <ChartReading
            key={line.key}
            label={`${line.label}, newest reading`}
            color={line.color}
            value={shown}
            note={capped ? `of ${ceiling}` : line.label.toLowerCase()}
            meter={capped && value !== undefined ? (value / ceiling) * 100 : undefined}
          />
        )
      })}
    </span>
  )

  return (
    // Each chart is found by its name, as the blocks around it are.
    <div
      role="group"
      aria-label={view.label}
      className={cn("min-w-0", wide && "lg:col-span-2", className)}
    >
      <ChartPanel
        plain
        title={view.label}
        actions={reading}
        rows={rows}
        series={view.series}
        unit={unit.unit}
        format={unit.format}
        axisFormat={unit.axisFormat}
        domain={axis?.domain ?? unit.domain}
        yTicks={axis?.ticks ?? unit.yTicks}
        events={events}
        height={180}
        showPeaks={false}
        note={note}
      />
    </div>
  )
})

/** The newest value a line has in the rows on screen. */
function latest(rows: readonly Record<string, unknown>[], key: string): number | undefined {
  for (let index = rows.length - 1; index >= 0; index--) {
    const value = rows[index][key]
    if (typeof value === "number") return value
  }
  return undefined
}
