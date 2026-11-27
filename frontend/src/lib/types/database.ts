/*
 * Databases: drivers and what each can do, connections and their readings,
 * access, poolers, TLS and resources, the fleet, query and server logs, and
 * the map of what talks to what.
 */

import type { LogSource } from "./logs"

export type DbDriver =
  "postgres" | "mysql" | "sqlite" | "sqlserver" | "clickhouse" | "oracle" | "mongodb" | "redis"

/**
 * The product behind a driver: MariaDB answers the `mysql` driver, Valkey the
 * `redis` one. A driver's own product carries the driver's id.
 */
export type DbFlavor =
  | DbDriver
  | "timescaledb"
  | "cockroachdb"
  | "yugabytedb"
  | "mariadb"
  | "percona"
  | "tidb"
  | "valkey"
  | "keydb"
  | "dragonfly"
  | "ferretdb"
  | "azure-sql-edge"

/**
 * What a database can be watched for: its server not answering, its disk,
 * its connections against its limit, its replication, and the backup jobs
 * that dump it failing or missing their time (`dbx.AlertKinds`).
 */
export type DbAlertKind =
  "down" | "disk" | "connections" | "replication" | "backup_failed" | "backup_missed"

/**
 * The capability flags that are a yes or a no, as `GET /databases/drivers`
 * serves them for every driver and every flavour (`dbx.Capabilities`; the
 * server's tests pin who has which). A flag says the feature exists for the
 * product; a route may still answer `supported: false` for one server.
 */
export type DbCapabilityFlag =
  // Connections and discovery.
  | "server" // a process that listens, logs and can be started; false = a file
  | "provision" // the dashboard can start one from its own images
  | "majorUpgrade" // a server started from those images can be moved to a newer major release
  | "serverTLS" // a server it started can speak TLS with a certificate from its own authority
  | "inventoryConnect" // can be connected from the inventory by its key
  | "hostAccount" // POST /host/grant can make or reset an account on a host server
  | "fileBased" // no host, port or account to ask for
  | "openByDefault" // a host server of this kind is tried without a password by the sync
  | "dump"
  // The workbench.
  | "sql"
  | "console" // SQL editor, Redis console, MongoDB command runner
  | "changeSets" // POST /changes: staged edits in one transaction
  | "keylessEdits" // a table without a primary key is edited by its whole row
  | "updateDefault" // an existing row's column can be set back to its default
  | "script" // POST /script
  | "transactions" // a script can run in one transaction
  | "queryCancel" // POST /query/cancel stops a run the editor started
  | "dollarQuoting"
  | "regexFilter" // the `regex` filter operator
  | "rowEstimate" // browse answers an estimated row count
  | "cellRead" // GET /cell: one whole value the grid clipped
  | "explainJSON" // a plan as a tree
  | "explainAnalyze" // a plan measured by running the statement
  | "indexWhatIf" // POST /explain/index-candidates and /explain/what-if: an index tried on a plan with HypoPG
  | "missingIndexes" // GET /missing-indexes: SQL Server's own list of the indexes its plans wanted
  | "assistant" // POST /assistant: the editor's assistant drafts SQL from the schema
  | "assistantPolicy" // the assistant drafts row-level security policies
  | "mcp" // an MCP key can be given it, and an AI client's tools reach it
  // The schema.
  | "ddl" // the create-table form; gate the other forms on `ddlOperations`
  | "schemas" // more than one namespace to choose from
  | "catalog" // GET /catalog and /object
  | "views"
  | "materializedViews"
  | "routines"
  | "triggers"
  | "sequences"
  | "enums"
  | "comments"
  | "indexes"
  | "extensions"
  | "rowSecurity" // GET /rls, the policy forms and the role tester
  | "pooler" // a PgBouncer can be run in front of the server
  // Watching and maintaining a server.
  | "stats" // GET /stats: one snapshot of counters and gauges
  | "sessions" // sessions, Redis clients, MongoDB operations
  | "kill" // end a session or disconnect a client
  | "cancel" // stop what a session is running and keep the session
  | "locks"
  | "replication"
  | "tableStats"
  | "indexStats"
  | "maintenance" // the actions are `maintenanceActions`
  | "settings" // the server's parameters can be read
  | "settingsWrite" // and one can be changed
  | "roles"
  | "privileges" // role detail, grants, grant and revoke
  | "statements"
  | "statementsReset"
  | "statementHistory"
  | "statementsSetup" // POST /statements/preload: the library loaded at start, then a restart
  | "queryLog"
  | "clickhouseViews" // parts, merges, mutations, running queries
  | "sqliteFile"
  // Redis.
  | "keys"
  | "keyTree"
  | "keyTypeFilter"
  | "keyMeta"
  | "valueDownload"
  | "keyEncoding"
  | "bulkKeys"
  | "streams"
  | "logicalDatabases"
  | "consoleClassify"
  | "serverInfo"
  | "commandStats"
  | "latency"
  | "queryLogReset"
  | "persistence"
  | "aofRewrite"
  | "memoryAnalysis"
  | "aclRules"
  | "pubsub"
  | "pubsubLive"
  | "monitor"
  // MongoDB.
  | "documents"
  | "shellSyntax"
  | "collections"
  | "collectionOptions"
  | "aggregation"
  | "schemaAnalysis"
  | "indexUsage"
  | "indexHide"
  | "validation"
  | "profiler"
  // Code generation and moving data.
  | "orm" // the targets are `ormTargets`
  | "seed" // POST /{id}/seed/plan and /{id}/seed: mock rows written into existing tables
  | "export" // the formats are `exportFormats`
  | "exportColumns"
  | "exportQuery"
  | "import" // the formats are `importFormats`
  | "importMapping"
  | "importUpsert"
  | "importReplace"
  | "importCreateTable"
  | "dumpSchemaOnly"
  | "dumpDataOnly"
  | "dumpTables"
  | "dumpCompression"
  | "dumpDatabases" // Redis's choice of numbered databases
  | "dumpUpload"
  | "restoreNewDatabase"
  | "serverDatabaseCreate" // POST /{id}/server/databases makes a database on this server
  | "serverDatabaseConnect" // POST /{id}/server/databases/connect opens another one as a connection
  | "copy"
  | "copyStructureOnly"
  | "pitr" // GET /{id}/pitr: the write-ahead log archived with WAL-G, recovered to a moment

