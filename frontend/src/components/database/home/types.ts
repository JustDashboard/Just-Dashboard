import type { DbPoolStats } from "@/lib/types"

/**
 * What the home reads, as the server answers it. Each type is the part of a
 * route's answer the home draws — the pages that own a route in full
 * (Performance, Advisor, Backups, the Redis and MongoDB areas) keep the whole
 * of it beside themselves.
 */

/** Sessions on the whole server, by what they are doing. */
export type DbConnectionCounts = {
  total: number
  active: number
  idle: number
  idleInTransaction: number
  /** Waiting on a lock. */
  waiting: number
  /** The configured limit; 0 where the engine has none. */
  max: number
  reserved?: number
}

/**
 * `GET /databases/{id}/stats` on a SQL engine: one snapshot of raw totals and
 * of readings of now. A counter is never a rate; the page keeps the samples
 * and divides.
 */
export type DbServerStats = {
  /** False when the engine refused the snapshot; `reason` is its own words. */
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
  counters: Record<string, number>
  gauges: Record<string, number>
  facts?: Record<string, string>
  /** Parts the engine refused, in sentences. */
  notes?: string[]
  pool: DbPoolStats | null
}

/** The same route on a Redis-family server: INFO, with the figures as numbers. */
export type RedisStats = {
  server: {
    counters?: Record<string, number>
    flavor?: string
    version?: string
    role?: "primary" | "replica" | "sentinel"
    mode?: "standalone" | "cluster" | "sentinel"
    /** The dashboard's clock when INFO answered. */
    sampledAtMs?: number
  }
}

/** The same route on MongoDB: `serverStatus`, one level deep. */
export type MongoStats = {
  server: {
    version?: string
    uptime?: number
    /** The server's clock, in milliseconds. */
    timestamp?: number
    role?: string
    storageEngine?: string
    connections?: { current?: number; available?: number; active?: number }
    network?: { bytesIn?: number; bytesOut?: number; numRequests?: number }
    opcounters?: Record<string, number>
    mem?: { resident?: number; virtual?: number }
    documents?: { inserted?: number; returned?: number; updated?: number; deleted?: number }
    scanned?: { keys?: number; documents?: number }
    queue?: {
      activeReaders?: number
      activeWriters?: number
      queuedReaders?: number
      queuedWriters?: number
    }
    cache?: { bytes?: number; maxBytes?: number; dirtyBytes?: number }
    repl?: { setName?: string; role?: string; primary?: string }
  }
}

/** `GET /databases/{id}/redis/server`: what the server is and how it keeps its data. */
export type RedisServer = {
  flavor: string
  version: string
  mode: "standalone" | "cluster" | "sentinel"
  role: "primary" | "replica" | "sentinel"
  /** The connection string's own database. */
  db: number
  databases: number
  uptimeSeconds: number
  notice?: string
  keyspace: { db: number; keys: number; expires: number; avgTtlMs: number }[]
  memory: {
    used: number
    rss: number
    peak: number
    /** 0 = no limit. */
    max: number
    policy?: string
    fragmentationRatio?: number
    systemTotal?: number
  }
  persistence: {
    loading: boolean
    rdb: {
      /** "" = snapshots off; absent = unknown. */
      schedule?: string
      lastSaveAt?: string
      changesSinceSave: number
      inProgress: boolean
      lastStatus?: string
    }
    aof: { supported: boolean; enabled: boolean; lastWriteStatus?: string }
  }
  replication: {
    role: "primary" | "replica"
    replicas: { addr: string; state: string; lagSeconds: number }[]
    primary?: { addr: string; up: boolean; lastIoSecondsAgo: number; syncing: boolean }
  }
}

/** `GET /databases/{id}/redis/commandstats`, by total time. */
export type RedisCommandStats = {
  commands: { command: string; calls: number; usec: number; usecPerCall: number }[]
  totalCalls: number
  totalUsec: number
  sampledAtMs: number
}

/** One namespace of `GET /databases/{id}/keys/tree`. */
export type RedisTreeFolder = {
  name: string
  prefix: string
  pattern: string
  count: number
  keyCount: number
  folders: number
  types: Record<string, number>
}

