import { plural } from "@/lib/format"
import type { SecretStore, SecretStoreOutcome, SecretStoreUse } from "@/lib/types"
import type { WireLine } from "@/components/wire-flow-model"
import { shortAge } from "@/components/deploy/short-age"

/**
 * What a store's row and its line in the picture say about it, kept apart
 * from the page so it is tested in `bun test src`. The sentence a store's
 * popover reads out is `storeHealth`'s in `secret-stores.ts`; this is the one
 * word beside the dot, who reads the stores, and the count on the section's
 * head.
 */

type Tried = Pick<SecretStore, "test" | "lastRead">

/** The newest thing tried against a store: a run's read or a Test, whichever is later. */
export function storeLatest(store: Tried): SecretStoreOutcome | undefined {
  const { lastRead: read, test } = store
  if (read && test) return Date.parse(read.at) >= Date.parse(test.at) ? read : test
  return read ?? test
}

export type StoreState = "answering" | "refused" | "untested"

export function storeState(store: Tried): StoreState {
  const latest = storeLatest(store)
  if (!latest) return "untested"
  return latest.ok ? "answering" : "refused"
}

/** The dot and the word in a store's state column: "Answering", "Refused 2m ago". */
export function storeStateWord(
  store: Tried,
  now = Date.now(),
): { tone: "running" | "warning" | "unknown"; label: string } {
  const latest = storeLatest(store)
  if (!latest) return { tone: "unknown", label: "Not tested" }
  if (latest.ok) return { tone: "running", label: "Answering" }
  return { tone: "warning", label: `Refused ${shortAge(latest.at, now)} ago` }
}

/**
 * A store's line into this server (design-system §2: the line is the state):
 * a pulse while it last answered, amber and still once it refused, still
 * before anything was tried.
 */
export function storeLine(store: Tried): WireLine {
  switch (storeState(store)) {
    case "refused":
      return { state: "warning" }
    case "untested":
      return { state: "up" }
    default:
      return { state: "up", moving: true, duration: 2.6 }
  }
}

/** The section head's count: "3 stores · 1 refused". */
export function storesCount(stores: Tried[]): string {
  const refused = stores.filter((store) => storeState(store) === "refused").length
  return [plural(stores.length, "store"), refused > 0 && `${refused} refused`]
    .filter(Boolean)
    .join(" · ")
}

export type StoreReader = {
  projectId: number
  projectName: string
  /** How many of its variables read a store. */
  variables: number
  /** The stores it reads, by id. */
  stores: number[]
  /** One of the stores it reads last refused, so its next run cannot read it. */
  refused: boolean
}

/**
 * The projects that read the stores, each once with how many of its
 * variables do, in the order the stores first name them.
 */
export function storeReaders(
  stores: (Pick<SecretStore, "id" | "usedBy"> & Tried)[],
): StoreReader[] {
  const readers = new Map<number, StoreReader>()
  for (const store of stores) {
    const refused = storeState(store) === "refused"
    for (const use of store.usedBy) {
      const reader = readers.get(use.projectId) ?? {
        projectId: use.projectId,
        projectName: use.projectName,
        variables: 0,
        stores: [],
        refused: false,
      }
      reader.variables += 1
      if (!reader.stores.includes(store.id)) reader.stores.push(store.id)
      reader.refused ||= refused
      readers.set(use.projectId, reader)
    }
  }
  return [...readers.values()]
}

/**
 * The variables reading one store, a project at a time, so a store read in
 * production and a preview says the project's name once.
 */
export function storeUsesByProject(uses: SecretStoreUse[]) {
  const projects = new Map<
    number,
    { projectId: number; projectName: string; uses: SecretStoreUse[] }
  >()
  for (const use of uses) {
    const entry = projects.get(use.projectId) ?? {
      projectId: use.projectId,
      projectName: use.projectName,
      uses: [],
    }
    entry.uses.push(use)
    projects.set(use.projectId, entry)
  }
  return [...projects.values()]
}
