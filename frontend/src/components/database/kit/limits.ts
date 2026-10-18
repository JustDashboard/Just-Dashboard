import { bytes } from "@/lib/format"
import type { DbHostCapacity, DbProvisionLimits } from "@/components/database/fleet/types"

/**
 * What a database's container may use, decided apart from its drawing: the
 * rules a limit is held to, the words it is said in, and what the engine's
 * own buffer means beside it. A new server's form and a server's Resources
 * page both ask these, so the two cannot disagree about what is allowed.
 */

export const MIB = 1024 * 1024

/** The smallest memory limit Docker accepts, which the server also holds a request to. */
export const MIN_MEMORY_MB = 6

/** A limit as a form holds it: MiB and cores, zero for no limit. */
export type LimitsDraft = DbProvisionLimits

/** A limit as Docker holds it: bytes and cores, zero for no limit. */
export type Limits = { memoryBytes: number; cpus: number }

/** The memory an engine keeps for itself, by its own setting (`GET /{id}/resources`). */
export type MemoryBuffer = { setting: string; bytes: number }

/** A size the way the rest of the product prints one, without a trailing ".0". */
export function sizeOf(value: number): string {
  return bytes(value).replace(/\.0 /, " ")
}

export function coresWord(cpus: number): string {
  const n = Number(cpus.toFixed(2))
  return `${n} ${n === 1 ? "core" : "cores"}`
}

/** The draft a form starts from: Docker's bytes in whole MiB, a quota to two places. */
export function draftOfLimits(limits: Limits): LimitsDraft {
  return { memoryMb: Math.round(limits.memoryBytes / MIB), cpus: Number(limits.cpus.toFixed(2)) }
}

/** The limits as a phrase: what a section's head says it currently is. */
export function limitsWords(limits: Limits): string {
  const { memoryBytes, cpus } = limits
  if (!memoryBytes && !cpus)
    return "No limits: it may use all of this server's memory and processors"
  const memory = memoryBytes ? `${sizeOf(memoryBytes)} of memory` : ""
  const cores = cpus ? coresWord(cpus) : ""
  if (memory && cores) return `Up to ${memory} and ${cores}`
  return memory
    ? `Up to ${memory}, any number of processors`
    : `Up to ${cores}, any amount of memory`
}

/**
 * What the server would refuse in a draft, by field, in the reader's terms.
 * `host` is this machine as Docker sees it, unknown until it has been read;
 * `usedBytes` is what the running container uses now, which a memory limit
 * may not go under: the kernel would stop the server on the spot.
 */
export function limitProblems(
  draft: LimitsDraft,
  host?: DbHostCapacity,
  usedBytes?: number,
): Partial<Record<"memory" | "cpus", string>> {
  const problems: Partial<Record<"memory" | "cpus", string>> = {}
  const { memoryMb, cpus } = draft
  if (memoryMb > 0 && memoryMb < MIN_MEMORY_MB) {
    problems.memory = `The smallest limit Docker takes is ${MIN_MEMORY_MB} MiB.`
  } else if (host?.memoryBytes && memoryMb * MIB > host.memoryBytes) {
    problems.memory = `This server has ${sizeOf(host.memoryBytes)}; a limit above that limits nothing.`
  } else if (usedBytes && memoryMb > 0 && memoryMb * MIB < usedBytes) {
    problems.memory = `It uses ${sizeOf(usedBytes)} now; a limit under that stops it at once.`
  }
  if (cpus > 0 && cpus < 0.01) {
    problems.cpus = "The smallest processor limit is 0.01 of a core."
  } else if (host?.cpus && cpus > host.cpus) {
    problems.cpus = `This server has ${coresWord(host.cpus)}.`
  }
  return problems
}

/**
 * The limits a new server's form fills in once the reader chooses to limit
 * it: the template's, kept to half this machine's memory and to the
 * processors it has, so the suggestion is never one the server refuses.
 */
export function defaultLimitsFor(suggested: DbProvisionLimits, host?: DbHostCapacity): LimitsDraft {
  if (!host) return suggested
  const half = Math.floor(host.memoryBytes / 2 / MIB / 64) * 64
  return {
    memoryMb: half > 0 ? Math.min(suggested.memoryMb, half) : suggested.memoryMb,
    cpus: host.cpus > 0 ? Math.min(suggested.cpus, host.cpus) : suggested.cpus,
  }
}

/** What a save sends: only what changed, so a field left alone keeps its limit. */
export function limitChanges(saved: LimitsDraft, draft: LimitsDraft): Partial<LimitsDraft> {
  const changes: Partial<LimitsDraft> = {}
  if (draft.memoryMb !== saved.memoryMb) changes.memoryMb = draft.memoryMb
  if (draft.cpus !== saved.cpus) changes.cpus = draft.cpus
  return changes
}

/**
 * The limits a draft takes away. Docker cannot lift one from a container that
 * exists, so saving these replaces the container.
 */
export function liftedLimits(saved: LimitsDraft, draft: LimitsDraft): ("memory" | "cpus")[] {
  const lifted: ("memory" | "cpus")[] = []
  if (saved.memoryMb > 0 && draft.memoryMb === 0) lifted.push("memory")
  if (saved.cpus > 0 && draft.cpus === 0) lifted.push("cpus")
  return lifted
}

/**
 * The engine's buffer beside a memory limit, as a fact: what the setting is,
 * and — where a limit is set under it, or the engine keeps nothing under the
 * limit at all — what the limit then does. It is said, never advised: the
 * setting is the operator's to change on the Parameters page or not.
 */
export function bufferFact(
  buffer: MemoryBuffer,
  memoryMb: number,
): { value: string; effect?: string } {
  if (buffer.bytes === 0) {
    return {
      value: "not set",
      effect: memoryMb
        ? "so nothing in the engine keeps it under this limit: the kernel stops it there"
        : undefined,
    }
  }
  const value = sizeOf(buffer.bytes)
  if (memoryMb && buffer.bytes > memoryMb * MIB) {
    return {
      value,
      effect: "more than this limit: the kernel stops the server once the engine fills it",
    }
  }
  return { value }
}
