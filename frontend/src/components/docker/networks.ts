import { membersKnown } from "@/lib/docker-networks"
import { hueFor, LANES } from "@/lib/hue"
import type {
  Container,
  DockerEvent,
  DockerNetwork,
  DockerNetworkingInfo,
  NetworkEndpoint,
  NetworkOwner as BackendOwner,
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
  {
    kind: "dashboard",
    label: "Just Dashboard",
    title: "Made by this dashboard, for itself or for a deployment",
  },
  { kind: "docker", label: "Docker's own", title: "bridge, host and none: made by Docker" },
]

/**
 * What a backend owner is called where a row says who made the network. A
 * deployment's networks are the dashboard's own work, so they share its chip
 * and say which deployment on the line.
 */
export function ownerLabel(owner: BackendOwner): string {
  switch (owner.kind) {
    case "system":
      return "Docker's own"
    case "dashboard":
      return "made by this dashboard"
    case "database-link":
      return owner.deployment ? `database link · ${owner.deployment}` : "database link"
    case "deployment":
      return owner.deployment ? `deployment · ${owner.deployment}` : "deployment · gone"
    case "compose":
      return owner.project ? `compose · ${owner.project}` : "compose"
    case "manual":
      return "standalone"
  }
}

const OWNER_CHIP: Record<BackendOwner["kind"], OwnerKind> = {
  system: "docker",
  dashboard: "dashboard",
  "database-link": "dashboard",
  deployment: "dashboard",
  compose: "compose",
  manual: "standalone",
}

/**
 * Who made a network. The backend's reading of its labels where it has made
 * one — it alone can tell a deployment's network from a database link, and
 * the dashboard's own stack from a compose project — and otherwise the
 * labels themselves: Docker's own three, a compose project (whose `docker
 * compose down` will remove it), this dashboard for a deployment's database,
 * or anything else — `docker network create`, or a tool that does the same.
 */
