import { expect as baseExpect, test, type Page } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A SQL server's Performance page, its Advisor, and its Logs.
 *
 * The claims here are the ones those pages make to their reader. A session
 * is read by its state, so an idle one is never the longest-running
 * statement; a blocked one names the session it is behind, whole, whatever
 * its id looks like; a view the engine lacks is not offered and nothing is
 * asked of it; a read that fails says so and can be tried again, and one
 * that fails over what is already shown leaves it there; a control is drawn
 * only for the role that may use it, and nothing that changes data is drawn
 * on a protected connection; a finding's fix is applied from the page only
 * where the server marks it safe, and behind a question that names what it
 * is about and says what will run.
 *
 * And the ones a review of them added: the readings are counts of the session
 * list, so a tile and the chip under it cannot disagree; nothing under the
 * readings moves once it is drawn; a table is exactly as wide as the view it
 * is in, at a laptop's width too; a read that does not come back says what it
 * is waiting for; and the keyboard goes back to where it was when a panel or
 * a confirmation closes.
 */

// These pages make several reads on arrival, and on a machine that is busy
// with other work the last of them can land after an assertion's five seconds.
const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const NOW = "2026-10-01T09:00:00Z"
const at = (step: number) => new Date(Date.parse(NOW) + step * 5_000).toISOString()

type Reply = { status: number; body: unknown }
const failing = (message: string, status = 502): Reply => ({
  status,
  body: { error: { code: status === 502 ? "query_failed" : "bad_request", message } },
})
const isReply = (value: unknown): value is Reply =>
  typeof value === "object" && value !== null && "status" in value && "body" in value

/** What a route answers: a value, or one made from how many times it has been asked. */
type Answer = unknown | ((call: number, url: URL) => unknown)

// ---- what the server answers ----------------------------------------------

const stats = (call: number) => ({
  supported: true,
  at: at(call),
  driver: "postgres",
  version: "PostgreSQL 16.4",
  uptimeSeconds: 46_000,
  role: "standalone",
  database: "shop_main",
  databaseBytes: 24_517_655,
  connections: { total: 12, active: 3, idle: 6, idleInTransaction: 1, waiting: 2, max: 100 },
  counters: {
    transactionsCommitted: 1_000 + call * 50,
    transactionsRolledBack: 0,
    blocksHit: 900_000 + call * 990,
    blocksRead: 2_700 + call * 10,
    rowsRead: 400_000 + call * 5_000,
    rowsWritten: 90_000 + call * 50,
    deadlocks: 2,
  },
  gauges: { longestQuerySeconds: 48, oldestTransactionSeconds: 300, replicas: 0 },
  pool: {
    open: 1,
    inUse: 0,
    idle: 1,
    waitCount: 0,
    waitDuration: "0s",
    maxOpen: 5,
    maxIdleClosed: 0,
    maxLifetimeClosed: 0,
  },
})

const session = (pid: string, status: string, extra: Record<string, unknown> = {}) => ({
  pid,
  user: "app",
  database: "shop_main",
  state: status.replaceAll("_", " "),
  status,
  seconds: 0,
  ...extra,
})

/**
 * A pooled server: one connection idle for a day, one holding a transaction
 * open, a session waiting on it, a statement running, and the dashboard's own
 * read. The blocker's id has a comma in it, as Oracle's do.
 */
const ACTIVITY = {
  supported: true,
  sessions: [
    session("900", "idle", { idleSeconds: 86_400, application: "pool", query: "COMMIT" }),
    session("12,3301", "idle_in_transaction", {
      user: "checkout",
      application: "checkout-api",
      idleSeconds: 95,
      transactionSeconds: 95,
      query: "UPDATE orders SET note = $1 WHERE id = $2",
    }),
    session("40,77", "blocked", {
      user: "billing",
      application: "billing-worker",
      seconds: 48,
      transactionSeconds: 48,
      wait: "Lock:transactionid",
      blockedBy: "12,3301",
      blockedByPids: ["12,3301"],
      query: "UPDATE orders SET status = 'paid' WHERE id = 1",
    }),
    session("52", "active", {
      user: "reports",
      application: "reports",
      seconds: 12,
      query: "SELECT count(*) FROM order_items",
    }),
    session("60", "active", { seconds: 0.01, self: true, query: "SELECT a.pid FROM activity a" }),
  ],
}

const wait = (waitingPid: string, blockingPid: string, extra: Record<string, unknown> = {}) => ({
  waitingPid,
  blockingPid,
  waitingUser: "app",
  blockingUser: "app",
  waitSeconds: 20,
  lockType: "relation",
  mode: "AccessShareLock",
  object: "public.orders",
  waitingQuery: `SELECT * FROM orders -- ${waitingPid}`,
  blockingQuery: `UPDATE orders SET note = 'x' -- ${blockingPid}`,
  ...extra,
})

/** 201 waits on 101; 301 waits on 201 and — as the server reports a queue — on 101 too. */
const LOCKS = {
  supported: true,
  truncated: false,
  waits: [
    wait("201", "101", {
      blockingState: "idle in transaction",
      blockingSeconds: 300,
      blockingUser: "checkout",
      mode: "ShareLock",
      lockType: "transactionid",
      object: "transaction 108448",
    }),
    wait("301", "201", { blockingState: "active", mode: "AccessExclusiveLock" }),
    wait("301", "101", { blockingState: "idle in transaction", mode: "AccessExclusiveLock" }),
    wait("401", "301", { blockingState: "active" }),
  ],
  locks: [
    {
      pid: "101",
      user: "checkout",
      lockType: "relation",
      mode: "RowExclusiveLock",
      granted: true,
      object: "public.orders",
    },
    {
      pid: "201",
      user: "app",
      lockType: "transactionid",
      mode: "ShareLock",
      granted: false,
      object: "transaction 108448",
    },
  ],
}

const statement = (
  id: string,
  query: string,
  share: number,
  extra: Record<string, unknown> = {},
) => ({
  id,
  query,
  calls: 100,
  totalMs: share * 1000,
  meanMs: share * 10,
  maxMs: share * 40,
  rows: 300,
  hitRatio: 1,
  share,
  ...extra,
})

const STATEMENTS = {
  supported: true,
  sort: "total",
  resettable: true,
  totalMs: 1000,
  since: "2026-10-01T08:00:00Z",
  statements: [
    statement("s1", "SELECT *\n  FROM orders\n WHERE customer_id = $1", 0.6, { hitRatio: 0.5 }),
    statement("s2", "UPDATE carts SET total = $1 WHERE id = $2", 0.3),
    statement("s3", "SELECT count(*) FROM order_items", 0.1),
  ],
}

const STATEMENTS_OFF = {
  supported: false,
  reason: "pg_stat_statements is not installed in this database",
  sort: "total",
  resettable: false,
  totalMs: 0,
  statements: [],
  enable: {
    extension: "pg_stat_statements",
    sql: "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;",
    available: true,
    preloaded: true,
    note: "The library is loaded; creating the extension in this database turns the statistics on.",
  },
}

const table = (schema: string, name: string, extra: Record<string, unknown> = {}) => ({
  schema,
  table: name,
  kind: "table",
  rows: 9_000,
  deadRows: 0,
  totalBytes: 3_400_000,
  tableBytes: 2_300_000,
  indexBytes: 1_000_000,
  toastBytes: 8_192,
  bloatBytes: 120_000,
  seqScans: 4,
  seqRowsRead: 18_000,
  indexScans: 18_000,
  indexRowsRead: 18_000,
  inserts: 9_000,
  updates: 0,
  deletes: 0,
  modsSinceAnalyze: 0,
  lastAutovacuum: "2026-10-01T06:00:00Z",
  lastAnalyze: "2026-10-01T06:00:00Z",
  ...extra,
})

const TABLESTATS = {
  supported: true,
  schema: "",
  truncated: false,
  tables: [
    table("public", "sessions_log", {
      rows: 20_000,
      deadRows: 40_000,
      totalBytes: 18_000_000,
      lastAutovacuum: undefined,
    }),
    table("analytics", "events", { rows: 60_000, totalBytes: 9_800_000 }),
    table("public", "orders"),
    table("public", "import_staging", {
      rows: 5_000,
      totalBytes: 256_000,
      bloatBytes: -1,
      lastAnalyze: undefined,
      lastAutovacuum: undefined,
    }),
  ],
}

const index = (name: string, extra: Record<string, unknown> = {}) => ({
  schema: "public",
  table: "orders",
  name,
  method: "btree",
  columns: ["status", "placed_at"],
  unique: false,
  primary: false,
  valid: true,
  bytes: 500_000,
  scans: 12,
  rowsRead: 40,
  unused: false,
  ...extra,
})

const INDEXSTATS = {
  supported: true,
  schema: "",
  truncated: false,
  indexes: [
    index("orders_pkey", {
      primary: true,
      unique: true,
      constraint: true,
      columns: ["id"],
      scans: 18_000,
    }),
    index("orders_status_placed_idx", {
      scans: 0,
      unused: true,
      duplicateOf: "orders_status_placed_copy",
    }),
    index("orders_status_placed_copy"),
    index("orders_status_only", {
      columns: ["status"],
      bytes: 80_000,
      scans: 0,
      unused: true,
      coveredBy: "orders_status_placed_copy",
    }),
    index("events_occurred_idx", {
      schema: "analytics",
      table: "events",
      columns: ["occurred_at"],
      bytes: 2_400_000,
      scans: 0,
      unused: true,
    }),
  ],
}

const POSTGRES_ACTIONS = {
  supported: true,
  actions: [
    {
      id: "vacuum",
      label: "Vacuum",
      description: "Reclaims the space dead rows hold. Runs alongside reads and writes.",
      scope: "either",
      requires: "service.control",
    },
    {
      id: "vacuum_analyze",
      label: "Vacuum and analyze",
      description: "Vacuums, then refreshes the planner's statistics in the same pass.",
      scope: "either",
      requires: "service.control",
    },
    {
      id: "analyze",
      label: "Analyze",
      description: "Refreshes the statistics the planner chooses plans from. Blocks nothing.",
      scope: "either",
      requires: "service.control",
    },
    {
      id: "vacuum_full",
      label: "Vacuum full",
      description:
        "Rewrites the table into a new file. Takes an exclusive lock: nothing can read or write the table until it finishes.",
      scope: "either",
      blocking: true,
      requires: "destructive",
    },
    {
      id: "reindex",
      label: "Reindex",
      description: "Rebuilds indexes from the table. Blocks writes unless run concurrently.",
      scope: "either",
      blocking: true,
      requires: "destructive",
      options: ["concurrently"],
    },
  ],
}

const SQLITE_ACTIONS = {
  supported: true,
  actions: [
    {
      id: "integrity_check",
      label: "Integrity check",
      description: "Reads every page looking for corruption. Changes nothing.",
      scope: "database",
      readOnly: true,
      requires: "service.control",
    },
    {
      id: "wal_checkpoint",
      label: "Checkpoint",
      description: "Moves what the write-ahead log holds into the database file.",
      scope: "database",
      requires: "service.control",
      options: ["mode"],
    },
    {
      id: "vacuum",
      label: "Vacuum",
      description: "Rebuilds the whole file. Nothing else can use the database while it runs.",
      scope: "database",
      blocking: true,
      requires: "destructive",
    },
  ],
}

const MAINTENANCE_RESULT = {
  action: "vacuum_analyze",
  statements: ['VACUUM (VERBOSE, ANALYZE) "public"."sessions_log"'],
  output: [
    'INFO: vacuuming "shop_main.public.sessions_log"',
    "tuples: 40000 removed, 20000 remain",
  ],
  outputTruncated: false,
  duration: "99ms",
  ok: true,
}

const REPLICATION = {
  supported: true,
  role: "primary",
  replicas: [
    {
      name: "replica-1",
      client: "10.0.0.7",
      state: "streaming",
      syncState: "async",
      lagBytes: 4096,
      lagSeconds: 2,
      replayLsn: "0/3000148",
      since: "2026-10-01T08:00:00Z",
    },
  ],
  slots: [
    { name: "replica_1", type: "physical", active: true, retainedBytes: 4096 },
    {
      name: "old_etl",
      type: "logical",
      plugin: "pgoutput",
      active: false,
      retainedBytes: 2_147_483_648,
      walStatus: "extended",
    },
  ],
  publications: [
    {
      name: "shop_feed",
      owner: "app",
      allTables: false,
      insert: true,
      update: true,
      delete: false,
      truncate: false,
      tables: 2,
    },
  ],
  subscriptions: [],
  sources: [],
  facts: { wal_level: "logical", max_wal_senders: "10" },
}

const STANDALONE = {
  supported: true,
  role: "standalone",
  replicas: [],
  slots: [],
  publications: [],
  subscriptions: [],
  sources: [],
  facts: { wal_level: "replica" },
}

