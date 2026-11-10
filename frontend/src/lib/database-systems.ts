import {
  byWorstThenName,
  graphFrom,
  systemsFrom,
  vias,
  type Draft,
  type Entry,
  type Graph,
  type System,
  type SystemNode,
} from "@/lib/server-systems"
import type { DbTopology, DbTopoNode, MapLink, MapTone, ServerMap } from "@/lib/types"

/**
 * The databases map as systems: one diagram for each database, drawn on the
 * server map's canvas with the server map's shapes (`server-systems.ts`), and
 * holding what that one database is wired to — what reads it on its left, the
 * container or service it runs in on its right, the volumes that keeps its
 * data in, and the backups that take a copy.
 *
 * Two readings are joined. The server map (`GET /map`) knows what each thing
 * is — a deployment's address, a container's image, a volume's size — and
 * the lines it can prove: a binding, a reference, a variable naming it, the
 * container that serves it, a mount, a dump. The databases topology
 * (`GET /databases/topology`) knows who is connected now: this server's own
 * processes and other machines by their sessions, whether a deployment's link
 * broke or went stale, and a container in the same stack or network that
 * could reach it. Either may be missing; whatever arrived is drawn.
 *
 * What more than one database is wired to — this server, one app reading two
 * databases, the Postgres container serving three — is drawn in each and
 * marked shared, so every diagram is whole on its own.
 *
 * Pure and unit-tested (`database-systems.test.js`).
 */

/** The kinds of the server map a database's diagram is made of. */
export const DATABASE_KINDS: ReadonlySet<string> = new Set([
  "databases",
  "projects",
  "containers",
  "services",
  "volumes",
  "backups",
])

/** How a thing is known to read a database, by the line between them. */
const READS = ["binding", "reference", "dependency", "env", "session", "stack", "network"]

/** The kinds that can read a database. */
const READERS = new Set(["projects", "containers", "host", "remote"])

/**
 * The ways a line is known, surest first, as the server ranks them
 * (`mapViaOrder` in `api/map_links.go`): what was declared before what was
 * seen, and a stack or network shared — a way it could be reached — last.
 */
const VIA_ORDER = [
  "binding",
  "reference",
  "dependency",
  "release",
  "route",
  "serves",
  "pooler",
  "source",
  "workdir",
  "member",
  "self",
  "mount",
  "certificate",
  "backup",
  "network",
  "env",
  "session",
  "stack",
]
const rank = (via: string) => {
  const i = VIA_ORDER.indexOf(via)
  return i === -1 ? VIA_ORDER.length : i
}

/**
 * Left to right: what reads it, the pooler between them, the database, what
 * it runs in, where that keeps its data, and the backups that take it.
 */
const COLUMN = { reader: 0, pooler: 1, database: 2, runs: 3, keeps: 4, backup: 5 } as const
type Side = keyof typeof COLUMN

const sessionsLine = (n: number) => (n === 1 ? "1 session open" : `${n} sessions open`)

function statusTone(status: string | undefined): MapTone {
  switch (status) {
    case "running":
    case "connected":
      return "ok"
    case "restarting":
    case "paused":
    case "created":
      return "warning"
    case "exited":
    case "dead":
      return "danger"
  }
  return "unknown"
}

/** A link's word from the topology as a line's tone: broken, stale, or nothing to say. */
function linkTone(status: string): MapTone | undefined {
  switch (status) {
    case "broken":
    case "failed":
    case "error":
      return "danger"
    case "stale":
      return "warning"
  }
  return undefined
}

const pairKey = (a: string, b: string) => (a < b ? `${a}|${b}` : `${b}|${a}`)

type Known = { tone?: MapTone; sessions: number }

/**
 * A reader the topology names, as the thing the server map draws it as where
 * the map has it — a deployment as its project, a container by its name — and
 * otherwise as itself, so a reading of the topology alone still draws whole.
 */
