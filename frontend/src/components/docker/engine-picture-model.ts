import { bytes, plural } from "@/lib/format"
import type { ComposeStack, Container, DockerNetwork, VolumeDetail } from "@/lib/types"
import type { WireState } from "@/components/wire-flow-model"
import {
  containerBucket,
  exitCodeOf,
  exitedWhen,
  exitWords,
  type ContainerBucket,
} from "@/components/docker/containers"

/**
 * What the Docker overview's picture draws, kept apart from React so it is
 * tested in `bun test src`: the compose projects and the containers started
 * by hand on one side of the engine, the networks and volumes it keeps on the
 * other, each with the state its line is drawn in, what it says in a few
 * words, and which nodes on the other side it travels with.
 */

export type EngineKind = "stack" | "container" | "network" | "volume"

export type EngineNode = {
  /** `stack:shop`, `container:<id>`, `network:<id>`, `volume:<name>`. */
  id: string
  kind: EngineKind
  name: string
  /** The part of the name that is its project's, drawn quieter: `shop_` of `shop_pgdata`. */
  prefix?: string
  href: string
  /** The line between it and the engine (`WireState`). */
  state: WireState
  /** The reading under its name. */
  words: string
  /** The image references its mark is drawn from: a project's services, a container's own. */
  images: string[]
  /** The nodes across the engine that light with it: a project's networks and volumes. */
  peers: string[]
}

export type EnginePicture = {
  start: EngineNode[]
  end: EngineNode[]
  /** The names each side had no room for, by kind, for its "more" node. */
  more: { start: string[]; networks: string[]; volumes: string[] }
  /** How many things each side holds in all, for the columns' counts. */
  totals: { stacks: number; standalone: number; networks: number; volumes: number }
}

/** How many nodes each side draws before the rest fold into one. */
export const ENGINE_ROOM = { start: 6, networks: 3, volumes: 3 } as const

/** Worst first: what broke, what should not be relied on, what runs, what is down, what never ran. */
const RANK: Record<WireState, number> = { danger: 0, warning: 1, up: 2, down: 3, idle: 4 }

const COMPOSE_PROJECT = "com.docker.compose.project"

/** Which of the Containers page's chips a container sits under: the one reading of "failing". */
type BucketOf = (container: Container) => ContainerBucket

/** A compose project's line: red while one of its containers is failing. */
function stackState(stack: ComposeStack, own: Container[], bucketOf: BucketOf): WireState {
  if (!stack.deployed) return "idle"
  const buckets = own.map(bucketOf)
  if (buckets.includes("failing") || stack.services.some((s) => s.health === "unhealthy")) {
    return "danger"
  }
  if (stack.state === "partial" || stack.state === "degraded" || buckets.includes("starting")) {
    return "warning"
  }
  return stack.running > 0 ? "up" : "down"
}

function stackWords(stack: ComposeStack, own: Container[], bucketOf: BucketOf): string {
  if (!stack.deployed) return `not deployed · ${plural(stack.total, "service")} defined`
  const failing = own.filter((c) => bucketOf(c) === "failing").length
  const total = stack.total || stack.services.length
  const up = `${stack.running} of ${total} running`
  if (failing > 0) return `${up} · ${failing} failing`
  if (stack.running === 0) return "stopped"
  return up
}

function containerState(bucket: ContainerBucket): WireState {
  if (bucket === "failing") return "danger"
  if (bucket === "starting") return "warning"
  return bucket === "running" ? "up" : "down"
}

function containerWords(container: Container, bucket: ContainerBucket, oom: boolean): string {
  if (container.state === "running") {
    if (container.health === "unhealthy") return "failing its health check"
    if (container.health === "starting") return "starting"
    return "running"
  }
  if (container.state === "restarting") return "restarting"
  if (container.state === "paused") return "paused"
  if (container.state === "created") return "never started"
  const when = exitedWhen(container)
  // A failure keeps how it ended; a stop somebody asked for is said as one.
  const how =
    bucket === "failing" ? (exitWords(exitCodeOf(container), oom) ?? container.state) : "stopped"
  return when ? `${how} · ${when}` : how
}

/** A network's line: carried while a container on it runs, dashed while nothing is on it. */
function networkState(network: DockerNetwork, running: Set<string>): WireState {
  if (network.membersKnown === false) return "idle"
  if (network.usedBy.length === 0) return "idle"
  return network.usedBy.some((name) => running.has(name)) ? "up" : "down"
}

