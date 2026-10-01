"use client"

import { cn } from "@/lib/utils"
import { TileTrend } from "@/components/metrics/sparkline"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import type { Reading } from "@/components/database/home/readings"

/**
 * A home's six readings as one run of tiles.
 *
 * `StatGrid` stops at five across, and a database's headline figures are six:
 * what is on it, what it is doing, how the cache is doing, what it weighs,
 * what it holds and when it was last backed up. Five and a sixth alone on a
 * row of its own is the one arrangement that reads as a mistake, so this is
 * `StatGrid` at three — one, two and three across are all counts six divides
 * into — taken to six across where the window has the width for six figures
 * at 24px.
 *
 * The two rules that redraw the hairlines for six name each cell twice. That
 * is only for the weight: the grid's own rules for two and three across are
 * as specific as a plain rule here would be, and which of two equal rules
 * wins is the stylesheet's order, which nothing in a class list controls.
 */
export function ReadingGrid({ className, ...props }: React.ComponentProps<typeof StatGrid>) {
  return (
    <StatGrid
      columns={3}
      className={cn(
        "2xl:grid-cols-6",
        "2xl:[&>*:nth-child(n):nth-child(n)]:border-t-0 2xl:[&>*:nth-child(n+2):nth-child(n)]:border-l",
        className,
      )}
      {...props}
    />
  )
}

/**
 * One reading as a tile. A figure not read yet holds its place as a bone; one
 * that could not be read is a dash with the reason under it — never a zero,
 * which is a reading. The figure rises once, when it lands (§11).
 */
export function ReadingTile({ reading }: { reading: Reading }) {
  const settled = !reading.pending
  return (
    <StatTile
      label={reading.label}
      value={
        <span
          // A figure that lands rises once (§11) — after its bone, and after
          // the dash of a read that failed and was tried again.
          key={!settled ? "bone" : reading.value === undefined ? "dash" : "figure"}
          className={cn("inline-block max-w-full truncate align-bottom", settled && "animate-rise")}
        >
          {!settled ? (
            <Skeleton className="my-1.5 h-5 w-20" />
          ) : reading.value === undefined ? (
            "—"
          ) : ticks(reading) ? (
            <NumberTicker value={reading.count ?? 0} />
          ) : (
            reading.value
          )}
        </span>
      }
      trailing={settled && reading.value !== undefined ? reading.trailing : undefined}
      meter={settled && reading.value !== undefined ? reading.meter : undefined}
      tone={settled && reading.value !== undefined ? reading.tone : "default"}
      trend={
        reading.trend && (
          <TileTrend
            values={reading.trend.values}
            label={reading.trend.label}
            color={reading.trend.color}
            max={reading.trend.max}
          />
        )
      }
      hint={settled ? reading.hint : undefined}
    />
  )
}

/**
 * Whether a figure is counted up to. Only a whole number short enough to be
 * written without a separator: past that the figure is written compactly
 * ("28.6K"), which is not a number a ticker can pass through.
 */
function ticks(reading: Reading): boolean {
  const count = reading.count
  return count !== undefined && Number.isInteger(count) && count >= 0 && count < 1000
}

export function ReadingTiles({ readings }: { readings: Reading[] }) {
  return (
    <ReadingGrid>
      {readings.map((reading) => (
        <ReadingTile key={reading.key} reading={reading} />
      ))}
    </ReadingGrid>
  )
}

/** The run before the first sample: six tiles' worth of bones, so the page does not jump. */
export function ReadingTilesSkeleton({ labels }: { labels: string[] }) {
  return (
    <ReadingTiles
      readings={labels.map((label) => ({ key: label, label, value: undefined, pending: true }))}
    />
  )
}
