"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import {
  Box,
  CheckCircle,
  Clock,
  ClockRewind,
  Cross,
  CrossCircle,
  Filter,
  Heart,
  Link as LinkGlyph,
  MagnifyingGlass,
  NetworkDevice,
  Pause,
  Play,
  Plus,
  RotateClockwise,
  Slash,
  Stop,
  StopCircle,
  Trash,
  Warning,
  type Icon,
} from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { clock, plural, relativeTime, timestamp } from "@/lib/format"
import type { DeploymentLifecycle, DockerEvent } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { InitialsMark } from "@/components/account/user-avatar"
import { GroupRule } from "@/components/flow"
import { laneStyle } from "@/components/logs/log-text"
import { FactDot } from "@/components/metrics/host-identity"
import { PaneFooter } from "@/components/panel"
import { ProductGlyph, ProductLogo, imageProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { InfoTip } from "@/components/form"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { LiveDot, WrapDot, socketReading } from "@/components/deploy/request-marks"
import { isCleanExit } from "@/components/deploy/traffic-strip"

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
 * kept is a bounded ring in this process's memory, which the footer names
 * rather than leaves the reader to infer: a restart of the dashboard empties
 * it, and the record starts again from there.
 *
 * Each event is drawn as the thing it happened to — the container as the
 * product it runs (the deployment's own mark, or the image's when it names
 * one), a network on the same tile with a network glyph — with what happened
 * as a glyph in the tile's corner in its tone, the way a session's system
 * sits on its browser (§14). The container's name takes a hue by name
 * (`LANES`), so release 20's and release 21's containers can be told apart
 * down the feed as the log console tells processes apart; and who did it is a
 * face or a mark and a word — the person whose press the audit log matched,
 * or Docker acting on its own.
 *
 * Grouped under the hour, on a rail down the marks, so an exit and the start a
 * minute after it read as one incident rather than two rows.
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
  product,
  moment,
  onClearMoment,
}: {
  projectId: number
  /** What the deployment is, for a container event whose image names nothing better. */
  product?: string
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
  const wide = useMediaQuery("(min-width: 640px)")

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
  const all = useMemo(() => dedupe([...(data?.events ?? []), ...live]), [data?.events, live])

  const needle = search.trim().toLowerCase()
  const wanted = new Set(kinds)
  const at = moment ? Date.parse(moment) : NaN
  // Two steps, so the kind chips count what the search and the moment leave:
  // counted over everything, a bar reading "All 4" sat over an empty moment
  // and a footer saying "0 events".
  const scoped = all.filter(
    (event) =>
      (!needle || `${event.message} ${event.name}`.toLowerCase().includes(needle)) &&
      (!Number.isFinite(at) || Math.abs(Date.parse(event.time) - at) <= 2 * 60_000),
  )
  const shown = scoped.filter((event) => wanted.size === 0 || wanted.has(event.type))
  // Over the whole record, not what the filters show: clearing a search or a
  // chip re-shows rows that were already here, and they must not rise as
  // though they had just arrived (§11).
  const arrived = useArrivals(all.map(eventKey))

  if (feed.error && !data) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <ErrorState error={feed.error} onRetry={feed.refresh} />
      </div>
    )
  }
  if (data && data.status !== "available") {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState
          icon={Slash}
          title="Container events are unavailable"
          description={data.reason}
        />
      </div>
    )
  }

  const filtered = needle !== "" || wanted.size > 0
  const groups = byHour(shown)

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-hairline px-2 py-1.5">
        <InputGroup className="h-10 basis-full sm:h-8 lg:max-w-md lg:min-w-64 lg:flex-1 lg:basis-0">
          <InputGroupAddon className="border-r-0 pr-0">
            <MagnifyingGlass />
          </InputGroupAddon>
          <InputGroupInput
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter — exited, unhealthy, a name"
            aria-label="Filter container events"
            className="font-mono placeholder:font-sans sm:text-xs"
          />
        </InputGroup>

        <ChipStrip className="scroll-affordance flex-1 max-sm:-mx-2 max-sm:px-2">
          {/* The moment a failing request sent the reader here is a scope,
              and a scope is a filter: a chosen chip with its way back — first,
              because it is the one narrowing the reader did not choose here. */}
          {moment && onClearMoment && (
            <FilterChip
              selected
              onClick={onClearMoment}
              aria-label={`Show everything, not only the two minutes around ${clock(moment)}`}
              title="Show everything"
            >
              <ClockRewind aria-hidden className="size-3 text-muted-foreground" />
              <span className="numeric">{clock(moment)} ±2 min</span>
              <Cross aria-hidden className="size-3 text-muted-foreground" />
            </FilterChip>
          )}
          <FilterChip selected={kinds.length === 0} onClick={() => setKinds([])}>
            All
            <ChipCount>{scoped.length}</ChipCount>
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
                <ChipCount>{scoped.filter((event) => event.type === kind.id).length}</ChipCount>
              </FilterChip>
            )
          })}
        </ChipStrip>

        {/* The dot is a claim about the socket (§11), in the tones the
            footer's words take. On a phone the chips scroll beside it, so it
            keeps a column of its own past a rule, as the request console's
            controls do — the chip cut off at the edge is the affordance, and
            it never runs under Live. */}
        <div className="ml-auto flex shrink-0 items-center max-sm:border-l max-sm:border-hairline max-sm:pl-1.5">
          <FilterChip
            selected={following}
            onClick={() => setFollowing(!following)}
            className="shrink-0"
            title="Follow the daemon's event stream, rather than waiting for the next poll"
          >
            {following && <LiveDot state={socket.state} />}
            Live
          </FilterChip>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-5 py-3">
        {moment && (
          <p className="mb-3 text-hint text-muted-foreground">
            Around <span className="numeric text-foreground">{timestamp(moment)}</span> — two
            minutes either side.
          </p>
        )}
        {data && !data.watching && (
          <Notice
            tone="warning"
            icon={Warning}
            title="The Docker event stream is not connected"
            className="mb-3"
          >
            This list is whatever was recorded before the connection dropped, and it stops here.
          </Notice>
        )}
        {!data ? (
          <LoadingRows rows={6} />
        ) : shown.length === 0 ? (
          <EmptyState {...emptyReading({ filtered, moment, since: data.since })} />
        ) : (
          <section aria-label="Container events" className="flex flex-col gap-3">
            {groups.map((group) => (
              <div key={group.key} className="min-w-0">
                <GroupRule label={group.label} count={group.events.length} />
                <ul className="relative mt-1">
                  {/* The rail down the marks: what happened within the hour
                      reads as one run rather than separate rows. */}
                  <span
                    aria-hidden
                    className="absolute top-2 bottom-2 left-4 w-px -translate-x-1/2 bg-hairline"
                  />
                  {group.events.map((event) => (
                    <LifecycleRow
                      key={eventKey(event)}
                      event={event}
                      projectId={projectId}
                      product={product}
                      wide={wide}
                      arrived={arrived.has(eventKey(event))}
                    />
                  ))}
                </ul>
              </div>
            ))}
          </section>
        )}
      </div>

      {data && (
        <PaneFooter className="gap-x-3 gap-y-1 px-3 text-hint text-muted-foreground sm:gap-x-2">
          {following ? (
            <Status {...socketReading(socket.state)} className="text-hint" />
          ) : (
            <Status tone="stopped" label="Polling" className="text-hint" />
          )}
          <WrapDot />
          <span className="numeric whitespace-nowrap">{plural(shown.length, "event")}</span>
          {data.since && (
            <>
              <WrapDot />
              <span className="whitespace-nowrap" title={timestamp(data.since)}>
                watching since <span className="numeric">{clock(data.since)}</span>
              </span>
            </>
          )}
          <WrapDot />
          <span className="flex items-center gap-1 whitespace-nowrap">
            kept in memory
            <InfoTip label="How long this record is kept">
              Kept in this process&apos;s memory and bounded, so this is the recent past rather than
              a permanent record — a restart of the dashboard starts it again. Everything the
              dashboard itself did is in the audit log, which survives one.
            </InfoTip>
          </span>
        </PaneFooter>
      )}
    </div>
  )
}

