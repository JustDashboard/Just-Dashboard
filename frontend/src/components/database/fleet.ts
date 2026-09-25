import type { DbFleet, DbFleetEntry, DbTopoEdge, DbTopoNode, DbTopology } from "@/lib/types"

/**
 * The control center's decisions, kept apart from its drawing so they can be
 * tested in a millisecond: which database needs attention, in what order the
 * cards stand, what the readings across the top add up to, and how a map of
 * databases and the things they feed is split into its two columns.
 */

export type FleetFilter = "all" | "attention" | "docker" | "host" | "remote"

/** A backup older than this is drawn as a warning on the card. */
export const STALE_BACKUP_MS = 7 * 24 * 60 * 60 * 1000

/** Why a database is in the attention list, or nothing. */
export function fleetConcern(
  entry: DbFleetEntry,
  now = Date.now(),
): { level: "critical" | "warning"; reason: string } | null {
  if (!entry.ok) return { level: "critical", reason: entry.error || "cannot be reached" }
  if (entry.exposure === "public")
    return { level: "warning", reason: "reachable from the internet" }
  if (entry.source !== "file" && !entry.lastBackup)
    return { level: "warning", reason: "never backed up" }
  if (entry.lastBackup && now - Date.parse(entry.lastBackup) > STALE_BACKUP_MS)
    return { level: "warning", reason: "last backup is over a week old" }
  return null
}

/** Worst first, then by name — the order every list in the product takes. */
export function sortFleet(entries: DbFleetEntry[], now = Date.now()): DbFleetEntry[] {
  const rank = (e: DbFleetEntry) => {
    const concern = fleetConcern(e, now)
    if (!concern) return 2
    return concern.level === "critical" ? 0 : 1
  }
  return [...entries].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
}

export function matchesFilter(entry: DbFleetEntry, filter: FleetFilter, now = Date.now()): boolean {
  switch (filter) {
    case "all":
      return true
    case "attention":
      return fleetConcern(entry, now) !== null
    case "docker":
      return entry.source === "docker"
    case "host":
      return entry.source === "host" || entry.source === "file"
    case "remote":
      return entry.source === "remote"
  }
}

export function matchesQuery(entry: DbFleetEntry, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return [entry.name, entry.driver, entry.database, entry.host, entry.container ?? "", entry.user]
    .join(" ")
    .toLowerCase()
    .includes(q)
}

/** The five readings across the top of the page. */
export function fleetReadings(fleet: DbFleet | undefined, now = Date.now()) {
  const list = fleet?.connections ?? []
  const reachable = list.filter((e) => e.ok).length
  const bytes = list.reduce((sum, e) => sum + (e.sizesKnown ? e.bytes : 0), 0)
  const sized = list.some((e) => e.sizesKnown)
  const objects = list.reduce((sum, e) => sum + e.objects, 0)
  const sessions = list.reduce((sum, e) => sum + e.sessions, 0)
  const consumers = list.reduce((sum, e) => sum + e.consumers, 0)
  const attention = list.filter((e) => fleetConcern(e, now) !== null).length
  const engines = [...new Set(list.map((e) => e.driver))]
  return {
    total: list.length,
    reachable,
    bytes,
    sized,
    objects,
    sessions,
    consumers,
    attention,
    engines,
    waiting: (fleet?.needsCredentials.length ?? 0) + (fleet?.unreachable.length ?? 0),
  }
}

/**
 * The map's two columns: the databases on one side and everything they feed
 * on the other, with each consumer knowing which databases reach it so a
 * narrow screen can say so in words where the lines cannot be drawn.
 */
export function splitTopology(topology: DbTopology | undefined) {
  const nodes = topology?.nodes ?? []
  const edges = topology?.edges ?? []
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const databases = nodes.filter((n) => n.kind === "database")
  const consumers = nodes.filter((n) => n.kind !== "database")
  const feeds = new Map<string, DbTopoEdge[]>()
  const fedBy = new Map<string, { from: DbTopoNode; edge: DbTopoEdge }[]>()
  for (const edge of edges) {
    const from = byId.get(edge.from)
    if (!from) continue
    feeds.set(edge.from, [...(feeds.get(edge.from) ?? []), edge])
    fedBy.set(edge.to, [...(fedBy.get(edge.to) ?? []), { from, edge }])
  }
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
  return { databases, consumers, feeds, fedBy }
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
