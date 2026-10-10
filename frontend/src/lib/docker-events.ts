import type { DockerEvent } from "@/lib/types"

/**
 * A container's or a stack's Docker events, read as what happened rather than
 * as the daemon's bookkeeping.
 *
 * A container in a restart loop writes an exit and a start every few seconds,
 * and a feed of two hundred of those is the one thing the reader already knew
 * — it keeps dying — pushing everything else off the screen. So a run of
 * restarts of one container that exits the same way each time is one entry
 * ("restarted ×17 in 12 min · exit 1") that opens onto the events it folds,
 * and the exit that ended the run, if it stayed down, stays a row of its own:
 * its last lines are the ones worth reading.
 */

/** One row of the feed: an event, or a restart loop folded into one. */
export type EventEntry =
  | {
      kind: "event"
      key: string
      event: DockerEvent
      /** The OOM killer's note on the exit it caused, folded into the exit's row. */
      oom?: DockerEvent
    }
  | {
      kind: "loop"
      key: string
      /** The container, as its events name it. */
      name: string
      /** How many times it exited and came back. */
      times: number
      /** The status every exit in the loop had; absent where Docker gave none. */
      exitCode?: string
      /** Whether the kernel's OOM killer was behind any of them. */
      oom: boolean
      /** The first exit and the last start: what "in 12 min" measures. */
      since: string
      until: string
      /** The loop's last exit: the lines before it are the latest attempt's. */
      exit: DockerEvent
      /** Everything folded, newest first. */
      events: DockerEvent[]
    }

/** Two exits further apart than this are two incidents, not one loop. */
const LOOP_GAP_MS = 10 * 60_000

/** Docker notes an OOM kill and the exit it caused this close together, in either order. */
const OOM_EXIT_MS = 1_000

export function eventKey(event: DockerEvent) {
  return `${event.time}|${event.type}|${event.action}|${event.id ?? event.name}`
}

/**
 * The socket sends the buffered past on connect and the poll reads the same
 * buffer, so the two overlap by design. The first copy of an event wins:
 * callers put the polled one first, which is the one the server laid against
 * the audit log.
 */
export function dedupeEvents(events: DockerEvent[]): DockerEvent[] {
  const seen = new Set<string>()
  const out: DockerEvent[] = []
  for (const event of events) {
    const key = eventKey(event)
    if (seen.has(key)) continue
    seen.add(key)
    out.push(event)
  }
  return out.sort((a, b) => at(b) - at(a))
}

function at(event: DockerEvent) {
  return Date.parse(event.time)
}

/** An exit and the start that brought it back, with what came between. */
type Cycle = { events: DockerEvent[]; exit: DockerEvent }

type Item = { cycle: Cycle } | { event: DockerEvent }

/** What may lead into an exit: the OOM killer's note, or the signal that ended it. */
const BEFORE_EXIT = new Set(["oom", "kill"])

function isHealth(event: DockerEvent) {
  return event.action.startsWith("health_status")
}

/**
 * One container's events, oldest first, cut into restart cycles and the
 * events between them. A cycle is `[oom|kill]* die [stop]* start [restart]`
 * — the shape of a restart policy firing, and of `docker restart`, which
 * sends kill, die, stop, start and restart in that order.
 */
function cycles(events: DockerEvent[]): Item[] {
  const items: Item[] = []
  let open: DockerEvent[] = []
  let exit: DockerEvent | undefined
  const spill = () => {
    for (const event of open) items.push({ event })
    open = []
    exit = undefined
  }
  for (const event of events) {
    const action = event.action
    if (event.type !== "container") {
      spill()
      items.push({ event })
    } else if (BEFORE_EXIT.has(action)) {
      // A kill after an exit that nothing restarted opens the next cycle.
      if (exit) spill()
      open.push(event)
    } else if (action === "die") {
      if (exit) spill()
      open.push(event)
      exit = event
    } else if (action === "stop" && exit) {
      open.push(event)
    } else if (action === "start" && exit) {
      open.push(event)
      items.push({ cycle: { events: open, exit } })
      open = []
      exit = undefined
    } else if (action === "restart" && !exit && items.length > 0) {
      const last = items[items.length - 1]
      if ("cycle" in last) last.cycle.events.push(event)
      else items.push({ event })
    } else {
      spill()
      items.push({ event })
    }
  }
  spill()
  return items
}

