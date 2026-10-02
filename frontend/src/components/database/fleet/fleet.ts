import type { DbFleet, DbFleetEntry, DbTopoEdge, DbTopoNode, DbTopology } from "@/lib/types"
import type { DbBackupSummary, DbPowerAction } from "@/components/database/fleet/types"

/**
 * The control center's decisions, kept apart from its drawing so they can be
 * tested in a millisecond: which database needs a hand and what the fix is,
 * what the readings across the top add up to, which cards a reading narrows
 * the fleet to, how the cards are shelved, and how a map of databases and the
 * things they feed is split into its two lanes.
 */

/** A backup older than this is worth a line in the attention list. */
export const STALE_BACKUP_MS = 7 * 24 * 60 * 60 * 1000

/** "Backed up" on the readings means a dump taken inside this window. */
export const FRESH_BACKUP_MS = 24 * 60 * 60 * 1000

/** What a concern is about, which is also what fixes it. */
export type ConcernKind =
  "broken" | "unreachable" | "stopped" | "paused" | "public" | "never-backed-up" | "stale-backup"

export type Concern = {
  kind: ConcernKind
  level: "critical" | "warning" | "notice"
  /** The few words a card's foot or a row's cell can hold. */
  reason: string
}

/** What the fleet's reading of a database says about its dumps. */
export type BackupReading = {
  /** When the newest dump was taken; undefined when there is none. */
  newest: string | undefined
  /** A dump, restore or copy is running against it now. */
  running: boolean
}

/**
 * The newest dump of each connection. The summary is the dump directories'
 * own listing and wins; where it could not be read the fleet's `lastBackup`
 * still says when the newest one was written.
 */
export function backupReadings(
  fleet: DbFleet | undefined,
  summary: DbBackupSummary | undefined,
): Map<number, BackupReading> {
  const out = new Map<number, BackupReading>()
  for (const entry of fleet?.connections ?? []) {
    out.set(entry.id, { newest: entry.lastBackup, running: false })
  }
  for (const row of summary?.connections ?? []) {
    out.set(row.id, {
      newest: row.newest?.takenAt ?? out.get(row.id)?.newest,
      running: row.job?.status === "running",
    })
  }
  return out
}

/** Whether the server was found down rather than failing: it was not dialled. */
function down(entry: DbFleetEntry) {
  return entry.state === "stopped" || entry.state === "paused"
}

/**
 * Everything wrong with one database, worst first. A database that is public
 * *and* has never been dumped has two things to fix, and the list that showed
 * only the first hid the second until the first was dealt with.
 *
 * `dumps` is whether the engine can be dumped from here at all: a database
 * nothing can back up is not "never backed up".
 */
export function fleetConcerns(
  entry: DbFleetEntry,
  options: { backup?: BackupReading; dumps?: boolean; now?: number } = {},
): Concern[] {
  const now = options.now ?? Date.now()
  const out: Concern[] = []
  if (entry.broken || entry.state === "broken") {
    // Nothing else can be said of a row that cannot be opened.
    return [{ kind: "broken", level: "critical", reason: "cannot be opened" }]
  }
  if (!entry.ok && !down(entry)) {
    out.push({ kind: "unreachable", level: "critical", reason: "cannot be reached" })
  }
  // A stopped server is somebody's decision until something depends on it.
  if (entry.state === "stopped" && entry.consumers > 0) {
    out.push({ kind: "stopped", level: "warning", reason: "stopped while in use" })
  }
  if (entry.state === "paused") {
    out.push({ kind: "paused", level: "warning", reason: "paused" })
  }
  if (entry.exposure === "public") {
    out.push({ kind: "public", level: "warning", reason: "reachable from the internet" })
  }
  if (options.dumps !== false) {
    const newest = options.backup ? options.backup.newest : entry.lastBackup
    if (!newest) {
      out.push({ kind: "never-backed-up", level: "warning", reason: "never backed up" })
    } else if (now - Date.parse(newest) > STALE_BACKUP_MS) {
      // The same weight as none at all, on the tile, the card and the list:
      // a dump nobody has taken for a week protects last week's data.
      out.push({ kind: "stale-backup", level: "warning", reason: "last backup is over a week old" })
    }
  }
  return out
}

const LEVEL_RANK = { critical: 0, warning: 1, notice: 2 } as const

