import type { CellValue, ClippedCell } from "@/components/database/grid"
import type { DbCatalogGroup, DbExportFormat, DbImportFormat } from "@/lib/types"

/**
 * What the table editor reads off the wire, as the server's contracts state it
 * (browse, changes and cell from the workbench stream; catalogue and table
 * detail from the schema stream; export and import from the transfer stream).
 * Kept beside the page that reads them: `lib/types.ts` still describes the
 * section these routes replaced.
 */

/** What a column holds, in the server's vocabulary. */
export type ValueKind =
  | "text"
  | "integer"
  | "decimal"
  | "float"
  | "boolean"
  | "date"
  | "time"
  | "datetime"
  | "interval"
  | "json"
  | "uuid"
  | "binary"
  | "array"
  | "other"

export type FilterOp =
  | "eq"
  | "ne"
  | "lt"
  | "lte"
  | "gt"
  | "gte"
  | "in"
  | "not_in"
  | "between"
  | "contains"
  | "not_contains"
  | "icontains"
  | "prefix"
  | "suffix"
  | "regex"
  | "is_null"
  | "not_null"

/** One condition of a browse, exactly as `GET /browse?filters=` takes it. */
export interface Filter {
  column: string
  op: FilterOp
  /** Every operator but the lists, the range and the two NULL tests. */
  value?: string
  /** `in` / `not_in` (1..200), `between` (exactly two). */
  values?: string[]
}

export interface SortKey {
  column: string
  desc: boolean
}

/** A page of a table (`GET /{id}/browse`). */
export interface BrowsePage {
  columns: string[]
  /** The driver's type name per column; may be "". Never branch on it. */
  types: string[]
  kinds?: ValueKind[]
  rows: CellValue[][]
  rowCount: number
  rowsAffected: number
  /** A Go duration: "1.2ms". */
  duration: string
  /** There is another page. */
  truncated: boolean
  /** The statement that ran, with bind markers where the values went. */
  statement: string
  clipped?: ClippedCell[]
  /** [] when the table has none. ClickHouse: the sorting key, which repeats. */
  primaryKey: string[]
  /** Engine statistics for the whole table, filters ignored; null = unknown. */
  estimatedRows: number | null
  /** What was applied, the tie-break included. */
  sort: SortKey[]
  limit: number
  offset: number
}

export interface DbColumn {
  name: string
  type: string
  nullable: boolean
  default?: string
  key?: string
  position: number
  comment?: string
  identity?: string
  generated?: string
  generatedKind?: "stored" | "virtual"
  typeKind?: string
  enumValues?: string[]
}

export interface DbIndex {
  name: string
  columns: string[]
  unique: boolean
  primary: boolean
  method?: string
  predicate?: string
  include?: string[]
  expression?: boolean
  definition?: string
  size?: number
  constraint?: string
  invalid?: boolean
}

export interface DbForeignKey {
  name: string
  columns: string[]
  refSchema?: string
  refTable: string
  refColumns: string[]
  onUpdate?: string
  onDelete?: string
}

/** A foreign key in another table that points at this one. */
export interface DbIncomingForeignKey {
  name: string
  schema?: string
  table: string
  columns: string[]
  refColumns: string[]
  onUpdate?: string
  onDelete?: string
}

export interface DbConstraint {
  name: string
  type: "check" | "unique" | "exclusion"
  columns: string[]
  definition?: string
}

export interface DbObjectFact {
  name: string
  value: string
}

/** One table in full (`GET /{id}/table`). */
export interface DbTableDetail {
  schema: string
  name: string
  columns: DbColumn[]
  primaryKey: string[]
  indexes: DbIndex[]
  foreignKeys: DbForeignKey[]
  createSql?: string
  createSqlSource?: "engine" | "generated"
  /** "table" | "view" | "materialized view" | "partitioned table" | "partition" | … */
  type?: string
  owner?: string
  comment?: string
  /** -1 = unknown. */
  estimatedRows: number
  size?: number
  dataSize?: number
  indexSize?: number
  constraints: DbConstraint[]
  referencedBy: DbIncomingForeignKey[]
  facts: DbObjectFact[]
}