/**
 * The events, newest first, with each run of two or more restarts of one
 * container that exited the same way folded into a loop entry.
 *
 * A health check changing its verdict between two restarts belongs to the
 * loop — it is usually the reason for the next one — and is folded with it;
 * anything else that happens to the container ends the run. Another
 * container's events never do: in a stack's feed the database looping beside
 * a steady web server is still one loop.
 */
export function foldRestarts(events: DockerEvent[]): EventEntry[] {
  const byContainer = new Map<string, DockerEvent[]>()
  for (const event of [...events].sort((a, b) => at(a) - at(b))) {
    const owner = event.type === "container" ? (event.id ?? event.name) : `${event.type}:`
    const list = byContainer.get(owner)
    if (list) list.push(event)
    else byContainer.set(owner, [event])
  }

  const entries: { time: number; entry: EventEntry }[] = []

  for (const list of byContainer.values()) {
    // The container's events that stay rows of their own.
    const singles: DockerEvent[] = []
    const single = (event: DockerEvent) => singles.push(event)
    let run: Cycle[] = []
    // Health verdicts seen since the run's last cycle: folded into it if
    // another cycle follows, their own rows if the run ends here.
    let between: DockerEvent[] = []
    const close = () => {
      if (run.length >= 2) {
        const folded = run.flatMap((c) => c.events)
        const exit = run[run.length - 1].exit
        const first = folded[0]
        const last = folded[folded.length - 1]
        entries.push({
          time: at(last),
          entry: {
            kind: "loop",
            key: `loop|${eventKey(first)}`,
            name: exit.name,
            times: run.length,
            exitCode: exit.exitCode,
            oom: folded.some((e) => e.action === "oom"),
            since: first.time,
            until: last.time,
            exit,
            events: [...folded].reverse(),
          },
        })
      } else {
        for (const cycle of run) cycle.events.forEach(single)
      }
      between.forEach(single)
      run = []
      between = []
    }
    for (const item of cycles(list)) {
      if ("event" in item) {
        if (run.length > 0 && isHealth(item.event)) {
          between.push(item.event)
          continue
        }
        close()
        single(item.event)
        continue
      }
      const previous = run[run.length - 1]
      const previousEnd = previous ? at(previous.events[previous.events.length - 1]) : 0
      const chained =
        previous &&
        (previous.exit.exitCode ?? "") === (item.cycle.exit.exitCode ?? "") &&
        at(item.cycle.events[0]) - previousEnd <= LOOP_GAP_MS
      if (!chained) close()
      if (chained && between.length > 0) {
        // The verdicts between two restarts of one loop go inside it, in
        // their place in time.
        item.cycle.events.unshift(...between)
        between = []
      }
      run.push(item.cycle)
    }
    close()
    entries.push(...withOomExits(singles))
  }

  return entries.sort((a, b) => b.time - a.time).map((e) => e.entry)
}

/**
 * One container's rows, with each OOM note on the exit it caused.
 *
 * A container the kernel killed for memory says so twice — `oom`, and `die`
 * with 137 a moment later — and two rows, each opening onto the same minute
 * of output, read as two incidents. So the note goes on the exit's row. An
 * `oom` with no exit beside it is a child the kernel chose while the
 * container lived on, and stays a row of its own.
 */
