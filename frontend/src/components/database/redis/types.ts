/**
 * What the Redis routes answer and accept, as their contract writes them
 * (`mountDatabaseRedisRoutes`). Kept beside the pages that read them: no
 * other part of the section speaks to these routes.
 */

/** Plain text when the bytes are valid UTF-8; otherwise base64. Never mangled. */
export type RedisBytes = string | { base64: string }

/** A sorted-set score. Redis allows ±inf; JSON does not. */
export type RedisScore = number | "inf" | "-inf"

export type RedisKey = {
  key: RedisBytes
  /** "string" | "list" | "set" | "zset" | "hash" | "stream" | "ReJSON-RL" | a module's own name. */
  type: string
  /** Seconds; -1 = no expiry. */
  ttl: number
  /** String bytes, or element count; 0 for a type with no length command. */
  size: number
  memory?: number
}

export type RedisPage = {
  keys: RedisKey[]
  /** Sent back as it came: a scan cursor is an unsigned 64-bit number. */
  cursor: string
  done: boolean
  db: number
  total: number
}

export type RedisTreeFolder = {
  name: RedisBytes
  /** Full path with its trailing delimiter: what expands the folder. */
  prefix: RedisBytes
  /** The glob that lists exactly this namespace's keys. */
  pattern: RedisBytes
  count: number
  keyCount: number
  folders: number
  types: Record<string, number>
  children?: RedisTreeFolder[]
  childrenOmitted?: number
}

export type RedisTree = {
  db: number
  delimiter: string
  prefix: RedisBytes
  folders: RedisTreeFolder[]
  foldersOmitted?: number
  keys: RedisKey[]
  keyCount: number
  count: number
  types: Record<string, number>
  scanned: number
  cursor: string
  complete: boolean
  total: number
  elapsedMs: number
}

export type RedisMetaFact = "memory" | "encoding" | "idleSeconds" | "frequency"

export type RedisKeyMeta = {
  key: RedisBytes
  db: number
  type: string
  ttl: number
  /** Milliseconds; -1 none. */
  pttl: number
  /** RFC 3339; absent when the key does not expire or the moment is past the year 9999. */
  expiresAt?: string
  length: number
  memory?: number
  encoding?: string
  idleSeconds?: number
  frequency?: number
  /** Why a fact is absent, in a sentence to show. */
  unavailable?: Partial<Record<RedisMetaFact, string>>
}

export type RedisRow = {
  field?: RedisBytes
  index?: number
  score?: RedisScore
  id?: string
  value?: RedisBytes
  /** Hash: the field's own expiry in seconds (-1 none), where the server has one. */
  ttl?: number
  fields?: [RedisBytes, RedisBytes][]
  /** The row carries only the first 64 KiB of its value. */
  truncated?: true
  bytes?: number
}

export type RedisMembers = {
  key: RedisBytes
  db: number
  type: string
  ttl: number
  pttl: number
  /** The whole key: bytes for a string, elements otherwise. */
  length: number
  rows: RedisRow[]
  string?: { value: RedisBytes; offset: number }
  json?: { path: string; matches?: unknown[]; bytes: number; tooLarge?: boolean }
  cursor: string
  done: boolean
  /** A module type with no reader: the sentence to show. */
  unsupported?: string
}

export type RedisWriteType = "string" | "hash" | "list" | "set" | "zset" | "stream" | "json"

export type RedisWriteRequest = {
  key: RedisBytes
  type?: RedisWriteType
  create?: boolean
  value?: RedisBytes
  field?: RedisBytes
  score?: RedisScore | string
  index?: number
  insert?: boolean
  position?: "head" | "tail"
  expect?: RedisBytes
  replace?: RedisBytes
  /** Seconds: >0 set; negative removes; absent keeps the key's expiry. */
  ttl?: number
  id?: string
  entries?: [RedisBytes, RedisBytes][]
  path?: string
}

export type RedisDeleteRequest = {
  key?: RedisBytes
  keys?: RedisBytes[]
  type?: string
  member?: RedisBytes
  members?: RedisBytes[]
  index?: number
  expect?: RedisBytes
  path?: string
}

export type RedisBulkAction = "delete" | "expire" | "persist"

export type RedisBulkRequest = {
  pattern: string
  type?: string
  action: RedisBulkAction
  ttl?: number
  dryRun?: boolean
  cursor?: string
  limit?: number
}

export type RedisBulkResult = {
  action: string
  dryRun: boolean
  db: number
  pattern: string
  type?: string
  matched: number
  affected: number
  complete: boolean
  cursor: string
  sample: RedisBytes[]
  total: number
  elapsedMs: number
}

