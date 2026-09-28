import type { PortEvent, SeenListener } from "@/lib/types"
import { clockMinute, duration } from "@/lib/format"
import { foldDualStack, reachGroup, type ReachGroup } from "@/components/proxy/ports"

/** How long a socket reads as new once the history first saw it listening. */
export const NEW_FOR_MS = 24 * 3_600_000

/** A row of the list, dated: one socket, or a service's folded pair. */
export type DatedSocket = Partial<SeenListener> & { twin?: Partial<SeenListener> }

/**
 * When a row was first seen listening: the later of a folded pair's, since a
 * service that began answering in a second family today changed today.
 * Undefined where neither was seen opening.
 */
export function firstSeen(socket: DatedSocket): string | undefined {
  let latest: string | undefined
  for (const seen of [socket.firstSeen, socket.twin?.firstSeen]) {
    if (
      seen &&
      !Number.isNaN(Date.parse(seen)) &&
      (!latest || Date.parse(seen) > Date.parse(latest))
    ) {
      latest = seen
    }
  }
  return latest
}

/** A row first seen listening in the day before `now`. */
export function isNew(socket: DatedSocket, now: number): boolean {
  const seen = firstSeen(socket)
  return seen !== undefined && now - Date.parse(seen) < NEW_FOR_MS
}

/** "at 14:02", or "yesterday at 21:10": when, within the last day, a socket was first seen. */
export function seenWords(iso: string, now: number): string {
  const at = new Date(iso)
  const time = clockMinute(iso)
  return at.toDateString() === new Date(now).toDateString() ? `at ${time}` : `yesterday at ${time}`
}

/** A history event, with the other family's when it folds into one change. */
export type ChangeSocket = PortEvent & { twin?: PortEvent }

/**
 * One line of the Changes list: a socket (or a service's two families)
 * opening, closing, or passing to another program — the history's closing
 * and opening of one socket in one sample, read as the one event it was.
 */
export type PortChange = {
  key: string
  kind: "opened" | "closed" | "replaced"
  at: string
  after: string
  /** The socket that opened or closed; for a new owner, the socket as it holds it. */
  socket: ChangeSocket
  /** For a new owner, the program that held the socket before. */
  previous?: PortEvent
}

function endpointKey(event: Pick<PortEvent, "protocol" | "family" | "address" | "port">): string {
  return `${event.protocol} ${event.family} ${event.address} ${event.port}`
}

const KIND_ORDER: Record<PortChange["kind"], number> = { closed: 0, replaced: 1, opened: 2 }

/**
 * The history's events as the lines the Changes list draws, newest sample
 * first and by port within one. A socket closed and opened in the same
 * sample passed from one program to another. A service's IPv4 and IPv6
 * sockets changing together are one line, folded by the rule the list of
 * listening sockets folds them by.
 */
export function foldChanges(events: PortEvent[]): PortChange[] {
  const samples = new Map<string, PortEvent[]>()
  for (const event of events) {
    samples.set(event.at, [...(samples.get(event.at) ?? []), event])
  }
  const out: PortChange[] = []
  for (const [at, batch] of samples) {
    const closings = new Map<string, PortEvent>()
    for (const event of batch) {
      if (event.kind === "closed") closings.set(endpointKey(event), event)
    }
    const opened: PortEvent[] = []
    const replaced: PortEvent[] = []
    const previous = new Map<string, PortEvent>()
    for (const event of batch) {
      if (event.kind !== "opened") continue
      const before = closings.get(endpointKey(event))
      if (before) {
        replaced.push(event)
        previous.set(endpointKey(event), before)
      } else {
        opened.push(event)
      }
    }
    const closed = batch.filter(
      (event) => event.kind === "closed" && !previous.has(endpointKey(event)),
    )
    const lines: PortChange[] = []
    const add = (kind: PortChange["kind"], sockets: PortEvent[]) => {
      for (const socket of foldDualStack(sockets) as ChangeSocket[]) {
        lines.push({
          key: `${at} ${kind} ${endpointKey(socket)}`,
          kind,
          at,
          after: socket.after,
          socket,
          previous: kind === "replaced" ? previous.get(endpointKey(socket)) : undefined,
        })
      }
    }
    add("closed", closed)
    add("replaced", replaced)
    add("opened", opened)
    lines.sort(
      (a, b) =>
        a.socket.port - b.socket.port ||
        a.socket.protocol.localeCompare(b.socket.protocol) ||
        KIND_ORDER[a.kind] - KIND_ORDER[b.kind],
    )
    out.push(...lines)
  }
  return out
}

/** The change's addresses, IPv4 first as the history orders them. */
export function changeAddresses(change: PortChange): string[] {
  const { socket } = change
  return socket.twin ? [socket.address, socket.twin.address] : [socket.address]
}

/**
 * When a change happened. Two samples a minute apart date it to the minute;
 * further apart — the dashboard was stopped, or a sample failed — the change
 * is somewhere in the span, and saying the later minute alone would claim a
 * precision nobody measured.
 */
export function changeWhen(
  change: Pick<PortChange, "at" | "after">,
  intervalSeconds: number,
): { time: string; span?: string } {
  const gap = (Date.parse(change.at) - Date.parse(change.after)) / 1000
  const time = clockMinute(change.at)
  if (!(gap > intervalSeconds * 1.5)) return { time }
  const from = new Date(change.after)
  const to = new Date(change.at)
  const dated = from.toDateString() !== to.toDateString()
  const at = (d: Date) =>
    dated
      ? d.toLocaleString(undefined, {
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
          hour12: false,
        })
      : clockMinute(d.toISOString())
  return { time, span: `Sometime between ${at(from)} and ${at(to)}, when nothing was sampled` }
}

/**
 * How long a socket that closed had listened: "open 2h 14m", or since before
 * the history began, which is all the history knows of one that was there
 * from its start.
 */
export function heldFor(change: PortChange): string | undefined {
  if (change.kind !== "closed") return undefined
  if (change.socket.baseline) return "open since before recording began"
  const seconds = (Date.parse(change.at) - Date.parse(change.socket.since)) / 1000
  return seconds >= 60 ? `open ${duration(seconds)}` : "open under a minute"
}

/** The changes by who could reach the socket, as the list's chips split it. */
export function tallyChanges(changes: PortChange[]): Record<"all" | ReachGroup, number> {
  const tally = { all: 0, internet: 0, private: 0, local: 0 }
  for (const change of changes) {
    tally.all++
    tally[reachGroup(change.socket)]++
  }
  return tally
}

/**
 * The changes grouped by the day they were seen on, newest first. A column
 * of dates where most rows are today's is noise; the day belongs on the
 * group and the minute on the row.
 */
export function changesByDay(changes: PortChange[], now: number): [string, PortChange[]][] {
  const days = new Map<string, PortChange[]>()
  for (const change of changes) {
    const label = dayLabel(change.at, now)
    days.set(label, [...(days.get(label) ?? []), change])
  }
  return [...days]
}

export function dayLabel(iso: string, now: number): string {
  const date = new Date(iso)
  const today = new Date(now)
  const yesterday = new Date(now)
  yesterday.setDate(today.getDate() - 1)
  if (date.toDateString() === today.toDateString()) return "Today"
  if (date.toDateString() === yesterday.toDateString()) return "Yesterday"
  return date.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" })
}
