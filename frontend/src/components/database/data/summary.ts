import type { DbCatalogGroup } from "@/lib/types"
import type { DbCatalog, DbCatalogObject } from "@/components/database/data/types"

/**
 * What a schema amounts to, read off the catalogue the rail already holds:
 * how many of each kind, about how many rows, how much of the disk — and
 * which of its tables are the large ones. It is what the page shows where a
 * table would be, before one is chosen.
 */

/** The catalogue's groups that hold rows, in the rail's order. */
const ROW_GROUPS: readonly DbCatalogGroup[] = [
  "tables",
  "views",
  "materializedViews",
  "dictionaries",
]

export interface SchemaSummary {
  /** How many objects each group holds; a group the engine lacks is absent. */
  counts: { group: DbCatalogGroup; count: number }[]
  /** The engine's estimates added up, or null when it gave none. */
  rows: number | null
  /** Bytes on disk added up, or null when the engine gave no sizes. */
  size: number | null
  /** The largest objects, largest first. */
  largest: {
    object: DbCatalogObject
    group: DbCatalogGroup
    /** 0–1 against the largest. */
    share: number
  }[]
  /** What `largest` is ranked by: bytes where the engine reports them, else rows. */
  rankedBy: "size" | "rows" | null
}

const known = (value: number | undefined): value is number => value !== undefined && value >= 0

export function schemaSummary(catalog: Pick<DbCatalog, "objects">, most = 8): SchemaSummary {
  const all = ROW_GROUPS.flatMap((group) =>
    (catalog.objects[group] ?? []).map((object) => ({ object, group })),
  )
  const counts = ROW_GROUPS.flatMap((group) => {
    const held = catalog.objects[group]
    return held && held.length > 0 ? [{ group, count: held.length }] : []
  })
  const estimates = all.map((entry) => entry.object.estimatedRows).filter(known)
  const sizes = all.map((entry) => entry.object.size).filter(known)
  // Bytes rank the tables where most of them have a size; a few engines
  // report none, and there the row estimates do.
  const rankedBy =
    sizes.length > 0 && sizes.length * 2 >= all.length
      ? "size"
      : estimates.length > 0
        ? "rows"
        : null
  const measure = (object: DbCatalogObject) =>
    rankedBy === "size" ? object.size : rankedBy === "rows" ? object.estimatedRows : undefined
  const ranked = all
    .filter((entry) => known(measure(entry.object)) && (measure(entry.object) ?? 0) > 0)
    .sort((a, b) => (measure(b.object) ?? 0) - (measure(a.object) ?? 0))
    .slice(0, most)
  const top = ranked.length > 0 ? (measure(ranked[0].object) ?? 0) : 0
  return {
    counts,
    rows: estimates.length > 0 ? estimates.reduce((sum, n) => sum + n, 0) : null,
    size: sizes.length > 0 ? sizes.reduce((sum, n) => sum + n, 0) : null,
    largest: ranked.map((entry) => ({
      ...entry,
      share: top > 0 ? (measure(entry.object) ?? 0) / top : 0,
    })),
    rankedBy,
  }
}