const SQLITE_STATS = {
  supported: true,
  at: NOW,
  driver: "sqlite",
  version: "SQLite 3.46.0",
  role: "standalone",
  database: "/srv/notes/notes.db",
  databaseBytes: 319_488,
  counters: {},
  gauges: {
    fileBytes: 319_488,
    walBytes: 0,
    pageSize: 4096,
    pageCount: 78,
    freelistPages: 0,
    reclaimableBytes: 0,
    tables: 3,
    indexes: 1,
  },
  facts: { journalMode: "delete", synchronous: "full", encoding: "UTF-8" },
  pool: null,
}

const SQLITE_FILE = {
  path: "/srv/notes/notes.db",
  fileBytes: 319_488,
  walBytes: 0,
  shmBytes: 0,
  modified: "2026-10-01T08:00:00Z",
  pageSize: 4096,
  pageCount: 78,
  freelistPages: 0,
  reclaimableBytes: 0,
  journalMode: "delete",
  autoVacuum: "none",
  synchronous: "full",
  encoding: "UTF-8",
  foreignKeys: true,
  userVersion: 0,
  applicationId: 0,
  schemaVersion: 5,
  version: "3.46.0",
  objects: { table: 3, index: 1 },
  attached: [{ name: "main", file: "/srv/notes/notes.db" }],
  compileOptions: ["ENABLE_DBSTAT_VTAB", "THREADSAFE=1"],
  sizesKnown: true,
}

const CLICKHOUSE_STATS = (call: number) => ({
  supported: true,
  at: at(call),
  driver: "clickhouse",
  version: "ClickHouse 24.8.14",
  database: "analytics",
  databaseBytes: 1_942_970,
  connections: { total: 1, active: 1, idle: 0, idleInTransaction: 0, waiting: 0, max: 4096 },
  counters: {
    queries: 27_000 + call * 10,
    selectQueries: 15_000 + call * 8,
    insertedRows: 23_000_000,
  },
  gauges: {
    runningQueries: 2,
    runningMerges: 1,
    runningMutations: 0,
    totalParts: 65,
    memoryResident: 691_314_688,
  },
  pool: null,
})

const CH_QUERIES = {
  queries: [
    {
      id: "ea49d737-ec04-4d1b-b280-0f8baa22366e",
      user: "etl",
      client: "10.0.0.9",
      clientName: "clickhouse-go",
      database: "analytics",
      kind: "Insert",
      elapsed: 42,
      rowsRead: 1_200_000,
      bytesRead: 48_000_000,
      totalRows: 2_400_000,
      progress: 0.5,
      rowsWritten: 0,
      memory: 64_000_000,
      peakMemory: 96_000_000,
      cancelled: false,
      query: "INSERT INTO page_views SELECT * FROM staging",
    },
    {
      id: "self",
      user: "dashboard",
      elapsed: 0.01,
      rowsRead: 0,
      bytesRead: 0,
      totalRows: 0,
      progress: -1,
      rowsWritten: 0,
      memory: 0,
      peakMemory: 0,
      cancelled: false,
      query: "SELECT query_id FROM system.processes",
      self: true,
    },
  ],
}

const CH_PARTS = {
  database: "analytics",
  truncated: false,
  partitions: [
    {
      database: "analytics",
      table: "page_views",
      partition: "202609",
      parts: 7,
      rows: 195_800,
      bytes: 1_900_000,
      uncompressedBytes: 4_800_000,
      modified: NOW,
    },
    {
      database: "analytics",
      table: "daily_totals",
      partition: "tuple()",
      parts: 1,
      rows: 31,
      bytes: 414,
      uncompressedBytes: 310,
      modified: NOW,
    },
  ],
  parts: [
    {
      database: "analytics",
      table: "page_views",
      partition: "202609",
      name: "202609_1_7_1",
      active: true,
      rows: 195_800,
      bytes: 1_900_000,
      compressedBytes: 1_900_000,
      uncompressedBytes: 4_800_000,
      marks: 25,
      level: 1,
      type: "Wide",
      disk: "default",
      modified: NOW,
    },
  ],
}

const CH_MERGES = {
  merges: [
    {
      database: "analytics",
      table: "page_views",
      elapsed: 12,
      progress: 0.25,
      parts: 6,
      resultPart: "202609_1_6_2",
      bytes: 12_000_000,
      rowsRead: 50_000,
      rowsWritten: 50_000,
      memory: 8_000_000,
      isMutation: false,
    },
  ],
}

const CH_MUTATIONS = {
  mutations: [
    {
      database: "analytics",
      table: "page_views",
      id: "mutation_9.txt",
      command: "DELETE WHERE is_bot = 1",
      created: NOW,
      partsToDo: 0,
      done: false,
      failReason: "Code: 341. Unfinished",
      failedAt: NOW,
    },
  ],
}

const advice = (
  id: string,
  level: string,
  category: string,
  extra: Record<string, unknown> = {},
): { id: string; sql?: string; [field: string]: unknown } => ({
  id,
  level,
  category,
  title: id,
  detail: `What was measured for ${id}.`,
  advice: `What to do about ${id}.`,
  ...extra,
})

const ADVISOR = {
  checkedAt: NOW,
  silences: ["Accounts could not be assessed: permission denied for table pg_authid"],
  tablesOmitted: 0,
  tablesChecked: 9,
  engineChecks: true,
  truncated: false,
  version: "PostgreSQL 16.4",
  endOfLife: {
    product: "PostgreSQL",
    release: "16",
    date: "2028-11-09T00:00:00Z",
    past: false,
    daysLeft: 768,
  },
  findings: [
    advice("connections-near-limit", "critical", "performance", {
      title: "92 of 100 connections are in use",
      targets: [{ kind: "setting", name: "max_connections" }],
      sql: "ALTER SYSTEM SET max_connections = '150';",
      link: "/databases/1/settings",
    }),
    advice("no-backup", "warning", "reliability", {
      title: "No backup of this database has been taken from here",
      targets: [{ kind: "database", name: "shop" }],
      link: "/databases/1/backups",
    }),
    advice("unindexed-foreign-key", "warning", "performance", {
      title: "2 foreign keys with no index",
      objects: ["orders(customer_id)", "order_items(product_id)"],
      targets: [
        {
          kind: "table",
          schema: "public",
          name: "orders",
          detail: "customer_id",
          sql: 'CREATE INDEX "orders_customer_id_idx" ON "public"."orders" ("customer_id");',
        },
        {
          kind: "table",
          schema: "public",
          name: "order_items",
          detail: "product_id",
          sql: 'CREATE INDEX "order_items_product_id_idx" ON "public"."order_items" ("product_id");',
        },
      ],
      sql: 'CREATE INDEX "orders_customer_id_idx" ON "public"."orders" ("customer_id");\nCREATE INDEX "order_items_product_id_idx" ON "public"."order_items" ("product_id");',
    }),
    advice("no-primary-key", "warning", "reliability", {
      title: "1 table with no primary key",
      targets: [
        {
          kind: "table",
          schema: "public",
          name: "audit_log",
          detail: "adds an identity column; the table is rewritten and locked while it is",
          sql: 'ALTER TABLE "public"."audit_log" ADD COLUMN id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY;',
        },
      ],
      sql: 'ALTER TABLE "public"."audit_log" ADD COLUMN id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY;',
    }),
    advice("dead-rows", "warning", "maintenance", {
      title: "1 table carrying dead rows",
      targets: [
        {
          kind: "table",
          schema: "public",
          name: "sessions_log",
          sql: 'VACUUM ANALYZE "public"."sessions_log";',
        },
      ],
      sql: 'VACUUM ANALYZE "public"."sessions_log";',
      link: "/databases/1/performance",
    }),
    advice("slow-log-off", "notice", "maintenance", {
      title: "Slow statements are not logged",
      targets: [{ kind: "setting", name: "log_min_duration_statement" }],
    }),
  ],
}

/** The server's verdict on a statement, as the Query page would run it under. */
const classify = (_call: number, _url: URL, body: unknown) => {
  const query = (body as { query?: string })?.query ?? ""
  const destructive = !/^\s*create\s+index/i.test(query) || /\b(alter|drop|vacuum)\b/i.test(query)
  return {
    destructive,
    level: destructive ? "high" : "medium",
    reasons: [destructive ? "alters a database object" : "creates a database object"],
    statements: [],
  }
}

const ANSWERS: Record<string, unknown> = {
  "GET 1/stats": stats,
  "GET 1/activity": ACTIVITY,
  "GET 1/locks": LOCKS,
  "GET 1/statements": STATEMENTS,
  "GET 1/tablestats": TABLESTATS,
  "GET 1/indexstats": INDEXSTATS,
  "GET 1/maintenance": POSTGRES_ACTIONS,
  "GET 1/replication": REPLICATION,
  "GET 1/advisor": ADVISOR,
  "POST 1/classify": classify,
  "POST 1/activity/cancel": { cancelled: "x" },
  "POST 1/activity/kill": { killed: "x" },
  "POST 1/maintenance": MAINTENANCE_RESULT,
  "POST 7/maintenance": {
    action: "wal_checkpoint",
    statements: ["PRAGMA wal_checkpoint(PASSIVE)"],
    output: ["busy=0 log=0 checkpointed=0"],
    outputTruncated: false,
    duration: "1ms",
    ok: true,
  },
  "POST 1/statements/reset": { ok: true, statement: "SELECT pg_stat_statements_reset()" },
  "POST 1/server/extensions": { ok: true },
  "POST 1/script": { statements: [{ index: 0, sql: "", status: "ok" }], failed: -1 },
  "POST 1/explain": {
    result: {
      columns: ["QUERY PLAN"],
      rows: [
        ["Aggregate  (cost=289.50..289.51 rows=1 width=8)"],
        ["  ->  Seq Scan on order_items"],
      ],
    },
    format: "text",
    analyzed: false,
    rolledBack: false,
  },
  "GET 7/stats": SQLITE_STATS,
  "GET 7/sqlite/file": SQLITE_FILE,
  "GET 7/maintenance": SQLITE_ACTIONS,
  "GET 7/tablestats": {
    supported: true,
    schema: "main",
    truncated: false,
    tables: [
      {
        ...table("main", "notes"),
        deadRows: -1,
        seqScans: -1,
        indexScans: -1,
        seqRowsRead: -1,
        indexRowsRead: -1,
        inserts: -1,
        updates: -1,
        deletes: -1,
        modsSinceAnalyze: -1,
        lastAutovacuum: undefined,
        lastAnalyze: undefined,
      },
    ],
  },
  "GET 7/indexstats": {
    supported: true,
    schema: "main",
    truncated: false,
    indexes: [
      index("notes_notebook_idx", {
        schema: "main",
        table: "notes",
        columns: ["notebook_id"],
        scans: -1,
        rowsRead: -1,
      }),
    ],
    notes: ["SQLite keeps no index use counts, so no index can be called unused."],
  },
  "GET 7/advisor": { ...ADVISOR, silences: [], endOfLife: undefined, findings: [] },
  "GET 2/stats": CLICKHOUSE_STATS,
  "GET 2/clickhouse/queries": CH_QUERIES,
  "GET 2/clickhouse/parts": CH_PARTS,
  "GET 2/clickhouse/merges": CH_MERGES,
  "GET 2/clickhouse/mutations": CH_MUTATIONS,
  "POST 2/activity/kill": { killed: "x" },
}

/** The fixture's MariaDB connection, answering as a ClickHouse server. */
const AS_CLICKHOUSE: DatabaseMock = {
  rows: { 2: { driver: "clickhouse", name: "analytics" } },
  summaries: {
    2: {
      flavor: "clickhouse",
      flavorLabel: "ClickHouse",
      versionNumber: "24.8.14",
      version: "ClickHouse 24.8.14",
    },
  },
}

type Sent = { request: string; body: unknown; query: string }

/**
 * The section's fixture, with the operations routes answered over it. `seen`
 * is every request made about one database, in order; `sent` the ones that
 * were not reads, with what they carried.
 */
