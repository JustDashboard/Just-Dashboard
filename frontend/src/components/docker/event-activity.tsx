"use client"

import { useMemo } from "react"
import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { cn } from "@/lib/utils"
import { duration, plural, relativeTime, timestamp } from "@/lib/format"
import {
  axisTicks,
  foldRestarts,
  outcomeOf,
  spanWords,
  type ContainerActivity,
  type EventOutcome,
} from "@/lib/docker-events"
import { hueFor, LANES } from "@/lib/hue"
import type { Container, DockerEvent } from "@/lib/types"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { rowReveal } from "@/components/icon-action"
import { ROW_BLEED } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { EventMark } from "@/components/docker/event-marks"
import { EventSource } from "@/components/docker/event-feed"
import { stateWord } from "@/components/docker/container-cells"

/**
 * Each outcome in the hue that reads it: a failure red, a restart or a check
 * that turned amber, a container coming up green, the routine quiet. Objects
 * made and removed take a tag hue (§15: colour that names a kind), so a
 * deploy's burst of creates never reads as trouble.
 */
export const OUTCOME: Record<EventOutcome, { label: string; color: string }> = {
  failed: { label: "Failed", color: "var(--destructive)" },
  unhealthy: { label: "Unhealthy", color: "var(--warning)" },
  restarted: { label: "Restarted", color: "var(--warning)" },
  started: { label: "Started", color: "var(--success)" },
  healthy: { label: "Healthy", color: "var(--success)" },
  stopped: { label: "Stopped", color: "var(--muted-foreground)" },
  changed: { label: "Created or removed", color: "var(--tag-cyan)" },
  other: { label: "Other", color: "var(--tag-slate)" },
}

/** The legend over the lanes, in the order the outcomes matter. */
const KEY: EventOutcome[] = ["failed", "restarted", "started", "stopped", "changed"]

export function OutcomeKey({ className }: { className?: string }) {
  return (
    <ul
      aria-label="What the marks mean"
      className={cn("flex flex-wrap items-center gap-x-3 gap-y-1 text-hint", className)}
    >
      {KEY.map((outcome) => (
        <li key={outcome} className="flex items-center gap-1.5 text-muted-foreground">
          <span
            aria-hidden
            className="size-1.5 rounded-full"
            style={{ background: OUTCOME[outcome].color }}
          />
          {outcome === "restarted" ? "Restarted or unhealthy" : OUTCOME[outcome].label}
        </li>
      ))}
    </ul>
  )
}

/** The window the lanes draw: `since` is where the record starts, when that is inside it. */
export type Span = { from: number; to: number; since?: number }

/** Where `time` falls across the window, as a share of the track. */
function place(time: number, span: Span) {
  return Math.min(100, Math.max(0, ((time - span.from) / (span.to - span.from)) * 100))
}

const TICK = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
})
const TICK_DAY = new Intl.DateTimeFormat(undefined, { weekday: "short" })

function tickLabel(time: number) {
  const date = new Date(time)
  return date.getHours() === 0 && date.getMinutes() === 0
    ? TICK_DAY.format(date)
    : TICK.format(date)
}

/**
 * The times across the head of the Activity column, "now" at its right end,
 * so a mark three rows down is read against the hour above it.
 */
export function LaneAxis({ span, className }: { span: Span; className?: string }) {
  const ticks = useMemo(() => axisTicks(span.from, span.to, 6), [span.from, span.to])
  return (
    <div aria-hidden className={cn("relative h-4 text-micro text-muted-foreground", className)}>
      {ticks.map((time) => {
        const left = place(time, span)
        // A label that would sit on "now" or run off the start is left out.
        if (left < 4 || left > 88) return null
        return (
          <span
            key={time}
            className="numeric absolute top-0 -translate-x-1/2 whitespace-nowrap"
            style={{ left: `${left}%` }}
          >
            {tickLabel(time)}
          </span>
        )
      })}
      <span className="absolute top-0 right-0 font-medium text-foreground">now</span>
    </div>
  )
}

/** How long a mark is new enough to breathe: it is what just happened. */
const FRESH_MS = 2 * 60_000

/**
 * One container's window as a track: each event a mark at its time in its
 * outcome's hue — failures and health that turned as dots that stand out of
 * the line, everything else as ticks — and a restart loop as a red band
 * under the marks it folds, so twenty exits in five minutes read as one
 * stretch of trouble rather than a smear. The track is dashed where the
 * dashboard was not yet listening: nothing is drawn there because nothing was
 * kept, not because nothing happened. The newest mark breathes for two
 * minutes, since it is what just happened.
 */