function eventKey(event: DockerEvent) {
  return `${event.time}|${event.type}|${event.action}|${event.id ?? event.name}`
}

/**
 * The events under the hour they happened in, newest first. A day is named
 * only when the record spans more than one, so a morning's feed reads "11:00"
 * rather than a date the reader already knows.
 */
function byHour(events: DockerEvent[]) {
  const hourOf = (event: DockerEvent) => {
    const date = new Date(event.time)
    date.setMinutes(0, 0, 0)
    return date
  }
  const days = new Set(events.map((event) => hourOf(event).toDateString()))
  const groups: { key: string; label: string; events: DockerEvent[] }[] = []
  for (const event of events) {
    const hour = hourOf(event)
    const key = hour.toISOString()
    const group = groups[groups.length - 1]
    if (group?.key === key) {
      group.events.push(event)
      continue
    }
    const time = hour.toLocaleTimeString(undefined, {
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    })
    groups.push({
      key,
      label:
        days.size > 1
          ? `${hour.toLocaleDateString(undefined, { month: "short", day: "numeric" })} · ${time}`
          : time,
      events: [event],
    })
  }
  return groups
}

/**
 * Why there is nothing to show, which is four different pieces of news, each
 * with the mark that says it: a steady record, a record too young to be
 * evidence, a moment with nothing in it, and a filter that matched nothing.
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
}): { title: string; description: string; icon: Icon } {
  if (filtered) {
    return {
      icon: Filter,
      title: "Nothing matches that filter",
      description:
        "No recorded event carries that text or belongs to that kind. Clear the filter to see the whole record.",
    }
  }
  if (moment) {
    return {
      icon: Clock,
      title: "Nothing happened to the container then",
      description:
        "No exit, restart, OOM kill or health change within two minutes of that request. Whatever failed, it was not the container's life.",
    }
  }
  const startedAt = since ? Date.parse(since) : NaN
  if (Number.isFinite(startedAt) && Date.now() - startedAt < RECENTLY_STARTED_MS) {
    return {
      icon: ClockRewind,
      title: "Nothing has happened since the dashboard started",
      description: `This record lives in the dashboard's own memory and begins when it starts — which was ${relativeTime(
        since!,
      )}. Nothing from before then was kept, so this is not yet evidence that the deployment has been steady.`,
    }
  }
  return {
    icon: CheckCircle,
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
    const key = eventKey(event)
    if (seen.has(key)) continue
    seen.add(key)
    out.push(event)
  }
  return out.sort((a, b) => b.time.localeCompare(a.time))
}

/**
 * What happened, as the glyph in the corner of the thing it happened to, in
 * the tone of a reading of state: an exit that failed in red, a restart in
 * amber, a start or a passing health check in green, and the bookkeeping —
 * created, removed, connected — quiet. A clean exit and a kill are
 * bookkeeping too: every stop sends both, and the server calls them notices.
 */