export type RedisStreamGroup = {
  name: RedisBytes
  lastDeliveredId: string
  pending: number
  oldestPendingId?: string
  newestPendingId?: string
  lag?: number
  entriesRead?: number
  consumers: { name: RedisBytes; pending: number; idleMs: number }[]
}

export type RedisStreamInfo = {
  key: RedisBytes
  db: number
  length: number
  firstId?: string
  lastId?: string
  lastGeneratedId?: string
  entriesAdded?: number
  groups: RedisStreamGroup[]
  groupsOmitted?: number
}

export type RedisStreamPending = {
  entries: { id: string; consumer: RedisBytes; idleMs: number; deliveries: number }[]
  cursor: string
  done: boolean
}

export type RedisCommandClass = "read" | "write" | "dangerous" | "blocked"

export type RedisVerdict = {
  name: string
  class: RedisCommandClass
  /** Also needs system.admin. */
  admin: boolean
  /** May stall every other client while it runs. */
  slow: boolean
  /** False: not in the dashboard's table, classified from the server's flags. */
  known: boolean
  /** Sentence fragments whose subject is the command: "deletes keys". */
  reasons: string[]
}

export type RedisClassifyResponse = RedisVerdict & {
  requires: ("service.control" | "system.admin" | "destructive")[]
  allowed: boolean
}

export type RedisReply =
  | { type: "nil" }
  | { type: "status"; value: string }
  | { type: "string"; value: RedisBytes; truncated?: true; length?: number }
  | { type: "integer"; value: number | string }
  | { type: "double"; value: RedisScore | "nan" }
  | { type: "boolean"; value: boolean }
  | { type: "bignumber"; value: string }
  | { type: "error"; value: string }
  | { type: "array"; items: RedisReply[]; truncated?: true; length?: number }
  | {
      type: "map"
      entries: { key: RedisReply; value: RedisReply }[]
      truncated?: true
      length?: number
    }

export type RedisCommandResult = RedisVerdict & {
  db: number
  ms: number
  reply: RedisReply
  truncated: boolean
}

export type RedisCommandRef = {
  /** Upper case; subcommands as "CONFIG SET". */
  name: string
  group?: string
  summary?: string
  since?: string
  complexity?: string
  syntax?: string
  arity: number
  flags: string[]
  categories: string[]
  class: RedisCommandClass
  admin: boolean
  slow?: boolean
  deprecated?: boolean
}

export type RedisCommandReference = {
  flavor: string
  version: string
  /** False: names, arity and flags only — the server does not describe its commands. */
  documented: boolean
  commands: RedisCommandRef[]
}

export type RedisFeatures = {
  databases: boolean
  scanType: boolean
  memoryUsage: boolean
  objectEncoding: boolean
  objectIdleTime: boolean
  objectFreq: boolean
  unlink: boolean
  copy: boolean
  keepTtl: boolean
  hashFieldTtl: boolean
  streams: boolean
  json: boolean
  search: boolean
  timeSeries: boolean
  commandDocs: boolean
  slowlog: boolean
  latency: boolean
  config: boolean
  acl: boolean
  clientList: boolean
  monitor: boolean
  pubsub: boolean
  functions: boolean
  aof: boolean
}

export type RedisKeyspace = { db: number; keys: number; expires: number; avgTtlMs: number }

export type RedisServer = {
  flavor: "redis" | "valkey" | "keydb" | "dragonfly"
  version: string
  redisVersion: string
  mode: "standalone" | "cluster" | "sentinel"
  features: RedisFeatures
  modules: { name: string; version: number }[]
  role: "primary" | "replica" | "sentinel"
  /** The connection string's database: where the picker starts. */
  db: number
  /** How many logical databases (1 on a cluster node, 0 on a sentinel). */
  databases: number
  uptimeSeconds: number
  sampledAtMs: number
  /** Set for a cluster node or a sentinel. */
  notice?: string
  configFile?: string
  /** Only the databases that hold keys. */
  keyspace: RedisKeyspace[]
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
    dir?: string
    file?: string
    rdb: {
      /** "" = snapshots off; absent = unknown. */
      schedule?: string
      lastSaveAt?: string
      changesSinceSave: number
      inProgress: boolean
      lastStatus?: string
      lastDurationSeconds?: number
    }
    aof: {
      supported: boolean
      enabled: boolean
      rewriteInProgress: boolean
      lastRewriteStatus?: string
      lastWriteStatus?: string
      currentSize?: number
      baseSize?: number
      fsync?: string
    }
  }
  replication: {
    role: "primary" | "replica"
    offset: number
    replicas: { addr: string; state: string; offset: number; lagSeconds: number }[]
    primary?: {
      addr: string
      up: boolean
      lastIoSecondsAgo: number
      syncing: boolean
      offset: number
      readOnly: boolean
    }
  }
  cluster?: { state: string; slotsAssigned: number; knownNodes: number; size: number }
  sections: { name: string; fields: { name: string; value: string }[] }[]
}

