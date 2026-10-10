import type { DotTone } from "@/components/status-dot"
import { foldRestarts, type EventEntry } from "@/lib/docker-events"
import type { Container, ContainerStats, DockerEvent } from "@/lib/types"

/**
 * The containers page's reading of each container, kept apart from the page
 * so the words and the order can be tested without a browser.
 *
 * Docker's own states are seven words, and two of the questions the page is
 * opened with cut across them: a running container failing its health check
 * is in as much trouble as one in a restart loop, and one the kernel killed
 * for memory is as stopped as one somebody stopped and in a very different
 * way. So the page counts and narrows by five buckets read from the state,
 * the health check, the exit status and the event log together.
 */
export type ContainerBucket = "failing" | "starting" | "running" | "paused" | "stopped"

/** Worst first, which is the order the table opens in. */
export const BUCKET_RANK: Record<ContainerBucket, number> = {
  failing: 0,
  starting: 1,
  running: 2,
  paused: 3,
  stopped: 4,
}

/**
 * The statuses a deliberate stop leaves behind. `docker stop` sends SIGTERM
 * (143) and, ten seconds later, SIGKILL (137); Ctrl-C on an attached
 * container is SIGINT (130). None of them says the container failed — 137
 * says so only when the kernel's OOM killer sent it, which the event log
 * knows and the listing does not.
 */
const DELIBERATE = new Set([0, 130, 137, 143])

/** The status in Docker's "Exited (137) 2 hours ago", or undefined when it gave none. */
export function exitCodeOf(container: Pick<Container, "status">): number | undefined {
  const match = container.status.match(/^(?:Exited|Restarting)\s*\((-?\d+)\)/i)
  return match ? Number(match[1]) : undefined
}

/** When the listing's status puts the last exit: "2 hours ago", "About a minute ago". */
export function exitedWhen(container: Pick<Container, "status">): string | undefined {
  const match = container.status.match(/^Exited\s*\(-?\d+\)\s*(.+)$/i)
  return match?.[1]?.trim().replace(/^About an? /i, "a ") || undefined
}

export function containerBucket(container: Container, oomKilled = false): ContainerBucket {
  switch (container.state) {
    case "restarting":
    case "dead":
      return "failing"
    case "paused":
      return "paused"
    case "running":
      if (container.health === "unhealthy") return "failing"
      if (container.health === "starting") return "starting"
      return "running"
    case "exited": {
      if (oomKilled) return "failing"
      const code = exitCodeOf(container)
      return code !== undefined && !DELIBERATE.has(code) ? "failing" : "stopped"
    }
    default:
      return "stopped"
  }
}

export function bucketTone(bucket: ContainerBucket): DotTone {
  switch (bucket) {
    case "failing":
      return "danger"
    case "starting":
      return "warning"
    case "running":
      return "running"
    case "paused":
      return "notice"
    case "stopped":
      return "stopped"
  }
}

/**
 * The state as one word: Docker's own where it says what is true, a sharper
 * one where it does not. "Exited" is three different facts — a job that
 * finished, a service somebody stopped, and a crash — and only the exit
 * status tells them apart.
 */
export function containerWord(container: Container, oomKilled = false): string {
  switch (container.state) {
    case "running":
      if (container.health === "unhealthy") return "Unhealthy"
      if (container.health === "starting") return "Starting"
      return "Running"
    case "restarting":
      return "Restarting"
    case "paused":
      return "Paused"
    case "dead":
      return "Dead"
    case "created":
      return "Never started"
    case "removing":
      return "Being removed"
    case "exited": {
      if (oomKilled) return "Out of memory"
      const code = exitCodeOf(container)
      if (code === undefined || code === 0) return "Stopped"
      return DELIBERATE.has(code) ? "Stopped" : "Crashed"
    }
    default:
      return container.state
  }
}

/** An exit status in words: what a 137 or a 143 means to somebody who has not memorised signals. */
export function exitWords(code: number | undefined, oomKilled = false): string | undefined {
  if (oomKilled) return "killed for memory"
  if (code === undefined) return undefined
  switch (code) {
    case 0:
      return "exited cleanly"
    case 130:
      return "interrupted"
    case 137:
      return "killed"
    case 143:
      return "stopped"
    default:
      return `exit ${code}`
  }
}

export type SortKey = "state" | "name" | "cpu" | "memory"
export type SortDir = "asc" | "desc"
export type ContainerSort = { key: SortKey; dir: SortDir }

/** The direction a column sorts in when it is first pressed: figures heaviest first. */
export const FIRST_DIR: Record<SortKey, SortDir> = {
  state: "asc",
  name: "asc",
  cpu: "desc",
  memory: "desc",
}

