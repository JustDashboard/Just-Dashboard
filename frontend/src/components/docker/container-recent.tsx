"use client"

import { useCallback, useMemo, useState } from "react"
import { ArrowRight } from "@/components/icons"
import { get } from "@/lib/api"
import { clock, relativeTime, timestamp } from "@/lib/format"
import { dedupeEvents, foldRestarts, spanWords, type EventEntry } from "@/lib/docker-events"
import type { ContainerDetail, DockerEvent, DockerEventFeed } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { Panel, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Tag } from "@/components/tag"
import { EventMark, EventWho } from "@/components/docker/event-marks"
import { containerProduct } from "@/components/product-logo"

/** As many as the block shows; the Events view beside the logs has the rest. */
const SHOWN = 6

/**
 * What just happened to the container: Docker's event log for it, newest
 * first, a restart loop folded to one line ("restarted ×23 in 19 min · exit
 * 1"), each with who did it where the audit log can say — this dashboard, or
 * Docker on its own.
 *
 * The page said how the container *is* and never what had *happened* to it,
 * so an exit an hour ago or a restart somebody pressed this morning took the
 * Logs tab and its Events view to find. The same feed, polled and followed
 * live, so an exit while the page is open rises into the list.
 */
export function ContainerRecent({
  detail,
  onEvents,
}: {
  detail: Pick<ContainerDetail, "id" | "image" | "labels">
  /** Opens the logs on their Events view, where every event is. */
  onEvents: () => void
}) {
  const scope = useMemo(() => ({ container: detail.id }), [detail.id])
  const feed = usePoll<DockerEventFeed>(
    (signal) => get<DockerEventFeed>("/docker/events", { ...scope, limit: 200 }, signal),
    30_000,
    [detail.id],
  )
  const [live, setLive] = useState<DockerEvent[]>([])
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    setLive((prev) => [...(envelope.data as DockerEvent[])].concat(prev).slice(0, 200))
  }, [])
  useSocket("/docker/events/stream", { query: scope, onMessage })

  const entries = useMemo(
    () =>
      foldRestarts(dedupeEvents([...(feed.data?.events ?? []), ...live]))
        // An exec is a health probe or somebody's shell: not something that happened to it.
        .filter((entry) => entry.kind === "loop" || !entry.event.action.startsWith("exec_"))
        .slice(0, SHOWN),
    [feed.data?.events, live],
  )
  const arrived = useArrivals(entries.map((entry) => entry.key))
  const product = containerProduct(detail)

  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title="Recent"
        actions={
          <button
            type="button"
            onClick={onEvents}
            className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
          >
            All events <ArrowRight className="size-3" />
          </button>
        }
      />
      {feed.data && entries.length === 0 ? (
        <EmptyNote className="pt-3">
          Nothing has happened to it since the dashboard began listening
          {feed.data.since ? `, ${relativeTime(feed.data.since)}` : ""}.
        </EmptyNote>
      ) : (
        <ul className="divide-y divide-hairline" aria-label="Recent events">
          {entries.map((entry) => (
            <RecentRow
              key={entry.key}
              entry={entry}
              product={product}
              arrived={arrived.has(entry.key)}
            />
          ))}
        </ul>
      )}
    </Panel>
  )
}

function RecentRow({
  entry,
  product,
  arrived,
}: {
  entry: EventEntry
  product: string
  arrived: boolean
}) {
  const loop = entry.kind === "loop" ? entry : undefined
  const event = loop ? loop.exit : entry.kind === "event" ? entry.event : undefined
  if (!event) return null
  const oom = entry.kind === "event" ? entry.oom : undefined
  const title = loop
    ? `Restarted ×${loop.times} in ${spanWords(Date.parse(loop.until) - Date.parse(loop.since))}`
    : (oom ?? event).message
  const at = loop ? loop.until : event.time
  const exit = loop ? loop.exitCode : event.exitCode
  return (
    <li className={cn("flex min-w-0 items-center gap-3 py-2.5", arrived && "animate-rise")}>
      <EventMark event={event} product={product} loop={Boolean(loop)} />
      <div className="min-w-0 flex-1">
        <p className="truncate text-body leading-5 font-medium">{title}</p>
        <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          <span className="numeric" title={timestamp(at)}>
            {loop ? `${clock(loop.since)}–${clock(loop.until)}` : clock(event.time)}
          </span>
          <span className="text-muted-foreground/40">·</span>
          <span className="truncate">{relativeTime(at)}</span>
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {(oom || loop?.oom) && <Tag tone="danger">out of memory</Tag>}
        {exit && exit !== "0" && (
          <Tag tone="danger" mono>
            exit {exit}
          </Tag>
        )}
        <span className="max-sm:hidden">
          <EventWho event={event} />
        </span>
      </div>
    </li>
  )
}
