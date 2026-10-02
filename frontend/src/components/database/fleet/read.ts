import { get } from "@/lib/api"
import type { DbFleet, DbTopology } from "@/lib/types"
import type {
  DbBackupSummary,
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
  return value
}

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