async function mockOps(
  page: Page,
  options: DatabaseMock & { answers?: Record<string, Answer>; capabilities?: string[] } = {},
) {
  const mock = await mockDatabases(page, options)
  const answers: Record<string, Answer> = { ...ANSWERS, ...options.answers }
  const calls = new Map<string, number>()
  const seen: string[] = []
  const sent: Sent[] = []

  if (options.capabilities) {
    const capabilities = options.capabilities
    await page.route("**/api/v1/auth/session", (route) =>
      route.fulfill({
        json: {
          authenticated: true,
          needsTotp: false,
          needsEnrollment: false,
          require2fa: false,
          capabilities,
          user: {
            id: 2,
            username: "operator",
            displayName: "Operator",
            avatarVersion: 0,
            role: "limited",
            totpEnabled: true,
            disabled: false,
            mustChangePassword: false,
            lastLoginAt: NOW,
            createdAt: NOW,
          },
        },
      }),
    )
  }
  await page.route("**/api/v1/databases/*/**", async (route) => {
    const url = new URL(route.request().url())
    const match = /^\/api\/v1\/databases\/(\d+)(\/.+)$/.exec(url.pathname)
    if (!match) return route.fallback()
    const method = route.request().method()
    const key = `${method} ${match[1]}${match[2]}`
    seen.push(key)
    const body = method === "GET" ? undefined : route.request().postDataJSON()
    if (method !== "GET") sent.push({ request: key, body, query: url.search })
    if (method === "GET" && match[2] === "/stats/history" && !Object.hasOwn(answers, key)) {
      const saved = answers[`GET ${match[1]}/stats`]
      if (saved === undefined) return route.fulfill({ json: { samples: [] } })
      const samples = []
      for (let index = 0; index < 3; index++) {
        const value = await (typeof saved === "function" ? saved(index, url) : saved)
        if (isReply(value)) return route.fulfill({ status: value.status, json: value.body })
        samples.push({ at: Date.parse(NOW) - 60_000 + index * 5_000, stats: value, gap: false })
      }
      return route.fulfill({ json: { samples, everySeconds: 30, retentionHours: 168 } })
    }
    if (!Object.hasOwn(answers, key)) return route.fallback()
    const call = calls.get(key) ?? 0
    calls.set(key, call + 1)
    const answer = answers[key]
    // An answer may be a promise: a read held until the test lets it go.
    const value = await (typeof answer === "function"
      ? (answer as (call: number, url: URL, body: unknown) => unknown)(call, url, body)
      : answer)
    if (isReply(value)) return route.fulfill({ status: value.status, json: value.body })
    return route.fulfill({ json: value }).catch(() => undefined)
  })
  return { ...mock, seen, sent, calls }
}

/**
 * A clock the test moves: the page's polls fire only when the test says so,
 * so "one reading" and "the reading after it" are states a test stays in.
 */
async function holdTime(page: Page) {
  await page.clock.install({ time: new Date(NOW) })
  await page.clock.pauseAt(new Date(Date.parse(NOW) + 1_000))
}
const tick = (page: Page, ms = 30_000) => page.clock.runFor(ms)

/** Opens a page of the section and waits for the list of connections to have answered. */
async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const views = (page: Page) => page.getByRole("group", { name: "Performance views" })
const view = (page: Page, name: string | RegExp) => views(page).getByRole("button", { name })
const region = (page: Page, name: string) => page.getByRole("region", { name, exact: true })
const sessionRow = (page: Page, pid: string) =>
  page.getByRole("row", { name: `Session ${pid}`, exact: true })
const dialog = (page: Page) => page.getByRole("dialog")

// ---------------------------------------------------------------------------
// Performance
// ---------------------------------------------------------------------------

test("the page opens on recorded activity and offers the views its engine has", async ({
  page,
}) => {
  await holdTime(page)
  const ops = await mockOps(page)
  await visit(page, "/databases/1/performance")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(view(page, /^Locks/)).toHaveText(/Locks\s*1/)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await tick(page)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  await expect(views(page).getByRole("button")).toHaveText([
    /^Overview$/,
    /^Sessions/,
    /^Locks/,
    /^Statements$/,
    /^Tables$/,
    /^Indexes$/,
    /^Replication$/,
  ])
  await expect(view(page, /^Overview$/)).toHaveAttribute("aria-pressed", "true")
  // The charts show saved activity as soon as the page opens.
  await expect(page.getByText(/Recorded activity/)).toBeVisible()
  await expect(page.getByRole("heading", { name: "Sessions", level: 2 })).toBeVisible()
  // A chart with nothing above zero in the window is named, not drawn flat.
  await expect(page.getByText(/Nothing else has moved\./)).toBeVisible()
  await expect(page.getByRole("heading", { name: "Lock waits and deadlocks" })).toHaveCount(0)
  // The snapshot and the session list are the page's; a view's own read waits for the view.
  expect(ops.seen.filter((request) => !/\/(stats|stats\/history|activity)$/.test(request))).toEqual(
    [],
  )
})

test("a file-based engine is read as its file, and is asked nothing a server would be", async ({
  page,
}) => {
  const ops = await mockOps(page)
  // A view the engine lacks is not opened by an address that names it.
  await visit(page, "/databases/7/performance?view=sessions")

  await expect(views(page).getByRole("button")).toHaveText(["File", "Tables", "Indexes"])
  await expect(view(page, "File")).toHaveAttribute("aria-pressed", "true")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(region(page, "The file")).toContainText("/srv/notes/notes.db")

  // The engine's own housekeeping, with a choice inside an action as one control.
  const maintenance = region(page, "Maintenance")
  await expect(maintenance.getByText("Integrity check")).toBeVisible()
  await maintenance.getByRole("radio", { name: "passive" }).click()
  await maintenance.getByRole("button", { name: "Checkpoint, passive: the whole file" }).click()
  await expect(dialog(page)).toContainText("Finished")
  expect(ops.sent.find((sent) => sent.request === "POST 7/maintenance")?.body).toEqual({
    action: "wal_checkpoint",
    options: { mode: "passive" },
  })
  await dialog(page).getByRole("button", { name: "Close" }).first().click()

  // What locks the file is asked about first.
  await maintenance.getByRole("button", { name: "Vacuum: the whole file" }).click()
  await expect(dialog(page)).toContainText("Nothing else can use the database while it runs")
  await dialog(page).getByRole("button", { name: "Cancel" }).click()

  await view(page, "Indexes").click()
  await expect(page.getByText("SQLite keeps no index use counts")).toBeVisible()
  // No use counts: no Scans column, and no index accused of being unused.
  await expect(region(page, "Indexes").getByRole("columnheader", { name: "Scans" })).toHaveCount(0)

  expect(
    ops.seen.filter((request) => /\/(activity|locks|statements|replication)$/.test(request)),
  ).toEqual([])
})

test("an idle session is never the longest-running statement, and idle sessions fold away", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=sessions")
  const sessions = region(page, "Sessions")

  // The connection idle for a day is not what has run longest.
  await expect(sessions.getByText("Longest statement")).toContainText("48s")
  await expect(sessionRow(page, "900")).toHaveCount(0)

  const chips = sessions.getByRole("group", { name: "Sessions by state" })
  await expect(chips.getByRole("button")).toHaveText([
    /Active\s*2/,
    /Blocked\s*1/,
    /Idle in transaction\s*1/,
    /Idle\s*1/,
  ])

  // The one holding others up leads; the dashboard's own read is marked.
  await expect(sessions.getByRole("row").nth(1)).toHaveAttribute("aria-label", "Session 12,3301")
  await expect(sessionRow(page, "60")).toContainText("this dashboard")
  // A transaction left open and idle past its line is amber; a wait past its line, red.
  await expect(sessionRow(page, "12,3301").locator(".text-warning").first()).toBeVisible()
  await expect(sessionRow(page, "40,77").locator(".text-destructive").first()).toBeVisible()

  // Folded, and there when asked for.
  await sessions.getByRole("button", { name: /1 idle session/ }).click()
  await expect(sessionRow(page, "900")).toContainText("1d")

  // A state chip is the list of that state, and is in the address.
  await chips.getByRole("button", { name: /Blocked/ }).click()
  await expect(page).toHaveURL(/state=blocked/)
  await expect(sessions.getByRole("row")).toHaveCount(2)
  await expect(sessionRow(page, "40,77")).toBeVisible()

  await sessions.getByLabel("Filter the sessions").fill("no-such-app")
  await expect(sessions.getByText("No session matches no-such-app.")).toBeVisible()
})

test("a blocked session names the one it is behind, whole, and each opens the other", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=sessions")

  // The blocker's id has a comma in it: it is matched whole, never split.
  await expect(sessionRow(page, "12,3301")).toContainText("blocking 1")
  const behind = sessionRow(page, "40,77").getByRole("button", {
    name: "Open session 12,3301, which this one waits on",
  })
  await expect(behind).toHaveText("12,3301")
  await behind.click()

  const panel = dialog(page)
  await expect(panel).toContainText("Session 12,3301")
  await expect(panel).toContainText("checkout-api")
  await expect(panel.getByText("Transaction open for")).toBeVisible()
  // It is idle inside a transaction: there is no statement to cancel.
  await expect(panel.getByRole("button", { name: "Terminate session" })).toBeVisible()
  await expect(panel.getByRole("button", { name: "Cancel statement" })).toHaveCount(0)
  // And from the blocker to the one it holds.
  await panel.getByRole("button", { name: "40,77" }).click()
  await expect(dialog(page)).toContainText("Session 40,77")
  await expect(dialog(page).getByText("Waiting for")).toBeVisible()
})

test("cancelling and terminating are confirmed with the session named, and sent for it", async ({
  page,
}) => {
  const ops = await mockOps(page)
  await visit(page, "/databases/1/performance?view=sessions")

  await page.getByRole("button", { name: "Cancel the statement of session 52" }).click()
  const confirm = dialog(page)
  await expect(confirm).toContainText("Cancel statement")
  await expect(confirm).toContainText("Session 52")
  await expect(confirm).toContainText("SELECT count(*) FROM order_items")
  await confirm.getByRole("button", { name: "Stop the statement" }).click()
  await expect.poll(() => ops.sent.map((sent) => sent.request)).toEqual(["POST 1/activity/cancel"])
  expect(ops.sent[0].body).toEqual({ pid: "52" })

  await page.getByRole("button", { name: "Terminate session 12,3301" }).click()
  await expect(dialog(page)).toContainText("its open transaction is rolled back")
  await dialog(page).getByRole("button", { name: "Terminate session" }).click()
  await expect
    .poll(() => ops.sent.at(-1))
    .toMatchObject({
      request: "POST 1/activity/kill",
      body: { pid: "12,3301" },
    })

  // The dashboard's own session is never offered either.
  await expect(sessionRow(page, "60").getByRole("button")).toHaveCount(0)
  // A session idle in a transaction has nothing to cancel.
  await expect(
    page.getByRole("button", { name: "Cancel the statement of session 12,3301" }),
  ).toHaveCount(0)
})

