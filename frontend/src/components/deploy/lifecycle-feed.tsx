"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import {
  CheckCircle,
  ChevronDown,
  Clock,
  ClockRewind,
  Cross,
  Filter,
  MagnifyingGlass,
  Slash,
  TerminalWindow,
  Warning,
  type Icon,
} from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { clock, plural, relativeTime, timestamp } from "@/lib/format"
import { dedupeEvents, eventKey } from "@/lib/docker-events"
import { dockerSource } from "@/lib/log-sources"
import type { DeploymentLifecycle, DockerEvent } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { GroupRule } from "@/components/flow"
import { laneStyle } from "@/components/logs/log-text"
import { FactDot } from "@/components/metrics/host-identity"
import { PaneFooter } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { InfoTip } from "@/components/form"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { LiveDot, WrapDot, socketReading } from "@/components/deploy/request-marks"
import { isCleanExit } from "@/components/deploy/traffic-strip"
import { EventMark, EventWho, HAPPENED } from "@/components/docker/event-marks"
import { LinesBlock, OutputLines } from "@/components/deploy/output-lines"
import { foldRestarts, loopSpan, type FeedItem } from "@/components/deploy/logs-model"

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
 * minute after it read as one incident rather than two rows — and a crash
 * loop, the same exit and the same start over and over, as the one row it is
 * ("restarted ×12 in 9 min · exit 1"), which unfolds. An exit or an OOM kill
 * carries what the container printed in the minute before it, a press away
 * and open on the newest failure: the reason it died is almost always its
 * own last words, and they are the one thing Docker's event does not say.
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
  outputFor,
  exists,
}: {
  projectId: number
  /** What the deployment is, for a container event whose image names nothing better. */
  product?: string
  /** An instant to look around — a failing request's — scoping the list to ±2 minutes. */
  moment?: string
  onClearMoment?: () => void
  /** The page's Output view on this event's container at its moment, where the page has it. */
  outputFor?: (event: DockerEvent) => (() => void) | undefined
  /**
   * Whether the event's container is still there; absent while that is not
   * known. A removed container's output went with it, so its last lines are
   * said to be gone rather than asked for and refused.
   */
  exists?: (event: DockerEvent) => boolean
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
  const all = useMemo(() => dedupeEvents([...(data?.events ?? []), ...live]), [data?.events, live])

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
  const items = foldRestarts(shown, eventKey)
  const groups = byHour(items)
  // The newest failure opens on its last words; the rest are a press away,
  // so a feed of forty exits is not forty reads of the containers' logs.
  const newestFailure = items.find(
    (item) =>
      (item.kind === "loop" && !item.clean) ||
      (item.kind === "event" &&
        item.event.type === "container" &&
        (item.event.action === "oom" || (item.event.action === "die" && !isCleanExit(item.event)))),
  )?.key

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
                <GroupRule label={group.label} count={group.count} />
                <ul className="relative mt-1">
                  {/* The rail down the marks: what happened within the hour
                      reads as one run rather than separate rows. */}
                  <span
                    aria-hidden
                    className="absolute top-2 bottom-2 left-4 w-px -translate-x-1/2 bg-hairline"
                  />
                  {group.items.map((item) =>
                    item.kind === "loop" ? (
                      <LoopRow
                        key={item.key}
                        loop={item}
                        projectId={projectId}
                        product={product}
                        wide={wide}
                        arrived={arrived.has(eventKey(item.events[0]))}
                        lastLines={item.key === newestFailure}
                        onOutput={outputFor?.(item.exit)}
                        gone={exists?.(item.exit) === false}
                      />
                    ) : (
                      <LifecycleRow
                        key={item.key}
                        event={item.event}
                        projectId={projectId}
                        product={product}
                        wide={wide}
                        arrived={arrived.has(item.key)}
                        lastLines={item.key === newestFailure}
                        onOutput={outputFor?.(item.event)}
                        gone={exists?.(item.event) === false}
                      />
                    ),
                  )}
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

/**
 * The rows under the hour they happened in, newest first — a crash loop under
 * the hour of its newest restart, counted as the events it holds. A day is
 * named only when the record spans more than one, so a morning's feed reads
 * "11:00" rather than a date the reader already knows.
 */
function byHour(items: FeedItem[]) {
  const hourOf = (item: FeedItem) => {
    const date = new Date(item.time)
    date.setMinutes(0, 0, 0)
    return date
  }
  const size = (item: FeedItem) => (item.kind === "loop" ? item.events.length : 1)
  const days = new Set(items.map((item) => hourOf(item).toDateString()))
  const groups: { key: string; label: string; count: number; items: FeedItem[] }[] = []
  for (const item of items) {
    const hour = hourOf(item)
    const key = hour.toISOString()
    const group = groups[groups.length - 1]
    if (group?.key === key) {
      group.items.push(item)
      group.count += size(item)
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
      count: size(item),
      items: [item],
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
 *
 * An exit or an OOM kill has a "Last lines" fold: what the container printed
 * in the minute before, read when it opens — offered only while the container
 * is there to read. Once it is removed, the newest failure says so in the
 * fold's place and the others offer nothing: a press that can only fail is
 * not a verb.
 */
function LifecycleRow({
  event,
  projectId,
  product,
  wide,
  arrived,
  lastLines,
  onOutput,
  gone,
}: {
  event: DockerEvent
  projectId: number
  product?: string
  wide: boolean
  arrived: boolean
  /** Open on the last lines: the newest failure in the feed. */
  lastLines?: boolean
  onOutput?: () => void
  /** The container has been removed, and what it printed with it. */
  gone?: boolean
}) {
  const [reading, setReading] = useState(Boolean(lastLines))
  const release = event.owner?.["release-number"] ?? event.owner?.["release-id"]
  const runId = event.owner?.["run-id"]
  const stopped = event.type === "container" && (event.action === "die" || event.action === "oom")
  const readable = stopped && Boolean(event.id) && !gone
  const edge = (
    <>
      {readable && <LastLinesToggle open={reading} onToggle={() => setReading(!reading)} />}
      {/* An exit code is the one fact that changes what you do next, so it
          stays on the row at every width rather than inside the sentence. */}
      {event.exitCode && event.exitCode !== "0" && (
        <Tag tone="danger" mono>
          exit {event.exitCode}
        </Tag>
      )}
      <EventWho event={event} />
      <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
        {relativeTime(event.time)}
      </span>
    </>
  )
  return (
    <li className={cn("relative flex min-w-0 items-start gap-3 py-2", arrived && "animate-rise")}>
      <EventMark event={event} product={product} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-start gap-3">
          <div className="min-w-0 flex-1">
            <p className="truncate text-body leading-5 font-medium">{event.message}</p>
            <EventFacts event={event} projectId={projectId} release={release} runId={runId} />
          </div>
          {wide && <div className="flex shrink-0 items-center gap-2 self-center">{edge}</div>}
        </div>
        {!wide && <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">{edge}</div>}
        {readable && reading && <LastLines event={event} onOutput={onOutput} />}
        {stopped && gone && lastLines && <LinesGone />}
      </div>
    </li>
  )
}

/** When, which container in its hue, and the release it belongs to as a link to its run. */
function EventFacts({
  event,
  projectId,
  release,
  runId,
}: {
  event: DockerEvent
  projectId: number
  release?: string
  runId?: string
}) {
  return (
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
  )
}

function LastLinesToggle({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  return (
    <Button
      size="xs"
      variant="ghost"
      aria-expanded={open}
      className="h-6 shrink-0 px-1.5 text-hint"
      onClick={onToggle}
    >
      <TerminalWindow className="size-3" />
      Last lines
      <ChevronDown className={cn("size-3 transition-transform", open && "rotate-180")} />
    </Button>
  )
}

/**
 * What a container printed in the minute before it stopped, through its own
 * lens: the stack trace, the "out of memory", the port somebody else held.
 */
function LastLines({ event, onOutput }: { event: DockerEvent; onOutput?: () => void }) {
  const at = Date.parse(event.time)
  if (!event.id || !Number.isFinite(at)) return null
  return (
    <OutputLines
      title="Last lines"
      facts={<span className="numeric">the minute before {clock(event.time)}</span>}
      query={{
        source: dockerSource(event.id),
        since: new Date(at - 60_000).toISOString(),
        // The exit itself, as the event wrote it: the next attempt starts up
        // a moment later and is not why it stopped.
        until: event.time,
        limit: 20,
      }}
      empty="The container wrote nothing in the minute before it stopped."
      action={
        onOutput && (
          <Button size="xs" variant="ghost" className="h-6 px-1.5 text-hint" onClick={onOutput}>
            Open in Output
          </Button>
        )
      }
    />
  )
}

/** Where the last lines would be, for a container that has been removed. */
function LinesGone() {
  return (
    <LinesBlock title="Last lines">
      <p className="text-hint text-muted-foreground">
        This container has since been removed, and what it printed went with it.
      </p>
    </LinesBlock>
  )
}

/** A loop's mark: the restart in amber when it crashes, quiet when each run ended cleanly. */
const LOOP_BADGE: Record<"failing" | "clean", [Icon, string]> = {
  failing: HAPPENED.restart,
  clean: [HAPPENED.restart[0], "text-muted-foreground"],
}

/**
 * A crash loop, folded: the container, how many times it was started again,
 * over how long, and the exit it kept making — with the loop's own events a
 * press away and the last lines before its newest exit, which are the lines
 * that say why it keeps dying. A loop of clean exits — a job its restart
 * policy runs again — is said as one, in no failure's tone.
 */
function LoopRow({
  loop,
  projectId,
  product,
  wide,
  arrived,
  lastLines,
  onOutput,
  gone,
}: {
  loop: Extract<FeedItem, { kind: "loop" }>
  projectId: number
  product?: string
  wide: boolean
  arrived: boolean
  lastLines?: boolean
  onOutput?: () => void
  gone?: boolean
}) {
  const [reading, setReading] = useState(Boolean(lastLines))
  const [unfolded, setUnfolded] = useState(false)
  const readable = Boolean(loop.exit.id) && !gone
  const newest = loop.events[0]
  const release = newest.owner?.["release-number"] ?? newest.owner?.["release-id"]
  const runId = newest.owner?.["run-id"]
  const edge = (
    <>
      {readable && <LastLinesToggle open={reading} onToggle={() => setReading(!reading)} />}
      <Button
        size="xs"
        variant="ghost"
        aria-expanded={unfolded}
        className="h-6 shrink-0 px-1.5 text-hint"
        onClick={() => setUnfolded(!unfolded)}
      >
        {plural(loop.events.length, "event")}
        <ChevronDown className={cn("size-3 transition-transform", unfolded && "rotate-180")} />
      </Button>
      {/* A restart policy is Docker acting on its own, which is the whole
          story of a loop nobody pressed anything for. */}
      <EventWho event={newest} />
      <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
        {relativeTime(newest.time)}
      </span>
    </>
  )
  return (
    <li className={cn("relative flex min-w-0 items-start gap-3 py-2", arrived && "animate-rise")}>
      <EventMark
        event={newest}
        product={product}
        badge={LOOP_BADGE[loop.clean ? "clean" : "failing"]}
      />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-start gap-3">
          <div className="min-w-0 flex-1">
            <p className="truncate text-body leading-5 font-medium">
              Restarted ×{loop.restarts} {loopSpan(loop.spanMs)}
              {loop.oom && <span className="text-destructive"> · OOM-killed</span>}
              {loop.exitCode && (
                <span className="numeric text-destructive"> · exit {loop.exitCode}</span>
              )}
              {loop.clean && <span className="text-muted-foreground"> · each exit clean</span>}
            </p>
            <EventFacts event={newest} projectId={projectId} release={release} runId={runId} />
          </div>
          {wide && <div className="flex shrink-0 items-center gap-2 self-center">{edge}</div>}
        </div>
        {!wide && <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">{edge}</div>}
        {readable && reading && <LastLines event={loop.exit} onOutput={onOutput} />}
        {gone && lastLines && <LinesGone />}
        {unfolded && (
          <ul aria-label="The loop's events" className="mt-2 border-l border-hairline pl-3">
            {loop.events.map((event) => (
              <li
                key={eventKey(event)}
                className="flex min-w-0 items-center gap-2 py-0.5 text-hint text-muted-foreground"
              >
                <span className="numeric shrink-0" title={timestamp(event.time)}>
                  {clock(event.time)}
                </span>
                <span className="truncate text-foreground">{event.message}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </li>
  )
}
