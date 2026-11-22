import { plural } from "@/lib/format"
import { LANES } from "@/lib/hue"
import type { DbFleetEntry } from "@/lib/types"
import type { fleetReadings } from "@/components/database/fleet/fleet"

/**
 * The control center's identity line and its cards' hues, worked out apart
 * from their drawing: which hue each database is drawn in, the engines as
 * the line's mark, how many things the databases feed, and the one-line
 * verdict at the line's end. The band of storage, sessions and backups this
 * also worked out went in 0.9.1 (`fleet-protection.tsx` stands where it was).
 */

/**
 * The hues a database is drawn in: the lane palette without slate, which is
 * every bar's muted "everything else" and reads as no database in particular.
 */
export const DATABASE_HUES = LANES.filter((hue) => hue !== "var(--tag-slate)")

/**
 * Each database's hue, the line of its last hour on its card. Dealt in the
 * order the connections were saved rather than hashed from the name: five
 * databases are five hues, where a hash of five names promised nothing, and a
 * database saved later takes the next hue without moving anybody else's.
 */
export function fleetHues(entries: readonly Pick<DbFleetEntry, "id">[]): Map<number, string> {
  const order = [...new Set(entries.map((entry) => entry.id))].sort((a, b) => a - b)
  return new Map(order.map((id, index) => [id, DATABASE_HUES[index % DATABASE_HUES.length]]))
}

export type FleetVerdict = { tone: "running" | "warning" | "danger"; label: string }

/**
 * The identity line's last word on the fleet: how many do not answer, else how
 * many are stopped, else that every one answers. Nothing is claimed for a
 * fleet with nothing in it.
 */
export function fleetVerdict(
  readings: Pick<ReturnType<typeof fleetReadings>, "total" | "failing" | "stopped">,
): FleetVerdict | undefined {
  const { total, failing, stopped } = readings
  if (total === 0) return undefined
  if (failing > 0) return { tone: "danger", label: `${plural(failing, "database")} not answering` }
  if (stopped > 0) return { tone: "warning", label: `${plural(stopped, "database")} stopped` }
  return { tone: "running", label: total === 1 ? "Answering" : `All ${total} answering` }
}

/**
 * The engines on the server as the identity line's mark: each product once,
 * the commonest first and ties in the order given, and a product with no
 * bundled logo left out rather than drawn as a guess (§14).
 */
export function fleetMarks(logos: readonly (string | undefined)[]): string[] {
  const counts = new Map<string, number>()
  for (const logo of logos) if (logo) counts.set(logo, (counts.get(logo) ?? 0) + 1)
  // A stable sort: products as common as each other keep the fleet's order.
  return [...counts.entries()].sort((a, b) => b[1] - a[1]).map(([logo]) => logo)
}

/** How many things the databases feed, counted once each however many databases reach one. */
export function readerCount(
  topology:
    { edges: { from: string; to: string }[]; nodes: { id: string; kind: string }[] } | undefined,
): number {
  if (!topology) return 0
  const databases = new Set(
    topology.nodes.filter((node) => node.kind === "database").map((node) => node.id),
  )
  return new Set(topology.edges.filter((edge) => databases.has(edge.from)).map((edge) => edge.to))
    .size
}
