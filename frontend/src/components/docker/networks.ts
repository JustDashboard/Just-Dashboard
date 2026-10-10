import { hueFor, LANES } from "@/lib/hue"
import type {
  Container,
  DockerEvent,
  DockerNetwork,
  DockerNetworkingInfo,
  NetworkEndpoint,
} from "@/lib/types"

/**
 * What the Networks page reads off a network, a container and the daemon's
 * events, kept apart from the components so each rule is tested once: who
 * made a network, its colour, the address space it took, the names a member
 * answers to and what just changed.
 */

/** The three Docker creates and will not let you remove. */
export const SYSTEM_NETWORKS = ["bridge", "host", "none"]

export function isSystem(network: Pick<DockerNetwork, "name">) {
  return SYSTEM_NETWORKS.includes(network.name)
}

export type OwnerKind = "compose" | "dashboard" | "standalone" | "docker"

export type NetworkOwner = {
  kind: OwnerKind
  /** As the second line of a row says it: `compose · shop`, `standalone`. */
  label: string
  project?: string
}

/** Each kind as its chip names it, in the order the chips are drawn. */
export const OWNER_KINDS: { kind: OwnerKind; label: string; title: string }[] = [
  { kind: "compose", label: "Compose", title: "Made by a compose project" },
  { kind: "standalone", label: "Standalone", title: "Made with docker network create" },
  { kind: "dashboard", label: "Just Dashboard", title: "Made by this dashboard for a deployment" },
  { kind: "docker", label: "Docker's own", title: "bridge, host and none: made by Docker" },
]

/**
 * Who made a network, from its labels: Docker's own three, a compose project
 * (whose `docker compose down` will remove it), this dashboard for a
 * deployment's database, or anything else — `docker network create`, or a
 * tool that does the same.
 */
export function networkOwner(network: Pick<DockerNetwork, "name" | "labels">): NetworkOwner {
  if (isSystem(network)) return { kind: "docker", label: "Docker's own" }
  const project = network.labels?.["com.docker.compose.project"]
  if (project) return { kind: "compose", label: `compose · ${project}`, project }
  if (network.labels?.["io.just-dashboard.managed"] === "true") {
    return {
      kind: "dashboard",
      label: network.labels["io.just-dashboard.database-network"]
        ? "a deployment's database network"
        : "made by this dashboard",
    }
  }
  return { kind: "standalone", label: "standalone" }
}

/**
 * A network's colour wherever it is drawn — its row's key, its span of the
 * traffic bar, its blocks of the address pool, a member's chip — so a network
 * is found across the page by its colour before its name is read. From the
 * lane hues, which no state uses (§3); Docker's own three take slate, since
 * nothing about them is chosen.
 */
export function networkHue(name: string) {
  return SYSTEM_NETWORKS.includes(name) ? "var(--tag-slate)" : hueFor(name, LANES)
}

/** Every network a container is on, with nothing attached last and Docker's own after them. */
export function networkOrder(a: DockerNetwork, b: DockerNetwork) {
  const rank = (n: DockerNetwork) => (isSystem(n) ? 2 : n.usedBy.length === 0 ? 1 : 0)
  return rank(a) - rank(b) || a.name.localeCompare(b.name)
}

/**
 * Whether Docker refuses `docker network connect` here. Only a swarm network
 * made without `--attachable` does; a local network takes a container at any
 * time, compose's included, whatever its `attachable` flag says.
 */
export function refusesAttach(network: Pick<DockerNetwork, "scope" | "attachable">) {
  return network.scope === "swarm" && !network.attachable
}

/** Removable from this page: not Docker's own, and nothing attached (Docker refuses otherwise). */
export function isUnused(network: DockerNetwork) {
  return !isSystem(network) && network.usedBy.length === 0
}

// ------------------------------------------------------------- addresses ---

type Range = { start: number; end: number; bits: number }

/** An IPv4 network as the first and last address it spans; anything else is null. */
export function parseV4(cidr: string): Range | null {
  const match = cidr.trim().match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(?:\/(\d{1,2}))?$/)
  if (!match) return null
  const octets = match.slice(1, 5).map(Number)
  const bits = match[5] === undefined ? 32 : Number(match[5])
  if (octets.some((o) => o > 255) || bits > 32) return null
  const address = octets.reduce((n, o) => n * 256 + o, 0)
  const size = 2 ** (32 - bits)
  const start = Math.floor(address / size) * size
  return { start, end: start + size - 1, bits }
}