export type RedisTree = {
  db: number
  delimiter: string
  folders: RedisTreeFolder[]
  foldersOmitted?: number
  /** Keys with no namespace, seen by this call. */
  keyCount: number
  /** Keys this call examined. */
  count: number
  /** Those keys by type, as the server names each. */
  types?: Record<string, number>
  scanned: number
  complete: boolean
  /** DBSIZE. */
  total: number
}

/** One collection of `GET /databases/{id}/mongo/collections`. */
export type MongoCollection = {
  name: string
  type: "collection" | "view" | "timeseries"
  system: boolean
  /** False for a view, and past the first 400 collections of a database. */
  statsKnown: boolean
  count: number
  /** Uncompressed data bytes. */
  size: number
  storageSize: number
  indexCount: number
  indexSize: number
}

export type MongoCollections = {
  database: string
  collections: MongoCollection[]
  statsTruncated: boolean
}

/** `GET /databases/{id}/mongo/profiler`: the slow operations a database recorded. */
export type MongoProfiler = {
  database: string
  /** off, slow operations, every operation. */
  level: 0 | 1 | 2
  slowMs: number
  entries: {
    time: string
    op: string
    ns: string
    millis: number
    command: string
    planSummary: string
    docsExamined: number
    returned: number
  }[]
}

export type DbAdviceTarget = {
  kind: string
  schema?: string
  name: string
  table?: string
  detail?: string
  sql?: string
}

/** One finding of `GET /databases/{id}/advisor`. */
export type DbAdvice = {
  id: string
  level: "critical" | "warning" | "notice"
  category: "security" | "performance" | "reliability" | "maintenance"
  title: string
  detail: string
  advice?: string
  objects?: string[]
  targets?: DbAdviceTarget[]
  sql?: string
  /** A page of the dashboard that acts on it, as an absolute path. */
  link?: string
}

export type DbAdviseReport = {
  checkedAt: string
  /** What was not assessed, in sentences. */
  silences: string[]
  findings: DbAdvice[]
  tablesChecked: number
  truncated: boolean
}

export type DbStatement = {
  id: string
  /** A shape with its literals removed, never one execution. */
  query: string
  calls: number
  totalMs: number
  meanMs: number
  rows: number
  /** Of all tracked statements' runtime, 0..1. */
  share: number
}

export type DbStatements = {
  statements: DbStatement[]
  supported: boolean
  reason?: string
  totalMs: number
  since?: string
  /** Present when the statistics are off and could be turned on. */
  enable?: {
    extension?: string
    /** Set only when that one statement is enough. */
    sql?: string
    available: boolean
    preloaded: boolean
    note?: string
  }
}

export type DbTableStat = {
  schema: string
  table: string
  /** An estimate; -1 where the engine keeps none. */
  rows: number
  totalBytes: number
}

export type DbTableStats = {
  supported: boolean
  reason?: string
  /** Largest first. */
  tables: DbTableStat[]
  /** The schema has more tables than were asked for. */
  truncated: boolean
}

export type DbBackupFile = {
  file: string
  size: number
  takenAt: string
  format: string
  durationMs?: number
  tool?: string
  summary?: string
  origin?: "dump" | "upload" | "safety"
}

/** A dump, a restore or a copy the server is running in the background. */
export type DbTransferJob = {
  id: string
  title: string
  status: "running" | "succeeded" | "failed" | "cancelled"
  error?: string
  startedAt: string
}

export type DbBackups = {
  dir: string
  /** Newest first. A dump still being written is not among them. */
  files: DbBackupFile[]
  job?: DbTransferJob
}

/** One database of `GET /databases/{id}/schemas`: what the server holds besides this one. */
export type DbServerDatabase = {
  name: string
  size?: number
  owner?: string
  encoding?: string
}

/** `GET /databases/{id}/sqlite/file`: the file, as SQLite describes it. */
export type SqliteFile = {
  path: string
  fileBytes: number
  walBytes: number
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
  version: string
  objects: Record<string, number>
}
