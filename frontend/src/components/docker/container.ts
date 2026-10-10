import { duration } from "@/lib/format"
import type {
  Container,
  ContainerDetail,
  ContainerHistoryPoint,
  FailureDiagnosis,
  PortExposure,
  PortRoute,
} from "@/lib/types"
import type { DotTone } from "@/components/status-dot"

/**
 * The words one container's page reads its container in: the verdict at the
 * end of its identity line, its exit, its restart policy, the containers it
 * keeps company with, and its ports joined to where they are reached from.
 * Pure, so `container.test.js` can hold each answer still.
 */

export type Verdict = {
  tone: DotTone
  /** The state as the reader asks it: is it working. */
  word: string
  /** What qualifies it: the health check, the exit, the loop. */
  detail?: string
  /** Whether the dot breathes, because the state is being kept rather than remembered. */
  live: boolean
}

/**
 * An exit status in words. 0 is a program that finished; 130, 137 and 143 are
 * the signals a stop sends (interrupt, kill, terminate), so a container
 * somebody stopped reads as stopped. Anything else is the program's own
 * failure.
 */
export function exitWords(code: number): string {
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

/** Whether an exit was the program failing rather than being stopped. */
export function crashed(code: number) {
  return ![0, 130, 137, 143].includes(code)
}

/**
 * What the container is doing, as one verdict. Docker's state alone calls a
 * container failing its health check "running" and a crash loop
 * "restarting", the word a deliberate restart also gets; the failure
 * diagnosis is what tells a loop from a restart.
 */
export function containerVerdict(
  detail: Pick<ContainerDetail, "state" | "health" | "hasHealthcheck" | "exitCode">,
  failure?: Pick<FailureDiagnosis, "state" | "restarts">,
): Verdict {
  const looping = failure?.state === "looping" || failure?.restarts.looping === true
  switch (detail.state) {
    case "running":
      if (looping) return { tone: "danger", word: "Restarting in a loop", live: true }
      if (detail.health === "unhealthy")
        return { tone: "danger", word: "Failing its health check", live: true }
      if (detail.health === "starting")
        return { tone: "warning", word: "Starting", detail: "checking its health", live: true }
      return {
        tone: "running",
        word: "Running",
        detail:
          detail.health === "healthy"
            ? "healthy"
            : detail.hasHealthcheck
              ? undefined
              : "no health check",
        live: true,
      }
    case "restarting":
      return looping
        ? {
            tone: "danger",
            word: "Restarting in a loop",
            detail: exitWords(detail.exitCode),
            live: true,
          }
        : { tone: "warning", word: "Restarting", live: true }
    case "paused":
      return { tone: "warning", word: "Paused", detail: "frozen in memory", live: false }
    case "created":
      return { tone: "stopped", word: "Never started", live: false }
    case "dead":
      return { tone: "danger", word: "Dead", detail: "Docker could not remove it", live: false }
    case "removing":
      return { tone: "warning", word: "Being removed", live: false }
    case "exited":
      return crashed(detail.exitCode)
        ? { tone: "danger", word: "Crashed", detail: exitWords(detail.exitCode), live: false }
        : { tone: "stopped", word: "Stopped", detail: exitWords(detail.exitCode), live: false }
    default:
      return { tone: "unknown", word: detail.state, live: false }
  }
}

/** How the container comes back, as the sentence its restart policy means. */
export function restartWords(policy: string | undefined): string {
  const [name, count] = (policy ?? "").split(":")
  switch (name) {
    case "always":
      return "always restarted"
    case "unless-stopped":
      return "restarted unless stopped"
    case "on-failure":
      return count ? `restarted on failure, up to ${count} times` : "restarted on failure"
    default:
      return "never restarted"
  }
}

/** How long a running container has been up, against a clock that ticks. */
export function upWords(startedAt: string | undefined, now: number): string | undefined {
  const since = startedAt ? Date.parse(startedAt) : NaN
  if (!Number.isFinite(since) || since <= 0) return undefined
  return `up ${duration(Math.max(0, (now - since) / 1000))}`
}

/**
 * When a container that is not running last changed, out of Docker's own
 * status sentence — "Exited (1) 3 hours ago" is "stopped 3 hours ago", and a
 * restart in progress counts from its last exit. The exit code is left out:
 * the verdict beside it already says it in words.
 */
export function sinceWords(state: string, status: string): string | undefined {
  const ago = status.replace(/^[A-Za-z]+\s*(\(\d+\))?\s*/, "").trim()
  if (!/ ago$/.test(ago)) return undefined
  const when = ago.charAt(0).toLowerCase() + ago.slice(1)
  if (state === "exited") return `stopped ${when}`
  if (state === "restarting") return `last exit ${when}`
  return undefined
}

/** The networks Docker makes for everything; sharing one says nothing about two containers. */
const SHARED_BY_ALL = new Set(["bridge", "host", "none"])

export type Company = {
  /** What the containers have in common, which the table is titled by. */
  kind: "stack" | "network"
  name: string
  containers: Container[]
}

/**
 * The containers this one keeps company with: its compose project, which is
 * what an operator means by "the app", or else the containers on its own
 * networks, the ones it reaches by name. A container alone on Docker's
 * default bridge has no company worth a table.
 *
 * The container itself stays in the list, in its place, so the table reads as
 * the project with this row marked rather than as everything but it.
 */
export function companyOf(
  container: Pick<Container, "id" | "composeStack" | "networks">,
  all: Container[],
): Company | undefined {
  if (container.composeStack) {
    const members = all.filter((one) => one.composeStack === container.composeStack)
    if (members.length > 1)
      return { kind: "stack", name: container.composeStack, containers: byService(members) }
  }
  const networks = (container.networks ?? []).filter((name) => !SHARED_BY_ALL.has(name))
  if (networks.length === 0) return undefined
  const members = all.filter(
    (one) => one.id === container.id || one.networks.some((name) => networks.includes(name)),
  )
  if (members.length <= 1) return undefined
  return { kind: "network", name: networks[0], containers: byService(members) }
}

/** By compose service where there is one, so a project reads in the order its file names it. */
function byService(members: Container[]) {
  return [...members].sort((a, b) =>
    (a.composeService ?? a.name).localeCompare(b.composeService ?? b.name),
  )
}

export type PortRow = {
  key: string
  /** `127.0.0.1:5678`, or the bare port where it is on every interface. */
  published: string
  hostPort?: number
  containerPort: number
  protocol: string
  scope: PortExposure["scope"]
  route?: PortRoute
}

/**
 * The ports, each once: Docker's binding joined to the route the server traced
 * from it through the proxy and the firewall. The binding is a fact the
 * container always has; the route is a conclusion that arrives later, so a
 * port is drawn from the first and annotated by the second.
 */
export function portRows(exposure: PortExposure[], routes: PortRoute[] | undefined): PortRow[] {
  const key = (ip: string | undefined, port: number | undefined, protocol: string) =>
    `${ip ?? ""}:${port ?? ""}/${protocol}`
  const traced = new Map(
    (routes ?? []).map((route) => [key(route.hostIp, route.hostPort, route.protocol), route]),
  )
  return exposure.map((port) => {
    const id = key(port.hostIp, port.hostPort, port.protocol)
    const ip = port.hostIp?.includes(":") ? `[${port.hostIp}]` : port.hostIp
    return {
      key: `${id}>${port.containerPort}`,
      published:
        port.hostPort === undefined
          ? "not published"
          : port.scope === "all" || !ip
            ? String(port.hostPort)
            : `${ip}:${port.hostPort}`,
      hostPort: port.hostPort,
      containerPort: port.containerPort,
      protocol: port.protocol,
      scope: port.scope,
      route: traced.get(id),
    }
  })
}

/**
 * Where a port is reached from, in words, with the tone that deserves. A port
 * the internet reaches around the firewall is amber: it works, and it should
 * not be relied on.
 */
export function reachWords(row: Pick<PortRow, "scope" | "route">): {
  word: string
  tone: DotTone
} {
  const route = row.route
  if (route?.reach === "proxied")
    return { word: route.vhost ? `through ${route.vhost}` : "through the proxy", tone: "running" }
  if (route?.reach === "external") return { word: "from anywhere", tone: "warning" }
  if (route?.reach === "blocked") return { word: "blocked by the firewall", tone: "stopped" }
  if (route?.reach === "server-only" || row.scope === "loopback")
    return { word: "from this server only", tone: "stopped" }
  if (row.scope === "internal") return { word: "from its networks only", tone: "stopped" }
  if (row.scope === "private") return { word: "from the private network", tone: "notice" }
  if (row.scope === "all") return { word: "from every interface", tone: "warning" }
  return { word: "not worked out", tone: "unknown" }
}

/**
 * One measurement out of a container's recorded buckets, as the line a tile
 * draws. A bucket with no reading (the first after a start) is left out
 * rather than drawn as a fall to zero.
 */
export function trendOf(
  points: ContainerHistoryPoint[] | undefined,
  read: (point: ContainerHistoryPoint) => number | null | undefined,
): number[] {
  const values: number[] = []
  for (const point of points ?? []) {
    const value = read(point)
    if (typeof value === "number" && Number.isFinite(value)) values.push(value)
  }
  return values
}

/** The busiest of a recorded series, for "peaked at" under a reading that has stopped. */
export function peak(values: number[]): number | undefined {
  return values.length > 0 ? Math.max(...values) : undefined
}

/**
 * An image reference as its repository and its tag. The registry's port is a
 * colon too, so the tag is what follows the last colon after the last slash.
 */
export function splitImage(image: string): [string, string | undefined] {
  const at = image.indexOf("@")
  const reference = at === -1 ? image : image.slice(0, at)
  const slash = reference.lastIndexOf("/")
  const colon = reference.lastIndexOf(":")
  if (colon > slash) return [reference.slice(0, colon), reference.slice(colon + 1)]
  return [reference, at === -1 ? undefined : image.slice(at + 1, at + 20)]
}

export type EnvKind = "credential" | "address" | "path" | "flag" | "number" | "text"

/** The kinds in the order the Environment's chips draw them. */
export const ENV_KINDS: { kind: EnvKind; label: string }[] = [
  { kind: "credential", label: "Credentials" },
  { kind: "address", label: "Addresses" },
  { kind: "path", label: "Paths" },
  { kind: "flag", label: "Switches" },
  { kind: "number", label: "Numbers" },
  { kind: "text", label: "Text" },
]

/**
 * What an environment value is, read from the value rather than the name —
 * except for a credential, which is decided by its name (`isSecretEnvKey`)
 * because a hidden value cannot be read. An address is somewhere the
 * application connects to: a URL, or a host with or without its port.
 */
export function envKind(value: string, secret: boolean): EnvKind {
  if (secret) return "credential"
  const v = value.trim()
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(v)) return "address"
  if (/^(true|false|yes|no|on|off)$/i.test(v)) return "flag"
  if (/^-?\d+(\.\d+)?$/.test(v)) return "number"
  if (v.startsWith("/") || v.startsWith("./")) return "path"
  if (/^[a-z0-9-]+(\.[a-z0-9-]+)+(:\d+)?$/i.test(v) || /^[a-z0-9-]+:\d+$/i.test(v)) return "address"
  return "text"
}

/**
 * The namespace a variable's name opens with — `DB` of `DB_POSTGRESDB_HOST`,
 * `N8N` of `N8N_HOST` — which names the part of the application it sets.
 * A name with no underscore is its own namespace.
 */
export function envPrefix(name: string): string {
  const at = name.indexOf("_")
  return at > 0 ? name.slice(0, at) : name
}