export function EventLane({
  events,
  span,
  now,
  label,
  className,
}: {
  events: DockerEvent[]
  span: Span
  now: number
  label: string
  className?: string
}) {
  const ticks = useMemo(() => axisTicks(span.from, span.to, 6), [span.from, span.to])
  const loops = useMemo(
    () => foldRestarts(events).filter((entry) => entry.kind === "loop"),
    [events],
  )
  const newest = events[0]
  const unrecorded =
    span.since !== undefined && span.since > span.from ? place(span.since, span) : 0
  // Drawn quietest first, so a failure is never under a start.
  const marks = [...events].sort((a, b) => weight(outcomeOf(a)) - weight(outcomeOf(b)))
  const failures = events.filter((e) => outcomeOf(e) === "failed").length

  return (
    <div
      role="img"
      aria-label={`${label}: ${plural(events.length, "event")}${
        failures ? `, ${plural(failures, "failure")}` : ""
      }`}
      className={cn("relative h-6 min-w-0", className)}
    >
      {ticks.map((time) => (
        <span
          key={time}
          aria-hidden
          className="absolute inset-y-0 w-px bg-hairline"
          style={{ left: `${place(time, span)}%` }}
        />
      ))}
      {unrecorded > 0 && (
        <span
          aria-hidden
          title="The dashboard was not listening yet"
          className="absolute top-1/2 left-0 border-t border-dashed border-muted-foreground/30"
          style={{ width: `${unrecorded}%` }}
        />
      )}
      <span
        aria-hidden
        className="absolute top-1/2 right-0 h-px bg-border"
        style={{ left: `${unrecorded}%` }}
      />
      {loops.map((loop) => {
        const left = place(Date.parse(loop.since), span)
        const right = place(Date.parse(loop.until), span)
        return (
          <span
            key={loop.key}
            aria-hidden
            title={`Restarted ×${loop.times} in ${spanWords(
              Date.parse(loop.until) - Date.parse(loop.since),
            )}`}
            className="absolute top-1/2 h-2 -translate-y-1/2 rounded-full"
            style={{
              left: `${left}%`,
              width: `max(0.375rem, ${right - left}%)`,
              background: "color-mix(in oklab, var(--destructive) 35%, transparent)",
            }}
          />
        )
      })}
      {marks.map((event, index) => (
        <Mark
          key={`${event.time}|${event.action}|${index}`}
          event={event}
          left={place(Date.parse(event.time), span)}
          fresh={event === newest && now - Date.parse(event.time) < FRESH_MS}
        />
      ))}
    </div>
  )
}

function weight(outcome: EventOutcome) {
  return outcome === "failed" ? 4 : outcome === "unhealthy" || outcome === "restarted" ? 3 : 1
}

function Mark({ event, left, fresh }: { event: DockerEvent; left: number; fresh: boolean }) {
  const outcome = outcomeOf(event)
  const color = OUTCOME[outcome].color
  const dot = outcome === "failed" || outcome === "unhealthy"
  return (
    <span
      aria-hidden
      title={`${timestamp(event.time)} — ${event.message}`}
      className="absolute top-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center justify-center"
      style={{ left: `${left}%` }}
    >
      {fresh && (
        <span
          className="absolute size-3 animate-breathe rounded-full"
          style={{ background: color }}
        />
      )}
      <span
        className={cn(
          "relative",
          dot
            ? "size-2 rounded-full ring-2 ring-background"
            : outcome === "restarted" || outcome === "started" || outcome === "changed"
              ? "h-3 w-0.5 rounded-full"
              : "h-2 w-px",
        )}
        style={{ background: color }}
      />
    </span>
  )
}

/**
 * The containers the record names, one row each, with what happened to them
 * summed and drawn across the window: the table the page used to make a
 * reader assemble from a column of sentences.
 *
 * Each row is the container as the product its image runs, with its last
 * outcome in the corner (`EventMark`); its state now, read from Docker's own
 * listing — up for how long, the exit it stopped with, or the restart loop
 * it is in, shimmering while it goes — or that it was removed; its window as
 * a lane of marks; how many of its exits failed and how many times it came
 * back; when it was last touched, and by whom. Worst first: a loop still
 * going, then a container that failed, then by when it was last touched.
 *
 * A press narrows the feed under it to that container; its name opens the
 * container's own page while Docker still lists it. Wide, from `xl`, the
 * columns are fixed and the lane takes what is left; below it the same row is
 * drawn down instead of across, its lane the row's whole width.
 */
