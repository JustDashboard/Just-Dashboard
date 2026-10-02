/**
 * What the MongoDB routes answer with, as the server's contract states them.
 *
 * Every field that holds a document, a list of documents or an `_id` travels
 * as text — Extended JSON, or the shell's own spelling — never as JSON the
 * browser has parsed: a 64-bit integer, a Decimal128 and the order of a
 * document's fields do not survive `JSON.parse`.
 */

/** One document. `canonical` is what an editor loads and sends back; `relaxed` is for drawing only. */
export type MongoDoc = {
  /** Canonical Extended JSON of the `_id` alone. `""` when a projection removed it. */
  id: string
  /** The whole document as canonical Extended JSON text, in its stored field order. */
  canonical: string
  /** The same document, parsed, for display. Never sent back. */
  relaxed: Record<string, unknown>
  /** Size in BSON bytes. */
  size: number
  /** SHA-256 of the document's BSON as returned: the guard a replace sends back. */
  digest: string
}

/** The query bar. Every string is Extended JSON or shell syntax; `""` is none. */
export type MongoFindSpec = {
  filter?: string
  projection?: string
  sort?: string
  collation?: string
  hint?: string
  skip?: number
  limit?: number
  maxTimeMS?: number
}

export type MongoCount = {
  value: number
  /** `false`: `value` is the collection's stored estimate. */
  exact: boolean
  /** `"collection"`: the whole collection's size, an upper bound on the matches. */
  scope: "filter" | "collection"
}

export type MongoFindResult = {
  documents: MongoDoc[]
  returned: number
  skip: number
  limit: number
  hasMore: boolean
  /** The page ended early at 8 MiB of BSON rather than at `limit`. */
  truncated: boolean
  count: MongoCount | null
  durationMs: number
  statement: string
}

export type MongoWriteResult = {
  dryRun: boolean
  matched: number
  modified: number
  deleted: number
  upsertedId?: string
}

export type MongoWriteError = { index: number; code: number; message: string }
export type MongoInsertResult = {
  inserted: number
  insertedIds: string[]
  errors: MongoWriteError[]
}

export type MongoTimeSeries = {
  timeField: string
  metaField?: string
  granularity?: "seconds" | "minutes" | "hours"
  bucketMaxSpanSeconds?: number
  bucketRoundingSeconds?: number
}

export type MongoValidationLevel = "off" | "strict" | "moderate"
export type MongoValidationAction = "error" | "warn" | "errorAndLog"

export type MongoCollection = {
  name: string
  type: "collection" | "view" | "timeseries"
  system: boolean
  readOnly: boolean
  /** `false` for a view, and past the first 400 collections of a database. */
  statsKnown: boolean
  count: number
  size: number
  avgObjSize: number
  storageSize: number
  indexCount: number
  indexSize: number
  capped: boolean
  cappedSize?: number
  cappedMax?: number
  clustered: boolean
  viewOn?: string
  pipeline?: string
  timeseries?: MongoTimeSeries
  expireAfterSeconds?: number
  validator?: string
  validationLevel?: MongoValidationLevel
  validationAction?: MongoValidationAction
  collation?: string
}

export type MongoCollections = {
  database: string
  collections: MongoCollection[]
  statsTruncated: boolean
}

export type MongoCreateCollection = {
  database?: string
  collection: string
  capped?: boolean
  size?: number
  max?: number
  timeseries?: MongoTimeSeries
  expireAfterSeconds?: number
  clusteredIndex?: boolean
  validator?: string
  validationLevel?: MongoValidationLevel
  validationAction?: MongoValidationAction
  viewOn?: string
  pipeline?: string
  collation?: string
}

export type MongoDatabase = {
  name: string
  collections: number
  views: number
  objects: number
  avgObjSize: number
  dataSize: number
  storageSize: number
  indexes: number
  indexSize: number
  sizeOnDisk: number
  empty: boolean
  /** `false`: this account may list the database but not measure it. */
  statsKnown: boolean
}

export type MongoIndexKeyType = "asc" | "desc" | "text" | "hashed" | "2dsphere" | "2d"
export type MongoIndexKey = { field: string; type: string }