function withOomExits(events: DockerEvent[]): { time: number; entry: EventEntry }[] {
  const oomOf = new Map<DockerEvent, DockerEvent>()
  const folded = new Set<DockerEvent>()
  for (const exit of events) {
    if (exit.action !== "die") continue
    const oom = events.find(
      (e) => e.action === "oom" && !folded.has(e) && Math.abs(at(e) - at(exit)) <= OOM_EXIT_MS,
    )
    if (!oom) continue
    oomOf.set(exit, oom)
    folded.add(oom)
  }
  return events
    .filter((event) => !folded.has(event))
    .map((event) => ({
      time: at(event),
      entry: { kind: "event", key: eventKey(event), event, oom: oomOf.get(event) },
    }))
}

/** The minute before an exit is what "last lines" reads. */
export const LAST_LINES_MS = 60_000

/** How much of that minute is shown: its end, where the reason is. */
export const LAST_LINES = 20

/**
 * The search for the minute before an exit: its last lines, up to the exit
 * itself. The bound is the event's own time, to the nanosecond — the next
 * attempt starts up a restart policy's hundred milliseconds later, and is
 * not why it died.
 */
export function lastLinesSearch(time: string) {
  return {
    since: new Date(Date.parse(time) - LAST_LINES_MS).toISOString(),
    until: time,
    limit: LAST_LINES,
  }
}

/** "40 s", "12 min", "2 h 5 min": how long a loop has been going. */
export function spanWords(ms: number) {
  const seconds = Math.max(0, Math.round(ms / 1000))
  if (seconds < 60) return `${seconds} s`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest > 0 ? `${hours} h ${rest} min` : `${hours} h`
}

/** One run of a container's health check, as Docker kept it. */
export type HealthProbe = {
  start: string
  /** Absent while it is still running. */
  end?: string
  exitCode: number
  output: string
}

export type ContainerHealth = {
  /** `starting`, `healthy` or `unhealthy`. */
  status: string
  failingStreak: number
  /** What the check runs, as the image or compose file wrote it. */
  test?: string
  /** Newest first. Docker keeps the last five. */
  probes: HealthProbe[]
}

/**
 * Docker writes nanoseconds, and a date parser that follows the standard to
 * the letter reads three digits of a fraction and no more.
 */
