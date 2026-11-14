import { expect as baseExpect, test, type Page } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"
import { risen } from "./risen"

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

// ---- the statement history ------------------------------------------------

const MINUTE = 60_000
const iso = (ms: number) => new Date(ms).toISOString()

/** Where each point of a window ending now ends, cut as the server cuts it: on whole multiples of its width. */
function windowBuckets(now: number, hours: number, seconds: number) {
  const width = seconds * 1000
  const first = Math.floor((now - hours * 3_600_000) / width)
  const last = Math.floor(now / width)
  return Array.from({ length: last - first + 1 }, (_, index) =>
    Math.min((first + index + 1) * width, now),
  )
}

type Recorded = {
  id: string
  query: string
  calls: number
  totalMs: number
  rows: number
  rowsRead?: number
  p50Ms?: number
  p95Ms?: number
  p99Ms?: number
  /** Its time across the window, 0 to 1 of the way through. */
  curve: (x: number) => number
}

/** A web shop's PostgreSQL over the last hour: one lookup is half the work, and a stock update has just begun to cost. */
const SHOP_HISTORY: Recorded[] = [
  {
    id: "-4520113301",
    query:
      "SELECT o.id, o.total, o.status FROM orders o WHERE o.customer_id = $1 ORDER BY o.created_at DESC LIMIT $2",
    calls: 18_412,
    totalMs: 22_094,
    rows: 183_920,
    curve: (x) => 0.6 + 0.8 * x,
  },
  {
    id: "7731904",
    query: "UPDATE inventory SET reserved = reserved + $1 WHERE sku = $2",
    calls: 2_911,
    totalMs: 12_540,
    rows: 2_911,
    curve: (x) => (x > 0.7 ? 2.6 : 0.35),
  },
  {
    id: "193377",
    query: "INSERT INTO page_views (path, referrer, viewed_at) VALUES ($1, $2, $3)",
    calls: 40_220,
    totalMs: 6_830,
    rows: 40_220,
    curve: (x) => 1 + 0.25 * Math.sin(x * 12),
  },
  {
    id: "-88120045",
    query: "SELECT count(*) FROM order_items WHERE order_id = ANY($1)",
    calls: 96,
    totalMs: 4_420,
    rows: 96,
    curve: (x) => (Math.round(x * 10) % 3 === 0 ? 1.8 : 0.3),
  },
  {
    id: "5501",
    query: "DELETE FROM sessions WHERE expires_at < $1",
    calls: 12,
    totalMs: 1_310,
    rows: 4_380,
    curve: (x) => (Math.round(x * 12) % 4 === 0 ? 1 : 0),
  },
]

/** An analytics ClickHouse: real run times, so percentiles, and the rows each query read. */
const ANALYTICS_HISTORY: Recorded[] = [
  {
    id: "14227355820374926633",
    query:
      "SELECT path, count() AS views FROM page_views WHERE viewed_at > now() - INTERVAL ? HOUR GROUP BY path ORDER BY views DESC LIMIT ?",
    calls: 1_204,
    totalMs: 96_320,
    rows: 120_400,
    rowsRead: 4_816_000_000,
    p50Ms: 61,
    p95Ms: 212,
    p99Ms: 487,
    curve: (x) => 0.8 + 0.4 * x,
  },
  {
    id: "3390412281605321",
    query: "INSERT INTO page_views (path, referrer, viewed_at, session_id) VALUES",
    calls: 3_610,
    totalMs: 21_660,
    rows: 0,
    rowsRead: 0,
    p50Ms: 5,
    p95Ms: 13,
    p99Ms: 22.6,
    curve: () => 1,
  },
  {
    id: "902211873340115",
    query: "SELECT uniqExact(session_id) FROM events WHERE event_date = today() AND kind = ?",
    calls: 58,
    totalMs: 14_790,
    rows: 58,
    rowsRead: 812_000_000,
    p50Ms: 241,
    p95Ms: 402,
    p99Ms: 431,
    curve: (x) => (x > 0.5 ? 1.5 : 0.5),
  },
]

const historyBucket = (hours: number) =>
  hours === 1 ? 300 : hours === 6 ? 900 : hours === 24 ? 3600 : 25_200
const chartBucket = (hours: number) => (hours <= 6 ? 300 : hours === 24 ? 600 : 3600)
/** A longer window holds more of the same work. */
const scaleOf = (hours: number) => (hours === 1 ? 1 : hours * 0.8)

function historyFacts(hours: number, bucketSeconds: number, recorded: Recorded[], now: number) {
  const scale = scaleOf(hours)
  const intervals = hours * 12 - 1
  return {
    supported: true,
    hours,
    everySeconds: 300,
    retentionHours: 168,
    keptPerInterval: 50,
    bucketSeconds,
    from: iso(now - hours * 3_600_000),
    until: iso(now),
    intervals,
    recordedSeconds: intervals * 300,
    firstAt: iso(now - (hours * 60 - 3) * MINUTE),
    totalMs: Math.round(recorded.reduce((sum, r) => sum + r.totalMs, 0) * 1.02 * scale),
    rowsRead: recorded.some((r) => r.rowsRead !== undefined),
    percentiles: recorded.some((r) => r.p95Ms !== undefined),
    resets: [],
    reading: { at: iso(now - 2 * MINUTE - 13_000), supported: true },
    nextAt: iso(now + 2 * MINUTE + 47_000),
  }
}

function historyEntry(r: Recorded, hours: number, total: number) {
  const scale = scaleOf(hours)
  const { curve, ...figures } = r
  void curve
  return {
    ...figures,
    calls: Math.round(r.calls * scale),
    totalMs: r.totalMs * scale,
    meanMs: r.totalMs / r.calls,
    rows: Math.round(r.rows * scale),
    ...(r.rowsRead === undefined ? {} : { rowsRead: Math.round(r.rowsRead * scale) }),
    share: (r.totalMs * scale) / total,
  }
}

const HISTORY_ORDER: Record<string, (r: ReturnType<typeof historyEntry>) => number> = {
  total: (r) => r.totalMs,
  calls: (r) => r.calls,
  mean: (r) => r.meanMs,
  rows: (r) => r.rows,
  rowsRead: (r) => r.rowsRead ?? -1,
  p95: (r) => r.p95Ms ?? -1,
}

/** `GET /{id}/statements/history`, ordered as asked, over the window asked for. */
const statementHistory =
  (recorded: Recorded[], over: Record<string, unknown> = {}) =>
  (_call: number, url: URL) => {
    const now = Date.now()
    const hours = Number(url.searchParams.get("hours") ?? 1)
    const sort = url.searchParams.get("sort") ?? "total"
    const facts = historyFacts(hours, historyBucket(hours), recorded, now)
    const buckets = windowBuckets(now, hours, facts.bucketSeconds)
    const statements = recorded
      .map((r) => ({
        ...historyEntry(r, hours, facts.totalMs),
        trend: buckets.map((_, index) =>
          index === 0
            ? null
            : Math.round(
                ((r.totalMs * scaleOf(hours)) / buckets.length) * r.curve(index / buckets.length),
              ),
        ),
      }))
      .sort((a, b) => HISTORY_ORDER[sort](b) - HISTORY_ORDER[sort](a))
    return { ...facts, sort, buckets, statements, ...over }
  }

/**
 * `GET /{id}/statements/history/{statement}`: its points, the first before
 * recording began. `gap` is a point a third of the way through that no
 * reading fell in, and `reset` the counters started again two thirds of the
 * way through.
 */
const statementHistoryOf =
  (recorded: Recorded[], r: Recorded, marks: { gap?: boolean; reset?: boolean } = {}) =>
  (_call: number, url: URL) => {
    const now = Date.now()
    const hours = Number(url.searchParams.get("hours") ?? 1)
    const facts = historyFacts(hours, chartBucket(hours), recorded, now)
    const at = windowBuckets(now, hours, facts.bucketSeconds)
    const entry = historyEntry(r, hours, facts.totalMs)
    const gap = marks.gap ? Math.round(at.length / 3) : -1
    const resets = marks.reset ? [iso(at[Math.round((at.length * 2) / 3)])] : []
    const points = at.map((ts, index) => {
      if (index === 0 || index === gap)
        return { at: ts, recorded: false, calls: 0, totalMs: 0, rows: 0 }
      const weight = r.curve(index / at.length) / at.length
      const calls = Math.max(1, Math.round(entry.calls * weight))
      const totalMs = entry.totalMs * weight
      return {
        at: ts,
        recorded: true,
        calls,
        totalMs,
        meanMs: totalMs / calls,
        rows: Math.round(entry.rows * weight),
        ...(r.rowsRead === undefined
          ? {}
          : { rowsRead: Math.round((entry.rowsRead ?? 0) * weight) }),
        ...(r.p95Ms === undefined
          ? {}
          : {
              p50Ms: r.p50Ms! * (0.9 + 0.2 * weight * at.length),
              p95Ms: r.p95Ms * (0.9 + 0.2 * weight * at.length),
              p99Ms: r.p99Ms! * (0.9 + 0.2 * weight * at.length),
            }),
      }
    })
    return { ...facts, resets, statement: entry, points }
  }

