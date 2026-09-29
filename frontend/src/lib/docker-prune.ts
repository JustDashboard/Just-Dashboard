import { post } from "@/lib/api"
import { bytes } from "@/lib/format"
import type { PruneReport } from "@/lib/types"

/**
 * What a sweep is allowed to touch, mirroring `dockerx.PruneOptions`.
 *
 * There is one of these rather than a prune call per button because the three
 * places that reclaim disk — the finding, the disk panel and the overview —
 * used to be three different requests, and only one of them reached what the
 * page was promising. The finding advertised images *and* build cache and sent
 * the operator to a button that pruned dangling images alone; on a host with
 * nothing dangling, that is a promise of forty gigabytes answered by nothing at
 * all.
 */
export type PruneScope = {
  /** Tagged images no container uses, not merely the dangling ones. */
  allImages?: boolean
  buildCache?: boolean
  /** Every cache entry not in use, which is the figure the UI quotes. */
  allBuildCache?: boolean
  /** The part that can delete persistent data. */
  volumes?: boolean
}

/**
 * The sweep the "reclaim" affordances run.
 *
 * Images and build cache together, volumes never: that is exactly the sum the
 * dashboard reports as reclaimable, so the button and the number beside it are
 * the same claim.
 */
export const RECLAIM_SAFE: PruneScope = {
  allImages: true,
  buildCache: true,
  allBuildCache: true,
}

export function prune(scope: PruneScope) {
  return post<PruneReport[]>("/docker/prune", undefined, {
    query: {
      allImages: scope.allImages ? "true" : undefined,
      buildCache: scope.buildCache ? "true" : undefined,
      allBuildCache: scope.allBuildCache ? "true" : undefined,
      volumes: scope.volumes ? "true" : undefined,
    },
  })
}

/**
 * What to tell the operator afterwards.
 *
 * It reports the total the daemon actually freed rather than the estimate that
 * prompted the press, and it names any part that refused. A sweep is several
 * independent prunes; one of them failing silently is how "I pressed it and
 * nothing happened" happens.
 */
/**
 * The dedicated single-kind prunes.
 *
 * `POST /docker/containers/prune` and `POST /docker/networks/prune` were
 * mounted from the day the tabs were and never called by anything, so a
 * stopped container or a network left behind by a removed stack could only be
 * cleared one row at a time. They are the right call for a control that names
 * one kind: running the whole sweep from a button labelled "Containers" would
 * remove images and networks the operator never agreed to.
 */
export function pruneContainers() {
  return post<PruneReport>("/docker/containers/prune")
}

export function pruneNetworks() {
  return post<PruneReport>("/docker/networks/prune")
}

export function pruneSummary(reports: PruneReport[]): {
  reclaimed: number
  message: string
  failed: string[]
} {
  const reclaimed = reports.reduce((sum, r) => sum + r.spaceReclaimed, 0)
  const failed = reports.filter((r) => r.error).map((r) => `${r.kind}: ${r.error}`)
  const moved = reports.filter((r) => r.spaceReclaimed > 0 || r.items.length > 0)

  // "Reclaimed 0 B" is a true and useless sentence. When nothing moved and
  // nothing failed, the honest reading is that there was nothing to reclaim —
  // which is a different message from a button that did not work, and the
  // operator deserves to be able to tell them apart.
  let message: string
  if (failed.length && reclaimed === 0) {
    message = `Nothing was reclaimed — ${failed.join("; ")}`
  } else if (reclaimed === 0 && moved.length === 0) {
    message = "Nothing to reclaim — Docker is already as small as it goes"
  } else {
    const parts = moved
      .filter((r) => r.spaceReclaimed > 0)
      .map((r) => `${bytes(r.spaceReclaimed)} of ${r.kind}`)
    message = `Reclaimed ${bytes(reclaimed)}${parts.length > 1 ? ` — ${parts.join(", ")}` : ""}`
    if (failed.length) message += `. Some parts did not run: ${failed.join("; ")}`
  }
  return { reclaimed, message, failed }
}
