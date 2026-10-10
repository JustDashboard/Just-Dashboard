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
 * An exit with status 0 — a routine stop, the old container of a release
 * swap. The server already calls it a notice rather than an error, so it is
 * never counted as a disruption.
 */
export function isCleanExit(event: DockerEvent) {
  return event.type === "container" && event.action === "die" && event.level !== "error"
}
