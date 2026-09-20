"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { MagnifyingGlass } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { relativeTime, timestamp } from "@/lib/format"
import type { DeploymentLifecycle, DockerEvent } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Hint } from "@/components/docker/explain"
import { RowList, Row } from "@/components/row-list"
import { FilterChip, ChipCount } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Input } from "@/components/ui/input"

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
 * the answer to "why did this restart at 04:00" is otherwise a shrug. What is
 * kept is a bounded ring in this process's memory, which is the boundary the
 * empty state names rather than leaves the reader to infer: a restart of the
 * dashboard empties it, and the record starts again from there.
 *
 * It is searched and followed like the two views beside it. A feed that only
 * polls is a feed that tells you about the restart up to ten seconds after the
 * page next to it has drawn the 502s, and the two readings are supposed to sit
 * on one timeline.
 */
const KINDS = [
  { id: "container", label: "Containers" },
  { id: "network", label: "Networks" },
] as const

/** Below this, "watching since" is a restart rather than a quiet afternoon. */
const RECENTLY_STARTED_MS = 60 * 60_000

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
  const [search, setSearch] = useSessionState("deploy.events.query", "")
  // Empty means everything, which is what the server does with no `kinds` — so
  // "All" is a real state rather than "every box ticked", and the two cannot
  // disagree.
  const [kinds, setKinds] = useSessionState<string[]>("deploy.events.kinds", [])
  const [live, setLive] = useState<DockerEvent[]>([])
  const [following, setFollowing] = useState(true)

  const feed = usePoll<DeploymentLifecycle>(
    (signal) => get<DeploymentLifecycle>(`/deploy/${projectId}/lifecycle`, { limit: 200 }, signal),
    10000,
    [projectId],
  )

  // The socket carries everything this environment produces and the filters are
  // applied below, for the reason the host feed gives: re-subscribing on every
  // keystroke would drop the connection four times a word.
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    setLive((prev) => [...(envelope.data as DockerEvent[])].concat(prev).slice(0, 500))
  }, [])
  const socket = useSocket(`/deploy/${projectId}/lifecycle/stream`, {
    onMessage,
    enabled: following,
  })

  const data = feed.data
  // The polled copy first: it is the one the server has laid against the audit
  // log, and an arriving event has no trigger on it yet. Deduping in this order
  // means a row stops saying "docker itself" once the poll knows better,
  // instead of flickering back to it every time the socket repeats itself.
  const all = useMemo(
    () => dedupe([...(data?.events ?? []), ...live]),
    [data?.events, live],
  )

  const needle = search.trim().toLowerCase()
  const wanted = new Set(kinds)
  const at = moment ? Date.parse(moment) : NaN
  const shown = all.filter((event) => {
    if (wanted.size > 0 && !wanted.has(event.type)) return false
    if (needle && !`${event.message} ${event.name}`.toLowerCase().includes(needle)) return false
    if (Number.isFinite(at) && Math.abs(Date.parse(event.time) - at) > 2 * 60_000) return false
    return true
  })

  if (feed.error && !data) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <ErrorState error={feed.error} onRetry={feed.refresh} />
      </div>
    )
  }
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

  const filtered = needle !== "" || wanted.size > 0

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b border-hairline px-2 py-1.5">
        <label className="relative flex min-w-48 flex-1 items-center">
          <MagnifyingGlass className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground" />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter events — exited, unhealthy, a container name"
            aria-label="Filter container events"
            className="h-8 pl-8 font-mono text-xs"
          />
        </label>

        <div className="flex shrink-0 items-center gap-1">
          <FilterChip selected={kinds.length === 0} onClick={() => setKinds([])}>
            All
            <ChipCount>{all.length}</ChipCount>
          </FilterChip>
          {KINDS.map((kind) => {
            const on = wanted.has(kind.id)
            return (
              <FilterChip
                key={kind.id}
                selected={on}
                onClick={() =>
                  setKinds(on ? kinds.filter((k) => k !== kind.id) : [...kinds, kind.id])
                }
              >
                {kind.label}
                <ChipCount>{all.filter((event) => event.type === kind.id).length}</ChipCount>
              </FilterChip>
            )
          })}
        </div>

        <FilterChip
          selected={following}
          onClick={() => setFollowing(!following)}
          className="shrink-0"
          title="Follow the daemon's event stream, rather than waiting for the next poll"
        >
          {following && socket.state === "open" && (
            <span className="size-1.5 rounded-full bg-success animate-breathe" />
          )}
          Live
        </FilterChip>
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-5 py-3">
        {moment && (
          <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
            <span>
              Around <span className="numeric text-foreground">{timestamp(moment)}</span> — two
              minutes either side.
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
        {shown.length === 0 ? (
          <EmptyState {...emptyReading({ filtered, moment, since: data.since })} />
        ) : (
          <>
            <RowList>
              {shown.map((event, i) => (
                <LifecycleRow
                  key={`${event.time}:${event.id ?? ""}:${i}`}
                  event={event}
                  projectId={projectId}
                />
              ))}
            </RowList>
            <Hint className="pt-3">
              Kept in this process&apos;s memory and bounded, so this is the recent past rather than
              a permanent record — a restart of the dashboard starts it again. Everything the
              dashboard itself did is in the audit log, which survives one.
            </Hint>
          </>
        )}
      </div>
    </div>
  )
}

