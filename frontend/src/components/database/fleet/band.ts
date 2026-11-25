import type { fleetReadings, Nouns } from "@/components/database/fleet/fleet"

/**
 * The control center's identity line, worked out apart from its drawing: the
 * verdict beside its title and what the databases feed. The band of storage,
 * sessions and backups this also worked out went in 0.9.1
 * (`fleet-protection.tsx` stands where it was); each database's hue, the line
 * of its last hour on its card, and the engines as the line's mark went on
 * 24 Nov 2026 with the line and the hour.
 */

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
  // Out of how many: the line's title is "Databases", so the verdict is
  // where the count is said.
  const of = (n: number, word: string) =>
    total === 1 ? word[0].toUpperCase() + word.slice(1) : `${n} of ${total} ${word}`
  if (failing > 0) return { tone: "danger", label: of(failing, "not answering") }
  if (stopped > 0) return { tone: "warning", label: of(stopped, "stopped") }
  return { tone: "running", label: total === 1 ? "Answering" : `All ${total} answering` }
}

/**
 * What the databases feed, counted once each however many databases reach
 * one, and the noun that names them: "apps" while every one is a deployment
 * or a container, "clients" once a machine reads them too.
 */
export function readers(
  topology:
    { edges: { from: string; to: string }[]; nodes: { id: string; kind: string }[] } | undefined,
): { count: number; nouns: Nouns } {
  const apps = { one: "app", many: "apps" }
  if (!topology) return { count: 0, nouns: apps }
  const kinds = new Map(topology.nodes.map((node) => [node.id, node.kind]))
  const databases = new Set(
    topology.nodes.filter((node) => node.kind === "database").map((node) => node.id),
  )
  const read = new Set(
    topology.edges.filter((edge) => databases.has(edge.from)).map((edge) => edge.to),
  )
  const machines = [...read].some(
    (id) => !["deployment", "container"].includes(kinds.get(id) ?? ""),
  )
  return { count: read.size, nouns: machines ? { one: "client", many: "clients" } : apps }
}
