"use client"

import { useState } from "react"
import Link from "next/link"
import { ChevronRight, Terminal } from "@/components/icons"
import { cn } from "@/lib/utils"
import { clock, relativeTime, timestamp } from "@/lib/format"
import { eventKey, spanWords, type FeedEntry } from "@/lib/docker-events"
import { hueFor, LANES } from "@/lib/hue"
import type { DockerEvent } from "@/lib/types"
import { useArrivals } from "@/hooks/use-arrivals"
import { InitialsMark } from "@/components/account/user-avatar"
import { GroupRule } from "@/components/flow"
import { ProductGlyph } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { EventMark } from "@/components/docker/event-marks"

/**
 * The record itself: every event in the window, newest first, under the hour
 * it happened in, each drawn as the thing it happened to with what happened
 * in its corner — the vocabulary a container's own Events view and a
 * deployment's feed already use (`event-marks.tsx`).
 *
 * A restart loop is one row ("restarted ×8 in 7 min · exit 1") that opens
 * onto the events it folds, where the page used to list sixteen lines of a
 * database dying and starting and push everything else off the screen, and
 * so is a container's run of events moments apart — the kill, exit, stop,
 * start and restart of one press of Restart (`foldBursts`). Every
 * row says who did it, and its hover says how that is known: Docker records
 * what happened and never who asked, so the server lays the record against
 * the audit log. A row new since the page opened rises into place.
 */
export function EventFeed({
  entries,
  onContainer,
}: {
  entries: FeedEntry[]
  /** Narrows the page to one container, from the row that names it. */
  onContainer: (key: string) => void
}) {
  // Keyed on the entries, so a loop that grows keeps its row and an event
  // the socket just delivered rises; a changed filter remounts the feed
  // rather than replaying arrivals into it.
  const arrived = useArrivals(entries.map((entry) => entry.key))
  const groups = byHour(entries)
  return (
    <section aria-label="Event feed" className="flex flex-col gap-3">
      {groups.map((group) => (
        <div key={group.key} className="min-w-0">
          <GroupRule label={group.label} count={group.entries.length} />
          <ul className="mt-1">
            {group.entries.map((entry) => (
              <FeedRow
                key={entry.key}
                entry={entry}
                arrived={arrived.has(entry.key)}
                onContainer={onContainer}
              />
            ))}
          </ul>
        </div>
      ))}
    </section>
  )
}

/** One column template for every row, so a group's times, subjects and sources line up. */
const ROW =
  "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 md:grid-cols-[4.75rem_minmax(0,1fr)_minmax(0,11rem)_minmax(0,9.5rem)]"