/**
 * Why there is nothing to show, which is four different pieces of news.
 *
 * The one worth separating out is a dashboard that restarted a few minutes ago.
 * The record is this process's, so "nothing has happened since 07:28" is not the
 * steadiness it reads as — it is the buffer having been emptied at 07:28, and a
 * container that died at 04:00 is simply gone. Saying "which for a running
 * deployment is the reading you want" in that state is a claim the page cannot
 * make.
 */
function emptyReading({
  filtered,
  moment,
  since,
}: {
  filtered: boolean
  moment?: string
  since?: string
}): { title: string; description: string } {
  if (filtered) {
    return {
      title: "Nothing matches that filter",
      description:
        "No recorded event carries that text or belongs to that kind. Clear the filter to see the whole record.",
    }
  }
  if (moment) {
    return {
      title: "Nothing happened to the container then",
      description:
        "No exit, restart, OOM kill or health change within two minutes of that request. Whatever failed, it was not the container's life.",
    }
  }
  const startedAt = since ? Date.parse(since) : NaN
  if (Number.isFinite(startedAt) && Date.now() - startedAt < RECENTLY_STARTED_MS) {
    return {
      title: "Nothing has happened since the dashboard started",
      description: `This record lives in the dashboard's own memory and begins when it starts — which was ${relativeTime(
        since!,
      )}. Nothing from before then was kept, so this is not yet evidence that the deployment has been steady.`,
    }
  }
  return {
    title: "Nothing has happened to these containers",
    description: since
      ? `Watching since ${timestamp(since)}. No start, stop, restart, exit or health change has been recorded for this deployment since then — which for a running deployment is the reading you want. Nothing survives a restart of the dashboard, so the record begins there rather than at the deployment's first release.`
      : "No start, stop, restart, exit or health change has been recorded for this deployment.",
  }
}

/**
 * The socket sends the buffered past on connect and the poll reads the same
 * buffer, so the two overlap by design. Identity is the timestamp plus the
 * object: Docker's own event ids are the object's, not the event's.
 */
function dedupe(events: DockerEvent[]): DockerEvent[] {
  const seen = new Set<string>()
  const out: DockerEvent[] = []
  for (const event of events) {
    const key = `${event.time}|${event.type}|${event.action}|${event.id ?? event.name}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(event)
  }
  return out.sort((a, b) => b.time.localeCompare(a.time))
}

/**
 * One event. The message is already a sentence by the time it reaches here —
 * "container exited with status 137" rather than the raw pair ("container",
 * "die") — because translating Docker's event vocabulary belongs in one place,
 * not in every component that shows an event.
 *
 * The release and the audit entry are links rather than text. Both are the
 * reader's next move: "release 20 restarted twice" is a question about that
 * release, and "this dashboard did it" is worth nothing if finding out which
 * press means filtering the audit log by hand.
 */
function LifecycleRow({ event, projectId }: { event: DockerEvent; projectId: number }) {
  const release = event.owner?.["release-number"] ?? event.owner?.["release-id"]
  const runId = event.owner?.["run-id"]
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
        <span className="flex flex-wrap items-baseline gap-x-2">
          <span>{timestamp(event.time)}</span>
          {event.name && <span>· {event.name}</span>}
          {release &&
            (runId ? (
              <Link
                href={`/deploy/${projectId}/runs/${runId}`}
                className="rounded-sm text-foreground focus-ring hover:underline"
                title="Open the run that put this release on the server"
              >
                · release {release}
              </Link>
            ) : (
              <span>· release {release}</span>
            ))}
        </span>
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
            <Link
              href={`/audit?action=${encodeURIComponent(event.trigger.action)}`}
              className="shrink-0 rounded-sm text-micro whitespace-nowrap text-primary focus-ring hover:underline"
              title={`Audit entry ${event.trigger.auditId} — ${event.trigger.action} by ${
                event.trigger.actor || "an unnamed session"
              }. A name and a window, so a likely cause rather than a recorded one.`}
            >
              this dashboard
            </Link>
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
