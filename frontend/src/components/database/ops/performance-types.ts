/**
 * What the operations routes of a SQL server answer, as their contract states
 * them. Kept beside the pages that read them: the section's shared types hold
 * only what several areas draw.
 *
 * A count the engine does not keep is `-1`, never `0`. Timestamps are RFC 3339
 * in UTC.
 */

// ---- GET /{id}/stats -------------------------------------------------------

export type DbPoolStats = {
  open: number
  inUse: number
  idle: number
  waitCount: number
  waitDuration: string
  maxOpen: number
  maxIdleClosed: number
  maxLifetimeClosed: number
}

export type DbConnectionCounts = {
  /** User sessions on the whole server. */
  total: number
  active: number
  idle: number
  idleInTransaction: number
  /** Waiting on a lock. */
  waiting: number
  /** The configured limit; 0 = the engine has none. */
  max: number
  reserved?: number
}

export type DbServerStats = {
  /** False when the engine refused the snapshot; `reason` is its own refusal. */
  supported: boolean
  reason?: string
  /** When the snapshot was read, on the dashboard's clock. */
  at: string
  driver: string
  version?: string
  startedAt?: string
  uptimeSeconds?: number
  role?: "primary" | "replica" | "standalone"
  database?: string
  databaseBytes: number
  connections?: DbConnectionCounts
  countersSince?: string
  /** Monotonic raw totals. Never a rate. */
  counters: Record<string, number>
  /** Readings of now. */
  gauges: Record<string, number>
  facts?: Record<string, string>
  /** Parts the engine refused, in sentences. */
  notes?: string[]
  pool: DbPoolStats | null
}

// ---- GET /{id}/activity ----------------------------------------------------

export type DbSessionStatus = "active" | "idle" | "idle_in_transaction" | "blocked" | "background"

export type DbActivity = {
  /** What cancel and kill take. Oracle's is "sid,serial#". */
  pid: string
  user?: string
  database?: string
  /** The engine's own word. */
  state?: string
  /** The same thing in shared words. */
  status?: DbSessionStatus
  /** How long the running statement has run; 0 when none is running. */
  seconds: number
  idleSeconds?: number
  transactionSeconds?: number
  query?: string
  client?: string
  application?: string
  wait?: string
  waitType?: string
  waitEvent?: string
  /** The old comma-joined form. Never split it: Oracle's pid has a comma. */
  blockedBy?: string
  /** Each entry is the `pid` of another row. */
  blockedByPids?: string[]
  transactionStart?: string
  queryStart?: string
  stateSince?: string
  connectedAt?: string
  self?: boolean
}

export type DbActivityResponse = { sessions: DbActivity[]; supported: boolean; reason?: string }

// ---- GET /{id}/locks -------------------------------------------------------

export type DbLockWait = {
  waitingPid: string
  blockingPid: string
  waitingUser?: string
  /** Empty when the blocker is not a session (a prepared transaction). */
  blockingUser?: string
  waitingQuery?: string
  /** The blocker's last statement — usually not the one holding the lock. */
  blockingQuery?: string
  blockingState?: string
  object?: string
  lockType?: string
  mode?: string
  waitSeconds: number
  /** The age of the blocker's transaction. */
  blockingSeconds?: number
}

export type DbHeldLock = {
  pid: string
  user?: string
  lockType?: string
  mode?: string
  granted: boolean
  object?: string
  state?: string
  query?: string
}

export type DbLocks = {
  supported: boolean
  reason?: string
  /** One row per (waiter, blocker), longest wait first. */
  waits: DbLockWait[]
  locks: DbHeldLock[]
  /** `locks` was cut at 500. */
  truncated: boolean
  notes?: string[]
}

// ---- GET /{id}/replication -------------------------------------------------

export type DbReplica = {
  name?: string
  client?: string
  state?: string
  syncState?: string
  /** -1 = unknown. */
  lagBytes: number
  lagSeconds: number
  sentLsn?: string
  replayLsn?: string
  since?: string
}

export type DbReplicationSlot = {
  name: string
  type?: string
  plugin?: string
  database?: string
  active: boolean
  temporary?: boolean
  /** WAL held back for the slot; -1 unknown. */
  retainedBytes: number
  walStatus?: string
}

export type DbPublication = {
  name: string
  owner?: string
  allTables: boolean
  insert: boolean
  update: boolean
  delete: boolean
  truncate: boolean
  tables: number
}

export type DbSubscription = {
  name: string
  owner?: string
  enabled: boolean
  publications: string[]
  running: boolean
  receivedLsn?: string
  lastMessage?: string
}

/** This server's own side when it follows another. */
export type DbReplicaSource = {
  channel?: string
  host?: string
  port?: string
  user?: string
  state?: string
  ioRunning: boolean
  sqlRunning: boolean
  lagSeconds: number
  lastError?: string
  position?: string
  gtid?: string
}

export type DbReplication = {
  supported: boolean
  reason?: string
  role: "primary" | "replica" | "standalone" | ""
  replicas: DbReplica[]
  slots: DbReplicationSlot[]
  publications: DbPublication[]
  subscriptions: DbSubscription[]
  sources: DbReplicaSource[]
  facts?: Record<string, string>
  notes?: string[]
}

// ---- GET /{id}/tablestats, /indexstats -------------------------------------

