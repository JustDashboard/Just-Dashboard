/**
 * The schema as a graph (`GET /{id}/graph`), as the schema stream's contract
 * states it. A table is known by its `id` — its schema and its name together
 * — and nothing here is ever keyed by a name alone: two schemas may each have
 * an `orders`, and a picture that spans both has to hold both.
 *
 * Kept beside the page that reads them: `lib/types.ts` still describes the
 * route as it was, when a table was its bare name.
 */

export interface DbGraphColumn {
  name: string
  type: string
  nullable: boolean
  primaryKey: boolean
  /** Bare name of the referenced table … */
  foreignKey?: string
  /** … and its node id. */
  foreignKeyId?: string
  unique?: boolean
}

export interface DbGraphTable {
  /** `schema.name`: what a node, a position, a note and a colour are keyed by. */
  id: string
  schema: string
  name: string
  type: string
  rows: number
  comment?: string
  columns: DbGraphColumn[]
}

export interface DbGraphEdge {
  name: string
  /** Node ids. `to` may name a table outside the tables returned. */
  from: string
  to: string
  fromSchema?: string
  fromTable: string
  fromColumn: string
  toSchema?: string
  toTable: string
  toColumn: string
  onDelete?: string
  onUpdate?: string
  cardinality: "one-to-one" | "many-to-one"
}

export interface DbSchemaGraph {
  /** The schema asked for; "" when it was every schema that is not the engine's own. */
  schema: string
  tables: DbGraphTable[]
  edges: DbGraphEdge[]
  /** There are more tables than `limit`, and the picture holds the first of them. */
  truncated: boolean
  /** How many there are to draw. */
  total: number
  limit: number
}

/** What the server keeps of an arrangement: the document whole, and when it was saved. */
export interface DbDiagramLayoutResponse {
  layout: Record<string, unknown> | null
  updatedAt?: string
}

/**
 * A graph as the page reads it, whatever came back: an older server (and the
 * browser fixture) sends tables without an `id` and edges without `from` /
 * `to`, and those are filled in here the way the server now names them.
 */
export function readGraph(raw: Partial<DbSchemaGraph> | undefined, schema: string): DbSchemaGraph {
  const idOf = (tableSchema: string | undefined, name: string) =>
    tableSchema ? `${tableSchema}.${name}` : name
  const tables = (raw?.tables ?? []).map((table) => ({
    ...table,
    id: table.id ?? idOf(table.schema, table.name),
    columns: table.columns ?? [],
  }))
  const edges = (raw?.edges ?? []).map((edge) => ({
    ...edge,
    from: edge.from ?? idOf(edge.fromSchema ?? raw?.schema, edge.fromTable),
    to: edge.to ?? idOf(edge.toSchema ?? raw?.schema, edge.toTable),
  }))
  return {
    schema: raw?.schema ?? schema,
    tables,
    edges,
    truncated: raw?.truncated ?? false,
    total: raw?.total ?? tables.length,
    limit: raw?.limit ?? tables.length,
  }
}
