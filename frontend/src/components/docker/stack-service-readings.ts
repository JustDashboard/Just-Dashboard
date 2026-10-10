import type {
  ComposeService,
  Container,
  ContainerPort,
  ContainerStats,
  DockerEvent,
} from "@/lib/types"
import type { DotTone } from "@/components/status-dot"
import { foldRestarts, type EventEntry } from "@/lib/docker-events"
import { duration } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"

/**
 * What a stack's page reads off the stack, the containers socket and the
 * stack's events, kept apart from the page so the words and the joins can be
 * tested without a browser.
 *
 * A compose service is three things wearing one name: the service the file
 * declares, the container compose made for it, and that container's live
 * frame. The stack poll knows the first two every ten seconds; the socket
 * knows the container the moment Docker does. So a service is read from the
 * socket's copy of its container where there is one and from the poll
 * otherwise, and its state is read with the event log beside it, because the
 * listing says 137 for a container the kernel killed for memory and for one
 * somebody stopped, and says nothing at all about a restart loop.
 */

/** Where a service sits for the table's chips and the verdict. Every service is in one. */
export type ServiceBucket = "failing" | "starting" | "missing" | "stopped" | "paused" | "running"

/** Worst first, which is the order the table opens in. */
export const BUCKET_RANK: Record<ServiceBucket, number> = {
  failing: 0,
  starting: 1,
  missing: 2,
  stopped: 3,
  paused: 4,
  running: 5,
}