/** A branch of the schema catalogue (`GET /catalog`). */
export type DbCatalogGroup =
  | "tables"
  | "views"
  | "materializedViews"
  | "functions"
  | "procedures"
  | "triggers"
  | "sequences"
  | "types"
  | "events"
  | "packages"
  | "synonyms"
  | "dictionaries"

/** A structure change the engine's forms can make (`/ddl/*`). */
export type DbDdlOperation =
  | "createTable"
  | "dropTable"
  | "truncate"
  | "addColumn"
  | "dropColumn"
  | "renameColumn"
  | "renameTable"
  | "alterColumn"
  | "createIndex"
  | "dropIndex"
  | "foreignKeys"
  | "uniqueConstraints"
  | "checkConstraints"
  | "views"
  | "materializedViews"
  | "schemas"
  | "tableComments"
  | "columnComments"
  | "enumTypes"
  | "indexMethod"
  | "indexPartial"
  | "indexIfNotExists"
  | "indexConcurrently"
  | "functions"
  | "procedures"
  | "triggers"

export type DbExportFormat = "csv" | "tsv" | "json" | "ndjson" | "sql"
export type DbImportFormat = "csv" | "tsv" | "json" | "ndjson"

/**
 * What a product can do, as the server states it: the `capabilities` object
 * of a driver, of each of its flavours (`GET /databases/drivers`) and of one
 * connection (`GET /databases/{id}`, resolved for the flavour that answered).
 * Every key is always present. Nothing reads these directly: a page asks the
 * engine registry (`components/database/engine.ts`, `engine.can(…)`).
 *
 * Besides the yes-or-no flags there are lists, empty for an engine without the
 * feature, and flags that are a word where the feature comes in more than one
 * form and `false` where the engine has none of them.
 */
