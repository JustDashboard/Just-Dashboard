"use client"

import { useLayoutEffect, useRef, useState } from "react"
import { plural } from "@/lib/format"
import type { GitHubContributionDay, GitHubContributions } from "@/lib/github-types"
import { cn } from "@/lib/utils"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import {
  CELL,
  CELL_GAP,
  WEEKDAY_GUTTER,
  busiestDay,
  dayTitle,
  dayWords,
  monthLabels,
  streaks,
  weeksThatFit,
} from "@/components/github/home/model"

const LEVEL_FILL = [
  "fill-contribution-0",
  "fill-contribution-1",
  "fill-contribution-2",
  "fill-contribution-3",
  "fill-contribution-4",
] as const

const LEVEL_BG = [
  "bg-contribution-0",
  "bg-contribution-1",
  "bg-contribution-2",
  "bg-contribution-3",
  "bg-contribution-4",
] as const

/** The month names' row over the cells. */
const MONTH_ROW = 16
/** The calendar's height, whatever it says: seven rows under the month names (§11). */
const HEIGHT = MONTH_ROW + 7 * CELL + 6 * CELL_GAP

/**
 * The account's year as GitHub's profile draws it: a week to a column,
 * Sunday at the top, each day a cell in one of five greens. It is the page's
 * picture (D2) — the one thing on it that says at a glance whether this
 * account is alive — so it heads the Overview, over the repositories.
 *
 * Above it, the year in words: how many contributions, and the streaks. Under
 * it, the day under the pointer (or the busiest day when nothing is), and the
 * key. The calendar draws as many of the latest weeks as the width holds, so
 * a phone shows its last few months rather than a calendar to scroll
 * sideways (D12); its height is fixed, so nothing under it moves when it
 * lands.
 */
export function ContributionCalendar({
  contributions,
  message,
  loading,
}: {
  contributions?: GitHubContributions
  /** Why GitHub did not share the calendar, when it did not. */
  message?: string
  loading?: boolean
}) {
  const box = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  const [hovered, setHovered] = useState<GitHubContributionDay | undefined>()

  useLayoutEffect(() => {
    const node = box.current
    if (!node) return
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width))
    observer.observe(node)
    setWidth(node.getBoundingClientRect().width)
    return () => observer.disconnect()
  }, [])

  const weeks = contributions?.weeks ?? []
  const shown = weeks.slice(weeks.length - weeksThatFit(width, weeks.length))
  const months = monthLabels(shown)
  const { longest, current } = streaks(contributions)
  const busiest = busiestDay(contributions)
  const svgWidth = WEEKDAY_GUTTER + shown.length * (CELL + CELL_GAP) - CELL_GAP

  return (
    <section
      aria-label="Contributions"
      data-slot="github-contributions"
      className="flex min-w-0 flex-col gap-3"
    >
      <div className="flex min-h-6 min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2 className="flex min-w-0 items-baseline gap-1.5 text-body text-muted-foreground">
          {contributions ? (
            <>
              <NumberTicker
                value={contributions.total}
                className="text-lg leading-6 font-semibold tracking-tight text-foreground"
              />
              <span>{contributions.total === 1 ? "contribution" : "contributions"} this year</span>
            </>
          ) : loading ? (
            <Skeleton className="h-6 w-56" />
          ) : (
            <span className="text-foreground">Contributions</span>
          )}
        </h2>
        {contributions && contributions.total > 0 && (
          <p className="numeric flex items-center gap-3 text-hint text-muted-foreground">
            <span>
              Longest streak{" "}
              <span className="font-medium text-foreground">{plural(longest, "day")}</span>
            </span>
            <span>
              Current <span className="font-medium text-foreground">{plural(current, "day")}</span>
            </span>
          </p>
        )}
      </div>

      <div ref={box} className="min-w-0" style={{ height: HEIGHT }}>
        {contributions && width > 0 ? (
          <svg
            role="img"
            aria-label={`${plural(contributions.total, "contribution")} in the last year`}
            width={svgWidth}
            height={HEIGHT}
            className="block max-w-full overflow-visible"
            onPointerLeave={() => setHovered(undefined)}
          >
            {months.map((month) => (
              <text
                key={`${month.column}-${month.label}`}
                x={WEEKDAY_GUTTER + month.column * (CELL + CELL_GAP)}
                y={11}
                className="fill-muted-foreground text-micro"
              >
                {month.label}
              </text>
            ))}
            {[
              [1, "Mon"],
              [3, "Wed"],
              [5, "Fri"],
            ].map(([row, label]) => (
              <text
                key={label}
                x={0}
                y={MONTH_ROW + (row as number) * (CELL + CELL_GAP) + CELL - 2}
                className="fill-muted-foreground text-micro"
              >
                {label}
              </text>
            ))}
            {shown.map((week, column) => (
              <g
                key={week.days[0]?.date ?? column}
                transform={`translate(${WEEKDAY_GUTTER + column * (CELL + CELL_GAP)} ${MONTH_ROW})`}
                className="animate-in duration-500 fade-in-0 fill-mode-backwards"
                style={{ animationDelay: `${Math.min(column, 52) * 8}ms` }}
              >
                {week.days.map((day) => (
                  <rect
                    key={day.date}
                    data-level={day.level}
                    y={day.weekday * (CELL + CELL_GAP)}
                    width={CELL}
                    height={CELL}
                    rx={2.5}
                    className={cn(
                      LEVEL_FILL[day.level] ?? LEVEL_FILL[0],
                      "stroke-hairline transition-opacity",
                      hovered && hovered.date !== day.date && "opacity-60",
                    )}
                    strokeWidth={day.level === 0 ? 0 : 0.5}
                    onPointerEnter={() => setHovered(day)}
                  >
                    <title>{dayTitle(day)}</title>
                  </rect>
                ))}
              </g>
            ))}
          </svg>
        ) : loading || (contributions && width === 0) ? (
          <Skeleton className="h-full w-full rounded-lg" />
        ) : (
          <div className="flex h-full items-center justify-center rounded-lg bg-surface-sunken px-4 text-center text-hint text-muted-foreground">
            {message ?? "GitHub did not share the contribution calendar."}
          </div>
        )}
      </div>

      <div className="flex h-4 min-w-0 items-center justify-between gap-4 text-hint text-muted-foreground">
        <span
          className="numeric min-w-0 truncate"
          aria-live="polite"
          data-slot="github-contributions-day"
        >
          {hovered
            ? dayTitle(hovered)
            : busiest
              ? `Busiest day ${dayWords(busiest.date)}, ${plural(busiest.count, "contribution")}`
              : ""}
        </span>
        {contributions && (
          <span aria-hidden className="flex shrink-0 items-center gap-1">
            Less
            {LEVEL_BG.map((fill) => (
              <span key={fill} className={cn("size-2.5 rounded-xs", fill)} />
            ))}
            More
          </span>
        )}
      </div>
    </section>
  )
}
