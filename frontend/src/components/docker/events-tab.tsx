"use client"

import { useCallback, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { ChartActivity, DotMark, Stop } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DockerEvent, DockerEventFeed } from "@/lib/types"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { SearchInput } from "@/components/page"
import { Hint } from "@/components/docker/explain"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"

/**
 * What the daemon did, including while nobody was looking.
 *
 * Docker emits an event for everything it does and keeps none of them:
 * `docker events` shows you what happens from the moment you run it, so the
 * answer to "why did this restart at 04:00" is nowhere. The dashboard is a
 * long-running process already connected to the thing producing the record, so
 * it listens and keeps the recent past.
 *
 * Two changes over the version this replaces, both about being able to trust
 * what you are looking at:
 *
 * The filter opened on Containers with no way to say "everything" and no
 * indication that anything was being hidden, so an image pull simply did not
 * appear and the feed looked broken. There is an All chip now, it is the
 * default, and the selected chip is filled rather than outlined — the outline
 * was also what keyboard focus used, so a keyboard user could not tell which
 * filters were on.
 *
 * And every row now says who did it. Docker records what happened and never
 * who asked, so "container removed" was equally consistent with a colleague
 * pressing a button and with something on the host nobody knows about. The
 * server correlates against the audit log to tell those apart.
 */

const KINDS = [
  { id: "container", label: "Containers" },
  { id: "image", label: "Images" },
  { id: "volume", label: "Volumes" },
  { id: "network", label: "Networks" },
]

export function EventsTab() {
  // Empty means everything, which is what the server does with no `kinds` —
  // so "All" is a real state rather than "every box ticked", and the two
  // cannot disagree.
  const [kinds, setKinds] = useSessionState<string[]>("docker.events.kinds", [])
  const [search, setSearch] = useSessionState("docker.events.query", "")
  const [live, setLive] = useState<DockerEvent[]>([])

  const query = useMemo(() => ({ limit: 300, kinds: kinds.join(","), search }), [kinds, search])

  const { data, error, loading } = usePoll<DockerEventFeed>(
    (signal) => get<DockerEventFeed>("/docker/events", query, signal),
    // The socket below carries anything new; this is the backfill and the
    // filter, so it only needs to re-run when the filter changes.
    0,
    [JSON.stringify(query)],
  )

  // The live feed is unfiltered on the wire — the filters are cheap in the
  // browser once the batch is small, and re-subscribing on every keystroke
  // would drop the socket four times a word.
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    const batch = envelope.data as DockerEvent[]
    setLive((prev) => [...batch].concat(prev).slice(0, 500))
  }, [])
  useSocket("/docker/events/stream", { onMessage })

  const all = useMemo(() => dedupe([...live, ...(data?.events ?? [])]), [live, data])

  const counts = useMemo(() => {
    const out: Record<string, number> = {}
    for (const e of all) out[e.type] = (out[e.type] ?? 0) + 1
    return out
  }, [all])

  const wanted = new Set(kinds)
  const needle = search.trim().toLowerCase()
  const merged = all.filter(
    (e) =>
      (wanted.size === 0 || wanted.has(e.type)) &&
      (!needle || `${e.message} ${e.name} ${e.image ?? ""}`.toLowerCase().includes(needle)),
  )
  const groups = groupByDay(merged)

  return (
    // Plain: the feed is the page, and a title, a hairline and the day
    // markers are what structure it.
    <Panel plain>
      <PanelHeader
        title="Events"
        actions={
          <Status
            state={data?.listening ? "connected" : "disconnected"}
            live={Boolean(data?.listening)}
            label={data?.listening ? "Live" : "Offline"}
          />
        }
      />
      <PanelToolbar>
        <SearchInput
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Filter events"
        />
        <div className="flex flex-wrap gap-1">
          <FilterChip selected={kinds.length === 0} onClick={() => setKinds([])}>
            All
            <ChipCount>{all.length}</ChipCount>
          </FilterChip>
          {KINDS.map((k) => (
            <FilterChip
              key={k.id}
              selected={kinds.includes(k.id)}
              onClick={() =>
                setKinds((prev) =>
                  prev.includes(k.id) ? prev.filter((x) => x !== k.id) : [...prev, k.id],
                )
              }
            >
              {k.label}
              {counts[k.id] ? <ChipCount>{counts[k.id]}</ChipCount> : null}
            </FilterChip>
          ))}
        </div>
      </PanelToolbar>
      <PanelBody flush>
        {loading && !data ? (
          <LoadingRows className="py-3" />
        ) : error ? (
          <ErrorState error={error} />
        ) : merged.length === 0 ? (
          <EmptyState
            icon={ChartActivity}
            title={kinds.length || needle ? "Nothing matches that filter" : "Nothing yet"}
            description={
              kinds.length || needle
                ? "The daemon has done nothing of that kind since the dashboard started listening."
                : data?.listening
                  ? "The daemon has done nothing worth recording since the dashboard started listening. Events appear here as they happen."
                  : "The dashboard is not connected to Docker's event stream, so nothing is being kept."
            }
          />
        ) : (
          /* Padded by the rows' bleed, so the hover wash has room without the
             container growing a sideways scrollbar. Rises once when the feed
             lands. */
          <div className="-mx-3 max-h-[calc(100svh-26rem)] animate-rise overflow-auto px-3">
            {groups.map(([day, events]) => (
              <section key={day}>
                <h3 className="sticky top-0 z-10 border-b border-hairline bg-background/90 py-1 text-hint font-medium text-muted-foreground backdrop-blur">
                  {day}
                </h3>
                <ul className="divide-y divide-hairline">
                  {events.map((event, i) => (
                    <EventRow key={`${event.time}-${event.id}-${i}`} event={event} />
                  ))}
                </ul>
              </section>
            ))}
          </div>
        )}
      </PanelBody>
      {merged.length > 0 && (
        <PanelFooter>
          <Hint>
            Kept in memory and bounded, so this is the recent past rather than a permanent record.
            Everything the dashboard itself did is in the audit log, which survives a restart.
          </Hint>
        </PanelFooter>
      )}
    </Panel>
  )
}