const HAPPENED: Record<string, [Icon, string]> = {
  die: [CrossCircle, "text-destructive"],
  oom: [CrossCircle, "text-destructive"],
  kill: [Stop, "text-muted-foreground"],
  restart: [RotateClockwise, "text-warning"],
  start: [Play, "text-success"],
  unpause: [Play, "text-success"],
  healthy: [Heart, "text-success"],
  unhealthy: [Heart, "text-warning"],
  stop: [StopCircle, "text-muted-foreground"],
  pause: [Pause, "text-muted-foreground"],
  create: [Plus, "text-muted-foreground"],
  destroy: [Trash, "text-muted-foreground"],
  connect: [LinkGlyph, "text-muted-foreground"],
  disconnect: [LinkGlyph, "text-muted-foreground"],
}

function happened(event: DockerEvent) {
  if (isCleanExit(event)) return HAPPENED.stop
  if (event.action.startsWith("health_status")) {
    return HAPPENED[event.action.endsWith("unhealthy") ? "unhealthy" : "healthy"]
  }
  return HAPPENED[event.action]
}

/**
 * A container is the product it runs: the image's own mark when its reference
 * names one (an n8n or Grafana deployment), otherwise the deployment's — a
 * built image is named after the project, which no logo is. A network keeps
 * a glyph on the same tile, so the titles line up.
 */