function readerOf(
  node: DbTopoNode,
  items: Map<string, Entry>,
  containerByName: Map<string, string>,
  sessions: Map<string, number>,
): Entry {
  switch (node.kind) {
    case "deployment": {
      // The project it is an environment of; an older server names it only in the link.
      const project = node.projectId ?? Number(/^\/deploy\/(\d+)/.exec(node.href ?? "")?.[1])
      const id = project ? `project:${project}` : node.id
      return (
        items.get(id) ?? {
          kind: "projects",
          item: {
            id,
            name: node.name,
            product: node.product,
            projectId: project || undefined,
            tone: statusTone(node.status),
            state: node.status ?? "linked",
            detail: node.detail,
            href: node.href ?? "/deploy",
          },
        }
      )
    }
    case "container": {
      const known = containerByName.get(node.name)
      if (known) return items.get(known)!
      const id = `container:${node.name}`
      return {
        kind: "containers",
        item: {
          id,
          name: node.name,
          product: node.product,
          tone: statusTone(node.status),
          state: node.status ?? "seen",
          detail: node.detail,
          href: node.href ?? "/docker/containers",
        },
      }
    }
    case "host":
      return {
        kind: "host",
        item: {
          id: node.id,
          name: node.name,
          tone: "ok",
          state: sessionsLine(sessions.get(node.id) ?? 0),
          detail: node.detail,
          href: "/processes",
        },
      }
    default:
      return {
        kind: "remote",
        item: {
          id: node.id,
          name: node.name,
          tone: "ok",
          state: sessionsLine(sessions.get(node.id) ?? 0),
          detail: node.detail,
          href: `/network/connections?q=${encodeURIComponent(node.name)}`,
        },
      }
  }
}

/** One line for each pair, named by the surest way it is known, with the others in `also`. */
function mergeLinks(links: MapLink[]): MapLink[] {
  const at = new Map<string, { from: string; to: string; ways: Set<string> }>()
  for (const link of links) {
    const key = pairKey(link.from, link.to)
    const have = at.get(key)
    if (have) for (const way of vias(link)) have.ways.add(way)
    else at.set(key, { from: link.from, to: link.to, ways: new Set(vias(link)) })
  }
  return [...at.values()].map(({ from, to, ways }) => {
    const all = [...ways].sort((a, b) => rank(a) - rank(b))
    return all.length > 1
      ? { from, to, via: all[0], also: all.slice(1) }
      : { from, to, via: all[0] }
  })
}

/** The server map's things and lines, with what the topology adds laid over them. */
function joined(map: ServerMap | undefined, topology: DbTopology | undefined) {
  const items = new Map<string, Entry>()
  for (const kind of map?.kinds ?? []) {
    if (!DATABASE_KINDS.has(kind.key) || kind.state === "absent") continue
    for (const item of kind.items) items.set(item.id, { item, kind: kind.key })
  }
  const links: MapLink[] = [...(map?.links ?? [])]
  const known = new Map<string, Known>()
  if (!topology) return { items, links, known }

  const containerByName = new Map<string, string>()
  for (const { item, kind } of items.values()) {
    if (kind === "containers") containerByName.set(item.name, item.id)
  }
  const byId = new Map(topology.nodes.map((node) => [node.id, node]))
  // What this server's processes and another machine hold open, across every
  // database: the box says it once, each line says its own.
  const sessions = new Map<string, number>()
  for (const edge of topology.edges) {
    sessions.set(edge.to, (sessions.get(edge.to) ?? 0) + edge.sessions)
  }

  for (const node of topology.nodes) {
    if (node.kind !== "database" || items.has(node.id)) continue
    // A database the map has not read: drawn from the topology until it has.
    items.set(node.id, {
      kind: "databases",
      item: {
        id: node.id,
        name: node.name,
        product: node.product,
        tone: "unknown",
        state: "not read yet",
        detail: node.detail,
        href: node.href ?? "/databases",
      },
    })
  }
  for (const edge of topology.edges) {
    const from = byId.get(edge.from)
    const to = byId.get(edge.to)
    if (!from || !to || from.kind !== "database" || !items.has(from.id)) continue
    const reader = readerOf(to, items, containerByName, sessions)
    if (!items.has(reader.item.id)) items.set(reader.item.id, reader)
    const ways = edge.via.length > 0 ? [...edge.via].sort((a, b) => rank(a) - rank(b)) : ["session"]
    links.push({ from: reader.item.id, to: from.id, via: ways[0], also: ways.slice(1) })
    const key = pairKey(reader.item.id, from.id)
    const held = known.get(key) ?? { sessions: 0 }
    held.sessions += edge.sessions
    held.tone = held.tone === "danger" ? "danger" : (linkTone(edge.status) ?? held.tone)
    known.set(key, held)
  }
  return { items, links: mergeLinks(links), known }
}