/**
 * The table's order. By state is the default and puts what is failing first,
 * then by name within a state; by a figure, a container with no reading — not
 * running — goes last in either direction, because "nothing" is neither the
 * heaviest nor the lightest.
 */
export function sortContainers(
  containers: Container[],
  sort: ContainerSort,
  stats: Record<string, ContainerStats>,
  bucketOf: (container: Container) => ContainerBucket,
): Container[] {
  const flip = sort.dir === "asc" ? 1 : -1
  const byName = (a: Container, b: Container) => a.name.localeCompare(b.name)
  const figure = (c: Container) => {
    const stat = stats[c.id]
    if (!stat) return undefined
    if (sort.key === "cpu") return stat.cpuReady === false ? undefined : stat.cpuPercent
    return stat.memUsage
  }
  return [...containers].sort((a, b) => {
    switch (sort.key) {
      case "name":
        return flip * byName(a, b)
      case "state": {
        const rank = BUCKET_RANK[bucketOf(a)] - BUCKET_RANK[bucketOf(b)]
        return rank !== 0 ? flip * rank : byName(a, b)
      }
      default: {
        const x = figure(a)
        const y = figure(b)
        if (x === undefined || y === undefined) {
          if (x === y) return byName(a, b)
          return x === undefined ? 1 : -1
        }
        return x !== y ? flip * (x - y) : byName(a, b)
      }
    }
  })
}

export type NetRate = { rx: number; tx: number }

/**
 * Bytes a second in and out, from two frames of Docker's cumulative
 * counters. A counter that went backwards is a container that restarted
 * between the frames, and a frame too close to the last says nothing, so
 * neither has a rate; nor does a container on the host's network, whose
 * traffic Docker cannot attribute.
 */
export function networkRates(
  previous: Record<string, ContainerStats>,
  next: ContainerStats[],
): Record<string, NetRate> {
  const rates: Record<string, NetRate> = {}
  for (const stat of next) {
    const before = previous[stat.id]
    if (!before || stat.networkAvailable === false) continue
    const seconds = (Date.parse(stat.ts) - Date.parse(before.ts)) / 1000
    if (!(seconds >= 0.5)) continue
    const rx = stat.netRx - before.netRx
    const tx = stat.netTx - before.netTx
    if (rx < 0 || tx < 0) continue
    rates[stat.id] = { rx: rx / seconds, tx: tx / seconds }
  }
  return rates
}

/**
 * The container actions worth a line in Recent. The rest of what Docker
 * reports — `exec_start`, `attach`, `top` — is the daemon answering
 * questions, and a feed of those is a feed of this page's own polling.
 */
const RECENT_ACTIONS = new Set([
  "start",
  "die",
  "oom",
  "kill",
  "restart",
  "pause",
  "unpause",
  "destroy",
  "create",
  "health_status: unhealthy",
  "health_status: healthy",
])

/**
 * What happened to the containers lately, newest first, with each restart
 * loop one entry and each OOM kill on the exit it caused. A `stop` and a
 * `kill` say only that an exit is coming, and the exit says it better; a
 * health check passing is news only after it had failed, so a healthy
 * verdict is kept only when the same container's previous one was not.
 */
export function recentEntries(events: DockerEvent[]): EventEntry[] {
  const containers = events.filter((e) => e.type === "container" && RECENT_ACTIONS.has(e.action))
  const ordered = [...containers].sort((a, b) => Date.parse(a.time) - Date.parse(b.time))
  const lastHealth = new Map<string, string>()
  const kept: DockerEvent[] = []
  for (const event of ordered) {
    const owner = event.id ?? event.name
    if (event.action.startsWith("health_status")) {
      const previous = lastHealth.get(owner)
      lastHealth.set(owner, event.action)
      if (event.action === "health_status: healthy" && previous !== "health_status: unhealthy")
        continue
    }
    kept.push(event)
  }
  return foldRestarts(kept).filter(
    (entry) => entry.kind === "loop" || entry.event.action !== "kill",
  )
}

/**
 * The containers whose last exit the kernel's OOM killer caused, read from
 * the event log: the listing says 137, which `docker stop` also leaves.
 */
export function oomKilledIds(entries: EventEntry[]): Set<string> {
  const ids = new Set<string>()
  const settled = new Set<string>()
  for (const entry of entries) {
    const event = entry.kind === "loop" ? entry.exit : entry.event
    const id = event.id
    if (!id || settled.has(id)) continue
    if (entry.kind === "event" && event.action !== "die") {
      // A start after the exit means the exit is no longer the latest word.
      if (event.action === "start") settled.add(id)
      continue
    }
    settled.add(id)
    if (entry.kind === "loop" ? entry.oom : Boolean(entry.oom)) ids.add(id)
  }
  return ids
}
