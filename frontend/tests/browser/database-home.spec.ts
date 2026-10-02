import { expect as baseExpect, test, type Page } from "@playwright/test"
import {
  APP,
  CACHE,
  CONNECTIONS,
  NOTES,
  SHOP,
  hold,
  mockDatabases,
  summaryOf,
  type DatabaseMock,
} from "./database-fixture"

/**
 * A database's home: this engine's control panel, and the verbs that start,
 * stop, restart and protect the server behind it.
 *
 * The claims here are the ones the page makes to its reader. Its figures are
 * read off the server and a rate is two readings apart; each engine family
 * has its own home, and no route of another family is asked; a block that
 * could not be read says so and can be tried again, and one that stops
 * updating keeps what it had; a server that is not answering keeps its home
 * and is asked nothing; a verb is drawn only where the server offers it and
 * the role may use it, and a change in flight is said until it lands.
 */

// The home draws a dozen reads on arrival, and on a machine that is busy with
// other work the last of them can land after an assertion's five seconds. An
// assertion that holds is no slower for the longer wait.
const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const NOW = "2026-10-01T09:00:00Z"
const at = (step: number) => new Date(Date.parse(NOW) + step * 5_000).toISOString()

/** A reply that is not a 200. */
type Reply = { status: number; body: unknown }
const failing = (message: string): Reply => ({
  status: 502,
  body: { error: { code: "query_failed", message } },
})
const isReply = (value: unknown): value is Reply =>
  typeof value === "object" && value !== null && "status" in value && "body" in value

/** What a route answers: a value, or one made from how many times it has been asked. */
type Answer = unknown | ((call: number) => unknown)

// ---- PostgreSQL ------------------------------------------------------------

const sqlStats = (call: number) => ({
  supported: true,
  at: at(call),
  driver: "postgres",
  version: "PostgreSQL 16.4",
  // Held still: the page's clock runs on while an assertion waits, and the
  // facts read off a sample should not depend on which sample it was.
  uptimeSeconds: 46_000,
  role: "standalone",
  database: "shop_main",
  databaseBytes: 24_517_655,
  connections: { total: 12, active: 2, idle: 10, idleInTransaction: 0, waiting: 0, max: 100 },
  counters: {
    transactionsCommitted: 1_000 + call * 50,
    transactionsRolledBack: 0,
    blocksHit: 900_000 + call * 990,
    blocksRead: 2_700 + call * 10,
    rowsRead: 400_000 + call * 5_000,
    rowsWritten: 90_000 + call * 50,
  },
  gauges: { replicas: 0 },
  pool: null,
})

const ADVISOR = {
  checkedAt: NOW,
  silences: [],
  tablesOmitted: 0,
  tablesChecked: 7,
  engineChecks: true,
  truncated: false,
  findings: [
    {
      id: "no-backup",
      level: "warning",
      category: "reliability",
      title: "No backup of this database has been taken from here",
      detail: "Nothing in this dashboard's backup directory would bring the database back.",
      advice: "Take one now, and schedule them under Backups.",
      targets: [{ kind: "database", name: "shop" }],
      link: "/databases/1/backups",
    },
    {
      id: "unindexed-foreign-key",
      level: "warning",
      category: "performance",
      title: "1 foreign key with no index",
      detail: "Every delete of the referenced row scans the whole referencing table.",
      advice: "Create an index on the referencing columns.",
      objects: ["orders(customer_id)"],
      sql: 'CREATE INDEX "orders_customer_id_idx" ON "public"."orders" ("customer_id");',
    },
  ],
}

const STATEMENTS = {
  supported: true,
  sort: "total",
  resettable: true,
  totalMs: 100,
  since: "2026-10-01T08:00:00Z",
  statements: [
    {
      id: "1",
      query: "SELECT *\n  FROM orders\n WHERE customer_id = $1",
      calls: 1_200,
      totalMs: 60,
      meanMs: 0.05,
      rows: 1_200,
      hitRatio: 1,
      share: 0.6,
    },
    {
      id: "2",
      query: "UPDATE carts SET total = $1 WHERE id = $2",
      calls: 40,
      totalMs: 20,
      meanMs: 0.5,
      rows: 40,
      hitRatio: 1,
      share: 0.2,
    },
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
    sql: "CREATE EXTENSION pg_stat_statements;",
    available: true,
    preloaded: true,
    note: "The module is loaded; the extension has to be created in this database.",
  },
}

const TABLESTATS = {
  supported: true,
  schema: "",
  truncated: false,
  tables: [
    { schema: "analytics", table: "events", rows: 60_000, totalBytes: 9_863_168 },
    { schema: "public", table: "orders", rows: 9_000, totalBytes: 3_399_680 },
  ],
}

const CONSUMERS = {
  checkedAt: NOW,
  nodes: [
    { id: "db:1", kind: "database", name: "shop", product: "postgres", connId: 1 },
    {
      id: "deploy:7",
      kind: "deployment",
      name: "shop-api",
      product: "nodejs",
      detail: "production",
      href: "/deploy/7",
    },
    { id: "host", kind: "host", name: "This server", detail: "processes on the machine" },
  ],
  edges: [
    { from: "db:1", to: "host", via: ["session"], sessions: 1, status: "observed" },
    { from: "db:1", to: "deploy:7", via: ["binding", "session"], sessions: 8, status: "connected" },
  ],
}

const DUMP = {
  file: "shop-20261001-060000.dump",
  size: 420_000,
  takenAt: "2026-10-01T06:00:00Z",
  format: "pg_dump archive",
  durationMs: 900,
  tool: "pg_dump",
  summary: "written by pg_dump",
  origin: "dump",
}
const BACKUPS = {
  dir: "/var/lib/just-dashboard/backups/databases/shop",
  files: [DUMP, { ...DUMP, file: "shop-20260930-060000.dump", takenAt: "2026-09-30T06:00:00Z" }],
  options: {},
}
const NO_BACKUPS = { dir: BACKUPS.dir, files: [], options: {} }

const SCHEMAS = [
  { name: "shop_main", size: 24_517_655, owner: "app" },
  { name: "shop_staging", size: 1_048_576, owner: "app" },
]

// ---- Redis -----------------------------------------------------------------

const redisStats = (call: number) => ({
  server: {
    redis_version: "7.4.1",
    flavor: "redis",
    version: "7.4.1",
    role: "primary",
    mode: "standalone",
    sampledAtMs: Date.parse(NOW) + call * 5_000,
    counters: {
      uptime_in_seconds: 14_800 + call * 5,
      connected_clients: 4,
      blocked_clients: 0,
      used_memory: 8_437_360,
      used_memory_rss: 13_590_528,
      maxmemory: 0,
      total_commands_processed: 92_000 + call * 500,
      total_net_input_bytes: 12_000_000 + call * 5_000,
      total_net_output_bytes: 480_000 + call * 1_000,
      keyspace_hits: 300 + call * 30,
      keyspace_misses: 100 + call * 10,
      evicted_keys: 0,
      rejected_connections: 0,
      keys: 28_600,
      expires: 20_500,
      rdb_last_save_time: Date.parse(NOW) / 1000 - 590,
      rdb_changes_since_last_save: 12,
    },
  },
})

const REDIS_SERVER = {
  flavor: "redis",
  version: "7.4.1",
  redisVersion: "7.4.1",
  mode: "standalone",
  role: "primary",
  db: 0,
  databases: 16,
  uptimeSeconds: 14_800,
  sampledAtMs: Date.parse(NOW),
  keyspace: [
    { db: 0, keys: 2_860, expires: 2_050, avgTtlMs: 147_000_000 },
    { db: 3, keys: 40, expires: 0, avgTtlMs: 0 },
  ],
  memory: { used: 8_486_736, rss: 13_590_528, peak: 8_568_056, max: 0, policy: "noeviction" },
  persistence: {
    loading: false,
    rdb: { schedule: "3600 1", changesSinceSave: 12, inProgress: false, lastStatus: "ok" },
    aof: { supported: true, enabled: false, lastWriteStatus: "ok" },
  },
  replication: { role: "primary", offset: 0, replicas: [] },
  sections: [],
}

const COMMANDSTATS = {
  commands: [
    { command: "SET", calls: 63_090, usec: 86_609, usecPerCall: 1.37 },
    // The dashboard's own reads, and whatever else asks the server about itself.
    { command: "INFO", calls: 2_400, usec: 60_000, usecPerCall: 25 },
    { command: "HSET", calls: 12_030, usec: 38_265, usecPerCall: 3.18 },
    { command: "CONFIG|GET", calls: 1, usec: 126, usecPerCall: 126 },
  ],
  totalCalls: 77_521,
  totalUsec: 185_000,
  sampledAtMs: Date.parse(NOW),
}

const TREE = {
  db: 0,
  delimiter: ":",
  prefix: "",
  folders: [
    {
      name: "session",
      prefix: "session:",
      pattern: "session:*",
      count: 1_500,
      keyCount: 1_500,
      folders: 0,
      types: { string: 1_500 },
    },
    {
      name: "user",
      prefix: "user:",
      pattern: "user:*",
      count: 800,
      keyCount: 0,
      folders: 400,
      types: { hash: 400, set: 400 },
    },
  ],
  keys: [],
  keyCount: 0,
  count: 2_300,
  types: { string: 1_500, hash: 400, set: 400 },
  scanned: 2_300,
  cursor: "0",
  complete: true,
  total: 2_300,
  elapsedMs: 4,
}