export type RedisCounter =
  | "uptime_in_seconds"
  | "connected_clients"
  | "blocked_clients"
  | "used_memory"
  | "used_memory_rss"
  | "used_memory_peak"
  | "maxmemory"
  | "mem_fragmentation_ratio"
  | "total_connections_received"
  | "total_commands_processed"
  | "instantaneous_ops_per_sec"
  | "total_net_input_bytes"
  | "total_net_output_bytes"
  | "instantaneous_input_kbps"
  | "instantaneous_output_kbps"
  | "rejected_connections"
  | "expired_keys"
  | "evicted_keys"
  | "keyspace_hits"
  | "keyspace_misses"
  | "pubsub_channels"
  | "pubsub_patterns"
  | "connected_slaves"
  | "used_cpu_sys"
  | "used_cpu_user"
  | "rdb_changes_since_last_save"
  | "rdb_last_save_time"
  | "keys"
  | "expires"

export type RedisStats = {
  server: {
    counters: Partial<Record<RedisCounter, number>>
    flavor: string
    version: string
    role: "primary" | "replica" | "sentinel"
    mode: "standalone" | "cluster" | "sentinel"
    /** The dashboard's clock when INFO answered. */
    sampledAtMs: number
  }
}

export type RedisCommandStat = {
  command: string
  calls: number
  usec: number
  usecPerCall: number
  rejected?: number
  failed?: number
  p50Us?: number
  p99Us?: number
  p999Us?: number
}

export type RedisCommandStats = {
  commands: RedisCommandStat[]
  totalCalls: number
  totalUsec: number
  sampledAtMs: number
}

export type RedisLatency = {
  supported: boolean
  enabled: boolean
  thresholdMs?: number
  events: { event: string; at: string; latestMs: number; maxMs: number }[]
  reason?: string
}

export type RedisClientInfo = {
  id: number
  addr: string
  laddr?: string
  name: string
  user?: string
  ageSeconds: number
  idleSeconds: number
  db: number
  /** The last command, name only. */
  command: string
  /** The server's letters: N normal, S replica, M primary, P pubsub, x in MULTI, b blocked, O monitor. */
  flags: string
  subscriptions: number
  inTransaction: boolean
  outputMemory: number
  totalMemory: number
  library?: string
  /** The connection this very request used. */
  self: boolean
}

export type RedisSlowlog = {
  supported: boolean
  reason?: string
  /** <0 off, 0 logs everything. */
  thresholdUs?: number
  maxLen?: number
  length: number
  entries: {
    id: number
    at: string
    durationUs: number
    command: string
    args: RedisBytes[]
    client?: string
    clientName?: string
  }[]
}

export type RedisAnalysisGroup = {
  name: RedisBytes
  keys: number
  memory: number
  estimatedKeys: number
  estimatedMemory: number
}

export type RedisAnalysis = {
  db: number
  delimiter: string
  sampledAtMs: number
  elapsedMs: number
  total: number
  sampled: number
  /** The sample was the whole database: nothing is an estimate. */
  complete: boolean
  scale: number
  timedOut: boolean
  memory: number
  estimatedMemory: number
  usedMemory: number
  types: RedisAnalysisGroup[]
  namespaces: RedisAnalysisGroup[]
  namespacesOmitted?: number
  topKeys: {
    key: RedisBytes
    type: string
    memory: number
    ttl: number
    size: number
    encoding?: string
  }[]
  /** Always five, in this order: none, hour, day, week, later. */
  expiry: RedisAnalysisGroup[]
  encodings: RedisAnalysisGroup[]
  unavailable?: Partial<Record<"memory" | "encodings", string>>
}

export type RedisPubSub = {
  channels: { name: RedisBytes; subscribers: number }[]
  channelsOmitted?: number
  patterns: number
}

export type RedisMonitorEvent = {
  /** The server's clock, in seconds. */
  at: number
  db: number
  client: string
  command: string
  args: string[]
}

export type RedisFeedEnd = {
  reason: "duration" | "limit" | "error"
  count: number
  dropped: number
  error?: string
}

export type RedisMessage = {
  channel: RedisBytes
  pattern?: string
  payload: RedisBytes
  bytes: number
  truncated?: true
}
