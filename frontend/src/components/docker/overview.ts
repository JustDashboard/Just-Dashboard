import type { Container, DockerEvent } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"

/**
 * The words the Docker overview reads the daemon's events in: the last thing
 * that happened to each container. Which containers are failing is the
 * Containers page's reading (`containers.ts`), so the overview's verdict and
 * the table it opens count the same ones.
 */

/** Docker's "Exited (137) 12 minutes ago", as its code and how long ago. */
export function exitOf(container: Pick<Container, "state" | "status">) {
  if (container.state !== "exited") return undefined
  const match = container.status.match(/^Exited \((-?\d+)\)\s*(.*)$/i)
  if (!match) return undefined
  return { code: Number(match[1]), ago: match[2].trim() }
}

/**
 * Whether a container that is not running stopped because something went
 * wrong. 0 is a process that finished and 143 one that honoured a stop; any
 * other status is the process or the kernel ending it, which is what the
 * runtime findings call a failure too.
 */
export function crashed(container: Pick<Container, "state" | "status">) {
  const exit = exitOf(container)
  return exit !== undefined && exit.code !== 0 && exit.code !== 143
}

/** The actions an event is worth a line for. The rest — exec, attach, kill before a die — are the daemon working. */
const WORDS: Record<string, [string, DotTone]> = {
  start: ["started", "running"],
  restart: ["restarted", "warning"],
  oom: ["ran out of memory", "danger"],
  "health_status: unhealthy": ["failing its health check", "danger"],
  "health_status: healthy": ["healthy again", "running"],
  destroy: ["removed", "stopped"],
  create: ["created", "notice"],
  pause: ["paused", "warning"],
  unpause: ["resumed", "running"],
}

/** How close an OOM kill and the exit it causes are, for the two to be read as one. */
const OOM_WINDOW = 10_000

const DAY = 86_400_000
const HOUR = 3_600_000

export type ContainerChange = {
  /** The container's name, which survives a compose redeploy where its id does not. */
  name: string
  id?: string
  image?: string
  stack?: string
  action: string
  verb: string
  tone: DotTone
  /** Unix milliseconds. */
  at: number
  /** Exits with an error in the hour before this change: a container that keeps coming back is still a problem. */
  crashes: number
}

function eventWords(event: DockerEvent, oom: boolean): [string, DotTone] | undefined {
  if (event.action === "die") {
    if (oom) return WORDS.oom
    const code = event.exitCode ?? ""
    if (code === "" || code === "0") return ["stopped", "stopped"]
    if (code === "143") return ["stopped", "stopped"]
    return [`exited ${code}`, "danger"]
  }
  return WORDS[event.action]
}

/**
 * The last thing that happened to each container in the last day, newest
 * first — one line per container, as Services' Recent block has one per unit.
 *
 * A stop is a kill, a die and a stop in that order, a restart is a die and a
 * start, and an OOM kill is an oom and a die a moment apart; each is read as
 * the one thing it was. A container that started after crashing says how
 * many times it crashed in the hour, because "started" over a restart loop is
 * the loop hiding behind its last good moment.
 */
export function recentChanges(events: DockerEvent[], now: number): ContainerChange[] {
  const ofContainers = events
    .filter((e) => e.type === "container" && e.name)
    .map((e) => ({ event: e, at: Date.parse(e.time) }))
    .filter(({ at }) => Number.isFinite(at) && now - at <= DAY && at <= now + 60_000)
    .sort((a, b) => b.at - a.at)

  const seen = new Set<string>()
  const changes: ContainerChange[] = []
  for (const { event, at } of ofContainers) {
    if (seen.has(event.name)) continue
    const mine = ofContainers.filter((e) => e.event.name === event.name)
    const oom =
      event.action === "die" &&
      mine.some((e) => e.event.action === "oom" && Math.abs(e.at - at) <= OOM_WINDOW)
    const words = eventWords(event, oom)
    if (!words) continue
    // An oom with its exit beside it is drawn once, by the exit.
    if (event.action === "oom" && mine.some((e) => e.event.action === "die" && e.at >= at)) {
      continue
    }
    seen.add(event.name)
    const crashes = mine.filter(
      (e) =>
        e.event.action === "die" &&
        e.at <= at &&
        at - e.at <= HOUR &&
        (e.event.exitCode ?? "0") !== "0" &&
        e.event.exitCode !== "143",
    ).length
    changes.push({
      name: event.name,
      id: event.id,
      image: event.image,
      stack: event.stack,
      action: event.action,
      verb: words[0],
      tone: words[1],
      at,
      crashes,
    })
  }
  return changes
}

/** Every event the feeds have sent, once: the socket replays its buffer on every reconnect. */
export function mergeEvents(previous: DockerEvent[], batch: DockerEvent[], limit = 300) {
  const key = (e: DockerEvent) => `${e.time}\u0000${e.type}\u0000${e.action}\u0000${e.id ?? e.name}`
  const seen = new Set<string>()
  const out: DockerEvent[] = []
  for (const event of [...batch, ...previous]) {
    const k = key(event)
    if (seen.has(k)) continue
    seen.add(k)
    out.push(event)
  }
  return out.sort((a, b) => Date.parse(b.time) - Date.parse(a.time)).slice(0, limit)
}