function isoMillis(value: unknown): string | undefined {
  if (typeof value !== "string" || value === "" || value.startsWith("0001-")) return undefined
  return value.replace(/(\.\d{3})\d+/, "$1")
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

/**
 * The health check out of a container's inspect document: its verdict, what
 * it runs and its last few probes. Nothing for a container without one —
 * Docker leaves `State.Health` out rather than saying "none".
 */
export function healthOf(inspect: unknown): ContainerHealth | undefined {
  const health = record(record(record(inspect)?.State)?.Health)
  if (!health || typeof health.Status !== "string" || health.Status === "") return undefined
  const test = record(record(record(inspect)?.Config)?.Healthcheck)?.Test
  const words = Array.isArray(test) ? test.filter((w): w is string => typeof w === "string") : []
  // `CMD-SHELL` hands its one string to a shell, `CMD` runs its words.
  const command = words[0] === "CMD-SHELL" || words[0] === "CMD" ? words.slice(1).join(" ") : ""
  const log = Array.isArray(health.Log) ? health.Log : []
  const probes = log
    .map(record)
    .filter((p): p is Record<string, unknown> => Boolean(p))
    .flatMap((p): HealthProbe[] => {
      const start = isoMillis(p.Start)
      if (!start) return []
      return [
        {
          start,
          end: isoMillis(p.End),
          exitCode: typeof p.ExitCode === "number" ? p.ExitCode : -1,
          output: typeof p.Output === "string" ? p.Output.trim() : "",
        },
      ]
    })
    .sort((a, b) => Date.parse(b.start) - Date.parse(a.start))
  return {
    status: health.Status,
    failingStreak: typeof health.FailingStreak === "number" ? health.FailingStreak : 0,
    test: command || undefined,
    probes,
  }
}

/** How long after a kill its exit is still the one that was asked for: a stop's grace period, with room. */
const ASKED_EXIT_MS = 30_000

/**
 * The record with the exits somebody asked for read as the stops they were.
 *
 * The server calls any exit status but 0 an error, and most programs answer
 * the SIGTERM of `docker stop` or `docker restart` with 143 — so a restart
 * pressed in this dashboard read as a crash. Docker sends `kill` before
 * every exit it was asked for, and none before an exit nobody asked for (a
 * crash, or the kernel's OOM kill, which is `oom` and then `die`), so a
 * failing exit moments after a kill of the same container comes back as a
 * notice: drawn, counted and folded as a stop, its status still said.
 */
export function settleAskedExits(events: DockerEvent[]): DockerEvent[] {
  const kills = new Map<string, number[]>()
  for (const event of events) {
    if (event.type !== "container" || event.action !== "kill") continue
    const key = event.id || event.name
    kills.set(key, [...(kills.get(key) ?? []), at(event)])
  }
  if (kills.size === 0) return events
  return events.map((event) => {
    if (event.type !== "container" || event.action !== "die" || event.level !== "error") {
      return event
    }
    const time = at(event)
    const asked = kills
      .get(event.id || event.name)
      ?.some((kill) => kill <= time && time - kill <= ASKED_EXIT_MS)
    return asked ? { ...event, level: "notice" } : event
  })
}

/**
 * What an event did, read the way the host's Events page colours it: an exit
 * that failed or an OOM kill, a health check that turned, a container that
 * came back or was asked to stop, an object made or removed, and the
 * bookkeeping — a network joined, a volume mounted — that is none of those.
 */
export type EventOutcome =
  "failed" | "unhealthy" | "restarted" | "started" | "healthy" | "stopped" | "changed" | "other"

const STOPPED = new Set(["stop", "kill", "pause"])
const CHANGED = new Set([
  "create",
  "destroy",
  "rename",
  "update",
  "pull",
  "push",
  "delete",
  "tag",
  "untag",
  "import",
  "load",
])

export function outcomeOf(event: DockerEvent): EventOutcome {
  const action = event.action
  if (event.type === "container") {
    // The server calls a failing exit an error and a status-0 one a notice,
    // which is the line `isCleanExit` draws too.
    if (action === "die") return event.level === "error" ? "failed" : "stopped"
    if (action === "oom") return "failed"
    if (action.startsWith("health_status")) {
      return action.endsWith("unhealthy") ? "unhealthy" : "healthy"
    }
    if (action === "restart") return "restarted"
    if (action === "start" || action === "unpause") return "started"
    if (STOPPED.has(action)) return "stopped"
  }
  if (CHANGED.has(action)) return "changed"
  return "other"
}

/** One container's part of the record, summed for the Events page's table. */
export type ContainerActivity = {
  /** The container's id where its events carry one, else its name. */
  key: string
  id?: string
  name: string
  image?: string
  stack?: string
  service?: string
  /** Newest first. */
  events: DockerEvent[]
  /** Exits that failed, with an OOM kill and the exit it caused counted once. */
  failures: number
  /** How many times it came back after exiting — a restart policy firing, or `docker restart`. */
  restarts: number
  oom: boolean
  last: DockerEvent
  /** The newest exit, clean or not. */
  lastExit?: DockerEvent
  /** Its newest entry is a restart loop that has not stopped going. */
  looping: boolean
  /** The last thing that happened to it was its removal. */
  removed: boolean
  /** Who in this dashboard acted on it, most recent first. */
  actors: string[]
  /** Actions on it that nothing in the audit log or compose explains. */
  outside: number
}

/**
 * Every container the record names, with what happened to it summed: the
 * table on the Events page, one row per container rather than one per event.
 * Ordered by what needs the reader first — a loop still going, then a
 * container that failed, the newest of each first — and then by when it was
 * last touched.
 */
export function containerActivity(events: DockerEvent[], now: number): ContainerActivity[] {
  const byContainer = new Map<string, DockerEvent[]>()
  for (const event of events) {
    if (event.type !== "container") continue
    const key = event.id || event.name
    const list = byContainer.get(key)
    if (list) list.push(event)
    else byContainer.set(key, [event])
  }

  const rows: ContainerActivity[] = []
  for (const [key, list] of byContainer) {
    const newest = [...list].sort((a, b) => at(b) - at(a))
    const oldest = [...newest].reverse()
    const restarts = comebacks(oldest).size
    const failures = oldest.filter((e) => e.action === "die" && e.level === "error").length
    let ooms = 0
    // An OOM kill is its own failure only where no failing exit stands beside it.
    for (const oom of oldest) {
      if (oom.action !== "oom") continue
      const exit = oldest.some(
        (e) =>
          e.action === "die" && e.level === "error" && Math.abs(at(e) - at(oom)) <= OOM_EXIT_MS,
      )
      if (!exit) ooms++
    }
    const first = foldRestarts(newest)[0]
    const actors: string[] = []
    for (const event of newest) {
      const actor = event.trigger?.actor
      if (actor && !actors.includes(actor)) actors.push(actor)
    }
    const last = newest[0]
    const named = newest.find((e) => e.name) ?? last
    rows.push({
      key,
      id: last.id,
      name: named.name,
      image: newest.find((e) => e.image)?.image,
      stack: newest.find((e) => e.stack)?.stack,
      service: newest.find((e) => e.service)?.service,
      events: newest,
      failures: failures + ooms,
      restarts,
      oom: newest.some((e) => e.action === "oom"),
      last,
      lastExit: newest.find((e) => e.action === "die"),
      looping: first?.kind === "loop" && now - Date.parse(first.until) <= LOOP_GAP_MS,
      removed: last.action === "destroy",
      actors,
      outside: newest.filter((e) => e.source === "docker" && !e.trigger).length,
    })
  }

  const rank = (row: ContainerActivity) =>
    row.looping ? 0 : row.failures > 0 && !row.removed ? 1 : 2
  return rows.sort((a, b) => rank(a) - rank(b) || at(b.last) - at(a.last))
}

/**
 * The starts that brought a container back after it exited — a restart
 * policy firing, or the start inside `docker restart` — as opposed to the
 * first start of one just created. Any order in; each container is read
 * oldest first on its own.
 */
export function comebacks(events: DockerEvent[]): Set<DockerEvent> {
  const out = new Set<DockerEvent>()
  const exited = new Map<string, boolean>()
  for (const event of [...events].sort((a, b) => at(a) - at(b))) {
    if (event.type !== "container") continue
    const key = event.id || event.name
    if (event.action === "die") exited.set(key, true)
    else if (event.action === "create") exited.set(key, false)
    else if (event.action === "start" && exited.get(key)) {
      out.add(event)
      exited.set(key, false)
    }
  }
  return out
}

/**
 * How many of the events fall in each of `count` equal slices of the window,
 * oldest first: a tile's trend, read off the record rather than sampled.
 */
export function perSlice(
  events: DockerEvent[],
  from: number,
  to: number,
  count: number,
  keep: (event: DockerEvent) => boolean = () => true,
): number[] {
  const slices = Array.from({ length: count }, () => 0)
  const span = to - from
  if (span <= 0 || count <= 0) return slices
  for (const event of events) {
    const time = at(event)
    if (time < from || time > to || !keep(event)) continue
    slices[Math.min(count - 1, Math.floor(((time - from) / span) * count))]++
  }
  return slices
}

/** The steps an axis may take, finest first: whatever puts at most a handful of labels across it. */
const AXIS_STEPS = [5, 10, 15, 30, 60, 120, 180, 360, 720, 1440].map((m) => m * 60_000)

/**
 * The times an axis over `from`–`to` labels, on the local clock's round
 * minutes, hours and midnights: at most `most` of them.
 */
export function axisTicks(from: number, to: number, most = 6): number[] {
  const span = to - from
  if (span <= 0 || most <= 0) return []
  const day = 24 * 60 * 60_000
  const step = AXIS_STEPS.find((s) => span / s <= most) ?? Math.ceil(span / most / day) * day
  // The local clock's offset, so a three-hour step lands on 03:00 and not on
  // whatever three-hour mark UTC has here.
  const offset = new Date(from).getTimezoneOffset() * 60_000
  const out: number[] = []
  for (let t = Math.ceil((from - offset) / step) * step + offset; t <= to; t += step) out.push(t)
  return out
}

/**
 * A feed row: an event, a restart loop, or a burst — one container's events
 * moments apart, read as the one thing that happened.
 */
export type FeedEntry =
  | EventEntry
  | {
      kind: "burst"
      key: string
      /** The event the row is titled by: what the burst amounts to. */
      lead: DockerEvent
      /** The OOM killer's note on the lead exit, where it has one. */
      oom?: DockerEvent
      /** Newest first. */
      events: DockerEvent[]
    }

/** Two of a container's events further apart than this are two things that happened. */
const BURST_MS = 5_000

/**
 * What a burst amounts to, most telling first: a failure, then a restart, a
 * removal, a start, a stop, a creation; the clean exit and the kill inside a
 * stop say least.
 */
function leadRank(event: DockerEvent) {
  if (event.action === "die") return event.level === "error" ? 0 : 7
  const rank: Record<string, number> = {
    restart: 1,
    destroy: 2,
    start: 3,
    unpause: 3,
    stop: 4,
    pause: 4,
    create: 5,
    rename: 6,
    update: 6,
    kill: 8,
  }
  return rank[event.action] ?? 9
}

/** The actions a burst is made of; a health verdict always stands on its own. */
const BURSTS = new Set([
  "create",
  "start",
  "stop",
  "kill",
  "die",
  "restart",
  "destroy",
  "pause",
  "unpause",
  "rename",
  "update",
])

/**
 * The feed with each container's runs of events moments apart folded into
 * one row. `docker restart` writes kill, die, stop, start and restart inside
 * a second, compose writes create and start, a one-off job die and destroy —
 * five rows for one press, which a feed of them buries everything else
 * under. The row is titled by what the run amounts to (`leadRank`) and opens
 * onto its events. Loops and other kinds of object pass through untouched.
 */
export function foldBursts(entries: EventEntry[]): FeedEntry[] {
  const out: { time: number; entry: FeedEntry }[] = []
  const byContainer = new Map<string, Extract<EventEntry, { kind: "event" }>[]>()
  for (const entry of entries) {
    const event = entry.kind === "event" ? entry.event : undefined
    if (!event || event.type !== "container" || !BURSTS.has(event.action)) {
      out.push({ time: entry.kind === "loop" ? Date.parse(entry.until) : at(entry.event), entry })
      continue
    }
    const key = event.id || event.name
    byContainer.set(key, [
      ...(byContainer.get(key) ?? []),
      entry as Extract<EventEntry, { kind: "event" }>,
    ])
  }

  for (const list of byContainer.values()) {
    const oldest = [...list].sort((a, b) => at(a.event) - at(b.event))
    let run: typeof oldest = []
    const close = () => {
      if (run.length === 1) {
        out.push({ time: at(run[0].event), entry: run[0] })
      } else if (run.length > 1) {
        const lead = run.reduce((best, e) => (leadRank(e.event) < leadRank(best.event) ? e : best))
        const newest = run[run.length - 1].event
        out.push({
          time: at(newest),
          entry: {
            kind: "burst",
            key: `burst|${eventKey(run[0].event)}`,
            lead: lead.event,
            oom: lead.oom,
            events: run.map((e) => e.event).reverse(),
          },
        })
      }
      run = []
    }
    for (const entry of oldest) {
      const previous = run[run.length - 1]
      if (previous && at(entry.event) - at(previous.event) > BURST_MS) close()
      run.push(entry)
    }
    close()
  }

  return out.sort((a, b) => b.time - a.time).map((e) => e.entry)
}