test("a role that may not remove things is offered no way to stop, reset or lock", async ({
  page,
}) => {
  await mockOps(page, { capabilities: ["read", "service.control"] })
  await visit(page, "/databases/1/performance?view=sessions")
  await expect(sessionRow(page, "52")).toBeVisible()
  await expect(page.getByRole("button", { name: /^(Cancel the statement|Terminate)/ })).toHaveCount(
    0,
  )

  await view(page, /^Statements$/).click()
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(4)
  await expect(page.getByRole("button", { name: "Reset statistics" })).toHaveCount(0)

  // Maintenance that runs alongside the application is service control; what locks is not.
  await view(page, /^Tables$/).click()
  await page.getByRole("button", { name: "Maintain public.orders" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Vacuum", "Vacuum and analyze", "Analyze"])
})

test("a viewer reads everything and is offered nothing that changes anything", async ({ page }) => {
  await mockOps(page, { viewer: true })
  await visit(page, "/databases/1/performance?view=tables")
  await expect(region(page, "Tables").getByRole("row")).toHaveCount(5)
  await expect(page.getByRole("button", { name: /^Maintain/ })).toHaveCount(0)

  await view(page, /^Statements$/).click()
  await region(page, "Statements")
    .getByRole("row", { name: /UPDATE carts/ })
    .click()
  await expect(dialog(page)).toContainText("Of runtime")
  await expect(dialog(page).getByRole("button", { name: "Open in Query" })).toHaveCount(0)
})

test("who waits on whom is a tree, with a queue drawn once", async ({ page }) => {
  const ops = await mockOps(page)
  await visit(page, "/databases/1/performance?view=locks")
  const locks = region(page, "Locks")

  await expect(locks).toContainText("3 sessions are waiting behind one other.")
  const tree = locks.getByRole("list", { name: "Blocking sessions" })
  // One top: the session that waits on nobody.
  await expect(tree.locator("> li")).toHaveCount(1)
  const top = tree.locator("> li").first()
  await expect(top).toContainText("Session 101")
  await expect(top).toContainText("idle in transaction")
  await expect(top).toContainText("transaction open 5m")
  await expect(top).toContainText("blocking 3")
  // 301 is behind 201, which is behind 101: it stands once, at its place in the queue.
  await expect(top.getByText("Session 301")).toHaveCount(1)
  await expect(top.locator("> div > ul > li")).toHaveCount(1)
  await expect(top.locator("> div > ul > li").first()).toContainText("Session 201")
  await expect(top.locator("> div > ul > li > div > ul > li").first()).toContainText("Session 301")
  await expect(top).toContainText("AccessExclusiveLock")
  await expect(top).toContainText("public.orders")

  // The blocker is idle in a transaction: only ending it lets go.
  await expect(
    page.getByRole("button", { name: "Cancel the statement of session 101" }),
  ).toHaveCount(0)
  await page.getByRole("button", { name: "Terminate session 101" }).click()
  await dialog(page).getByRole("button", { name: "Terminate session" }).click()
  await expect
    .poll(() => ops.sent.at(-1))
    .toMatchObject({
      request: "POST 1/activity/kill",
      body: { pid: "101" },
    })

  // The lock table is reference, folded under the tree.
  await locks.getByRole("button", { name: /The lock table/ }).click()
  await expect(locks.getByRole("row", { name: /RowExclusiveLock/ })).toContainText("Granted")
})

test("locks an account may not read say which grant, and nothing waiting says so", async ({
  page,
}) => {
  await mockOps(page, {
    answers: {
      "GET 1/locks": {
        supported: false,
        reason:
          "The lock tables could not be read: SELECT command denied to user 'app' for table data_lock_waits",
        waits: [],
        locks: [],
        truncated: false,
      },
    },
  })
  await visit(page, "/databases/1/performance?view=locks")
  await expect(page.getByText("The locks cannot be read")).toBeVisible()
  await expect(page.getByText(/SELECT command denied to user 'app'/)).toBeVisible()
})

test("statements are ranked by their share, ordered by the server, and one opens with its plan", async ({
  page,
}) => {
  const ops = await mockOps(page, {
    answers: {
      "GET 1/statements": (_call: number, url: URL) => ({
        ...STATEMENTS,
        sort: url.searchParams.get("sort"),
      }),
    },
  })
  // The home's link lands here.
  await visit(page, "/databases/1/performance?view=statements")
  const statements = region(page, "Statements")

  const rows = statements.getByRole("row")
  await expect(rows).toHaveCount(4)
  // A statement is one line, whatever its layout was.
  await expect(rows.nth(1)).toContainText("SELECT * FROM orders WHERE customer_id = $1")
  await expect(rows.nth(1)).toContainText("60%")
  // Reads that missed the cache are called out.
  await expect(rows.nth(1).locator(".text-warning")).toHaveText("50.0%")
  await expect(statements).toContainText("1.0 s of statement time counted")

  // Another order is asked of the server, which ranks over everything it tracked.
  const asked = page.waitForRequest((request) => request.url().includes("/statements?sort=mean"))
  await statements.getByRole("radio", { name: "Mean" }).click()
  await asked
  await expect(page).toHaveURL(/sort=mean/)

  // A shape is planned with values put where its placeholders stand: one
  // field for each, named by the words before it.
  await rows.nth(1).click()
  await expect(page).toHaveURL(/statement=s1/)
  const panel = dialog(page)
  await expect(panel).toContainText("an engine plans values")
  const value = panel.getByRole("textbox", { name: "The value for $1, after customer_id =" })
  await value.fill("42")
  await panel.getByRole("button", { name: "Explain with these values" }).click()
  await expect(panel).toContainText("Seq Scan on order_items")
  expect(ops.sent.find((sent) => sent.request === "POST 1/explain")?.body).toEqual({
    query: "SELECT *\n  FROM orders\n WHERE customer_id = 42",
    format: "text",
  })
  await page.keyboard.press("Escape")
  await expect(page).not.toHaveURL(/statement=/)
  // The keyboard goes back to the row that opened it.
  await expect(rows.nth(1)).toBeFocused()
  ops.sent.length = 0

  // One with no values in it is planned as it stands. Nothing is executed.
  await rows.nth(3).click()
  await dialog(page).getByRole("button", { name: "Explain" }).click()
  await expect(dialog(page)).toContainText("Seq Scan on order_items")
  expect(ops.sent.find((sent) => sent.request === "POST 1/explain")?.body).toEqual({
    query: "SELECT count(*) FROM order_items",
    format: "text",
  })

  await dialog(page).getByRole("button", { name: "Open in Query" }).click()
  await expect.poll(() => new URL(page.url()).pathname).toBe("/databases/1/query")
  await expect(page.locator("[data-slot=sql-editor] .view-lines")).toHaveText(
    "SELECT count(*) FROM order_items",
  )
  await expect(page.getByText("Nothing has been run in this tab", { exact: true })).toBeVisible()
})

test("statement statistics that are off say how to turn them on, to the role that may", async ({
  page,
}) => {
  let on = false
  const ops = await mockOps(page, {
    answers: {
      "GET 1/statements": () => (on ? STATEMENTS : STATEMENTS_OFF),
      "POST 1/server/extensions": () => {
        on = true
        return { ok: true }
      },
    },
  })
  await visit(page, "/databases/1/performance?view=statements")
  await expect(page.getByText("This server is not counting its statements")).toBeVisible()
  await expect(page.getByText(/creating the extension in this database turns/)).toBeVisible()
  await page.getByRole("button", { name: "Turn on statement statistics" }).click()
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(4)
  expect(ops.sent.find((sent) => sent.request === "POST 1/server/extensions")?.body).toEqual({
    name: "pg_stat_statements",
  })
})

test("a role that may not administer is told the statistics are off and offered no switch", async ({
  page,
}) => {
  await mockOps(page, {
    capabilities: ["read", "service.control", "destructive"],
    answers: { "GET 1/statements": STATEMENTS_OFF },
  })
  await visit(page, "/databases/1/performance?view=statements")
  await expect(page.getByText("This server is not counting its statements")).toBeVisible()
  await expect(page.getByRole("button", { name: "Turn on statement statistics" })).toHaveCount(0)
})

test("resetting the statistics is confirmed as the whole server's", async ({ page }) => {
  const ops = await mockOps(page)
  await visit(page, "/databases/1/performance?view=statements")
  await page.getByRole("button", { name: "Reset statistics" }).click()
  await expect(dialog(page)).toContainText("for the whole server, not only this database")
  await dialog(page).getByRole("button", { name: "Reset statistics" }).click()
  await expect.poll(() => ops.sent.map((sent) => sent.request)).toEqual(["POST 1/statements/reset"])
})

test("a view that could not be read says so and is tried again; one that stops updating keeps what it had", async ({
  page,
}) => {
  await holdTime(page)
  // Said by the test, not counted by the route: a development build mounts
  // every effect twice, and a failure meant for the first read would be spent
  // on the one that is thrown away.
  const down = { statements: true, activity: false }
  await mockOps(page, {
    answers: {
      "GET 1/statements": () =>
        down.statements ? failing("pq: canceling statement due to statement timeout") : STATEMENTS,
      "GET 1/activity": () =>
        down.activity ? failing("pq: the database system is shutting down") : ACTIVITY,
    },
  })
  await visit(page, "/databases/1/performance?view=statements")
  // It used to draw nothing at all.
  await expect(page.getByText("Could not read the statements")).toBeVisible()
  await expect(page.getByText(/canceling statement due to statement timeout/)).toBeVisible()
  down.statements = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(4)

  await view(page, /^Sessions/).click()
  await expect(sessionRow(page, "52")).toBeVisible()
  down.activity = true
  await tick(page)
  // The rows stay; the header says they are the reading before.
  await expect(region(page, "Sessions").getByText("Not updating")).toBeVisible()
  await expect(sessionRow(page, "52")).toBeVisible()
  await expect(sessionRow(page, "40,77")).toBeVisible()
})

test("statistics that cannot be read are dashes, never zeros, with the way to try again", async ({
  page,
}) => {
  let down = true
  await mockOps(page, {
    answers: {
      "GET 1/stats": (call: number) =>
        down ? failing("dial tcp: connection refused") : stats(call),
    },
  })
  await visit(page, "/databases/1/performance?view=replication")
  await expect(page.getByText("Could not read the server's statistics")).toBeVisible()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  // The view under the figures is its own read, and is there.
  await expect(region(page, "Replication")).toContainText("Primary")
  down = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
})

test("a snapshot the engine refuses is said in its words, and the views go on", async ({
  page,
}) => {
  await mockOps(page, {
    answers: {
      "GET 1/stats": {
        supported: false,
        reason: "permission denied for view pg_stat_database",
        at: NOW,
        driver: "postgres",
        databaseBytes: 0,
        counters: {},
        gauges: {},
        pool: null,
      },
    },
  })
  await visit(page, "/databases/1/performance?view=sessions")
  await expect(page.getByText("This server's statistics are not available")).toBeVisible()
  await expect(page.getByText("permission denied for view pg_stat_database")).toBeVisible()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(sessionRow(page, "52")).toBeVisible()
})

test("tables show what the engine keeps, flag what is overdue, and run its maintenance", async ({
  page,
}) => {
  const ops = await mockOps(page)
  await visit(page, "/databases/1/performance?view=tables")
  const tables = region(page, "Tables")

  // Largest first, as the server ranks them.
  const names = () =>
    tables
      .getByRole("row")
      .evaluateAll((rows) =>
        rows.slice(1).map((row) => row.querySelector("button > span:not(.sr-only)")?.textContent),
      )
  await expect(tables.getByRole("row")).toHaveCount(5)
  expect(await names()).toEqual([
    "public.sessions_log",
    "analytics.events",
    "public.orders",
    "public.import_staging",
  ])
  // A vacuum is overdue past the advisor's own line; a table with rows was never analysed.
  const overdue = tables.getByRole("row", { name: /sessions_log/ })
  await expect(overdue.locator(".text-warning")).toContainText("40K · 67%")
  await expect(
    tables.getByRole("row", { name: /import_staging/ }).locator(".text-warning"),
  ).toHaveText("never")
  // What the engine could not work out is a dash, not a zero.
  await expect(tables.getByRole("row", { name: /import_staging/ })).toContainText("—")

  // The order is the reader's, and says which way it runs.
  await tables.getByRole("button", { name: "Rows", exact: true }).click()
  await expect(tables.getByRole("columnheader", { name: "Rows", exact: true })).toHaveAttribute(
    "aria-sort",
    "descending",
  )
  expect((await names())[0]).toBe("analytics.events")

  // The schemas are chips on the page: nothing is narrowed by a place it does not show.
  const scope = tables.getByRole("group", { name: "Narrow to one of the schemas" })
  await expect(scope.getByRole("button", { name: /All schemas/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await scope.getByRole("button", { name: /analytics/ }).click()
  await expect(page).toHaveURL(/scope=analytics/)
  await expect(tables.getByRole("row")).toHaveCount(2)
  await scope.getByRole("button", { name: /All schemas/ }).click()

  // Maintenance that locks nothing runs at once and shows what the engine said.
  await page.getByRole("button", { name: "Maintain public.sessions_log" }).click()
  await page.getByRole("menuitem", { name: "Vacuum and analyze" }).click()
  await expect(dialog(page)).toContainText("Finished")
  await expect(dialog(page)).toContainText('VACUUM (VERBOSE, ANALYZE) "public"."sessions_log"')
  await expect(dialog(page)).toContainText("tuples: 40000 removed")
  expect(ops.sent.at(-1)).toMatchObject({
    request: "POST 1/maintenance",
    body: { action: "vacuum_analyze", schema: "public", table: "sessions_log" },
  })
  await dialog(page).getByRole("button", { name: "Close" }).first().click()

  // What locks the table is asked about first, with the table named.
  await page.getByRole("button", { name: "Maintain public.orders" }).click()
  await page.getByRole("menuitem", { name: "Vacuum full" }).click()
  await expect(dialog(page)).toContainText("public.orders")
  await expect(dialog(page)).toContainText("nothing can read or write the table until it finishes")
  await dialog(page).getByRole("button", { name: "Vacuum full" }).click()
  await expect
    .poll(() => ops.sent.at(-1)?.body)
    .toEqual({
      action: "vacuum_full",
      schema: "public",
      table: "orders",
    })
  await dialog(page).getByRole("button", { name: "Close" }).first().click()

  // A table opens on everything the engine keeps about it; the address holds which.
  await tables.getByRole("button", { name: "Open public.orders" }).click()
  await expect(page).toHaveURL(/object=public%2Forders/)
  await expect(dialog(page)).toContainText("Sequential scans")
  await expect(dialog(page).getByRole("link", { name: "Open in Data" })).toHaveAttribute(
    "href",
    "/databases/1/data?schema=public&table=orders",
  )
  // The choice inside an action is one control on its row, and the answer
  // already chosen is the one that does not stop the table.
  await expect(dialog(page).getByRole("radio", { name: "concurrently" })).toHaveAttribute(
    "aria-checked",
    "true",
  )
  await dialog(page).getByRole("button", { name: "Reindex concurrently: public.orders" }).click()
  await dialog(page).getByRole("button", { name: "Reindex concurrently" }).click()
  await expect
    .poll(() => ops.sent.at(-1)?.body)
    .toEqual({
      action: "reindex",
      schema: "public",
      table: "orders",
      options: { concurrently: true },
    })
})

test("a maintenance run the engine refuses says so in its words", async ({ page }) => {
  await mockOps(page, {
    answers: {
      "POST 1/maintenance": failing('pq: relation "public.orders" does not exist', 400),
    },
  })
  await visit(page, "/databases/1/performance?view=tables")
  await page.getByRole("button", { name: "Maintain public.orders" }).click()
  await page.getByRole("menuitem", { name: "Analyze", exact: true }).click()
  await expect(dialog(page)).toContainText("The engine refused")
  await expect(dialog(page)).toContainText('relation "public.orders" does not exist')
})

test("a protected connection keeps the ways to stop work and draws nothing that changes it", async ({
  page,
}) => {
  await mockOps(page, { rows: { 1: { readOnly: true } } })
  await visit(page, "/databases/1/performance?view=sessions")
  // Stopping a runaway statement changes no data.
  await expect(
    page.getByRole("button", { name: "Cancel the statement of session 52" }),
  ).toBeVisible()

  await view(page, /^Tables$/).click()
  await expect(region(page, "Tables").getByRole("row")).toHaveCount(5)
  await expect(page.getByRole("button", { name: /^Maintain/ })).toHaveCount(0)

  await view(page, /^Statements$/).click()
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(4)
  await expect(page.getByRole("button", { name: "Reset statistics" })).toHaveCount(0)

  // On a file, the checks that change nothing stay.
  await mockOps(page, { rows: { 7: { readOnly: true } } })
  await visit(page, "/databases/7/performance")
  const maintenance = region(page, "Maintenance")
  await expect(maintenance.getByRole("button", { name: /Integrity check/ })).toBeVisible()
  await expect(maintenance.getByRole("button", { name: /Vacuum|Checkpoint/ })).toHaveCount(0)
})

test("indexes are read by what is wrong with them, and a reason narrows the list", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=indexes")
  const indexes = region(page, "Indexes")
  const chips = indexes.getByRole("group", { name: "Indexes by what is wrong with them" })
  await expect(chips.getByRole("button")).toHaveText([
    /All\s*5/,
    /Duplicate\s*1/,
    /Covered\s*1/,
    /Unused\s*3/,
  ])

  await expect(indexes.getByRole("row", { name: /orders_status_placed_idx/ })).toContainText(
    "duplicate of orders_status_placed_copy",
  )
  await chips.getByRole("button", { name: /Unused/ }).click()
  await expect(page).toHaveURL(/flag=unused/)
  await expect(indexes.getByRole("row")).toHaveCount(4)
  await expect(indexes).toContainText("3 indexes have never been scanned, holding 2.8 MB.")

  // Its table opens in the Tables view.
  await indexes
    .getByRole("row", { name: /events_occurred_idx/ })
    .getByRole("button", { name: "Open the table analytics.events" })
    .click()
  // The table's panel is open over the Tables view: the address says both.
  await expect(page).toHaveURL(/view=tables&object=analytics%2Fevents/)
  await expect(dialog(page)).toContainText("analytics.events")
  await page.keyboard.press("Escape")
  await expect(view(page, /^Tables$/)).toHaveAttribute("aria-pressed", "true")
})

test("replication says what the server is, and calls out a slot nothing reads", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=replication")
  const replication = region(page, "Replication")
  await expect(replication).toContainText("Primary")
  await expect(replication.getByRole("row", { name: /replica-1/ })).toContainText("streaming")
  await expect(replication.getByRole("row", { name: /replica-1/ })).toContainText("2s · 4.0 KB")
  const idle = replication.getByRole("row", { name: /old_etl/ })
  await expect(idle).toContainText("Nothing — it holds the log")
  await expect(idle).toContainText("2.0 GB")
  await expect(replication.getByRole("row", { name: /shop_feed/ })).toContainText("insert")

  await mockOps(page, { answers: { "GET 1/replication": STANDALONE } })
  await visit(page, "/databases/1/performance?view=replication")
  await expect(page.getByText("This server is not replicating")).toBeVisible()
})

test("an analytic engine is read by its queries, parts and merges", async ({ page }) => {
  const ops = await mockOps(page, AS_CLICKHOUSE)
  await visit(page, "/databases/2/performance")
  await expect(views(page).getByRole("button")).toHaveText([
    /^Overview$/,
    /^Running queries/,
    /^Statements$/,
    /^Tables$/,
    /^Indexes$/,
    /^Parts$/,
    /^Merges/,
  ])
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  await view(page, /^Running queries/).click()
  const queries = region(page, "Running queries")
  // The page's own read of the list is not a query to watch.
  await expect(queries.getByRole("row")).toHaveCount(2)
  await expect(queries.getByRole("row").nth(1)).toContainText("INSERT INTO page_views")
  await expect(queries.getByRole("meter", { name: "Rows read of the estimate" })).toHaveAttribute(
    "aria-valuenow",
    "50",
  )
  await page.getByRole("button", { name: /^Stop query ea49d737/ }).click()
  await dialog(page).getByRole("button", { name: "Stop query" }).click()
  await expect
    .poll(() => ops.sent.at(-1))
    .toMatchObject({
      request: "POST 2/activity/kill",
      body: { pid: "ea49d737-ec04-4d1b-b280-0f8baa22366e" },
    })

  await view(page, /^Parts$/).click()
  await expect(region(page, "Parts").getByRole("row", { name: /202609/ })).toContainText("7")

  await view(page, /^Merges/).click()
  await expect(region(page, "Merges").getByRole("meter")).toHaveAttribute("aria-valuenow", "25")
  await expect(region(page, "Mutations")).toContainText("Failing")
  await expect(region(page, "Mutations")).toContainText("Code: 341. Unfinished")
  // It has no session list, no lock table: neither is asked.
  expect(ops.seen.filter((request) => /\/(activity|locks)$/.test(request))).toEqual([])
})

test("a stopped server keeps its pages and is asked nothing", async ({ page }) => {
  const ops = await mockOps(page, {
    summaries: { 1: { state: "stopped", ok: false, latencyMs: 0 } },
  })
  await visit(page, "/databases/1/performance?view=sessions")
  await expect(page.getByText("shop is stopped")).toBeVisible()
  await expect(page.getByRole("link", { name: "Open Home" })).toHaveAttribute(
    "href",
    "/databases/1",
  )
  await visit(page, "/databases/1/advisor")
  await expect(page.getByText("shop is stopped")).toBeVisible()
  expect(ops.seen).toEqual([])
})

// ---------------------------------------------------------------------------
// What a review of the page asked for
// ---------------------------------------------------------------------------

/** A read the test holds open, and lets go when it chooses. */
function held<T>() {
  let release: (value: T) => void = () => {}
  const until = new Promise<T>((resolve) => {
    release = resolve
  })
  return { until, release }
}

test("session filters use the live list while charts use recorded statistics", async ({ page }) => {
  await holdTime(page)
  // An engine whose counters know nothing of a row lock: nobody waits, by them.
  await mockOps(page, {
    answers: {
      "GET 1/stats": (call: number) => ({
        ...stats(call),
        connections: { total: 5, active: 3, idle: 1, idleInTransaction: 1, waiting: 0, max: 151 },
      }),
    },
  })
  await visit(page, "/databases/1/performance?view=sessions")
  const chips = region(page, "Sessions").getByRole("group", { name: "Sessions by state" })
  await expect(chips.getByRole("button", { name: /Blocked/ })).toHaveText(/Blocked\s*1/)
  await expect(chips.getByRole("button", { name: /Active/ })).toHaveText(/Active\s*2/)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(view(page, /^Locks/)).toHaveText(/Locks\s*1/)

  // A historical chart uses saved engine counts rather than today's session list.
  await tick(page)
  await view(page, /^Overview$/).click()
  const sessions = page
    .locator("[data-slot=panel]")
    .filter({ has: page.getByRole("heading", { name: "Sessions", level: 2 }) })
  await expect(sessions.getByRole("row", { name: /Waiting on a lock/ })).toContainText("0")
  await expect(sessions.getByRole("row", { name: /Working/ })).toContainText("3")
})

test("the session controls and rows stay in place while statistics update", async ({ page }) => {
  await holdTime(page)
  const first = held<null>()
  await mockOps(page, {
    answers: {
      "GET 1/stats": (call: number) =>
        call === 0 ? first.until.then(() => stats(0)) : stats(call),
    },
  })
  await visit(page, "/databases/1/performance?view=sessions")
  await expect(sessionRow(page, "52")).toBeVisible()
  const strip = async () => {
    // Once the page's own arrival has finished: that is a movement of four pixels, on purpose.
    await expect
      .poll(() => page.locator("[data-slot=page]").evaluate((el) => getComputedStyle(el).transform))
      .toMatch(/none|matrix\(1, 0, 0, 1, 0, 0\)/)
    return views(page).evaluate((el) => Math.round(el.getBoundingClientRect().top))
  }
  // Before any figure has been read.
  const bones = await strip()
  first.release(null)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  expect(await strip()).toBe(bones)
  // And when the trends have their second point and are drawn. A tick is
  // let land before the next: the page asks again only once it has answered.
  await tick(page)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await tick(page)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  expect(await strip()).toBe(bones)
  const row = () =>
    sessionRow(page, "52").evaluate((el) => Math.round(el.getBoundingClientRect().top))
  const before = await row()
  await tick(page)
  expect(await row()).toBe(before)
})

test("a blocker is a session if the server says who it is, and is cancelled only if it runs a statement", async ({
  page,
}) => {
  await mockOps(page, {
    answers: {
      "GET 1/activity": {
        supported: true,
        sessions: [
          session("2472", "idle_in_transaction", {
            user: "root",
            state: "Sleep",
            application: "inventory-cli",
            transactionSeconds: 25,
          }),
          session("2473", "blocked", {
            user: "root",
            application: "inventory-worker",
            seconds: 24,
            blockedByPids: ["2472"],
            query: "UPDATE items SET id = id WHERE id = 1",
          }),
        ],
      },
      "GET 1/locks": {
        supported: true,
        truncated: false,
        locks: [],
        waits: [
          // The lock list calls an open transaction "running", and has no statement for it.
          wait("2473", "2472", {
            waitingUser: "root",
            blockingUser: "root",
            blockingState: "RUNNING",
            blockingQuery: undefined,
            blockingSeconds: 25,
            waitingQuery: "UPDATE items SET id = id WHERE id = 1",
          }),
          // No account named: not a session at all.
          wait("88", "prepared-9", {
            blockingUser: undefined,
            blockingQuery: undefined,
            blockingState: undefined,
          }),
        ],
      },
    },
  })
  await visit(page, "/databases/1/performance?view=locks")
  const tree = region(page, "Locks").getByRole("list", { name: "Blocking sessions" })
  const session2472 = tree.locator("> li").filter({ hasText: "Session 2472" })
  await expect(session2472).toContainText("No statement is running")
  await expect(session2472).not.toContainText("Not a session")
  // Which application, from the session list: on a pooled server every account is the same.
  await expect(session2472).toContainText("inventory-cli")
  await expect(session2472).toContainText("idle in transaction")
  await expect(session2472).toContainText("inventory-worker")
  // Nothing to cancel on a session that runs nothing; ending it is what lets go.
  await expect(
    page.getByRole("button", { name: "Cancel the statement of session 2472" }),
  ).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Terminate session 2472" })).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Cancel the statement of session 2473" }),
  ).toBeVisible()

  const phantom = tree.locator("> li").filter({ hasText: "Session prepared-9" })
  await expect(phantom).toContainText("Not a session")
  await expect(page.getByRole("button", { name: /session prepared-9$/ })).toHaveCount(0)
})

