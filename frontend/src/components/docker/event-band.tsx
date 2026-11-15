"use client"

import { CrossCircle, Plus, RotateClockwise, Terminal, type Icon } from "@/components/icons"
import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { timeAt } from "@/components/bar-reading-model"
import { relativeTime } from "@/lib/format"
import type { DockerEvent } from "@/lib/types"
import { OUTCOME } from "@/components/docker/event-activity"
import { EventMark } from "@/components/docker/event-marks"

/** The four readings, each a question a press narrows the page to. */
export type EventReading = "failures" | "restarts" | "changes" | "external"

export const READING_LABEL: Record<EventReading, string> = {
  failures: "Failures",
  restarts: "Restarts",
  changes: "Created or removed",
  external: "External",
}

const READING_MARK: Record<EventReading, [Icon, string, string]> = {
  failures: [CrossCircle, "text-destructive", OUTCOME.failed.color],
  restarts: [RotateClockwise, "text-warning", OUTCOME.restarted.color],
  changes: [Plus, "text-tag-cyan", OUTCOME.changed.color],
  external: [Terminal, "text-tag-violet", "var(--tag-violet)"],
}

/** How many of the newest notable events Recent draws. */
const SHOWN = 12

/**
 * The band the Events page opens on, as every Docker page opens on one
 * (PGH-22): what happened over the window as one bar of its four kinds, each
 * a press that narrows the page to what it counts, and Recent, the newest
 * failures, comebacks and unexplained actions on the window's axis, each a
 * press that narrows the page to its container.
 *
 * It replaces a row of four tiles with sparklines. A count over time was the
 * one reading the tiles had over the bands, and Recent draws when they were.
 */
export function EventBand({
  counts,
  hints,
  reading,
  onReading,
  notable,
  from,
  to,
  words,
  selected,
  onContainer,
  windowPicker,
}: {
  counts: Record<EventReading, number>
  /** A few words after each kind's name: who failed, who came back. */
  hints: Record<EventReading, string>
  reading: EventReading | ""
  onReading: (next: EventReading) => void
  /** The window's notable events, newest first. */
  notable: DockerEvent[]
  from: number
  to: number
  /** The window in words: "the last day". */
  words: string
  selected: string
  onContainer: (key: string) => void
  /** The page's window picker, on the first reading's line. */
  windowPicker?: React.ReactNode
}) {
  const kinds = Object.keys(READING_LABEL) as EventReading[]
  const total = kinds.reduce((sum, kind) => sum + counts[kind], 0)
  const parts: ReadingPart[] = kinds
    .filter((kind) => counts[kind] > 0)
    .map((kind) => {
      const [Glyph, ink, color] = READING_MARK[kind]
      return {
        key: kind,
        value: counts[kind],
        color,
        mark: <Glyph aria-hidden className={`size-3.5 ${ink}`} />,
        name: READING_LABEL[kind],
        detail: hints[kind],
        figure: counts[kind],
        label: `${READING_LABEL[kind]} ${counts[kind]}`,
        press: {
          label: `Show only ${READING_LABEL[kind].toLowerCase()}`,
          pressed: reading === kind,
          onPress: () => onReading(kind),
        },
      }
    })

  const recent: ReadingPart[] = notable.slice(0, SHOWN + 1).map((event) => {
    const key = event.id || event.name
    return {
      key: `${event.time}/${key}/${event.action}`,
      value: 1,
      at: timeAt(Date.parse(event.time), to, to - from),
      mark: <EventMark event={event} loop={false} />,
      name: event.name,
      detail: event.message,
      figure: (
        <span className="text-hint font-normal text-muted-foreground">
          {relativeTime(event.time)}
        </span>
      ),
      label: `${event.message} ${relativeTime(event.time)}`,
      title: new Date(event.time).toLocaleString(),
      press:
        event.type === "container"
          ? {
              label: `Only ${event.name}'s events`,
              pressed: selected === key,
              onPress: () => onContainer(key),
            }
          : undefined,
    }
  })

  return (
    <div
      data-slot="event-band"
      className="grid min-w-0 animate-rise gap-x-10 gap-y-6 lg:grid-cols-2"
    >
      <BarReading
        title="Outcomes"
        label="Events by outcome"
        list="What happened"
        fixed
        figure={
          windowPicker ?? (
            <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
              {total > 0 ? `${total} in ${words}` : words}
            </span>
          )
        }
        message={
          total === 0
            ? `Nothing failed, restarted, or was created or removed in ${words}.`
            : undefined
        }
        parts={parts}
      />
      <BarReading
        title="Recent"
        label="Recent notable events"
        list="Latest failures, restarts and outside actions"
        ticks
        figure={
          <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
            {words}
          </span>
        }
        message={
          recent.length === 0
            ? `Nothing failed, came back or happened from outside in ${words}.`
            : undefined
        }
        parts={recent}
      />
    </div>
  )
}