function FeedRow({
  entry,
  arrived,
  onContainer,
}: {
  entry: FeedEntry
  arrived: boolean
  onContainer: (key: string) => void
}) {
  const [unfolded, setUnfolded] = useState(false)
  const loop = entry.kind === "loop" ? entry : undefined
  const event =
    entry.kind === "loop" ? entry.exit : entry.kind === "burst" ? entry.lead : entry.event
  const oom = entry.kind === "loop" ? undefined : entry.oom
  const folded = entry.kind === "event" ? undefined : entry.events
  const title = loop
    ? `${event.name} restarted ×${loop.times} in ${spanWords(
        Date.parse(loop.until) - Date.parse(loop.since),
      )}`
    : (oom ?? event).message
  const time = loop ? loop.until : folded ? folded[0].time : event.time
  const exitCode = loop ? loop.exitCode : event.exitCode

  return (
    <li className={cn("min-w-0", arrived && "animate-rise")}>
      <div className={cn(ROW, "py-1.5 transition-colors hover:bg-row-hover", ROW_BLEED)}>
        <span
          className="numeric hidden font-mono text-hint text-muted-foreground md:block"
          title={timestamp(time)}
        >
          {clock(time)}
        </span>
        <div className="flex min-w-0 items-center gap-3">
          <EventMark event={event} loop={Boolean(loop)} />
          <div className="min-w-0">
            <p className="flex min-w-0 items-center gap-2">
              <span className="truncate text-body font-medium">{title}</span>
              {exitCode && exitCode !== "0" && (
                // Red only for an exit nobody asked for: a stop's 143 is its answer.
                <Tag tone={loop || event.level === "error" ? "danger" : "default"} mono>
                  exit {exitCode}
                </Tag>
              )}
              {(oom || loop?.oom) && <Tag tone="danger">out of memory</Tag>}
            </p>
            <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
              {loop ? (
                <span className="numeric" title={timestamp(loop.since)}>
                  {clock(loop.since)}–{clock(loop.until)}
                </span>
              ) : (
                <span className="numeric md:hidden" title={timestamp(time)}>
                  {clock(time)}
                </span>
              )}
              {folded && (
                <Button
                  size="xs"
                  variant="ghost"
                  className="h-5 px-1.5 text-hint text-muted-foreground"
                  aria-expanded={unfolded}
                  onClick={() => setUnfolded(!unfolded)}
                >
                  <ChevronRight
                    aria-hidden
                    className={cn("size-3 transition-transform", unfolded && "rotate-90")}
                  />
                  {unfolded ? "Fold" : "Show"} the {folded.length} events
                </Button>
              )}
              <span className="numeric max-md:hidden" title={timestamp(time)}>
                {relativeTime(time)}
              </span>
            </p>
          </div>
        </div>
        <Subject event={event} onContainer={onContainer} />
        <div className="col-span-2 flex min-w-0 pl-11 md:col-span-1 md:pl-0">
          <EventSource event={event} />
        </div>
      </div>
      {folded && unfolded && (
        <ul className="mb-1 ml-11 border-l border-hairline pl-3 md:ml-[7.75rem]">
          {folded.map((member) => (
            <li
              key={eventKey(member)}
              className="flex min-w-0 items-baseline gap-2 py-0.5 text-xs text-muted-foreground"
            >
              <span className="numeric shrink-0 font-mono" title={timestamp(member.time)}>
                {clock(member.time)}
              </span>
              <span className="min-w-0 truncate text-foreground">{member.message}</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

/**
 * What it happened to, past the sentence: a container's compose project and
 * service in the project's lane hue, a button that narrows the page to it;
 * an image, a network or a volume by its own name.
 */
function Subject({
  event,
  onContainer,
}: {
  event: DockerEvent
  onContainer: (key: string) => void
}) {
  if (event.type !== "container") {
    return (
      <span className="hidden min-w-0 truncate font-mono text-hint text-muted-foreground md:block">
        {event.type}
      </span>
    )
  }
  const key = event.id || event.name
  return (
    <button
      type="button"
      onClick={() => onContainer(key)}
      title={`Only ${event.name}'s events`}
      className="hidden min-w-0 items-center gap-1.5 rounded-sm text-left text-hint focus-ring hover:underline md:flex"
    >
      {event.stack ? (
        <>
          <span className="truncate" style={{ color: hueFor(event.stack, LANES) }}>
            {event.stack}
          </span>
          {event.service && (
            <span className="truncate font-mono text-muted-foreground">/{event.service}</span>
          )}
        </>
      ) : (
        <span className="truncate font-mono text-muted-foreground">{event.name}</span>
      )}
    </button>
  )
}

/**
 * Where an event came from, as a mark and a word: the person whose press the
 * audit log matched, compose, Docker on its own, or something outside this
 * dashboard — a shell on the host, a CI job, another tool holding the socket.
 * "Docker removed a container" and "somebody here removed a container" are
 * the same event and different news, and the second is the one that stops an
 * operator hunting for an intruder. The hover says how each is known.
 */
export function EventSource({ event, className }: { event: DockerEvent; className?: string }) {
  const actor = event.trigger?.actor || "an unnamed session"
  const label = event.trigger ? (
    <>
      <InitialsMark name={event.trigger.actor || "?"} size="xs" />
      <span className="truncate text-foreground">{event.trigger.actor || "this dashboard"}</span>
    </>
  ) : event.source === "compose" ? (
    <>
      <ProductGlyph id="docker-compose" />
      <span className="truncate text-muted-foreground">Compose</span>
    </>
  ) : event.source === "daemon" ? (
    <>
      <ProductGlyph id="docker" />
      <span className="truncate text-muted-foreground">Docker itself</span>
    </>
  ) : (
    <>
      <Terminal aria-hidden className="size-3.5 shrink-0 text-warning" />
      <span className="truncate text-warning">External</span>
    </>
  )

  return (
    <HoverCard openDelay={150}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          aria-label="Where this event came from"
          className={cn(
            "flex max-w-full min-w-0 cursor-help items-center gap-1.5 rounded-sm text-xs whitespace-nowrap focus-ring",
            className,
          )}
        >
          {label}
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-80 space-y-1.5 text-xs leading-relaxed">
        {event.trigger ? (
          <>
            <p className="text-body font-medium">Triggered from this dashboard</p>
            <p className="text-muted-foreground">
              An audit entry for <b>{event.trigger.action}</b> by <b>{actor}</b> names the same
              object within a minute of this event. That is a likely cause rather than a recorded
              one — Docker does not say who asked.
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
              project. Nothing in the audit log matches it, so it was run from a shell rather than
              from here.
            </p>
          </>
        ) : event.source === "daemon" ? (
          <>
            <p className="text-body font-medium">Docker did this on its own</p>
            <p className="text-muted-foreground">
              Nobody asked for it — a restart policy firing, a health check changing verdict, or the
              kernel stopping a container.
            </p>
          </>
        ) : (
          <>
            <p className="text-body font-medium">External Docker action</p>
            <p className="text-muted-foreground">
              Nothing in this dashboard&apos;s audit log matches this event, so it came from
              somewhere else: a shell on this server, a CI job, or another tool holding the Docker
              socket.
            </p>
          </>
        )}
      </HoverCardContent>
    </HoverCard>
  )
}

/**
 * The entries under the hour they happened in, newest first. The day is
 * named on the hour that starts it, and only when the record spans more than
 * one: nine tenths of a feed is today, and "Today" on every group is noise.
 */
function byHour(entries: FeedEntry[]) {
  const timeOf = (entry: FeedEntry) =>
    entry.kind === "loop"
      ? entry.until
      : entry.kind === "burst"
        ? entry.events[0].time
        : entry.event.time
  const hourOf = (entry: FeedEntry) => {
    const date = new Date(timeOf(entry))
    date.setMinutes(0, 0, 0)
    return date
  }
  const days = new Set(entries.map((entry) => hourOf(entry).toDateString()))
  const today = new Date().toDateString()
  const yesterday = new Date(Date.now() - 86_400_000).toDateString()
  const groups: { key: string; label: string; entries: FeedEntry[] }[] = []
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
    const day = hour.toDateString()
    const dayName =
      day === today
        ? "Today"
        : day === yesterday
          ? "Yesterday"
          : hour.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" })
    groups.push({ key, label: days.size > 1 ? `${dayName} · ${time}` : time, entries: [entry] })
  }
  return groups
}