export function bucketTone(bucket: ServiceBucket): DotTone {
  switch (bucket) {
    case "failing":
      return "danger"
    case "starting":
    case "missing":
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
 * The statuses a deliberate stop leaves behind: `docker stop` sends SIGTERM
 * (143) and then SIGKILL (137), Ctrl-C is SIGINT (130). None of them says the
 * service failed — 137 says so only when the OOM killer sent it, which the
 * event log knows and the listing does not.
 */
const DELIBERATE = new Set([0, 130, 137, 143])

/** The status in Docker's "Exited (137) 2 hours ago" or "Restarting (1) 8 seconds ago". */
export function exitCodeOf(status: string | undefined): number | undefined {
  const match = status?.match(/^(?:Exited|Restarting)\s*\((-?\d+)\)/i)
  return match ? Number(match[1]) : undefined
}

/** When the listing puts the last exit: "2 hours ago", "a minute ago". */
export function exitedWhen(status: string | undefined): string | undefined {
  const match = status?.match(/^Exited\s*\(-?\d+\)\s*(.+)$/i)
  return match?.[1]?.trim().replace(/^About an? /i, "a ") || undefined
}

/** An exit status in words, for somebody who has not memorised the signals. */
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

/** A run of restarts, from the event log: what the listing's one word cannot say. */
export type RestartLoop = { times: number; since: number; until: number; exitCode?: string }

export type NetRate = { rx: number; tx: number }

export type ServiceReading = {
  /** The compose service's name, which is unique in a stack. */
  key: string
  service: ComposeService
  /** The socket's copy of its container, which moves the moment Docker does. */
  container?: Container
  /** The container's id, from the socket or the poll; none for a service never created. */
  containerId?: string
  stat?: ContainerStats
  rate?: NetRate
  /** The processor's last hour, a bucket a point. */
  trend?: number[]
  /** Running under this project with no service in the compose file. */
  orphan: boolean
  bucket: ServiceBucket
  /** Docker's state, as the freshest source says it. */
  state: string
  health?: string
  /** The state as one word: Docker's where it is true, a sharper one where it is not. */
  word: string
  exitCode?: number
  oomKilled: boolean
  loop?: RestartLoop
  /** The service's lane: the hue its name takes here, in the band and in its logs. */
  lane: string
}

/** A service's colour: its name's lane, the one the stack's log draws its lines in. */
export function serviceLane(name: string) {
  return hueFor(name, LANES)
}

/** A loop this long ago is history, not the state the service is in. */
const LOOP_FRESH_MS = 15 * 60_000

/**
 * What the event log says about each container: whether its last exit was
 * the OOM killer's, and the restart loop it is in. Keyed by container id and
 * read from the folded entries newest first, so the latest word wins — with
 * the loop looked for on its own, because a container that is down between
 * two tries has its newest exit as a row of its own after the loop it ends.
 */
export function eventFacts(
  entries: EventEntry[],
  now: number,
): Map<string, { oomKilled: boolean; loop?: RestartLoop }> {
  const facts = new Map<string, { oomKilled: boolean; loop?: RestartLoop }>()
  const fact = (id: string) => {
    const known = facts.get(id) ?? { oomKilled: false }
    facts.set(id, known)
    return known
  }
  const settled = new Set<string>()
  const looped = new Set<string>()
  for (const entry of entries) {
    const event = entry.kind === "loop" ? entry.exit : entry.event
    const id = event.id
    if (!id) continue
    if (entry.kind === "loop" && !looped.has(id)) {
      looped.add(id)
      const until = Date.parse(entry.until)
      if (now - until <= LOOP_FRESH_MS) {
        fact(id).loop = {
          times: entry.times,
          since: Date.parse(entry.since),
          until,
          exitCode: entry.exitCode,
        }
      }
    }
    if (settled.has(id)) continue
    if (entry.kind === "loop") {
      settled.add(id)
      fact(id).oomKilled = entry.oom
      continue
    }
    // A start after the exit means the exit is no longer the latest word.
    if (event.action === "start") {
      settled.add(id)
      fact(id)
      continue
    }
    if (event.action !== "die") continue
    settled.add(id)
    fact(id).oomKilled = Boolean(entry.oom)
  }
  return facts
}

function bucketOf(
  state: string,
  health: string | undefined,
  code: number | undefined,
  oomKilled: boolean,
  missing: boolean,
): ServiceBucket {
  if (missing) return "missing"
  switch (state) {
    case "restarting":
    case "dead":
      return "failing"
    case "paused":
      return "paused"
    case "running":
      if (health === "unhealthy") return "failing"
      if (health === "starting") return "starting"
      return "running"
    case "exited":
      if (oomKilled) return "failing"
      return code !== undefined && !DELIBERATE.has(code) ? "failing" : "stopped"
    default:
      return "stopped"
  }
}

function wordOf(
  state: string,
  health: string | undefined,
  code: number | undefined,
  oomKilled: boolean,
  missing: boolean,
): string {
  if (missing) return "Not created"
  switch (state) {
    case "running":
      if (health === "unhealthy") return "Unhealthy"
      if (health === "starting") return "Starting"
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
    case "exited":
      if (oomKilled) return "Out of memory"
      if (code === undefined || DELIBERATE.has(code)) return "Stopped"
      return "Crashed"
    default:
      return state ? state[0].toUpperCase() + state.slice(1) : "Unknown"
  }
}

/**
 * Bytes a second in and out, from two frames of Docker's cumulative
 * counters. A counter that went backwards is a container that restarted
 * between the frames and a frame too close to the last says nothing, so
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
 * Every service of the stack joined to its live container, worst first and
 * by name within a state, so a row does not move because a reading did.
 */
export function serviceReadings({
  services,
  orphans,
  containers,
  stats,
  rates,
  trends,
  events,
  now,
}: {
  services: ComposeService[]
  orphans: string[]
  /** The containers socket's list; only this stack's are joined. */
  containers: Container[]
  stats: Record<string, ContainerStats>
  rates: Record<string, NetRate>
  /** Each container's last hour, by container name. */
  trends: Map<string, number[]>
  events: DockerEvent[]
  now: number
}): ServiceReading[] {
  const byId = new Map(containers.map((c) => [c.id, c]))
  const byService = new Map<string, Container>()
  for (const c of containers) if (c.composeService) byService.set(c.composeService, c)
  const facts = eventFacts(foldRestarts(events), now)
  const orphaned = new Set(orphans)

  return services
    .map((service): ServiceReading => {
      const missing = Boolean(service.missing)
      const container = missing
        ? undefined
        : ((service.container ? byId.get(service.container) : undefined) ??
          byService.get(service.name))
      const containerId = container?.id ?? (service.container || undefined)
      const state = container?.state ?? service.state
      const health = container ? container.health : service.health
      const status = container?.status ?? service.status
      const code = exitCodeOf(status)
      const fact = containerId ? facts.get(containerId) : undefined
      const oomKilled = state === "exited" && Boolean(fact?.oomKilled)
      const loop = state === "running" && health !== "unhealthy" ? undefined : fact?.loop
      const up = state === "running"
      return {
        key: service.name,
        service,
        container,
        containerId,
        stat: up && containerId ? stats[containerId] : undefined,
        rate: up && containerId ? rates[containerId] : undefined,
        trend: container ? trends.get(container.name) : undefined,
        orphan: orphaned.has(service.name),
        bucket: bucketOf(state, health, code, oomKilled, missing),
        state,
        health,
        word: wordOf(state, health, code, oomKilled, missing),
        exitCode: code,
        oomKilled,
        loop,
        lane: serviceLane(service.name),
      }
    })
    .sort(
      (a, b) =>
        BUCKET_RANK[a.bucket] - BUCKET_RANK[b.bucket] ||
        a.service.name.localeCompare(b.service.name),
    )
}

/**
 * The line under a service's state: how long it has been in it and whether
 * anything is checking it, or how it went down and when. Uptime is counted
 * from the start Docker recorded where the socket brought one, so it ticks;
 * otherwise from the listing's figure.
 */
export function stateDetail(
  reading: Pick<
    ServiceReading,
    "bucket" | "state" | "health" | "container" | "service" | "exitCode" | "oomKilled" | "loop"
  >,
  now: number,
): { text: string; tone?: "danger" | "warning" } | undefined {
  const { container } = reading
  if (reading.bucket === "missing") return { text: "in the compose file, never created" }
  switch (reading.state) {
    case "running": {
      const started = container?.startedAt ? Date.parse(container.startedAt) : undefined
      const seconds =
        started !== undefined ? Math.max(0, (now - started) / 1000) : container?.uptimeSeconds
      const parts = seconds ? [`up ${duration(seconds)}`] : []
      if (reading.health === "unhealthy") {
        return { text: ["failing its check", ...parts].join(" · "), tone: "danger" }
      }
      if (reading.health === "starting") parts.push("checking")
      else if (reading.health) parts.push(reading.health)
      else if (container?.inspected && !container.hasHealthcheck) parts.push("no health check")
      return parts.length > 0 ? { text: parts.join(" · ") } : undefined
    }
    case "restarting": {
      const why = reading.loop?.exitCode
        ? `exit ${reading.loop.exitCode}`
        : exitWords(reading.exitCode, reading.oomKilled)
      if (reading.loop) {
        const span = duration((reading.loop.until - reading.loop.since) / 1000)
        return {
          text: [`restarted ×${reading.loop.times} in ${span}`, why].filter(Boolean).join(" · "),
          tone: "danger",
        }
      }
      return { text: why ? `coming back · ${why}` : "coming back", tone: "danger" }
    }
    case "exited": {
      const words = exitWords(reading.exitCode, reading.oomKilled)
      const when = exitedWhen(container?.status ?? reading.service.status)
      const text = [words, when].filter(Boolean).join(" · ")
      return text ? { text, tone: reading.bucket === "failing" ? "danger" : undefined } : undefined
    }
    case "created":
      return { text: "created, never started" }
    case "dead":
      return { text: "Docker could not remove it", tone: "danger" }
    default:
      return undefined
  }
}

/** How many services sit in each bucket. */
export function bucketCounts(readings: ServiceReading[]): Record<ServiceBucket, number> {
  const counts: Record<ServiceBucket, number> = {
    failing: 0,
    starting: 0,
    missing: 0,
    stopped: 0,
    paused: 0,
    running: 0,
  }
  for (const r of readings) counts[r.bucket]++
  return counts
}

export type StackVerdict = {
  tone: DotTone
  label: string
  /** The bucket a press of the verdict narrows the table to, where there is one. */
  bucket?: ServiceBucket
}

function services(n: number) {
  return n === 1 ? "1 service" : `${n} services`
}

/**
 * The stack in one phrase, for the end of its identity line: the worst thing
 * true of it, counted, so "1 service failing" is said before "1 not created"
 * and both before "All 5 running".
 */
export function stackVerdict(readings: ServiceReading[], deployed: boolean): StackVerdict {
  const counts = bucketCounts(readings)
  const present = readings.length - counts.missing
  if (!deployed || present === 0) return { tone: "unknown", label: "Not deployed" }
  if (counts.failing > 0)
    return { tone: "danger", label: `${services(counts.failing)} failing`, bucket: "failing" }
  if (counts.starting > 0)
    return { tone: "warning", label: `${services(counts.starting)} starting`, bucket: "starting" }
  if (counts.missing > 0)
    return { tone: "warning", label: `${services(counts.missing)} not created`, bucket: "missing" }
  const orphans = readings.filter((r) => r.orphan).length
  if (orphans > 0) return { tone: "warning", label: `${services(orphans)} not in the compose file` }
  if (counts.running === 0) return { tone: "stopped", label: "Stopped", bucket: "stopped" }
  if (counts.stopped + counts.paused > 0)
    return {
      tone: "warning",
      label: `${counts.stopped + counts.paused} of ${present} stopped`,
      bucket: "stopped",
    }
  return { tone: "running", label: present === 1 ? "Running" : `All ${present} running` }
}

/** A change to one of the stack's services, as one line of Recent. */
export type ServiceChange = {
  key: string
  /** Milliseconds since the epoch. */
  at: number
  service: string
  containerId?: string
  verb: string
  tone: DotTone
}

/**
 * What happened to the stack's services lately, newest first: a restart loop
 * is one line, an OOM kill is said on the exit it caused, a health check
 * passing is news only after it had failed, and a create, a kill or a stop is
 * left to the start or the exit that follows, which says what happened.
 */
export function serviceChanges(events: DockerEvent[]): ServiceChange[] {
  const containers = events.filter((e) => e.type === "container")
  const ordered = [...containers].sort((a, b) => Date.parse(a.time) - Date.parse(b.time))
  const lastHealth = new Map<string, string>()
  const kept: DockerEvent[] = []
  for (const event of ordered) {
    const owner = event.id ?? event.name
    if (event.action.startsWith("health_status")) {
      const previous = lastHealth.get(owner)
      lastHealth.set(owner, event.action)
      if (!event.action.endsWith("unhealthy") && !previous?.endsWith("unhealthy")) continue
    }
    kept.push(event)
  }
  const out: ServiceChange[] = []
  for (const entry of foldRestarts(kept)) {
    if (entry.kind === "loop") {
      const minutes = Math.max(
        1,
        Math.round((Date.parse(entry.until) - Date.parse(entry.since)) / 60_000),
      )
      const why = entry.oom
        ? "killed for memory"
        : entry.exitCode && entry.exitCode !== "0"
          ? `exit ${entry.exitCode}`
          : undefined
      out.push({
        key: entry.key,
        at: Date.parse(entry.until),
        service: entry.exit.service || entry.name,
        containerId: entry.exit.id,
        verb: `restarted ×${entry.times} in ${minutes} min${why ? ` · ${why}` : ""}`,
        tone: "danger",
      })
      continue
    }
    const said = changeWords(entry.event, Boolean(entry.oom))
    if (!said) continue
    out.push({
      key: entry.key,
      at: Date.parse(entry.event.time),
      service: entry.event.service || entry.event.name,
      containerId: entry.event.id,
      ...said,
    })
  }
  return out.sort((a, b) => b.at - a.at)
}

function changeWords(
  event: DockerEvent,
  oomKilled: boolean,
): { verb: string; tone: DotTone } | undefined {
  const action = event.action
  if (action.startsWith("health_status")) {
    return action.endsWith("unhealthy")
      ? { verb: "failing its check", tone: "danger" }
      : { verb: "passing its check again", tone: "running" }
  }
  switch (action) {
    case "start":
      return { verb: "started", tone: "running" }
    case "unpause":
      return { verb: "resumed", tone: "running" }
    case "restart":
      return { verb: "restarted", tone: "warning" }
    case "oom":
      return { verb: "out of memory", tone: "danger" }
    case "die": {
      if (oomKilled) return { verb: "killed for memory", tone: "danger" }
      const code = event.exitCode ? Number(event.exitCode) : undefined
      if (event.level === "error" && code !== undefined && !DELIBERATE.has(code))
        return { verb: `exited ${code}`, tone: "danger" }
      return { verb: exitWords(code) ?? "exited", tone: "stopped" }
    }
    case "pause":
      return { verb: "paused", tone: "stopped" }
    case "destroy":
      return { verb: "removed", tone: "stopped" }
    default:
      return undefined
  }
}

/** Where a published port is bound, in the three answers that differ. */
export type Binding = "all" | "loopback" | "address"

export function bindingOf(ip: string | undefined): Binding {
  if (!ip || ip === "0.0.0.0" || ip === "::") return "all"
  if (ip === "127.0.0.1" || ip === "::1" || ip.startsWith("127.")) return "loopback"
  return "address"
}

/** One way into the stack: a port on the host, and the services that answer it. */
export type WayIn = {
  key: string
  binding: Binding
  /** The address as written, for a binding to one interface. */
  ip?: string
  hostPort: number
  protocol: string
  /** The services behind it, each with the port inside its container. */
  targets: { service: string; port: number }[]
}

/**
 * The stack's published ports, one per port on the host. Docker lists a
 * port bound to every interface twice, once for IPv4 and once for IPv6, and
 * the two are one way in.
 */
export function waysIn(readings: Pick<ServiceReading, "key" | "service">[]): WayIn[] {
  const ways = new Map<string, WayIn>()
  for (const reading of readings) {
    for (const port of reading.service.ports as ContainerPort[]) {
      if (!port.publicPort) continue
      const binding = bindingOf(port.ip)
      const key = `${binding === "address" ? port.ip : binding}:${port.publicPort}/${port.type}`
      const way = ways.get(key) ?? {
        key,
        binding,
        ip: binding === "address" ? port.ip : undefined,
        hostPort: port.publicPort,
        protocol: port.type,
        targets: [],
      }
      if (!way.targets.some((t) => t.service === reading.key && t.port === port.privatePort))
        way.targets.push({ service: reading.key, port: port.privatePort })
      ways.set(key, way)
    }
  }
  return [...ways.values()].sort(
    (a, b) => a.hostPort - b.hostPort || a.protocol.localeCompare(b.protocol),
  )
}

/** A Docker network the stack's containers are on, and which of them. */
export type StackNetwork = { name: string; services: string[] }

/**
 * The networks the stack's containers are attached to, read from the
 * socket's copy of each container. Compose names a project's networks
 * `<project>_<name>`; one shared with other stacks keeps its own name.
 */
export function stackNetworks(readings: Pick<ServiceReading, "key" | "container">[]) {
  const networks = new Map<string, string[]>()
  for (const reading of readings) {
    for (const name of reading.container?.networks ?? []) {
      const list = networks.get(name) ?? []
      list.push(reading.key)
      networks.set(name, list)
    }
  }
  return [...networks.entries()]
    .map(([name, services]): StackNetwork => ({ name, services: services.sort() }))
    .sort((a, b) => b.services.length - a.services.length || a.name.localeCompare(b.name))
}

/** A project's network without the project's prefix: `shop_backend` is `backend` in `shop`. */
export function networkShortName(network: string, stack: string) {
  return network.startsWith(`${stack}_`) ? network.slice(stack.length + 1) : network
}
