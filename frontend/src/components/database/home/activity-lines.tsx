"use client"

import { memo, useMemo } from "react"
import { ArrowUpDown, Cpu, Layers, Lightning, Table, Users, type Icon } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { TileTrend } from "@/components/metrics/sparkline"
import { WireMark } from "@/components/deploy/wire"
import { HistoryRange } from "@/components/database/home/activity"
import { BlockLink, NotUpdating } from "@/components/database/home/blocks"
import { offeredViews, type ChartView } from "@/components/database/home/charts"
import { lineReading } from "@/components/database/home/lines"
import type { Sample } from "@/components/database/home/samples"
import { HISTORY_RANGES, SAMPLE_EVERY_MS, keptFor } from "@/components/database/home/use-samples"
import { useDatabase } from "@/components/database/shell/database-context"

/** What a view measures, drawn on its mark. One that is new takes the layers. */
const GLYPH: Record<string, Icon> = {
  sessions: Users,
  clients: Users,
  connections: Users,
  throughput: Lightning,
  commands: Lightning,
  operations: Lightning,
  queries: Lightning,
  rows: Table,
  cache: Layers,
  memory: Cpu,
  network: ArrowUpDown,
}

/**
 * A database's activity on its home: one line a view — sessions, throughput,
 * rows, the cache — each said in words on a mark in its measurement's hue,
 * with the recorded window at the line's end.
 *
 * They were four full charts, the same four Performance draws from the same
 * component, so the front page was two screens of plots before anything
 * else. A project's Overview reads its traffic and its container as lines and
 * leaves the charts to Runtime; a database's home does the same and leaves
 * them to Performance, which the block's own link and every line open: the
 * axes, the Min/Mean/Max/Now figures and the marks on the time axis are
 * there. The window is still chosen here, on the title's line, and each
 * line's shape is that window, so a week's sessions are still one press away
 * on an engine whose Performance draws only the last hour.
 *
 * Every line is one height whether or not it has a reading, so the page under
 * the block does not move when the recorder's first answer lands. "Now" is
 * the newest recorded reading, at most one interval old: the record is
 * polled, not a socket, so nothing here says Live (§11).
 */
export const ActivityLines = memo(function ActivityLines({
  views,
  samples,
  loading,
  error,
  hours,
  onHours,
  retentionHours,
}: {
  views: readonly ChartView[]
  /** The recorded readings of the chosen window, oldest first. */
  samples: readonly Sample[]
  hours: number
  onHours: (hours: number) => void
  retentionHours: number
  /** No sample has landed yet. */
  loading: boolean
  /** Why the latest poll failed. The samples before it are still read. */
  error?: Error
}) {
  const { engine, href } = useDatabase()
  const newest = samples[samples.length - 1]
  const offered = useMemo(() => offeredViews(views, newest), [views, newest])
  const readings = useMemo(
    () => offered.map((entry) => ({ view: entry, reading: lineReading(entry, samples) })),
    [offered, samples],
  )
  if (offered.length === 0) return null

  const performance = engine.has("performance") ? href("performance") : undefined
  const window = HISTORY_RANGES.find((range) => range.hours === hours)?.label ?? "hour"
  const waiting = loading
    ? "Reading the server…"
    : error && samples.length === 0
      ? `The server's statistics could not be read: ${error.message}`
      : "Rates need two recorded readings"
  return (
    // The block is found by its name, as the page's other blocks are.
    <Panel plain aria-label="Activity" className="min-w-0">
      <PanelHeader
        title="Activity"
        actions={
          <>
            {error && samples.length > 0 && <NotUpdating error={error} />}
            {/* How long the record goes back, on the line that picks the
                window; how it is collected is on the pointer. */}
            <span
              className="text-hint text-muted-foreground max-sm:hidden"
              title={`Collected every ${SAMPLE_EVERY_MS / 1000} seconds, including while this page is closed.`}
            >
              kept for {keptFor(retentionHours)}
            </span>
            {performance && <BlockLink href={performance}>Performance</BlockLink>}
            <HistoryRange hours={hours} onHours={onHours} retentionHours={retentionHours} />
          </>
        }
      />
      <PanelBody>
        <ReadingLines>
          {readings.map(({ view, reading }) => {
            const first = view.series[0]
            const Glyph = GLYPH[view.id] ?? Layers
            return (
              <ReadingLine
                key={view.id}
                href={performance}
                label={`${view.label} on Performance`}
                mark={
                  <WireMark size="md" hue={first.color}>
                    <Glyph aria-hidden />
                  </WireMark>
                }
                name={view.label}
                hint={reading.words ? reading.rest : waiting}
                trend={
                  <TileTrend
                    values={reading.values}
                    max={reading.max}
                    color={first.color}
                    label={`${view.label} over the last ${window.replace(/^1 /, "")}`}
                  />
                }
              >
                {reading.words ? (
                  <span className="numeric inline-block animate-rise">{reading.words}</span>
                ) : (
                  <NoReading>No reading yet</NoReading>
                )}
              </ReadingLine>
            )
          })}
        </ReadingLines>
      </PanelBody>
    </Panel>
  )
})
