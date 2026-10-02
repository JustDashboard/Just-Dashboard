import type {
  GridColumn,
  GridColumnKind,
  GridFilterRequest,
  GridRow,
} from "@/components/database/grid"
import {
  cellOf,
  dotted,
  printCanonical,
  type BsonNode,
  type CellKind,
} from "@/components/database/mongo/bson"
import type { MongoDoc } from "@/components/database/mongo/types"

/** A listed document with its tree; `null` where the text could not be read as a document. */
export type Listed = { doc: MongoDoc; root: BsonNode | null }

const GRID_KIND: Record<CellKind, GridColumnKind> = {
  text: "text",
  number: "number",
  boolean: "boolean",
  datetime: "datetime",
  uuid: "uuid",
  json: "json",
  array: "array",
  unknown: "unknown",
}

type TableModel = {
  columns: GridColumn[]
  rows: GridRow[]
  /** Per column, a drawn value's canonical Extended JSON: what a filter on it is written with. */
  literals: Map<string, Map<string, string>>
}

/**
 * The documents as rows over the union of their top-level fields, in the
 * order the fields are first met, `_id` first. A column takes a kind when
 * every value in it has that kind, and is plain where they differ.
 */
export function tableModel(listed: Listed[]): TableModel {
  const names: string[] = []
  const seen = new Set<string>()
  const kinds = new Map<string, Set<CellKind>>()
  const literals = new Map<string, Map<string, string>>()
  const cells: Map<string, unknown>[] = []
  for (const { root } of listed) {
    const row = new Map<string, unknown>()
    if (root?.type === "object") {
      for (const field of root.fields) {
        if (!seen.has(field.name)) {
          seen.add(field.name)
          names.push(field.name)
        }
        const cell = cellOf(field.value)
        row.set(field.name, cell.value)
        if (field.value.type !== "null") {
          kinds.set(field.name, (kinds.get(field.name) ?? new Set()).add(cell.kind))
        }
        const known = literals.get(field.name) ?? new Map<string, string>()
        const key = JSON.stringify(cell.value)
        if (!known.has(key)) known.set(key, printCanonical(field.value))
        literals.set(field.name, known)
      }
    }
    cells.push(row)
  }
  if (seen.has("_id")) names.splice(0, 0, ...names.splice(names.indexOf("_id"), 1))
  const columns: GridColumn[] = names.map((name) => {
    const found = kinds.get(name)
    const kind = found?.size === 1 ? GRID_KIND[[...found][0]] : "unknown"
    return { key: name, name, typeName: "", kind, nullable: true }
  })
  const rows: GridRow[] = cells.map(
    // A field the document lacks reads as NULL here, which is also how the
    // engine itself matches one: `{ field: null }` finds both.
    (row) => names.map((name) => (row.get(name) ?? null) as GridRow[number]),
  )
  return { columns, rows, literals }
}

/** The filter clause a "filter by this value" on a cell asks for, or `null` when it cannot be written. */
export function cellFilter(request: GridFilterRequest, model: TableModel): string | null {
  const name = dotted([request.column.key])
  if (name === null) return null
  const key = JSON.stringify(name)
  if (request.op === "is_null") return `{ ${key}: null }`
  if (request.op === "not_null") return `{ ${key}: { "$ne": null } }`
  const literal = model.literals.get(request.column.key)?.get(JSON.stringify(request.value))
  if (literal === undefined) return null
  return request.op === "eq" ? `{ ${key}: ${literal} }` : `{ ${key}: { "$ne": ${literal} } }`
}
