import type { DockerNetwork, NetworkOwner } from "./types"

/** One thing a connect, a disconnect or a removal would run into. */
export type NetworkConflict = {
  code: string
  /** A block is refused by the backend too; a warning is confirmed; a note changes nothing. */
  level: "block" | "warn" | "info"
  message: string
  subjects?: string[]
}

/** GET /docker/networks/{id}/connect|disconnect|removal: read afresh, changes nothing. */
export type NetworkChangePreview = {
  network: string
  networkId: string
  container?: string
  owner: NetworkOwner
  conflicts: NetworkConflict[]
  blocked: boolean
  checkedAt: string
}

/** A network the Engine's own prune would remove, with what removing it disturbs. */
export type PruneCandidate = {
  id: string
  name: string
  owner: NetworkOwner
  conflicts: NetworkConflict[]
  /** Nothing refuses it and nothing names it: the reviewed prune removes only these. */
  removable: boolean
}

export type NetworkPrunePreview = { candidates: PruneCandidate[]; checkedAt: string }

/** POST /docker/networks/prune: what was removed, and what was rechecked and kept. */
export type NetworkPruneResult = {
  items: string[]
  skipped: { id: string; name?: string; reason: string }[]
}

export type NetworkDriver = {
  name: string
  source: "builtin" | "plugin"
  scope: string
  creatable: boolean
  reason?: string
  options: { key: string; description: string }[]
}

export type NetworkDriverCatalogue = {
  drivers: NetworkDriver[]
  swarm: string
  manager: boolean
  pluginsRead: boolean
  checkedAt: string
  limitations: string[]
}

/** The previews' shape, checked before it is believed: an older backend answers otherwise. */
export function isChangePreview(value: unknown): value is NetworkChangePreview {
  const preview = value as NetworkChangePreview | undefined
  return Boolean(
    preview && Array.isArray(preview.conflicts) && typeof preview.blocked === "boolean",
  )
}

export function isPrunePreview(value: unknown): value is NetworkPrunePreview {
  return Array.isArray((value as NetworkPrunePreview | undefined)?.candidates)
}

export function isDriverCatalogue(value: unknown): value is NetworkDriverCatalogue {
  return Array.isArray((value as NetworkDriverCatalogue | undefined)?.drivers)
}

const LEVEL_RANK: Record<NetworkConflict["level"], number> = { block: 0, warn: 1, info: 2 }

/** Refusals first, then what to confirm, then notes; otherwise as the backend ordered them. */
export function orderedConflicts(conflicts: NetworkConflict[]): NetworkConflict[] {
  return conflicts
    .map((conflict, index) => ({ conflict, index }))
    .sort(
      (a, b) => LEVEL_RANK[a.conflict.level] - LEVEL_RANK[b.conflict.level] || a.index - b.index,
    )
    .map(({ conflict }) => conflict)
}

/** The owner as the card's tag says it. */
export function ownerWords(owner: NetworkOwner | undefined): string | undefined {
  switch (owner?.kind) {
    case "system":
      return "Docker system"
    case "dashboard":
      return "This dashboard"
    case "database-link":
      return owner.deployment ? `Database link · ${owner.deployment}` : "Database link"
    case "deployment":
      return owner.deployment ? `Deployment · ${owner.deployment}` : "Deployment (gone)"
    case "compose":
      return owner.project ? `Compose · ${owner.project}` : "Compose"
    case "manual":
      return "Created by hand"
  }
  return undefined
}

/** What acts on a network next, said where its owner is shown in full. */
export function ownerConsequence(owner: NetworkOwner | undefined): string | undefined {
  switch (owner?.kind) {
    case "system":
      return "Docker creates it and never removes it."
    case "dashboard":
      return "Part of the dashboard's own stack; its members cannot be detached here."
    case "database-link":
      return "A deployment's database links; that deployment reconciles its members."
    case "deployment":
      return owner.deployment
        ? "Reconciled by its deployment on the next deploy."
        : "Its deployment environment no longer exists."
    case "compose":
      return "Compose recreates it the next time its project comes up."
    case "manual":
      return "Nothing recreates it once removed."
  }
  return undefined
}

/** Members unread is not members none: a failed listing never makes a network look unused. */
export function membersKnown(network: Pick<DockerNetwork, "membersKnown">) {
  return network.membersKnown !== false
}

/** The reviewed prune: what it removes, and what it keeps with the first reason why. */
export function prunePlan(candidates: PruneCandidate[]) {
  const removed = candidates.filter((c) => c.removable)
  const kept = candidates
    .filter((c) => !c.removable)
    .map((c) => ({
      ...c,
      reason:
        orderedConflicts(c.conflicts).find((conflict) => conflict.level !== "info")?.message ??
        "It is not removable now.",
    }))
  return { removed, kept }
}

/**
 * What the creation form can say about a driver before asking: whether this
 * Engine has it, and which option keys a built-in driver would silently ignore.
 */
export function driverReading(
  catalogue: NetworkDriverCatalogue | undefined,
  driver: string,
  options: string,
) {
  const name = driver.trim() || "bridge"
  if (!catalogue) return { known: false as const, name }
  const found = catalogue.drivers.find((d) => d.name === name)
  const keys = options
    .split("\n")
    .map((line) => line.slice(0, Math.max(0, line.indexOf("="))).trim())
    .filter(Boolean)
  const ignored =
    found?.source === "builtin" && found.options.length > 0
      ? keys.filter((key) => !found.options.some((option) => option.key === key))
      : []
  return { known: true as const, name, driver: found, ignored }
}