export function v4(n: number) {
  return [24, 16, 8, 0].map((shift) => Math.floor(n / 2 ** shift) % 256).join(".")
}

/** How many addresses a container can be given on a subnet: less the network, broadcast and gateway. */
export function hostCapacity(cidr: string) {
  const range = parseV4(cidr)
  if (!range) return 0
  return Math.max(range.end - range.start + 1 - 3, 0)
}

/**
 * Docker's own pools, used when `default-address-pools` is not set: fifteen
 * /16s across 172.17–172.31 and sixteen /20s of 192.168.0.0/16 — thirty-one
 * networks, after which `docker network create` and `compose up` fail with
 * "could not find an available, non-overlapping IPv4 address pool".
 */
export const BUILTIN_POOLS = [
  { base: "172.17.0.0/16", size: 16 },
  { base: "172.18.0.0/16", size: 16 },
  { base: "172.19.0.0/16", size: 16 },
  { base: "172.20.0.0/14", size: 16 },
  { base: "172.24.0.0/14", size: 16 },
  { base: "172.28.0.0/14", size: 16 },
  { base: "192.168.0.0/16", size: 20 },
]

/** Past this many blocks the strip draws its start, where Docker allocates first. */
export const SHOWN_BLOCKS = 64

export type PoolBlock = {
  cidr: string
  /** The network holding it; a block can be held by a subnet carved inside it. */
  network?: string
}

export type AddressPlan = {
  /** Whether these are Docker's built-in pools rather than `default-address-pools`. */
  builtin: boolean
  pools: { base: string; size: number }[]
  total: number
  used: number
  /** The strip: every block when there are few, the start of the pool otherwise. */
  blocks: PoolBlock[]
  /** The first block nothing holds, which a network made without a subnet is carved from. */
  firstFree?: string
  /** IPv4 subnets that are in no pool: picked by hand, as a macvlan on the LAN is. */
  outside: { network: string; cidr: string }[]
  /** used ÷ total at or past this is said in amber, and a full pool in red. */
  pressure: "ok" | "warning" | "full"
}

/**
 * The address space Docker carves networks from, block by block, and which
 * network holds each. A network made without a subnet takes the first free
 * block, so the strip fills from its start; one made with a subnet holds
 * whatever block it falls in, and one outside every pool is listed apart.
 */
export function addressPlan(
  info: DockerNetworkingInfo | undefined,
  networks: Pick<DockerNetwork, "name" | "subnets">[],
): AddressPlan {
  const configured = (info?.DefaultAddressPools ?? []).filter((p) => parseV4(p.Base))
  const builtin = configured.length === 0
  const pools = builtin ? BUILTIN_POOLS : configured.map((p) => ({ base: p.Base, size: p.Size }))

  const subnets = networks.flatMap((n) =>
    n.subnets.flatMap((cidr) => {
      const range = parseV4(cidr)
      return range ? [{ network: n.name, cidr, range }] : []
    }),
  )

  let total = 0
  const held = new Map<number, string>()
  const inPool = new Set<string>()
  const located: { index: number; start: number; size: number }[] = []
  for (const pool of pools) {
    const range = parseV4(pool.base)!
    const size = 2 ** (32 - Math.max(pool.size, range.bits))
    const count = (range.end - range.start + 1) / size
    located.push({ index: total, start: range.start, size })
    for (const s of subnets) {
      if (s.range.end < range.start || s.range.start > range.end) continue
      inPool.add(s.cidr)
      const first = Math.floor((Math.max(s.range.start, range.start) - range.start) / size)
      const last = Math.floor((Math.min(s.range.end, range.end) - range.start) / size)
      for (let i = first; i <= last; i++) {
        if (!held.has(total + i)) held.set(total + i, s.network)
      }
    }
    total += count
  }

  const blockAt = (index: number) => {
    const pool = [...located].reverse().find((p) => p.index <= index)!
    const start = pool.start + (index - pool.index) * pool.size
    return `${v4(start)}/${32 - Math.log2(pool.size)}`
  }

  let firstFree: string | undefined
  for (let i = 0; i < total; i++) {
    if (!held.has(i)) {
      firstFree = blockAt(i)
      break
    }
  }

  let lastHeld = -1
  for (const index of held.keys()) if (index > lastHeld) lastHeld = index
  const shown =
    total <= SHOWN_BLOCKS
      ? total
      : Math.min(SHOWN_BLOCKS, Math.max(32, Math.ceil((lastHeld + 2) / 8) * 8))
  const blocks = Array.from({ length: shown }, (_, i) => ({
    cidr: blockAt(i),
    network: held.get(i),
  }))
  const used = held.size
  return {
    builtin,
    pools,
    total,
    used,
    blocks,
    firstFree,
    outside: subnets
      .filter((s) => !inPool.has(s.cidr))
      .map((s) => ({ network: s.network, cidr: s.cidr })),
    pressure: used >= total ? "full" : used / total >= 0.8 ? "warning" : "ok",
  }
}