export type DbCapabilities = Record<DbCapabilityFlag, boolean> & {
  /** What a database of this engine can be watched for (`/databases/alerts`). */
  alertKinds: DbAlertKind[]
  catalogGroups: DbCatalogGroup[]
  ddlOperations: DbDdlOperation[]
  /** The ids `POST /maintenance` takes; `GET /maintenance` describes them. */
  maintenanceActions: string[]
  /** The ids `POST /orm` takes; `GET /databases/orm/targets` describes them. */
  ormTargets: string[]
  /** What `GET /export` writes. */
  exportFormats: DbExportFormat[]
  /** What `POST /import/upload` reads. */
  importFormats: DbImportFormat[]
  /** What identifies a row: `none` where the engine's key does not (ClickHouse). */
  rowIdentity: "primaryKey" | "none" | false
  /** Whether an applied change hands the row back: always, or only by its key. */
  returnsChangedRow: "always" | "byKey" | false
  /** How a read is kept from writing: the engine refuses, or the write is rolled back. */
  readOnlyScope: "enforced" | "rollback" | false
  /** A failed import leaves nothing behind; MongoDB's only when it replaces. */
  importAtomic: boolean | "replace"
  /** Redis JSON values: a module the server may have, or built in. */
  json: boolean | "module"
  /** An expiry on one field of a hash, as the release that brought it ("7.4+"). */
  hashFieldTtl: string | false
  /** What `GET /redis/commands` holds: the server's documentation, or names only. */
  commandReference: "docs" | "names" | false
  /**
   * The setting the engine keeps its own memory in (`shared_buffers`,
   * `innodb_buffer_pool_size`, `maxmemory`, …), read beside its container's
   * memory limit by `GET /{id}/resources`.
   */
  memoryBuffer: string | false
  /**
   * Bringing a database over from another server into a new one here:
   * `replication` keeps it current until a cutover (PostgreSQL's logical
   * replication), `stream` reads a dump from the source as it loads it.
   */
  onlineImport: "replication" | "stream" | false
}

/** Any key of the capabilities object: what `engine.can(…)` takes. */
export type DbCapabilityName = keyof DbCapabilities

/** One engine, as `GET /databases/drivers` describes it. */
export type DbDriverInfo = {
  id: DbDriver
  /** The driver's label covers every product it talks to ("MySQL / MariaDB"). */
  label: string
  kind: "sql" | "document" | "keyvalue"
  placeholder: string
  /** `capabilities.sql` and `capabilities.ddl`, kept for older readers. */
  sql: boolean
  ddl: boolean
  /** SQL engines only. */
  columnTypes?: string[]
  /** The operators a browse filter may use on this engine. SQL engines only. */
  filterOps?: string[]
  /** 0 for an engine that is a file. */
  defaultPort: number
  /** A connection string in the form this engine's driver takes. */
  dsnExample: string
  /** The driver's own product. */
  capabilities: DbCapabilities
  /** Every product the driver talks to, its own first (`flavors[0].id === id`). */
  flavors: { id: DbFlavor; label: string; capabilities: DbCapabilities }[]
}

/** A saved connection, as `GET /databases/` lists it. The DSN is never sent. */
export type DbConnection = {
  id: number
  name: string
  driver: DbDriver
  host: string
  port: string
  user: string
  database: string
  createdAt: string
  /** The operator's label for what the database is for: production, staging, … */
  environment: string
  /** Protected: the dashboard refuses every change to its data or schema. */
  readOnly: boolean
  notes: string
  /**
   * The inventory key of the server or file on this machine it was made from
   * (`docker:shop-db`, `host:postgres:5432`), `""` for one typed in by hand.
   */
  origin: string
  /** The saved row can no longer be opened; only edit and forget work on it. */
  broken?: boolean
  brokenReason?: string
  /**
   * What only the server behind it can say, laid in from the connection's
   * summary once that has answered: what actually answered, its own
   * description of itself ("PostgreSQL 16.4"), and what that product can do.
   * The list itself does not carry them.
   */
  flavor?: DbFlavor
  version?: string
  capabilities?: DbCapabilities
}