/** Which side of its database each thing it takes is drawn on. */
function sidesOf(graph: Graph, database: string) {
  const side = new Map<string, Side>([[database, "database"]])
  const near = (id: string) => graph.adjacent.get(id) ?? []
  const kindOf = (id: string) => graph.items.get(id)?.kind ?? ""
  const take = (id: string, as: Side) => {
    if (!side.has(id)) side.set(id, as)
  }

  // Where it runs first: a container that serves it is never one of its readers.
  const runs: string[] = []
  for (const { other, link } of near(database)) {
    const ways = vias(link)
    const kind = kindOf(other)
    if ((kind === "containers" || kind === "services") && ways.includes("serves")) {
      take(other, "runs")
      runs.push(other)
    }
  }
  for (const { other, link } of near(database)) {
    const ways = vias(link)
    const kind = kindOf(other)
    if (kind === "containers" && ways.includes("pooler")) take(other, "pooler")
    else if (READERS.has(kind) && ways.some((way) => READS.includes(way))) take(other, "reader")
    else if (kind === "backups" && ways.includes("backup")) take(other, "backup")
  }
  // What the container it runs in keeps its data in, and the backups of that.
  const keeps: string[] = []
  for (const id of runs) {
    for (const { other, link } of near(id)) {
      const kind = kindOf(other)
      if (kind === "volumes" && vias(link).includes("mount") && !side.has(other)) {
        take(other, "keeps")
        keeps.push(other)
      } else if (kind === "backups" && vias(link).includes("backup")) take(other, "backup")
    }
  }
  for (const id of keeps) {
    for (const { other, link } of near(id)) {
      if (kindOf(other) === "backups" && vias(link).includes("backup")) take(other, "backup")
    }
  }
  return side
}

/**
 * Every database's diagram, worst first and then by name, from whatever of
 * the two readings has arrived.
 */
export function detectDatabaseSystems(
  map: ServerMap | undefined,
  topology: DbTopology | undefined,
): System[] {
  const { items, links, known } = joined(map, topology)
  const graph = graphFrom(items, links)
  const databases = [...items.values()].filter((e) => e.kind === "databases").sort(byWorstThenName)
  const drafts: Draft[] = databases.map((anchor) => {
    const side = sidesOf(graph, anchor.item.id)
    // Readers in the order the reading listed them, the database first.
    const members = [...side.keys()]
    return {
      id: `system:${anchor.item.id}`,
      kind: "database",
      anchor,
      members,
      column: (itemId) => COLUMN[side.get(itemId) ?? "reader"],
    }
  })
  const systems = systemsFrom(graph, drafts)
  // What the topology knows of a line the map cannot say: broken, stale, busy.
  for (const system of systems) {
    const itemOf = new Map(system.nodes.map((n) => [n.id, n.itemId]))
    for (const edge of system.edges) {
      const a = itemOf.get(edge.source)
      const b = itemOf.get(edge.target)
      if (!a || !b) continue
      const held = known.get(pairKey(a, b))
      if (!held) continue
      if (held.tone) edge.tone = held.tone
      if (held.sessions > 0) edge.sessions = held.sessions
    }
  }
  return systems
}

/** What a database's diagram holds, by the side of it each thing is on. */
export function databaseCensus(system: System) {
  const anchor = system.nodes.find((n) => n.itemId === system.anchor.id)
  const wired = new Map<string, { via: string[]; sessions: number }>()
  for (const edge of system.edges) {
    const other =
      edge.source === anchor?.id ? edge.target : edge.target === anchor?.id ? edge.source : null
    if (other) wired.set(other, { via: [edge.via, ...edge.also], sessions: edge.sessions ?? 0 })
  }
  const readers: SystemNode[] = []
  const runs: SystemNode[] = []
  let sessions = 0
  for (const node of system.nodes) {
    const line = wired.get(node.id)
    if (!line) continue
    sessions += line.sessions
    if (line.via.includes("serves")) runs.push(node)
    else if (READERS.has(node.kind) && line.via.some((via) => READS.includes(via))) {
      readers.push(node)
    }
  }
  return {
    readers,
    runs,
    sessions,
    volumes: system.nodes.filter((n) => n.kind === "volumes").length,
    backups: system.nodes.filter((n) => n.kind === "backups").length,
  }
}

/** A database's diagram in a few words: "feeds 2 things · 3 sessions open · in shop-db". */
export function databaseCensusLine(system: System, most = 3) {
  const c = databaseCensus(system)
  const parts = [
    c.readers.length === 0
      ? "nothing seen reading it"
      : c.readers.length === 1
        ? "feeds 1 thing"
        : `feeds ${c.readers.length} things`,
    c.sessions > 0 && sessionsLine(c.sessions),
    c.runs[0] && `in ${c.runs[0].item.name}`,
  ].filter((part): part is string => Boolean(part))
  return parts.slice(0, most).join(" · ")
}