/** Worst first, then by name — the order every list in the product takes. */
export function sortFleet(
  entries: DbFleetEntry[],
  concernsOf: (entry: DbFleetEntry) => Concern[] = (entry) => fleetConcerns(entry),
): DbFleetEntry[] {
  const rank = (entry: DbFleetEntry) => {
    const worst = concernsOf(entry)[0]
    return worst ? LEVEL_RANK[worst.level] : 3
  }
  return [...entries].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
}

/**
 * How loudly a fact on a card or in a table cell is said: the level of the
 * concern it is about, so the list of findings, the card and the table never
 * give one fact three weights.
 */
export function concernLevel(
  concerns: readonly Concern[],
  ...kinds: ConcernKind[]
): Concern["level"] | undefined {
  return concerns.find((concern) => kinds.includes(concern.kind))?.level
}

/** The order the kinds of concern are listed in, inside one level. */
const KIND_ORDER: readonly ConcernKind[] = [
  "broken",
  "unreachable",
  "stopped",
  "paused",
  "public",
  "never-backed-up",
  "stale-backup",
]

/** Past this many databases with the same concern, they are one finding that names them. */
export const GROUP_ABOVE = 2

export type ConcernGroup = {
  kind: ConcernKind
  level: Concern["level"]
  entries: DbFleetEntry[]
  /** One finding for all of them, rather than one each. */
  grouped: boolean
}

/**
 * The fleet's concerns as the attention list draws them: by what is wrong,
 * worst first. Two databases with the same thing wrong are two findings; more
 * than that are one finding naming them, because forty databases with seven
 * open ports are one fact about the server, not seven rows that push the
 * fleet off the screen.
 */
export function concernGroups(
  entries: readonly DbFleetEntry[],
  concernsOf: (entry: DbFleetEntry) => Concern[],
): ConcernGroup[] {
  const groups = new Map<ConcernKind, ConcernGroup>()
  for (const entry of entries) {
    for (const concern of concernsOf(entry)) {
      const held = groups.get(concern.kind) ?? {
        kind: concern.kind,
        level: concern.level,
        entries: [],
        grouped: false,
      }
      held.entries.push(entry)
      groups.set(concern.kind, held)
    }
  }
  return [...groups.values()]
    .map((group) => ({ ...group, grouped: group.entries.length > GROUP_ABOVE }))
    .sort(
      (a, b) =>
        LEVEL_RANK[a.level] - LEVEL_RANK[b.level] ||
        KIND_ORDER.indexOf(a.kind) - KIND_ORDER.indexOf(b.kind),
    )
}

/**
 * What a database holds, in bytes: what its engine reported, or — for a file
 * the engine says nothing about — what discovery measured on disk. One answer
 * for the tile, the card, the table and the order they are put in; undefined
 * where nobody knows.
 */
export function storedBytes(
  entry: DbFleetEntry,
  measured?: ReadonlyMap<number, number>,
): number | undefined {
  if (entry.sizesKnown) return entry.bytes
  return measured?.get(entry.id)
}

/** Which part of the fleet a reading narrows the cards to. */
export type FleetShow = "all" | "down" | "stored" | "busy" | "unprotected"

export const FLEET_SHOWS: readonly FleetShow[] = ["all", "down", "stored", "busy", "unprotected"]

/** Whether a card stays when a reading has been pressed. */
export function matchesShow(
  entry: DbFleetEntry,
  show: FleetShow,
  options: { backup?: BackupReading; dumps?: boolean; now?: number; stored?: number } = {},
): boolean {
  switch (show) {
    case "all":
      return true
    case "down":
      return entry.state !== "running" || !entry.ok
    case "stored":
      return (options.stored ?? storedBytes(entry) ?? 0) > 0
    case "busy":
      return entry.sessions > 0
    case "unprotected": {
      if (options.dumps === false || entry.broken) return false
      const newest = options.backup ? options.backup.newest : entry.lastBackup
      return !newest || (options.now ?? Date.now()) - Date.parse(newest) > FRESH_BACKUP_MS
    }
  }
}

/** A narrowed fleet in the order its reading asks for: the largest, the busiest. */
export function orderForShow(
  entries: DbFleetEntry[],
  show: FleetShow,
  storedOf: (entry: DbFleetEntry) => number | undefined = storedBytes,
): DbFleetEntry[] {
  if (show === "stored") {
    return [...entries].sort((a, b) => (storedOf(b) ?? 0) - (storedOf(a) ?? 0))
  }
  if (show === "busy") return [...entries].sort((a, b) => b.sessions - a.sessions)
  return entries
}