export type DbTableStat = {
  schema: string
  table: string
  kind?: "table" | "partitioned table" | "materialized view"
  engine?: string
  rows: number
  deadRows: number
  totalBytes: number
  tableBytes: number
  indexBytes: number
  toastBytes: number
  /** An estimate; -1 = not derivable. */
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

export type DbTableStats = {
  supported: boolean
  reason?: string
  schema: string
  /** Largest first. */
  tables: DbTableStat[]
  truncated: boolean
  notes?: string[]
}

export type DbIndexStat = {
  schema: string
  table: string
  name: string
  method?: string
  columns: string[]
  definition?: string
  unique: boolean
  primary: boolean
  /** False = a failed concurrent build, or a disabled index. */
  valid: boolean
  /** Dropping it is dropping a constraint. */
  constraint?: boolean
  bytes: number
  /** -1 = the engine does not count. */
  scans: number
  rowsRead: number
  unused: boolean
  /** An identical index on the same table — this is the copy to drop. */
  duplicateOf?: string
  /** A wider index whose leading columns are exactly this one's. */
  coveredBy?: string
}

export type DbIndexStats = {
  supported: boolean
  reason?: string
  schema: string
  indexes: DbIndexStat[]
  truncated: boolean
  notes?: string[]
}

// ---- /{id}/maintenance -----------------------------------------------------

export type DbMaintenanceOption = "concurrently" | "mode" | "final" | "online"

export type DbMaintenanceAction = {
  id: string
  label: string
  /** One or two sentences, including what it locks. */
  description: string
  /** table: `table` required; database: `table` refused. */
  scope: "table" | "database" | "either"
  /** Locks what it works on while it runs. */
  blocking?: boolean
  /** Can lose rows it cannot recover. */
  destructive?: boolean
  /** Reads and reports, changes nothing. */
  readOnly?: boolean
  /** The capability the run asks for. */
  requires: "service.control" | "destructive"
  options?: DbMaintenanceOption[]
}

export type DbMaintenanceList = { actions: DbMaintenanceAction[]; supported: boolean }

export type DbCheckpointMode = "passive" | "full" | "restart" | "truncate"

export type DbMaintenanceRequest = {
  action: string
  schema?: string
  table?: string
  index?: string
  options?: {
    concurrently?: boolean
    mode?: DbCheckpointMode
    final?: boolean
    online?: boolean
  }
}

export type DbMaintenanceResult = {
  action: string
  /** The SQL that ran. */
  statements: string[]
  /** The engine's own lines: at most 500. */
  output: string[]
  outputTruncated: boolean
  /** A Go duration, e.g. "132ms". */
  duration: string
  /** False = it ran and reported a problem in its output. */
  ok: boolean
}

// ---- GET /{id}/statements --------------------------------------------------

export type DbStatementSort = "total" | "mean" | "calls" | "rows" | "max"

export type DbStatement = {
  id: string
  /** A shape: the constants are already replaced. Not runnable as it stands. */
  query: string
  calls: number
  totalMs: number
  meanMs: number
  maxMs?: number
  rows: number
  /** -1 = unknown. */
  hitRatio: number
  /** totalMs of every tracked statement's, 0..1. */
  share: number
}

export type DbStatementsEnable = {
  extension?: string
  /** Present only when that one statement is enough. */
  sql?: string
  available: boolean
  preloaded: boolean
  note?: string
}

export type DbStatements = {
  statements: DbStatement[]
  supported: boolean
  reason?: string
  totalMs: number
  sort: string
  resettable: boolean
  since?: string
  enable?: DbStatementsEnable
}

// ---- POST /{id}/explain ----------------------------------------------------

export type DbPlanResult = {
  columns: string[]
  rows: (string | null)[][]
  statement?: string
}

export type DbExplainResponse = {
  result: DbPlanResult
  plan?: unknown
  format: "text" | "json"
  analyzed: boolean
  rolledBack: boolean
}

// ---- ClickHouse ------------------------------------------------------------

export type ChPartition = {
  database: string
  table: string
  partition: string
  parts: number
  rows: number
  bytes: number
  uncompressedBytes: number
  modified?: string
}

export type ChPart = {
  database: string
  table: string
  partition: string
  name: string
  active: boolean
  rows: number
  bytes: number
  compressedBytes: number
  uncompressedBytes: number
  marks: number
  level: number
  type: string
  disk: string
  modified?: string
}

export type ChParts = {
  database: string
  table?: string
  partitions: ChPartition[]
  parts: ChPart[]
  truncated: boolean
}

export type ChMerge = {
  database: string
  table: string
  elapsed: number
  /** 0..1 */
  progress: number
  parts: number
  resultPart: string
  bytes: number
  rowsRead: number
  rowsWritten: number
  memory: number
  isMutation: boolean
  type?: string
  algorithm?: string
}

export type ChMutation = {
  database: string
  table: string
  id: string
  command: string
  created?: string
  partsToDo: number
  done: boolean
  failReason?: string
  failedAt?: string
}

export type ChQuery = {
  id: string
  user: string
  client?: string
  clientName?: string
  database?: string
  kind?: string
  elapsed: number
  rowsRead: number
  bytesRead: number
  /** The server's estimate; 0 = none. */
  totalRows: number
  /** 0..1, or -1 when there is no estimate. */
  progress: number
  rowsWritten: number
  memory: number
  peakMemory: number
  cancelled: boolean
  query: string
  self?: boolean
}

// ---- SQLite ----------------------------------------------------------------

export type SqliteFile = {
  path: string
  fileBytes: number
  walBytes: number
  shmBytes: number
  modified?: string
  pageSize: number
  pageCount: number
  freelistPages: number
  reclaimableBytes: number
  journalMode: string
  autoVacuum: string
  synchronous: string
  encoding: string
  foreignKeys: boolean
  userVersion: number
  applicationId: number
  schemaVersion: number
  version: string
  /** table, index, view, trigger → count. */
  objects: Record<string, number>
  attached: { name: string; file: string }[]
  compileOptions: string[]
  /** dbstat is built in, so the table statistics have sizes. */
  sizesKnown: boolean
}
