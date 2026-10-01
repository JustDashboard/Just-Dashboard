"use client"

import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { TileTrend } from "@/components/metrics/sparkline"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import type { Part, Reading } from "@/components/database/home/readings"

/**
 * A home's six readings as one run of tiles.
 *
 * `StatGrid` stops at five across, and a database's headline figures are six:
 * what is on it, what it is doing, how the cache is doing, what it weighs,
 * what it holds and when it was last backed up. Five and a sixth alone on a
 * row of its own is the one arrangement that reads as a mistake, so this is
 * `StatGrid` at three — two and three across are both counts six divides
 * into — taken to six across where the window has the width for six figures
 * at 24px.
 *
 * It is `dense`: two across on a phone as well. One to a row, six tiles were
 * seven hundred pixels before the first thing the page is about, and a
 * database's figures are short enough to share a phone's width.
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
      dense
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
 * which is a reading; one whose poll has failed since is drawn in the quiet
 * ink. The figure rises once, when it lands (§11).
 */
export function ReadingTile({ reading, trends }: { reading: Reading; trends: boolean }) {
  const settled = !reading.pending
  const read = settled && reading.value !== undefined
  return (
    <StatTile
      label={reading.label}
      value={
        <span
          // A figure that lands rises once (§11) — after its bone, and after
          // the dash of a read that failed and was tried again.
          key={!settled ? "bone" : reading.value === undefined ? "dash" : "figure"}
          className={cn(
            "inline-block max-w-full truncate align-bottom",
            settled && "animate-rise",
            reading.stale && "text-muted-foreground",
          )}
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
      trailing={read ? reading.trailing : undefined}
      meter={read ? reading.meter : undefined}
      tone={read && !reading.stale ? reading.tone : "default"}
      trend={
        trends &&
        reading.trend && (
          <TileTrend
            values={reading.trend.values}
            label={reading.trend.label}
            color={reading.trend.color}
            max={reading.trend.max}
          />
        )
      }
      hint={
        !settled ? undefined : read && !reading.stale && reading.parts ? (
          <Parts label={reading.label} parts={reading.parts} />
        ) : (
          reading.hint
        )
      }
    />
  )
}

/**
 * What a figure is made of: one bar in the series colours, where a tile with
 * a ceiling has its meter, and the parts' names under it as its legend.
 *
 * It rides in the tile's hint because that is where it belongs in the tile's
 * fixed order — figure, bar, one line — and the tile's own bar takes one
 * number. Spans throughout: the hint is a paragraph.
 */
function Parts({ label, parts }: { label: string; parts: Part[] }) {
  return (
    <>
      <span
        role="img"
        aria-label={`${label}, by its largest parts: ${parts
          .map((part) => `${part.label} ${part.value}`)
          .join(", ")}`}
        className="mt-1 mb-1.5 flex h-1 w-full gap-px overflow-hidden rounded-full bg-meter-track"
      >
        {parts.map((part) => (
          <span
            key={part.key}
            title={`${part.label} · ${part.value}`}
            className="h-full first:rounded-l-full"
            style={{ width: `${Math.max(part.share * 100, 1)}%`, background: part.color }}
          />
        ))}
      </span>
      {/* The legend measures itself: as many names as the tile has room to
          print whole, the largest parts first. The bar and its name for
          assistive technology carry all of them. */}
      <span className="@container flex min-w-0 items-center gap-x-2.5 overflow-hidden">
        {parts.map((part, index) => (
          <span
            key={part.key}
            title={`${part.label} · ${part.value}`}
            className={cn(
              "min-w-0 items-center gap-1",
              index < 2 && "inline-flex",
              index === 2 && "hidden @[10.5rem]:inline-flex",
              index > 2 && "hidden @[15rem]:inline-flex",
            )}
          >
            <span
              aria-hidden
              className="size-1.5 shrink-0 rounded-full"
              style={{ background: part.color }}
            />
            <span className="truncate">{part.label}</span>
          </span>
        ))}
      </span>
    </>
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
  // Chosen once, not hidden with a class (§12): the tile leaves no band for a
  // trend that is not there, and two across on a phone a 36px sparkline under
  // every other figure is what made six tiles a screen and a half.
  const trends = useMediaQuery("(min-width: 640px)")
  return (
    <ReadingGrid>
      {readings.map((reading) => (
        <ReadingTile key={reading.key} reading={reading} trends={trends} />
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