test("two statements with one digest are two rows, each opening its own figures", async ({
  page,
}) => {
  const keyed: string[] = []
  page.on("console", (message) => {
    if (/same key/.test(message.text())) keyed.push(message.text())
  })
  await mockOps(page, {
    answers: {
      "GET 1/statements": {
        ...STATEMENTS,
        statements: [
          statement("d1", "SELECT SCHEMA ( )", 0.5, { calls: 6 }),
          statement("d1", "SELECT SCHEMA ( )", 0.3, { calls: 5 }),
          statement("s3", "SELECT count(*) FROM order_items", 0.2),
        ],
      },
    },
  })
  await visit(page, "/databases/1/performance?view=statements")
  const rows = region(page, "Statements").getByRole("row")
  await expect(rows).toHaveCount(4)
  await rows.nth(2).click()
  await expect(page).toHaveURL(/statement=d1(~|%7E)1/)
  await expect(dialog(page)).toContainText("30%")
  // Calls: the second row's five, not the first's six.
  await expect(dialog(page).getByText("5", { exact: true })).toBeVisible()
  await expect(dialog(page).getByText("6", { exact: true })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await rows.nth(1).click()
  await expect(dialog(page).getByText("6", { exact: true })).toBeVisible()
  // One row is the open one. (The page under a panel is out of the accessibility tree.)
  await expect(page.locator("[data-slot=page] tr[data-state=selected]")).toHaveCount(1)
  expect(keyed).toEqual([])
})

test("a statement with more placeholders than a form is quick for is handed to Query", async ({
  page,
}) => {
  const many = `SELECT ${Array.from({ length: 20 }, (_, n) => `$${n + 1}`).join(", ")}`
  await mockOps(page, {
    answers: {
      "GET 1/statements": { ...STATEMENTS, statements: [statement("m1", many, 1)] },
    },
  })
  await visit(page, "/databases/1/performance?view=statements&statement=m1")
  await expect(dialog(page)).toContainText("This shape has 20 placeholders")
  await expect(dialog(page).getByRole("textbox")).toHaveCount(0)
  await expect(dialog(page).getByRole("button", { name: "Open in Query" })).toBeVisible()
})

test("a read that does not come back says what it is waiting for, and a failure after a long silence says it was one", async ({
  page,
}) => {
  await holdTime(page)
  let read = held<unknown>()
  await mockOps(page, { answers: { "GET 1/tablestats": () => read.until } })
  await visit(page, "/databases/1/performance?view=tables")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  // A moment's wait is a skeleton and nothing more.
  await expect(page.getByText(/^Waiting for/)).toHaveCount(0)
  await tick(page, 5_000)
  await expect(page.getByText("Waiting for PostgreSQL to answer")).toBeVisible()
  await expect(
    page.getByText(/stands behind any session that holds an exclusive lock/),
  ).toBeVisible()

  // Half a minute later whatever stands between gives the read up.
  await tick(page, 25_000)
  const first = read
  read = held<unknown>()
  first.release(failing("Internal Server Error", 500))
  await expect(page.getByText("Could not read the tables")).toBeVisible()
  await expect(page.getByText(/The read was given up after 30s without an answer/)).toBeVisible()

  // Asked again, it is the wait again and not the old failure.
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByText("Could not read the tables")).toHaveCount(0)
  await tick(page, 5_000)
  await expect(page.getByText("Waiting for PostgreSQL to answer")).toBeVisible()

  // The way to who is in the way.
  await page.getByRole("button", { name: "See who is waiting on whom" }).click()
  await expect(page).toHaveURL(/view=locks/)
  await expect(region(page, "Locks")).toContainText("3 sessions are waiting behind one other.")
  read.release(TABLESTATS)
})