export interface DbCatalogSchema {
  name: string
  owner?: string
  comment?: string
  system?: boolean
  default?: boolean
  /** Tables and views inside; -1 when the engine was not asked. */
  tables: number
  detail?: string
}

export interface DbCatalogObject {
  kind: string
  schema: string
  name: string
  owner?: string
  comment?: string
  /** -1 = unknown. Present only for kinds that hold rows. */
  estimatedRows?: number
  size?: number
  table?: string
  detail?: string
}

/** What a schema holds (`GET /{id}/catalog`). */
export interface DbCatalog {
  schema: string
  defaultSchema: string
  schemas: DbCatalogSchema[]
  /** A group the engine lacks is absent; one it has and that is empty is []. */
  objects: Partial<Record<DbCatalogGroup, DbCatalogObject[]>>
  truncated: DbCatalogGroup[]
  limit: number
  errors?: Partial<Record<DbCatalogGroup, string>>
}

export type ChangeOp = "insert" | "update" | "delete"

export interface ChangeOutcome {
  index: number
  op: ChangeOp
  statement: string
  rowsAffected: number
  row?: Record<string, CellValue>
  clipped?: string[]
}

/** What `POST /{id}/changes` answers, for a dry run and for the real thing. */
export interface ChangesResponse {
  applied: boolean
  dryRun: boolean
  keyColumns: string[]
  /** One per change, values written in — for reading only. */
  statements: string[]
  results: ChangeOutcome[]
  attempts: number
  duration: string
}

/** One whole value (`GET /{id}/cell`). */
export interface CellRead {
  column: string
  type: string
  kind: ValueKind
  encoding: "text" | "base64" | "json" | "null"
  value: unknown
  size: number
}

export interface DbExportState {
  status: "running" | "complete" | "truncated" | "failed"
  rows: number
  format: DbExportFormat
  error?: string
  startedAt: string
  finishedAt?: string
}

export type DbImportMode = "insert" | "upsert" | "replace"

export interface DbImportOptions {
  schema?: string
  table: string
  format?: DbImportFormat
  delimiter?: string
  quote?: string
  header?: boolean
  encoding?: "utf-8" | "utf-16" | "latin-1"
  nullToken?: string
  /** Source column → table column; "" leaves the source column out. */
  mapping?: Record<string, string>
  mode?: DbImportMode
  /** What an upsert matches rows by; absent = the primary key. */
  conflict?: { constraint?: string; columns?: string[] }
  skipBadRows?: boolean
  dryRun?: boolean
  /** Import into a table that does not exist yet; `columns` override the inferred ones by name. */
  createTable?: { columns?: DbNewColumn[] }
}

/** A column of a table the import makes. */
export interface DbNewColumn {
  name: string
  /** "" = the type the server inferred from the file. */
  type: string
  notNull: boolean
  primaryKey: boolean
  default?: string
}

export interface DbImportColumn {
  source: string
  index: number
  /** "" = not imported. */
  target: string
  inferred: "empty" | "integer" | "decimal" | "boolean" | "date" | "datetime" | "json" | "text"
  targetType?: string
  examples: string[]
  warning?: string
}

export interface DbImportRowError {
  row: number
  line: number
  message: string
}

export interface DbImportReport {
  dryRun: boolean
  schema: string
  table: string
  format: DbImportFormat
  encoding: "utf-8" | "utf-16" | "latin-1"
  mode: DbImportMode
  columns: DbImportColumn[]
  rowsRead: number
  inserted: number
  updated: number
  skipped: number
  errors: DbImportRowError[]
  errorsTruncated: boolean
  preview?: { columns: string[]; rows: (string | null)[][] }
  /** With `createTable`: the statement the table is made with, and whether it was. */
  create?: { statement: string; columns: DbNewColumn[]; created: boolean }
  key?: string[]
  statement: string
  atomic: boolean
  warnings: string[]
}

/** What a structure change answers, run or previewed (`/ddl/*`). */
export interface DbDdlResult {
  statement: string
  statements: string[]
  preview?: true
}
