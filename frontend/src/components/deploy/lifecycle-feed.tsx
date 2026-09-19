"use client"

import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { relativeTime, timestamp } from "@/lib/format"
import type { DeploymentLifecycle, DockerEvent } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { RowList, Row } from "@/components/row-list"
import { Tag } from "@/components/tag"

/**
 * What Docker did to this deployment's containers.
 *
 * The third reading the Logs page needs, and the one that resolves the other
 * two. A container that prints nothing because the application logs nothing
 * and a container that prints nothing because it was OOM-killed four minutes
 * ago are identical in a log tail — and a burst of 502s in the request record
 * with a restart at the same minute is one incident, not two.
 *
 * The events are Docker's own, kept by the dashboard because the daemon keeps
 * none: `docker events` shows you what happens from the moment you run it, so
 * the answer to "why did this restart at 04:00" is otherwise a shrug.
 */
export function LifecycleFeed({
  projectId,
  moment,
  onClearMoment,
}: {
  projectId: number
  /** An instant to look around — a failing request's — scoping the list to ±2 minutes. */
  moment?: string
  onClearMoment?: () => void
}) {
  const feed = usePoll<DeploymentLifecycle>(
    (signal) => get<DeploymentLifecycle>(`/deploy/${projectId}/lifecycle`, undefined, signal),
    10000,
    [projectId],
  )

  if (feed.error && !feed.data) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <ErrorState error={feed.error} onRetry={feed.refresh} />
      </div>
    )
  }
  const data = feed.data
  if (!data) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6 text-hint text-muted-foreground">
        Reading the container record…
      </div>
    )
  }
  if (data.status !== "available") {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState title="Container events are unavailable" description={data.reason} />
      </div>
    )
  }

  const at = moment ? Date.parse(moment) : NaN
  const around = Number.isFinite(at)
    ? data.events.filter((e) => Math.abs(Date.parse(e.time) - at) <= 2 * 60_000)
    : data.events

  return (
    <div className="min-h-0 flex-1 overflow-auto px-5 py-3">
      {moment && (
        <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
          <span>
            Around <span className="numeric text-foreground">{timestamp(moment)}</span> — two minutes
            either side.
          </span>
          {onClearMoment && (
            <button
              type="button"
              onClick={onClearMoment}
              className="rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Show everything
            </button>
          )}
        </div>
      )}
      {!data.watching && (
        <Notice tone="warning" title="The Docker event stream is not connected" className="mb-3">
          This list is whatever was recorded before the connection dropped, and it stops here.
        </Notice>
      )}
      {around.length === 0 ? (
        <EmptyState
          title={moment ? "Nothing happened to the container then" : "Nothing has happened to these containers"}
          description={
            moment
              ? "No exit, restart, OOM kill or health change within two minutes of that request. Whatever failed, it was not the container's life."
              : data.since
                ? `Watching since ${timestamp(data.since)}. No start, stop, restart, exit or health change has been recorded for this deployment since then — which for a running deployment is the reading you want.`
                : "No start, stop, restart, exit or health change has been recorded for this deployment."
          }
        />
      ) : (
        <RowList>
          {around.map((event, i) => (
            <LifecycleRow key={`${event.time}:${event.id ?? ""}:${i}`} event={event} />
          ))}
        </RowList>
      )}
    </div>
  )
}

/**
 * One event. The message is already a sentence by the time it reaches here —
 * "container exited with status 137" rather than the raw pair ("container",
 * "die") — because translating Docker's event vocabulary belongs in one place,
 * not in every component that shows an event.
 */
function LifecycleRow({ event }: { event: DockerEvent }) {
  return (
    <Row
      leading={
        <span
          aria-hidden
          className={cn(
            "size-1.5 rounded-full",
            event.level === "error"
              ? "bg-destructive"
              : event.level === "notice"
                ? "bg-warning"
                : "bg-muted-foreground/40",
          )}
        />
      }
      title={event.message}
      subtitle={
        [
          timestamp(event.time),
          event.name,
          event.owner?.["release-id"] ? `release ${event.owner["release-id"]}` : null,
        ]
          .filter(Boolean)
          .join(" · ") || undefined
      }
      trailing={
        <>
          {/* An exit code is the one fact that changes what you do next, so it
              stays on the row at every width rather than inside the sentence. */}
          {event.exitCode && event.exitCode !== "0" && (
            <Tag tone="danger" mono>
              exit {event.exitCode}
            </Tag>
          )}
          {event.trigger ? (
            <Tag title={`Audit entry ${event.trigger.auditId} — ${event.trigger.action}`}>
              this dashboard
            </Tag>
          ) : event.source === "daemon" ? (
            <Tag title="Docker acted on its own: a restart policy firing, or the OOM reaper.">
              docker itself
            </Tag>
          ) : null}
          <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
            {relativeTime(event.time)}
          </span>
        </>
      }
    />
  )
}