export function matchesQuery(entry: DbFleetEntry, query: string, engineLabel = ""): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return [
    entry.name,
    entry.driver,
    entry.flavor ?? "",
    engineLabel,
    entry.database,
    entry.host,
    // The address as a card prints it, so the port read off one finds it.
    entry.port ? `${entry.host}:${entry.port}` : "",
    entry.container ?? "",
    entry.unit ?? "",
    entry.environment ?? "",
    entry.user,
  ]
    .join(" ")
    .toLowerCase()
    .includes(q)
}

export type FleetSortKey = "name" | "size" | "objects" | "sessions" | "backup"
export type FleetSort = { key: FleetSortKey; dir: "asc" | "desc" } | null

/** The table's rows in the order a pressed heading asks for; unpressed, the order they came in. */
export function sortRows(
  entries: DbFleetEntry[],
  sort: FleetSort,
  backupOf: (entry: DbFleetEntry) => string | undefined,
  storedOf: (entry: DbFleetEntry) => number | undefined = storedBytes,
): DbFleetEntry[] {
  if (!sort) return entries
  const value = (entry: DbFleetEntry): number | string => {
    switch (sort.key) {
      case "name":
        return entry.name.toLowerCase()
      case "size":
        // A size nobody reported sorts below every size that was.
        return storedOf(entry) ?? -1
      case "objects":
        return entry.objects
      case "sessions":
        return entry.sessions
      case "backup":
        return Date.parse(backupOf(entry) ?? "") || 0
    }
  }
  const sign = sort.dir === "asc" ? 1 : -1
  return [...entries].sort((a, b) => {
    const x = value(a)
    const y = value(b)
    const order =
      typeof x === "string" && typeof y === "string" ? x.localeCompare(y) : Number(x) - Number(y)
    return order * sign || a.name.localeCompare(b.name)
  })
}

/**
 * What a press on a heading does to the order: sort by it the natural way
 * (names up, figures down), then the other way, then back to the page's own
 * order.
 */
export function nextSort(held: FleetSort, key: FleetSortKey): FleetSort {
  const natural = key === "name" ? "asc" : "desc"
  if (held?.key !== key) return { key, dir: natural }
  if (held.dir === natural) return { key, dir: natural === "asc" ? "desc" : "asc" }
  return null
}

/** How the cards are shelved. */
export type FleetGrouping = "place" | "engine" | "environment"

export type FleetGroup = {
  key: string
  label: string
  entries: DbFleetEntry[]
}

const PLACES: { key: DbFleetEntry["source"]; label: string }[] = [
  { key: "docker", label: "Containers" },
  { key: "host", label: "On this server" },
  { key: "file", label: "Files" },
  { key: "remote", label: "Elsewhere" },
  { key: "unknown", label: "Cannot be opened" },
]

/**
 * The cards in their shelves, each shelf in the order the cards arrived in.
 * By place the shelves keep a fixed order (containers, this server, files,
 * elsewhere); by engine and by environment they are in name order, with the
 * databases nobody labelled last.
 */
export function groupFleet(
  entries: DbFleetEntry[],
  by: FleetGrouping,
  engineLabel: (entry: DbFleetEntry) => string,
): FleetGroup[] {
  if (by === "place") {
    return PLACES.map((place) => ({
      key: place.key,
      label: place.label,
      entries: entries.filter((entry) => entry.source === place.key),
    })).filter((group) => group.entries.length > 0)
  }
  const groups = new Map<string, FleetGroup>()
  for (const entry of entries) {
    const label = by === "engine" ? engineLabel(entry) : (entry.environment ?? "").trim()
    const key = label.toLowerCase()
    const group = groups.get(key) ?? {
      key,
      label: label || "No environment",
      entries: [],
    }
    group.entries.push(entry)
    groups.set(key, group)
  }
  return [...groups.values()].sort(
    (a, b) => Number(a.key === "") - Number(b.key === "") || a.label.localeCompare(b.label),
  )
}

/**
 * Where a connection's server runs, as its card and its table row say it: the
 * literal that tells it from its neighbours, and what kind of thing that is.
 */
