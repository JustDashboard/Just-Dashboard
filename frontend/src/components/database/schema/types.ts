import type { DbCatalogObject } from "@/components/database/data/types"

/**
 * What the schema browser reads off the wire beyond what the table editor
 * already describes (`data/types.ts`: the catalogue, a table's detail, a
 * structure change's answer). These are the schema stream's object routes and
 * the operations stream's statistics and maintenance, as their contracts
 * state them, kept beside the page that reads them.
 */
export type {
  DbCatalog,
  DbCatalogSchema,
  DbColumn,
  DbConstraint,
  DbForeignKey,
  DbIncomingForeignKey,
  DbIndex,
  DbObjectFact,
  DbTableDetail,
} from "@/components/database/data/types"

/** What the catalogue calls a thing; the address carries it as written. */
export type DbObjectKind =
  | "table"
  | "view"
  | "materialized_view"
  | "function"
  | "procedure"
  | "trigger"
  | "sequence"
  | "enum"
  | "domain"
  | "composite"
  | "range"
  | "type"
  | "event"
  | "package"
  | "synonym"
  | "dictionary"

/** One object of the catalogue, with the fields the kinds without rows carry. */
export interface SchemaObject extends DbCatalogObject {
  /** A routine's argument list; it tells overloads apart and goes back to `/object` verbatim. */
  signature?: string
  returns?: string
  language?: string
  /** An enum's labels, in declared order. */
  values?: string[]
  /** PostgreSQL: the extension that installed this function. */
  extension?: string
}

/** One object's definition (`GET /{id}/object`). */
export interface DbObjectDefinition {
  kind: DbObjectKind
  schema: string
  name: string
  signature?: string
  table?: string
  /** The CREATE statement; may be "" and then `note` says why. */
  definition: string
  /** The engine's own text, or assembled by the dashboard from the catalogue. */
  source: "engine" | "generated"
  note?: string
  owner?: string
  comment?: string
  language?: string
  returns?: string
  values?: string[]
  details: { name: string; value: string }[]
}

/** What a structure change answers, run or previewed (`/ddl/*`). */
export interface DdlAnswer {
  statement: string
  statements: string[]
  preview?: true
  /** Functions the request's own SQL calls that the server does not vouch for. */
  calls?: string[]
}

/** One table's figures (`GET /{id}/tablestats`). A count the engine does not keep is -1. */
export interface DbTableStat {
  schema: string
  table: string
  kind?: string
  engine?: string
  rows: number
  deadRows: number
  totalBytes: number
  tableBytes: number
  indexBytes: number
  toastBytes: number
  /** An estimate; -1 where it cannot be derived. */
  bloatBytes: number
  seqScans: number
  seqRowsRead: number
  indexScans: number
  indexRowsRead: number
  inserts: number
  updates: number
  deletes: number
  modsSinceAnalyze: number
  lastVacuum?: string
  lastAutovacuum?: string
  lastAnalyze?: string
  lastAutoanalyze?: string
  parts?: number
  uncompressedBytes?: number
}

export interface DbTableStats {
  supported: boolean
  reason?: string
  schema: string
  tables: DbTableStat[]
  truncated: boolean
  notes?: string[]
}

/** One index's figures (`GET /{id}/indexstats`). */
export interface DbIndexStat {
  schema: string
  table: string
  name: string
  method?: string
  columns: string[]
  definition?: string
  unique: boolean
  primary: boolean
  valid: boolean
  constraint?: boolean
  bytes: number
  /** -1 = the engine does not count. */
  scans: number
  rowsRead: number
  unused: boolean
  duplicateOf?: string
  coveredBy?: string
}

export interface DbIndexStats {
  supported: boolean
  reason?: string
  schema: string
  indexes: DbIndexStat[]
  truncated: boolean
  notes?: string[]
}

/** One action of the engine's closed maintenance list (`GET /{id}/maintenance`). */
export interface DbMaintenanceAction {
  id: string
  label: string
  description: string
  scope: "table" | "database" | "either"
  blocking?: boolean
  destructive?: boolean
  readOnly?: boolean
  requires: "service.control" | "destructive"
  options?: ("concurrently" | "mode" | "final" | "online")[]
}

export interface DbMaintenanceResult {
  action: string
  statements: string[]
  output: string[]
  outputTruncated: boolean
  duration: string
  ok: boolean
}