const NO_HISTORY_YET = (_call: number, url: URL) => ({
  ...statementHistory(SHOP_HISTORY)(0, url),
  intervals: 0,
  recordedSeconds: 0,
  firstAt: undefined,
  totalMs: 0,
  statements: [],
})

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
  "GET 1/statements/history": statementHistory(SHOP_HISTORY),
  ...Object.fromEntries(
    SHOP_HISTORY.map((r, index) => [
      `GET 1/statements/history/${r.id}`,
      // The stock update's history has a reading missed and a reset in it.
      statementHistoryOf(SHOP_HISTORY, r, index === 1 ? { gap: true, reset: true } : {}),
    ]),
  ),
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
  "GET 2/statements/history": statementHistory(ANALYTICS_HISTORY),
  ...Object.fromEntries(
    ANALYTICS_HISTORY.map((r) => [
      `GET 2/statements/history/${r.id}`,
      statementHistoryOf(ANALYTICS_HISTORY, r),
    ]),
  ),
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
/** The identity line a page of one thing opens on: "About session 52". */
const about = (page: Page, name: string) => page.getByRole("region", { name, exact: true })
const aboutStatement = (page: Page) => about(page, "About this statement")
/** The open view of such a page, and the strip that picks it. */
const detailView = (page: Page) => page.locator("[data-slot=detail-view]")
const tab = (page: Page, name: string | RegExp) => page.getByRole("tab", { name })
/** The list a page was opened from, folded beside it from `xl`. */
const rail = (page: Page, name: string) => page.getByRole("navigation", { name, exact: true })
/** The way back to the list, over the identity line. */
const back = (page: Page, name: string) =>
  page.locator("[data-slot=page-context]").getByRole("link", { name, exact: true })
/** What a fact on a title line explains, read from its tooltip. */
async function explained(page: Page, scope: ReturnType<typeof region>, label: string) {
  await scope.getByRole("button", { name: label, exact: true }).hover()
  return page.getByRole("tooltip")
}

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
    /^Statement history$/,
    /^Tables$/,
    /^Indexes$/,
    /^Replication$/,
  ])
  await expect(view(page, /^Overview$/)).toHaveAttribute("aria-pressed", "true")
  // The charts show saved activity as soon as the page opens.
  await expect(region(page, "Activity")).toContainText(/recorded, kept for 7 days/)
  await expect(page.getByRole("heading", { name: "Sessions", level: 2 })).toBeVisible()
  // A chart with nothing above zero in the window is named, not drawn flat.
  await expect(page.getByText("Lock waits and deadlocks stayed at zero.")).toBeVisible()
  await expect(page.getByRole("heading", { name: "Lock waits and deadlocks" })).toHaveCount(0)
  // The snapshot and the session list are the page's, and what uses the database, which draws
  // each client as itself; a view's own read waits for the view.
  expect(
    ops.seen.filter((request) => !/\/(stats|stats\/history|activity|consumers)$/.test(request)),
  ).toEqual([])
})

