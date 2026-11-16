"use client"

import { ChartActivity, Warning, type Icon } from "@/components/icons"
import { readingLook, readingShown, type ReadingsWindow } from "@/components/logs/logs-model"
import type { LensReadingTile } from "@/components/logs/lens-readings"
import { TileTrend } from "@/components/sparkline"
import { NoReading, ReadingLine, ReadingMark } from "@/components/reading-line"
import { NumberTicker } from "@/components/ui/number-ticker"

/** A reading's glyph and its own hue (§10): one colour for the mark and the line. */
export type ReadingLook = { icon: Icon; hue: string }

/**
 * One of a log lens's readings as a `ReadingLine`: its glyph on its hue, its
 * name, the count over the lens's window as words, and the window's shape at
 * the line's end in the same hue.
 *
 * It is the one way a lens reading is drawn. A container's and a stack's
 * logs drew the same readings as a row of `StatTile`s (`ReadingTile`, the
 * shape D4 retires) and Security drew them as these lines; the security
 * panels' copy now re-exports this one.
 *
 * A page that gives each reading a `look` (Security's glyph and hue per
 * reading) passes it; without one a reading is drawn as a series on the
 * first chart hue, in its tone once it is above zero, so the line that needs
 * looking at is the amber or red one.
 *
 * A press narrows the page's log to the lines the figure counts
 * (`ReadingLine`'s own `onPress`); pressed again it lets them go. Every
 * state is the line's one height, so a figure landing moves nothing.
 */
export function LensReadingLine({
  tile,
  window,
  look,
  pressed,
  onPick,
}: {
  tile: LensReadingTile
  window: ReadingsWindow
  look?: ReadingLook
  /** The log is narrowed to this reading's lines now. */
  pressed?: boolean
  onPick?: () => void
}) {
  const { reading, figure } = tile
  const { value, decimals } = readingShown(reading, figure, window)
  const drawn = readingLook(reading, figure ? value : 0)
  const lit = drawn.tone
  const hue = look?.hue ?? drawn.hue
  const Glyph = look?.icon ?? (lit === "danger" || lit === "warning" ? Warning : ChartActivity)
  const hint =
    figure && value === 0 && reading.requires
      ? `Logged only with ${reading.requires} set`
      : reading.hint
  const name = reading.label.toLowerCase()
  return (
    <ReadingLine
      onPress={onPick}
      label={
        pressed ? `Show every line again, not only the ${name}` : `Show the lines behind ${name}`
      }
      className={pressed ? "bg-accent" : undefined}
      mark={<ReadingMark glyph={Glyph} hue={hue} tone={look ? undefined : lit} />}
      name={reading.label}
      hint={hint}
      trend={
        figure?.series && (
          <TileTrend values={figure.series} color={hue} label={`${reading.label} over ${window.long}`} />
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
