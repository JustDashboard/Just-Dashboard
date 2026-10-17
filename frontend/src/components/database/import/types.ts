import type { Job } from "@/lib/types"

/**
 * What the import routes answer, as the server writes them. A database is
 * brought over from another server either by replication — the target stays
 * current until a cutover — or as a dump read from the source while it loads.
 */

export type DbImportMode = "replication" | "stream"

/**
 * Where an import is: `running` while a job does its work (the first part of a
 * replication, the whole of a stream, or a cutover's wait), `replicating`
 * between those two, and one of the three ways it ends.
 */
export type DbImportStatus = "running" | "replicating" | "cutting" | "done" | "failed" | "cancelled"

/** The address the import reads from, without its credentials. */
export type DbImportSource = {
  address: string
  user?: string
  database?: string
}

export type DbImport = {
  id: number
  /** What the publication, the subscription and the slot are called. */
  name: string
  mode: DbImportMode
  status: DbImportStatus
  /** The new database on this server. */
  database: string
  source: DbImportSource
  structureOnly: boolean
  /** The dashboard made the publication on the source, and removes it. */
  publicationMade: boolean
  /** Why it failed, or what it left behind. */
  detail?: string
  startedBy: string
  createdAt: string
  updatedAt: string
  finishedAt?: string
  /** The job doing its work now. */
  job?: Job
}

export type DbImportList = { imports: DbImport[] }

export type DbReplicationTable = {
  schema: string
  name: string
  /** The planner's estimate; -1 where it has never counted. */
  estimatedRows: number
  size: number
  /** How an update names its row: `none` makes the source refuse updates once published. */
  identity: "key" | "index" | "full" | "none"
}

export type DbReplicationReport = {
  version: string
  versionNumber: number
  database: string
  walLevel: string
  tables: DbReplicationTable[]
  sequences: number
  blockers: string[]
  warnings: string[]
}

export type DbImportInspection = {
  reachable: boolean
  error?: string
  /** The ways this engine can bring a database over, the better one first. */
  forms: DbImportMode[]
  source: DbImportSource & { version?: string; flavor?: string; flavorLabel?: string }
  /** What the publication, subscription and slot would be called. */
  name: string
  /** A dump can be loaded. */
  ready: boolean
  blockers: string[]
  warnings: string[]
  replication?: DbReplicationReport
  replicationReady: boolean
  /** The statement that publishes the tables, for an operator who runs it themselves. */
  publicationSql?: string
}

export type DbImportRequest = {
  source: string
  mode?: DbImportMode
  database: string
  name?: string
  structureOnly?: boolean
  publicationExists?: boolean
}

export type DbImportStarted = { import: DbImport; job: Job }

export type DbCopyProgress = {
  phase: "copying" | "dumping" | "loading"
  tables: number
  tablesDone: number
  rows: number
  /** What the planner expected; a table it never counted adds nothing. */
  estimatedRows: number
  /** What has passed from the dump to the load. */
  bytes: number
  table?: string
}

export type DbSubscriptionState = "waiting" | "copying" | "copied" | "synchronizing" | "ready"

export type DbSubscriptionTable = {
  schema: string
  name: string
  state: DbSubscriptionState
  /** Rows taken so far while a table copies; -1 when the server does not say. */
  rowsCopied: number
}

export type DbSubscription = {
  exists: boolean
  enabled: boolean
  tables: DbSubscriptionTable[]
  ready: number
  /** The worker that applies changes is running. */
  applying: boolean
  /** Failures the server recorded; -1 before PostgreSQL 15. */
  errors: number
}

export type DbSlotLag = {
  exists: boolean
  active: boolean
  /** WAL the source wrote that the target has not confirmed. Zero is caught up. */
  bytes: number
}

export type DbImportLive = {
  import: DbImport
  progress?: DbCopyProgress
  subscription?: DbSubscription
  lag?: DbSlotLag
  /** Why the source could not be asked just now. */
  sourceError?: string
}

export type DbImportRemoved = { warnings: string[] }