test("the Overview draws who is connected right now as their programs, and a state opens its sessions", async ({
  page,
}) => {
  await mockOps(page, {
    answers: {
      "GET 1/consumers": {
        checkedAt: NOW,
        nodes: [
          { id: "db:1", kind: "database", name: "shop", product: "postgres", connId: 1 },
          {
            id: "deploy:7",
            kind: "deployment",
            name: "checkout-api",
            product: "nodejs",
            href: "/deploy/7",
          },
        ],
        edges: [
          { from: "db:1", to: "deploy:7", via: ["binding"], sessions: 1, status: "connected" },
        ],
      },
    },
  })
  await visit(page, "/databases/1/performance")
  const now = region(page, "Right now")
  const clients = now.getByRole("list", { name: "Connected" }).getByRole("listitem")
  // What is wrong first — a session stuck behind a lock, then one holding a transaction open —
  // then the busiest, and the dashboard's own connection last, with its pool.
  await expect(clients).toHaveCount(5)
  await expect(clients.nth(0)).toContainText("billing-worker")
  await expect(clients.nth(0)).toContainText("1 waiting on a lock")
  await expect(clients.nth(1)).toContainText("Deployment")
  await expect(clients.nth(1).getByRole("link", { name: "Open checkout-api" })).toHaveAttribute(
    "href",
    "/deploy/7",
  )
  await expect(clients.nth(1)).toContainText("1 in a transaction")
  await expect(clients.nth(2)).toContainText("reports")
  await expect(clients.nth(4)).toContainText("This dashboard")
  await expect(clients.nth(4)).toContainText("1 of 5 pooled open")
  // The four states, each counted, and the dashboard's own read is none of them.
  const doing = now.getByRole("list", { name: "Doing" }).getByRole("link")
  await expect(doing).toHaveText([
    /1\s*Running a statement/,
    /1\s*Waiting on a lock/,
    /1\s*Holding a transaction/,
    /1\s*Connected, idle/,
  ])
  await now.getByRole("link", { name: "1 session waiting on a lock: open them" }).click()
  await expect(view(page, /^Sessions/)).toHaveAttribute("aria-pressed", "true")
  await expect(page).toHaveURL(/state=blocked/)
  await expect(sessionRow(page, "40,77")).toBeVisible()
  await expect(sessionRow(page, "52")).toHaveCount(0)
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

  // The blocker is a page of its own, and its id is one segment of the address.
  await expect(page).toHaveURL(/\/performance\/sessions\/12~2C3301$/)
  const blocker = about(page, "About session 12,3301")
  await expect(blocker).toContainText("Session 12,3301")
  await expect(blocker).toContainText("checkout-api")
  await expect(detailView(page).getByText("Transaction open for")).toBeVisible()
  // It is idle inside a transaction: there is no statement to cancel.
  await expect(blocker.getByRole("button", { name: "Terminate session" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Cancel statement" })).toHaveCount(0)
  // And from the blocker to the one it holds, on the view that says who waits on whom.
  await tab(page, /^Waits/).click()
  await expect(page).toHaveURL(/tab=waits/)
  await detailView(page).getByRole("link", { name: "Open session 40,77" }).click()
  await expect(about(page, "About session 40,77")).toContainText("Session 40,77")
  await expect(detailView(page).getByRole("link", { name: "Open session 12,3301" })).toBeVisible()
  await tab(page, "Activity").click()
  await expect(detailView(page).getByText("Waiting for")).toBeVisible()
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
  await expect(aboutStatement(page)).toContainText("30% of runtime")
  await tab(page, "Text").click()
  await expect(detailView(page).locator("[data-slot=code-view]")).toBeVisible()
  await expect(page.getByRole("button", { name: "Open in Query" })).toHaveCount(0)
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

test("statements are ranked by their share, ordered by the server, and one opens on its page with its plan", async ({
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

  // A row opens the statement's own page, and the order the list was in goes with it.
  await rows.nth(1).click()
  await expect(page).toHaveURL(/\/performance\/statements\/s1\?sort=mean$/)
  await expect(aboutStatement(page)).toContainText("SELECT * FROM orders WHERE customer_id = $1")
  await expect(aboutStatement(page)).toContainText("60% of runtime")
  await expect(aboutStatement(page)).toContainText("50% of its reads miss the cache")
  // Its figures are readings, not a strip of tiles.
  await expect(detailView(page).locator("[data-slot=reading-line]")).toHaveCount(4)
  await expect(detailView(page)).toContainText("60% of all statement time")
  await expect(detailView(page)).toContainText("24 ms at its slowest")
  await expect(page.locator("[data-slot=metric-strip], [data-slot=stat-grid]")).toHaveCount(0)

  // A shape is planned with values put where its placeholders stand: one
  // field for each, named by the words before it.
  await tab(page, "Plan").click()
  await expect(page).toHaveURL(/tab=plan/)
  const plan = detailView(page)
  await expect(plan).toContainText("an engine plans values")
  const value = plan.getByRole("textbox", { name: "The value for $1, after customer_id =" })
  await value.fill("42")
  await plan.getByRole("button", { name: "Explain with these values" }).click()
  await expect(plan).toContainText("Seq Scan on order_items")
  expect(ops.sent.find((sent) => sent.request === "POST 1/explain")?.body).toEqual({
    query: "SELECT *\n  FROM orders\n WHERE customer_id = 42",
    format: "text",
  })
  // The way back is the list, in the order it was left in.
  await back(page, "Statements").click()
  await expect(page).toHaveURL(/view=statements/)
  await expect(page).toHaveURL(/sort=mean/)
  ops.sent.length = 0

  // One with no values in it is planned as it stands. Nothing is executed.
  await rows.nth(3).click()
  await tab(page, "Plan").click()
  await detailView(page).getByRole("button", { name: "Explain", exact: true }).click()
  await expect(detailView(page)).toContainText("Seq Scan on order_items")
  expect(ops.sent.find((sent) => sent.request === "POST 1/explain")?.body).toEqual({
    query: "SELECT count(*) FROM order_items",
    format: "text",
  })

  await aboutStatement(page).getByRole("button", { name: "Open in Query" }).click()
  await expect(page).toHaveURL(/\/databases\/1\/query\?/)
  expect(new URL(page.url()).searchParams.get("sql")).toBe("SELECT count(*) FROM order_items")
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

// ---- a PostgreSQL server's statements, as shapes ------------------------------

const SHAPES_TOTAL_MS = 842_000
const shaped = (
  id: string,
  query: string,
  totalMs: number,
  calls: number,
  extra: Record<string, unknown> = {},
) =>
  statement(id, query, totalMs / SHAPES_TOTAL_MS, {
    totalMs,
    calls,
    meanMs: totalMs / calls,
    maxMs: calls === 1 ? totalMs : (totalMs / calls) * 6,
    rows: 0,
    hitRatio: -1,
    ...extra,
  })

/**
 * What the list holds of a PostgreSQL server's statements: the extension's
 * `$n` where it took a value out of a statement it planned, and a `?` wherever
 * the backend took one out of a statement the extension keeps as it was sent
 * — a role's password, a setting, a comment.
 */
const PG_SHAPES = {
  ...STATEMENTS,
  totalMs: SHAPES_TOTAL_MS,
  statements: [
    shaped(
      "p1",
      "SELECT id, total\n  FROM orders\n WHERE customer_id = $1\n ORDER BY ?",
      438_000,
      18_400,
      {
        rows: 220_800,
        hitRatio: 0.97,
      },
    ),
    shaped("p2", "UPDATE carts SET total = $1, updated_at = now() WHERE id = $2", 227_000, 6_120, {
      rows: 6_120,
      hitRatio: 0.99,
    }),
    shaped("p5", "SET statement_timeout = ?", 101_000, 24_800),
    shaped("p4", "ALTER ROLE app_reader IN DATABASE shop_main SET search_path = ?, ?", 42_000, 3),
    shaped(
      "p3",
      "CREATE ROLE app_reader WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD ?",
      25_000,
      1,
    ),
    shaped("p6", "COMMENT ON TABLE orders IS ?", 9_000, 1),
  ],
}

const shapeRow = (page: Page, text: RegExp) =>
  region(page, "Statements").getByRole("row", { name: text })

/** What the engine plans the first of those, with its values put back, as. */
const ORDERS_PLAN = {
  result: {
    columns: ["QUERY PLAN"],
    rows: [
      ["Sort  (cost=48.21..48.29 rows=31 width=16)"],
      ["  Sort Key: total"],
      [
        "  ->  Index Scan using orders_customer_id_idx on orders  (cost=0.29..47.44 rows=31 width=16)",
      ],
      ["        Index Cond: (customer_id = 42)"],
    ],
  },
  format: "text",
  analyzed: false,
  rolledBack: false,
}

/** A statement's page on its Text view, once its editor holds the text. */
async function openedStatement(page: Page, text: string) {
  await expect(aboutStatement(page)).toBeVisible()
  await tab(page, "Text").click()
  const view = detailView(page)
  await expect(view.locator("[data-slot=code-view]")).toContainText(text, { timeout: 15_000 })
  return view
}

test("a PostgreSQL statement is listed as its shape, with a ? where a password or a setting was", async ({
  page,
}) => {
  const ops = await mockOps(page, {
    answers: { "GET 1/statements": PG_SHAPES, "POST 1/explain": ORDERS_PLAN },
  })
  await visit(page, "/databases/1/performance?view=statements")
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(7)
  await expect(shapeRow(page, /CREATE ROLE app_reader/)).toContainText("NOCREATEROLE PASSWORD ?")
  await expect(shapeRow(page, /SET statement_timeout/)).toContainText("SET statement_timeout = ?")
  // What a shape is was a paragraph under the table: it is said by the fact on the title line.
  await expect(
    await explained(page, region(page, "Statements"), "What a statement's text is"),
  ).toContainText("every value in it is replaced before it leaves the server")

  // A statement the extension kept whole is still a shape, and has no plan.
  await shapeRow(page, /CREATE ROLE app_reader/).click()
  const text = await openedStatement(page, "NOCREATEROLE PASSWORD ?")
  await expect(text).toContainText("The statement, as a shape")
  await tab(page, "Plan").click()
  const plan = detailView(page)
  await expect(plan).toContainText("Only a statement that reads or changes rows has a plan")
  await expect(plan.getByRole("textbox")).toHaveCount(0)
  // A view is not a place Back steps through: Back from the page is the list.
  await page.goBack()
  await expect(page).toHaveURL(/view=statements/)

  // One that was planned asks for the extension's $1 and for the ? beside it.
  await shapeRow(page, /SELECT id, total/).click()
  await openedStatement(page, "customer_id = $1")
  await tab(page, "Plan").click()
  await plan.getByRole("textbox", { name: "The value for $1, after customer_id =" }).fill("42")
  await plan.getByRole("textbox", { name: "The value for ?, after ORDER BY" }).fill("2")
  await plan.getByRole("button", { name: "Explain with these values" }).click()
  await expect(plan).toContainText("Index Scan using orders_customer_id_idx on orders")
  expect(ops.sent.find((sent) => sent.request === "POST 1/explain")?.body).toEqual({
    query: "SELECT id, total\n  FROM orders\n WHERE customer_id = 42\n ORDER BY 2",
    format: "text",
  })
})

test("an account that may only read is shown the shapes and offered nothing that runs one", async ({
  page,
}) => {
  await mockOps(page, { viewer: true, answers: { "GET 1/statements": PG_SHAPES } })
  await visit(page, "/databases/1/performance?view=statements")
  await expect(shapeRow(page, /CREATE ROLE app_reader/)).toContainText("PASSWORD ?")
  await expect(page.getByRole("button", { name: "Reset statistics" })).toHaveCount(0)
  await shapeRow(page, /ALTER ROLE app_reader/).click()
  const text = await openedStatement(page, "SET search_path = ?, ?")
  await expect(text).toContainText("The statement, as a shape")
  await expect(page.getByRole("button", { name: "Open in Query" })).toHaveCount(0)
})

test("a PostgreSQL list with nothing counted yet says so", async ({ page }) => {
  await mockOps(page, {
    answers: { "GET 1/statements": { ...PG_SHAPES, totalMs: 0, statements: [] } },
  })
  await visit(page, "/databases/1/performance?view=statements")
  await expect(
    page.getByText("No statement has been counted yet. The list fills as they run."),
  ).toBeVisible()
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(0)
})

test("a PostgreSQL list that cannot be read says why, and can be asked for again", async ({
  page,
}) => {
  let refused = true
  await mockOps(page, {
    answers: {
      "GET 1/statements": () =>
        refused ? failing("pq: permission denied for view pg_stat_statements") : PG_SHAPES,
    },
  })
  await visit(page, "/databases/1/performance?view=statements")
  await expect(page.getByText("Could not read the statements")).toBeVisible()
  await expect(page.getByText(/permission denied for view pg_stat_statements/)).toBeVisible()
  refused = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(shapeRow(page, /CREATE ROLE app_reader/)).toContainText("PASSWORD ?")
})

test("the shapes keep to a phone's width, and so does a statement's page", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockOps(page, { answers: { "GET 1/statements": PG_SHAPES } })
  await visit(page, "/databases/1/performance?view=statements")
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(7)
  await keepsTheRules(page, "a PostgreSQL server's statements at a phone's width")
  await visit(page, "/databases/1/performance/statements/p3")
  await expect(await openedStatement(page, "NOCREATEROLE PASSWORD ?")).toContainText(
    "The statement, as a shape",
  )
  await keepsTheRules(page, "a statement's page at a phone's width")
  // There is no rail on a phone: the page is the one pane, and the arrows walk the list.
  await expect(rail(page, "Statements")).toBeHidden()
  await page.getByRole("button", { name: "Next statement" }).click()
  await expect(page).toHaveURL(/\/statements\/p6\?tab=text$/)
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

  // A table opens as a page on everything the engine keeps about it; the address is which.
  await tables.getByRole("button", { name: "Open public.orders" }).click()
  await expect(page).toHaveURL(/\/performance\/tables\/public\.orders$/)
  const orders = about(page, "About public.orders")
  await expect(orders).toContainText("3.2 MB")
  await expect(orders).toContainText("11% of the database")
  await expect(detailView(page)).toContainText("Sequential scans")
  // What its bytes are is one strip, where each row of the list drew its own.
  await expect(
    detailView(page).getByRole("img", {
      name: "Rows 2.2 MB, Indexes 976.6 KB, Large values 8.0 KB",
    }),
  ).toBeVisible()
  await expect(orders.getByRole("link", { name: "Open in Data" })).toHaveAttribute(
    "href",
    "/databases/1/data?schema=public&table=orders",
  )
  // The choice inside an action is one control on its row, and the answer
  // already chosen is the one that does not stop the table.
  await tab(page, "Maintenance").click()
  await expect(page).toHaveURL(/tab=maintenance/)
  await expect(detailView(page).getByRole("radio", { name: "concurrently" })).toHaveAttribute(
    "aria-checked",
    "true",
  )
  await detailView(page)
    .getByRole("button", { name: "Reindex concurrently: public.orders" })
    .click()
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

  // Its table opens as the table's own page, and the way back from it is Tables.
  await indexes
    .getByRole("row", { name: /events_occurred_idx/ })
    .getByRole("button", { name: "Open the table analytics.events" })
    .click()
  await expect(page).toHaveURL(/\/performance\/tables\/analytics\.events$/)
  await expect(about(page, "About analytics.events")).toContainText("analytics.events")
  await back(page, "Tables").click()
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
    /^Statement history$/,
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

test("two statements with one digest are two rows, each opening its own page", async ({ page }) => {
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
  // The second is told apart by its place, written where no layer between
  // the address bar and the page reads it as anything.
  await expect(page).toHaveURL(/\/performance\/statements\/d1~7E1$/)
  await expect(aboutStatement(page)).toContainText("30% of runtime")
  // Calls: the second row's five, not the first's six.
  await expect(aboutStatement(page)).toContainText("5 calls")
  await expect(aboutStatement(page)).not.toContainText("6 calls")
  // Both are in the list beside it, and one of them is the one on screen.
  await expect(rail(page, "Statements").getByRole("link")).toHaveCount(3)
  await expect(rail(page, "Statements").locator("[aria-current=page]")).toHaveCount(1)
  await page.goBack()
  await rows.nth(1).click()
  await expect(page).toHaveURL(/\/performance\/statements\/d1$/)
  await expect(aboutStatement(page)).toContainText("6 calls")
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
  await visit(page, "/databases/1/performance/statements/m1?tab=plan")
  await expect(detailView(page)).toContainText("This shape has 20 placeholders")
  await expect(detailView(page).getByRole("textbox")).toHaveCount(0)
  await expect(aboutStatement(page).getByRole("button", { name: "Open in Query" })).toBeVisible()
})

// ---------------------------------------------------------------------------
// Statement history
// ---------------------------------------------------------------------------

const historyView = (page: Page) => region(page, "Statement history")

test("statement history ranks a window's statements by the column pressed, each with its trend, and one opens its charts", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=history")
  const history = historyView(page)
  const rows = history.getByRole("row")
  await expect(rows).toHaveCount(SHOP_HISTORY.length + 1)
  await expect(rows.nth(1)).toContainText("SELECT o.id, o.total, o.status FROM orders o")
  await expect(rows.nth(1)).toContainText("46%")
  // Each row draws its time across the window.
  await expect(history.getByRole("img", { name: "Its time over the last hour" })).toHaveCount(
    SHOP_HISTORY.length,
  )
  // A row is one line: its trend is a column beside its text, not a band under it.
  const heights = await rows.evaluateAll((all) =>
    all.slice(1).map((row) => Math.round(row.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)
  expect(heights[0]).toBeLessThan(44)
  await expect(history).toContainText("recorded over the last hour")
  // What the figures are made from, in the engine's own terms, is behind the fact on the title line.
  await expect(await explained(page, history, "How these figures are made")).toContainText(
    /pg_stat_statements keeps running totals for each statement[\s\S]*so there are no percentiles/,
  )
  // PostgreSQL keeps no run times and counts no rows read: neither column is drawn.
  await expect(history.getByRole("columnheader", { name: "19 in 20" })).toHaveCount(0)
  await expect(history.getByRole("columnheader", { name: "Examined" })).toHaveCount(0)
  await expect(history.getByRole("columnheader", { name: "Share" })).toHaveAttribute(
    "aria-sort",
    "descending",
  )

  // Another order is asked of the server, which ranks the whole window.
  const asked = page.waitForRequest((request) =>
    /\/statements\/history\?.*sort=calls/.test(request.url()),
  )
  await history.getByRole("button", { name: "Calls" }).click()
  await asked
  await expect(page).toHaveURL(/sort=calls/)
  await expect(history.getByRole("columnheader", { name: "Calls" })).toHaveAttribute(
    "aria-sort",
    "descending",
  )
  await expect(rows.nth(1)).toContainText("INSERT INTO page_views")

  await history.getByRole("textbox", { name: "Filter the statement history" }).fill("inventory")
  await expect(rows).toHaveCount(2)
  await history.getByRole("textbox", { name: "Filter the statement history" }).fill("")

  // A recorded statement opens the statement's page, on its History view.
  await history.getByRole("row", { name: /UPDATE inventory/ }).click()
  await expect(page).toHaveURL(/\/performance\/statements\/7731904\?tab=history$/)
  const recorded = detailView(page)
  for (const chart of ["Time spent", "Calls", "Time per call", "Rows"]) {
    await expect(recorded.getByRole("heading", { name: chart, exact: true })).toBeVisible()
  }
  await expect(recorded).toContainText("a point for every 5 minutes")
  await expect(recorded).toContainText("A mark on the time axis is a reset")
  // Its figures over the window read as one line, not a strip of tiles.
  await expect(recorded.locator("[data-slot=figure-line]")).toContainText(
    "26% of the window's statement time",
  )
  await expect(page.locator("[data-slot=metric-strip]")).toHaveCount(0)
  await expect(aboutStatement(page)).toContainText(
    "UPDATE inventory SET reserved = reserved + $1 WHERE sku = $2",
  )
  // The server no longer lists it among its top statements: said, with its recorded share.
  await expect(aboutStatement(page)).toContainText("No longer among the top statements")
  await expect(aboutStatement(page).getByRole("button", { name: "Open in Query" })).toBeVisible()
  // The way back is the list it was opened from.
  await back(page, "Statement history").click()
  await expect(page).toHaveURL(/view=history/)
  await expect(history.getByRole("row", { name: /UPDATE inventory/ })).toBeVisible()
})

test("statement history is read over the Overview's window, and a range pressed in it is asked for", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=history")
  const history = historyView(page)
  await expect(history.getByRole("row")).toHaveCount(SHOP_HISTORY.length + 1)
  const asked = page.waitForRequest((request) =>
    /\/statements\/history\?.*hours=24/.test(request.url()),
  )
  await history.getByRole("radio", { name: "24 hours" }).click()
  await asked
  await expect(history).toContainText("recorded over the last 24 hours")
  await view(page, /^Overview$/).click()
  await expect(page.getByRole("radio", { name: "24 hours" })).toBeChecked()
})

test("statement history before its second reading says when the first point comes", async ({
  page,
}) => {
  await mockOps(page, { answers: { "GET 1/statements/history": NO_HISTORY_YET } })
  await visit(page, "/databases/1/performance?view=history")
  await expect(page.getByText("No statement history yet")).toBeVisible()
  await expect(page.getByText(/the next is in about 3 minutes/)).toBeVisible()
  await expect(page.getByText(/kept for 7 days/)).toBeVisible()
})

test("statement history a server cannot give says why, with the way to the view that turns it on", async ({
  page,
}) => {
  await mockOps(page, {
    answers: {
      "GET 1/statements/history": (call: number, url: URL) => ({
        ...NO_HISTORY_YET(call, url),
        reading: {
          at: iso(Date.now() - 3 * MINUTE),
          supported: false,
          reason: "pg_stat_statements is not enabled in this database",
        },
      }),
    },
  })
  await visit(page, "/databases/1/performance?view=history")
  await expect(page.getByText("The statements this server runs cannot be recorded")).toBeVisible()
  await expect(page.getByText("pg_stat_statements is not enabled in this database")).toBeVisible()
  await page.getByRole("button", { name: "Open Statements" }).click()
  await expect(page).toHaveURL(/view=statements/)
  await expect(page.getByText("This server is not counting its statements")).toHaveCount(0)
})

test("statement history that could not be read says so and is tried again", async ({ page }) => {
  let down = true
  await mockOps(page, {
    answers: {
      "GET 1/statements/history": (call: number, url: URL) =>
        down ? failing("database is locked", 500) : statementHistory(SHOP_HISTORY)(call, url),
    },
  })
  await visit(page, "/databases/1/performance?view=history")
  await expect(page.getByText("Could not read the statement history")).toBeVisible()
  down = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(historyView(page).getByRole("row")).toHaveCount(SHOP_HISTORY.length + 1)
})

test("a role that may only read reads the statement history and is offered nothing to run", async ({
  page,
}) => {
  await mockOps(page, { viewer: true })
  await visit(page, `/databases/1/performance/statements/${SHOP_HISTORY[0].id}?tab=history`)
  await expect(
    detailView(page).getByRole("heading", { name: "Time spent", exact: true }),
  ).toBeVisible()
  await expect(aboutStatement(page)).toContainText("SELECT o.id, o.total, o.status FROM orders o")
  await expect(page.getByRole("button", { name: "Open in Query" })).toHaveCount(0)
})

test("an engine that keeps run times gives the history its percentiles and the rows each query read", async ({
  page,
}) => {
  await mockOps(page, AS_CLICKHOUSE)
  await visit(page, "/databases/2/performance?view=history")
  const history = historyView(page)
  await expect(history.getByRole("row")).toHaveCount(ANALYTICS_HISTORY.length + 1)
  await expect(history.getByRole("columnheader", { name: "19 in 20" })).toBeVisible()
  await expect(history.getByRole("columnheader", { name: "Examined" })).toBeVisible()
  await expect(history.getByRole("row").nth(1)).toContainText("212 ms")
  await expect(await explained(page, history, "How these figures are made")).toContainText(
    "ClickHouse's query log holds every finished query",
  )
  await history.getByRole("row").nth(1).click()
  const recorded = detailView(page)
  await expect(recorded.getByText("99 in 100 within", { exact: true }).first()).toBeVisible()
  // The percentiles are said in words on the line of its figures.
  await expect(recorded.locator("[data-slot=figure-line]")).toContainText("99 in 100 inside 487 ms")
  await expect(recorded).toContainText("40K for each returned")
})

test("a file has no statement history to offer", async ({ page }) => {
  const ops = await mockOps(page)
  await visit(page, "/databases/7/performance")
  await expect(view(page, /^File$/)).toBeVisible()
  await expect(view(page, /^Statement history$/)).toHaveCount(0)
  expect(ops.seen.filter((request) => request.includes("/statements/history"))).toEqual([])
})

test("statement history fits a phone, its list and its charts", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=history")
  await expect(historyView(page).getByRole("row")).toHaveCount(SHOP_HISTORY.length + 1)
  const sideways = () =>
    page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
  expect(await sideways()).toBe(false)
  await historyView(page).getByRole("row").nth(1).click()
  await expect(detailView(page).getByRole("heading", { name: "Rows", exact: true })).toBeVisible()
  expect(await sideways()).toBe(false)
  const wide = await detailView(page).evaluate((el) => el.scrollWidth > el.clientWidth + 1)
  expect(wide).toBe(false)
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

test("a row opens its page from the keyboard, and the keyboard goes back to where it was when a question or a run closes", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=sessions")

  // A session's page, opened from its row with the keyboard; Back is the list.
  await sessionRow(page, "52").focus()
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/\/performance\/sessions\/52$/)
  await expect(about(page, "About session 52")).toContainText("Session 52")
  await page.goBack()
  await expect(sessionRow(page, "52")).toBeVisible()

  // A confirmation refused.
  const cancel = page.getByRole("button", { name: "Cancel the statement of session 52" })
  await cancel.focus()
  await page.keyboard.press("Enter")
  await expect(dialog(page)).toContainText("Cancel statement")
  await page.keyboard.press("Escape")
  await expect(cancel).toBeFocused()

  // A table's page, a run from the list on it, and a question from a row's menu.
  await view(page, /^Tables$/).click()
  const open = region(page, "Tables").getByRole("button", { name: "Open public.orders" })
  await open.focus()
  await page.keyboard.press("Enter")
  await expect(detailView(page)).toContainText("Sequential scans")
  await tab(page, "Maintenance").click()
  const analyze = detailView(page).getByRole("button", {
    name: "Analyze: public.orders",
    exact: true,
  })
  await analyze.click()
  await expect(dialog(page)).toContainText("Finished")
  await dialog(page).getByRole("button", { name: "Close" }).first().click()
  await expect(analyze).toBeFocused()
  await page.goBack()
  await expect(open).toBeVisible()

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
  await visit(page, "/databases/1/performance/sessions/40~2C77")
  const session = about(page, "About session 40,77")
  await expect(session).toContainText("Session 40,77")
  await expect(session).toContainText("billing-worker")
  await expect(session).toContainText("Blocked")
  // The engine's word for what a held-up session waits on is on its page…
  await expect(detailView(page)).toContainText("Lock:transactionid")
  await back(page, "Sessions").click()
  // …and the list's "Waiting on" is only what a reader can act on: who it is behind.
  await expect(sessionRow(page, "40,77")).toContainText("behind")
  await expect(sessionRow(page, "52")).not.toContainText("ClientRead")

  // A session can wait on something the list does not hold, and its address says so.
  await visit(page, "/databases/1/performance/sessions/nope")
  await expect(page.getByText("Session nope is not in the session list")).toBeVisible()
  await expect(page.getByText(/It may be a prepared transaction/)).toBeVisible()
  await page.getByRole("link", { name: "Back to sessions" }).click()
  await expect(sessionRow(page, "52")).toBeVisible()
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
  // It is the engine's word for what the thread is doing, and is on the session's page as that.
  await sessionRow(page, "52").click()
  await expect(detailView(page).getByText("Doing", { exact: true })).toBeVisible()
  await expect(detailView(page)).toContainText("Timeout:PgSleep")
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

test("an address that names a schema the database does not hold narrows nothing, and a table it does not hold says so", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=tables&scope=nope")
  const tables = region(page, "Tables")
  await expect(tables.getByRole("row")).toHaveCount(5)
  await expect(
    tables
      .getByRole("group", { name: "Narrow to one of the schemas" })
      .getByRole("button", { name: /All schemas/ }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect(page).not.toHaveURL(/scope=/)

  // A table the database no longer holds has a page that says so, with the way back.
  await visit(page, "/databases/1/performance/tables/public.gone")
  await expect(page.getByText("This database holds no table named public.gone")).toBeVisible()
  await page.getByRole("link", { name: "Back to tables" }).click()
  await expect(tables.getByRole("row")).toHaveCount(5)
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
    "Share",
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
    .toEqual(["Table", "Rows", "Size", "Share", "Dead rows", "Vacuumed", "Analysed", "Actions"])
  await page.setViewportSize({ width: 1024, height: 900 })
  await expect
    .poll(heads)
    .toEqual(["Table", "Rows", "Size", "Share", "Dead rows", "Vacuumed", "Actions"])
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
  // Each chart is found by its name, as the database home's are.
  const sessions = page.getByRole("group", { name: "Sessions", exact: true })
  await expect(sessions.getByRole("row", { name: /Open/ })).toBeVisible()
  // It has no transactions to sit idle in and no lock waits: neither is a line at zero.
  await expect(sessions).not.toContainText("Idle in a transaction")
  await expect(sessions).not.toContainText("Waiting on a lock")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
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
  await expect(region(page, "Top statements").locator("[data-slot=rank-list] button")).toHaveCount(
    3,
  )

  // A list read from the server's own query table is a list of queries.
  await mockOps(page, {
    queryLog: { supported: true, source: "slow_log", entries: [ENTRY], truncated: false },
  })
  await visit(page, "/databases/2/logs")
  await expect(page.getByText("Slow queries", { exact: true })).toBeVisible()
  await expect(page.getByText("1 query", { exact: true })).toBeVisible()
})

test("the server's log is drawn from each message, and Insights draws no cards", async ({
  page,
}) => {
  const prefix = "2026-10-16 03:12:01.000 UTC [7002] app@shop "
  const at = "2026-10-16T03:12:01Z"
  await mockOps(page, {
    logSources: {
      sources: [
        {
          id: "docker:shop-db",
          label: "shop-db",
          kind: "container",
          lens: "postgres",
          primary: true,
        },
      ],
    },
    logLines: [
      {
        text: `${prefix}FATAL:  password authentication failed for user "app"`,
        level: "error",
        timestamp: at,
        stream: "stderr",
        event: "auth_failed",
        attrs: { user: "app", db: "shop", error_class: "auth" },
        body: prefix.length + "FATAL:  ".length,
      },
      {
        text: `${prefix}DETAIL:  Role "app" does not exist.`,
        level: "error",
        timestamp: at,
        stream: "stderr",
        cont: true,
        body: prefix.length,
      },
    ],
  })
  await visit(page, "/databases/1/logs?view=search")
  const pane = page.getByLabel("Log lines")
  await expect(pane.getByText('password authentication failed for user "app"')).toBeVisible()
  // The pid, user@db and severity are the columns and the event word: the
  // row does not say them twice, and a lens-read line carries no stderr mark.
  await expect(pane).not.toContainText("[7002]")
  await expect(pane).not.toContainText("FATAL:")
  await expect(pane).toContainText('DETAIL:  Role "app" does not exist.')
  await expect(pane).not.toContainText("stderr")

  await page.getByRole("button", { name: "Insights", exact: true }).click()
  await expect(page.getByText("Events over time")).toBeVisible()
  for (const tile of ["Lines", "Busiest", "Warnings"]) {
    await expect(page.getByText(tile, { exact: true })).toHaveCount(0)
  }
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
      // A glyph that turns has no centre line to be off: its ink is wherever the
      // turn has it at that instant, and under load this caught a spinner mid-turn.
      if (style.animationName !== "none" && style.animationIterationCount === "infinite") continue
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
      "history",
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
    for (const name of ["", "sessions", "history", "parts", "merges"]) {
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

    // With a confirmation open over a list.
    await visit(page, "/databases/1/performance?view=sessions")
    await page.getByRole("button", { name: "Terminate session 12,3301" }).first().click()
    await expect(dialog(page)).toContainText("Terminate session")
    await keepsTheRules(page, "the sessions with a confirmation open", true)

    // The pages a list opens for one thing, on each of their views.
    await visit(page, "/databases/1/performance/tables/public.orders")
    await expect(detailView(page)).toContainText("Sequential scans")
    await keepsTheRules(page, "a table's page")
    await visit(page, "/databases/1/performance/tables/public.orders?tab=maintenance")
    await expect(detailView(page)).toContainText("Housekeeping")
    await keepsTheRules(page, "a table's maintenance")
    await visit(page, "/databases/1/performance/statements/s2")
    await expect(detailView(page).locator("[data-slot=reading-line]")).toHaveCount(4)
    await keepsTheRules(page, "a statement's page")
    await visit(page, "/databases/1/performance/statements/s2?tab=plan")
    await expect(detailView(page).getByRole("textbox")).toHaveCount(2)
    await keepsTheRules(page, "a statement's plan")
    await visit(page, `/databases/1/performance/statements/${SHOP_HISTORY[1].id}?tab=history`)
    await expect(detailView(page)).toContainText("Time per call")
    await keepsTheRules(page, "a statement's history")
    await visit(page, "/databases/1/performance/sessions/40~2C77")
    await expect(about(page, "About session 40,77")).toContainText("Session 40,77")
    await keepsTheRules(page, "a session's page")
    await visit(page, "/databases/1/performance/sessions/40~2C77?tab=waits")
    await expect(detailView(page).getByRole("link", { name: "Open session 12,3301" })).toBeVisible()
    await keepsTheRules(page, "a session's waits")
  })
}

// ---------------------------------------------------------------------------
// The pages a list opens for one thing
// ---------------------------------------------------------------------------

/** The top edge of something, once the page's arrival has finished. */
async function topOf(page: Page, selector: string) {
  await risen(page)
  return page
    .locator(selector)
    .first()
    .evaluate((el) => Math.round(el.getBoundingClientRect().top))
}

/**
 * How far each row of a list stands from the one before it, in whichever
 * shape the width gives the list. The pitch rather than the height: a list's
 * first row has no rule over it, so it is a pixel shorter and stands where
 * the rest do.
 */
const rowPitches = (page: Page, selector: string) =>
  page.locator(selector).evaluateAll((rows) => {
    const tops = rows.map((row) => row.getBoundingClientRect().top)
    return tops.slice(1).map((top, at) => top - tops[at])
  })

/** One pitch down the list, to within the half pixel a line box can come to. */
function expectOnePitch(pitches: number[]) {
  expect(pitches.length).toBeGreaterThan(1)
  expect(Math.max(...pitches) - Math.min(...pitches)).toBeLessThan(1)
}

test("the addresses that opened the sheets land on the pages they named", async ({ page }) => {
  await mockOps(page)
  // A statement, with the order its list was in.
  await visit(page, "/databases/1/performance?view=statements&statement=s2&sort=calls")
  await expect(page).toHaveURL(/\/performance\/statements\/s2\?sort=calls$/)
  await expect(aboutStatement(page)).toContainText("UPDATE carts SET total = $1 WHERE id = $2")
  // A recorded statement, on its history.
  await visit(page, "/databases/1/performance?view=history&statement=7731904")
  await expect(page).toHaveURL(/\/performance\/statements\/7731904\?tab=history$/)
  await expect(tab(page, "History")).toHaveAttribute("aria-selected", "true")
  // A table, by the key that held its schema and name escaped.
  await visit(
    page,
    "/databases/1/performance?view=tables&scope=analytics&object=analytics%2Fevents",
  )
  await expect(page).toHaveURL(/\/performance\/tables\/analytics\.events\?scope=analytics$/)
  await expect(about(page, "About analytics.events")).toBeVisible()
  // The schema the list was narrowed to goes with it: the rail is that list.
  await expect(rail(page, "Tables").getByRole("link")).toHaveCount(1)
  // A session, with the state its list was narrowed to.
  await visit(page, "/databases/1/performance?view=sessions&state=blocked&session=40%2C77")
  await expect(page).toHaveURL(/\/performance\/sessions\/40~2C77\?state=blocked$/)
  await expect(about(page, "About session 40,77")).toBeVisible()
  await expect(rail(page, "Sessions").getByRole("link")).toHaveCount(1)
  // The sheet is not what Back returns to.
  await visit(page, "/databases/1/performance?view=statements")
  await page.goto("/databases/1/performance?view=statements&statement=s1")
  await expect(aboutStatement(page)).toBeVisible()
  await page.goBack()
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(4)
})

test("a name with a dot and one with a slash are each one segment of an address, and open", async ({
  page,
}) => {
  await mockOps(page, {
    answers: {
      "GET 1/tablestats": {
        ...TABLESTATS,
        tables: [
          table("reports.2026", "q1/q2", { totalBytes: 6_000_000 }),
          table("public", "orders.archive"),
          ...TABLESTATS.tables,
        ],
      },
      "GET 1/statements": {
        ...STATEMENTS,
        statements: [
          statement("a/b.c", "SELECT 1 FROM slashed", 0.7),
          statement("", "SELECT 2 FROM unnamed", 0.2),
          statement("s3", "SELECT count(*) FROM order_items", 0.1),
        ],
      },
    },
  })
  await visit(page, "/databases/1/performance?view=tables")
  await region(page, "Tables").getByRole("button", { name: "Open reports.2026.q1/q2" }).click()
  // The one dot left in the segment is the one between the schema and the name.
  await expect(page).toHaveURL(/\/performance\/tables\/reports~2E2026\.q1~2Fq2$/)
  await expect(about(page, "About reports.2026.q1/q2")).toContainText("q1/q2")
  // The address alone opens it: nothing between the bar and the page reads it as a path.
  await visit(page, "/databases/1/performance/tables/public.orders~2Earchive")
  await expect(about(page, "About public.orders.archive")).toContainText("orders.archive")
  await expect(page.locator("[data-slot=database-strip]")).toBeVisible()

  await visit(page, "/databases/1/performance?view=statements")
  await region(page, "Statements")
    .getByRole("row", { name: /SELECT 1 FROM slashed/ })
    .click()
  await expect(page).toHaveURL(/\/performance\/statements\/a~2Fb~2Ec$/)
  await expect(aboutStatement(page)).toContainText("SELECT 1 FROM slashed")
  // A statement another role ran has no id, and still has a page.
  await page.getByRole("button", { name: "Next statement" }).click()
  await expect(page).toHaveURL(/\/performance\/statements\/~$/)
  await expect(aboutStatement(page)).toContainText("SELECT 2 FROM unnamed")
  // A percent-escape is the spelling that does not arrive whole: it is not read as a name.
  await visit(page, "/databases/1/performance/statements/a%2Fb.c")
  await expect(page.getByText(/not among the ones the server lists/)).toBeVisible()
})

test("a statement's page walks its list: the rail, the arrows, Alt and the arrow keys, with the view kept", async ({
  page,
}) => {
  await mockOps(page)
  await visit(page, "/databases/1/performance?view=statements")
  // The list's own filter is the rail's.
  await region(page, "Statements").getByLabel("Filter the statements").fill("SELECT")
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(3)
  await region(page, "Statements")
    .getByRole("row", { name: /SELECT \* FROM orders/ })
    .click()
  const list = rail(page, "Statements")
  await expect(list.getByLabel("Filter the statements")).toHaveValue("SELECT")
  await expect(list.getByRole("link")).toHaveCount(2)
  await expect(list).toContainText("2 of 3 statements · by total time")
  await expect(list.locator("[aria-current=page]")).toContainText("SELECT * FROM orders")

  // The view on screen goes with the reader to the next statement.
  await tab(page, "Text").click()
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/statements\/s3\?tab=text$/)
  await expect(detailView(page).locator("[data-slot=code-view]")).toContainText("order_items")
  await expect(page.getByRole("button", { name: "Next statement" })).toBeDisabled()
  await page.getByRole("button", { name: "Previous statement" }).click()
  await expect(page).toHaveURL(/\/statements\/s1\?tab=text$/)
  // The filter cleared in the rail is cleared in the list it came from.
  await list.getByLabel("Filter the statements").fill("")
  await expect(list.getByRole("link")).toHaveCount(3)
  await list.getByRole("link", { name: /UPDATE carts/ }).click()
  await expect(aboutStatement(page)).toContainText("UPDATE carts SET total")
  await back(page, "Statements").click()
  await expect(region(page, "Statements").getByRole("row")).toHaveCount(4)
})

test("a session that ends while its page is open is said to have ended, and offers nothing to stop", async ({
  page,
}) => {
  let gone = false
  const ops = await mockOps(page, {
    answers: {
      "GET 1/activity": () =>
        gone
          ? { ...ACTIVITY, sessions: ACTIVITY.sessions.filter((entry) => entry.pid !== "52") }
          : ACTIVITY,
    },
  })
  await visit(page, "/databases/1/performance/sessions/52")
  const session = about(page, "About session 52")
  await expect(session).toContainText("Active")
  await expect(detailView(page)).toContainText("SELECT count(*) FROM order_items")

  // Its statement is cancelled from the page, behind the same question the list asks.
  await session.getByRole("button", { name: "Cancel statement" }).click()
  await expect(dialog(page)).toContainText("Session 52")
  gone = true
  await dialog(page).getByRole("button", { name: "Stop the statement" }).click()
  await expect.poll(() => ops.sent.map((sent) => sent.request)).toEqual(["POST 1/activity/cancel"])
  expect(ops.sent[0].body).toEqual({ pid: "52" })

  // The list no longer holds it: the page stays, as it was last seen, and says so.
  await expect(session).toContainText("Ended, shown as it was last seen")
  await expect(detailView(page)).toContainText("SELECT count(*) FROM order_items")
  await expect(
    page.getByRole("button", { name: /^(Cancel statement|Terminate session)$/ }),
  ).toHaveCount(0)
  // It is a state of the page, not a failure of it.
  await expect(page.getByText(/could not/i)).toHaveCount(0)
  // It is still on its own rail, apart from the sessions that are there.
  await expect(rail(page, "Sessions").locator("[aria-current=page]")).toContainText("ended")
})

test("terminating is the page's command where there is nothing to cancel, and in its menu where there is", async ({
  page,
}) => {
  const ops = await mockOps(page)
  await visit(page, "/databases/1/performance/sessions/12~2C3301")
  await about(page, "About session 12,3301")
    .getByRole("button", { name: "Terminate session" })
    .click()
  await expect(dialog(page)).toContainText("its open transaction is rolled back")
  await dialog(page).getByRole("button", { name: "Terminate session" }).click()
  await expect
    .poll(() => ops.sent.at(-1))
    .toMatchObject({ request: "POST 1/activity/kill", body: { pid: "12,3301" } })

  await visit(page, "/databases/1/performance/sessions/52")
  await page.getByRole("button", { name: "More for session 52" }).click()
  await expect(page.getByRole("menuitem")).toHaveText([
    "Open in Query",
    "Copy statement",
    "Terminate session",
  ])
  await page.getByRole("menuitem", { name: "Terminate session" }).click()
  await expect(dialog(page)).toContainText("Session 52")
  await page.keyboard.press("Escape")
  // The dashboard's own session is offered neither.
  await visit(page, "/databases/1/performance/sessions/60")
  await expect(about(page, "About session 60")).toContainText("this dashboard")
  await expect(
    page.getByRole("button", { name: /^(Cancel statement|Terminate session)$/ }),
  ).toHaveCount(0)
})

test("an account that may only read sees no control it cannot use on the three pages", async ({
  page,
}) => {
  await mockOps(page, { viewer: true })
  await visit(page, "/databases/1/performance/statements/s1")
  await expect(aboutStatement(page)).toContainText("60% of runtime")
  await expect(page.getByRole("button", { name: "Open in Query" })).toHaveCount(0)
  await tab(page, "Plan").click()
  await expect(
    detailView(page).getByRole("button", { name: "Explain with these values" }),
  ).toBeVisible()
  await expect(detailView(page).getByRole("button", { name: /Open in Query/ })).toHaveCount(0)

  await visit(page, "/databases/1/performance/sessions/52")
  await expect(about(page, "About session 52")).toContainText("Active")
  await expect(
    page.getByRole("button", { name: /^(Cancel statement|Terminate session)$/ }),
  ).toHaveCount(0)
  await page.getByRole("button", { name: "More for session 52" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Copy statement"])
  await page.keyboard.press("Escape")

  await visit(page, "/databases/1/performance/tables/public.orders")
  await expect(
    about(page, "About public.orders").getByRole("link", { name: "Open in Data" }),
  ).toBeVisible()
  // What it may read of the table's keeping is there; nothing that runs is.
  await tab(page, "Maintenance").click()
  await expect(detailView(page)).toContainText("Vacuumed by the engine")
  await expect(detailView(page).getByRole("button")).toHaveCount(0)
  await page.getByRole("button", { name: "More for public.orders" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Open in Schema"])
})

for (const width of [1280, 390]) {
  test(`a list's rows are one height with and without what only some of them have, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockOps(page, {
      answers: {
        "GET 1/activity": {
          ...ACTIVITY,
          sessions: [...ACTIVITY.sessions, session("70", "active", { seconds: 2 })],
        },
      },
    })
    // No bar under a statement, a table or an index: a share is a figure.
    for (const name of ["statements", "tables", "indexes"]) {
      await visit(page, `/databases/1/performance?view=${name}`)
      await expect(
        page.locator("[data-slot=page] tbody tr, [data-slot=page] li").first(),
      ).toBeVisible()
      await expect(page.locator("[data-slot=page] .bg-meter-track")).toHaveCount(0)
      await expect(page.locator("[data-slot=stat-grid], [data-slot=metric-strip]")).toHaveCount(0)
    }

    await visit(page, "/databases/1/performance?view=statements")
    await expect(region(page, "Statements").getByRole("row")).toHaveCount(4)
    expectOnePitch(await rowPitches(page, "[data-slot=page] tbody tr"))

    // A table that is overdue and one that says nothing of itself.
    await visit(page, "/databases/1/performance?view=tables")
    await expect(page.locator("[data-list-row=table]")).toHaveCount(4)
    expectOnePitch(await rowPitches(page, "[data-list-row=table]"))
    if (width >= 1280) {
      await expect(page.locator("[data-list-row=table] [data-slot=share]")).toHaveText([
        "57%",
        "31%",
        "11%",
        "1%",
      ])
    }
    // The strip is the whole database's, and does not grow from row to row.
    await expect(
      page.locator("[data-slot=tables-split]").getByRole("img", { name: /^Rows .*, Indexes / }),
    ).toBeVisible()

    // An index with two things wrong beside one with nothing to say.
    await visit(page, "/databases/1/performance?view=indexes")
    await expect(page.locator("[data-list-row=index]")).toHaveCount(5)
    expectOnePitch(await rowPitches(page, "[data-list-row=index]"))

    // A session that waits on another, one with no statement, one with neither.
    await visit(page, "/databases/1/performance?view=sessions")
    await expect(page.locator("[data-list-row=session]")).toHaveCount(5)
    expectOnePitch(await rowPitches(page, "[data-list-row=session]"))
  })

  test(`a page's opening block is in the same place while it is read and once it has been, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const statements = held<null>()
    const tables = held<null>()
    const activity = held<null>()
    await mockOps(page, {
      answers: {
        "GET 1/statements": () => statements.until.then(() => STATEMENTS),
        "GET 1/tablestats": () => tables.until.then(() => TABLESTATS),
        "GET 1/activity": () => activity.until.then(() => ACTIVITY),
      },
    })
    const line = "[data-slot=host-identity]"
    const strip = "[role=tablist]"

    await visit(page, "/databases/1/performance/statements/s2")
    await expect(page.getByText("Reading the statement…")).toBeVisible()
    const statementLine = await topOf(page, line)
    statements.release(null)
    await expect(aboutStatement(page)).toContainText("30% of runtime")
    expect(await topOf(page, line)).toBe(statementLine)
    const statementStrip = await topOf(page, strip)
    // Stepping to a statement with something wrong with its reads moves nothing either.
    await page.getByRole("button", { name: "Previous statement" }).click()
    await expect(aboutStatement(page)).toContainText("miss the cache")
    if (width >= 1280) expect(await topOf(page, strip)).toBe(statementStrip)

    await visit(page, "/databases/1/performance/tables/public.sessions_log")
    await expect(page.getByText("Reading its figures…")).toBeVisible()
    const tableLine = await topOf(page, line)
    tables.release(null)
    await expect(about(page, "About public.sessions_log")).toContainText("a vacuum is due")
    expect(await topOf(page, line)).toBe(tableLine)

    await visit(page, "/databases/1/performance/sessions/52")
    await expect(page.locator(line).getByText("Reading the sessions…")).toBeVisible()
    const sessionLine = await topOf(page, line)
    activity.release(null)
    await expect(about(page, "About session 52")).toContainText("Active")
    expect(await topOf(page, line)).toBe(sessionLine)
  })
}

test("a page of one thing is a single pane on a phone, with the way back and nothing cut off sideways", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockOps(page)
  for (const [path, name] of [
    ["/databases/1/performance/statements/s1", "About this statement"],
    ["/databases/1/performance/tables/public.orders?tab=maintenance", "About public.orders"],
    ["/databases/1/performance/sessions/40~2C77?tab=waits", "About session 40,77"],
  ] as const) {
    await visit(page, path)
    await expect(about(page, name)).toBeVisible()
    await expect(page.locator("[data-slot=detail-rail]")).toBeHidden()
    await expect(page.locator("[data-slot=page-context]").getByRole("link")).toBeVisible()
    await keepsTheRules(page, `${name} at a phone's width`)
  }
})

test("a page of an engine that has no such thing says so and asks the server nothing", async ({
  page,
}) => {
  const ops = await mockOps(page)
  // A file has no sessions and no statement statistics.
  await visit(page, "/databases/7/performance/sessions/1")
  await expect(page.getByText(/has nothing of the kind for this page to read/)).toBeVisible()
  await visit(page, "/databases/7/performance/statements/s1")
  await expect(page.getByRole("link", { name: "Open Performance" })).toBeVisible()
  expect(ops.seen.filter((request) => /\/(activity|statements)/.test(request))).toEqual([])
})

test("a top statement on Logs opens the statement's page, not a dialog of its own", async ({
  page,
}) => {
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
  await page.getByRole("button", { name: "Top statements" }).click()
  const top = region(page, "Top statements").locator("[data-slot=rank-list] button")
  await expect(top).toHaveCount(3)
  await top.nth(1).click()
  await expect(dialog(page)).toHaveCount(0)
  await expect(page).toHaveURL(/\/databases\/1\/performance\/statements\/s2$/)
  await expect(aboutStatement(page)).toContainText("UPDATE carts SET total = $1 WHERE id = $2")
  await expect(page.locator("[data-slot=metric-strip]")).toHaveCount(0)
})

test("Performance's lists and the pages they open, for the record @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the screenshots go to")
  test.setTimeout(600_000)
  const dir = process.env.JD_SHOTS
  const wait = { statements: held<null>(), tables: held<null>(), activity: held<null>() }
  // What the routes answer is the test's to change between captures.
  const now = {
    viewer: false,
    loading: false,
    statements: STATEMENTS as unknown,
    tables: TABLESTATS as unknown,
    activity: ACTIVITY as unknown,
  }
  const answers = {
    "GET 1/statements": () =>
      now.loading ? wait.statements.until.then(() => now.statements) : now.statements,
    "GET 1/tablestats": () => (now.loading ? wait.tables.until.then(() => now.tables) : now.tables),
    "GET 1/activity": () =>
      now.loading ? wait.activity.until.then(() => now.activity) : now.activity,
    "POST 1/explain": ORDERS_PLAN,
  }
  const mocked = async (viewer: boolean) => {
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockOps(page, {
      viewer,
      answers,
      queryLog: {
        supported: true,
        source: "log",
        entries: [ENTRY],
        truncated: false,
        threshold: "250ms",
      },
    })
  }

  // JD_SHOTS_WIDTHS narrows the run to one width: "390".
  const widths = (process.env.JD_SHOTS_WIDTHS ?? "1280,390").split(",").map(Number)
  for (const width of widths) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    const shot = async (name: string, path: string, ready?: () => Promise<unknown>) => {
      await visit(page, path)
      if (ready) await ready()
      if (!now.loading) await page.waitForLoadState("networkidle")
      await risen(page)
      await page.waitForTimeout(400)
      await page.screenshot({ path: `${dir}/after-${name}-${width}.png`, fullPage: true })
    }
    const drawn = (name: string) => () => expect(about(page, name)).toBeVisible()
    Object.assign(now, {
      viewer: false,
      loading: false,
      statements: STATEMENTS,
      tables: TABLESTATS,
      activity: ACTIVITY,
    })
    await mocked(false)

    // The lists.
    await shot("performance-overview-full", "/databases/1/performance")
    await shot("performance-statements-full", "/databases/1/performance?view=statements")
    await shot("performance-history-full", "/databases/1/performance?view=history")
    await shot("performance-tables-full", "/databases/1/performance?view=tables")
    await shot("performance-indexes-full", "/databases/1/performance?view=indexes")
    await shot("performance-sessions-full", "/databases/1/performance?view=sessions")
    await shot("logs-top-statements", "/databases/1/logs", async () => {
      await page.getByRole("button", { name: "Top statements" }).click()
      await expect(region(page, "Top statements").locator("[data-slot=rank-row]")).toHaveCount(3)
    })

    // A statement.
    const statementUrl = "/databases/1/performance/statements/s1"
    await shot("statement-full", statementUrl, drawn("About this statement"))
    await shot("statement-text", `${statementUrl}?tab=text`, () =>
      expect(detailView(page).locator("[data-slot=code-view]")).toContainText("customer_id"),
    )
    await shot("statement-plan", `${statementUrl}?tab=plan`, async () => {
      await detailView(page).getByRole("textbox").first().fill("42")
      await detailView(page).getByRole("button", { name: "Explain with these values" }).click()
      await expect(detailView(page)).toContainText("Index Scan")
    })
    await shot("statement-history", "/databases/1/performance/statements/7731904?tab=history", () =>
      expect(detailView(page).getByRole("heading", { name: "Rows", exact: true })).toBeVisible(),
    )
    await shot("statement-gone", "/databases/1/performance/statements/nope", () =>
      expect(page.getByText(/not among the ones the server lists/)).toBeVisible(),
    )

    // A table.
    const tableUrl = "/databases/1/performance/tables/public.sessions_log"
    await shot("table-full", tableUrl, drawn("About public.sessions_log"))
    await shot("table-maintenance", `${tableUrl}?tab=maintenance`, () =>
      expect(detailView(page)).toContainText("Housekeeping"),
    )
    await shot("table-missing", "/databases/1/performance/tables/public.gone", () =>
      expect(page.getByText(/holds no table named/)).toBeVisible(),
    )

    // A session.
    await shot("session-full", "/databases/1/performance/sessions/52", drawn("About session 52"))
    await shot("session-waits", "/databases/1/performance/sessions/40~2C77?tab=waits", () =>
      expect(detailView(page).getByRole("link", { name: "Open session 12,3301" })).toBeVisible(),
    )
    await shot("session-missing", "/databases/1/performance/sessions/nope", () =>
      expect(page.getByText(/is not in the session list/)).toBeVisible(),
    )
    await visit(page, "/databases/1/performance/sessions/52")
    await expect(about(page, "About session 52")).toContainText("Active")
    now.activity = { ...ACTIVITY, sessions: ACTIVITY.sessions.filter((one) => one.pid !== "52") }
    await expect(about(page, "About session 52")).toContainText("Ended", { timeout: 20_000 })
    await page.screenshot({ path: `${dir}/after-session-ended-${width}.png`, fullPage: true })
    now.activity = ACTIVITY

    // Empty, off and failed.
    now.statements = { ...STATEMENTS, totalMs: 0, statements: [] }
    await shot("performance-statements-empty", "/databases/1/performance?view=statements", () =>
      expect(page.getByText(/No statement has been counted yet/)).toBeVisible(),
    )
    now.statements = STATEMENTS_OFF
    await shot("performance-statements-off", "/databases/1/performance?view=statements", () =>
      expect(page.getByText("This server is not counting its statements")).toBeVisible(),
    )
    await shot("statement-off", statementUrl, () =>
      expect(page.getByText("This server is not counting its statements").last()).toBeVisible(),
    )
    now.statements = failing("pq: permission denied for view pg_stat_statements")
    await shot("performance-statements-failed", "/databases/1/performance?view=statements", () =>
      expect(page.getByText("Could not read the statements")).toBeVisible(),
    )
    await shot("statement-failed", statementUrl, () =>
      expect(page.getByText("Could not read the statements")).toBeVisible(),
    )
    now.statements = STATEMENTS
    now.tables = { ...TABLESTATS, tables: [] }
    await shot("performance-tables-empty", "/databases/1/performance?view=tables", () =>
      expect(page.getByText(/holds no tables yet/)).toBeVisible(),
    )
    now.tables = TABLESTATS

    // While it is read.
    now.loading = true
    await shot("performance-statements-loading", "/databases/1/performance?view=statements")
    await shot("statement-loading", statementUrl, () =>
      expect(page.getByText("Reading the statement…")).toBeVisible(),
    )
    await shot("table-loading", tableUrl, () =>
      expect(page.getByText("Reading its figures…")).toBeVisible(),
    )
    await shot("session-loading", "/databases/1/performance/sessions/52", () =>
      expect(
        page.locator("[data-slot=host-identity]").getByText("Reading the sessions…"),
      ).toBeVisible(),
    )
    now.loading = false

    // An account that may only read.
    await mocked(true)
    await shot("performance-statements-readonly", "/databases/1/performance?view=statements", () =>
      expect(region(page, "Statements").getByRole("row")).toHaveCount(4),
    )
    await shot("statement-readonly", statementUrl, drawn("About this statement"))
    await shot("table-readonly", `${tableUrl}?tab=maintenance`, () =>
      expect(detailView(page)).toContainText("Housekeeping"),
    )
    await shot(
      "session-readonly",
      "/databases/1/performance/sessions/52",
      drawn("About session 52"),
    )
  }
})
