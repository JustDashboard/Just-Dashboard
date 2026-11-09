"use client"

import { useMemo } from "react"
import { cn } from "@/lib/utils"
import {
  clockLabel,
  countdown,
  cronRunsBetween,
  longClock,
  RUN_CAP,
  wallClock,
} from "@/lib/schedule"
import { useNow } from "@/components/deploy/vocabulary"
import { describeCron } from "@/lib/cron"
import { KIND } from "@/components/procs/schedule-band"

const HOUR = 3600_000
const DAY = 24 * HOUR

/**
 * The parts every schedule's sheet is built from — a cron job's, a package
 * cron line's and a timer's — so the three read as one kind of thing:
 *
 *   1. what the schedule is, in a sentence, with the expression it was
 *      written as under it (`ScheduleSummary`);
 *   2. its figures as tiles;
 *   3. when it runs: the next few times as a list beside the week as a
 *      picture (`WhenItRuns`);
 *   4. what it runs, the details, and the runs it left in a log, each a
 *      `SheetSection` — a title and a hairline, on one 24px gutter.
 *
 * The sheets were three tiles, a week strip, a command and a log stacked at
 * 20px with nothing louder than another, and the schedule itself — the one
 * thing a reader opens a schedule for — was a run of mono under the title.
 */
export function SheetSection({
  title,
  actions,
  className,
  children,
}: {
  title: React.ReactNode
  actions?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <section className={cn("min-w-0 border-t border-hairline px-6 py-6", className)}>
      <div className="mb-4 flex min-h-6 items-center justify-between gap-3">
        <h3 className="text-body font-semibold">{title}</h3>
        {actions}
      </div>
      {children}
    </section>
  )
}

/** The schedule in a sentence, at the head of a sheet, with what it was written as under it. */
export function ScheduleSummary({
  sentence,
  expression,
  facts,
  notice,
}: {
  sentence: React.ReactNode
  expression?: React.ReactNode
  /** Short facts after the expression: who it runs as, the clock it is read on. */
  facts?: React.ReactNode[]
  notice?: React.ReactNode
}) {
  const shown = (facts ?? []).filter(Boolean)
  return (
    <section className="space-y-4 px-6 pt-6 pb-6">
      {notice}
      <div className="space-y-1.5">
        <p className="eyebrow">Schedule</p>
        <p className="text-base leading-snug font-semibold first-letter:uppercase">{sentence}</p>
        {(expression || shown.length > 0) && (
          <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            {expression && (
              <code className="rounded-sm bg-surface-sunken px-1.5 py-0.5 font-mono text-foreground">
                {expression}
              </code>
            )}
            {shown.map((fact, i) => (
              <span key={i} className="inline-flex items-center gap-2">
                {(i > 0 || expression) && (
                  <span aria-hidden className="text-hairline">
                    ·
                  </span>
                )}
                {fact}
              </span>
            ))}
          </p>
        )}
      </div>
    </section>
  )
}

/**
 * The next runs as a list beside the week as a picture: the list is the
 * answer to "when exactly", the week the answer to "how often, and when in
 * the day", which a sentence like "15 2 * * 1-5" does not carry.
 */
export function WhenItRuns({
  times,
  dense,
  zone,
  week,
  empty,
}: {
  /** The next runs, soonest first. */
  times: number[]
  dense?: boolean
  zone?: string
  week?: React.ReactNode
  /** What to say when there is no run to list. */
  empty: React.ReactNode
}) {
  const now = useNow(1000)
  const shown = times.filter((t) => t >= now).slice(0, 5)
  return (
    <div className="grid min-w-0 gap-6 sm:grid-cols-[minmax(0,13rem)_minmax(0,1fr)]">
      <div className="min-w-0">
        <p className="eyebrow mb-2">Next runs</p>
        {shown.length === 0 ? (
          <p className="text-body text-muted-foreground">{empty}</p>
        ) : (
          <ol aria-label="Upcoming" className="divide-y divide-hairline">
            {shown.map((at) => {
              const label = clockLabel(at, now, zone)
              return (
                <li
                  key={at}
                  className="flex h-8 items-center justify-between gap-3 text-body"
                  title={longClock(at, zone)}
                >
                  <span className="numeric">
                    {label.day && <span className="mr-1.5 text-muted-foreground">{label.day}</span>}
                    <span className="font-medium">{label.time}</span>
                  </span>
                  <span className="numeric text-hint text-muted-foreground">
                    in {countdown(at - now)}
                  </span>
                </li>
              )
            })}
          </ol>
        )}
        {dense && shown.length > 0 && (
          <p className="mt-2 text-hint text-muted-foreground">and every few minutes after</p>
        )}
      </div>
      {week && (
        <div className="min-w-0">
          <p className="eyebrow mb-2">This week</p>
          {week}
        </div>
      )}
    </div>
  )
}

/** Midnight of the day `at` falls in, on the host's clock. */
function startOfDay(at: number, zone?: string) {
  const clock = wallClock(at, zone)
  return Math.floor(at / 60_000) * 60_000 - (clock.hour * 60 + clock.minute) * 60_000
}

