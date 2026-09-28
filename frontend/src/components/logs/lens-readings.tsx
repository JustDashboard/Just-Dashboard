"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import type { LogSearchResult } from "@/lib/types"
import type { LogFilterState } from "@/components/logs/types"
import { fieldsOf, resolveRange } from "@/lib/log-filter"
import {
  readingFigure,
  readingFilter,
  readingSearches,
  type ReadingFigure,
} from "@/lib/log-insights"
import type { LensReading, LogLens } from "@/lib/log-lenses"
import {
  READINGS_WINDOWS,
  readingShown,
  readingsWindowOf,
  sameQuestion,
  type ReadingsWindow,
} from "@/components/logs/logs-model"
import type { LogTimeRange } from "@/components/logs/types"
import { usePoll } from "@/hooks/use-poll"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { TileTrend } from "@/components/metrics/sparkline"
import { StatButton, StatGrid, StatTile } from "@/components/stat-tile"

const NO_READINGS: LensReading[] = []

/** How often the figures are read again: a minute, as the Overview's tiles are. */
const REFRESH = 60_000

/** The narrowest a reading's tile reads whole: its name, its figure and its line. */
const TILE_MIN = 200

export type LensReadingTile = {
  reading: LensReading
  /** Absent until the search that serves it has answered, or when it failed. */
  figure?: ReadingFigure
}

export type LensReadingsState = {
  tiles: LensReadingTile[]
  window: ReadingsWindow
  loading: boolean
}

/**
 * A lens's readings for one source: what its log adds up to over the
 * lens's window, independent of the filter on screen — a figure that moved
 * with every chip would stop being a reading of the source and become an
 * echo of the question.
 *
 * The readings on one key share a search, those on levels share another,
 * and a distinct count asks on its own (`lib/log-insights.ts`), so a lens's
 * five tiles cost two or three scans, read again each minute. A page with a
 * `StatGrid` of its own takes the tiles from here and draws them into it
 * with `ReadingTile`; `LensReadings` is the grid for a page without one.
 * A page that draws only some of them names those (`only`), and the others
 * are not searched for: each is a scan a minute for a figure nobody sees.
 *
 * Drawn among figures of a window the reader picked — Insights' — they are
 * read over that window (`range`) rather than the lens's own, so one view
 * does not hold two answers to "how many errors" from two stretches of time.
 */
export function useLensReadings(
  sourceId: string,
  lens: LogLens | undefined,
  options: {
    forcedLens?: string
    enabled?: boolean
    only?: readonly string[]
    range?: { range: LogTimeRange; since: string; until: string }
  } = {},
): LensReadingsState {
  const onlyKey = options.only?.join(",")
  const readings = useMemo(() => {
    const all = lens?.readings ?? NO_READINGS
    if (onlyKey === undefined) return all
    const wanted = onlyKey.split(",")
    return all.filter((reading) => wanted.includes(reading.id))
  }, [lens, onlyKey])
  const rangeId = options.range?.range
  const rangeSince = options.range?.since ?? ""
  const rangeUntil = options.range?.until ?? ""
  const picked = useMemo(
    () => (rangeId ? readingsWindowOf(rangeId, rangeSince, rangeUntil) : undefined),
    [rangeId, rangeSince, rangeUntil],
  )
  const window = picked ?? READINGS_WINDOWS[lens?.readingsWindow ?? "1h"]
  const searches = useMemo(() => readingSearches(readings), [readings])
  const forced = options.forcedLens || undefined

  const poll = usePoll(
    async (signal) => {
      const bounds =
        picked && rangeId
          ? resolveRange(rangeId, rangeSince, rangeUntil)
          : { since: new Date(Date.now() - window.minutes * 60_000).toISOString() }
      // Settled one by one: a distinct count the server refused should not
      // take the four counts beside it down with it.
      const answers = await Promise.allSettled(
        searches.map((search) =>
          get<LogSearchResult>(
            "/logs/search",
            { source: sourceId, lens: forced, ...bounds, ...search.params },
            signal,
          ),
        ),
      )
      const figures = new Map<string, ReadingFigure>()
      answers.forEach((answer, i) => {
        if (answer.status !== "fulfilled") return
        for (const read of searches[i].reads)
          figures.set(read.id, readingFigure(read, answer.value))
      })
      return figures
    },
    REFRESH,
    [sourceId, lens?.id, forced, onlyKey, rangeId, rangeSince, rangeUntil],
    { enabled: (options.enabled ?? true) && Boolean(sourceId) && searches.length > 0 },
  )

  const tiles = useMemo(
    () => readings.map((reading) => ({ reading, figure: poll.data?.get(reading.id) })),
    [readings, poll.data],
  )
  return { tiles, window, loading: poll.loading && !poll.data }
}