export type MongoIndex = {
  name: string
  keys: MongoIndexKey[]
  key: string
  kind: "regular" | "text" | "geospatial" | "hashed" | "wildcard"
  /** The `_id` index: cannot be dropped, hidden or changed. */
  primary: boolean
  unique: boolean
  sparse: boolean
  hidden: boolean
  expireAfterSeconds: number | null
  partialFilterExpression?: string
  collation?: string
  weights?: string
  wildcardProjection?: string
  defaultLanguage?: string
  /** Bytes, `-1` unknown. */
  size: number
  /** `null`: the server would not say. */
  usage: { ops: number; since: string } | null
  building: boolean
}

export type MongoIndexes = { indexes: MongoIndex[]; usageNote?: string }

export type MongoCreateIndex = {
  database?: string
  collection: string
  keys: { field: string; type: MongoIndexKeyType }[]
  name?: string
  unique?: boolean
  sparse?: boolean
  hidden?: boolean
  expireAfterSeconds?: number
  partialFilterExpression?: string
  collation?: string
  weights?: string
  defaultLanguage?: string
  wildcardProjection?: string
}

export type MongoValidation = {
  /** Canonical Extended JSON, `""` when there is none: what the editor loads. */
  validator: string
  validatorRelaxed: string
  level: MongoValidationLevel
  action: MongoValidationAction
}

export type MongoValidationCheck = {
  proposed: boolean
  failing: number
  /** `false`: counting ran out of time; `failing` is a lower bound. */
  exact: boolean
  /** Estimated collection size, `-1` unknown. */
  total: number
  samples: MongoDoc[]
  durationMs: number
}

export type MongoPipelineInfo = {
  stages: string[]
  writes: boolean
  writeStage?: "$out" | "$merge"
  unknown: string[]
}

export type MongoAggregateResult = {
  writes: boolean
  pipeline: MongoPipelineInfo
  documents: MongoDoc[]
  returned: number
  hasMore: boolean
  truncated: boolean
  durationMs: number
  statement: string
}

export type MongoPreview = {
  documents: MongoDoc[]
  returned: number
  stage: number
  stages: number
  /** A `$limit` was put in front of the first grouping stage. */
  inputLimited: boolean
  inputLimit: number
  /** The stage asked for writes: it was not run, and the documents are its input. */
  writeStage?: "$out" | "$merge"
  durationMs: number
}

export type MongoPlanNode = {
  stage: string
  index?: string
  keyPattern?: string
  direction?: string
  indexBounds?: string
  filter?: string
  /** The four figures are `null` unless the plan was executed. */
  returned: number | null
  docsExamined: number | null
  keysExamined: number | null
  timeMs: number | null
  /** The root is the last thing done; the leaves are the scans. */
  children: MongoPlanNode[]
}

export type MongoExplainVerbosity = "queryPlanner" | "executionStats" | "allPlansExecution"

export type MongoExplain = {
  summary: {
    namespace: string
    verbosity: string
    executed: boolean
    engine: "classic" | "sbe"
    returned: number | null
    keysExamined: number | null
    docsExamined: number | null
    timeMs: number | null
    indexesUsed: string[]
    collectionScan: boolean
    inMemorySort: boolean
    usedDisk: boolean
    rejectedPlans: number
  }
  plan: MongoPlanNode
  raw: string
  note?: string
}

export type MongoSchemaType = {
  type: string
  count: number
  share: number
  min?: string
  max?: string
  avg?: number
  minLength?: number
  maxLength?: number
  avgLength?: number
  top?: { value: string; count: number }[]
  distinct?: number
  distinctCapped?: boolean
}

export type MongoSchemaField = {
  /** Dotted; an array's elements are `tags[]`, documents inside continue `items[].sku`. */
  path: string
  name: string
  depth: number
  documents: number
  presence: number
  occurrences: number
  types: MongoSchemaType[]
  indexed: boolean
  indexes: string[]
}

export type MongoSchema = {
  database: string
  collection: string
  sampled: number
  requested: number
  /** Estimated collection size, `-1` unknown. */
  total: number
  truncated: boolean
  fields: MongoSchemaField[]
  durationMs: number
}

