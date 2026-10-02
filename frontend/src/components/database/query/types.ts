import type { CellValue, ClippedCell } from "@/components/database/grid"

/**
 * What the SQL editor reads off the wire, as the workbench and schema
 * contracts state it: one statement and a script, what a statement would do
 * before it runs, a plan, the history and the saved queries, and the outline
 * the schema tree and the completion are drawn from. Kept beside the page that
 * reads them.
 */

export type RiskLevel = "read" | "medium" | "high" | "critical"

/** What running a statement would do, as the server judges it. */
export interface Risk {
  /** The level is high or critical: running it needs the destructive capability. */
  destructive: boolean
  level: RiskLevel
  /** Sentences for the reader; none for a statement that only reads. */
  reasons: string[]
}

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

export interface QueryResult {
  columns: string[]
  /** The driver's type name per column; may be "". */
  types: string[]
  /** Absent for a statement that returned no result set. */
  kinds?: ValueKind[]
  rows: CellValue[][]
  rowCount: number
  rowsAffected: number
  /** A Go duration: "1.2ms". */
  duration: string
  /** More rows existed than were returned. */
  truncated: boolean
  statement: string
  clipped?: ClippedCell[]
}

export interface QueryResponse {
  result: QueryResult
  risk: Risk
}

export interface ScriptStep {
  index: number
  sql: string
  /** 1-based line of the statement's first token in the script that was sent. */
  line: number
  risk: Risk
  /** Skipped: it came after the statement that failed. */
  status: "ok" | "error" | "skipped"
  result?: QueryResult
  error?: string
  durationMs: number
}

export interface ScriptResponse {
  statements: ScriptStep[]
  /** The index of the statement that stopped the script, or -1. */
  failed: number
  transaction: "none" | "read_only" | "committed" | "rolled_back"
  durationMs: number
  risk: Risk
}

export interface ClassifyResponse extends Risk {
  /** Empty when the text could not be read; `reasons` then says why. */
  statements: { sql: string; line: number; risk: Risk }[]
}

export interface ExplainResponse {
  /** The plan as the engine returned it: rows of text, or one JSON cell. */
  result: QueryResult
  /** The parsed plan, when it was asked for as JSON. */
  plan?: unknown
  format: "text" | "json"
  analyzed: boolean
  /** A statement that changes data was executed to measure it, and undone. */
  rolledBack: boolean
}

export interface HistoryEntry {
  id: number
  sql: string
  risk: RiskLevel
  success: boolean
  durationMs: number
  rowCount: number
  rowsAffected?: number
  /** The engine's own text, or "the statement was cancelled". */
  error?: string
  ranAt: string
}

export interface SavedQuery {
  id: number
  name: string
  sql: string
  createdAt: string
  updatedAt?: string
}

export interface OutlineEntry {
  id: string
  schema: string
  name: string
  type: string
}

/** Every table and view with its column names, in one read. */
export interface Outline {
  schema: string
  /** Column names in order, by `entry.id`. */
  tables: Record<string, string[]>
  entries: OutlineEntry[]
  truncated: boolean
  total: number
  limit: number
}

export interface CatalogSchema {
  name: string
  system?: boolean
  default?: boolean
  tables: number
}

/** The part of the catalogue this page reads: which schemas there are, and which is the connection's own. */
export interface CatalogHead {
  schema: string
  defaultSchema: string
  schemas: CatalogSchema[]
}

export interface ColumnInfo {
  name: string
  type: string
  nullable: boolean
}
