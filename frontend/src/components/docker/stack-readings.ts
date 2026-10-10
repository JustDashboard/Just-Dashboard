import type {
  ComposeService,
  ComposeStack,
  Container,
  ContainerStats,
  DockerEvent,
} from "@/lib/types"
import { hueFor, LANES } from "@/lib/hue"
import type { DotTone } from "@/components/status-dot"

/**
 * What the Stacks page reads off the stack list, the containers socket and
 * the daemon's events: which chip a stack is counted under, each stack's
 * services joined to their live containers and summed, and the last things
 * that happened to them.
 */

/**
 * A stack's colour: its name's lane, the same on every visit. A compose
 * project is a namespace, which is what lanes name (§15), and it is what ties
 * a stack's containers in the table to its name in Recent.
 */
export function stackLane(stack: string) {
  return hueFor(stack, LANES)
}

/** Where a stack sits for the page's state chips. Every stack is in exactly one. */
export type StackBucket = "attention" | "running" | "stopped" | "undeployed"

/** A stack the dashboard has something to act on: a bad service, a part down, or a leftover. */
export function needsAttention(stack: ComposeStack) {
  return (
    stack.orphans.length > 0 ||
    stack.services.some((s) => s.health === "unhealthy") ||
    stack.state === "degraded" ||
    stack.state === "partial"
  )
}

export function stackBucket(stack: ComposeStack): StackBucket {
  if (needsAttention(stack)) return "attention"
  if (stack.running > 0) return "running"
  if (stack.deployed) return "stopped"
  return "undeployed"
}

const BUCKET_RANK: Record<StackBucket, number> = {
  attention: 0,
  running: 1,
  stopped: 2,
  undeployed: 3,
}

/** One row under a stack: the compose service, and its container where one exists. */
export type ServiceLine = {
  /** The container's id, or the stack and service for one compose never created. */
  key: string
  service: ComposeService
  /** The socket's copy, which moves the moment Docker does; the stack list polls. */
  container?: Container
  stat?: ContainerStats
  /** Running under this project name with no service in the compose file. */
  orphan: boolean
  state: string
  health?: string
}

export type StackLine = {
  stack: ComposeStack
  bucket: StackBucket
  lines: ServiceLine[]
  /** Percent of one core, summed over the stack's measured containers. */
  cpu: number
  memory: number
  /** Whether any running container has a reading yet. */
  measured: boolean
}

/** Worst first: failing its check, then down, then never created, then up. */
function serviceRank(line: ServiceLine) {
  if (line.health === "unhealthy") return 0
  if (line.service.missing) return 2
  if (line.state !== "running") return 1
  return 3
}

/**
 * Every stack with its services joined to the containers socket, worst first:
 * the stacks that need attention, then those running, those stopped, and the
 * compose files never deployed — each in name order, so a stack does not move
 * because a reading did.
 */
export function stackLines(
  stacks: ComposeStack[],
  containers: Container[],
  stats: Record<string, ContainerStats>,
): StackLine[] {
  const byId = new Map(containers.map((c) => [c.id, c]))
  return stacks
    .map((stack) => {
      const orphans = new Set(stack.orphans)
      const lines = stack.services
        .map((service): ServiceLine => {
          const container = service.container ? byId.get(service.container) : undefined
          return {
            key: service.container || `${stack.name}/${service.name}`,
            service,
            container,
            stat: service.container ? stats[service.container] : undefined,
            orphan: orphans.has(service.name),
            state: container?.state ?? service.state,
            health: container ? container.health : service.health,
          }
        })
        .sort(
          (a, b) => serviceRank(a) - serviceRank(b) || a.service.name.localeCompare(b.service.name),
        )
      let cpu = 0
      let memory = 0
      let measured = false
      for (const line of lines) {
        if (line.state !== "running" || !line.stat) continue
        memory += line.stat.memUsage
        if (line.stat.cpuReady === false) continue
        cpu += line.stat.cpuPercent
        measured = true
      }
      return { stack, bucket: stackBucket(stack), lines, cpu, memory, measured }
    })
    .sort(
      (a, b) =>
        BUCKET_RANK[a.bucket] - BUCKET_RANK[b.bucket] || a.stack.name.localeCompare(b.stack.name),
    )
}

/** The exit code Docker wrote into a stopped container's status, "Exited (137) 3 minutes ago". */
export function exitCode(status: string): number | undefined {
  const match = status.match(/^Exited \((\d+)\)/i)
  return match ? Number(match[1]) : undefined
}

/** A change to one of the stacks' containers, as one line of the Recent block. */
export type StackChange = {
  key: string
  /** Milliseconds since the epoch. */
  at: number
  stack: string
  service: string
  image?: string
  verb: string
  tone: DotTone
}

/** An oom and the die it causes are one event a second apart, and are said as one. */
const FOLD_MS = 5000

/**
 * The daemon's events that changed whether a stack's service is serving,
 * newest first. A create, a kill and a network attach are the steps of
 * something else — the start or the exit that follows says what happened —
 * so they are left out, and an out-of-memory kill and the exit it causes are
 * one line rather than two.
 */
export function stackChanges(events: DockerEvent[]): StackChange[] {
  const out: StackChange[] = []
  const sorted = events
    .filter((e) => e.type === "container" && e.stack)
    .map((e) => ({ event: e, at: Date.parse(e.time) }))
    .sort((a, b) => b.at - a.at)
  for (const { event, at } of sorted) {
    const said = changeWords(event)
    if (!said) continue
    const service = event.service || event.name
    const previous = out[out.length - 1]
    if (
      previous &&
      previous.stack === event.stack &&
      previous.service === service &&
      Math.abs(previous.at - at) <= FOLD_MS &&
      (event.action === "oom" || previous.verb === "out of memory")
    ) {
      out[out.length - 1] = { ...previous, verb: "killed for memory", tone: "danger" }
      continue
    }
    out.push({
      key: `${event.time}/${event.stack}/${service}/${event.action}`,
      at,
      stack: event.stack!,
      service,
      image: event.image,
      ...said,
    })
  }
  return out
}

function changeWords(event: DockerEvent): { verb: string; tone: DotTone } | undefined {
  const action = event.action
  if (action.startsWith("health_status")) {
    return action.endsWith("unhealthy")
      ? { verb: "unhealthy", tone: "danger" }
      : { verb: "healthy", tone: "running" }
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
    case "die":
      return event.level === "error" && event.exitCode && event.exitCode !== "0"
        ? { verb: `exited ${event.exitCode}`, tone: "danger" }
        : { verb: "exited", tone: "stopped" }
    case "stop":
      return { verb: "stopped", tone: "stopped" }
    case "pause":
      return { verb: "paused", tone: "stopped" }
    case "destroy":
      return { verb: "removed", tone: "stopped" }
    default:
      return undefined
  }
}