// ---- MongoDB ---------------------------------------------------------------

const mongoStats = (call: number) => ({
  server: {
    host: "mongo",
    version: "8.0.4",
    uptime: 46_000 + call * 5,
    timestamp: Date.parse(NOW) + call * 5_000,
    role: "standalone",
    storageEngine: "wiredTiger",
    connections: { current: 3, available: 97, active: 1 },
    network: { bytesIn: 1_000 + call * 500, bytesOut: 2_000 + call * 900, numRequests: 70 },
    opcounters: {
      insert: 100 + call * 10,
      query: 50 + call * 20,
      update: 5,
      delete: 1,
      getmore: 0,
      command: 600 + call * 20,
    },
    mem: { resident: 297, virtual: 902 },
    documents: { inserted: 100, returned: 500, updated: 5, deleted: 1 },
    scanned: { keys: 100, documents: 600 },
    queue: { activeReaders: 0, activeWriters: 0, queuedReaders: 0, queuedWriters: 0 },
    cache: { bytes: 67_108_864, maxBytes: 268_435_456, dirtyBytes: 0 },
  },
})

const collection = (name: string, count: number, size: number, over = {}) => ({
  name,
  type: "collection",
  system: false,
  readOnly: false,
  statsKnown: true,
  count,
  size,
  avgObjSize: 170,
  storageSize: size / 2,
  indexCount: 1,
  indexSize: 20_480,
  capped: false,
  clustered: false,
  ...over,
})
const COLLECTIONS = {
  database: "app_main",
  statsTruncated: false,
  collections: [collection("orders", 6_000, 1_048_576), collection("users", 1_500, 349_184)],
}
const PROFILER_OFF = { database: "app_main", level: 0, slowMs: 100, sampleRate: 1, entries: [] }

// ---- SQLite ----------------------------------------------------------------

const sqliteStats = (call: number) => ({
  supported: true,
  at: at(call),
  driver: "sqlite",
  version: "SQLite 3.46.0",
  role: "standalone",
  database: "/srv/notes/notes.db",
  databaseBytes: 319_488,
  counters: {},
  gauges: {
    fileBytes: 319_488,
    walBytes: 0,
    pageSize: 4_096,
    pageCount: 78,
    freelistPages: 0,
    reclaimableBytes: 0,
    tables: 3,
    indexes: 1,
  },
  facts: { journalMode: "delete", synchronous: "full", encoding: "UTF-8", foreignKeys: "true" },
  pool: null,
})
const SQLITE_FILE = {
  path: "/srv/notes/notes.db",
  fileBytes: 319_488,
  walBytes: 0,
  shmBytes: 0,
  modified: "2026-10-01T08:00:00Z",
  pageSize: 4_096,
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
  objects: { table: 3, index: 1, view: 1 },
  attached: [],
  compileOptions: [],
  sizesKnown: true,
}

/** What the home's reads answer unless a test says otherwise, by `METHOD <id><path>`. */
const ANSWERS: Record<string, Answer> = {
  "GET 1/stats": sqlStats,
  "GET 1/advisor": ADVISOR,
  "GET 1/statements": STATEMENTS,
  "GET 1/tablestats": TABLESTATS,
  "GET 1/consumers": CONSUMERS,
  "GET 1/backups": BACKUPS,
  "GET 1/schemas": SCHEMAS,
  "GET 4/stats": redisStats,
  "GET 4/redis/server": REDIS_SERVER,
  "GET 4/redis/commandstats": COMMANDSTATS,
  "GET 4/keys/tree": TREE,
  "GET 4/consumers": { ...CONSUMERS, nodes: [], edges: [] },
  "GET 4/backups": NO_BACKUPS,
  "GET 5/stats": mongoStats,
  "GET 5/mongo/collections": COLLECTIONS,
  "GET 5/mongo/profiler": PROFILER_OFF,
  "GET 5/consumers": { ...CONSUMERS, nodes: [], edges: [] },
  "GET 5/backups": NO_BACKUPS,
  "GET 5/schemas": [{ name: "app_main", size: 778_240 }],
  "GET 7/stats": sqliteStats,
  "GET 7/advisor": { ...ADVISOR, findings: [] },
  "GET 7/sqlite/file": SQLITE_FILE,
  "GET 7/tablestats": {
    ...TABLESTATS,
    tables: [{ schema: "main", table: "notes", rows: 2_000, totalBytes: 307_200 }],
  },
  "GET 7/consumers": { ...CONSUMERS, nodes: [], edges: [] },
  "GET 7/backups": NO_BACKUPS,
}

type Sent = { request: string; body: unknown }

/**
 * The section's fixture, with the home's own reads answered over it. `seen`
 * is every request made about one database's contents, in order; `sent` the
 * ones that were not reads, with what they carried.
 */
async function mockHome(
  page: Page,
  options: DatabaseMock & { answers?: Record<string, Answer>; capabilities?: string[] } = {},
) {
  const mock = await mockDatabases(page, options)
  const table = { ...ANSWERS, ...options.answers }
  const calls = new Map<string, number>()
  const seen: string[] = []
  const sent: Sent[] = []

  if (options.capabilities) {
    const capabilities = options.capabilities
    // A role between the fixture's two: it controls services and destroys nothing.
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
  await page.route("**/api/v1/docker/containers/*", (route) => route.fulfill({ json: CONTAINER }))
  await page.route("**/api/v1/databases/*/**", async (route) => {
    const url = new URL(route.request().url())
    const match = /^\/api\/v1\/databases\/(\d+)(\/.+)$/.exec(url.pathname)
    if (!match) return route.fallback()
    const method = route.request().method()
    const key = `${method} ${match[1]}${match[2]}`
    seen.push(key)
    if (method !== "GET") sent.push({ request: key, body: route.request().postDataJSON() })
    if (!Object.hasOwn(table, key)) return route.fallback()
    const call = calls.get(key) ?? 0
    calls.set(key, call + 1)
    const answer = table[key]
    const value =
      typeof answer === "function" ? (answer as (call: number) => unknown)(call) : answer
    if (isReply(value)) return route.fulfill({ status: value.status, json: value.body })
    return route.fulfill({ json: value })
  })
  return { ...mock, seen, sent, calls }
}

/**
 * A clock the test moves. The page's timers fire only when the test says so,
 * so "one reading" and "two readings" are states a test stays in for as long
 * as its assertions take, not moments it has to catch between two polls.
 */
async function holdTime(page: Page) {
  await page.clock.install({ time: new Date(NOW) })
  await page.clock.pauseAt(new Date(Date.parse(NOW) + 1_000))
}

/** Lets time pass on the page: by default, as far as the next reading. */
const tick = (page: Page, ms = 5_000) => page.clock.runFor(ms)

const block = (page: Page, name: string) => page.getByRole("region", { name, exact: true })
const tile = (page: Page, label: string) =>
  page.locator("[data-slot=stat-tile]").filter({
    has: page.locator("p.eyebrow").getByText(label, { exact: true }),
  })
const tiles = (page: Page) =>
  page
    .locator("[data-slot=stat-tile] p.eyebrow")
    .evaluateAll((labels) => labels.map((label) => label.textContent?.trim()))
/** What a block's ranked rows are called aloud, in order. */
const names = (scope: ReturnType<typeof block>) =>
  scope
    .locator("[data-slot=bar-list] button")
    .evaluateAll((rows) => rows.map((row) => row.getAttribute("aria-label")))
const where = (page: Page) => new URL(page.url()).pathname + new URL(page.url()).search
const menu = (page: Page, name = "shop") =>
  page.getByRole("button", { name: `Actions for ${name}` })
const menuWords = (page: Page) =>
  page.getByRole("menuitem").evaluateAll((items) => items.map((item) => item.textContent?.trim()))

/** What Docker says of the container a database runs in: its limits, and how it restarts. */
const CONTAINER = {
  id: "abc123",
  name: "shop-db",
  names: ["shop-db"],
  image: "postgres:16-alpine",
  state: "running",
  status: "Up 3 hours",
  inspected: true,
  memoryLimit: 536_870_912,
  cpuLimit: 1.5,
  restartPolicy: "unless-stopped",
  hasHealthcheck: true,
}

/**
 * Opens a page of the section and waits for the list of connections to have
 * answered: the assertions after it then measure the page, not how long a
 * busy machine took to serve the application.
 */
async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

/** The connection's summary with its server in a container, for the verbs that act on one. */
const IN_DOCKER = {
  source: "docker",
  container: {
    id: "abc123",
    name: "shop-db",
    image: "postgres:16-alpine",
    state: "running",
    status: "Up 3 hours",
    health: "healthy",
  },
  power: { via: "docker", start: false, stop: true, restart: true },
  managed: true,
}
const STOPPED = {
  ...IN_DOCKER,
  state: "stopped",
  ok: false,
  latencyMs: 0,
  container: { ...IN_DOCKER.container, state: "exited", status: "Exited (0) 2 days ago" },
  power: { via: "docker", start: true, stop: false, restart: false },
}

// ---------------------------------------------------------------------------

test("a SQL database's home is its figures, read off the server", async ({ page }) => {
  await holdTime(page)
  const home = await mockHome(page, {
    summaries: { 1: { ...IN_DOCKER, lastBackup: DUMP.takenAt } },
  })
  await visit(page, "/databases/1")

  // What it is, in one line: the engine, where it runs, how to reach it.
  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity).toContainText("PostgreSQL 16.4")
  await expect(identity).toContainText("shop-db")
  await expect(identity).toContainText("127.0.0.1:5432")
  await expect(identity).toContainText("answers in 3 ms")
  await expect(identity.getByRole("link", { name: "Open data" })).toHaveAttribute(
    "href",
    "/databases/1/data",
  )
  await expect(identity.getByRole("link", { name: "Query" })).toHaveAttribute(
    "href",
    "/databases/1/query",
  )

  await expect
    .poll(() => tiles(page))
    .toEqual(["Sessions", "Transactions", "Cache hit", "Size", "Tables", "Last backup"])
  await expect(tile(page, "Sessions")).toContainText("12")
  await expect(tile(page, "Sessions")).toContainText("of 100")
  await expect(tile(page, "Sessions").getByRole("meter")).toHaveAttribute("aria-valuenow", "12")
  await expect(tile(page, "Size")).toContainText("23.4 MB")
  await expect(tile(page, "Tables")).toContainText("about 69K rows")
  await expect(tile(page, "Last backup")).toContainText("3h ago")
  await expect(tile(page, "Last backup")).toContainText("pg_dump")

  // One reading is no rate: the tile says what it is waiting for.
  await expect(tile(page, "Transactions")).toContainText("The rate needs a second reading")
  await tick(page)
  // Fifty more commits, five seconds later on the answer's own clock.
  await expect(tile(page, "Transactions")).toContainText("10")
  await expect(tile(page, "Transactions")).toContainText("a second")
  await expect(tile(page, "Transactions")).toContainText("1,000 rows read · 10 written")
  await expect(tile(page, "Cache hit")).toContainText("99.0%")
  // A count that stands is counted up to, once there is time to count in.
  await expect(tile(page, "Tables")).toContainText("2")
  await expect(identity).toContainText("up 12h 46m")

  // The page reads this database, not every database.
  expect(home.asked).not.toContain("GET /databases/fleet")
})

