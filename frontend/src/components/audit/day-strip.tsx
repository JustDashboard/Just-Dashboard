"use client"

import { useState } from "react"
import { cn } from "@/lib/utils"
import { DAY_HOURS, hourTone, hourWords, type DayReading } from "@/components/audit/day"

/** An hour's fill, in the availability ribbon's hues: an hour nothing changed in is the track. */
const HOUR_FILL = {
  quiet: "bg-meter-track",
  ok: "bg-success/80",
  dented: "bg-warning",
  failed: "bg-destructive",
} as const

const hourName = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" })

/** "14:00 to 15:00", and "since 14:00" for the hour that is still running. */
function hourSpan(from: number, running: boolean) {
  const start = hourName.format(new Date(from))
  return running ? `since ${start}` : `${start} to ${hourName.format(new Date(from + 3_600_000))}`
}

/**
 * The last day as one strip: an hour to a stretch, oldest on the left, in the
 * colour of how its changes went, the way a project's Overview draws a month
 * of availability. Green is an hour whose every change went through, amber one
 * where some were refused, red one where all were, and the track an hour in
 * which nothing was changed.
 *
 * It replaced four tiles (changes, failed, people, sign-ins) whose figures are
 * the identity line's facts now. What it adds is *when*: a refused sign-in at
 * four in the morning and one at noon were the same "1" on the Failed tile.
 *
 * The line under it holds the parts of the product the day's changes touched,
 * busiest first, and pointing at an hour puts that hour there in their place,
 * so the block is one height whichever it says, and the height it has while
 * the day is still being read.
 */
export function DayStrip({
  day,
  error,
  onRetry,
}: {
  /** Undefined while the day is being read: the strip is drawn as its track. */
  day: DayReading | undefined
  error?: Error | null
  onRetry?: () => void
}) {
  const [pointed, setPointed] = useState<number>()
  const hours = day?.hours
  const shown = pointed !== undefined ? hours?.[pointed] : undefined
  return (
    <div className="min-w-0" onPointerLeave={() => setPointed(undefined)}>
      <ol
        aria-label="Changes over the last 24 hours, by the hour"
        data-testid="audit-day"
        aria-busy={!day && !error ? true : undefined}
        className="flex h-6 items-stretch gap-0.5"
      >
        {Array.from({ length: DAY_HOURS }, (_, index) => {
          const hour = hours?.[index]
          const tone = hour ? hourTone(hour) : "quiet"
          return (
            // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- a thumb's way to point at an hour, as onPointerEnter is a mouse's; the hour's words are its title as well
            <li
              key={index}
              title={
                hour
                  ? `${hourSpan(hour.from, index === DAY_HOURS - 1)}: ${hourWords(hour)}`
                  : undefined
              }
              data-tone={tone}
              onPointerEnter={() => hour && setPointed(index)}
              onClick={() => hour && setPointed(index)}
              className={cn(
                "min-w-0 flex-1 rounded-sm [transition:opacity_150ms_ease-out]",
                HOUR_FILL[tone],
                pointed !== undefined && pointed !== index && "opacity-40",
              )}
            />
          )
        })}
      </ol>
      <div className="mt-2 flex min-h-5 min-w-0 items-baseline justify-between gap-x-4 text-hint text-muted-foreground">
        {shown ? (
          <p key={shown.from} className="numeric min-w-0 animate-rise truncate">
            <span className="font-medium text-foreground">
              {hourSpan(shown.from, pointed === DAY_HOURS - 1)}
            </span>{" "}
            · {hourWords(shown)}
          </p>
        ) : error && !day ? (
          // The identity line over the strip says the day was not read; this
          // is the way to ask again, where the day's words would be.
          <p className="min-w-0 truncate">
            {onRetry ? (
              <button
                type="button"
                onClick={onRetry}
                className="rounded-sm font-medium text-foreground underline-offset-2 focus-ring hover:underline"
              >
                Read again
              </button>
            ) : (
              "\u00a0"
            )}
          </p>
        ) : !day ? (
          <p>&nbsp;</p>
        ) : day.sections.length === 0 ? (
          <p className="min-w-0 truncate">Nothing was changed</p>
        ) : (
          <dl className="numeric flex min-w-0 gap-x-4 overflow-hidden whitespace-nowrap">
            {day.sections.slice(0, 5).map((section) => (
              <div key={section.label} className="flex shrink-0 gap-1.5">
                <dt>{section.label}</dt>
                <dd className="font-medium text-foreground">{section.changes.toLocaleString()}</dd>
              </div>
            ))}
          </dl>
        )}
        <span className="numeric shrink-0 text-micro max-sm:hidden">24 hours ago to now</span>
      </div>
    </div>
  )
}