test("a refusal that came at once is not called a wait", async ({ page }) => {
  await mockOps(page, {
    answers: { "GET 1/tablestats": failing("pq: permission denied for table pg_statistic") },
  })
  await visit(page, "/databases/1/performance?view=tables")
  await expect(page.getByText("Could not read the tables")).toBeVisible()
  await expect(page.getByText(/permission denied for table pg_statistic/)).toBeVisible()
  await expect(page.getByText(/The read was given up/)).toHaveCount(0)
})

test("the keyboard goes back to where it was when a panel or a question closes", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=sessions")

  // A session's panel, opened from its row and closed with Escape.
  await sessionRow(page, "52").focus()
  await page.keyboard.press("Enter")
  await expect(dialog(page)).toContainText("Session 52")
  // The panel is in the address: a link to one session opens on it.
  await expect(page).toHaveURL(/session=52/)
  await page.keyboard.press("Escape")
  await expect(dialog(page)).toHaveCount(0)
  await expect(sessionRow(page, "52")).toBeFocused()
  await expect(page).not.toHaveURL(/session=/)

  // A confirmation refused.
  const cancel = page.getByRole("button", { name: "Cancel the statement of session 52" })
  await cancel.focus()
  await page.keyboard.press("Enter")
  await expect(dialog(page)).toContainText("Cancel statement")
  await page.keyboard.press("Escape")
  await expect(cancel).toBeFocused()

  // A table's panel, a run from a list in it, and a question from a row's menu.
  await view(page, /^Tables$/).click()
  const open = region(page, "Tables").getByRole("button", { name: "Open public.orders" })
  await open.focus()
  await page.keyboard.press("Enter")
  await expect(dialog(page)).toContainText("Sequential scans")
  const analyze = dialog(page).getByRole("button", {
    name: "Analyze: public.orders",
    exact: true,
  })
  await analyze.click()
  await expect(dialog(page).last()).toContainText("Finished")
  await dialog(page).last().getByRole("button", { name: "Close" }).first().click()
  await expect(analyze).toBeFocused()
  await page.keyboard.press("Escape")
  await expect(open).toBeFocused()

  const menu = page.getByRole("button", { name: "Maintain public.orders" })
  await menu.focus()
  await page.keyboard.press("Enter")
  await page.getByRole("menuitem", { name: "Vacuum full" }).click()
  await expect(dialog(page)).toContainText("nothing can read or write the table")
  await dialog(page).getByRole("button", { name: "Cancel" }).click()
  await expect(menu).toBeFocused()
})

test("a session by its address, and one the list does not hold", async ({ page }) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=sessions&session=40%2C77")
  await expect(dialog(page)).toContainText("Session 40,77")
  await expect(dialog(page)).toContainText("billing-worker")
  // The engine's word for what a held-up session waits on is in its panel…
  await expect(dialog(page)).toContainText("Lock:transactionid")
  await page.keyboard.press("Escape")
  // …and the list's "Waiting on" is only what a reader can act on: who it is behind.
  await expect(sessionRow(page, "40,77")).toContainText("behind")
  await expect(sessionRow(page, "52")).not.toContainText("ClientRead")

  await visit(page, "/databases/1/performance?view=sessions&session=nope")
  await expect(dialog(page)).toContainText("Not in the session list")
})

test("a session that is not waiting has nothing under Waiting on", async ({ page }) => {
  await mockOps(page, {
    answers: {
      "GET 1/activity": {
        supported: true,
        sessions: [
          session("52", "active", { seconds: 3, wait: "Timeout:PgSleep", query: "SELECT 1" }),
          session("53", "idle_in_transaction", {
            transactionSeconds: 3,
            wait: "Client:ClientRead",
          }),
        ],
      },
    },
  })
  await visit(page, "/databases/1/performance?view=sessions")
  await expect(sessionRow(page, "52")).toBeVisible()
  await expect(region(page, "Sessions")).not.toContainText("PgSleep")
  await expect(region(page, "Sessions")).not.toContainText("ClientRead")
  // It is the engine's word for what the thread is doing, and is in the panel as that.
  await sessionRow(page, "52").click()
  await expect(dialog(page).getByText("Doing", { exact: true })).toBeVisible()
  await expect(dialog(page)).toContainText("Timeout:PgSleep")
})

test("the idle sessions stand in the same columns as the ones above them", async ({ page }) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=sessions")
  const sessions = region(page, "Sessions")
  await sessions.getByRole("button", { name: /1 idle session/ }).click()
  await expect(sessionRow(page, "900")).toBeVisible()
  const lefts = (pid: string) =>
    sessionRow(page, pid).evaluate((row) =>
      [...row.querySelectorAll("td")].map((cell) => Math.round(cell.getBoundingClientRect().left)),
    )
  expect(await lefts("900")).toEqual(await lefts("52"))
  // One head, drawn once: the second run's is there for a screen reader only.
  const heads = sessions.getByRole("columnheader", { name: "State", exact: true })
  await expect(heads).toHaveCount(2)
  await expect(sessions.locator("thead").nth(1)).toHaveClass(/sr-only/)
})

test("a view is a place: Back from one is the one before", async ({ page }) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance")
  await expect(view(page, /^Overview$/)).toHaveAttribute("aria-pressed", "true")
  await view(page, /^Sessions/).click()
  await expect(page).toHaveURL(/view=sessions/)
  await view(page, /^Locks/).click()
  await expect(page).toHaveURL(/view=locks/)
  await expect(region(page, "Locks")).toBeVisible()

  await page.goBack()
  await expect(view(page, /^Sessions/)).toHaveAttribute("aria-pressed", "true")
  await expect(region(page, "Sessions")).toBeVisible()
  await page.goBack()
  await expect(view(page, /^Overview$/)).toHaveAttribute("aria-pressed", "true")
  await page.goForward()
  await expect(view(page, /^Sessions/)).toHaveAttribute("aria-pressed", "true")
  // What a view was narrowed to comes back with it.
  await region(page, "Sessions")
    .getByRole("group", { name: "Sessions by state" })
    .getByRole("button", { name: /Blocked/ })
    .click()
  await expect(page).toHaveURL(/state=blocked/)
  await view(page, /^Statements$/).click()
  await expect(page).not.toHaveURL(/state=/)
  await page.goBack()
  await expect(page).toHaveURL(/view=sessions&state=blocked|state=blocked&view=sessions/)
})

test("an address that names a schema or a table the database does not hold narrows and opens nothing", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=tables&scope=nope&object=public%2Fgone")
  const tables = region(page, "Tables")
  await expect(tables.getByRole("row")).toHaveCount(5)
  await expect(
    tables
      .getByRole("group", { name: "Narrow to one of the schemas" })
      .getByRole("button", { name: /All schemas/ }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect(dialog(page)).toHaveCount(0)
  await expect(page).not.toHaveURL(/scope=|object=/)
})

test("the engine's maintenance that could not be read is said, not left out", async ({ page }) => {
  let down = true
  await mockOps(page, {
    answers: {
      "GET 1/maintenance": () =>
        down ? failing("pq: canceling statement due to statement timeout") : POSTGRES_ACTIONS,
    },
  })
  await visit(page, "/databases/1/performance?view=tables")
  const tables = region(page, "Tables")
  await expect(tables.getByRole("row")).toHaveCount(5)
  await expect(
    tables.getByText(/The maintenance this engine offers could not be read/),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: /^Maintain/ })).toHaveCount(0)
  down = false
  await tables.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByRole("button", { name: "Maintain public.orders" })).toBeVisible()
  await expect(tables.getByText(/could not be read/)).toHaveCount(0)
})

test("a table gives up its columns one at a time as the view narrows, and is rows when it has no room", async ({
  page,
}) => {
  await mockOps(page)
  const tables = region(page, "Tables")
  const heads = () =>
    tables
      .getByRole("columnheader")
      .evaluateAll((cells) => cells.map((cell) => cell.textContent?.trim()).filter(Boolean))

  await page.setViewportSize({ width: 1720, height: 900 })
  await visit(page, "/databases/1/performance?view=tables")
  await expect(tables.getByRole("row")).toHaveCount(5)
  expect(await heads()).toEqual([
    "Table",
    "Rows",
    "Size",
    "Dead rows",
    "Unused space",
    "Sequential scans",
    "Vacuumed",
    "Analysed",
    "Actions",
  ])
  // A laptop, with the rail beside the page: what qualifies a figure goes before the figure.
  await page.setViewportSize({ width: 1152, height: 900 })
  await expect
    .poll(heads)
    .toEqual(["Table", "Rows", "Size", "Dead rows", "Vacuumed", "Analysed", "Actions"])
  await page.setViewportSize({ width: 1024, height: 900 })
  await expect.poll(heads).toEqual(["Table", "Rows", "Size", "Dead rows", "Vacuumed", "Actions"])
  // What its own column said is said on the row.
  await expect(tables.getByRole("row", { name: /import_staging/ })).toContainText("never analysed")
  // The menu is on screen at every one of them.
  const menu = page.getByRole("button", { name: "Maintain public.orders" })
  const box = await menu.boundingBox()
  expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(1024)

  // No room for a table: every table as rows, and nothing dropped.
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(tables.getByRole("table")).toHaveCount(0)
  await expect(tables.getByRole("listitem").filter({ hasText: "import_staging" })).toContainText(
    "never analysed",
  )
  await expect(menu).toBeVisible()
})

test("an analytic engine's charts leave out what it has no notion of", async ({ page }) => {
  await holdTime(page)
  await mockOps(page, AS_CLICKHOUSE)
  await visit(page, "/databases/2/performance")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await tick(page)
  const sessions = page
    .locator("[data-slot=panel]")
    .filter({ has: page.getByRole("heading", { name: "Sessions", level: 2 }) })
  await expect(sessions.getByRole("row", { name: /Open/ })).toBeVisible()
  // It has no transactions to sit idle in and no lock waits: neither is a line at zero.
  await expect(sessions).not.toContainText("Idle in a transaction")
  await expect(sessions).not.toContainText("Waiting on a lock")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
})

// ---------------------------------------------------------------------------
// Advisor
// ---------------------------------------------------------------------------

const findings = (page: Page) => region(page, "Findings")
const finding = (page: Page, title: string | RegExp) =>
  findings(page).getByRole("button", { name: title })

test("the advisor counts its findings by severity and by category, and each narrows the list", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/advisor")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  const chips = findings(page).getByRole("group", { name: "Findings by category" })
  await expect(chips.getByRole("button")).toHaveText([
    /All\s*6/,
    /Reliability\s*2/,
    /Performance\s*2/,
    /Maintenance\s*2/,
  ])
  // A row names what it is about.
  await expect(finding(page, /1 table with no primary key/)).toContainText("public.audit_log")
  await expect(finding(page, /2 foreign keys with no index/)).toContainText("2 tables")

  await page.getByRole("button", { name: "Warnings" }).click()
  await expect(page).toHaveURL(/level=warning/)
  await expect(findings(page).locator("[data-slot=accordion-item]")).toHaveCount(4)
  // The chips count what the severity leaves.
  await expect(chips.getByRole("button")).toHaveText([
    /All\s*4/,
    /Reliability\s*2/,
    /Performance\s*1/,
    /Maintenance\s*1/,
  ])
  await chips.getByRole("button", { name: /Performance/ }).click()
  await expect(page).toHaveURL(/category=performance/)
  await expect(findings(page).locator("[data-slot=accordion-item]")).toHaveCount(1)

  // What the report could not assess is said, and what the release has left.
  const rests = region(page, "What the report rests on")
  await expect(rests).toContainText("Accounts could not be assessed")
  await expect(rests).toContainText("PostgreSQL 16")
  await expect(rests).toContainText("Maintained")
  await expect(rests).toContainText("768 days from now")
})