export type MongoServer = {
  /** The server's clock, ms since 1970. */
  timestamp: number
  host: string
  version: string
  process: string
  uptime: number
  storageEngine: string
  topology: "standalone" | "replicaset" | "sharded"
  role: "standalone" | "primary" | "secondary" | "arbiter" | "mongos" | "other"
  setName?: string
  primary?: string
  opcounters: Record<string, number>
  connections: { current: number; available: number; active: number; totalCreated: number }
  network: { bytesIn: number; bytesOut: number; numRequests: number }
  /** MiB. */
  memory: { resident: number; virtual: number }
  cache: {
    bytes: number
    maxBytes: number
    dirtyBytes: number
    pagesRead: number
    pagesWritten: number
  } | null
  documents: { inserted: number; returned: number; updated: number; deleted: number }
  scanned: { keys: number; documents: number }
  queue: {
    activeReaders: number
    activeWriters: number
    queuedReaders: number
    queuedWriters: number
  }
  latency: {
    readsMicros: number
    readsOps: number
    writesMicros: number
    writesOps: number
    commandsMicros: number
    commandsOps: number
  }
  cursors: { open: number; timedOut: number }
  asserts: Record<string, number>
}

export type MongoOperation = {
  /** A number on a mongod, `shard:number` through a mongos: passed back verbatim. */
  opId: string
  op: string
  ns: string
  command: string
  commandTruncated: boolean
  secondsRunning: number
  client: string
  appName: string
  user: string
  desc: string
  active: boolean
  waitingForLock: boolean
  planSummary: string
  message: string
  killPending: boolean
  connectionId: number
  numYields: number
}

export type MongoProfilerEntry = {
  time: string
  op: string
  ns: string
  millis: number
  command: string
  commandTruncated: boolean
  planSummary: string
  docsExamined: number
  keysExamined: number
  returned: number
  modified: number
  deleted: number
  inserted: number
  responseLength: number
  numYields: number
  hasSortStage: boolean
  usedDisk: boolean
  client: string
  user: string
  appName: string
  error?: string
}

export type MongoProfilerLevel = 0 | 1 | 2

export type MongoProfilerState = {
  database: string
  level: MongoProfilerLevel
  slowMs: number
  sampleRate: number
  filter?: string
}

export type MongoProfiler = MongoProfilerState & { entries: MongoProfilerEntry[] }

export type MongoMember = {
  id: number
  name: string
  state: string
  health: boolean
  self: boolean
  uptime: number
  optimeDate?: string
  lagSeconds: number | null
  pingMs: number
  syncSource?: string
  lastHeartbeat?: string
  message?: string
}

export type MongoReplication = {
  /** `false` on a standalone server or through a mongos: nothing else is set. */
  replicaSet: boolean
  reason?: string
  setName?: string
  myState?: string
  members: MongoMember[]
  oplog: {
    sizeBytes: number
    usedBytes: number
    first?: string
    last?: string
    windowSeconds: number
  } | null
}

export type MongoCommandClass = "read" | "write" | "destructive" | "blocked"

export type MongoVerdict = {
  /** The command's first key. */
  command: string
  class: MongoCommandClass
  /** Needs `system.admin` on top. */
  admin: boolean
  /** `false`: not in the table, treated as destructive. */
  known: boolean
  reason?: string
  target?: string
}

export type MongoCapability = "service.control" | "system.admin" | "destructive"

export type MongoClassification = {
  verdict: MongoVerdict
  requires: MongoCapability[]
  allowed: boolean
  database: string
}

export type MongoCommandResult = {
  verdict: MongoVerdict
  database: string
  reply: {
    canonical: string
    /** `null` when the reply is over 4 MiB. */
    relaxed: Record<string, unknown> | null
    size: number
    /** The command opened a cursor with more than its first batch; it was closed. */
    more: boolean
    durationMs: number
  }
}

/** `POST /{id}/import/upload`: what the file's rows became. */
export type MongoImportReport = {
  dryRun: boolean
  schema: string
  table: string
  format: "csv" | "tsv" | "json" | "ndjson"
  mode: "insert" | "upsert" | "replace"
  columns: { source: string; index: number; inferred: string; examples: string[] }[]
  rowsRead: number
  inserted: number
  skipped: number
  errors: { row: number; line: number; message: string }[]
  errorsTruncated: boolean
  preview?: { columns: string[]; rows: (string | null)[][] }
  atomic: boolean
  warnings: string[]
}