// ----------------------------------------------------------------- names ---

/**
 * What the others on a network can call a container, and when there is
 * nothing, why. On a network a person or compose made, Docker's DNS answers
 * to the container's name and its compose service (and any alias given at
 * attach, which only the network's own inspect lists). On the default bridge
 * it answers to nothing — the commonest reason "db" does not resolve — and on
 * the host network a container is the host.
 */
export function answersTo(
  container: Pick<Container, "name" | "composeService">,
  network: Pick<DockerNetwork, "name" | "driver">,
): { names: string[]; why?: string } {
  if (network.driver === "host" || network.name === "host") {
    return { names: [], why: "shares the host's own addresses" }
  }
  if (network.driver === "null" || network.name === "none") return { names: [], why: "no network" }
  if (network.name === "bridge") return { names: [], why: "no names on the default bridge" }
  const names = [container.composeService, container.name].filter(
    (n, i, all): n is string => Boolean(n) && all.indexOf(n) === i,
  )
  return { names }
}

/** Each container's place on every network, from the listing's endpoints. */
export function endpointsByContainer(networks: DockerNetwork[]) {
  const out = new Map<string, { network: DockerNetwork; endpoint: NetworkEndpoint }[]>()
  for (const network of networks) {
    for (const endpoint of network.endpoints ?? []) {
      const list = out.get(endpoint.container) ?? []
      list.push({ network, endpoint })
      out.set(endpoint.container, list)
    }
  }
  for (const list of out.values()) list.sort((a, b) => networkOrder(a.network, b.network))
  return out
}

/** An address without its prefix, for a cell that already says which network it is on. */
export function bareAddress(cidr: string | undefined) {
  return cidr ? cidr.replace(/\/\d+$/, "") : undefined
}

// ---------------------------------------------------------------- recent ---

export type NetworkChange = {
  key: string
  at: number
  action: "joined" | "left" | "created" | "deleted"
  network: string
  networkId?: string
  /** The container's name, or undefined for one that no longer exists. */
  container?: string
  containerId?: string
}

const ACTIONS: Record<string, NetworkChange["action"]> = {
  connect: "joined",
  disconnect: "left",
  create: "created",
  destroy: "deleted",
}

/**
 * Docker's network events as what happened, newest first: who joined or left
 * which network, and which networks were made or removed. The event names a
 * container only by id; it is named from the listing, and one that is gone
 * since is "a container". A recreate leaves and rejoins within seconds; both
 * are kept, because both happened and the second is the state now.
 */
export function networkChanges(
  events: DockerEvent[],
  containers: Pick<Container, "id" | "name">[],
  limit = 6,
): NetworkChange[] {
  const names = new Map(containers.map((c) => [c.id, c.name]))
  const seen = new Set<string>()
  const out: NetworkChange[] = []
  for (const ev of events) {
    const action = ACTIONS[ev.action]
    if (ev.type !== "network" || !action) continue
    const key = `${ev.time}|${ev.action}|${ev.id ?? ev.name}|${ev.container ?? ""}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push({
      key,
      at: Date.parse(ev.time),
      action,
      network: ev.name,
      networkId: ev.id,
      container: ev.container ? names.get(ev.container) : undefined,
      containerId: ev.container,
    })
  }
  return out.sort((a, b) => b.at - a.at).slice(0, limit)
}
