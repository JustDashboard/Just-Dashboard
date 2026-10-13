import { LANES } from "@/lib/hue"
import type { DbFleetEntry } from "@/lib/types"
import {
  FRESH_BACKUP_MS,
  STALE_BACKUP_MS,
  type fleetReadings,
} from "@/components/database/fleet/fleet"

/**
 * The control center's band and identity line, worked out apart from their
 * drawing: which hue each database is drawn in, who holds the stored bytes
 * and the open sessions, how old every database's newest dump is, and the
 * one-line verdict at the identity line's end.
 */

/**
 * The hues a database is drawn in: the lane palette without slate, which is
 * the bar's muted "everything else" and would read as part of it.
 */
export const DATABASE_HUES = LANES.filter((hue) => hue !== "var(--tag-slate)")

/**
 * Each database's hue, the same on its card's size bar, its span on a bar, its dot
 * on the backup runway and its key in a row. Dealt in the order the
 * connections were saved rather than hashed from the name: five databases are
 * five hues, where a hash of five names promised nothing, and a database
 * saved later takes the next hue without moving anybody else's.
 */
export function fleetHues(entries: readonly Pick<DbFleetEntry, "id">[]): Map<number, string> {
  const order = [...new Set(entries.map((entry) => entry.id))].sort((a, b) => a - b)
  return new Map(order.map((id, index) => [id, DATABASE_HUES[index % DATABASE_HUES.length]]))
}

export type Share = { entry: DbFleetEntry; value: number }

/**
 * A figure shared out across the fleet, as one bar draws it: the `shown`
 * largest as spans, everything else one muted span. A database that reports
 * nothing (`undefined`) is counted apart rather than drawn as a zero, and a
 * zero takes no span of its own.
 */
export function shares(
  entries: readonly DbFleetEntry[],
  valueOf: (entry: DbFleetEntry) => number | undefined,
  shown = 5,
) {
  const known: Share[] = []
  let unknown = 0
  for (const entry of entries) {
    const value = valueOf(entry)
    if (value === undefined || !Number.isFinite(value)) unknown += 1
    else known.push({ entry, value })
  }
  const ranked = known
    .filter((share) => share.value > 0)
    .sort((a, b) => b.value - a.value || a.entry.name.localeCompare(b.entry.name))
  const top = ranked.slice(0, shown)
  const total = known.reduce((sum, share) => sum + share.value, 0)
  return {
    top,
    total,
    /** What the databases past the shown ones hold together. */
    rest: total - top.reduce((sum, share) => sum + share.value, 0),
    /** How many hold something and were not shown. */
    more: ranked.length - top.length,
    /** How many said nothing at all. */
    unknown,
    /** The largest single share: what a card's own meter is measured against. */
    largest: ranked[0]?.value ?? 0,
  }
}

/** The runway's far end: a fortnight, so a week's staleness sits at its middle. */
export const RUNWAY_DAYS = 14

const DAY_MS = 24 * 60 * 60 * 1000

/**
 * How a database's newest dump stands: inside the day the readings call
 * backed up, inside the week the attention rules allow, older than that, or
 * none at all.
 */
export type BackupStanding = "fresh" | "aging" | "stale" | "never"

export type RunwayRow = {
  entry: DbFleetEntry
  standing: BackupStanding
  /** When the newest dump was taken. */
  newest?: string
  /** Where it sits on the runway, 0 (now) to 1 (a fortnight or older). */
  position?: number
}

/**
 * Every database that can be dumped from here, with how old its newest dump
 * is, worst first: the ones never dumped, then the oldest. A database whose
 * engine cannot be dumped from here is not "never backed up", and is left
 * off rather than counted against the server.
 */
export function backupRunway(
  entries: readonly DbFleetEntry[],
  options: {
    newestOf: (entry: DbFleetEntry) => string | undefined
    dumps?: (entry: DbFleetEntry) => boolean
    now?: number
  },
): RunwayRow[] {
  const now = options.now ?? Date.now()
  const rows = entries
    .filter((entry) => !entry.broken && entry.state !== "broken")
    .filter((entry) => options.dumps?.(entry) ?? true)
    .map((entry): RunwayRow => {
      const newest = options.newestOf(entry)
      const taken = newest ? Date.parse(newest) : NaN
      if (!newest || Number.isNaN(taken)) return { entry, standing: "never" }
      const age = Math.max(0, now - taken)
      return {
        entry,
        newest,
        position: Math.min(age / (RUNWAY_DAYS * DAY_MS), 1),
        standing: age <= FRESH_BACKUP_MS ? "fresh" : age <= STALE_BACKUP_MS ? "aging" : "stale",
      }
    })
  return rows.sort(
    (a, b) =>
      Number(b.standing === "never") - Number(a.standing === "never") ||
      (b.position ?? 0) - (a.position ?? 0) ||
      a.entry.name.localeCompare(b.entry.name),
  )
}

/** Where the runway's warning stretch begins: the age the attention rules call stale. */
export const STALE_AT = STALE_BACKUP_MS / (RUNWAY_DAYS * DAY_MS)

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
  const databases = (n: number) => (n === 1 ? "1 database" : `${n} databases`)
  if (failing > 0) return { tone: "danger", label: `${databases(failing)} not answering` }
  if (stopped > 0) return { tone: "warning", label: `${databases(stopped)} stopped` }
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
