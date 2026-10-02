import type {
  GridColumn,
  GridColumnKind,
  GridFilterRequest,
  GridRow,
  GridSort,
} from "@/components/database/grid"
import {
  TYPE_LABEL,
  cellOf,
  dotted,
  printCanonical,
  shellScalar,
  type BsonNode,
  type BsonType,
  type CellKind,
} from "@/components/database/mongo/bson"
import { scan, textOf } from "@/components/database/mongo/shell"
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
  /** Cells drawn as NULL because the document has no such field, not because it holds a null. */
  absent: number
}

/**
 * The documents as rows over the union of their top-level fields, in the
 * order the fields are first met, `_id` first.
 *
 * A column takes a kind when every value in it has that kind. Where the
 * values differ in type the column is plain and each value is spelled the way
 * the shell spells it — `"6abe…"` beside `ObjectId("6abe…")` — because the
 * bare digits of the two would read as the same thing. Its head says which
 * type the column holds, and, for a field only some of the documents have,
 * how many of them: a NULL in such a column may be a field that is not there.
 */
export function tableModel(listed: Listed[]): TableModel {
  const names: string[] = []
  const seen = new Set<string>()
  const kinds = new Map<string, Set<CellKind>>()
  const types = new Map<string, Set<BsonType>>()
  const held = new Map<string, number>()
  const nodes: Map<string, BsonNode>[] = []
  for (const { root } of listed) {
    const row = new Map<string, BsonNode>()
    if (root?.type === "object") {
      for (const field of root.fields) {
        if (!seen.has(field.name)) {
          seen.add(field.name)
          names.push(field.name)
        }
        row.set(field.name, field.value)
        held.set(field.name, (held.get(field.name) ?? 0) + 1)
        if (field.value.type !== "null") {
          kinds.set(field.name, (kinds.get(field.name) ?? new Set()).add(cellOf(field.value).kind))
          types.set(field.name, (types.get(field.name) ?? new Set()).add(field.value.type))
        }
      }
    }
    nodes.push(row)
  }
  if (seen.has("_id")) names.splice(0, 0, ...names.splice(names.indexOf("_id"), 1))

  const mixed = new Set(names.filter((name) => (kinds.get(name)?.size ?? 0) > 1))
  const columns: GridColumn[] = names.map((name) => {
    const found = kinds.get(name)
    const kind = found?.size === 1 ? GRID_KIND[[...found][0]] : "unknown"
    const of = types.get(name)
    const type = !of || of.size === 0 ? "Null" : of.size === 1 ? TYPE_LABEL[[...of][0]] : "mixed"
    const count = held.get(name) ?? 0
    const typeName = count < listed.length ? `${type} · ${count} of ${listed.length}` : type
    return { key: name, name, typeName, kind, nullable: true }
  })

  const literals = new Map<string, Map<string, string>>()
  let absent = 0
  const rows: GridRow[] = nodes.map((row) =>
    names.map((name) => {
      const node = row.get(name)
      if (node === undefined) {
        // A field the document lacks reads as NULL here, which is also how the
        // engine itself matches one: `{ field: null }` finds both.
        absent++
        return null
      }
      const value = (
        mixed.has(name) && node.type !== "object" && node.type !== "array" && node.type !== "null"
          ? shellScalar(node)
          : cellOf(node).value
      ) as GridRow[number]
      const known = literals.get(name) ?? new Map<string, string>()
      const key = JSON.stringify(value)
      if (!known.has(key)) known.set(key, printCanonical(node))
      literals.set(name, known)
      return value
    }),
  )
  return { columns, rows, literals, absent }
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

/* -------------------------------------------------------------------- sort */

/**
 * The query bar's Sort for the order a table's heads ask for: `{ "age": -1 }`.
 * `null` when a column cannot be named in a sort (its name holds a dot or
 * starts with `$`); the empty text when no column is sorted by.
 */
export function sortText(sort: GridSort): string | null {
  const parts: string[] = []
  for (const key of sort) {
    const name = dotted([key.column])
    if (name === null) return null
    parts.push(`${JSON.stringify(name)}: ${key.desc ? -1 : 1}`)
  }
  return parts.length === 0 ? "" : `{ ${parts.join(", ")} }`
}

/**
 * The order the query bar's Sort states, as the table's heads show it. A sort
 * that is not a plain list of fields going up or down — a text score, a
 * misspelling — marks no head: the server is the one that reads it.
 */
export function sortOf(text: string): GridSort {
  if (!text.trim()) return []
  const read = scan(text)
  if (!read.ok || read.value.kind !== "document") return []
  const sort: { column: string; desc: boolean }[] = []
  for (const field of read.value.fields) {
    const way = Number(textOf(text, field.value))
    if (way !== 1 && way !== -1) return []
    sort.push({ column: field.key, desc: way === -1 })
  }
  return sort
}
