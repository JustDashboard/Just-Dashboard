import { bytes, plural } from "@/lib/format"
import { compact } from "@/components/database/home/readings"

/** One schema of a SQL database, as the picture's right lane draws it. */
export type SchemaShare = { schema: string; bytes: number; rows: number; tables: number }

/**
 * The tables of a database summed by schema, largest first: what each schema
 * holds and how many of the tables read it is. Ranked by bytes where the
 * engine states sizes and by rows where it does not, the order the largest
 * tables are listed in under it.
 */
export function bySchema(
  tables: readonly { schema: string; rows: number; bytes: number }[],
  sizesKnown: boolean,
): SchemaShare[] {
  const sums = new Map<string, SchemaShare>()
  for (const table of tables) {
    const name = table.schema || "default"
    const sum = sums.get(name) ?? { schema: name, bytes: 0, rows: 0, tables: 0 }
    sum.bytes += Math.max(table.bytes, 0)
    sum.rows += Math.max(table.rows, 0)
    sum.tables += 1
    sums.set(name, sum)
  }
  const weigh = (share: SchemaShare) => (sizesKnown ? share.bytes : share.rows)
  return [...sums.values()].sort((a, b) => weigh(b) - weigh(a) || a.schema.localeCompare(b.schema))
}

/** A schema's line under its name: its size where it is known, then how many tables. */
export function schemaDetail(share: SchemaShare, sizesKnown: boolean, noun = "table"): string {
  const count = plural(share.tables, noun)
  return sizesKnown ? `${bytes(share.bytes)} · ${count}` : `${compact(share.rows)} rows · ${count}`
}

const DAY = 86_400_000

/**
 * How the wire to a database's dumps is drawn, by the newest one's age:
 * green while it is under a day old, amber past a week — the age the
 * section calls stale everywhere else — and plain between. A database with
 * none is plain too; the picture dashes that wire.
 */
export function copyTone(
  takenAt: string | undefined,
  now: number,
): "success" | "warning" | "default" {
  const taken = takenAt ? Date.parse(takenAt) : NaN
  if (Number.isNaN(taken)) return "default"
  if (now - taken > 7 * DAY) return "warning"
  return now - taken < DAY ? "success" : "default"
}