export function networkOwner(
  network: Pick<DockerNetwork, "name" | "labels"> & { owner?: BackendOwner },
): NetworkOwner {
  if (network.owner) {
    return {
      kind: OWNER_CHIP[network.owner.kind],
      label: ownerLabel(network.owner),
      project: network.owner.project,
    }
  }
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
  const rank = (n: DockerNetwork) => (isSystem(n) ? 2 : isUnused(n) ? 1 : 0)
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

/**
 * Removable from this page: not Docker's own, and nothing attached (Docker
 * refuses otherwise). Members that could not be read are not none: a failed
 * container listing leaves `usedBy` empty, and a network must never look
 * unused for that.
 */
export function isUnused(network: DockerNetwork) {
  return !isSystem(network) && membersKnown(network) && network.usedBy.length === 0
}

/** The dashboard's own private network, which takes no other container. */
export function isDashboardOwn(network: Pick<DockerNetwork, "owner">) {
  return network.owner?.kind === "dashboard"
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

// ---------------------------------------------------------------- a page ---

/** A network's own page; a view other than the one it opens on is in the address. */
export function networkHref(name: string, tab?: string) {
  const path = `/docker/networks/${encodeURIComponent(name)}`
  return tab && tab !== "overview" ? `${path}?tab=${encodeURIComponent(tab)}` : path
}

/**
 * The network an address names: by its name, else by its id, as the Engine
 * resolves one. A deployment's removal plan and the old sheet's address both
 * name a network by id.
 */
export function findNetwork<T extends Pick<DockerNetwork, "id" | "name">>(
  networks: T[],
  key: string,
): T | undefined {
  return networks.find((n) => n.name === key) ?? networks.find((n) => n.id === key)
}

/** Where the table keeps its search and its owner chip for the tab, read by a network's rail too. */
export const NETWORK_FILTER_KEYS = {
  query: "docker.networks.query",
  shelf: "docker.networks.shelf",
} as const

export type Shelf = OwnerKind | "unused" | ""

const SHELVES = ["compose", "dashboard", "standalone", "docker", "unused"]

/** A remembered chip, or none where what was remembered is no chip. */
export function shelfOf(remembered: string): Shelf {
  return (SHELVES.includes(remembered) ? remembered : "") as Shelf
}

export function shelfLabel(shelf: Shelf) {
  if (shelf === "unused") return "Unused"
  return OWNER_KINDS.find((k) => k.kind === shelf)?.label ?? ""
}

/** The networks a search and an owner chip leave, in the order they were given. */
export function filterNetworks(networks: DockerNetwork[], query: string, shelf: Shelf) {
  const needle = query.trim().toLowerCase()
  return networks.filter((n) => {
    if (shelf === "unused" && !isUnused(n)) return false
    if (shelf && shelf !== "unused" && networkOwner(n).kind !== shelf) return false
    if (!needle) return true
    return (
      n.name.toLowerCase().includes(needle) ||
      n.driver.toLowerCase().includes(needle) ||
      n.subnets.some((s) => s.toLowerCase().includes(needle)) ||
      n.usedBy.some((c) => c.toLowerCase().includes(needle))
    )
  })
}

/**
 * Why a container cannot be attached from here, or undefined where one can:
 * Docker's own three and the host network take none this way, the dashboard's
 * private network takes no other, and a swarm network without `--attachable`
 * takes only services. The first two offer no control at all (`hidden`).
 */
export function attachRefusal(
  network: Pick<DockerNetwork, "name" | "driver" | "scope" | "attachable" | "owner">,
): { hidden: boolean; reason?: string } | undefined {
  if (isSystem(network) || network.driver === "host") return { hidden: true }
  if (isDashboardOwn(network)) {
    return {
      hidden: false,
      reason: "The dashboard's own private network takes no other containers",
    }
  }
  if (refusesAttach(network)) {
    return {
      hidden: false,
      reason: "A swarm network made without --attachable takes only services",
    }
  }
  return undefined
}

/** Why a network cannot be removed, or undefined where Docker would remove it. */
export function removeRefusal(network: DockerNetwork): string | undefined {
  if (isSystem(network)) return "Docker's own network — cannot be removed"
  if (!membersKnown(network)) return "Who uses it could not be read — cannot judge a removal"
  if (network.usedBy.length > 0) {
    return `In use by ${network.usedBy.length} container${network.usedBy.length === 1 ? "" : "s"} — cannot be removed while attached`
  }
  return undefined
}

export type NetworkStanding = {
  tone: "running" | "stopped" | "warning"
  label: string
}

/**
 * Where a network stands, as the word beside its name: unread where the
 * container listing failed, unused where nothing is attached to one somebody
 * made (it holds a block of the pool), and otherwise in use, or how many of
 * its members run where some do not.
 */
export function networkStanding(network: DockerNetwork, running?: number): NetworkStanding {
  if (!membersKnown(network)) return { tone: "warning", label: "members unread" }
  const count = network.usedBy.length
  if (count === 0) {
    return isSystem(network)
      ? { tone: "stopped", label: "nothing attached" }
      : { tone: "warning", label: "unused" }
  }
  if (running === undefined || running >= count) return { tone: "running", label: "in use" }
  return { tone: "warning", label: `${running} of ${count} running` }
}

/** A member as a network's page reads it, from the inspect or, before it lands, the listing. */
export type MemberReading = {
  id: string
  name: string
  ipv4?: string
  ipv6?: string
  /** Undefined until the network's own inspect has listed them. */
  aliases?: string[]
  state: string
  /** The other networks it is on: what joins this network to them. */
  others: string[]
  unread?: boolean
  ingress?: boolean
  dashboard?: boolean
}

type InspectedMember = {
  id: string
  name: string
  ipv4?: string
  ipv6?: string
  aliases: string[]
  state?: string
  unread?: boolean
  networks?: string[]
  ingress?: boolean
  dashboard?: boolean
}

/**
 * Who is on a network. The inspect's members where it has landed, with each
 * one's state as the containers socket has it now; before that the listing's
 * endpoints, so the page draws who is there without waiting for the inspect
 * (which reads every member for its aliases).
 */
export function memberReadings(
  network: Pick<DockerNetwork, "name" | "endpoints"> & { members?: InspectedMember[] },
  containers: Map<string, Pick<Container, "state" | "networks">>,
): MemberReading[] {
  const others = (id: string, known?: string[]) =>
    (known ?? containers.get(id)?.networks ?? []).filter((name) => name !== network.name)
  if (network.members) {
    return network.members.map((m) => ({
      id: m.id,
      name: m.name,
      ipv4: m.ipv4,
      ipv6: m.ipv6,
      aliases: m.aliases,
      state: containers.get(m.id)?.state ?? m.state ?? "unknown",
      others: others(m.id, m.networks),
      unread: m.unread,
      ingress: m.ingress,
      dashboard: m.dashboard,
    }))
  }
  return (network.endpoints ?? []).map((e) => ({
    id: e.container,
    name: e.name,
    ipv4: e.ipv4,
    ipv6: e.ipv6,
    state: containers.get(e.container)?.state ?? "unknown",
    others: others(e.container),
  }))
}

/** The names the others on the network reach a member by, without the short id Docker adds. */
export function memberNames(member: Pick<MemberReading, "id" | "name" | "aliases">) {
  const names = (member.aliases ?? []).filter((alias) => !member.id.startsWith(alias))
  return names.length > 0 ? [...new Set(names)] : [member.name]
}

export type JoinedNetwork = {
  name: string
  /** The members on both, which is how traffic gets from one to the other. */
  through: string[]
  /** Whether any of them is running: a join through stopped containers carries nothing. */
  up: boolean
}

/** The other networks a network's members are on, the most-joined first. */
export function joinedNetworks(
  members: Pick<MemberReading, "name" | "others" | "state">[],
): JoinedNetwork[] {
  const out = new Map<string, JoinedNetwork>()
  for (const member of members) {
    for (const name of member.others) {
      const joined = out.get(name) ?? { name, through: [], up: false }
      joined.through.push(member.name)
      joined.up ||= member.state === "running"
      out.set(name, joined)
    }
  }
  return [...out.values()].sort(
    (a, b) => b.through.length - a.through.length || a.name.localeCompare(b.name),
  )
}

export type WayOut =
  /** Through this server's address on the network's bridge. */
  | { kind: "gateway"; gateway?: string }
  /** Straight onto a network card's own network: a macvlan or ipvlan. */
  | { kind: "parent"; device?: string; gateway?: string }
  /** `--internal`: its members reach each other and nothing beyond. */
  | { kind: "internal" }
  /** The host network: its containers are on the server's own interfaces. */
  | { kind: "host" }
  /** The `none` network: no interface at all. */
  | { kind: "none" }

/** How a network's members leave it, which is the other end of its picture. */
export function wayOut(
  network: Pick<DockerNetwork, "driver" | "internal" | "gateway"> & {
    options?: Record<string, string>
  },
): WayOut {
  if (network.driver === "host") return { kind: "host" }
  if (network.driver === "null") return { kind: "none" }
  if (network.internal) return { kind: "internal" }
  // The card is one of the driver's options, which only the inspect has: it
  // is a card's network either way, and named once the inspect lands.
  if (network.driver === "macvlan" || network.driver === "ipvlan") {
    return { kind: "parent", device: network.options?.parent, gateway: network.gateway }
  }
  return { kind: "gateway", gateway: network.gateway }
}