export function whereWord(entry: DbFleetEntry): { label: string; text: string; mono: boolean } {
  const address = `${entry.host}${entry.port ? `:${entry.port}` : ""}`
  switch (entry.source) {
    case "docker":
      return {
        label: "Container",
        text: entry.container ?? "a container",
        mono: Boolean(entry.container),
      }
    case "host":
      // A server with no unit of its own is told from its neighbours by its port.
      return entry.unit
        ? { label: "Unit", text: entry.unit, mono: true }
        : { label: "Listens on", text: address, mono: true }
    case "file":
      return { label: "File", text: entry.database, mono: true }
    case "remote":
      return { label: "Address", text: address, mono: true }
    default:
      return { label: "Where", text: "unknown", mono: false }
  }
}

/** Where a database can be reached from, in the words a card's foot has room for. */
export function reachWord(exposure: DbFleetEntry["exposure"]): string {
  switch (exposure) {
    case "local":
      return "this server only"
    case "private":
      return "a private network"
    case "public":
      return "the internet"
    case "remote":
      return "another machine"
    default:
      return "unknown"
  }
}

/**
 * Which power actions to offer from a list, read off what the fleet says: a
 * server in a container or under a unit of its own can be started when it is
 * down and stopped or restarted when it is up. The connection's own summary
 * has the last word (`power`), and the server refuses what does not apply —
 * this is only what keeps a menu from offering Stop on a remote server.
 */
export function powerOffers(entry: DbFleetEntry): Record<DbPowerAction, boolean> {
  const managed =
    (entry.source === "docker" && Boolean(entry.container)) ||
    (entry.source === "host" && Boolean(entry.unit))
  if (!managed || entry.broken) return { start: false, stop: false, restart: false }
  if (entry.state === "stopped") return { start: true, stop: false, restart: false }
  // A paused container can only be stopped from here.
  if (entry.state === "paused") return { start: false, stop: true, restart: false }
  return { start: false, stop: true, restart: true }
}

/** The figures across the top of the page. */
export function fleetReadings(
  fleet: DbFleet | undefined,
  options: {
    backups?: Map<number, BackupReading>
    dumps?: (entry: DbFleetEntry) => boolean
    /** What each database holds, where it is known: `storedBytes` unless given. */
    storedOf?: (entry: DbFleetEntry) => number | undefined
    now?: number
  } = {},
) {
  const list = fleet?.connections ?? []
  const now = options.now ?? Date.now()
  const running = list.filter((e) => e.ok && e.state === "running").length
  const stopped = list.filter((e) => e.state === "stopped" || e.state === "paused").length
  const failing = list.length - running - stopped
  const storedOf = options.storedOf ?? storedBytes
  const sizes = list.flatMap((e) => storedOf(e) ?? [])
  const answering = list.filter((e) => e.ok)
  const dumpable = list.filter((e) => !e.broken && (options.dumps?.(e) ?? true))
  const newestOf = (e: DbFleetEntry) =>
    options.backups ? options.backups.get(e.id)?.newest : e.lastBackup
  const fresh = dumpable.filter((e) => {
    const newest = newestOf(e)
    return newest !== undefined && now - Date.parse(newest) <= FRESH_BACKUP_MS
  }).length
  return {
    total: list.length,
    running,
    stopped,
    failing,
    /** The sum of the sizes that were reported; `sized` says how many were. */
    bytes: sizes.reduce((sum, size) => sum + size, 0),
    sized: sizes.length,
    /** Open sessions across the servers that answered; `answering` says how many did. */
    sessions: answering.reduce((sum, e) => sum + e.sessions, 0),
    answering: answering.length,
    dumpable: dumpable.length,
    fresh,
    never: dumpable.filter((e) => !newestOf(e)).length,
  }
}

/** One reading of a figure the page keeps while it is open. */
export type Sample = { at: string; value: number }

/**
 * A figure's samples with the newest reading laid in: nothing when the stamp
 * is the one already held (a poll that was answered from the server's cache),
 * and never more than `keep` of them.
 */
export function pushSample(samples: Sample[], at: string, value: number, keep = 60): Sample[] {
  if (samples.length > 0 && samples[samples.length - 1].at === at) return samples
  return [...samples, { at, value }].slice(-keep)
}

/**
 * What each database feeds, read off the map: the things with a link from
 * it, their products for the marks, and how many there are.
 */