export function ActivityTable({
  rows,
  containers,
  span,
  now,
  selected,
  onSelect,
}: {
  rows: ContainerActivity[]
  containers?: Container[]
  span: Span
  now: number
  selected: string
  onSelect: (key: string) => void
}) {
  const arrived = useArrivals(rows.map((row) => row.key))
  const wide = useMediaQuery("(min-width: 1280px)")
  const live = (row: ContainerActivity) => listed(row, containers)

  if (wide) {
    return (
      <Table className="table-fixed" containerClassName="max-h-[calc(100svh-13rem)]">
        <TableHeader className={stickyTableHeader}>
          <TableRow>
            <TableHead className="w-60">Container</TableHead>
            <TableHead className="w-44">State</TableHead>
            <TableHead>
              <span className="sr-only">Activity</span>
              <LaneAxis span={span} />
            </TableHead>
            <TableHead className="w-18 text-right">Failed</TableHead>
            <TableHead className="w-20 text-right">Restarts</TableHead>
            <TableHead className="w-24 text-right">Last</TableHead>
            <TableHead className="w-36">Who</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow
              key={row.key}
              data-state={selected === row.key ? "selected" : undefined}
              aria-selected={selected === row.key}
              className={cn("group", arrived.has(row.key) && "animate-rise")}
              onActivate={() => onSelect(row.key)}
            >
              <TableCell className="py-2">
                <Identity row={row} container={live(row)} />
              </TableCell>
              <TableCell className="py-2">
                <StateNow row={row} container={live(row)} loaded={containers !== undefined} />
              </TableCell>
              <TableCell className="py-2">
                <EventLane events={row.events} span={span} now={now} label={row.name} />
              </TableCell>
              <TableCell className="py-2 text-right">
                <Failures row={row} />
              </TableCell>
              <TableCell className="py-2 text-right">
                <Restarts row={row} />
              </TableCell>
              <TableCell
                className="numeric py-2 text-right text-hint text-muted-foreground"
                title={timestamp(row.last.time)}
              >
                {relativeTime(row.last.time)}
              </TableCell>
              <TableCell className="py-2">
                <Who row={row} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )
  }

  return (
    <div className="px-4">
      {/* One axis over every lane, where the table puts it in its head. */}
      <LaneAxis span={span} className="mt-3 ml-11" />
      <ul className="divide-y divide-hairline">
        {rows.map((row) => (
          <li
            key={row.key}
            data-state={selected === row.key ? "selected" : undefined}
            className={cn(
              "min-w-0 cursor-pointer py-3 transition-colors hover:bg-row-hover data-[state=selected]:bg-accent",
              ROW_BLEED,
              arrived.has(row.key) && "animate-rise",
            )}
            onClick={(event) => {
              if ((event.target as HTMLElement).closest("a, button")) return
              onSelect(row.key)
            }}
          >
            <div className="flex min-w-0 items-start justify-between gap-3">
              <Identity row={row} container={live(row)} />
              <span
                className="numeric shrink-0 text-hint text-muted-foreground"
                title={timestamp(row.last.time)}
              >
                {relativeTime(row.last.time)}
              </span>
            </div>
            <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 pl-11 text-hint">
              <StateNow row={row} container={live(row)} loaded={containers !== undefined} inline />
              {row.failures > 0 && (
                <span className="numeric text-destructive">{plural(row.failures, "failure")}</span>
              )}
              {row.restarts > 0 && (
                <span className={cn("numeric", row.restarts >= 3 && "text-warning")}>
                  {plural(row.restarts, "restart")}
                </span>
              )}
              <Who row={row} />
            </div>
            <EventLane
              events={row.events}
              span={span}
              now={now}
              label={row.name}
              className="mt-1.5 ml-11"
            />
          </li>
        ))}
      </ul>
    </div>
  )
}

/** The container Docker lists now under the id the events name, if it still does. */
export function listed(row: ContainerActivity, containers?: Container[]) {
  if (!containers || !row.id) return undefined
  const id = row.id
  return containers.find((c) => c.id === id || id.startsWith(c.id) || c.id.startsWith(id))
}

/**
 * The container as its product, with its last outcome in the corner, its
 * name — the way onto its own page while it exists — and its project and
 * service in the project's lane hue over its image.
 */
function Identity({ row, container }: { row: ContainerActivity; container?: Container }) {
  return (
    <div className="flex w-full min-w-0 items-center gap-3">
      <EventMark event={row.last} loop={row.looping} />
      <div className="min-w-0">
        {container ? (
          <Link
            href={`/docker/containers/${encodeURIComponent(container.id)}`}
            className="flex max-w-full min-w-0 items-center gap-1 rounded-sm text-body font-medium focus-ring hover:underline"
            title={`Open ${row.name}`}
          >
            <span className="truncate">{row.name}</span>
            <ArrowRight
              aria-hidden
              className={cn("size-3 shrink-0 text-muted-foreground", rowReveal())}
            />
          </Link>
        ) : (
          <p
            className={cn(
              "truncate text-body font-medium",
              row.removed && "text-muted-foreground line-through decoration-muted-foreground/40",
            )}
          >
            {row.name}
          </p>
        )}
        <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          {row.stack && (
            <span className="shrink-0" style={{ color: hueFor(row.stack, LANES) }}>
              {row.stack}
              {row.service && <span className="text-muted-foreground">/{row.service}</span>}
            </span>
          )}
          {row.stack && row.image && <span aria-hidden>·</span>}
          {row.image && <span className="truncate font-mono">{row.image}</span>}
        </p>
      </div>
    </div>
  )
}

