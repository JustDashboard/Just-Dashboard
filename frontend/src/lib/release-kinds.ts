import type { ChangeKind, Release } from "@/lib/types"

/**
 * The order a release's kinds are counted in: what somebody deciding whether
 * to upgrade a root-equivalent panel looks for first, security ahead of
 * everything, then what is new, then the rest.
 */
export const KIND_ORDER: ChangeKind[] = [
  "security",
  "added",
  "changed",
  "fixed",
  "deprecated",
  "removed",
]

/**
 * What a run of releases holds, by kind — "1 security · 6 new · 3 fixed" —
 * with the kinds nothing changed under left out. One release is a run of one,
 * which is how the history counts each entry; the update notice counts every
 * release between the installed version and the newest at once.
 */
export function kindCounts(releases: Release[]): { kind: ChangeKind; n: number }[] {
  return KIND_ORDER.map((kind) => ({
    kind,
    n: releases.reduce(
      (sum, release) => sum + release.changes.filter((change) => change.kind === kind).length,
      0,
    ),
  })).filter((count) => count.n > 0)
}