/** One entry in a connection's recent-statement history. */
export type DbHistoryEntry = {
  id: number
  sql: string
  risk: string
  success: boolean
  durationMs: number
  rowCount: number
  ranAt: string
}

/** What one table costs on disk. Row counts are the engine's estimate. */
export type DbTableSize = {
  schema: string
  table: string
  rows: number
  bytes: number
  dataBytes: number
  indexBytes: number
}

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

export type DbOverview = {
  schema: string
  tables: DbTableSize[]
  totalBytes: number
  totalRows: number
  tableCount: number
  /** False where the engine cannot report bytes — show rows and say so. */
  sizesKnown: boolean
  pool: DbPoolStats
}

/** How the recorded database activity is kept and where it is scraped from. */
export type DbMetricsExport = {
  retentionDays: number
  options: { days: number; estimatedBytes: number | null }[]
  /** Servers the recorder has saved a reading of in the last day. */
  connections: number
  samples: number
  storedBytes: number | null
  everySeconds: number
  /** Days kept whole; older samples are thinned. */
  rawDays: number
  coarseMinutes: number
  /** Path of the scrape endpoint on this dashboard. */
  endpoint: string
}

// --- detected and provisioned database servers ----------------------------

/**
 * Where a connection's server can be reached from, as GET /databases/{id}/access
 * reads it off the container's port binding and the host's firewall.
 *
 * `detected` is the fact the Remove row keys off: a server the sync would
 * connect again on the next page load is not worth a button that forgets it.
 * `managed` is whether the binding can be changed from here — a container on
 * this host that is not compose-owned and publishes the engine's port.
 */
export type DbAccess = {
  detected: boolean
  container?: string
  composeProject?: string
  managed: boolean
  /**
   * Read off the port binding: this machine only, one of its addresses, every
   * interface — and, every interface with the gateway keeping the port to a
   * list, `allowlist`.
   */
  exposure: "local" | "public" | "private" | "allowlist" | "remote"
  port?: number
  publicAddresses: string[]
  firewall: {
    backend?: string
    active: boolean
    open: boolean
    /** The sources the firewall's other allow rules for the port name. */
    allowed: string[]
    editable: boolean
  }
  /** The one host address a private binding publishes on. */
  address?: string
  /**
   * What the gateway keeps the port to. Set while the binding is not every
   * interface only when a change could not finish taking it away.
   */
  allow: string[]
  gateway: DbGateway
  /** This machine's addresses the port can be published on alone. */
  addresses: DbHostAddress[]
  /** The address this request came from, which the list's form offers. */
  client?: string
}

/** Whether the gateway table can keep a port to a list, and what it lets in besides. */
export type DbGateway = {
  available: boolean
  reason?: string
  /** The networks every drop in the gateway table lets through first. */
  alsoAdmitted: string[]
  /** The kernel holds the port's rules now. */
  enforced: boolean
  /** Connection attempts the rules dropped since the table was loaded. */
  refused: number
}

/** One address of this machine a port can be published on alone. */
export type DbHostAddress = {
  address: string
  interface?: string
  /** Who the address faces: tailnet, vpn, private or uplink. */
  network: "tailnet" | "vpn" | "private" | "uplink"
}

/** What PUT /databases/{id}/access takes. */
export type DbAccessRequest =
  | { exposure: "local" | "public" }
  | { exposure: "private"; address: string }
  | { exposure: "allowlist"; allow: string[] }

export type DbPoolMode = "transaction" | "session"
export type DbPoolerAuth = "userlist" | "auth_query"

/** What an operator chooses about a pooler (`PUT /databases/{id}/pooler`). */
export type DbPoolerSettings = {
  poolMode: DbPoolMode
  poolSize: number
  maxClientConn: number
  authMode: DbPoolerAuth
}

/**
 * A connection's pooler, as GET /databases/{id}/pooler reads it. `available`
 * is whether one can be turned on, with `reason` when not; a connection that
 * has one is always available. `state` is the container's own word, or
 * `missing` when its container is gone. `inSync` is whether the files it holds
 * were made from the connection's current password.
 */
