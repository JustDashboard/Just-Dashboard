"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import type { LogSearchResult } from "@/lib/types"
import type { LogFilterState } from "@/components/logs/types"
import { fieldsOf } from "@/lib/log/filter"
import {
  readingFigure,
  readingFilter,
  readingSearches,
  type ReadingFigure,
} from "@/lib/log/insights"
import type { LensReading, LogLens } from "@/lib/log/lenses"
import { READINGS_WINDOWS, sameQuestion, type ReadingsWindow } from "@/components/logs/logs-model"
import { usePoll } from "@/hooks/use-poll"
import { ReadingLines } from "@/components/reading-line"
import { LensReadingLine } from "@/components/logs/lens-reading-line"

const NO_READINGS: LensReading[] = []

/** How often the figures are read again: a minute, as the Overview's tiles are. */
const REFRESH = 60_000

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
 * and a distinct count asks on its own (`lib/log/insights.ts`), so a lens's
 * five readings cost two or three scans, read again each minute. A page that
 * draws them among readings of its own takes them from here as
 * `LensReadingLine`s; `LensReadings` is the run of them for a page without.
 * A page that draws only some of them names those (`only`), and the others
 * are not searched for: each is a scan a minute for a figure nobody sees.
 */
export function useLensReadings(
  sourceId: string,
  lens: LogLens | undefined,
  options: {
    forcedLens?: string
    enabled?: boolean
    only?: readonly string[]
  } = {},
): LensReadingsState {
  const onlyKey = options.only?.join(",")
  const readings = useMemo(() => {
    const all = lens?.readings ?? NO_READINGS
    if (onlyKey === undefined) return all
    const wanted = onlyKey.split(",")
    return all.filter((reading) => wanted.includes(reading.id))
  }, [lens, onlyKey])
  const window = READINGS_WINDOWS[lens?.readingsWindow ?? "1h"]
  const searches = useMemo(() => readingSearches(readings), [readings])
  const forced = options.forcedLens || undefined

  const poll = usePoll(
    async (signal) => {
      const since = new Date(Date.now() - window.minutes * 60_000).toISOString()
      // Settled one by one: a distinct count the server refused should not
      // take the four counts beside it down with it.
      const answers = await Promise.allSettled(
        searches.map((search) =>
          get<LogSearchResult>(
            "/logs/search",
            { source: sourceId, lens: forced, since, ...search.params },
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
    [sourceId, lens?.id, forced, onlyKey],
    { enabled: (options.enabled ?? true) && Boolean(sourceId) && searches.length > 0 },
  )

  const tiles = useMemo(
    () => readings.map((reading) => ({ reading, figure: poll.data?.get(reading.id) })),
    [readings, poll.data],
  )
  return { tiles, window, loading: poll.loading && !poll.data }
}

/**
 * The readings as lines over the pane, for a page without readings of its
 * own: a container's logs, a stack's. A press narrows the pane to the lines a
 * reading counts, and pressing the one whose filter is on screen lets it go —
 * the quick views' rule. They were a row of `StatTile`s; a reading is said in
 * words (D4), as Security says the same readings.
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
  if (tiles.length === 0) return null
  return (
    <ReadingLines className={className}>
      {tiles.map((tile) => {
        const pressed = readingPressed(tile.reading, filter)
        return (
          <LensReadingLine
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
    </ReadingLines>
  )
}

export function readingPressed(reading: LensReading, filter: LogFilterState) {
  return sameQuestion({ fields: fieldsOf(filter), levels: filter.levels }, reading)
}