/**
 * A schedule's next seven days as seven lines of a day each, midnight to
 * midnight on the host's clock, a mark at every run and the count at the
 * end: the shape a five-field expression has and its sentence cannot carry —
 * "15 2 * * 1-5" is five marks and two empty lines, "*\/15 8-18 * * *" a
 * block across each working day. Today's line has a hairline at this minute
 * and the runs before it dimmed.
 *
 * `spread` draws, after each run, how late a timer's random delay may make
 * it, as a faint wash: why certbot fires at 06:41 when its calendar says
 * midnight is the wash, not a sentence.
 *
 * The job sheet draws a job's; the job editor draws the one being written,
 * so the picture changes as the fields do.
 */
export function WeekStrip({
  schedule,
  color = KIND.cron.color,
  className,
  timeZone,
  spread,
}: {
  schedule: string
  color?: string
  className?: string
  timeZone?: string
  /** How long after each run it may really fire, in milliseconds. */
  spread?: number
}) {
  const now = useNow(60_000)
  const today = startOfDay(now, timeZone)
  const days = useMemo(
    () =>
      Array.from({ length: 7 }, (_, i) => {
        // A day is the span between two midnights, which daylight saving
        // makes 23 or 25 hours long once a year.
        const from = startOfDay(today + i * DAY + 12 * HOUR, timeZone)
        const until = startOfDay(from + DAY + 12 * HOUR, timeZone)
        const clock = wallClock(from + 12 * HOUR, timeZone)
        return {
          from,
          until,
          name: i === 0 ? "Today" : clock.weekday,
          date: clock.date.split(" ")[0],
          ...cronRunsBetween(schedule, from - 1, until - 1, RUN_CAP, timeZone),
        }
      }),
    [schedule, today, timeZone],
  )
  const place = (at: number, from: number, until: number) =>
    Math.min(100, Math.max(0, ((at - from) / (until - from)) * 100))
  const total = days.reduce((n, day) => n + day.times.length, 0)
  const dense = days.some((day) => day.dense)

  return (
    <div
      role="img"
      aria-label={`${describeCron(schedule)}, over the next seven days: ${dense ? "more than " : ""}${total} runs`}
      className={cn("space-y-1.5", className)}
    >
      {days.map((day, i) => (
        <div
          key={day.from}
          className="grid grid-cols-[3.25rem_minmax(0,1fr)_2.5rem] items-center gap-3"
        >
          <span
            className={cn(
              "numeric truncate text-hint",
              i === 0 ? "font-medium text-foreground" : "text-muted-foreground",
            )}
          >
            {day.name}
            {i > 0 && <span className="ml-1 text-muted-foreground/70">{day.date}</span>}
          </span>
          <div className="relative h-5 overflow-hidden rounded-[4px] bg-muted/40">
            {[25, 50, 75].map((left) => (
              <span
                key={left}
                aria-hidden
                className="absolute inset-y-0 w-px bg-hairline"
                style={{ left: `${left}%` }}
              />
            ))}
            {spread &&
              !day.dense &&
              day.times.map((at) => (
                <span
                  key={`spread:${at}`}
                  aria-hidden
                  className="absolute inset-y-1 rounded-[2px]"
                  style={{
                    left: `${place(at, day.from, day.until)}%`,
                    width: `${place(at + spread, day.from, day.until) - place(at, day.from, day.until)}%`,
                    background: `color-mix(in oklab, ${color} 22%, transparent)`,
                  }}
                />
              ))}
            {day.dense ? (
              <span
                aria-hidden
                className="absolute inset-y-1 rounded-[2px]"
                style={{
                  left: `${place(day.times[0], day.from, day.until)}%`,
                  right: 0,
                  background: `color-mix(in oklab, ${color} 55%, transparent)`,
                }}
              />
            ) : (
              day.times.map((at) => (
                <span
                  key={at}
                  aria-hidden
                  className="absolute inset-y-0.5 w-[3px] -translate-x-1/2 rounded-full"
                  style={{
                    left: `${place(at, day.from, day.until)}%`,
                    background: color,
                    opacity: at < now ? 0.35 : 1,
                  }}
                />
              ))
            )}
            {i === 0 && (
              <span
                aria-hidden
                className="absolute inset-y-0 w-px bg-foreground"
                style={{ left: `${place(now, day.from, day.until)}%` }}
              />
            )}
          </div>
          <span className="numeric text-right text-hint text-muted-foreground">
            {day.times.length === 0 ? "—" : `${day.times.length}${day.dense ? "+" : ""}`}
          </span>
        </div>
      ))}
      <div
        aria-hidden
        className="grid grid-cols-[3.25rem_minmax(0,1fr)_2.5rem] gap-3 text-micro text-muted-foreground"
      >
        <span />
        <div className="numeric flex justify-between">
          <span>00</span>
          <span>06</span>
          <span>12</span>
          <span>18</span>
          <span>24</span>
        </div>
        <span className="text-right">runs</span>
      </div>
    </div>
  )
}
