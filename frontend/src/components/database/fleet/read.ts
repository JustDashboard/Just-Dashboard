import { get } from "@/lib/api"
import type { DbFleet, DbTopology } from "@/lib/types"
import type {
  DbBackupSummary,
  DbHostCapacity,
  DbInventory,
  DbProvisionTemplate,
} from "@/components/database/fleet/types"

/**
 * The section-wide reads, each held to the shape the pages are about to walk.
 *
 * A dashboard a version behind its frontend, or a proxy in between, answers a
 * route it does not know with something — an empty list, a page of HTML — and
 * a reading that is not the shape is a failed reading: the block that wanted
 * it says so and offers to ask again, where reading `instances` off a list
 * took the whole page down.
 */
async function read<T>(
  path: string,
  signal: AbortSignal,
  shaped: (value: T) => boolean,
): Promise<T> {
  const value = await get<T>(path, undefined, signal)
  if (!value || !shaped(value)) {
    throw new Error("The server answered this read with something the page cannot use.")
  }
  last.set(path, { value, at: Date.now() })
  return value
}

/**
 * The last answer to each read, for the next page that opens on it to draw at
 * once (`usePoll`'s `seed`) while it asks again: leaving the control center
 * for the map or a database and coming back drew its skeleton every time. A
 * reading older than a few minutes is not offered — by then it says more about
 * the past than the page's own "checked" time would admit at a glance.
 */
const last = new Map<string, { value: unknown; at: number }>()
const SEED_MS = 5 * 60_000

// Signing out forgets them with the rest of the working state, so the next
// account to sign in on this tab is never drawn what the last one was shown.
if (typeof window !== "undefined") {
  window.addEventListener("jd-working-state-cleared", () => last.clear())
}

function lastOf<T>(path: string): T | undefined {
  const held = last.get(path)
  return held && Date.now() - held.at < SEED_MS ? (held.value as T) : undefined
}

export const lastFleet = () => lastOf<DbFleet>("/databases/fleet")
export const lastInventory = () => lastOf<DbInventory>("/databases/inventory")
export const lastBackupSummary = () => lastOf<DbBackupSummary>("/databases/backups/summary")
export const lastTopology = () => lastOf<DbTopology>("/databases/topology")

export const readFleet = (signal: AbortSignal) =>
  read<DbFleet>("/databases/fleet", signal, (value) => Array.isArray(value.connections))

export const readInventory = (signal: AbortSignal) =>
  read<DbInventory>(
    "/databases/inventory",
    signal,
    (value) => Array.isArray(value.instances) && Array.isArray(value.scans),
  )

export const readBackupSummary = (signal: AbortSignal) =>
  read<DbBackupSummary>("/databases/backups/summary", signal, (value) =>
    Array.isArray(value.connections),
  )

export const readTopology = (signal: AbortSignal) =>
  read<DbTopology>(
    "/databases/topology",
    signal,
    (value) => Array.isArray(value.nodes) && Array.isArray(value.edges),
  )

export const readTemplates = (signal: AbortSignal) =>
  read<DbProvisionTemplate[]>("/databases/provision/options", signal, Array.isArray)

export const readCapacity = (signal: AbortSignal) =>
  read<DbHostCapacity>(
    "/databases/provision/capacity",
    signal,
    (value) => typeof value.cpus === "number" && typeof value.memoryBytes === "number",
  )