function EventMark({ event, product }: { event: DockerEvent; product?: string }) {
  const named = event.image ? imageProduct(event.image) : undefined
  const id =
    event.type === "container"
      ? named && named !== "docker"
        ? named
        : (product ?? "docker")
      : undefined
  const badge = happened(event)
  const Glyph = badge?.[0]
  return (
    <span className="relative z-10 flex shrink-0">
      <ProductLogo id={id} size="sm" fallback={event.type === "network" ? NetworkDevice : Box} />
      {Glyph && (
        <span
          aria-hidden
          className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background"
        >
          <Glyph className={cn("size-2.5", badge[1])} />
        </span>
      )}
    </span>
  )
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
 *
 * On a phone what sits at the row's right edge — the exit code, who, when —
 * goes to a third line under the name instead, so the sentence keeps the
 * width it needs; chosen once, as `JobCard` chooses its last run's place.
 */
function LifecycleRow({
  event,
  projectId,
  product,
  wide,
  arrived,
}: {
  event: DockerEvent
  projectId: number
  product?: string
  wide: boolean
  arrived: boolean
}) {
  const release = event.owner?.["release-number"] ?? event.owner?.["release-id"]
  const runId = event.owner?.["run-id"]
  const edge = (
    <>
      {/* An exit code is the one fact that changes what you do next, so it
          stays on the row at every width rather than inside the sentence. */}
      {event.exitCode && event.exitCode !== "0" && (
        <Tag tone="danger" mono>
          exit {event.exitCode}
        </Tag>
      )}
      {event.trigger ? (
        <span className="flex shrink-0 items-center gap-1.5">
          <InitialsMark name={event.trigger.actor || "?"} size="xs" />
          <Link
            href={`/audit?action=${encodeURIComponent(event.trigger.action)}`}
            className="rounded-sm text-xs whitespace-nowrap text-foreground focus-ring hover:underline"
            title={`Audit entry ${event.trigger.auditId} — ${event.trigger.action} by ${
              event.trigger.actor || "an unnamed session"
            }. A name and a window, so a likely cause rather than a recorded one.`}
          >
            this dashboard
          </Link>
        </span>
      ) : event.source === "daemon" ? (
        // The same slot as "this dashboard", at the same rank: both answer
        // who did it. A small-caps tag is for a fixed property of the row.
        <span className="flex shrink-0 items-center gap-1.5">
          <ProductGlyph id="docker" />
          <span
            className="text-xs whitespace-nowrap text-muted-foreground"
            title="Docker acted on its own: a restart policy firing, or the OOM reaper."
          >
            docker itself
          </span>
        </span>
      ) : null}
      <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
        {relativeTime(event.time)}
      </span>
    </>
  )
  return (
    <li className={cn("relative flex min-w-0 items-start gap-3 py-2", arrived && "animate-rise")}>
      <EventMark event={event} product={product} />
      <div className="min-w-0 flex-1">
        <p className="truncate text-body leading-5 font-medium">{event.message}</p>
        <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-hint text-muted-foreground">
          <span className="numeric" title={timestamp(event.time)}>
            {clock(event.time)}
          </span>
          {event.name && (
            <>
              <FactDot />
              <span className="truncate font-mono" style={laneStyle(event.name)}>
                {event.name}
              </span>
            </>
          )}
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
        </p>
        {!wide && <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">{edge}</div>}
      </div>
      {wide && <div className="flex shrink-0 items-center gap-2 self-center">{edge}</div>}
    </li>
  )
}
