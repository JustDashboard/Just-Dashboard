"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { CheckCircle, ChevronRight, ClockRewind, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { ApiError, errorMessage, get } from "@/lib/api"
import { clock, plural, relativeTime, timestamp } from "@/lib/format"
import {
  dedupeEvents,
  eventKey,
  foldRestarts,
  healthOf,
  lastLinesSearch,
  linesBefore,
  spanWords,
  type ContainerHealth,
  type EventEntry,
} from "@/lib/docker-events"
import { dockerSource } from "@/lib/log-sources"
import { useLogView } from "@/lib/log-view"
import type { DockerEvent, DockerEventFeed, LogLine, LogSearchResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import type { ServiceLogsContext, ServiceLogsView } from "@/components/logs/service-logs"
import { LogRow, eventColumnFor } from "@/components/logs/log-console"
import { laneStyle } from "@/components/logs/log-text"
import { EventMark, EventWho } from "@/components/docker/event-marks"
import { GroupRule } from "@/components/flow"
import { InfoTip } from "@/components/form"
import { FactDot } from "@/components/metrics/host-identity"
import { PaneFooter } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { WrapDot, socketReading } from "@/components/deploy/request-marks"
import { isCleanExit } from "@/components/deploy/traffic-strip"

/** How many of the newest failures open onto their last lines by themselves. */
const OPEN_FAILURES = 3

/** Below this, "watching since" is a restart rather than a quiet afternoon. */
const RECENTLY_STARTED_MS = 60 * 60_000

/**
 * The Events view, as every page that reads a container's or a stack's logs
 * offers it — the container's own page, the stack's, and `/logs` — so the
 * view is one definition rather than one per page.
 */
export function containerEventsView(
  scope: { containerId: string; healthcheck?: boolean } | { stack: string },
): ServiceLogsView {
  return {
    id: "events",
    label: "Events",
    render: (ctx) => <ContainerEvents {...scope} ctx={ctx} />,
  }
}

/**
 * What Docker did to one container, or to every container of a stack, as a
 * view beside its logs.
 *
 * A container that prints nothing because it logs nothing and one that prints
 * nothing because the kernel killed it four minutes ago read the same in a
 * tail, so the logs' own page carries the other record: the exits, starts,
 * kills and health verdicts the dashboard kept from Docker's event stream
 * (`/docker/events?container=` or `?stack=`, followed on its socket), with
 * the health check's own last probes above them when the container has one.
 *
 * Each exit that failed opens onto the minute of output before it — the
 * program's own account of why, which the exit code only summarises — and a
 * restart loop is one row ("restarted ×17 in 12 min · exit 1") rather than
 * thirty-four, opening onto the events it folds. From either, "Open in
 * History" is the log pane itself on the minutes around it.
 */
export function ContainerEvents({
  containerId,
  stack,
  healthcheck,
  ctx,
}: {
  containerId?: string
  /** A compose project, for the stack's view: every one of its containers. */
  stack?: string
  /** Whether the container has a health check, when the page knows; unknown, it is looked for. */
  healthcheck?: boolean
  ctx: ServiceLogsContext
}) {
  const scope = useMemo(
    () => (containerId ? { container: containerId } : { stack }),
    [containerId, stack],
  )
  const wide = useMediaQuery("(min-width: 640px)")

  // Polled as well as followed: the polled copy is the one the server laid
  // against the audit log, so "this dashboard" replaces "docker itself" once
  // the poll knows better.
  const feed = usePoll<DockerEventFeed>(
    (signal) => get<DockerEventFeed>("/docker/events", { ...scope, limit: 500 }, signal),
    10_000,
    [containerId, stack],
  )
  const [live, setLive] = useState<DockerEvent[]>([])
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    setLive((prev) => [...(envelope.data as DockerEvent[])].concat(prev).slice(0, 500))
  }, [])
  const socket = useSocket("/docker/events/stream", { query: scope, onMessage })

  // Docker keeps a health check's last five probes on the container; they
  // move with its interval, so they are read again at about that pace — and
  // not at all for a container the page knows has no check, since the
  // document that holds them is the whole inspect.
  const inspect = usePoll<unknown>(
    (signal) =>
      get<unknown>(
        `/docker/containers/${encodeURIComponent(containerId ?? "")}/raw`,
        undefined,
        signal,
      ),
    30_000,
    [containerId],
    { enabled: Boolean(containerId) && healthcheck !== false },
  )
  const health = useMemo(() => healthOf(inspect.data), [inspect.data])

  const data = feed.data
  const all = useMemo(() => dedupeEvents([...(data?.events ?? []), ...live]), [data?.events, live])
  const entries = useMemo(() => foldRestarts(all), [all])
  const arrived = useArrivals(entries.map((entry) => entry.key))

  // The newest few failures open onto their last lines, and the reader's own
  // press on any row wins over that.
  const [opened, setOpened] = useState<Record<string, boolean>>({})
  const failures = useMemo(
    () =>
      new Set(
        entries
          .filter((e) => e.kind === "loop" || (e.event.action === "die" && !isCleanExit(e.event)))
          .slice(0, OPEN_FAILURES)
          .map((e) => e.key),
      ),
    [entries],
  )
  const isOpen = (key: string) => opened[key] ?? failures.has(key)
  const toggle = (key: string) => setOpened((prev) => ({ ...prev, [key]: !isOpen(key) }))

  const product = ctx.source.product
  const openAround = (event: DockerEvent) => {
    const at = Date.parse(event.time)
    ctx.openHistory({
      since: new Date(at - 5 * 60_000).toISOString(),
      until: new Date(at + 60_000).toISOString(),
      // A stack's lines carry the container they came from; the one that
      // exited is the one asked about.
      fields: stack && event.id ? { container: [event.id.slice(0, 12)] } : undefined,
    })
  }

  if (feed.error && !data) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <ErrorState error={feed.error} onRetry={feed.refresh} />
      </div>
    )
  }

  const groups = byHour(entries)

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-auto px-4 py-3 sm:px-5">
        {health && <HealthProbes health={health} />}
        {data && !data.listening && (
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
          <LoadingRows rows={5} />
        ) : entries.length === 0 ? (
          <EmptyState {...emptyReading({ since: data.since, stack: Boolean(stack) })} />
        ) : (
          <section
            aria-label={stack ? "Stack events" : "Container events"}
            className="flex flex-col gap-3"
          >
            {groups.map((group) => (
              <div key={group.key} className="min-w-0">
                <GroupRule label={group.label} count={group.entries.length} />
                <ul className="relative mt-1">
                  {/* The rail down the marks: an exit and the start a moment
                      later read as one run rather than separate rows. */}
                  <span
                    aria-hidden
                    className="absolute top-2 bottom-2 left-4 w-px -translate-x-1/2 bg-hairline"
                  />
                  {group.entries.map((entry) => (
                    <EventItem
                      key={entry.key}
                      entry={entry}
                      product={product}
                      named={Boolean(stack)}
                      wide={wide}
                      arrived={arrived.has(entry.key)}
                      open={isOpen(entry.key)}
                      onToggle={() => toggle(entry.key)}
                      onOpenHistory={openAround}
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
          <Status {...socketReading(socket.state)} className="text-hint" />
          <WrapDot />
          <span className="numeric whitespace-nowrap">{plural(all.length, "event")}</span>
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
              Docker keeps no record of what it did. The dashboard listens and keeps the recent past
              in its own memory, bounded, so a restart of the dashboard starts the record again.
            </InfoTip>
          </span>
        </PaneFooter>
      )}
    </div>
  )
}

/**
 * The health check's last probes, which Docker keeps on the container and
 * nowhere else: what the check ran, whether each run passed, how long it
 * took and what it printed — the one place "unhealthy" says why.
 */
function HealthProbes({ health }: { health: ContainerHealth }) {
  const verdict =
    health.status === "healthy" ? "ok" : health.status === "unhealthy" ? "critical" : "notice"
  return (
    <section aria-label="Health check" className="mb-4 min-w-0">
      <GroupRule label="Health check" detail={health.test} />
      <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
        <Status
          verdict={verdict}
          label={health.status.charAt(0).toUpperCase() + health.status.slice(1)}
        />
        {health.failingStreak > 0 && (
          <span className="numeric text-muted-foreground">
            {health.failingStreak === 1
              ? "the last probe failed"
              : `the last ${health.failingStreak} probes failed`}
          </span>
        )}
      </div>
      {health.probes.length > 0 ? (
        <ul className="mt-2 divide-y divide-hairline">
          {health.probes.map((probe) => {
            const passed = probe.exitCode === 0
            const took =
              probe.end !== undefined ? Date.parse(probe.end) - Date.parse(probe.start) : undefined
            return (
              <li
                key={probe.start}
                className="grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-3 gap-y-0.5 py-1.5 text-xs sm:grid-cols-[4.5rem_4.5rem_3.5rem_minmax(0,1fr)]"
              >
                <span className={cn("font-medium", passed ? "text-success" : "text-destructive")}>
                  {passed
                    ? "passed"
                    : probe.exitCode < 0
                      ? /timeout/i.test(probe.output)
                        ? "timed out"
                        : "did not run"
                      : `exit ${probe.exitCode}`}
                </span>
                <span className="numeric text-muted-foreground" title={timestamp(probe.start)}>
                  {clock(probe.start)}
                </span>
                <span className="numeric text-muted-foreground max-sm:hidden">
                  {took === undefined
                    ? "running"
                    : took < 1000
                      ? `${took} ms`
                      : `${(took / 1000).toFixed(1)} s`}
                </span>
                <span
                  className={cn(
                    "col-span-2 line-clamp-3 font-mono wrap-anywhere whitespace-pre-wrap sm:col-span-1",
                    probe.output ? "text-foreground" : "text-muted-foreground",
                  )}
                  title={probe.output || undefined}
                >
                  {probe.output || "no output"}
                </span>
              </li>
            )
          })}
        </ul>
      ) : (
        <p className="mt-2 text-hint text-muted-foreground">
          No probe has run yet — the first waits for the check&apos;s start period.
        </p>
      )}
    </section>
  )
}

/**
 * One row: an event, or a restart loop folded into one. On a phone what sits
 * at the row's edge — the exit, who, when — goes to a line under the name so
 * the sentence keeps its width, as the deployment's feed does.
 */
function EventItem({
  entry,
  product,
  named,
  wide,
  arrived,
  open,
  onToggle,
  onOpenHistory,
}: {
  entry: EventEntry
  product?: string
  /** Name the container on each row: a stack's feed is several. */
  named: boolean
  wide: boolean
  arrived: boolean
  open: boolean
  onToggle: () => void
  onOpenHistory: (event: DockerEvent) => void
}) {
  const [unfolded, setUnfolded] = useState(false)
  const loop = entry.kind === "loop" ? entry : undefined
  const event = entry.kind === "loop" ? entry.exit : entry.event
  // The kernel's note on an exit it caused; its own row only when the
  // container lived on — a child the kernel chose — and then that row
  // carries the lines as an exit's does.
  const oom = entry.kind === "event" ? entry.oom : undefined
  const exited = event.action === "die" || event.action === "oom"
  const before = loop
    ? "the minute before its last exit"
    : oom || event.action === "oom"
      ? "the minute before the kill"
      : "the minute before it exited"
  // A stack's log names a container by its service, so its events do too:
  // one name, and one lane colour, for `db` on the whole page.
  const name = event.service || event.name

  const title = loop
    ? `Restarted ×${loop.times} in ${spanWords(Date.parse(loop.until) - Date.parse(loop.since))}${
        loop.exitCode ? ` · exit ${loop.exitCode}` : ""
      }${loop.oom ? " · out of memory" : ""}`
    : (oom ?? event).message
  const edge = (
    <>
      {/* An exit code is the one fact that changes what you do next, so it
          stays on the row at every width rather than inside the sentence. */}
      {!loop && event.exitCode && event.exitCode !== "0" && (
        <Tag tone="danger" mono>
          exit {event.exitCode}
        </Tag>
      )}
      <EventWho event={event} />
      <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
        {relativeTime(loop ? loop.until : event.time)}
      </span>
    </>
  )

  return (
    <li className={cn("relative min-w-0 py-2", arrived && "animate-rise")}>
      <div className="flex min-w-0 items-start gap-3">
        <EventMark event={event} product={product} loop={Boolean(loop)} />
        <div className="min-w-0 flex-1">
          <p className="truncate text-body leading-5 font-medium">{title}</p>
          <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-hint text-muted-foreground">
            <span className="numeric" title={timestamp(loop ? loop.since : event.time)}>
              {loop ? `${clock(loop.since)}–${clock(loop.until)}` : clock(event.time)}
            </span>
            {named && name && (
              <>
                <FactDot />
                <span className="truncate font-mono" style={laneStyle(name)}>
                  {name}
                </span>
              </>
            )}
          </p>
          {!wide && <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">{edge}</div>}
          {exited && (
            <div className="mt-1 flex flex-wrap items-center gap-x-1 gap-y-1">
              {loop && (
                <Button
                  size="xs"
                  variant="ghost"
                  className="-ml-2 h-6 text-muted-foreground"
                  aria-expanded={unfolded}
                  onClick={() => setUnfolded(!unfolded)}
                >
                  <ChevronRight
                    aria-hidden
                    className={cn("size-3 transition-transform", unfolded && "rotate-90")}
                  />
                  {unfolded ? "Fold" : "Show"} the {loop.events.length} events
                </Button>
              )}
              <Button
                size="xs"
                variant="ghost"
                className={cn("h-6 text-muted-foreground", !loop && "-ml-2")}
                aria-expanded={open}
                onClick={onToggle}
              >
                <ChevronRight
                  aria-hidden
                  className={cn("size-3 transition-transform", open && "rotate-90")}
                />
                Last lines
              </Button>
              {open && <span className="text-hint text-muted-foreground">{before}</span>}
            </div>
          )}
        </div>
        {wide && <div className="flex shrink-0 items-center gap-2 self-start pt-0.5">{edge}</div>}
      </div>
      {exited && open && (
        <LastLines event={event} before={before} onOpenHistory={() => onOpenHistory(event)} />
      )}
      {loop && unfolded && (
        <ul className="mt-1 ml-11 border-l border-hairline pl-3">
          {loop.events.map((folded) => (
            <li
              key={eventKey(folded)}
              className="flex min-w-0 items-baseline gap-2 py-0.5 text-xs text-muted-foreground"
            >
              <span className="numeric shrink-0" title={timestamp(folded.time)}>
                {clock(folded.time)}
              </span>
              <span className="min-w-0 truncate text-foreground">{folded.message}</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

/**
 * The minute of output before an exit, drawn by the log console's own row so
 * it reads exactly as the pane does. Asked for only when it is shown; a
 * container that has since been removed took its output with it, and says so.
 */
function LastLines({
  event,
  before,
  onOpenHistory,
}: {
  event: DockerEvent
  /** What the minute is before, in words: "the minute before it exited". */
  before: string
  onOpenHistory: () => void
}) {
  const { wrap, highlight, time } = useLogView()
  const [state, setState] = useState<{
    lines?: LogLine[]
    /** The lens the server read them through, for their event words. */
    lens?: string
    error?: string
    gone?: boolean
  }>({})
  const source = dockerSource(event.id ?? event.name)
  useEffect(() => {
    const controller = new AbortController()
    get<LogSearchResult>(
      "/logs/search",
      { source, ...lastLinesSearch(event.time) },
      controller.signal,
    ).then(
      (result) =>
        setState({ lines: linesBefore(result.lines ?? [], event.time), lens: result.lens }),
      (err) => {
        if (controller.signal.aborted) return
        setState({
          error: errorMessage(err),
          gone: err instanceof ApiError && err.status === 404,
        })
      },
    )
    return () => controller.abort()
  }, [source, event.time])

  const lines = state.lines
  const eventColumn = lines ? eventColumnFor(lines, state.lens) : false
  return (
    <div className="mt-1.5 ml-11 animate-rise">
      {state.gone ? (
        <p className="text-hint text-muted-foreground">
          This container has been removed, and its output went with it.
        </p>
      ) : state.error ? (
        <ErrorState error={new Error(state.error)} />
      ) : !lines ? (
        <p className="text-hint text-muted-foreground">Reading what it wrote…</p>
      ) : lines.length === 0 ? (
        <p className="text-hint text-muted-foreground">It wrote nothing in {before}.</p>
      ) : (
        <div className="max-h-72 overflow-auto rounded-lg border border-hairline bg-surface-sunken py-1 font-mono text-xs leading-relaxed">
          {lines.map((line, i) => (
            <LogRow
              key={i}
              line={line}
              time={time}
              prev={lines[i - 1]?.timestamp}
              wrap={wrap}
              highlight={highlight}
              lens={state.lens}
              eventColumn={eventColumn}
              cont={Boolean(line.cont) && i > 0}
            />
          ))}
        </div>
      )}
      <Button
        size="xs"
        variant="ghost"
        className="mt-1 -ml-2 h-6 text-muted-foreground"
        onClick={onOpenHistory}
      >
        <ClockRewind className="size-3" />
        Open in History
      </Button>
    </div>
  )
}

/**
 * The entries under the hour they happened in, newest first. A day is named
 * only when the record spans more than one.
 */
function byHour(entries: EventEntry[]) {
  const timeOf = (entry: EventEntry) => (entry.kind === "loop" ? entry.until : entry.event.time)
  const hourOf = (entry: EventEntry) => {
    const date = new Date(timeOf(entry))
    date.setMinutes(0, 0, 0)
    return date
  }
  const days = new Set(entries.map((entry) => hourOf(entry).toDateString()))
  const groups: { key: string; label: string; entries: EventEntry[] }[] = []
  for (const entry of entries) {
    const hour = hourOf(entry)
    const key = hour.toISOString()
    const group = groups[groups.length - 1]
    if (group?.key === key) {
      group.entries.push(entry)
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
      entries: [entry],
    })
  }
  return groups
}

/**
 * Why there is nothing to show. The record is this process's, so a dashboard
 * that restarted a few minutes ago has not yet seen anything — which is not
 * the steadiness "nothing happened" would claim.
 */
function emptyReading({ since, stack }: { since?: string; stack: boolean }) {
  const subject = stack ? "this stack's containers" : "this container"
  const been = stack ? "have been" : "has been"
  const startedAt = since ? Date.parse(since) : NaN
  if (Number.isFinite(startedAt) && Date.now() - startedAt < RECENTLY_STARTED_MS) {
    return {
      icon: ClockRewind,
      title: "Nothing has happened since the dashboard started",
      description: `This record lives in the dashboard's own memory and begins when it starts — ${relativeTime(
        since!,
      )}. Nothing from before then was kept, so this is not yet evidence that ${subject} ${been} steady.`,
    }
  }
  return {
    icon: CheckCircle,
    title: `Nothing has happened to ${subject}`,
    description: since
      ? `Watching since ${timestamp(since)}. No start, stop, restart, exit or health change has been recorded since then.`
      : "No start, stop, restart, exit or health change has been recorded.",
  }
}