test("a report arrives open: every finding of a short one, the worst of a long one", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/advisor")
  // Six findings: the page is what each is about and what fixes it, not a list of titles.
  const open = findings(page).locator("[data-slot=accordion-item][data-state=open]")
  await expect(open).toHaveCount(6)
  await expect(findings(page)).toContainText('CREATE INDEX "orders_customer_id_idx"')

  // A finding the reader closed stays closed, and one left open stays open,
  // when a filter that hid them is taken off again.
  await finding(page, /No backup/).click()
  await expect(open).toHaveCount(5)
  await page.getByRole("button", { name: "Notices" }).click()
  await expect(findings(page).locator("[data-slot=accordion-item]")).toHaveCount(1)
  await page.getByRole("button", { name: "Notices" }).click()
  await expect(open).toHaveCount(5)
  await expect(finding(page, /No backup/)).toHaveAttribute("aria-expanded", "false")

  // A long report opens on its worst three; the server lists them worst first.
  const many = Array.from({ length: 9 }, (_, n) =>
    advice(`check-${n}`, n === 0 ? "critical" : "warning", "performance", {
      title: `Finding number ${n}`,
    }),
  )
  await mockOps(page, { answers: { "GET 1/advisor": { ...ADVISOR, findings: many } } })
  await visit(page, "/databases/1/advisor")
  await expect(findings(page).locator("[data-slot=accordion-item]")).toHaveCount(9)
  await expect(open).toHaveCount(3)
  await expect(finding(page, "Finding number 0")).toHaveAttribute("aria-expanded", "true")
  await expect(finding(page, "Finding number 3")).toHaveAttribute("aria-expanded", "false")

  // A link to one finding opens that one.
  await visit(page, "/databases/1/advisor?finding=check-7")
  await expect(open).toHaveCount(1)
  await expect(finding(page, "Finding number 7")).toHaveAttribute("aria-expanded", "true")
})

test("a fix the server classes as safe is applied behind a confirmation that names its object", async ({
  page,
}) => {
  let fixed = false
  const ops = await mockOps(page, {
    answers: {
      "GET 1/advisor": () =>
        fixed
          ? {
              ...ADVISOR,
              findings: ADVISOR.findings.filter((f) => f.id !== "unindexed-foreign-key"),
            }
          : ADVISOR,
      "POST 1/script": () => {
        fixed = true
        return {
          statements: [
            { index: 0, sql: "", status: "ok" },
            { index: 1, sql: "", status: "ok" },
          ],
          failed: -1,
        }
      },
    },
  })
  await visit(page, "/databases/1/advisor")

  const body = findings(page)
  const about = body
    .locator("[data-slot=accordion-item]")
    .filter({ hasText: "2 foreign keys with no index" })
  await expect(
    about.getByRole("list", { name: "What it is about" }).getByRole("listitem"),
  ).toHaveCount(2)
  await expect(about).toContainText('CREATE INDEX "orders_customer_id_idx"')
  // The server was asked what it makes of the statement.
  await expect.poll(() => ops.sent.map((sent) => sent.request)).toContain("POST 1/classify")

  const apply = body.getByRole("button", { name: "Apply all 2" })
  await apply.click()
  const confirm = dialog(page)
  await expect(confirm).toContainText("2 tables")
  await expect(confirm).toContainText("Statements, run in this order")
  // It creates indexes and destroys nothing: the command wears the brand, not the red.
  const command = confirm.getByRole("button", { name: "Apply 2 statements" })
  await expect(command).not.toHaveAttribute("data-variant", "destructive")
  // An index is built alongside the table's writes unless the reader says
  // otherwise, and the statement shown is the one that will run.
  await expect(confirm.getByRole("radio", { name: "Concurrently" })).toHaveAttribute(
    "aria-checked",
    "true",
  )
  await expect(confirm).toContainText('CREATE INDEX CONCURRENTLY "order_items_product_id_idx"')
  await confirm.getByRole("radio", { name: "Blocking writes" }).click()
  await expect(confirm).toContainText("The table takes no writes until the index is built")
  await expect(confirm).toContainText('CREATE INDEX "order_items_product_id_idx"')
  await confirm.getByRole("radio", { name: "Concurrently" }).click()
  // Refused, the keyboard is back on the button that asked.
  await confirm.getByRole("button", { name: "Cancel" }).click()
  await expect(apply).toBeFocused()
  expect(ops.sent.filter((sent) => sent.request === "POST 1/script")).toEqual([])

  await apply.click()
  await dialog(page).getByRole("button", { name: "Apply 2 statements" }).click()
  await expect
    .poll(() => ops.sent.find((sent) => sent.request === "POST 1/script")?.body)
    .toEqual({
      script: (ADVISOR.findings[2].sql ?? "").replaceAll(
        "CREATE INDEX",
        "CREATE INDEX CONCURRENTLY",
      ),
    })
  // The report is read again, and the finding is gone.
  await expect(finding(page, /2 foreign keys with no index/)).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  // The button that asked went with it: the keyboard is left in the list, not on the page's body.
  await expect(findings(page)).toBeFocused()
})

test("one object's fix is applied on its own, from its row", async ({ page }) => {
  const ops = await mockOps(page)
  await visit(page, "/databases/1/advisor")
  await page.getByRole("button", { name: "Apply the fix to public.order_items" }).click()
  await expect(dialog(page)).toContainText("public.order_items")
  // The plain build is the reader's to choose, with what it costs said first.
  await dialog(page).getByRole("radio", { name: "Blocking writes" }).click()
  await dialog(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect
    .poll(() => ops.sent.find((sent) => sent.request === "POST 1/script")?.body)
    .toEqual({
      script: 'CREATE INDEX "order_items_product_id_idx" ON "public"."order_items" ("product_id");',
    })
})

test("an engine with one way of building an index is offered no choice of two", async ({
  page,
}) => {
  // The file-based engine has no concurrent build: its statement runs as the server wrote it.
  const ops = await mockOps(page, {
    answers: {
      "GET 7/advisor": {
        ...ADVISOR,
        silences: [],
        endOfLife: undefined,
        findings: [
          advice("unindexed-foreign-key", "warning", "performance", {
            title: "1 foreign key with no index",
            targets: [
              {
                kind: "table",
                name: "notes",
                detail: "notebook_id",
                sql: 'CREATE INDEX "notes_notebook_id_idx" ON "notes" ("notebook_id");',
              },
            ],
            sql: 'CREATE INDEX "notes_notebook_id_idx" ON "notes" ("notebook_id");',
          }),
        ],
      },
      "POST 7/classify": classify,
      "POST 7/script": { statements: [{ index: 0, sql: "", status: "ok" }], failed: -1 },
    },
  })
  await visit(page, "/databases/7/advisor")
  await findings(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(dialog(page)).toContainText("Building an index reads the whole table")
  await expect(dialog(page).getByRole("radio")).toHaveCount(0)
  await dialog(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect
    .poll(() => ops.sent.find((sent) => sent.request === "POST 7/script")?.body)
    .toEqual({ script: 'CREATE INDEX "notes_notebook_id_idx" ON "notes" ("notebook_id");' })
})

test("a fix the server classes as destructive is only handed to Query, and says why", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/advisor")
  const body = findings(page)
    .locator("[data-slot=accordion-item]")
    .filter({ hasText: "1 table with no primary key" })
  await expect(body).toContainText("ALTER TABLE")
  await expect(body).toContainText("it is reviewed and run on the Query page")
  await expect(body.getByRole("button", { name: /^Apply/ })).toHaveCount(0)

  await body.getByRole("button", { name: "Open in Query" }).click()
  await expect.poll(() => new URL(page.url()).pathname).toBe("/databases/1/query")
  await expect(page.locator("[data-slot=sql-editor] .view-lines")).toHaveText(
    ADVISOR.findings[3].sql!,
  )
  await expect(page.getByText("Nothing has been run in this tab", { exact: true })).toBeVisible()
})

test("a fix that is the engine's own maintenance is run as maintenance, with its output", async ({
  page,
}) => {
  const ops = await mockOps(page)
  await visit(page, "/databases/1/advisor")
  const apply = findings(page).getByRole("button", { name: "Apply: vacuum and analyze" })
  await apply.click()
  // The question names the table and the engine's action. It prints no
  // statement: the one the server runs is its own to write, and is shown,
  // with what the engine said, by the run.
  await expect(dialog(page)).toContainText("public.sessions_log")
  await expect(dialog(page)).toContainText("refreshes the planner's statistics in the same pass")
  await expect(dialog(page)).not.toContainText("VACUUM ANALYZE")
  const command = dialog(page).getByRole("button", { name: "Vacuum and analyze" })
  await expect(command).not.toHaveAttribute("data-variant", "destructive")
  await command.click()
  await expect(dialog(page)).toContainText('VACUUM (VERBOSE, ANALYZE) "public"."sessions_log"')
  await expect(dialog(page)).toContainText("tuples: 40000 removed")
  // It is not put to the classifier: the engine's list already marks it safe.
  const changes = ops.sent.filter((sent) => sent.request !== "POST 1/classify")
  expect(changes.map((sent) => sent.request)).toEqual(["POST 1/maintenance"])
  expect(changes[0].body).toEqual({
    action: "vacuum_analyze",
    schema: "public",
    table: "sessions_log",
  })
  expect(
    ops.sent
      .filter((sent) => sent.request === "POST 1/classify")
      .some((sent) => /VACUUM/.test((sent.body as { query: string }).query)),
  ).toBe(false)
  // When the run's dialog closes the keyboard is back on the button that asked.
  await dialog(page).getByRole("button", { name: "Close" }).first().click()
  await expect(apply).toBeFocused()
})

test("a finding with a page of this database to act on opens it, and no other", async ({
  page,
}) => {
  await mockOps(page, {
    answers: {
      "GET 1/advisor": {
        ...ADVISOR,
        findings: [
          ADVISOR.findings[1],
          advice("elsewhere", "warning", "security", {
            title: "Somewhere else",
            link: "/databases/2/settings",
          }),
          advice("outside", "warning", "security", {
            title: "Outside",
            link: "https://example.com/x",
          }),
        ],
      },
    },
  })
  await visit(page, "/databases/1/advisor")
  // All three arrive open: only the one that names a page of this database has a way to it.
  await expect(findings(page).getByText("What to do about elsewhere.")).toBeVisible()
  await expect(findings(page).getByText("What to do about outside.")).toBeVisible()
  await expect(findings(page).getByRole("button", { name: /^Open / })).toHaveCount(1)
  await findings(page).getByRole("button", { name: "Open Backups" }).click()
  await expect(page).toHaveURL(/\/databases\/1\/backups$/)
})

test("a report that did not cover everything says how far it got", async ({ page }) => {
  await mockOps(page, {
    answers: { "GET 1/advisor": { ...ADVISOR, truncated: true, tablesChecked: 300, findings: [] } },
  })
  await visit(page, "/databases/1/advisor")
  await expect(page.getByText("Part of this database was not checked")).toBeVisible()
  await expect(page.getByText(/the structure checks covered the first 300/)).toBeVisible()
  await expect(page.getByText(/found nothing to fix/)).toBeVisible()
})

test("no fix is applied on a protected connection, or by a role that may not", async ({ page }) => {
  const ops = await mockOps(page, { rows: { 1: { readOnly: true } } })
  await visit(page, "/databases/1/advisor")
  await expect(findings(page)).toContainText("This connection is protected")
  await expect(findings(page).getByRole("button", { name: /^Apply/ })).toHaveCount(0)
  // Nothing was asked of the classifier either.
  expect(ops.sent).toEqual([])

  await mockOps(page, { viewer: true })
  await visit(page, "/databases/1/advisor")
  await expect(findings(page)).toContainText("Your role may read the fix and not run it")
  await expect(findings(page).getByRole("button", { name: /^(Apply|Open in Query)/ })).toHaveCount(
    0,
  )
  // It can always be taken away.
  await expect(findings(page).getByRole("button", { name: "Copy" }).first()).toBeVisible()
})

test("a report that could not be read says so, and is read again on request", async ({ page }) => {
  let down = true
  await mockOps(page, {
    answers: {
      "GET 1/advisor": () => (down ? failing("pq: too many connections") : ADVISOR),
    },
  })
  await visit(page, "/databases/1/advisor")
  await expect(page.getByText("Could not read the advisor's report")).toBeVisible()
  await expect(page.getByText("pq: too many connections")).toBeVisible()
  down = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
})

test("a statement fix that fails part way says where it stopped", async ({ page }) => {
  await mockOps(page, {
    answers: {
      "POST 1/script": {
        statements: [
          { index: 0, sql: "", status: "ok" },
          {
            index: 1,
            sql: "",
            status: "error",
            error: 'pq: relation "order_items_product_id_idx" already exists',
          },
        ],
        failed: 1,
      },
    },
  })
  await visit(page, "/databases/1/advisor")
  await findings(page).getByRole("button", { name: "Apply all 2" }).click()
  await dialog(page).getByRole("button", { name: "Apply 2 statements" }).click()
  await expect(page.getByText("The fix stopped after 1 statement")).toBeVisible()
  await expect(page.getByText(/already exists/)).toBeVisible()
})

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

const ENTRY = {
  at: "2026-09-27T10:01:03.221Z",
  durationMs: 1843.2,
  query: "SELECT o.id, o.total FROM orders o JOIN customers c ON c.id = o.customer_id",
  user: "app",
  db: "shop",
}

test("the queries are named in the engine's words, from the registry and from where they were read", async ({
  page,
}) => {
  // A server whose log is not on this machine is its queries alone.
  await mockOps(page, {
    queryLog: {
      supported: true,
      source: "log",
      entries: [ENTRY],
      truncated: false,
      threshold: "250ms",
    },
  })
  await visit(page, "/databases/1/logs")
  await expect(page.getByText("Slow statements", { exact: true })).toBeVisible()
  await expect(page.getByText(/its log is not here/)).toBeVisible()
  await expect(page.getByText("1 statement", { exact: true })).toBeVisible()
  // The engine keeps statement statistics: the third reading is offered.
  await page.getByRole("button", { name: "Top statements" }).click()
  await expect(region(page, "Top statements").locator("[data-slot=bar-list] button")).toHaveCount(3)

  // A list read from the server's own query table is a list of queries.
  await mockOps(page, {
    queryLog: { supported: true, source: "slow_log", entries: [ENTRY], truncated: false },
  })
  await visit(page, "/databases/2/logs")
  await expect(page.getByText("Slow queries", { exact: true })).toBeVisible()
  await expect(page.getByText("1 query", { exact: true })).toBeVisible()
})

test("a file has no server keeping a log: its list is what was run from here", async ({ page }) => {
  const ops = await mockOps(page, {
    answers: {
      "GET 7/history": [
        {
          id: 1,
          sql: "SELECT * FROM notes",
          risk: "safe",
          success: true,
          durationMs: 3,
          rowCount: 12,
          ranAt: NOW,
        },
      ],
    },
  })
  await visit(page, "/databases/7/logs")
  await expect(page.getByText("Statements run from here", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: /SELECT \* FROM notes/ })).toBeVisible()
  await expect(page.getByText("run from this dashboard")).toBeVisible()
  // It keeps no statement statistics and has no server log to ask.
  await expect(page.getByRole("button", { name: "Top statements" })).toHaveCount(0)
  expect(ops.seen.filter((request) => /\/(querylog|statements)$/.test(request))).toEqual([])
})

test("a list of queries that could not be read says so, with the way to try again", async ({
  page,
}) => {
  let down = true
  await mockOps(page, {
    answers: {
      "GET 1/querylog": () =>
        down
          ? failing("pq: could not open the server log")
          : { supported: true, source: "log", entries: [ENTRY], truncated: false },
    },
  })
  await visit(page, "/databases/1/logs")
  await expect(page.getByText("Could not read the statements")).toBeVisible()
  down = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByRole("button", { name: /SELECT o\.id, o\.total/ })).toBeVisible()
})