export type DbPooler = {
  available: boolean
  reason?: string
  enabled: boolean
  settings: DbPoolerSettings
  defaults: DbPoolerSettings
  limits: { poolSize: number; maxClientConn: number }
  state?: string
  container?: string
  image: string
  network?: string
  exposure?: "local" | "public" | "private"
  port?: number
  linkedHost: string
  linkedPort: number
  reference: string
  inSync: boolean
  createdAt?: string
}

/** What PUT /databases/{id}/pooler did, and the reading afterwards. */
export type DbPoolerChange = {
  pooler: DbPooler
  warnings: string[]
  /** opened, closed, already, none, inactive, read-only or failed. */
  firewall?: string
  firewallError?: string
}

/** One row of PgBouncer's SHOW POOLS. */
export type DbPoolerPool = {
  database: string
  user: string
  poolMode: string
  clActive: number
  clWaiting: number
  svActive: number
  svIdle: number
  svUsed: number
  svTested: number
  svLogin: number
  maxWaitSec: number
}

/** One row of SHOW STATS: totals since it started, averages over its last period; microseconds and bytes. */
export type DbPoolerStat = {
  database: string
  totalXactCount: number
  totalQueryCount: number
  totalReceived: number
  totalSent: number
  totalXactTime: number
  totalQueryTime: number
  totalWaitTime: number
  avgXactCount: number
  avgQueryCount: number
  avgRecv: number
  avgSent: number
  avgXactTime: number
  avgQueryTime: number
  avgWaitTime: number
}

export type DbPoolerStats = {
  version: string
  pools: DbPoolerPool[]
  stats: DbPoolerStat[]
  at: string
}

/**
 * GET /databases/{id}/tls: whether a server the dashboard started speaks TLS
 * with a certificate from the dashboard's own authority, and what it needs
 * next. `supported: false` carries `reason`, in a sentence.
 */
export type DbTLS = {
  supported: boolean
  reason?: string
  enabled: boolean
  /** A connection without TLS is refused. Always true where the port speaks one protocol. */
  require: boolean
  /** The engine can accept TLS and plaintext on one port; Redis cannot. */
  canOptional: boolean
  /** This release can refuse plaintext. */
  canRequire: boolean
  /** The dashboard's own saved connection checks the certificate against the authority. */
  verified: boolean
  container?: string
  port?: number
  exposure?: DbAccess["exposure"]
  names: string[]
  serial?: string
  issuedAt?: string
  notAfter?: string
  /** When the renewal takes it up. */
  renewAt?: string
  renewedAt?: string
  /**
   * What a linked application is given: the string it had before TLS ("plain",
   * while TLS is only allowed), TLS without checking the certificate
   * ("unverified", once required), or TLS that checks and so needs the
   * authority's certificate ("needsCA", Redis).
   */
  applications?: "plain" | "unverified" | "needsCA"
  /** Deployment environments that reach this database by its internal name. */
  linked: number
  authority: {
    subject: string
    fingerprint: string
    notAfter: string
    createdAt: string
  } | null
  client?: {
    host: string
    port: number
    /** Absent for an engine whose client has no URL form. The password is a placeholder. */
    url?: string
    command: string
    caFile: string
  }
}

/** What PUT /databases/{id}/access did, and the reading afterwards. */
export type DbAccessChange = {
  access: DbAccess
  /** opened, closed, updated, already, none, inactive, read-only or failed. */
  firewall: string
  firewallError?: string
  /** kept, released, restored, unchanged or failed. */
  gateway: string
  gatewayError?: string
  /**
   * What the switch did to the connection's pooler, when it has one. `error`
   * is why it could not be moved; `removed` that it was taken down instead of
   * being left published (a move to this server that failed).
   */
  pooler?: {
    container: string
    port?: number
    exposure?: "local" | "public" | "private"
    recreated?: boolean
    firewall?: string
    firewallError?: string
    error?: string
    removed?: boolean
    closeError?: string
  }
}

/** One end of a change to where a server is reachable from. */
export type DbReach = {
  exposure: DbAccess["exposure"]
  hostIp: string
  allow?: string[]
}