test("the chart draws the page's own samples and says they are not a record", async ({ page }) => {
  await holdTime(page)
  await mockHome(page)
  await visit(page, "/databases/1")

  const activity = block(page, "Activity")
  await expect(activity).toContainText("Nothing here is recorded")
  await expect(tile(page, "Sessions")).toContainText("12")
  // The views are the strip every switch between views of a page wears.
  const views = activity.getByRole("tablist", { name: "What the chart shows" })
  await expect(views.getByRole("tab")).toHaveText(["Sessions", "Throughput", "Rows", "Cache"])
  await expect(views.getByRole("tab", { name: "Sessions" })).toHaveAttribute(
    "aria-selected",
    "true",
  )

  await tick(page)
  await expect(activity).toContainText(/Live readings over the last \d+s/)
  await expect(activity).toContainText("Open")
  await views.getByRole("tab", { name: "Throughput" }).click()
  await expect(activity).toContainText("Transactions")
  await expect(activity).toContainText("10/s")
  // Two readings are one rate: it is drawn, as the one point it is.
  await expect(activity.locator(".recharts-dot").first()).toBeVisible()

  // The view is the reader's arrangement of this database's home: it is there on return.
  await tick(page, 1_000)
  await page.reload()
  await expect(block(page, "Activity").getByRole("tab", { name: "Throughput" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
})

test("a chart of a few sessions counts them in ones", async ({ page }) => {
  await holdTime(page)
  await mockHome(page, {
    answers: {
      "GET 1/stats": (call: number) => ({
        ...sqlStats(call),
        connections: { total: 3, active: 1, idle: 2, idleInTransaction: 0, waiting: 0, max: 100 },
      }),
    },
  })
  await visit(page, "/databases/1")
  await expect(tile(page, "Sessions")).toContainText("3")
  await tick(page)
  // A scale fitted to three put ticks at 2.25 and 1.5 and printed "3, 2, 2, 1, 0".
  await expect
    .poll(() =>
      block(page, "Activity")
        .locator(".recharts-yAxis-tick-labels .recharts-cartesian-axis-tick-value")
        .allTextContents(),
    )
    .toEqual(["0", "1", "2", "3"])
})

test("a server that lists no session to the account says so, and charts none", async ({ page }) => {
  await holdTime(page)
  await mockHome(page, {
    answers: {
      // MySQL without PROCESS: the list comes back empty while the dashboard is on it.
      "GET 1/stats": (call: number) => ({
        ...sqlStats(call),
        connections: { total: 0, active: 0, idle: 0, idleInTransaction: 0, waiting: 0, max: 151 },
        gauges: { threadsRunning: 2 },
      }),
    },
  })
  await visit(page, "/databases/1")
  await expect(tile(page, "Sessions")).toContainText("—")
  await expect(tile(page, "Sessions")).toContainText(
    "Not listed to this account · 2 threads running",
  )
  await expect(tile(page, "Sessions")).not.toContainText("of 151")
  await expect(tile(page, "Sessions").getByRole("meter")).toHaveCount(0)
  await expect(block(page, "Activity").getByRole("tab")).toHaveText(["Throughput", "Rows", "Cache"])
})

test("what needs attention leads to the page that acts on it", async ({ page }) => {
  await mockHome(page)
  await visit(page, "/databases/1")

  const attention = block(page, "Needs attention")
  await expect(attention.getByRole("link", { name: "Advisor" })).toHaveAttribute(
    "href",
    "/databases/1/advisor",
  )
  // A finding that names a page of this database opens it.
  await attention
    .getByRole("button", { name: /No backup of this database has been taken from here/ })
    .click()
  await attention.getByRole("button", { name: "Open Backups" }).click()
  await expect(page).toHaveURL(/\/databases\/1\/backups$/)

  // One whose fix is a statement hands it to Query for review; nothing runs from here.
  await page.goBack()
  await block(page, "Needs attention")
    .getByRole("button", { name: /1 foreign key with no index/ })
    .click()
  await expect(block(page, "Needs attention")).toContainText("orders(customer_id)")
  await block(page, "Needs attention")
    .getByRole("button", { name: "Review the fix in Query" })
    .click()
  await expect(page).toHaveURL(/\/databases\/1\/query\?/)
  expect(new URL(page.url()).searchParams.get("sql")).toBe(ADVISOR.findings[1].sql)
})

test("an advisor link that leaves this database is not followed", async ({ page }) => {
  await mockHome(page, {
    answers: {
      "GET 1/advisor": {
        ...ADVISOR,
        findings: [{ ...ADVISOR.findings[0], link: "/databases/4/settings" }],
      },
    },
  })
  await visit(page, "/databases/1")
  const attention = block(page, "Needs attention")
  await attention.getByRole("button", { name: /No backup/ }).click()
  await expect(attention).toContainText("Take one now")
  await expect(attention.getByRole("button", { name: /^Open / })).toHaveCount(0)
})

test("the busiest statements are shapes on one line, and the largest tables are ways into Data", async ({
  page,
}) => {
  await mockHome(page)
  await visit(page, "/databases/1")

  const busiest = block(page, "Busiest statements")
  await expect(busiest).toContainText("SELECT * FROM orders WHERE customer_id = $1")
  await expect(busiest).toContainText("60%")
  await expect(busiest).toContainText("1,200 calls · 0.05 ms each")
  // A row is named by its opening and its share, not by four hundred characters of text.
  await expect
    .poll(() => names(busiest))
    .toEqual([
      "SELECT * FROM orders WHERE customer_id = $1 — 60% of runtime. Open Performance",
      "UPDATE carts SET total = $1 WHERE id = $2 — 20% of runtime. Open Performance",
    ])
  await expect(busiest.getByRole("link", { name: "Performance" })).toHaveAttribute(
    "href",
    "/databases/1/performance?view=statements",
  )

  const used = block(page, "Used by")
  await expect(used.getByRole("link", { name: /shop-api/ })).toHaveAttribute("href", "/deploy/7")
  await expect(used).toContainText("production · bound to it · connected now")
  await expect(used).toContainText("8 sessions")
  // The busiest consumer first; the database itself is not one of its users.
  await expect(used.locator("[data-slot=choice-row]")).toHaveCount(2)
  await expect(used.locator("[data-slot=choice-row]").first()).toContainText("shop-api")
  // The machine itself is nowhere to go: it keeps its card and has no link.
  await expect(used.getByRole("link")).toHaveCount(2)
  await expect(used.locator("[data-slot=choice-row]").last().getByRole("link")).toHaveCount(0)

  const largest = block(page, "Largest tables")
  await expect(largest).toContainText("analytics.events")
  await expect(largest).toContainText("9.4 MB")
  // Two schemas can hold a table of one name: the row's name says which.
  await largest.getByRole("button", { name: "Open public.orders in Data" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=orders")
})

test("statement statistics that are off say how to turn them on, to the role that may", async ({
  page,
}) => {
  const home = await mockHome(page, {
    answers: {
      "GET 1/statements": (call: number) => (call === 0 ? STATEMENTS_OFF : STATEMENTS),
      "POST 1/server/extensions": { ok: true },
    },
  })
  await visit(page, "/databases/1")

  const busiest = block(page, "Busiest statements")
  await expect(busiest).toContainText("This server is not counting its statements.")
  await expect(busiest).toContainText("the extension has to be created in this database")
  await busiest.getByRole("button", { name: "Turn on statement statistics" }).click()
  await expect(busiest).toContainText("SELECT * FROM orders")
  expect(home.sent).toEqual([
    { request: "POST 1/server/extensions", body: { name: "pg_stat_statements" } },
  ])
})

for (const [who, options] of [
  ["a role that does not administer", { viewer: true }],
  ["a protected connection", { rows: { 1: { readOnly: true } } }],
] as const) {
  test(`${who} is told the statistics are off and is offered no switch`, async ({ page }) => {
    await mockHome(page, {
      ...options,
      summaries: { 1: IN_DOCKER },
      answers: { "GET 1/statements": STATEMENTS_OFF, "GET 1/backups": NO_BACKUPS },
    })
    await visit(page, "/databases/1")

    await expect(block(page, "Busiest statements")).toContainText("is not counting its statements")
    await expect(page.getByRole("button", { name: "Turn on statement statistics" })).toHaveCount(0)
    // Nor any other control that writes to the server or adds to the list.
    await expect(block(page, "Databases on this server")).toContainText("shop_staging")
    await expect(page.getByRole("button", { name: /^Connect shop_staging/ })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "New database" })).toHaveCount(0)
  })
}

test("a block that could not be read says so, is tried again, and the rest stays", async ({
  page,
}) => {
  const failure = failing("pq: canceling statement due to statement timeout")
  const home = await mockHome(page, {
    answers: {
      "GET 1/tablestats": (call: number) => (call === 0 ? failure : TABLESTATS),
      "GET 1/consumers": failure,
    },
  })
  await visit(page, "/databases/1")

  // The failure is not an empty list, and not a skeleton that never ends.
  const largest = block(page, "Largest tables")
  await expect(largest).toContainText("Could not read the tables")
  await expect(largest).toContainText("statement timeout")
  await expect(largest).not.toContainText("No tables yet")
  await expect(tile(page, "Tables")).toContainText("Could not be counted")
  await expect(block(page, "Used by")).toContainText("Could not read what uses it")
  // Every other block read what it asked for.
  await expect(block(page, "Busiest statements")).toContainText("SELECT * FROM orders")
  await expect(tile(page, "Sessions")).toContainText("12")

  await largest.getByRole("button", { name: "Try again" }).click()
  await expect(largest).toContainText("analytics.events")
  // The figure counts up to its value when it is looked at, and the press
  // that retried scrolled it off the top.
  await tile(page, "Tables").scrollIntoViewIfNeeded()
  await expect(tile(page, "Tables")).toContainText("2")
  expect(home.calls.get("GET 1/tablestats")).toBe(2)
})

test("statistics that cannot be read are dashes, never zeros, with the way to try again", async ({
  page,
}) => {
  await mockHome(page, {
    answers: {
      "GET 1/stats": (call: number) =>
        call === 0 ? failing("pq: permission denied for view pg_stat_database") : sqlStats(call),
    },
  })
  await visit(page, "/databases/1")

  await expect(page.getByText("Could not read the server's statistics")).toBeVisible()
  await expect(tile(page, "Sessions")).toContainText("—")
  await expect(tile(page, "Sessions")).toContainText("Could not be read")
  await expect(tile(page, "Sessions")).not.toContainText("0")
  await expect(block(page, "Activity")).toContainText("permission denied")

  await page
    .getByRole("status")
    .filter({ hasText: "the server's statistics" })
    .getByRole("button", { name: "Try again" })
    .click()
  await expect(tile(page, "Sessions")).toContainText("12")
  await expect(page.getByText("Could not read the server's statistics")).toHaveCount(0)
})

test("a poll that fails over figures already shown leaves them, and says it stopped", async ({
  page,
}) => {
  await holdTime(page)
  await mockHome(page, {
    answers: {
      "GET 1/stats": (call: number) => (call < 2 ? sqlStats(call) : failing("connection reset")),
    },
  })
  await visit(page, "/databases/1")
  await expect(tile(page, "Sessions")).toContainText("12")
  await tick(page)
  await expect(tile(page, "Transactions")).toContainText("10")

  await tick(page)
  await expect(block(page, "Activity").getByText("Not updating")).toBeVisible()
  await expect(tile(page, "Transactions")).toContainText("10")
  await expect(tile(page, "Sessions")).toContainText("12")
  // The figures are the ones before, and each tile says so and since when.
  for (const label of ["Sessions", "Transactions", "Cache hit", "Size"]) {
    await expect(tile(page, label)).toContainText(/as of \d\d:\d\d:\d\d · not updating/)
  }
  await expect(tile(page, "Tables")).not.toContainText("not updating")
  await expect(page.getByText("Could not read the server's statistics")).toHaveCount(0)
})

test("a snapshot the engine refuses is said in its words, and the home goes on", async ({
  page,
}) => {
  await mockHome(page, {
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
  await visit(page, "/databases/1")
  await expect(page.getByText("This server's statistics are not available")).toBeVisible()
  await expect(page.getByText("permission denied for view pg_stat_database")).toBeVisible()
  await expect(tile(page, "Sessions")).toContainText("Not reported to this account")
  // A size the engine did not state is not a database of zero bytes.
  await expect(tile(page, "Size")).toContainText("—")
  await expect(tile(page, "Size")).not.toContainText("0 B")
  await expect(block(page, "Largest tables")).toContainText("analytics.events")
})

test("a key–value store has its own home and is asked nothing of a SQL server's", async ({
  page,
}) => {
  await holdTime(page)
  const home = await mockHome(page)
  await visit(page, "/databases/4")

  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity).toContainText("Redis 7.4.1")
  await expect(identity.getByRole("link", { name: "Browse keys" })).toHaveAttribute(
    "href",
    "/databases/4/data",
  )
  await expect(identity.getByRole("link", { name: "Console" })).toHaveAttribute(
    "href",
    "/databases/4/query",
  )
  await expect
    .poll(() => tiles(page))
    .toEqual(["Commands", "Memory", "Hit rate", "Clients", "Keys", "Last save"])
  await expect(tile(page, "Memory")).toContainText("8.0 MB")
  await expect(tile(page, "Memory")).toContainText("no limit set")
  await expect(tile(page, "Keys")).toContainText("28.6K")
  await expect(tile(page, "Keys")).toContainText("20.5K with an expiry")
  await expect(tile(page, "Last save")).toContainText("10m ago")
  await expect(tile(page, "Last save")).toContainText("12 changes since")
  await tick(page)
  await expect(tile(page, "Commands")).toContainText("100")
  await expect(tile(page, "Hit rate")).toContainText("75.0%")

  // No advisor on the server: what needs attention is read off its own answers.
  await expect(block(page, "Needs attention")).toContainText(
    "No backup of it has been taken from here",
  )
  // The commands that use the store are ranked; the ones that ask about it —
  // this page's own among them — are counted apart and said to be.
  const commands = block(page, "Busiest commands")
  await expect
    .poll(() => names(commands))
    .toEqual([
      "SET — 69% of the time. Open Performance",
      "HSET — 31% of the time. Open Performance",
    ])
  await expect(commands).toContainText("75,120 commands on keys")
  await expect(commands).toContainText("took another 33% of the server's time and are left out")

  await expect(block(page, "Databases on this server").getByRole("link")).toHaveText([
    /db 0/,
    /db 3/,
  ])
  await expect(
    block(page, "Databases on this server").getByRole("link", {
      name: "Browse the keys of database 3",
    }),
  ).toHaveAttribute("href", "/databases/4/data?db=3")

  const namespaces = block(page, "Largest namespaces")
  await expect(namespaces).toContainText("hash · set")
  // What the keys are by type, in the hue each type has wherever a key is drawn.
  await expect(
    namespaces.getByRole("img", { name: "Keys by type: String 1,500, Hash 400, Set 400" }),
  ).toBeVisible()
  await expect(namespaces.locator("[data-slot=key-types] li")).toHaveText([
    "String1,500",
    "Hash400",
    "Set400",
  ])
  // The keyspace is walked once, when the page opens, and not on a timer —
  // and before the first reading, so the walk is in no rate the page draws. Counted
  // here, on the home: the key browser this leads to walks it for itself.
  expect(home.calls.get("GET 4/keys/tree")).toBe(1)
  expect(home.seen.indexOf("GET 4/keys/tree")).toBeLessThan(home.seen.indexOf("GET 4/stats"))
  await namespaces.getByRole("button", { name: "Browse the keys under session:" }).click()
  await expect.poll(() => where(page)).toBe("/databases/4/data?db=0&pattern=session%3A*")

  const sql = /\/(advisor|statements|tablestats|overview|schemas|activity)$/
  expect(home.seen.filter((request) => sql.test(request))).toEqual([])
})

test("a document database has its own home, and its profiler is switched on by an administrator", async ({
  page,
}) => {
  await holdTime(page)
  const home = await mockHome(page, {
    answers: {
      "PUT 5/mongo/profiler": { database: "app_main", level: 1, slowMs: 100, sampleRate: 1 },
    },
  })
  await visit(page, "/databases/5")

  await expect(
    page.locator("[data-slot=host-identity]").getByRole("link", { name: "Open documents" }),
  ).toHaveAttribute("href", "/databases/5/data")
  await expect
    .poll(() => tiles(page))
    .toEqual(["Operations", "Connections", "Cache used", "Data size", "Collections", "Last backup"])
  await expect(tile(page, "Connections")).toContainText("of 100")
  await expect(tile(page, "Cache used")).toContainText("64.0 MB")
  await expect(tile(page, "Cache used").getByRole("meter")).toHaveAttribute("aria-valuenow", "25")
  await expect(tile(page, "Data size")).toContainText("1.3 MB")
  // Two figures for one database, each said to be what it is.
  await expect(tile(page, "Data size")).toContainText("uncompressed")
  await expect(block(page, "Databases on this server")).toContainText("760.0 KB on disk")
  await expect(tile(page, "Collections")).toContainText("about 7,500 documents")
  await expect(tile(page, "Last backup")).toContainText("Never")
  await tick(page)
  await expect(tile(page, "Operations")).toContainText("10")

  const slow = block(page, "Slowest operations")
  await expect(slow).toContainText("The profiler of app_main is off")
  await slow.getByRole("button", { name: "Record slow operations" }).click()
  await expect
    .poll(() => home.sent)
    .toEqual([{ request: "PUT 5/mongo/profiler", body: { database: "app_main", level: 1 } }])

  await block(page, "Largest collections")
    .getByRole("button", { name: /Open orders in/ })
    .click()
  await expect.poll(() => where(page)).toBe("/databases/5/data?db=app_main&collection=orders")
  expect(home.seen.filter((request) => /\/(advisor|statements|tablestats)$/.test(request))).toEqual(
    [],
  )
})

test("a file-based database is its file: no sessions, no chart, no server to reach", async ({
  page,
}) => {
  const home = await mockHome(page, {
    summaries: {
      7: {
        file: { path: "/srv/notes/notes.db", exists: true, size: 319_488, modified: NOW },
        power: {
          via: "",
          start: false,
          stop: false,
          restart: false,
          reason: "A SQLite database is a file; there is no server to start or stop.",
        },
      },
    },
  })
  await visit(page, "/databases/7")

  await expect
    .poll(() => tiles(page))
    .toEqual(["File size", "WAL size", "Pages", "Free pages", "Journal mode", "Last backup"])
  await expect(tile(page, "File size")).toContainText("312.0 KB")
  await expect(tile(page, "File size")).toContainText("3 tables · 1 index")
  await expect(tile(page, "Pages")).toContainText("× 4 KB")
  await expect(tile(page, "Journal mode")).toContainText("DELETE")

  await expect(block(page, "The file")).toContainText("/srv/notes/notes.db")
  await expect(block(page, "The file")).toContainText("3 tables · 1 index · 1 view")
  await expect(block(page, "Needs attention")).toContainText("found nothing to fix")
  await expect(block(page, "Runs as")).toContainText("A SQLite database is a file")
  await expect(page.getByRole("region", { name: "Activity" })).toHaveCount(0)
  await expect(page.getByRole("region", { name: "Reachable from" })).toHaveCount(0)
  await expect(page.getByRole("region", { name: "Databases on this server" })).toHaveCount(0)
  await page.waitForLoadState("networkidle")
  expect(home.seen.filter((request) => /\/(statements|access|schemas)$/.test(request))).toEqual([])
})

test("a stopped database keeps its home, is asked nothing, and Start brings it back", async ({
  page,
}) => {
  let summary = summaryOf(SHOP, STOPPED)
  const home = await mockHome(page, { answers: { "GET 1/backups": NO_BACKUPS } })
  await page.route("**/api/v1/databases/1", (route) => route.fulfill({ json: summary }))
  const starting = hold()
  await page.route("**/api/v1/databases/1/power", async (route) => {
    home.sent.push({ request: "POST 1/power", body: route.request().postDataJSON() })
    await starting.until
    summary = summaryOf(SHOP, IN_DOCKER)
    await route.fulfill({
      json: { action: "start", via: "docker", target: "shop-db", state: "running" },
    })
  })
  await visit(page, "/databases/1")

  await expect(page.getByText("shop is stopped")).toBeVisible()
  await expect(page.getByText("Docker says “Exited (0) 2 days ago”")).toBeVisible()
  await expect(page.locator("[data-slot=database-status]")).toHaveText("stopped")
  // Its home is still its own: where it runs, where it listens, what it kept.
  await expect(block(page, "Runs as")).toContainText("postgres:16-alpine")
  await expect(block(page, "Reachable from")).toContainText("This server only")
  await expect(block(page, "Backups")).toContainText("one can be taken once the server answers")
  // Nothing opens what cannot be opened, or dumps what cannot be dialled.
  await expect(page.getByRole("link", { name: "Open data" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Back up now" })).toHaveCount(0)
  await page.waitForLoadState("networkidle")
  expect(home.seen.filter((request) => !/\/(backups|access)$/.test(request))).toEqual([])

  // What Docker lets the container use is read from Docker, not from the engine.
  await expect(block(page, "Runs as")).toContainText("512 MB of memory · 1.5 cores")
  await expect(block(page, "Runs as")).toContainText("unless-stopped")
  await expect(
    block(page, "Runs as").getByRole("link", { name: "Open container" }),
  ).toHaveAttribute("href", "/docker/containers/shop-db")

  await page.getByRole("button", { name: "Start", exact: true }).click()
  // The change is said once, in the notice under the line, until it lands.
  const state = page.locator("[data-slot=database-state]")
  await expect(state).toHaveAttribute("role", "status")
  await expect(page.locator("[data-slot=database-changing]")).toHaveText("Starting…")
  await expect(page.locator("[data-slot=database-changing]")).toHaveCount(1)
  await expect(page.locator("[data-slot=database-status]")).toHaveCount(0)
  // The button that was pressed is gone: the keyboard is on what the press did.
  await expect(state).toBeFocused()
  // The notice is the change now, not the state it is leaving, and its command is gone.
  await expect(page.getByText("Its container shop-db is coming up")).toBeVisible()
  await expect(page.getByText("shop is stopped")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Start", exact: true })).toHaveCount(0)
  await menu(page).click()
  await expect(page.getByRole("menuitem", { name: "Starting…" })).toBeDisabled()
  await page.keyboard.press("Escape")
  expect(home.sent).toEqual([{ request: "POST 1/power", body: { action: "start" } }])

  starting.release()
  await expect(tile(page, "Sessions")).toContainText("12")
  await expect(page.locator("[data-slot=database-changing]")).toHaveCount(0)
  await expect(page.locator("[data-slot=database-status]")).toHaveText("connected")
})

test("Stop is confirmed with the server named, and the home follows it down", async ({ page }) => {
  let summary = summaryOf(SHOP, { ...IN_DOCKER, consumers: 2 })
  const home = await mockHome(page)
  await page.route("**/api/v1/databases/1", (route) => route.fulfill({ json: summary }))
  const stopping = hold()
  await page.route("**/api/v1/databases/1/power", async (route) => {
    home.sent.push({ request: "POST 1/power", body: route.request().postDataJSON() })
    await stopping.until
    summary = summaryOf(SHOP, { ...STOPPED, consumers: 2 })
    await route.fulfill({
      json: { action: "stop", via: "docker", target: "shop-db", state: "exited" },
    })
  })
  await visit(page, "/databases/1")
  await expect(tile(page, "Sessions")).toContainText("12")

  await menu(page).click()
  expect(await menuWords(page)).toEqual([
    "Check connection",
    "Restart",
    "Stop",
    "Protect",
    "Back up now",
    "Settings",
    "Forget this connection",
  ])
  await page.getByRole("menuitem", { name: "Stop", exact: true }).click()

  const dialog = page.getByRole("dialog", { name: "Stop shop" })
  await expect(dialog).toContainText("PostgreSQL 16.4")
  await expect(dialog).toContainText("shop-db")
  await expect(dialog).toContainText("2 deployment environments are bound to this database")
  // Thinking better of it hands the keyboard back to the menu it came from.
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(menu(page)).toBeFocused()
  // Nothing has been asked of the server yet.
  expect(home.sent).toEqual([])

  await menu(page).click()
  await page.getByRole("menuitem", { name: "Stop", exact: true }).click()
  await dialog.getByRole("button", { name: "Stop", exact: true }).click()

  // A home that is still answering says the change as plainly as one that is
  // not: the notice under the line, and nothing offered to open what is going.
  const state = page.locator("[data-slot=database-state]")
  await expect(state).toContainText("Stopping…")
  await expect(state).toContainText("Its container shop-db is being given time to shut down")
  await expect(page.locator("[data-slot=database-status]")).toHaveCount(0)
  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity.getByRole("button", { name: "Open data" })).toBeDisabled()
  await expect(identity.getByRole("button", { name: "Query" })).toBeDisabled()
  await expect(identity.getByRole("link", { name: "Open data" })).toHaveCount(0)
  await expect(tile(page, "Sessions")).toContainText("12")

  stopping.release()
  await expect(page.getByText("shop is stopped")).toBeVisible()
  await expect(state).toContainText("2 deployment environments are bound to it")
  expect(home.sent).toEqual([{ request: "POST 1/power", body: { action: "stop" } }])
  await expect(page.getByRole("button", { name: "Start", exact: true })).toBeVisible()
})

test("a start whose engine never answers is not reported as a start", async ({ page }) => {
  await holdTime(page)
  let summary = summaryOf(SHOP, STOPPED)
  let reads = 0
  await mockHome(page, { answers: { "GET 1/backups": NO_BACKUPS } })
  await page.route("**/api/v1/databases/1", (route) => {
    reads++
    return route.fulfill({ json: summary })
  })
  await page.route("**/api/v1/databases/1/power", (route) => {
    // The container starts; the engine inside it refuses for good.
    summary = summaryOf(SHOP, {
      ...IN_DOCKER,
      state: "unreachable",
      ok: false,
      error: "dial tcp 127.0.0.1:5432: connect: connection refused",
    })
    return route.fulfill({
      json: { action: "start", via: "docker", target: "shop-db", state: "running" },
    })
  })
  await visit(page, "/databases/1")
  await page.getByRole("button", { name: "Start", exact: true }).click()
  await expect(page.locator("[data-slot=database-changing]")).toHaveText("Starting…")

  // A minute of asking, two seconds apart, each answer read before the next
  // wait — until the page stops waiting, which is what is being measured.
  const changing = page.locator("[data-slot=database-changing]")
  for (let waited = 0; waited < 90_000 && (await changing.count()) > 0; waited += 2_000) {
    const before = reads
    await tick(page, 2_000)
    await expect.poll(async () => reads > before || (await changing.count()) === 0).toBe(true)
  }
  await tick(page, 1_000)
  await expect(page.getByText("shop is not answering yet")).toBeVisible()
  await expect(
    page.getByText("Container shop-db was started, but the engine is not accepting connections."),
  ).toBeVisible()
  await expect(page.getByText("shop started")).toHaveCount(0)
  await expect(page.locator("[data-slot=database-changing]")).toHaveCount(0)
  await expect(page.locator("[data-slot=database-state]")).toContainText("shop is not answering")
})

test("a change in flight is still said after a reload, and Start is not offered twice", async ({
  page,
}) => {
  let summary = summaryOf(SHOP, STOPPED)
  const posts: unknown[] = []
  await mockHome(page, { answers: { "GET 1/backups": NO_BACKUPS } })
  await page.route("**/api/v1/databases/1", (route) => route.fulfill({ json: summary }))
  // The request outlives the page that sent it: the reload cuts it off, the server goes on.
  await page.route("**/api/v1/databases/1/power", (route) => {
    posts.push(route.request().postDataJSON())
  })
  await visit(page, "/databases/1")
  await page.getByRole("button", { name: "Start", exact: true }).click()
  await expect(page.locator("[data-slot=database-changing]")).toHaveText("Starting…")

  await page.reload()
  await expect(page.locator("[data-slot=database-changing]")).toHaveText("Starting…")
  await expect(page.getByRole("button", { name: "Start", exact: true })).toHaveCount(0)
  await menu(page).click()
  await expect(page.getByRole("menuitem", { name: "Starting…" })).toBeDisabled()
  await page.keyboard.press("Escape")
  expect(posts).toEqual([{ action: "start" }])

  // Nobody here is waiting on the request any more: the change ends when the
  // server reads the state a start settles in.
  summary = summaryOf(SHOP, IN_DOCKER)
  await expect(tile(page, "Sessions")).toContainText("12")
  await expect(page.locator("[data-slot=database-changing]")).toHaveCount(0)
  await expect(page.locator("[data-slot=database-status]")).toHaveText("connected")
})

test("a refused change is said in the server's words and leaves the page as it was", async ({
  page,
}) => {
  await mockHome(page, { summaries: { 1: STOPPED }, answers: { "GET 1/backups": NO_BACKUPS } })
  await page.route("**/api/v1/databases/1/power", (route) =>
    route.fulfill({
      status: 409,
      json: {
        error: {
          code: "power_unavailable",
          message: "the container shop-db was removed, so there is nothing to start.",
        },
      },
    }),
  )
  await visit(page, "/databases/1")
  await page.getByRole("button", { name: "Start", exact: true }).click()

  await expect(page.getByText("Could not start shop")).toBeVisible()
  await expect(page.getByText("the container shop-db was removed")).toBeVisible()
  await expect(page.locator("[data-slot=database-changing]")).toHaveCount(0)
  await expect(page.getByText("shop is stopped")).toBeVisible()
})

test("a verb is drawn only for the role that may use it", async ({ page }) => {
  // Reading only: no verb that changes anything, started or stopped.
  await mockHome(page, { viewer: true, summaries: { 1: IN_DOCKER, 2: STOPPED } })
  await visit(page, "/databases/1")
  await expect(tile(page, "Sessions")).toContainText("12")
  await menu(page).click()
  // Settings is a page a reader may open; nothing on it writes for them.
  expect(await menuWords(page)).toEqual(["Check connection", "Settings"])
  await page.keyboard.press("Escape")
  await expect(page.getByRole("button", { name: "Back up now" })).toHaveCount(0)

  await visit(page, "/databases/2")
  await expect(page.getByText("blog is stopped")).toBeVisible()
  await expect(page.getByRole("button", { name: "Start", exact: true })).toHaveCount(0)
})

test("starting is service control; stopping, restarting and protecting ask for more", async ({
  page,
}) => {
  await mockHome(page, {
    capabilities: ["read", "service.control"],
    summaries: { 1: IN_DOCKER, 2: STOPPED },
  })
  await visit(page, "/databases/1")
  await expect(tile(page, "Sessions")).toContainText("12")
  await menu(page).click()
  expect(await menuWords(page)).toEqual(["Check connection", "Back up now", "Settings"])
  await page.keyboard.press("Escape")
  // A dump is the same capability as a start.
  await expect(page.getByRole("button", { name: "Back up now" })).toBeVisible()

  await visit(page, "/databases/2")
  await expect(page.getByRole("button", { name: "Start", exact: true })).toBeVisible()
  await menu(page, "blog").click()
  expect(await menuWords(page)).toEqual(["Check connection", "Start", "Back up now", "Settings"])
})

test("a server nothing here can start offers no verb, and says why", async ({ page }) => {
  await mockHome(page, {
    summaries: {
      1: {
        state: "stopped",
        ok: false,
        source: "host",
        container: undefined,
        power: {
          via: "",
          start: false,
          stop: false,
          restart: false,
          reason: "Several units could run this server: postgresql@15-main, postgresql@16-main.",
        },
      },
    },
    answers: { "GET 1/backups": NO_BACKUPS },
  })
  await visit(page, "/databases/1")
  await expect(page.getByText("shop is stopped")).toBeVisible()
  await expect(page.getByText("Several units could run this server").first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Start", exact: true })).toHaveCount(0)
  await menu(page).click()
  expect(await menuWords(page)).toEqual([
    "Check connection",
    "Protect",
    "Back up now",
    "Settings",
    "Forget this connection",
  ])
})

test("Protect is a property of the connection: one press, and the write controls go", async ({
  page,
}) => {
  let readOnly = false
  const puts: unknown[] = []
  await mockHome(page, { answers: { "GET 1/statements": STATEMENTS_OFF } })
  await page.route("**/api/v1/databases/", (route) =>
    route.fulfill({
      json: CONNECTIONS.map((conn) => (conn.id === 1 ? { ...conn, readOnly } : conn)),
    }),
  )
  await page.route("**/api/v1/databases/1", async (route) => {
    if (route.request().method() === "PUT") {
      puts.push(route.request().postDataJSON())
      readOnly = true
      return route.fulfill({ json: { ...SHOP, readOnly } })
    }
    return route.fulfill({ json: summaryOf({ ...SHOP, readOnly }, IN_DOCKER) })
  })
  await visit(page, "/databases/1")
  await expect(page.getByRole("button", { name: "New database" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Turn on statement statistics" })).toBeVisible()

  await menu(page).click()
  await page.getByRole("menuitem", { name: "Protect", exact: true }).click()
  await expect(page.getByText("shop is protected")).toBeVisible()
  expect(puts).toEqual([{ readOnly: true }])

  // No confirmation stood in the way: it destroys nothing and is undone the same way.
  await expect(page.locator("[data-slot=host-identity] [data-slot=tag]")).toHaveText("protected")
  await expect(page.getByRole("button", { name: "New database" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Turn on statement statistics" })).toHaveCount(0)
  await menu(page).click()
  expect(await menuWords(page)).toEqual([
    "Check connection",
    "Restart",
    "Stop",
    "Stop protecting",
    "Back up now",
    "Settings",
    "Forget this connection",
  ])
})

test("a database that refuses, and a connection that cannot be opened, each say what to do", async ({
  page,
}) => {
  const home = await mockHome(page, {
    rows: { 4: { broken: true, brokenReason: "The stored connection string no longer opens." } },
    summaries: {
      1: {
        state: "unreachable",
        ok: false,
        error: "dial tcp 127.0.0.1:5432: connect: connection refused",
        source: "host",
        power: { via: "", start: false, stop: false, restart: false },
      },
      4: {
        state: "broken",
        ok: false,
        error: "The stored connection string no longer opens.",
        source: "unknown",
        exposure: "unknown",
        power: { via: "", start: false, stop: false, restart: false },
      },
    },
    answers: { "GET 1/backups": NO_BACKUPS },
  })
  await visit(page, "/databases/1")
  await expect(page.getByText("shop is not answering")).toBeVisible()
  await expect(page.getByText("connect: connection refused")).toBeVisible()
  await expect(
    page
      .locator("p", { hasText: "The address and the password" })
      .getByRole("link", { name: "Settings" }),
  ).toHaveAttribute("href", "/databases/1/settings")
  const before = home.asked.filter((request) => request === "GET /databases/1").length
  await page.getByRole("button", { name: "Check again" }).click()
  await expect
    .poll(() => home.asked.filter((request) => request === "GET /databases/1").length)
    .toBeGreaterThan(before)
  expect(home.seen.filter((request) => request.includes("/stats"))).toEqual([])

  await visit(page, "/databases/4")
  await expect(page.getByText("This connection cannot be opened")).toBeVisible()
  await expect(page.getByText("The stored connection string no longer opens.")).toBeVisible()
  // Nothing is known about where it runs, so nothing is said about it.
  await expect(page.getByRole("region", { name: "Runs as" })).toHaveCount(0)
  expect(home.seen.filter((request) => request.startsWith("GET 4/"))).toEqual([])
})

test("Back up now starts a dump, shows it running, and lists it when it ends", async ({ page }) => {
  await holdTime(page)
  const job = {
    id: "job-1",
    kind: "database.transfer.1.backup",
    title: "Dump shop",
    status: "running",
    startedAt: NOW,
  }
  let state: "idle" | "running" | "done" = "idle"
  const home = await mockHome(page, {
    summaries: { 1: IN_DOCKER },
    answers: {
      "GET 1/backups": () =>
        state === "running" ? { ...NO_BACKUPS, job } : state === "done" ? BACKUPS : NO_BACKUPS,
      "POST 1/backup": () => {
        state = "running"
        return job
      },
    },
  })
  await page.route("**/api/v1/jobs/job-1", (route) =>
    route.fulfill({ json: { job: { ...job, status: "succeeded" }, lines: [] } }),
  )
  await visit(page, "/databases/1")

  const backups = block(page, "Backups")
  await expect(backups).toContainText("No dump of shop has been taken here")
  await expect(tile(page, "Last backup")).toContainText("Never")
  await backups.getByRole("button", { name: "Back up now" }).click()

  await expect(backups.locator("[data-slot=backup-running]")).toHaveText("Dump shop…")
  await expect(backups.getByRole("button", { name: "Back up now" })).toHaveCount(0)
  await expect(tile(page, "Last backup")).toContainText("A dump is being taken now")
  expect(home.sent).toEqual([{ request: "POST 1/backup", body: {} }])

  // A dump in flight is watched every two seconds, not every thirty.
  state = "done"
  const ended = page.waitForResponse("**/api/v1/jobs/job-1")
  await tick(page, 2_000)
  await expect(backups).toContainText("2 dumps")
  // How it ended is asked of the job, and said in a toast that draws on a timer of its own.
  await ended
  await tick(page, 1_000)
  await expect(page.getByText("Backup of shop taken")).toBeVisible()
  await expect(backups.getByRole("img", { name: "The last 2 dumps" })).toBeVisible()
  await expect(tile(page, "Last backup")).toContainText("3h ago")
  await expect(backups.getByRole("button", { name: "Back up now" })).toBeVisible()
})

test("another database of the same server is opened, connected, or made", async ({ page }) => {
  const STAGING = { ...SHOP, id: 9, name: "shop.shop_staging", database: "shop_staging" }
  const SCRATCH = { ...SHOP, id: 10, name: "shop.scratch", database: "scratch" }
  let saved = [...CONNECTIONS]
  const home = await mockHome(page, {
    answers: {
      "GET 1/schemas": [...SCHEMAS, { name: "analytics", size: 512, owner: "app" }],
      "POST 1/server/databases/connect": () => {
        saved = [...saved, STAGING]
        return STAGING
      },
      "POST 1/server/databases": () => {
        saved = [...saved, SCRATCH]
        return SCRATCH
      },
    },
  })
  await page.route("**/api/v1/databases/", (route) => route.fulfill({ json: saved }))
  for (const sibling of [STAGING, SCRATCH]) {
    await page.route(`**/api/v1/databases/${sibling.id}`, (route) =>
      route.fulfill({
        json: summaryOf(sibling, {
          flavor: "postgres",
          flavorLabel: "PostgreSQL",
          versionNumber: "16.4",
          version: "PostgreSQL 16.4",
        }),
      }),
    )
  }
  await visit(page, "/databases/1")

  const others = block(page, "Databases on this server")
  const cards = others.locator("[data-slot=choice-row]")
  await expect(cards.first()).toContainText("shop_main")
  await expect(cards.first()).toContainText("this one")
  await expect(cards.first()).toContainText("23.4 MB on disk · owned by app")
  // Every card carries the engine's mark, so the names start on one line.
  await expect(cards).toHaveCount(3)
  expect(
    new Set(
      await cards.evaluateAll((rows) =>
        rows.map((row) =>
          Math.round(
            row.querySelector("span.font-mono")!.getBoundingClientRect().x -
              row.getBoundingClientRect().x,
          ),
        ),
      ),
    ).size,
  ).toBe(1)
  expect(
    await cards.evaluateAll((rows) =>
      rows.map((row) => row.querySelectorAll("img, svg").length > 0),
    ),
  ).toEqual([true, true, true])
  await others.getByRole("button", { name: "Connect shop_staging" }).click()
  await expect(page).toHaveURL(/\/databases\/9$/)
  expect(home.sent).toEqual([
    { request: "POST 1/server/databases/connect", body: { database: "shop_staging" } },
  ])

  // Once saved, the same row is a way to it rather than a button.
  await visit(page, "/databases/1")
  await expect(
    block(page, "Databases on this server").getByRole("link", { name: /shop_staging/ }),
  ).toHaveAttribute("href", "/databases/9")

  await block(page, "Databases on this server")
    .getByRole("button", { name: "New database" })
    .click()
  const dialog = page.getByRole("dialog", { name: "New database" })
  // Closed without creating, the dialog hands the keyboard back to its button.
  await page.keyboard.press("Escape")
  await expect(
    block(page, "Databases on this server").getByRole("button", { name: "New database" }),
  ).toBeFocused()
  await block(page, "Databases on this server")
    .getByRole("button", { name: "New database" })
    .click()
  await expect(dialog).toContainText("PostgreSQL 16.4")
  await expect(dialog).toContainText("127.0.0.1:5432")
  await dialog.getByLabel("Name").fill("not a name")
  await expect(dialog.getByRole("button", { name: "Create" })).toBeDisabled()
  await dialog.getByLabel("Name").fill("scratch")
  await dialog.getByRole("button", { name: "Create" }).click()
  await expect(page).toHaveURL(/\/databases\/10$/)
  expect(home.sent.at(-1)).toEqual({
    request: "POST 1/server/databases",
    body: { name: "scratch", connect: true },
  })
})

test("where the database is reachable from is read by anyone; the firewall by an administrator", async ({
  page,
}) => {
  const home = await mockHome(page, {
    viewer: true,
    summaries: { 1: { ...IN_DOCKER, exposure: "public" } },
  })
  await visit(page, "/databases/1")
  const reach = block(page, "Reachable from")
  await expect(reach).toContainText("Anywhere")
  await expect(reach).toContainText("From Settings: the dashboard owns the binding")
  await expect(reach).not.toContainText("Firewall")
  await page.waitForLoadState("networkidle")
  // The administrator's read is not attempted for a role it would refuse.
  expect(home.asked).not.toContain("GET /databases/1/access")
})

// ---- the design system's rules, on these pages ------------------------------

/** Every visible button with no text of its own and no name from anywhere else. */
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
  // A menu drawn over the page takes the page out of the accessibility tree.
  if (overlay) return
  // The page's name is said once, to assistive technology; the rail shows where this is.
  const names = page.locator("[data-slot=page]").getByRole("heading", { level: 1 })
  await expect(names).toHaveCount(1)
  await expect(names).toHaveClass(/sr-only/)
}

/** Every label the identity line can carry, at their longest. */
const LABELLED: DatabaseMock = {
  rows: {
    1: { environment: "production-eu-west", readOnly: true },
    4: { environment: "staging" },
  },
  summaries: { 1: { ...IN_DOCKER, exposure: "public", consumers: 3 } },
}

/**
 * The identity line at whatever width it has: the name whole, and its facts
 * in lines of several, not a word to a line beside the commands.
 */
async function identityHolds(page: Page, what: string) {
  const line = page.locator("[data-slot=host-identity]")
  const name = await line
    .locator("p")
    .first()
    .evaluate((title) => ({
      cut: title.scrollWidth > title.clientWidth + 1,
      width: title.getBoundingClientRect().width,
    }))
  expect(name.cut, `the name is cut short on ${what}`).toBe(false)
  const facts = await line
    .locator("p")
    .nth(1)
    .evaluate((run) => {
      const box = run.getBoundingClientRect()
      return { width: box.width, lines: Math.round(box.height / 20) }
    })
  expect(facts.lines, `the facts run in lines on ${what}`).toBeLessThanOrEqual(3)
  // Facts that wrap do so because the line is full, not because the commands
  // left them a column a word wide.
  if (facts.lines > 1) {
    expect(facts.width, `the facts have room on ${what}`).toBeGreaterThan(280)
  }
  // No dot is left hanging where a line of facts ends, nor leads one.
  const stray = await line
    .locator("p")
    .nth(1)
    .evaluate((run) => {
      const clip = run.firstElementChild!.getBoundingClientRect()
      return [...run.querySelectorAll("[aria-hidden]")].filter((dot) => {
        const box = dot.getBoundingClientRect()
        const next = dot.nextElementSibling?.getBoundingClientRect()
        const shown = box.right > clip.left + 1 && box.left < clip.right - 1
        return shown && (!next || Math.abs(next.top - box.top) > 8)
      }).length
    })
  expect(stray, `a separator hangs at a line's end on ${what}`).toBe(0)
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 900 }],
  [" at a wide window", { width: 1720, height: 1000 }],
  [" at a small laptop's width", { width: 1024, height: 800 }],
  [" at a tablet's width", { width: 768, height: 1000 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`every engine's home keeps the design system's rules${label}`, async ({ page }) => {
    test.setTimeout(90_000)
    await page.setViewportSize(viewport)
    await holdTime(page)
    await mockHome(page, LABELLED)

    for (const conn of [SHOP, CACHE, APP, NOTES]) {
      await visit(page, `/databases/${conn.id}`)
      await expect(page.locator("[data-slot=stat-tile]").first()).toBeVisible()
      await page.waitForLoadState("networkidle")
      await tick(page)
      await page.waitForLoadState("networkidle")
      await keepsTheRules(page, `${conn.name}'s home`)
      await identityHolds(page, `${conn.name}'s home`)
    }

    // With a finding open and the database's menu drawn over the page.
    await visit(page, "/databases/1")
    await block(page, "Needs attention")
      .getByRole("button", { name: /1 foreign key/ })
      .click()
    await menu(page).click()
    await expect(page.getByRole("menuitem", { name: "Stop protecting" })).toBeVisible()
    await keepsTheRules(page, "the home with a finding and its menu open", true)
  })

  test(`a home that could not read, and one that is stopped, keep the rules${label}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport)
    const failure = failing("pq: canceling statement due to statement timeout")
    await mockHome(page, {
      ...LABELLED,
      summaries: { ...LABELLED.summaries, 2: STOPPED },
      answers: Object.fromEntries(
        ["stats", "advisor", "statements", "tablestats", "consumers", "backups", "schemas"].map(
          (name) => [`GET 1/${name}`, failure],
        ),
      ),
    })
    await visit(page, "/databases/1")
    await expect(page.getByText("Could not read the server's statistics")).toBeVisible()
    await page.waitForLoadState("networkidle")
    await keepsTheRules(page, "a home whose reads all failed")

    await visit(page, "/databases/2")
    await expect(page.getByText("blog is stopped")).toBeVisible()
    await page.waitForLoadState("networkidle")
    await keepsTheRules(page, "a stopped database's home")
  })
}

test("a long name with every label keeps its line where the rail leaves it little room", async ({
  page,
}) => {
  await mockHome(page, {
    rows: {
      1: {
        name: "orders-production-primary-eu",
        environment: "production-eu-west",
        readOnly: true,
      },
    },
    summaries: { 1: { ...IN_DOCKER, exposure: "public", consumers: 3 } },
  })
  for (const width of [768, 834, 900, 1024, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    await visit(page, "/databases/1")
    await expect(tile(page, "Sessions")).toContainText("12")
    await identityHolds(page, `a ${width}px window`)
    // Its commands are all there, and none is pushed off the line's end.
    const line = page.locator("[data-slot=host-identity]")
    await expect(line.getByRole("link", { name: "Open data" })).toBeVisible()
    await expect(line.getByRole("button", { name: /^Actions for/ })).toBeVisible()
    const edge = await line.evaluate((identity) => {
      const box = identity.getBoundingClientRect()
      return [...identity.querySelectorAll("a, button")].every((control) => {
        const at = control.getBoundingClientRect()
        return at.left >= box.left - 1 && at.right <= box.right + 1
      })
    })
    expect(edge, `a command leaves the line at ${width}px`).toBe(true)
  }
})

test("the six figures sit three across on a laptop, six where there is room, two on a phone", async ({
  page,
}) => {
  await mockHome(page)
  const columns = () =>
    page
      .locator("[data-slot=stat-grid]")
      .first()
      .evaluate((grid) => getComputedStyle(grid).gridTemplateColumns.split(" ").length)
  const edges = () =>
    page.locator("[data-slot=stat-grid] > *").evaluateAll((cells) =>
      cells.map((cell) => {
        const style = getComputedStyle(cell)
        return `${parseFloat(style.borderLeftWidth)}/${parseFloat(style.borderTopWidth)}`
      }),
    )

  await page.setViewportSize({ width: 1280, height: 900 })
  await visit(page, "/databases/1")
  await expect(tile(page, "Sessions")).toContainText("12")
  expect(await columns()).toBe(3)
  // A hairline between cells and only between them: left/top, cell by cell.
  expect(await edges()).toEqual(["0/0", "1/0", "1/0", "0/1", "1/1", "1/1"])

  await page.setViewportSize({ width: 1720, height: 900 })
  expect(await columns()).toBe(6)
  expect(await edges()).toEqual(["0/0", "1/0", "1/0", "1/0", "1/0", "1/0"])

  // Two across on a phone: six short figures are not a screen and a half.
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await columns()).toBe(2)
  expect(await edges()).toEqual(["0/0", "1/0", "0/1", "1/1", "0/1", "1/1"])
})

test("the size is drawn by what it is made of, and the list under it is the legend", async ({
  page,
}) => {
  await mockHome(page)
  await visit(page, "/databases/1")

  const size = tile(page, "Size")
  await expect(size).toContainText("23.4 MB")
  await expect(size).toContainText("on disk")
  await expect(
    size.getByRole("img", { name: "Size, by its largest parts: events 9.4 MB, orders 3.2 MB" }),
  ).toBeVisible()
  // The same two colours, in the same order, before the rows that name them.
  const swatch = (scope: ReturnType<typeof block>, selector: string) =>
    scope
      .locator(selector)
      .evaluateAll((dots) => dots.map((dot) => getComputedStyle(dot).backgroundColor))
  const legend = await swatch(size, "p span.size-1\\.5")
  expect(legend).toHaveLength(2)
  expect(new Set(legend).size).toBe(2)
  expect(
    await swatch(block(page, "Largest tables"), "[data-slot=bar-list] span.size-1\\.5"),
  ).toEqual(legend)
})

test("an empty database says so in words that fit what the reader may do", async ({ page }) => {
  const empty = { ...TABLESTATS, tables: [] }
  await mockHome(page, { answers: { "GET 1/tablestats": empty } })
  await visit(page, "/databases/1")
  await expect(tile(page, "Tables")).toContainText("no tables yet")
  await expect(tile(page, "Tables")).not.toContainText("keeps no row estimate")
  await expect(block(page, "Largest tables")).toContainText("Create one in Schema")

  // A protected connection is not told to do what would be refused.
  await mockHome(page, { rows: { 1: { readOnly: true } }, answers: { "GET 1/tablestats": empty } })
  await visit(page, "/databases/1")
  await expect(block(page, "Largest tables")).toContainText(
    "The connection is protected, so none can be made from here.",
  )
  await expect(block(page, "Largest tables")).not.toContainText("Create one")
})

test("an engine that reports no statistics has the figures it can read, and no chart", async ({
  page,
}) => {
  // A flavour whose catalogue offers no snapshot — the path Oracle and the
  // smaller PostgreSQL-compatible servers take.
  const home = await mockHome(page, {
    summaries: { 1: { ...IN_DOCKER, capabilities: { stats: false, statements: false } } },
  })
  await visit(page, "/databases/1")
  await expect.poll(() => tiles(page)).toEqual(["Tables", "Last backup"])
  await expect(page.getByRole("region", { name: "Activity" })).toHaveCount(0)
  await expect(page.getByRole("region", { name: "Busiest statements" })).toHaveCount(0)
  await expect(block(page, "Largest tables")).toContainText("analytics.events")
  await page.waitForLoadState("networkidle")
  expect(home.seen.filter((request) => /\/(stats|statements)$/.test(request))).toEqual([])
})
