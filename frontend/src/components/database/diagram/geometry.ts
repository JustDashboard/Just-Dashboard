import type { DbGraphColumn, DbGraphTable } from "@/components/database/diagram/types"

/**
 * The sizes a table is drawn at. The node component draws to them and the
 * layout computes with them, so what dagre is told a table measures is what
 * it measures: a wrong height is how boxes come to sit on the edges beneath
 * them, and it stays invisible until a table has thirty columns.
 */
export const ROW_HEIGHT = 26
export const HEADER_HEIGHT = 38
export const NOTE_HEIGHT = 24
export const NODE_WIDTH = 264

/** How much of each table is drawn: every column, only the keys, or the name alone. */
export type DiagramDetail = "all" | "keys" | "names"

/** The columns a table draws at this level of detail. */
export function visibleColumns(table: DbGraphTable, detail: DiagramDetail): DbGraphColumn[] {
  if (detail === "names") return []
  if (detail === "keys")
    return table.columns.filter((c) => c.primaryKey || c.foreignKey || c.unique)
  return table.columns
}

/** The node's height, as the node component will draw it. */
export function nodeHeight(table: DbGraphTable, detail: DiagramDetail, note?: string): number {
  const shown = visibleColumns(table, detail).length
  // The "n more columns" row takes space too; a names-only table draws none.
  const more = detail === "keys" && shown < table.columns.length ? 1 : 0
  const rows = detail === "names" ? 0 : Math.max(1, shown + more)
  return HEADER_HEIGHT + rows * ROW_HEIGHT + (note ? NOTE_HEIGHT : 0)
}

/** A row count for a narrow slot: 1_234_567 → "1.2M", not "1,234,567". */
export function compactRows(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
}

/**
 * Type names are for recognition here, not reproduction. "timestamp with time
 * zone" in a ten-pixel column pushes the name out of the box, and a reader
 * already knows what timestamptz means.
 */
export function shortType(type: string): string {
  const map: Record<string, string> = {
    "timestamp with time zone": "timestamptz",
    "timestamp without time zone": "timestamp",
    "character varying": "varchar",
    "double precision": "float8",
    bigint: "int8",
    integer: "int4",
    smallint: "int2",
    boolean: "bool",
    character: "char",
  }
  const mapped = map[type.toLowerCase()] ?? type
  return mapped.length > 14 ? mapped.slice(0, 13) + "…" : mapped
}
