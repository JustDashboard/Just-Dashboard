import type { DbCatalogGroup } from "@/lib/types"
import type { DbCatalog, DbTableStat, SchemaObject } from "@/components/database/schema/types"

/**
 * What a schema amounts to, for the page that stands where an object would:
 * how many tables, about how many rows, how much of the disk and how much of
 * that is indexes — and which tables are the large ones.
 *
 * Two sources say it. The catalogue the tree is drawn from carries an
 * estimate and a size for each table on some engines and neither on others;
 * the engine's own table statistics, where the engine keeps them, carry both
 * for every table and split the bytes into rows and indexes. Statistics win
 * where there are any, the catalogue fills in where there are none, and a
 * figure neither can give is absent — never zero.
 */

/** The catalogue's groups that hold rows, in the tree's order. */
const ROW_GROUPS: readonly DbCatalogGroup[] = [
  "tables",
  "views",
  "materializedViews",
  "dictionaries",
]

export type LargeObject = {
  object: SchemaObject
  group: DbCatalogGroup
  bytes: number | null
  rows: number | null
  /** 0–1 against the largest. */
  share: number
}

export type SchemaFigures = {
  tables: number
  /** The other kinds that hold rows, where the schema has any. */
  others: { group: DbCatalogGroup; count: number }[]
  rows: number | null
  bytes: number | null
  /** Bytes in indexes; null where the engine does not split them out. */
  indexBytes: number | null
  /** The engine's estimate of what a rewrite would give back; null where it has none. */
  reclaimable: number | null
  /** How the tables were read since the engine last reset its counters; null where it keeps none. */
  reads: { byIndex: number; byScan: number } | null
  largest: LargeObject[]
  rankedBy: "size" | "rows" | null
  /** How many tables the figures are added up over. */
  measured: number
}

const kept = (value: number | undefined | null): value is number =>
  value !== undefined && value !== null && value >= 0

const sum = (values: number[]) => values.reduce((total, value) => total + value, 0)

export function schemaFigures(
  catalog: Pick<DbCatalog, "objects">,
  stats?: readonly DbTableStat[],
  most = 8,
): SchemaFigures {
  const byName = new Map((stats ?? []).map((stat) => [stat.table, stat]))
  const all = ROW_GROUPS.flatMap((group) =>
    ((catalog.objects[group] ?? []) as SchemaObject[]).map((object) => {
      const stat = byName.get(object.name)
      return {
        object,
        group,
        rows: kept(stat?.rows)
          ? stat.rows
          : kept(object.estimatedRows)
            ? object.estimatedRows
            : null,
        bytes:
          stat && stat.totalBytes > 0 ? stat.totalBytes : kept(object.size) ? object.size : null,
        indexBytes: stat && stat.totalBytes > 0 && kept(stat.indexBytes) ? stat.indexBytes : null,
        bloat: stat && kept(stat.bloatBytes) ? stat.bloatBytes : null,
      }
    }),
  )
  const rows = all.flatMap((entry) => (entry.rows === null ? [] : [entry.rows]))
  const sizes = all.flatMap((entry) => (entry.bytes === null ? [] : [entry.bytes]))
  const indexes = all.flatMap((entry) => (entry.indexBytes === null ? [] : [entry.indexBytes]))
  const bloat = all.flatMap((entry) => (entry.bloat === null ? [] : [entry.bloat]))
  // Bytes rank the tables where most of them have a size; where few do, rows.
  const rankedBy =
    sizes.length > 0 && sizes.length * 2 >= all.length ? "size" : rows.length > 0 ? "rows" : null
  const measure = (entry: (typeof all)[number]) =>
    rankedBy === "size" ? entry.bytes : rankedBy === "rows" ? entry.rows : null
  const ranked = all
    .filter((entry) => (measure(entry) ?? 0) > 0)
    .sort((a, b) => (measure(b) ?? 0) - (measure(a) ?? 0))
    .slice(0, most)
  const top = ranked.length > 0 ? (measure(ranked[0]) ?? 0) : 0
  const counted = (stats ?? []).filter((stat) => kept(stat.seqScans) && kept(stat.indexScans))
  const reads = {
    byIndex: sum(counted.map((stat) => stat.indexScans)),
    byScan: sum(counted.map((stat) => stat.seqScans)),
  }
  return {
    tables: catalog.objects.tables?.length ?? 0,
    others: ROW_GROUPS.slice(1).flatMap((group) => {
      const count = catalog.objects[group]?.length ?? 0
      return count > 0 ? [{ group, count }] : []
    }),
    rows: rows.length > 0 ? sum(rows) : null,
    bytes: sizes.length > 0 ? sum(sizes) : null,
    indexBytes: indexes.length > 0 ? sum(indexes) : null,
    reclaimable: bloat.length > 0 ? sum(bloat) : null,
    reads: reads.byIndex + reads.byScan > 0 ? reads : null,
    largest: ranked.map((entry) => ({
      object: entry.object,
      group: entry.group,
      bytes: entry.bytes,
      rows: entry.rows,
      share: top > 0 ? (measure(entry) ?? 0) / top : 0,
    })),
    rankedBy,
    measured: sizes.length,
  }
}