/**
 * The socket sends the buffered past on connect and the poll fetches the same
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
 * Grouped by day, with the timestamp reduced to a time.
 *
 * A column of full dates on a feed where nine tenths of the rows are from
 * today is nine tenths noise; the date belongs on the group and the time on
 * the row.
 */
function groupByDay(events: DockerEvent[]): [string, DockerEvent[]][] {
  const out = new Map<string, DockerEvent[]>()
  for (const event of events) {
    const label = dayLabel(new Date(event.time))
    const bucket = out.get(label)
    if (bucket) bucket.push(event)
    else out.set(label, [event])
  }
  return [...out.entries()]
}

function dayLabel(date: Date): string {
  const today = new Date()
  const yesterday = new Date(today)
  yesterday.setDate(today.getDate() - 1)
  const sameDay = (a: Date, b: Date) => a.toDateString() === b.toDateString()
  if (sameDay(date, today)) return "Today"
  if (sameDay(date, yesterday)) return "Yesterday"
  return date.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" })
}

const LEVEL_ICON = {
  error: { icon: Stop, tone: "text-destructive" },
  notice: { icon: DotMark, tone: "text-primary" },
  info: { icon: DotMark, tone: "text-muted-foreground/50" },
} as const

/**
 * Where an event came from. "Docker removed a container" and "somebody in this
 * dashboard removed a container" are the same event and completely different
 * news, and the second is the one that stops an operator hunting for an
 * intruder.
 */
const SOURCE = {
  dashboard: { label: "Just Dashboard", tone: "text-primary" },
  compose: { label: "Compose", tone: "text-muted-foreground" },
  daemon: { label: "Docker daemon", tone: "text-muted-foreground" },
  docker: { label: "External", tone: "text-warning" },
} as const

function EventRow({ event }: { event: DockerEvent }) {
  const meta = LEVEL_ICON[event.level] ?? LEVEL_ICON.info
  const Icon = meta.icon
  const source = SOURCE[event.source as keyof typeof SOURCE] ?? SOURCE.docker

  return (
    <li className="min-w-0">
      <div
        className={cn(
          "flex min-w-0 items-baseline gap-3 px-4 py-1.5 text-xs transition-colors hover:bg-row-hover",
          ROW_BLEED,
        )}
      >
        <Icon className={cn("size-2.5 shrink-0 translate-y-0.5", meta.tone)} />
        <span className="numeric w-20 shrink-0 font-mono text-hint whitespace-nowrap text-muted-foreground">
          {new Date(event.time).toLocaleTimeString(undefined, {
            hour: "2-digit",
            minute: "2-digit",
            second: "2-digit",
          })}
        </span>
        <span className="min-w-0 flex-1 break-words">{event.message}</span>

        <HoverCard openDelay={150}>
          <HoverCardTrigger asChild>
            <button
              type="button"
              title="Where this event came from"
              className={cn(
                "shrink-0 cursor-help rounded-sm text-micro whitespace-nowrap focus-ring",
                source.tone,
              )}
            >
              {source.label}
            </button>
          </HoverCardTrigger>
          <HoverCardContent className="w-80 space-y-1.5 text-xs leading-relaxed">
            {event.trigger ? (
              <>
                <p className="text-body font-medium">Triggered by this dashboard</p>
                <p className="text-muted-foreground">
                  An audit entry for <b>{event.trigger.action}</b> by{" "}
                  <b>{event.trigger.actor || "an unnamed session"}</b> names the same object within
                  a minute of this event. That is a likely cause rather than a recorded one — Docker
                  does not say who asked.
                </p>
                <Link
                  href={`/audit?action=${encodeURIComponent(event.trigger.action)}`}
                  className="inline-block text-primary hover:underline"
                >
                  Open the audit log
                </Link>
              </>
            ) : event.source === "compose" ? (
              <>
                <p className="text-body font-medium">Compose owns this object</p>
                <p className="text-muted-foreground">
                  It carries the labels compose writes, so this is part of the <b>{event.stack}</b>{" "}
                  project. Nothing in the audit log matches it, so it was run from a shell rather
                  than from here.
                </p>
              </>
            ) : event.source === "daemon" ? (
              <>
                <p className="text-body font-medium">Docker did this on its own</p>
                <p className="text-muted-foreground">
                  Nobody asked for it — a restart policy firing, a health check changing verdict, or
                  the kernel stopping a container.
                </p>
              </>
            ) : (
              <>
                <p className="text-body font-medium">External Docker action</p>
                <p className="text-muted-foreground">
                  Nothing in this dashboard&apos;s audit log matches this event, so it came from
                  somewhere else: a shell on this server, a CI job, or another tool holding the
                  Docker socket.
                </p>
              </>
            )}
          </HoverCardContent>
        </HoverCard>

        {event.stack && <Tag>{event.stack}</Tag>}
      </div>
    </li>
  )
}
