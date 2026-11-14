"use client"

import type { Icon } from "@/components/icons"
import { readingShown } from "@/components/logs/logs-model"
import type { LensReadingTile } from "@/components/logs/lens-readings"
import type { ReadingsWindow } from "@/components/logs/logs-model"
import { WireMark } from "@/components/deploy/wire"
import { TileTrend } from "@/components/metrics/sparkline"
import { NoReading, ReadingLine } from "@/components/reading-line"
import { NumberTicker } from "@/components/ui/number-ticker"

/** A reading's glyph and its own hue (§10): one colour for the mark and the line. */
export type ReadingLook = { icon: Icon; hue: string }

/**
 * One of a log lens's readings as a `ReadingLine`: its glyph on its hue, its
 * name, the count over the lens's window as words, and the window's shape at
 * the line's end in the same hue. It is what a `ReadingTile` in a page's
 * `StatGrid` becomes on a page that reads in sentences.
 *
 * A press narrows the page's log to the lines the figure counts, as the tile
 * did (`ReadingLine`'s own `onPress`). Every state is the line's one height,
 * so a figure landing moves nothing.
 */
export function LensReadingLine({
  tile,
  window,
  look,
  onPick,
}: {
  tile: LensReadingTile
  window: ReadingsWindow
  look: ReadingLook
  onPick?: () => void
}) {
  const { reading, figure } = tile
  const { value, decimals } = readingShown(reading, figure, window)
  const Glyph = look.icon
  const hint =
    figure && value === 0 && reading.requires
      ? `Logged only with ${reading.requires} set`
      : reading.hint
  return (
    <ReadingLine
      onPress={onPick}
      label={`Show the lines behind ${reading.label.toLowerCase()}`}
      mark={
        <WireMark size="md" hue={look.hue}>
          <Glyph aria-hidden />
        </WireMark>
      }
      name={reading.label}
      hint={hint}
      trend={
        figure?.series && (
          <TileTrend
            values={figure.series}
            color={look.hue}
            label={`${reading.label} over ${window.long}`}
          />
        )
      }
    >
      {figure ? (
        <span className="numeric">
          <NumberTicker value={value} decimalPlaces={decimals} />
          {figure.capped && "+"}{" "}
          {reading.figure === "per_minute" ? "a minute" : `in ${window.long}`}
        </span>
      ) : (
        <NoReading>No reading yet</NoReading>
      )}
    </ReadingLine>
  )
}
