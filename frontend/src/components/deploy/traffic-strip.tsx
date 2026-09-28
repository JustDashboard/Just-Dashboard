"use client"

import { cn } from "@/lib/utils"
import { clockMinute, plural } from "@/lib/format"
import type { DockerEvent, RequestBucket } from "@/lib/types"

/**
 * The last hour of a deployment, a minute at a time, in the shape Backups
 * draws a job's last fourteen runs (`OutcomeStrip`): a mark per minute in the
 * colour of how that minute went, so an hour with one bad minute reads as
 * that before a figure is read. Tremor's `Tracker` is the registry's version
 * of the same picture; the pattern is taken, not the dependency.
 *
 * Two strips, one scale: the Failing reading's minutes and the Container
 * reading's events both run from the hour's start at the left to now at the
 * right, so a red minute and an exit tick at the same place are one incident
 * read twice.
 *
 * Squares with the mark's radius and no text, never dots (§4); token colours
 * only; and no motion of their own — the tile they sit in rises when its
 * reading lands (§11).
 */

/** Sixty minutes, which is what the readings above the pane are taken over. */
const MINUTES = 60

/**
 * One cell per minute of the hour. The server's columns run a minute each,
 * every minute present, from the hour's start to the newest request's minute
 * — the minute happening now, on a live site: an hour asked partway into a
 * minute touches sixty-one, and the server drops the clipped oldest one, not
 * the newest. So the cells are the columns in order; the minutes after the
 * newest request, which it has no column for, are drawn as the quiet they
 * were.
 */
export function MinuteStrip({ buckets }: { buckets: RequestBucket[] }) {
  const minutes = buckets.slice(-MINUTES)
  const failing = minutes.filter((bucket) => (bucket.counts["5xx"] ?? 0) > 0).length
  const quiet = MINUTES - minutes.filter((bucket) => bucket.total > 0).length
  return (
    <div
      role="img"
      aria-label={`Last hour by minute: ${plural(failing, "minute")} with server errors, ${quiet} without traffic`}
      className="flex h-9 min-w-0 items-end gap-px overflow-hidden pb-0.5"
    >
      {Array.from({ length: MINUTES }, (_, i) => {
        const bucket = minutes[i]
        const failed = bucket?.counts["5xx"] ?? 0
        return (
          <span
            key={i}
            title={
              bucket
                ? `${clockMinute(bucket.start)} · ${plural(bucket.total, "request")}${failed > 0 ? ` · ${failed} × 5xx` : ""}`
                : "No requests"
            }
            className={cn(
              "h-5 min-w-px flex-1 rounded-sm",
              !bucket || bucket.total === 0
                ? "bg-meter-track"
                : failed > 0
                  ? "bg-destructive"
                  : "bg-success/70",
            )}
          />
        )
      })}
    </div>
  )
}

/**
 * The actions the Container reading counts, and the colour each takes. A kill
 * is not one of them: Docker reports the exit that follows it, and counting
 * both would draw one stop as two.
 */
const TICK: Record<string, string> = {
  die: "bg-destructive",
  oom: "bg-destructive",
  restart: "bg-warning",
  start: "bg-success/70",
}

export function isTickEvent(event: DockerEvent) {
  return event.type === "container" && event.action in TICK
}

/**
 * An exit with status 0 — a routine stop, the old container of a release
 * swap. The server already calls it a notice rather than an error, and it is
 * drawn as one: a quiet tick, never the failure's red (§3).
 */
export function isCleanExit(event: DockerEvent) {
  return event.type === "container" && event.action === "die" && event.level !== "error"
}

function tickTone(event: DockerEvent) {
  return isCleanExit(event) ? "bg-muted-foreground" : TICK[event.action]
}

/**
 * The container's hour as a rail with a tick where each exit, restart and
 * start happened. Positioned by time rather than by minute cell, because two
 * events a few seconds apart are the story ("exited, then started") and a
 * cell would draw them as one.
 */
export function EventStrip({
  events,
  from,
  to,
}: {
  events: DockerEvent[]
  /** The hour's start and end, as the fetch that read the events saw them. */
  from: number
  to: number
}) {
  const ticks = events.filter(isTickEvent)
  const exits = ticks.filter((event) => tickTone(event) === "bg-destructive").length
  const stops = ticks.filter(isCleanExit).length
  const restarts = ticks.filter((event) => event.action === "restart").length
  const starts = ticks.filter((event) => event.action === "start").length
  const span = Math.max(to - from, 1)
  const label = [
    exits && plural(exits, "exit"),
    stops && plural(stops, "clean stop"),
    restarts && plural(restarts, "restart"),
    starts && plural(starts, "start"),
  ].filter(Boolean)
  return (
    <div
      role="img"
      aria-label={
        label.length > 0
          ? `${label.join(" and ")} in the last hour`
          : "No exit, restart or start in the last hour"
      }
      className="relative h-9 min-w-0"
    >
      <span aria-hidden className="absolute inset-x-0 bottom-0.5 h-px bg-hairline" />
      {ticks.map((event, i) => {
        const at = Math.min(Math.max((Date.parse(event.time) - from) / span, 0), 1)
        return (
          <span
            key={`${event.time}:${i}`}
            title={event.message}
            className={cn("absolute bottom-0.5 h-4 w-0.5 rounded-sm", tickTone(event))}
            style={{ left: `min(${at * 100}%, calc(100% - 2px))` }}
          />
        )
      })}
    </div>
  )
}