/**
 * What the container is doing now, from Docker's listing rather than from
 * the record: a loop is said as one, shimmering while it goes; a container
 * that stopped says how; one the record saw removed says so; one Docker no
 * longer lists and the record never saw go says that instead.
 */
function StateNow({
  row,
  container,
  loaded,
  inline,
}: {
  row: ContainerActivity
  container?: Container
  /** Whether Docker's listing has arrived: before it, "not listed" would be a guess. */
  loaded: boolean
  inline?: boolean
}) {
  let status: React.ReactNode
  let detail: React.ReactNode = null
  const exit = row.lastExit

  if (row.looping) {
    const loop = foldRestarts(row.events)[0]
    status = <Status tone="danger" label={<TextShimmer>Restart loop</TextShimmer>} />
    if (loop?.kind === "loop") {
      detail = (
        <span className="text-destructive/80">
          ×{loop.times} in {spanWords(Date.parse(loop.until) - Date.parse(loop.since))}
          {loop.exitCode ? ` · exit ${loop.exitCode}` : ""}
        </span>
      )
    }
  } else if (container) {
    const running = container.state === "running"
    const failedExit = exit && exit.level === "error"
    status = (
      <Status
        state={container.state}
        tone={!running && failedExit ? "danger" : undefined}
        live={running}
        label={stateWord(container.state)}
      />
    )
    if (running) {
      detail = (
        <>
          {container.uptimeSeconds > 0 && `up ${duration(container.uptimeSeconds)}`}
          {container.health === "unhealthy" && (
            <span className="text-destructive"> · unhealthy</span>
          )}
        </>
      )
    } else if (exit) {
      detail = (
        <span className={cn(failedExit && "text-destructive/80")}>
          {row.oom ? "out of memory" : `exit ${exit.exitCode || "0"}`} · {relativeTime(exit.time)}
        </span>
      )
    }
  } else if (row.removed) {
    status = <Status tone="stopped" label="Removed" />
    detail = relativeTime(row.last.time)
  } else if (!loaded) {
    status = <span className="text-muted-foreground/60">—</span>
  } else {
    status = <Status tone="unknown" label="Not listed" />
  }

  if (inline) {
    return (
      <span className="inline-flex min-w-0 items-center gap-2">
        {status}
        {detail && <span className="numeric truncate text-muted-foreground">{detail}</span>}
      </span>
    )
  }
  return (
    <div className="min-w-0">
      {status}
      {detail && <p className="numeric truncate text-hint text-muted-foreground">{detail}</p>}
    </div>
  )
}

function Failures({ row }: { row: ContainerActivity }) {
  if (row.failures === 0) return <span className="font-mono text-muted-foreground/50">0</span>
  return (
    <div className="flex flex-col items-end">
      <span className="numeric font-mono font-medium text-destructive">{row.failures}</span>
      {row.oom && <Tag tone="danger">oom</Tag>}
    </div>
  )
}

function Restarts({ row }: { row: ContainerActivity }) {
  return (
    <span
      className={cn(
        "numeric font-mono",
        row.restarts === 0
          ? "text-muted-foreground/50"
          : row.looping || row.restarts >= 3
            ? "font-medium text-warning"
            : "text-foreground",
      )}
    >
      {row.restarts}
    </span>
  )
}

/**
 * Who last acted on it, with how the page knows; under it, anybody else in
 * this dashboard who did, and what nothing here explains.
 */
function Who({ row }: { row: ContainerActivity }) {
  const others = row.actors.filter((actor) => actor !== row.last.trigger?.actor)
  const outside = row.last.source === "docker" && !row.last.trigger ? row.outside - 1 : row.outside
  return (
    <div className="min-w-0">
      <EventSource event={row.last} />
      {(others.length > 0 || outside > 0) && (
        <p className="truncate text-hint text-muted-foreground">
          {others.length > 0 && `also ${others.join(", ")}`}
          {others.length > 0 && outside > 0 && " · "}
          {outside > 0 && <span className="text-warning">{outside} external</span>}
        </p>
      )}
    </div>
  )
}