/** What POST /databases/{id}/access/preview says a change would do, in order. */
export type DbAccessPreview = {
  from: DbReach
  to: DbReach
  /** The container is replaced, which drops every session. */
  recreate: boolean
  steps: { kind: "gateway" | "recreate" | "firewall"; description: string; lines?: string[] }[]
  firewall: string
  firewallError?: string
  /**
   * What the switch did to the connection's pooler, when it has one. `error`
   * is why it could not be moved; `removed` that it was taken down instead of
   * being left published (a move to this server that failed).
   */
  pooler?: {
    container: string
    port?: number
    exposure?: "local" | "public" | "private"
    recreated?: boolean
    firewall?: string
    firewallError?: string
    error?: string
    removed?: boolean
    closeError?: string
  }
}

/** What a database's container may use, as GET /databases/{id}/resources reads it. */
export type DbResources = {
  container?: string
  /** The route can change the limits; `refusal` says why it cannot. */
  editable: boolean
  refusal?: string
  running: boolean
  /** Bytes and cores, zero for no limit. */
  limits: { memoryBytes: number; cpus: number }
  /** What it uses now, read while it runs. */
  usedBytes?: number
  /** The kernel stopped it, the last time it stopped, for going past its memory limit. */
  oomKilled?: boolean
  /** This machine as Docker sees it; zero where Docker did not say. */
  host: { memoryBytes: number; cpus: number }
  /** The memory the engine keeps for itself, by its own setting. */
  buffer?: { setting: string; bytes: number }
  /** Why the buffer could not be read from a server that runs. */
  bufferError?: string
}

/** What PUT /databases/{id}/resources did. */
export type DbResourcesChange = {
  limits: { memoryBytes: number; cpus: number }
  /** A limit was lifted, which replaced the container. */
  replaced: boolean
  warnings: string[]
}

/**
 * What POST /databases/sync did.
 *
 * `unreachable` is the half that has to be shown: a database this host is
 * running that was recognised and could not be connected to — almost always a
 * container on a compose network with no published port. Dropping it silently
 * is what made the reconcile look like it did nothing.
 */
export type DbUnreachableServer = {
  container: string
  driver: string
  reason: string
}

/**
 * A database installed on the machine rather than in a container.
 *
 * Everything about it is known except the password: a container states its
 * credentials in its environment, and an apt-installed server keeps them in
 * its own catalogue, where no amount of reading the machine finds them. So the
 * page asks for that one thing, and the server dials before it saves anything.
 */
export type DbCredentialServer = {
  driver: string
  host: string
  port: number
  process?: string
  name: string
  /** The engine's own conventional account and database, to open the form with. */
  user?: string
  database?: string
}

export type DbProvisionOption = {
  engine: string
  label: string
  image: string
  driver: string
  versions: { version: string; build?: { ready: boolean } }[]
  defaultVersion: string
}

// --- schema graph (the diagram) -------------------------------------------

// --- the fleet, the server behind a connection, and the map ----------------

/**
 * A release line's last day of maintenance, as the server's end-of-life table
 * dates it (`dbx/eol.go`). Absent wherever the table does not know the
 * release: nothing is said rather than a date guessed.
 */
export type DbEndOfLife = {
  /** The engine as its vendor names it: PostgreSQL, MariaDB, … */
  product: string
  /** The release line the date belongs to: "18", "8.0", "26.3". */
  release: string
  /** Midnight UTC of the last day: "2030-11-14T00:00:00Z". */
  date: string
  /** The day has gone by. */
  past: boolean
  /** Negative once it has. */
  daysLeft: number
}

/**
 * One connection as the control center's fleet lists it: the row's facts
 * with the readings the server answered — dialled, sized and counted in one
 * request (`GET /databases/fleet`).
 */