/**
 * The readings as the one row of figures a page without its own carries.
 * A press narrows the pane to the lines a figure counts, and pressing the
 * figure whose filter is on screen lets it go — the quick views' rule.
 */
export function LensReadings({
  readings,
  filter,
  onFilterChange,
  className,
}: {
  readings: LensReadingsState
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
  className?: string
}) {
  const { tiles, window } = readings
  const [ref, width] = useColumnWidth()
  if (tiles.length === 0) return null
  // As many to a row as the column they stand in holds, in rows as even as
  // they can be: five readings beside the logs page's rail are three and two,
  // where five across cut "Deadlocks & lock waits" to its first word.
  const fit = width > 0 ? Math.max(2, Math.floor(width / TILE_MIN)) : 5
  const rows = Math.ceil(tiles.length / Math.min(fit, 5))
  const columns = Math.min(Math.max(Math.ceil(tiles.length / rows), 2), 5) as 2 | 3 | 4 | 5
  return (
    <StatGrid ref={ref} columns={columns} dense className={className}>
      {tiles.map((tile) => {
        const pressed = readingPressed(tile.reading, filter)
        return (
          <ReadingTile
            key={tile.reading.id}
            tile={tile}
            window={window}
            pressed={pressed}
            onPick={() =>
              onFilterChange(
                pressed
                  ? { ...filter, fields: {}, levels: [] }
                  : readingFilter(filter, tile.reading),
              )
            }
          />
        )
      })}
    </StatGrid>
  )
}

export function readingPressed(reading: LensReading, filter: LogFilterState) {
  return sameQuestion({ fields: fieldsOf(filter), levels: filter.levels }, reading)
}

/**
 * One reading as a tile: the figure over the window — a rate for a
 * per-minute reading, a count of addresses for a distinct one — in its tone
 * only once it is above zero, and the window's shape under it in a series
 * colour (§10: a danger reading's line is `--chart-3`, never the status red).
 * A reading the server writes only with a setting on says so when it is zero,
 * rather than reading as good news.
 */
export function ReadingTile({
  tile,
  window,
  pressed,
  onPick,
}: {
  tile: LensReadingTile
  window: ReadingsWindow
  pressed?: boolean
  onPick?: () => void
}) {
  const { reading, figure } = tile
  const words = window
  const perMinute = reading.figure === "per_minute"
  const { value, text: shown } = readingShown(reading, figure, window)
  const hint =
    figure && value === 0 && reading.requires
      ? `Logged only with ${reading.requires} set`
      : reading.hint
  const content = (
    <StatTile
      label={reading.label}
      value={shown}
      trailing={figure ? (perMinute ? "per minute" : words.short) : undefined}
      tone={figure && value > 0 ? (reading.tone ?? "default") : "default"}
      trend={
        figure?.series ? (
          <TileTrend
            values={figure.series}
            color={reading.tone === "danger" ? "var(--chart-3)" : "var(--chart-1)"}
            label={`${reading.label} over ${words.long}`}
          />
        ) : undefined
      }
      hint={hint}
      className={onPick ? "h-full transition-colors group-hover:bg-row-hover" : undefined}
    />
  )
  if (!onPick) return content
  return (
    <StatButton
      label={
        pressed
          ? `Show every line again, not only the ${reading.label.toLowerCase()}`
          : `Show the lines behind ${reading.label.toLowerCase()}`
      }
      pressed={pressed}
      onClick={onPick}
    >
      {content}
    </StatButton>
  )
}