// ---------------------------------------------------------------------------
// The design system's rules
// ---------------------------------------------------------------------------

/** Icon-only controls with nothing for a screen reader to call them. */
function unnamedControls(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
      if (el.offsetParent === null && el.getAttribute("aria-hidden") !== "true") continue
      const text = (el.textContent ?? "").trim()
      if (text.length > 0) continue
      const named =
        el.getAttribute("aria-label") ||
        el.getAttribute("aria-labelledby") ||
        el.querySelector(".sr-only")
      if (!named) bad.push(el.outerHTML.slice(0, 160))
    }
    return bad
  })
}

/** Fully rounded, filled labels; a person's avatar is the one identity exception. */
function filledPills(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
      if (el.dataset.slot === "user-avatar") continue
      const s = getComputedStyle(el)
      const r = parseFloat(s.borderTopLeftRadius)
      const h = el.getBoundingClientRect().height
      if (!h || h > 32 || r < h / 2) continue
      const filled = s.backgroundColor !== "rgba(0, 0, 0, 0)" && s.backgroundColor !== "transparent"
      const text = (el.textContent ?? "").trim()
      if (filled && text.length > 0) bad.push(el.outerHTML.slice(0, 140))
    }
    return bad
  })
}

/** Text a centred row sets off its own centre line. */
function offCentreText(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const item of document.querySelectorAll<HTMLElement>("body *")) {
      const row = item.parentElement
      if (!row) continue
      const rowStyle = getComputedStyle(row)
      if (!rowStyle.display.endsWith("flex") || !rowStyle.flexDirection.startsWith("row")) continue
      const style = getComputedStyle(item)
      const align = ["auto", "normal"].includes(style.alignSelf)
        ? rowStyle.alignItems
        : style.alignSelf
      if (align !== "center" || style.display !== "block" || style.position === "absolute") continue
      const box = item.getBoundingClientRect()
      if (box.height < 2 || box.width < 2) continue
      const blocks = [...item.children].some((child) => {
        const display = getComputedStyle(child).display
        return display !== "none" && !display.startsWith("inline")
      })
      if (blocks) continue
      const range = document.createRange()
      range.selectNodeContents(item)
      const ink = [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0)
      if (ink.length === 0) continue
      const top = Math.min(...ink.map((r) => r.top))
      const bottom = Math.max(...ink.map((r) => r.bottom))
      const offset = (top + bottom) / 2 - (box.top + box.bottom) / 2
      if (Math.abs(offset) >= 1.5) {
        bad.push(`${offset.toFixed(1)}px ${item.outerHTML.slice(0, 140)}`)
      }
    }
    return bad
  })
}

async function keepsTheRules(page: Page, what: string, overlay = false) {
  expect(await unnamedControls(page), `unlabelled icon-only controls on ${what}`).toEqual([])
  expect(await offCentreText(page), `text off its row's centre line on ${what}`).toEqual([])
  expect(await filledPills(page), `fully rounded filled chips on ${what}`).toEqual([])
  const registers = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("[data-slot='page']")].map(
      (el) => el.dataset.register ?? "(unset)",
    ),
  )
  expect(registers, `${what} declares one reading page`).toEqual(["reading"])
  expect(
    await page.locator("[data-slot='flow-panel']").count(),
    `${what} has no foreground surface`,
  ).toBe(0)
  const sideways = await page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways, `${what} scrolls sideways`).toBe(false)
  // A table is as wide as the view it is in: none is cut off behind a
  // scroll of its own, at whatever width the rail leaves the page.
  const clipped = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("[data-slot=page] [data-slot=table-container]")]
      .filter((el) => el.scrollWidth > el.clientWidth + 1)
      .map((el) => `${el.scrollWidth} in ${el.clientWidth}: ${el.textContent?.slice(0, 60)}`),
  )
  expect(clipped, `tables wider than ${what}`).toEqual([])
  // A sheet drawn over the page takes the page out of the accessibility tree.
  if (overlay) return
  // The page's name is said once, to assistive technology; the rail shows where this is.
  const names = page.locator("[data-slot=page]").getByRole("heading", { level: 1 })
  await expect(names).toHaveCount(1)
  await expect(names).toHaveClass(/sr-only/)
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 900 }],
  [" at a wide window", { width: 1720, height: 1000 }],
  // The widths of an ordinary laptop, where the rail takes a quarter of the window.
  [" at 1152 wide", { width: 1152, height: 900 }],
  [" at 1024 wide", { width: 1024, height: 900 }],
  [" at 900 wide", { width: 900, height: 900 }],
  [" at a tablet's width", { width: 820, height: 1000 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`every view of Performance keeps the design system's rules${label}`, async ({ page }) => {
    test.setTimeout(120_000)
    await page.setViewportSize(viewport)
    await mockOps(page, {
      rows: { 1: { environment: "production-eu-west" } },
      ...AS_CLICKHOUSE,
    })
    for (const name of [
      "",
      "sessions",
      "locks",
      "statements",
      "tables",
      "indexes",
      "replication",
    ]) {
      await visit(page, `/databases/1/performance${name ? `?view=${name}` : ""}`)
      await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
      await page.waitForLoadState("networkidle")
      // What a view folds away is part of it: the lock table, the idle sessions.
      for (const fold of await page
        .locator("[data-slot=page] details:not([open]) > summary")
        .all()) {
        await fold.click()
      }
      await keepsTheRules(page, `the ${name || "overview"} view`)
    }
    await visit(page, "/databases/7/performance")
    await expect(region(page, "Maintenance").getByRole("button").first()).toBeVisible()
    await keepsTheRules(page, "a file's performance page")
    for (const name of ["", "sessions", "parts", "merges"]) {
      await visit(page, `/databases/2/performance${name ? `?view=${name}` : ""}`)
      await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
      await page.waitForLoadState("networkidle")
      for (const fold of await page
        .locator("[data-slot=page] details:not([open]) > summary")
        .all()) {
        await fold.click()
      }
      await keepsTheRules(page, `an analytic engine's ${name || "overview"} view`)
    }

    // With the sheets open over their lists.
    await visit(page, "/databases/1/performance?view=sessions")
    await page.getByRole("button", { name: "Terminate session 12,3301" }).first().click()
    await expect(dialog(page)).toContainText("Terminate session")
    await keepsTheRules(page, "the sessions with a confirmation open", true)
    await visit(page, "/databases/1/performance?view=tables&object=public%2Forders")
    await expect(dialog(page)).toContainText("Maintenance")
    await keepsTheRules(page, "a table's panel", true)
    await visit(page, "/databases/1/performance?view=statements&statement=s2")
    await expect(dialog(page)).toContainText("Of runtime")
    await expect(dialog(page).getByRole("textbox")).toHaveCount(2)
    await keepsTheRules(page, "a statement's panel", true)
    await visit(page, "/databases/1/performance?view=sessions&session=40%2C77")
    await expect(dialog(page)).toContainText("Session 40,77")
    await keepsTheRules(page, "a session's panel", true)
  })

  test(`the Advisor and what could not be read keep the rules${label}`, async ({ page }) => {
    test.setTimeout(90_000)
    await page.setViewportSize(viewport)
    await mockOps(page)
    await visit(page, "/databases/1/advisor")
    await expect(findings(page).getByRole("button", { name: "Open Backups" })).toBeVisible()
    await expect(findings(page).getByRole("button", { name: "Apply all 2" })).toBeVisible()
    await page.waitForLoadState("networkidle")
    await keepsTheRules(page, "the advisor with findings open")
    await findings(page).getByRole("button", { name: "Apply all 2" }).click()
    await expect(dialog(page)).toContainText("Statements, run in this order")
    await keepsTheRules(page, "the advisor's question before a fix", true)
    await page.keyboard.press("Escape")

    await visit(page, "/databases/7/advisor")
    await expect(page.getByText(/found nothing to fix/)).toBeVisible()
    await keepsTheRules(page, "an advisor with nothing to say")

    const failure = failing("pq: canceling statement due to statement timeout")
    await mockOps(page, {
      summaries: { 2: { state: "stopped", ok: false, latencyMs: 0 } },
      answers: Object.fromEntries(
        [
          "stats",
          "activity",
          "locks",
          "statements",
          "tablestats",
          "indexstats",
          "replication",
          "advisor",
        ].map((name) => [`GET 1/${name}`, failure]),
      ),
    })
    for (const name of ["sessions", "locks", "statements", "tables", "indexes", "replication"]) {
      await visit(page, `/databases/1/performance?view=${name}`)
      await expect(page.getByText(/^Could not read the (?!server)/)).toBeVisible()
      await keepsTheRules(page, `the ${name} view that could not be read`)
    }
    await visit(page, "/databases/1/advisor")
    await expect(page.getByText("Could not read the advisor's report")).toBeVisible()
    await keepsTheRules(page, "an advisor that could not be read")
    await visit(page, "/databases/2/performance")
    await expect(page.getByText("blog is stopped")).toBeVisible()
    await keepsTheRules(page, "a stopped server's performance page")

    await visit(page, "/databases/7/logs")
    await expect(page.getByText("Statements run from here", { exact: true })).toBeVisible()
    await keepsTheRules(page, "a file's queries")
  })
}