export type DbFleetEntry = DbConnection & {
  ok: boolean
  error?: string
  version?: string
  /** The bare number: "16.4". The fork's own, not the one it imitates. */
  versionNumber?: string
  /**
   * The product that answered: its driver's own until the server has answered
   * once, and what it last said it was while it is stopped or unreachable.
   */
  flavor: DbFlavor
  /**
   * What the server is doing. A stopped or paused one is not dialled, so it
   * is `ok: false` with no error and this is what says it did not fail.
   */
  state: "running" | "stopped" | "paused" | "unreachable" | "broken"
  latencyMs: number
  bytes: number
  sizesKnown: boolean
  objects: number
  objectWord: "tables" | "collections" | "keys"
  sessions: number
  /** `unknown` only on a row that cannot be opened. */
  source: "docker" | "host" | "remote" | "file" | "unknown"
  container?: string
  composeProject?: string
  /** The systemd unit of a server installed on the machine. */
  unit?: string
  exposure: DbAccess["exposure"] | "unknown"
  consumers: number
  /**
   * When it was last backed up from here: its newest dump, or the newest
   * backup job run that dumped it, whichever is later. Absent for never.
   */
  lastBackup?: string
  /** A MongoDB 8 server that cannot start on this kernel as it is. Absent otherwise. */
  kernelProblem?: DbKernelProblem
}

/**
 * MongoDB 8 on Linux 6.19 to 7.0.13, which it refuses to start on without
 * `GLIBC_TUNABLES=glibc.pthread.rseq=1` (MongoDB SERVER-121912).
 */
export type DbKernelProblem = {
  /** The alert, in a few words. */
  message: string
  /** What applying it does, or where the compose file has to say it. */
  detail: string
  kernel: string
  release: string
  /** `POST /databases/{id}/kernel-mitigation` applies it. */
  fixable: boolean
}

export type DbFleet = {
  connections: DbFleetEntry[]
  unreachable: DbUnreachableServer[]
  needsCredentials: DbCredentialServer[]
  checkedAt: string
}

/**
 * The logs a database's server writes, as GET /databases/{id}/logs/sources
 * follows the connection to them: its container, or the file its process
 * holds open, the file its package writes and its unit's journal. `primary`
 * is where the server writes its own lines — the file even while logrotate
 * has left it empty.
 */
export type DbLogSource = LogSource & { primary?: boolean }

export type DbLogSources = {
  sources: DbLogSource[]
  /** Logs the server writes that the log roots do not reach. */
  refused?: { path: string; reason: string }[]
  /** Why there is no source, when there is none. */
  reason?: string
  /** What the sources are when nothing answers for the connection: a stopped server's log. */
  note?: string
}

/** One statement the server itself recorded as slow — or, for ClickHouse, as run. */
export type DbQueryEntry = {
  at: string
  durationMs: number
  query: string
  fp?: string
  user?: string
  db?: string
  client?: string
  rows?: number
  examined?: number
  error?: string
  code?: string
}

/** GET /databases/{id}/querylog: the statements, where they were read, and what keeps them empty. */
export type DbQueryLog = {
  supported: boolean
  reason?: string
  source: "log" | "slowlog" | "query_log" | "slow_log" | "statements_history" | ""
  /**
   * The setting that keeps the log empty, the statement that changes it, and
   * the same change as parameters for Turn on to set through
   * `PUT /databases/{id}/settings`, in order.
   */
  enable?: {
    setting: string
    current: string
    sql: string
    changes?: { name: string; value: string }[]
    /** Why the connecting account cannot make the change; Turn on is then not offered. */
    blocked?: string
  }
  /** How slow a statement has to be to be recorded, in the engine's words. */
  threshold?: string
  entries: DbQueryEntry[]
  truncated: boolean
}

/** A node on the map of what talks to what. */
export type DbTopoNode = {
  id: string
  kind: "database" | "deployment" | "container" | "host" | "remote"
  name: string
  product?: string
  detail?: string
  status?: string
  href?: string
  connId?: number
  /** The project a deployment node is an environment of. */
  projectId?: number
}

export type DbTopoEdge = {
  from: string
  to: string
  via: ("binding" | "env" | "stack" | "network" | "session")[]
  sessions: number
  status: "connected" | "stale" | "broken" | "pending" | "observed" | string
}

export type DbTopology = {
  nodes: DbTopoNode[]
  edges: DbTopoEdge[]
  checkedAt: string
}
