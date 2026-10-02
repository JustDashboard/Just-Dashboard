import { isObjectKind } from "@/components/database/schema/kind-names"
import type { DbObjectKind, SchemaObject } from "@/components/database/schema/types"

/**
 * What the schema browser is showing, as its address states it.
 *
 * `schema` and `table` are the section's own keys, so a table chosen here is
 * the table Data opens on. A table, a view and a materialized view are all
 * `table` — each has columns and rows. Everything else in a schema is named
 * by its kind: `object` (function, trigger, sequence, enum …), `name`, and
 * the two things that tell namesakes apart, `sig` for a routine's arguments
 * and `on` for the table a trigger fires on.
 *
 * `view` is which reading of a table is open, and `new` asks for a creation
 * form on arrival: the table editor's "New table" is a link to `?new=table`.
 */
export const TABLE_VIEWS = [
  "columns",
  "indexes",
  "keys",
  "triggers",
  "definition",
  "statistics",
] as const
export type TableViewId = (typeof TABLE_VIEWS)[number]

export const CREATIONS = ["table", "view", "schema", "type"] as const
export type Creation = (typeof CREATIONS)[number]

export type Selected =
  | { type: "table"; schema: string; name: string }
  | {
      type: "object"
      kind: DbObjectKind
      schema: string
      name: string
      signature: string
      table: string
    }
  | null

export type SchemaAddress = {
  schema: string
  selected: Selected
  view: TableViewId
  creating: Creation | null
}

type Selection = { schema: string; table: string }

export function readAddress(selection: Selection, param: (name: string) => string): SchemaAddress {
  const kind = param("object")
  const name = param("name")
  const view = param("view")
  const creating = param("new")
  let selected: Selected = null
  if (selection.table) {
    selected = { type: "table", schema: selection.schema, name: selection.table }
  } else if (name && isObjectKind(kind)) {
    selected = {
      type: "object",
      kind,
      schema: selection.schema,
      name,
      signature: param("sig"),
      table: param("on"),
    }
  }
  return {
    schema: selection.schema,
    selected,
    view: (TABLE_VIEWS as readonly string[]).includes(view) ? (view as TableViewId) : "columns",
    creating: (CREATIONS as readonly string[]).includes(creating) ? (creating as Creation) : null,
  }
}

type Params = Record<string, string | null>

/** Every key of the address this page writes besides the section's own. */
const CLEARED: Params = { object: null, name: null, sig: null, on: null, view: null, new: null }

/** The keys that open a table (or a view): nothing of another object is left behind. */
export function tableParams(schema: string, name: string): Params {
  return { ...CLEARED, schema: schema || null, table: name }
}

/** The keys that open an object that holds no rows. */
export function objectParams(
  object: Pick<SchemaObject, "kind" | "schema" | "name" | "signature" | "table">,
): Params {
  return {
    ...CLEARED,
    schema: object.schema || null,
    table: null,
    object: object.kind,
    name: object.name,
    sig: object.signature || null,
    on: object.kind === "trigger" ? object.table || null : null,
  }
}

/** The keys that open a schema with nothing chosen in it. */
export function schemaParams(schema: string): Params {
  return { ...CLEARED, schema: schema || null, table: null }
}

/** How one object is told from every other in a list: kind, schema, name and what tells namesakes apart. */
export function objectKey(
  object: Pick<SchemaObject, "kind" | "schema" | "name" | "signature" | "table">,
): string {
  return [
    object.kind,
    object.schema,
    object.name,
    object.signature ?? "",
    object.kind === "trigger" ? (object.table ?? "") : "",
  ].join("\u0000")
}