function networkWords(network: DockerNetwork): string {
  if (network.membersKnown === false) return "members unread"
  const members =
    network.usedBy.length === 0 ? "no containers" : plural(network.usedBy.length, "container")
  return network.internal ? `${members} · internal` : members
}

/** A volume's line: amber where nothing mounts it, because a prune would delete it. */
function volumeState(volume: VolumeDetail, running: Set<string>): WireState {
  if (volume.usedBy.length === 0) return volume.mountType ? "idle" : "warning"
  return volume.usedBy.some((user) => running.has(user.name)) ? "up" : "down"
}

function volumeWords(volume: VolumeDetail): string {
  const size = volume.size > 0 ? bytes(volume.size) : undefined
  const held =
    volume.usedBy.length === 0
      ? volume.mountType
        ? "not mounted"
        : "nothing mounts it"
      : volume.usedBy.length === 1
        ? volume.usedBy[0].name
        : plural(volume.usedBy.length, "container")
  return [size, held].filter(Boolean).join(" · ")
}

/**
 * A volume's name as it is drawn: a Compose volume's project apart from the
 * rest, so the eye lands on what tells two of a project's volumes apart, and
 * an anonymous volume (sixty-four hex characters) as the twelve that name it.
 */
export function volumeName(volume: Pick<VolumeDetail, "name" | "labels">): {
  prefix?: string
  name: string
} {
  const project = volume.labels?.[COMPOSE_PROJECT]
  if (project && volume.name.startsWith(`${project}_`)) {
    return { prefix: `${project}_`, name: volume.name.slice(project.length + 1) }
  }
  return { name: /^[0-9a-f]{64}$/.test(volume.name) ? volume.name.slice(0, 12) : volume.name }
}

/**
 * The picture's two sides. `networks` and `volumes` are undefined until they
 * are read, and a side that was refused is drawn without them rather than as
 * empty.
 */