export function feedsByConnection(
  topology: DbTopology | undefined,
): Map<number, { products: string[]; count: number; sessions: number }> {
  const out = new Map<number, { products: string[]; count: number; sessions: number }>()
  if (!topology) return out
  const byId = new Map(topology.nodes.map((node) => [node.id, node]))
  for (const edge of topology.edges) {
    const from = byId.get(edge.from)
    const to = byId.get(edge.to)
    if (!from || !to || from.connId === undefined) continue
    const held = out.get(from.connId) ?? { products: [], count: 0, sessions: 0 }
    held.count += 1
    held.sessions += edge.sessions
    if (to.product && !held.products.includes(to.product)) held.products.push(to.product)
    out.set(from.connId, held)
  }
  return out
}

/**
 * The map's two lanes: the databases on one side and everything they feed on
 * the other, with each consumer knowing which databases reach it so a narrow
 * screen can say so in words where the lines cannot be drawn.
 *
 * `keep` narrows the picture to some of the databases: the others leave with
 * their links, and so does a consumer none of the kept ones feeds.
 */
export function splitTopology(
  topology: DbTopology | undefined,
  keep?: (node: DbTopoNode) => boolean,
) {
  const nodes = topology?.nodes ?? []
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const databases = nodes.filter((n) => n.kind === "database" && (keep?.(n) ?? true))
  const kept = new Set(databases.map((n) => n.id))
  const edges = (topology?.edges ?? []).filter((edge) => kept.has(edge.from) && byId.has(edge.to))
  const feeds = new Map<string, DbTopoEdge[]>()
  const fedBy = new Map<string, { from: DbTopoNode; edge: DbTopoEdge }[]>()
  for (const edge of edges) {
    const from = byId.get(edge.from)
    if (!from) continue
    feeds.set(edge.from, [...(feeds.get(edge.from) ?? []), edge])
    fedBy.set(edge.to, [...(fedBy.get(edge.to) ?? []), { from, edge }])
  }
  const consumers = nodes.filter((n) => n.kind !== "database" && (!keep || fedBy.has(n.id)))
  // A consumer with a broken link stands first; then the ones fed by the
  // most; then by name — the order the eye should meet them in.
  const worst = (id: string) =>
    Math.max(0, ...(fedBy.get(id) ?? []).map((f) => edgeRank(f.edge.status)))
  consumers.sort(
    (a, b) =>
      worst(b.id) - worst(a.id) ||
      (fedBy.get(b.id)?.length ?? 0) - (fedBy.get(a.id)?.length ?? 0) ||
      a.name.localeCompare(b.name),
  )
  return { databases, consumers, edges, feeds, fedBy }
}

export function edgeRank(status: string) {
  switch (status) {
    case "broken":
    case "failed":
    case "error":
      return 3
    case "stale":
      return 2
    case "pending":
      return 1
  }
  return 0
}

/** The worst link among several, as the word the map says it with. */
export function worstStatus(edges: DbTopoEdge[]): string {
  return edges.reduce(
    (worst, edge) => (edgeRank(edge.status) > edgeRank(worst) ? edge.status : worst),
    "observed",
  )
}

/** The map's own figures: how much is drawn, how much of it carries, how much is wrong. */
export function topologyReadings(split: ReturnType<typeof splitTopology>) {
  return {
    databases: split.databases.length,
    consumers: split.consumers.filter((node) => split.fedBy.has(node.id)).length,
    links: split.edges.length,
    sessions: split.edges.reduce((sum, edge) => sum + edge.sessions, 0),
    wrong: split.edges.filter((edge) => edgeRank(edge.status) >= 2).length,
  }
}

/** One line for how a link is known. */
export function describeEdge(edge: DbTopoEdge): string {
  const parts: string[] = []
  if (edge.via.includes("binding")) parts.push("linked by its deployment")
  if (edge.via.includes("env")) parts.push("named in its environment")
  if (edge.via.includes("stack")) parts.push("same compose stack")
  if (edge.via.includes("network")) parts.push("shares a network")
  if (edge.sessions > 0)
    parts.push(edge.sessions === 1 ? "1 open session" : `${edge.sessions} open sessions`)
  else if (edge.via.includes("session")) parts.push("seen connected")
  return parts.join(" · ")
}

/** The same in the two words a small card has room for. */
export function shortEdge(edge: DbTopoEdge): string {
  if (edge.sessions > 0) return `${edge.sessions} open`
  if (edge.via.includes("binding")) return "linked"
  if (edge.via.includes("env")) return "configured"
  if (edge.via.includes("stack")) return "same stack"
  if (edge.via.includes("network")) return "same network"
  return "seen"
}