export function enginePicture({
  containers,
  stacks,
  networks,
  volumes,
  oomKilled,
}: {
  containers: Container[]
  stacks: ComposeStack[]
  networks?: DockerNetwork[]
  volumes?: VolumeDetail[]
  /** The containers whose last exit the kernel caused for memory, from the event log. */
  oomKilled?: Set<string>
}): EnginePicture {
  const bucketOf: BucketOf = (c) => containerBucket(c, oomKilled?.has(c.id))
  const running = new Set(containers.filter((c) => c.state === "running").map((c) => c.name))
  const byStack = new Map<string, Container[]>()
  for (const container of containers) {
    if (!container.composeStack) continue
    byStack.set(container.composeStack, [...(byStack.get(container.composeStack) ?? []), container])
  }
  // Who a network or a volume belongs to on the other side: the project of a
  // container on it, or the container itself when no project owns it.
  const ownerOf = new Map(
    containers.map((c) => [
      c.name,
      c.composeStack ? `stack:${c.composeStack}` : `container:${c.id}`,
    ]),
  )
  const known = new Set(stacks.map((s) => `stack:${s.name}`))

  const left: EngineNode[] = [
    ...stacks.map((stack): EngineNode => {
      const own = byStack.get(stack.name) ?? []
      return {
        id: `stack:${stack.name}`,
        kind: "stack",
        name: stack.name,
        href: `/docker/stacks/${encodeURIComponent(stack.name)}`,
        state: stackState(stack, own, bucketOf),
        words: stackWords(stack, own, bucketOf),
        images: stack.services.map((s) => s.image).filter(Boolean),
        peers: [],
      }
    }),
    // A project the stack list does not know still has containers: they are
    // drawn as themselves rather than left off the picture.
    ...containers
      .filter((c) => !c.composeStack || !known.has(`stack:${c.composeStack}`))
      .map((container): EngineNode => {
        const bucket = bucketOf(container)
        return {
          id: `container:${container.id}`,
          kind: "container",
          name: container.name,
          href: `/docker/containers/${encodeURIComponent(container.id)}`,
          state: containerState(bucket),
          words: containerWords(container, bucket, oomKilled?.has(container.id) ?? false),
          images: [container.image],
          peers: [],
        }
      }),
  ]
  const leftIds = new Set(left.map((node) => node.id))
  const ownersOf = (names: string[]) => [
    ...new Set(
      names.map((name) => ownerOf.get(name)).filter((id): id is string => !!id && leftIds.has(id)),
    ),
  ]

  const members = new Map((networks ?? []).map((n) => [`network:${n.id}`, n.usedBy.length]))
  const nets = (networks ?? [])
    // Docker's own three with nothing on them are furniture, not this server's wiring.
    .filter((n) => !(n.owner?.kind === "system" && n.usedBy.length === 0))
    .map((network): EngineNode => {
      const project = network.owner?.project ?? network.labels?.[COMPOSE_PROJECT]
      const peers = ownersOf(network.usedBy)
      if (project && leftIds.has(`stack:${project}`) && !peers.includes(`stack:${project}`)) {
        peers.push(`stack:${project}`)
      }
      return {
        id: `network:${network.id}`,
        kind: "network",
        name: network.name,
        href: `/docker/networks?network=${encodeURIComponent(network.id)}`,
        state: networkState(network, running),
        words: networkWords(network),
        images: [],
        peers,
      }
    })
    .sort(
      (a, b) =>
        RANK[a.state] - RANK[b.state] ||
        (members.get(b.id) ?? 0) - (members.get(a.id) ?? 0) ||
        a.name.localeCompare(b.name),
    )

  const sizeOf = new Map((volumes ?? []).map((v) => [`volume:${v.name}`, v.size]))
  const vols = (volumes ?? [])
    .map((volume): EngineNode => {
      const project = volume.labels?.[COMPOSE_PROJECT]
      const peers = ownersOf(volume.usedBy.map((user) => user.name))
      if (project && leftIds.has(`stack:${project}`) && !peers.includes(`stack:${project}`)) {
        peers.push(`stack:${project}`)
      }
      return {
        id: `volume:${volume.name}`,
        kind: "volume",
        ...volumeName(volume),
        href: `/docker/volumes/${encodeURIComponent(volume.name)}`,
        state: volumeState(volume, running),
        words: volumeWords(volume),
        images: [],
        peers,
      }
    })
    .sort(
      (a, b) =>
        RANK[a.state] - RANK[b.state] ||
        (sizeOf.get(b.id) ?? 0) - (sizeOf.get(a.id) ?? 0) ||
        a.name.localeCompare(b.name),
    )

  for (const node of [...nets, ...vols]) {
    for (const peer of node.peers) left.find((l) => l.id === peer)?.peers.push(node.id)
  }
  // Projects before the containers started by hand, within what needs
  // looking at first.
  left.sort(
    (a, b) =>
      RANK[a.state] - RANK[b.state] ||
      (a.kind === b.kind ? 0 : a.kind === "stack" ? -1 : 1) ||
      a.name.localeCompare(b.name),
  )

  const start = fit(left, ENGINE_ROOM.start)
  const shownNets = fit(nets, ENGINE_ROOM.networks)
  const shownVols = fit(vols, ENGINE_ROOM.volumes)
  return {
    start,
    end: [...shownNets, ...shownVols],
    more: {
      start: left.slice(start.length).map(fullName),
      networks: nets.slice(shownNets.length).map(fullName),
      volumes: vols.slice(shownVols.length).map(fullName),
    },
    totals: {
      stacks: left.filter((node) => node.kind === "stack").length,
      standalone: left.filter((node) => node.kind === "container").length,
      networks: nets.length,
      volumes: vols.length,
    },
  }
}

const fullName = (node: EngineNode) => `${node.prefix ?? ""}${node.name}`

/** What a side has room for: one more than its room is drawn rather than a "1 more". */
function fit<T>(nodes: T[], room: number): T[] {
  return nodes.length <= room + 1 ? nodes : nodes.slice(0, room)
}

/** Whether `id` stays lit while `focus` is pointed at: the things a project or a container uses. */
export function engineRelated(nodes: EngineNode[]) {
  const peers = new Map(nodes.map((node) => [node.id, new Set(node.peers)]))
  return (focus: string, id: string) => peers.get(focus)?.has(id) ?? false
}

/** The engine's own line of facts under its name. */
export function engineWords(containers: Container[]) {
  const running = containers.filter((c) => c.state === "running").length
  return `${running} of ${plural(containers.length, "container")} running`
}
