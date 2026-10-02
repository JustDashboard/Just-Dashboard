import { expect as baseExpect, test, type Page } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A database's Access, Backups and Settings pages, and the verbs its menu
 * carries on every page.
 *
 * The claims here are the ones those pages make to their reader. A dump, a
 * restore and a copy are jobs on the server: the page begins one, shows it in
 * place and follows it by its id. A restore says where it goes before it
 * runs, takes a dump of what is there unless told not to, and is confirmed
 * with the database named. A save sends only what changed — a connection's
 * labels, an account's attributes, a user's rule — and a new password never
 * rewrites the options a connection string carried or the attributes an
 * account had. Deleting a database is typed for by its own name, and is not
 * offered at all where the connection names none. A control is drawn only
 * for the role that may use it, nothing that writes is drawn on a protected
 * connection, and the account the dashboard itself signs in with keeps what
 * would lock the dashboard out.
 */

// These pages make several reads on arrival, and on a machine that is busy
// with other work the last of them can land after an assertion's five seconds.
const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const NOW = "2026-10-01T09:00:00Z"
const ago = (hours: number) => new Date(Date.now() - hours * 3600_000).toISOString()

type Reply = { status: number; body: unknown }
const refusal = (code: string, message: string, status = 400, extra = {}): Reply => ({
  status,
  body: { error: { code, message, ...extra } },
})
const isReply = (value: unknown): value is Reply =>
  typeof value === "object" && value !== null && "status" in value && "body" in value

/** What a route answers: a value, or one made from how often it was asked and what it was sent. */
type Answer = unknown | ((call: number, url: URL, body: unknown) => unknown)

// ---- backups --------------------------------------------------------------

const DUMP_OPTIONS = {
  schemaOnly: true,
  dataOnly: true,
  tables: true,
  compression: true,
  databases: false,
  newDatabase: true,
}

const dump = (file: string, over: Record<string, unknown> = {}) => ({
  file,
  size: 1_090_017,
  takenAt: ago(2),
  format: "pg_dump archive",
  durationMs: 388,
  database: "shop_main",
  tool: "pg_dump",
  toolVersion: "16.12",
  summary: "written by pg_dump",
  contents: {},
  origin: "dump",
  by: "operator",
  ...over,
})

const BACKUPS = {
  dir: "/var/backups/jd/databases/shop",
  options: DUMP_OPTIONS,
  files: [
    dump("shop_main-new.dump", { note: "before the migration" }),
    dump("shop_main-safety.dump", { takenAt: ago(30), origin: "safety" }),
    dump("categories.sql", {
      takenAt: ago(50),
      origin: "upload",
      format: "SQL",
      tool: undefined,
      toolVersion: undefined,
      durationMs: undefined,
      summary: undefined,
      size: 3_317,
    }),
    dump("shop_main-tables.dump", {
      takenAt: ago(80),
      contents: { tables: ["orders", "customers"] },
      size: 251_000,
    }),
  ],
}

const job = (id: string, kind: string, status: string, over: Record<string, unknown> = {}) => ({
  id,
  kind: `database.transfer.1.${kind}`,
  title:
    kind === "backup" ? "Dump shop" : kind === "restore" ? "Restore shop" : "Copy shop to shop2",
  target: "shop",
  status,
  exitCode: 0,
  startedAt: NOW,
  startedBy: "operator",
  lines: 3,
  ...over,
})

const line = (seq: number, stream: string, text: string) => ({ seq, stream, text, at: NOW })

/** A job that is running the first time it is read and has ended the second. */
const jobThatEnds =
  (id: string, kind: string, result: Record<string, unknown>, over: Record<string, unknown> = {}) =>
  (call: number) => ({
    job: job(id, kind, call === 0 ? "running" : "succeeded", {
      endedAt: call === 0 ? undefined : NOW,
      ...over,
    }),
    lines: [
      line(1, "status", kind === "backup" ? "Dumping shop" : "Restoring shop"),
      line(2, "stdout", "pg_dump: reading schemas"),
      ...(call === 0 ? [] : [line(3, "result", JSON.stringify(result))]),
    ],
  })

// ---- settings -------------------------------------------------------------

const STORED_URL =
  "postgres://app:s3cret@127.0.0.1:5432/shop_main?sslmode=verify-full&connect_timeout=5"

const setting = (name: string, over: Record<string, unknown> = {}) => ({
  name,
  value: "1",
  editable: true,
  ...over,
})

const SETTINGS = {
  supported: true,
  all: true,
  writable: true,
  settings: [
    setting("work_mem", {
      value: "4096",
      unit: "kB",
      category: "Resource Usage / Memory",
      description: "Sets the maximum memory to be used for query workspaces.",
      type: "integer",
      default: "4096",
      min: "64",
      max: "2147483647",
      context: "user",
    }),
    setting("shared_buffers", {
      value: "32768",
      unit: "8kB",
      category: "Resource Usage / Memory",
      description: "Sets the number of shared memory buffers used by the server.",
      type: "integer",
      default: "16384",
      min: "16",
      max: "1073741823",
      context: "postmaster",
      restartRequired: true,
      pendingRestart: true,
      changed: true,
    }),
    setting("bgwriter_delay", {
      value: "210",
      unit: "ms",
      category: "Resource Usage / Background Writer",
      description: "Background writer sleep time between rounds.",
      type: "integer",
      default: "200",
      min: "10",
      max: "10000",
      context: "sighup",
      changed: true,
    }),
    setting("wal_level", {
      value: "replica",
      category: "Write-Ahead Log / Settings",
      type: "enum",
      enum: ["minimal", "replica", "logical"],
      default: "replica",
      context: "postmaster",
      restartRequired: true,
    }),
    setting("fsync", {
      value: "on",
      category: "Write-Ahead Log / Settings",
      type: "bool",
      default: "on",
      context: "sighup",
    }),
    setting("server_version", {
      value: "16.4",
      category: "Preset Options",
      type: "string",
      context: "internal",
      editable: false,
    }),
    setting("primary_conninfo", {
      value: "",
      category: "Replication / Standby Servers",
      type: "string",
      redacted: true,
      editable: false,
    }),
  ],
}

const REDIS_CONFIG = {
  supported: true,
  rewritable: true,
  configFile: "/etc/redis/redis.conf",
  groups: [
    {
      name: "memory",
      params: [
        { name: "maxmemory", value: "0" },
        { name: "maxmemory-policy", value: "noeviction" },
      ],
    },
    { name: "security", params: [{ name: "requirepass", value: "", secret: true, set: true }] },
    { name: "logging", params: [{ name: "slowlog-max-len", value: "128" }] },
  ],
}

const EXTENSIONS = {
  supported: true,
  editable: true,
  extensions: [
    {
      name: "pg_stat_statements",
      version: "1.10",
      availableVersion: "1.10",
      installed: true,
      schema: "public",
      comment: "track planning and execution statistics of all SQL statements executed",
    },
    {
      name: "pgcrypto",
      availableVersion: "1.3",
      installed: false,
      comment: "cryptographic functions",
    },
  ],
}

const SERVER_DATABASES = [
  { name: "shop_main", size: 24_000_000, owner: "app", encoding: "UTF8" },
  { name: "shop_staging", size: 8_000_000, owner: "app", encoding: "UTF8" },
]

const ACCESS = {
  detected: true,
  container: "shop-db",
  managed: true,
  exposure: "local",
  port: 5432,
  publicAddresses: ["203.0.113.7"],
  firewall: { backend: "ufw", active: true, open: false, editable: true },
}

const CONSUMERS = {
  checkedAt: NOW,
  nodes: [
    { id: "db:1", kind: "database", name: "shop", connId: 1 },
    { id: "deploy:7", kind: "deployment", name: "storefront", href: "/deploy/7" },
  ],
  edges: [
    { from: "deploy:7", to: "db:1", via: ["binding", "network"], sessions: 2, status: "connected" },
  ],
}

// ---- access ---------------------------------------------------------------

const role = (name: string, over: Record<string, unknown> = {}) => ({
  name,
  login: true,
  superuser: false,
  createDb: false,
  createRole: false,
  connectionLimit: -1,
  connections: 0,
  ...over,
})

const ROLES = {
  supported: true,
  roles: [
    role("app", { superuser: true, createDb: true, createRole: true, connections: 3 }),
    role("reporting", { connections: 1 }),
    role("readers", { login: false }),
    role("pg_monitor", { login: false, system: true }),
  ],
}

const PRIVILEGES = {
  supported: true,
  editable: [
    "password",
    "login",
    "superuser",
    "createDb",
    "createRole",
    "connectionLimit",
    "validUntil",
    "inherit",
    "replication",
    "bypassRls",
  ],
  levels: [
    {
      level: "database",
      privileges: ["CONNECT", "CREATE", "TEMPORARY", "ALL"],
      needs: ["database"],
      grantOption: true,
    },
    {
      level: "table",
      privileges: ["SELECT", "INSERT", "UPDATE", "DELETE", "ALL"],
      needs: ["schema", "table"],
      allObjects: true,
      future: true,
      grantOption: true,
    },
    { level: "role", privileges: [], needs: ["memberOf"] },
  ],
}

const tableGrant = (table: string, privileges: string[], over: Record<string, unknown> = {}) => ({
  grantee: "reporting",
  level: "table",
  database: "shop_main",
  schema: "public",
  table,
  privileges,
  grantor: "app",
  ...over,
})

const REPORTING_GRANTS = [
  {
    grantee: "reporting",
    level: "database",
    database: "shop_main",
    privileges: ["CONNECT"],
    grantor: "app",
  },
  tableGrant("customers", ["SELECT"]),
  tableGrant("orders", ["SELECT"]),
]

const detail = (name: string, over: Record<string, unknown> = {}) => ({
  ...(ROLES.roles.find((one) => one.name === name) ?? role(name)),
  attributes: { inherit: true, replication: false, bypassRls: false },
  members: [],
  config: [],
  grants: [],
  grantsTruncated: false,
  editable: PRIVILEGES.editable,
  ...over,
})

const CATALOG = {
  schema: "public",
  defaultSchema: "public",
  schemas: [
    { name: "public", default: true },
    { name: "pg_catalog", system: true },
  ],
  objects: {
    tables: [
      { kind: "table", schema: "public", name: "customers" },
      { kind: "table", schema: "public", name: "orders" },
      { kind: "table", schema: "public", name: "products" },
    ],
    views: [],
    sequences: [],
  },
}

const ACL = {
  supported: true,
  users: [
    {
      name: "default",
      enabled: true,
      noPassword: true,
      passwords: 0,
      keys: ["~*"],
      channels: ["&*"],
      commands: ["+@all"],
      unrestricted: true,
      system: true,
      self: true,
      rule: "on nopass ~* &* +@all",
    },
    {
      name: "worker",
      enabled: true,
      noPassword: false,
      passwords: 1,
      keys: ["~jobs:*"],
      channels: ["resetchannels"],
      commands: ["-@all", "+@read", "+@write"],
      unrestricted: false,
      system: false,
      self: false,
      rule: "on ~jobs:* resetchannels -@all +@read +@write",
    },
  ],
}

const MONGO_USERS = {
  users: [
    {
      user: "root",
      db: "admin",
      roles: [{ role: "root", db: "admin" }],
      mechanisms: ["SCRAM-SHA-256"],
      superuser: true,
      self: true,
    },
    {
      user: "api",
      db: "app_main",
      roles: [{ role: "readWrite", db: "app_main" }],
      mechanisms: ["SCRAM-SHA-256"],
      superuser: false,
      self: false,
    },
  ],
}

const mongoRoles = (_call: number, url: URL) => ({
  database: url.searchParams.get("database"),
  roles: [
    {
      role: "auditors",
      db: url.searchParams.get("database"),
      builtin: false,
      roles: [{ role: "read", db: "app_main" }],
      privileges: [{ resource: '{"db":"app_main","collection":"audit"}', actions: ["find"] }],
    },
    ...["dbAdmin", "dbOwner", "read", "readWrite"].map((name) => ({
      role: name,
      db: url.searchParams.get("database"),
      builtin: true,
      roles: [],
      privileges: [],
    })),
  ],
})

/** What every route of the three pages answers unless a test says otherwise. */
const ANSWERS: Record<string, Answer> = {
  "GET 1/backups": BACKUPS,
  "GET 1/access": ACCESS,
  "GET 1/url": { id: 1, name: "shop", driver: "postgres", reference: "", url: STORED_URL },
  "GET 1/settings": SETTINGS,
  "GET 1/server/extensions": EXTENSIONS,
  "GET 1/schemas": SERVER_DATABASES,
  "GET 1/consumers": CONSUMERS,
  "GET 1/server/roles": ROLES,
  "GET 1/server/privileges": PRIVILEGES,
  "GET 1/server/grants": { supported: true, truncated: false, grants: REPORTING_GRANTS },
  "GET 1/server/roles/reporting": detail("reporting", { grants: REPORTING_GRANTS }),
  "GET 1/server/roles/app": detail("app"),
  "GET 1/catalog": CATALOG,
  "GET 4/backups": {
    dir: "/var/backups/jd/databases/cache",
    files: [],
    options: {
      ...DUMP_OPTIONS,
      schemaOnly: false,
      dataOnly: false,
      tables: false,
      compression: false,
      databases: true,
    },
  },
  "GET 4/access": { ...ACCESS, container: "cache-db", port: 6379 },
  "GET 4/redis/config": REDIS_CONFIG,
  "GET 4/redis/server": { modules: [{ name: "ReJSON", version: 20609 }] },
  "GET 4/redis/acl": ACL,
  "GET 4/schemas": [{ name: "0", size: 12 }, { name: "1" }, { name: "3", size: 4 }],
  "GET 5/backups": {
    dir: "/var/backups/jd/databases/app",
    files: [],
    options: { ...DUMP_OPTIONS, schemaOnly: false, dataOnly: false },
  },
  "GET 5/access": { ...ACCESS, container: "app-db", port: 27017 },
  "GET 5/settings": {
    supported: true,
    settings: [
      { name: "connections.current", value: "4", category: "connections", editable: false },
    ],
  },
  "GET 5/schemas": [
    { name: "admin", size: 100 },
    { name: "app_main", size: 2000 },
  ],
  "GET 5/mongo/users": MONGO_USERS,
  "GET 5/mongo/roles": mongoRoles,
  "GET 7/backups": {
    dir: "/var/backups/jd/databases/notes",
    files: [
      dump("notes.sqlite", {
        format: "SQLite file",
        tool: "built-in",
        database: "/srv/notes/notes.db",
      }),
    ],
    options: { ...DUMP_OPTIONS, newDatabase: false },
  },
  "GET 7/settings": {
    supported: true,
    all: true,
    writable: true,
    settings: [
      setting("journal_mode", {
        value: "delete",
        category: "Durability",
        type: "enum",
        enum: ["delete", "truncate", "persist", "wal"],
        context: "file",
      }),
    ],
  },
}

type Sent = { request: string; body: unknown; query: string; confirm: string | null }

/**
 * The section's fixture, with the routes of the three pages answered over it.
 * `seen` is every request made about one database, in order; `sent` the ones
 * that were not reads, with what they carried. Routes outside a database —
 * the jobs, the scheduled backups, the connection test — are answered from
 * the same table, keyed by their path.
 */
async function mockOperate(
  page: Page,
  options: DatabaseMock & { answers?: Record<string, Answer>; capabilities?: string[] } = {},
) {
  const mock = await mockDatabases(page, options)
  const answers: Record<string, Answer> = {
    "GET /jobs/": [],
    "GET /backups/": [],
    ...ANSWERS,
    ...options.answers,
  }
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
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    const within = /^\/databases\/(\d+)(\/.+)?$/.exec(path)
    // One database's own row is the fixture's to read; what is written to it is this spec's.
    if (within && !within[2] && method === "GET") return route.fallback()
    const key = within ? `${method} ${within[1]}${within[2] ?? ""}` : `${method} ${path}`
    if (within) seen.push(key)
    let body: unknown
    if (method !== "GET") {
      try {
        body = route.request().postDataJSON()
      } catch {
        // A multipart upload has no JSON body.
        body = undefined
      }
      sent.push({
        request: key,
        body,
        query: url.search,
        confirm: route.request().headers()["x-confirm"] ?? null,
      })
    }
    if (!Object.hasOwn(answers, key)) return route.fallback()
    const call = calls.get(key) ?? 0
    calls.set(key, call + 1)
    const answer = answers[key]
    const value = await (typeof answer === "function"
      ? (answer as (call: number, url: URL, body: unknown) => unknown)(call, url, body)
      : answer)
    if (isReply(value)) return route.fulfill({ status: value.status, json: value.body })
    return route.fulfill({ json: value }).catch(() => undefined)
  })
  return { ...mock, seen, sent, calls }
}

/** Opens a page of the section and waits for the list of connections to have answered. */
async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const LIMITED = ["read", "service.control"]

const dialog = (page: Page) => page.getByRole("dialog")
const tile = (page: Page, label: string) =>
  page.locator("[data-slot=stat-tile]").filter({
    has: page.locator("p.eyebrow").getByText(label, { exact: true }),
  })
const dumps = (page: Page) => page.getByRole("region", { name: "Dumps" })
const dumpRow = (page: Page, file: string) => dumps(page).getByRole("row").filter({ hasText: file })
const progress = (page: Page) => page.locator("[data-slot=transfer-progress]")
const section = (page: Page, id: string) => page.locator(`section#${id}`)
const where = (page: Page) => new URL(page.url()).pathname + new URL(page.url()).search

// ---------------------------------------------------------------------------
// Backups
// ---------------------------------------------------------------------------

test("backups open on when it was last backed up and every dump with what it holds", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/1/backups")

  await expect(tile(page, "Last backup")).toContainText("2h")
  await expect(tile(page, "Size")).toContainText("1.0 MB")
  await expect(tile(page, "Kept")).toContainText("4")
  await expect(tile(page, "Schedule")).toContainText("None")

  await expect(dumpRow(page, "shop_main-new.dump")).toContainText("before the migration")
  await expect(dumpRow(page, "shop_main-new.dump")).toContainText("Everything")
  await expect(dumpRow(page, "shop_main-new.dump")).toContainText("pg_dump")
  await expect(dumpRow(page, "shop_main-safety.dump")).toContainText("safety dump")
  await expect(dumpRow(page, "categories.sql")).toContainText("uploaded")
  await expect(dumpRow(page, "shop_main-tables.dump")).toContainText("2 tables")
  // An uploaded file says what it is, not that it holds everything.
  await expect(dumpRow(page, "categories.sql")).not.toContainText("Everything")
  await expect(dumps(page)).toContainText("/var/backups/jd/databases/shop")
})

test("a dump is begun with the options chosen, shown in place, and followed to its end", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/backup": job("j1", "backup", "running"),
      "GET /jobs/j1": jobThatEnds("j1", "backup", {
        file: "shop_main-now.dump",
        size: 2048,
        summary: "written by pg_dump",
        tool: "pg_dump",
      }),
      "GET 1/tablestats": {
        supported: true,
        truncated: false,
        tables: [
          { schema: "public", table: "orders" },
          { schema: "public", table: "customers" },
        ],
      },
    },
  })
  await visit(page, "/databases/1/backups")
  await page.getByRole("button", { name: "Back up now" }).click()

  await dialog(page).getByRole("radio", { name: "Structure only" }).click()
  await dialog(page).getByRole("radio", { name: "Only some" }).click()
  // Nothing ticked yet: there is nothing to dump.
  await expect(dialog(page).getByRole("button", { name: "Back up now" })).toBeDisabled()
  await dialog(page).getByText("orders", { exact: true }).click()
  await dialog(page).getByLabel("Note").fill("before the migration")
  await dialog(page).getByRole("button", { name: "Back up now" }).click()

  await expect(progress(page)).toBeVisible()
  await expect(progress(page)).toContainText("pg_dump: reading schemas")
  await expect(progress(page)).toHaveAttribute("aria-label", "Dump shop, succeeded")
  await expect(progress(page)).toContainText("shop_main-now.dump · 2.0 KB · written by pg_dump")
  // The job is in the address: a reload shows the same one.
  expect(where(page)).toBe("/databases/1/backups?job=j1")

  expect(server.sent.find((one) => one.request === "POST 1/backup")?.body).toEqual({
    schemaOnly: true,
    tables: ["orders"],
    note: "before the migration",
  })
  // Its end is read from the list again, which is where the new dump shows.
  expect(server.seen.filter((one) => one === "GET 1/backups").length).toBeGreaterThan(1)

  await progress(page).getByRole("button", { name: "Dismiss" }).click()
  await expect(progress(page)).toHaveCount(0)
  await expect.poll(() => where(page)).toBe("/databases/1/backups")
})

test("a transfer already running is shown on arrival, and can be stopped by the role that may", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "GET 1/backups": { ...BACKUPS, job: job("j7", "restore", "running") },
      "GET /jobs/j7": {
        job: job("j7", "restore", "running"),
        lines: [line(1, "status", "Restoring shop"), line(2, "stdout", "12 statements applied")],
      },
      "POST /jobs/j7/cancel": {},
    },
  })
  await visit(page, "/databases/1/backups")

  await expect(progress(page)).toHaveAttribute("aria-label", "Restore shop, running")
  await expect(progress(page)).toContainText("12 statements applied")
  // One transfer at a time: the commands that would begin another wait.
  await expect(page.getByRole("button", { name: "Back up now" })).toBeDisabled()
  await expect(
    dumpRow(page, "shop_main-new.dump").getByRole("button", { name: /^Restore/ }),
  ).toBeDisabled()

  await progress(page).getByRole("button", { name: "Stop" }).click()
  await expect.poll(() => server.sent.map((one) => one.request)).toContain("POST /jobs/j7/cancel")
})

test("a restore says where it goes, takes a safety dump first, and is confirmed with the database named", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/restore": job("j2", "restore", "running"),
      "GET /jobs/j2": jobThatEnds("j2", "restore", {
        file: "shop_main-new.dump",
        database: "shop_main",
        safetyDump: "shop_main-before.dump",
      }),
    },
  })
  await visit(page, "/databases/1/backups")
  await dumpRow(page, "shop_main-new.dump")
    .getByRole("button", { name: "Restore shop_main-new.dump" })
    .click()

  // The target is the first thing the dialog says.
  await expect(dialog(page).getByRole("button", { name: /This database/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(dialog(page)).toContainText("What shop_main holds now is replaced")
  await expect(
    dialog(page).getByRole("switch", { name: /Take a dump of shop_main as it is now/ }),
  ).toBeChecked()
  await dialog(page).getByRole("button", { name: "Restore…" }).click()

  // The ordinary confirmation, with the database named and no phrase to type.
  const confirm = page.getByRole("dialog", { name: "Restore over shop_main" })
  await expect(confirm).toContainText("shop_main-new.dump")
  await expect(confirm).toContainText("A dump of the database as it is now is taken first")
  await expect(confirm.getByRole("textbox")).toHaveCount(0)
  await confirm.getByRole("button", { name: "Restore", exact: true }).click()

  await expect(progress(page)).toHaveAttribute("aria-label", "Restore shop, succeeded")
  await expect(progress(page)).toContainText("safety dump shop_main-before.dump")
  expect(server.sent.find((one) => one.request === "POST 1/restore")?.body).toEqual({
    file: "shop_main-new.dump",
    target: "this",
    dumpFirst: true,
  })
})

test("a restore into a new database touches nothing that exists, and ends on the way into it", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/restore": job("j3", "restore", "running", { title: "Restore into shop_copy" }),
      "GET /jobs/j3": jobThatEnds(
        "j3",
        "restore",
        { file: "shop_main-new.dump", database: "shop_copy" },
        { title: "Restore into shop_copy" },
      ),
      "POST 1/server/databases/connect": { id: 9, name: "shop.shop_copy", driver: "postgres" },
    },
  })
  await visit(page, "/databases/1/backups")
  await dumpRow(page, "shop_main-new.dump")
    .getByRole("button", { name: "Restore shop_main-new.dump" })
    .click()
  await dialog(page)
    .getByRole("button", { name: /A new database/ })
    .click()

  const name = dialog(page).getByLabel("Name of the new database")
  const run = dialog(page).getByRole("button", { name: "Restore into it" })
  await expect(run).toBeDisabled()
  await name.fill("shop-copy")
  await expect(dialog(page)).toContainText("Letters, digits and underscores")
  await name.fill("shop_main")
  await expect(dialog(page)).toContainText("That is the database this connection is on")
  await expect(run).toBeDisabled()
  await name.fill("shop_copy")
  await run.click()

  // No second question: nothing that exists is replaced.
  await expect(progress(page)).toHaveAttribute("aria-label", "Restore into shop_copy, succeeded")
  expect(server.sent.find((one) => one.request === "POST 1/restore")?.body).toEqual({
    file: "shop_main-new.dump",
    target: { newDatabase: "shop_copy" },
  })
  await page.getByRole("button", { name: "Connect shop_copy" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 1/server/databases/connect")?.body)
    .toEqual({ database: "shop_copy" })
})

test("a copy is a new database beside this one, and a transfer already running is shown instead", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/copy": (call: number) =>
        call === 0
          ? refusal("transfer_running", "a transfer is running", 409, { resource: "j8" })
          : job("j4", "copy", "running"),
      "GET /jobs/j8": {
        job: job("j8", "backup", "succeeded", { endedAt: NOW }),
        lines: [line(1, "status", "Dumping shop")],
      },
      "GET /jobs/j4": jobThatEnds("j4", "copy", { database: "shop2" }),
    },
  })
  await visit(page, "/databases/1/backups")
  await page.getByRole("button", { name: "Copy database" }).click()
  const run = dialog(page).getByRole("button", { name: "Copy", exact: true })
  await expect(run).toBeDisabled()
  await dialog(page).getByLabel("Name of the copy").fill("shop_main")
  await expect(dialog(page)).toContainText("That is the database this connection is on")
  await dialog(page).getByLabel("Name of the copy").fill("shop2")
  await dialog(page)
    .getByRole("switch", { name: /Copy the structure only/ })
    .click()
  await run.click()

  // Somebody else's transfer is on: the page shows that one, and starts nothing.
  await expect(dialog(page)).toHaveCount(0)
  await expect(progress(page)).toHaveAttribute("aria-label", "Dump shop, succeeded")
  await progress(page).getByRole("button", { name: "Dismiss" }).click()

  await page.getByRole("button", { name: "Copy database" }).click()
  await dialog(page).getByLabel("Name of the copy").fill("shop2")
  await dialog(page).getByRole("button", { name: "Copy", exact: true }).click()
  await expect(progress(page)).toHaveAttribute("aria-label", "Copy shop to shop2, succeeded")
  await expect(page.getByRole("button", { name: "Connect shop2" })).toBeVisible()
  expect(server.sent.filter((one) => one.request === "POST 1/copy").map((one) => one.body)).toEqual(
    [{ name: "shop2", structureOnly: true }, { name: "shop2" }],
  )
})

test("an uploaded dump is named before it is sent, and a refusal stays in the dialog", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/backups/upload": (call: number) =>
        call === 0
          ? refusal("too_large", "the file is larger than this server accepts", 413)
          : dump("from-elsewhere.sql", { origin: "upload", format: "SQL" }),
    },
  })
  await visit(page, "/databases/1/backups")
  await page.getByRole("button", { name: "Upload a dump" }).click()

  const send = dialog(page).getByRole("button", { name: "Upload", exact: true })
  await expect(send).toBeDisabled()
  await dialog(page)
    .getByLabel("The dump")
    .setInputFiles({
      name: "from elsewhere.sql",
      mimeType: "text/plain",
      buffer: Buffer.from("SELECT 1;"),
    })
  // A space is not a name the server takes: the file's own is offered without it.
  await expect(dialog(page).getByLabel("Kept as")).toHaveValue("from-elsewhere.sql")
  await dialog(page).getByLabel("Kept as").fill("categories.sql")
  await expect(dialog(page)).toContainText("already kept here")
  await expect(send).toBeDisabled()
  await dialog(page).getByLabel("Kept as").fill("from-elsewhere.sql")
  await dialog(page).getByLabel("Note").fill("the old server")

  await send.click()
  await expect(dialog(page).getByRole("alert")).toContainText("larger than this server accepts")
  await send.click()
  await expect(dialog(page)).toHaveCount(0)
  const upload = server.sent.filter((one) => one.request === "POST 1/backups/upload").at(-1)
  expect(new URLSearchParams(upload?.query).get("name")).toBe("from-elsewhere.sql")
  expect(new URLSearchParams(upload?.query).get("note")).toBe("the old server")
})

test("deleting a dump is asked with the dump named", async ({ page }) => {
  const server = await mockOperate(page, { answers: { "DELETE 1/backups": { ok: true } } })
  await visit(page, "/databases/1/backups")
  await dumpRow(page, "shop_main-tables.dump")
    .getByRole("button", { name: "Delete shop_main-tables.dump" })
    .click()
  await expect(dialog(page)).toContainText("shop_main-tables.dump")
  await expect(dialog(page).getByRole("textbox")).toHaveCount(0)
  await dialog(page).getByRole("button", { name: "Delete", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "DELETE 1/backups")?.body)
    .toEqual({ file: "shop_main-tables.dump" })
})

test("each role is offered what its capabilities allow on the dumps, and no more", async ({
  page,
}) => {
  // May control services, may not destroy or administer: a dump and a download, nothing else.
  await mockOperate(page, { capabilities: LIMITED })
  await visit(page, "/databases/1/backups")
  await expect(page.getByRole("button", { name: "Back up now" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Upload a dump" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy database" })).toHaveCount(0)
  const row = dumpRow(page, "shop_main-new.dump")
  await expect(row.getByRole("button", { name: /^Download/ })).toBeVisible()
  await expect(row.getByRole("button", { name: /^Restore/ })).toHaveCount(0)
  await expect(row.getByRole("button", { name: /^Delete/ })).toHaveCount(0)
})

test("a viewer reads the dumps and is offered nothing that takes, moves or removes one", async ({
  page,
}) => {
  await mockOperate(page, { viewer: true })
  await visit(page, "/databases/1/backups")
  await expect(dumpRow(page, "shop_main-new.dump")).toBeVisible()
  await expect(page.getByRole("button", { name: "Back up now" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Upload a dump" })).toHaveCount(0)
  await expect(dumps(page).getByRole("row").getByRole("button")).toHaveCount(0)
})

test("a protected connection keeps its dumps and offers no restore and no copy", async ({
  page,
}) => {
  await mockOperate(page, { rows: { 1: { readOnly: true } } })
  await visit(page, "/databases/1/backups")
  await expect(page.getByRole("button", { name: "Back up now" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy database" })).toHaveCount(0)
  const row = dumpRow(page, "shop_main-new.dump")
  await expect(row.getByRole("button", { name: /^Download/ })).toBeVisible()
  await expect(row.getByRole("button", { name: /^Restore/ })).toHaveCount(0)
  await expect(dumps(page)).toContainText("This connection is protected")
})

test("dumps that could not be read say so and are tried again; figures are dashes, never zeros", async ({
  page,
}) => {
  let failing = true
  await mockOperate(page, {
    answers: {
      "GET 1/backups": () =>
        failing ? refusal("internal", "the dump directory could not be listed", 500) : BACKUPS,
    },
  })
  await visit(page, "/databases/1/backups")
  await expect(dumps(page).getByRole("status")).toContainText("Could not read the dumps")
  await expect(dumps(page)).toContainText("the dump directory could not be listed")
  await expect(tile(page, "Last backup")).toContainText("could not be read")
  await expect(tile(page, "Kept")).not.toContainText("0")

  failing = false
  await dumps(page).getByRole("button", { name: "Try again" }).click()
  await expect(dumpRow(page, "shop_main-new.dump")).toBeVisible()
})

test("a database with no dump says what to do, and never backed up is a warning", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/5/backups")
  await expect(tile(page, "Last backup")).toContainText("Never")
  await expect(dumps(page)).toContainText("No dump of app is kept here")
  await expect(dumps(page).getByRole("button", { name: "Back up now" }).last()).toBeVisible()
})

test("an engine that keeps one database per connection offers no copy and no new database to restore into", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/7/backups")
  await expect(page.getByRole("button", { name: "Back up now" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy database" })).toHaveCount(0)
  await dumpRow(page, "notes.sqlite")
    .getByRole("button", { name: /^Restore/ })
    .click()
  await expect(dialog(page)).toContainText("Restores into /srv/notes/notes.db")
  await expect(dialog(page).getByRole("button", { name: /A new database/ })).toHaveCount(0)
  await expect(dialog(page)).toContainText(".bak")
})

test("a key–value server's dump names its numbered databases", async ({ page }) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 4/backup": job("j9", "backup", "running"),
      "GET /jobs/j9": jobThatEnds("j9", "backup", {}),
    },
  })
  await visit(page, "/databases/4/backups")
  await page.getByRole("button", { name: "Back up now" }).first().click()
  // Only the databases that hold keys are offered.
  const numbered = dialog(page).getByRole("group", { name: "Numbered databases that hold keys" })
  await expect(numbered.getByRole("button")).toHaveCount(2)
  await numbered.getByRole("button", { name: /db 3/ }).click()
  await dialog(page).getByRole("button", { name: "Back up now" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 4/backup")?.body)
    .toEqual({ databases: [3] })
})

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

test("settings are a form in sections, with only the sections the engine has", async ({ page }) => {
  await mockOperate(page)
  await visit(page, "/databases/1/settings")
  for (const id of [
    "connection",
    "reachability",
    "parameters",
    "extensions",
    "databases",
    "danger",
  ]) {
    await expect(section(page, id)).toBeVisible()
  }
  await expect(section(page, "parameters").getByRole("heading", { level: 3 })).toHaveText(
    "Server parameters",
  )

  // A file has no server: no reach, no extensions, no neighbours.
  await visit(page, "/databases/7/settings")
  await expect(section(page, "connection")).toBeVisible()
  await expect(section(page, "parameters").getByRole("heading", { level: 3 })).toHaveText(
    "Parameters",
  )
  await expect(section(page, "danger")).toBeVisible()
  for (const id of ["reachability", "extensions", "databases"]) {
    await expect(section(page, id)).toHaveCount(0)
  }
})

test("saving the connection's labels sends only what changed", async ({ page }) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 1": (_call: number, _url: URL, body: unknown) => ({ id: 1, ...(body as object) }),
    },
  })
  await visit(page, "/databases/1/settings")
  const form = page.getByRole("form", { name: "Connection labels" })
  const save = form.getByRole("button", { name: "Save", exact: true })
  await expect(save).toBeDisabled()

  await form.getByLabel("Notes").fill("the shop's orders")
  await expect(form).toContainText("1 unsaved change")
  await save.click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1")?.body)
    .toEqual({
      notes: "the shop's orders",
    })
})

test("a name the server would refuse is said before it is sent", async ({ page }) => {
  await mockOperate(page)
  await visit(page, "/databases/1/settings")
  const form = page.getByRole("form", { name: "Connection labels" })
  await form.getByLabel("Name").fill("blog")
  await expect(form.getByRole("alert")).toContainText("Another connection already has this name")
  await expect(form.getByRole("button", { name: "Save", exact: true })).toBeDisabled()
  await form.getByLabel("Name").fill("")
  await expect(form.getByRole("alert")).toContainText("A connection needs a name")
  await form.getByRole("button", { name: "Discard" }).click()
  await expect(form.getByLabel("Name")).toHaveValue("shop")
})

test("protecting a connection is one switch of the same form", async ({ page }) => {
  const server = await mockOperate(page, { answers: { "PUT 1": { id: 1 } } })
  await visit(page, "/databases/1/settings")
  const form = page.getByRole("form", { name: "Connection labels" })
  await form.getByRole("switch", { name: /Protected/ }).click()
  await form.getByRole("button", { name: "Save", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1")?.body)
    .toEqual({
      readOnly: true,
    })
})

test("a new password keeps every option the saved string carried, and is tested before it is saved", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST /databases/test": (_call: number, _url: URL, body: unknown) =>
        (body as { dsn: string }).dsn.includes("wrong")
          ? { ok: false, error: 'password authentication failed for user "app"' }
          : {
              ok: true,
              version: "PostgreSQL 16.4",
              versionNumber: "16.4",
              flavor: "postgres",
              flavorLabel: "PostgreSQL",
            },
      "PUT 1": { id: 1 },
    },
  })
  await visit(page, "/databases/1/settings")
  await section(page, "connection").getByRole("button", { name: "Edit", exact: true }).click()

  // The saved string is read from the server, and what the fields do not cover is listed as kept.
  const kept = dialog(page).locator("[data-slot=kept-options]")
  await expect(kept).toContainText("sslmode=verify-full")
  await expect(kept).toContainText("connect_timeout=5")
  await expect(dialog(page).getByLabel("Host")).toHaveValue("127.0.0.1")
  await expect(dialog(page).getByLabel("Account")).toHaveValue("app")
  // The password is not put back into the page.
  await expect(dialog(page).getByLabel("Password")).toHaveValue("")

  const save = dialog(page).getByRole("button", { name: "Save", exact: true })
  await expect(save).toBeDisabled()
  await dialog(page).getByLabel("Password").fill("wrong")
  await dialog(page).getByRole("button", { name: "Test" }).click()
  await expect(dialog(page)).toContainText("It refused the connection")
  await expect(dialog(page)).toContainText("password authentication failed")
  await expect(save).toBeDisabled()

  await dialog(page).getByLabel("Password").fill("n3w p@ss")
  // What will be saved, with the password hidden.
  await expect(dialog(page).locator("[data-slot=connection-preview]")).toHaveText(
    "postgres://app:••••••@127.0.0.1:5432/shop_main?sslmode=verify-full&connect_timeout=5",
  )
  await dialog(page).getByRole("button", { name: "Test" }).click()
  await expect(dialog(page).locator("[data-slot=test-result]")).toContainText(
    "Answered: PostgreSQL 16.4",
  )
  await save.click()

  const patched =
    "postgres://app:n3w%20p%40ss@127.0.0.1:5432/shop_main?sslmode=verify-full&connect_timeout=5"
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1")?.body)
    .toEqual({
      dsn: patched,
    })
  expect(server.sent.filter((one) => one.request === "POST /databases/test").at(-1)?.body).toEqual({
    driver: "postgres",
    dsn: patched,
  })
})

test("typed work in a dialog is not lost to Escape, and a save without a test is the reader's to say", async ({
  page,
}) => {
  const server = await mockOperate(page, { answers: { "PUT 1": { id: 1 } } })
  await visit(page, "/databases/1/settings")
  await section(page, "connection").getByRole("button", { name: "Edit", exact: true }).click()
  await expect(dialog(page).getByLabel("Host")).toHaveValue("127.0.0.1")
  await dialog(page).getByLabel("Host").fill("db.internal")

  await page.keyboard.press("Escape")
  await expect(dialog(page)).toContainText("Close and lose what you entered?")
  await dialog(page).getByRole("button", { name: "Keep editing" }).click()
  await expect(dialog(page).getByLabel("Host")).toHaveValue("db.internal")

  const save = dialog(page).getByRole("button", { name: "Save", exact: true })
  await expect(save).toBeDisabled()
  await dialog(page)
    .getByRole("switch", { name: /Save it without testing/ })
    .click()
  await save.click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1")?.body)
    .toEqual({
      dsn: "postgres://app:s3cret@db.internal:5432/shop_main?sslmode=verify-full&connect_timeout=5",
    })
})

test("publishing a server is two answers with their consequences, confirmed with the rule named", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 1/access": {
        access: { ...ACCESS, exposure: "public", firewall: { ...ACCESS.firewall, open: true } },
        firewall: "opened",
      },
    },
  })
  await visit(page, "/databases/1/settings")
  const reach = section(page, "reachability")
  await expect(reach).toContainText("ufw is on, and port 5432 is closed.")
  const group = reach.getByRole("group", { name: "Where it is reachable from" })
  await expect(group.getByRole("button", { name: /This server only/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await group.getByRole("button", { name: /Anywhere/ }).click()

  await expect(dialog(page)).toContainText("shop-db")
  await expect(dialog(page)).toContainText("adds a rule to ufw that allows port 5432 from anywhere")
  await expect(dialog(page)).toContainText("Every open session is dropped")
  await dialog(page).getByRole("button", { name: "Publish", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1/access")?.body)
    .toEqual({
      exposure: "public",
    })
})

test("a binding that is not the dashboard's to change says whose it is, and offers no control", async ({
  page,
}) => {
  await mockOperate(page, {
    answers: {
      "GET 1/access": {
        ...ACCESS,
        managed: false,
        exposure: "public",
        container: "stack-db-1",
        composeProject: "stack",
      },
    },
  })
  await visit(page, "/databases/1/settings")
  const reach = section(page, "reachability")
  await expect(reach.locator("[data-slot=reach-refusal]")).toContainText(
    "belongs to the compose project stack",
  )
  await expect(reach.getByRole("group", { name: "Where it is reachable from" })).toHaveCount(0)
  // A published port is a reading of state, said as one.
  await expect(reach).toContainText("Anything that can reach this machine can try it")
})

test("parameters are grouped, searchable, and changed with what ran shown afterwards", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 1/settings": (_call: number, _url: URL, body: unknown) => {
        const sent = body as { name: string; value?: string; reset?: boolean }
        return {
          name: sent.name,
          value: sent.reset ? "200" : sent.value,
          statements: sent.reset
            ? ['ALTER SYSTEM RESET "bgwriter_delay"', "SELECT pg_reload_conf()"]
            : [`ALTER SYSTEM SET "${sent.name}" = '${sent.value}'`, "SELECT pg_reload_conf()"],
          persisted: true,
          restartRequired: false,
        }
      },
    },
  })
  await visit(page, "/databases/1/settings")
  const parameters = section(page, "parameters")
  // Its headings are the engine's own, each with how many it holds.
  await expect(parameters.getByRole("button", { name: /Resource Usage/ })).toBeVisible()
  await expect(parameters.getByRole("button", { name: /Write-Ahead Log/ })).toBeVisible()
  // What waits for a restart is said at the top, as something to act on.
  await expect(parameters).toContainText("1 parameter is stored and not yet in effect")

  await parameters.getByLabel("Filter the parameters").fill("bgwriter")
  const row = parameters.locator("[data-parameter=bgwriter_delay]")
  await expect(row).toContainText("210")
  await expect(row).toContainText("changed")
  await expect(parameters.locator("[data-parameter]")).toHaveCount(1)
  // The search is in the address, so a link can name a parameter.
  await expect.poll(() => where(page)).toBe("/databases/1/settings?q=bgwriter")

  await row.getByRole("button", { name: "Change bgwriter_delay" }).click()
  const save = dialog(page).getByRole("button", { name: "Save", exact: true })
  await dialog(page).getByLabel("Value").fill("5")
  await expect(dialog(page)).toContainText("Between 10 and 10000 ms.")
  await expect(save).toBeDisabled()
  await dialog(page).getByLabel("Value").fill("250")
  await save.click()
  await expect(parameters.locator("[data-slot=parameter-change]")).toContainText(
    `ALTER SYSTEM SET "bgwriter_delay" = '250'`,
  )
  await expect(parameters.locator("[data-slot=parameter-change]")).toContainText("In effect now.")
  expect(server.sent.find((one) => one.request === "PUT 1/settings")?.body).toEqual({
    name: "bgwriter_delay",
    value: "250",
  })

  // One that is not the default can be put back to it.
  await row.getByRole("button", { name: "Change bgwriter_delay" }).click()
  await dialog(page).getByRole("button", { name: "Reset to default" }).click()
  await expect(parameters.locator("[data-slot=parameter-change]")).toContainText(
    'ALTER SYSTEM RESET "bgwriter_delay"',
  )
  expect(server.sent.filter((one) => one.request === "PUT 1/settings").at(-1)?.body).toEqual({
    name: "bgwriter_delay",
    reset: true,
  })
})

test("a parameter says what it is: waiting for a restart, withheld, or not the reader's to change", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/1/settings?only=pending")
  const parameters = section(page, "parameters")
  const buffers = parameters.locator("[data-parameter=shared_buffers]")
  await expect(buffers).toContainText("waiting for a restart")
  await expect(buffers).toContainText("256.0 MB")
  await expect(parameters.locator("[data-parameter]")).toHaveCount(1)

  await parameters.getByRole("button", { name: /^All/ }).click()
  await parameters.getByLabel("Filter the parameters").fill("conninfo")
  // A withheld value is said to be hidden: it is never drawn as empty.
  await expect(parameters.locator("[data-parameter=primary_conninfo]")).toContainText("hidden")
  await expect(parameters.getByRole("button", { name: "Change primary_conninfo" })).toHaveCount(0)
  await parameters.getByLabel("Filter the parameters").fill("server_version")
  await expect(parameters.locator("[data-parameter=server_version]")).toBeVisible()
  await expect(parameters.getByRole("button", { name: "Change server_version" })).toHaveCount(0)
})

test("a closed set and a yes/no are set by their own words", async ({ page }) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 1/settings": {
        name: "wal_level",
        value: "logical",
        statements: [`ALTER SYSTEM SET "wal_level" = 'logical'`],
        persisted: true,
        restartRequired: true,
      },
    },
  })
  await visit(page, "/databases/1/settings?q=wal_level")
  const parameters = section(page, "parameters")
  await parameters.getByRole("button", { name: "Change wal_level" }).click()
  await expect(dialog(page)).toContainText("takes effect only when the server is restarted")
  await dialog(page).getByRole("radio", { name: "logical" }).click()
  await dialog(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect(parameters.locator("[data-slot=parameter-change]")).toContainText(
    "It takes effect when the server is restarted.",
  )
  expect(server.sent.find((one) => one.request === "PUT 1/settings")?.body).toEqual({
    name: "wal_level",
    value: "logical",
  })
})

test("a key–value server's parameters are its own configuration, and a secret is write-only", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 4/redis/config": { name: "slowlog-max-len", secret: false, rewritten: true },
    },
  })
  await visit(page, "/databases/4/settings")
  const parameters = section(page, "parameters")
  await expect(parameters.getByRole("button", { name: /Memory/ })).toBeVisible()
  // Read from the server's own configuration route, not the SQL engines' list.
  expect(server.seen).toContain("GET 4/redis/config")
  expect(server.seen).not.toContain("GET 4/settings")

  await parameters.getByLabel("Filter the parameters").fill("requirepass")
  await expect(parameters.locator("[data-parameter=requirepass]")).toContainText("set")
  await parameters.getByLabel("Filter the parameters").fill("slowlog")
  await parameters.getByRole("button", { name: "Change slowlog-max-len" }).click()
  await dialog(page).getByLabel("Value").fill("256")
  await dialog(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 4/redis/config")?.body)
    .toEqual({
      name: "slowlog-max-len",
      value: "256",
      rewrite: true,
    })
  // It has modules where a SQL engine has extensions.
  await expect(section(page, "extensions").getByRole("heading", { level: 3 })).toHaveText("Modules")
  await expect(section(page, "extensions")).toContainText("ReJSON")
  // Its numbered databases are places to browse, not things to connect or create.
  const numbered = section(page, "databases")
  await expect(numbered.getByRole("link", { name: "Browse the keys of database 3" })).toBeVisible()
  await expect(numbered.getByRole("button", { name: "New database" })).toHaveCount(0)
})

test("an extension is enabled with one press and disabled after being asked", async ({ page }) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/server/extensions": { ok: true },
      "DELETE 1/server/extensions/pg_stat_statements": { ok: true },
    },
  })
  await visit(page, "/databases/1/settings")
  const extensions = section(page, "extensions")
  await extensions.getByRole("button", { name: "Disable pg_stat_statements" }).click()
  await expect(dialog(page)).toContainText("pg_stat_statements")
  await dialog(page).getByRole("button", { name: "Disable", exact: true }).click()
  await expect
    .poll(() => server.sent.map((one) => one.request))
    .toContain("DELETE 1/server/extensions/pg_stat_statements")

  await extensions.getByRole("button", { name: /^Available/ }).click()
  await extensions.getByRole("button", { name: "Enable pgcrypto" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 1/server/extensions")?.body)
    .toEqual({ name: "pgcrypto" })
})

test("another database of the server is opened as a connection, and a new one made", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/server/databases/connect": { id: 9, name: "shop.shop_staging", driver: "postgres" },
      "POST 1/server/databases": { id: 10, name: "shop.shop_next", driver: "postgres" },
    },
  })
  await visit(page, "/databases/1/settings")
  const databases = section(page, "databases")
  await databases.getByRole("button", { name: "Connect shop_staging" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 1/server/databases/connect")?.body)
    .toEqual({ database: "shop_staging" })

  await databases.getByRole("button", { name: "New database" }).click()
  await dialog(page).getByLabel("Name").fill("shop_main")
  await expect(dialog(page)).toContainText("already has a database of that name")
  await dialog(page).getByLabel("Name").fill("shop_next")
  await dialog(page).getByRole("button", { name: "Create", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 1/server/databases")?.body)
    .toEqual({ name: "shop_next", connect: true })
})

test("forgetting a linked connection is refused in the dialog, with the way to the deployment", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "DELETE 1": refusal(
        "database_linked",
        "remove the deployment's managed database network before dropping this linked database",
        409,
      ),
    },
  })
  await visit(page, "/databases/1/settings")
  await section(page, "danger").getByRole("button", { name: "Forget…" }).click()
  await expect(dialog(page)).toContainText("The database itself and its data are not touched")
  await expect(dialog(page).getByRole("textbox")).toHaveCount(0)
  await dialog(page).getByRole("button", { name: "Forget", exact: true }).click()

  await expect(dialog(page)).toContainText("It is linked to a deployment, so it was not forgotten")
  await expect(
    dialog(page).getByRole("link", { name: "storefront › Settings › Databases" }),
  ).toHaveAttribute("href", "/deploy/7/settings/databases")
  expect(server.sent.map((one) => one.request)).toContain("DELETE 1")
  // It was refused: the reader is still on the connection.
  expect(where(page)).toBe("/databases/1/settings")
})

test("deleting the database is typed for by its own name", async ({ page }) => {
  const server = await mockOperate(page, {
    answers: {
      "DELETE 1/database": {
        detail: "database dropped",
        database: "shop_main",
        connectionRemoved: true,
      },
    },
  })
  await visit(page, "/databases/1/settings")
  const danger = section(page, "danger")
  await expect(danger).toContainText("Typed to confirm: shop_main")
  await danger.getByRole("button", { name: "Delete…" }).click()

  const run = dialog(page).getByRole("button", { name: "Delete for good" })
  await expect(run).toBeDisabled()
  await dialog(page).getByRole("textbox").fill("shop")
  await expect(run).toBeDisabled()
  await dialog(page).getByRole("textbox").fill("shop_main")
  await run.click()

  await expect
    .poll(() => server.sent.find((one) => one.request === "DELETE 1/database"))
    .toBeTruthy()
  const sent = server.sent.find((one) => one.request === "DELETE 1/database")
  expect(sent?.body).toEqual({})
  expect(decodeURIComponent(sent?.confirm ?? "")).toBe("shop_main")
  // The connection went with its database.
  await expect.poll(() => where(page)).toBe("/databases")
})

test("a connection that names no database offers no delete, and never a dialog without a subject", async ({
  page,
}) => {
  await mockOperate(page, { rows: { 1: { database: "" } } })
  await visit(page, "/databases/1/settings")
  const danger = section(page, "danger")
  await expect(danger).toContainText("This connection names no database")
  await expect(danger.getByRole("button", { name: "Delete…" })).toHaveCount(0)
  // Forgetting does not depend on anything else being readable.
  await expect(danger.getByRole("button", { name: "Forget…" })).toBeVisible()
})

test("the phrase is the engine's own name for what goes: a file's name, a numbered database", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/7/settings")
  await expect(section(page, "danger")).toContainText("Typed to confirm: notes.db")
  await expect(section(page, "danger")).toContainText("The database file is deleted")
  await visit(page, "/databases/4/settings")
  await expect(section(page, "danger")).toContainText("Typed to confirm: db0")
  await expect(section(page, "danger")).toContainText("Every key in this numbered database")
})

test("removing a container is its own act, typed for, and works where the connection names nothing", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    rows: { 1: { database: "" } },
    summaries: {
      1: { container: { id: "c1", name: "shop-db", image: "postgres:16", state: "running" } },
    },
    answers: {
      "DELETE 1/database": {
        detail: "container shop-db removed with its data",
        database: "shop-db",
        container: "shop-db",
        connectionRemoved: true,
        warnings: [],
      },
    },
  })
  await visit(page, "/databases/1/settings")
  await section(page, "danger").getByRole("button", { name: "Remove…" }).click()
  await expect(dialog(page)).toContainText("Every database on that server goes with it")
  await dialog(page).getByRole("textbox").fill("shop-db")
  await dialog(page).getByRole("button", { name: "Remove for good" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "DELETE 1/database")?.body)
    .toEqual({
      removeContainer: true,
      database: "shop-db",
    })
})

test("a role that may not administer reads the settings and changes none of them", async ({
  page,
}) => {
  const server = await mockOperate(page, { capabilities: LIMITED })
  await visit(page, "/databases/1/settings")
  await expect(section(page, "connection")).toContainText("shop")
  await expect(page.getByRole("form", { name: "Connection labels" })).toHaveCount(0)
  await expect(
    section(page, "connection").getByRole("button", { name: "Edit", exact: true }),
  ).toHaveCount(0)
  await expect(section(page, "danger")).toHaveCount(0)
  await expect(
    section(page, "databases").getByRole("button", { name: /Connect|New database/ }),
  ).toHaveCount(0)
  await expect(
    section(page, "extensions").getByRole("button", { name: /^(Enable|Disable) / }),
  ).toHaveCount(0)
  await section(page, "parameters").getByLabel("Filter the parameters").fill("work_mem")
  await expect(section(page, "parameters").locator("[data-parameter=work_mem]")).toBeVisible()
  await expect(section(page, "parameters").getByRole("button", { name: /^Change / })).toHaveCount(0)
  // The addresses and the firewall are an administrator's read: it is not asked.
  expect(server.seen).not.toContain("GET 1/access")
  expect(server.seen).not.toContain("GET 1/url")
})

test("a protected connection keeps its labels editable and offers nothing that changes the server", async ({
  page,
}) => {
  await mockOperate(page, { rows: { 1: { readOnly: true } } })
  await visit(page, "/databases/1/settings")
  // Protection is taken off where it was put on.
  await expect(
    page
      .getByRole("form", { name: "Connection labels" })
      .getByRole("switch", { name: /Protected/ }),
  ).toBeChecked()
  await section(page, "parameters").getByLabel("Filter the parameters").fill("work_mem")
  await expect(section(page, "parameters").getByRole("button", { name: /^Change / })).toHaveCount(0)
  await expect(
    section(page, "extensions").getByRole("button", { name: /^(Enable|Disable) / }),
  ).toHaveCount(0)
  await expect(
    section(page, "databases").getByRole("button", { name: /Connect|New database/ }),
  ).toHaveCount(0)
  await expect(section(page, "danger")).toContainText("This connection is protected")
  await expect(section(page, "danger").getByRole("button", { name: "Delete…" })).toHaveCount(0)
  // Forgetting touches nothing in the database, and stays.
  await expect(section(page, "danger").getByRole("button", { name: "Forget…" })).toBeVisible()
})

test("parameters that cannot be read say so and are tried again; the rest of the page stands", async ({
  page,
}) => {
  let failing = true
  await mockOperate(page, {
    answers: {
      "GET 1/settings": () =>
        failing ? refusal("query_failed", "permission denied for view pg_settings", 502) : SETTINGS,
    },
  })
  await visit(page, "/databases/1/settings")
  const parameters = section(page, "parameters")
  await expect(parameters.getByRole("status")).toContainText("Could not read the parameters")
  await expect(parameters).toContainText("permission denied for view pg_settings")
  await expect(section(page, "extensions")).toContainText("pg_stat_statements")
  failing = false
  await parameters.getByRole("button", { name: "Try again" }).click()
  await expect(parameters.getByRole("button", { name: /Resource Usage/ })).toBeVisible()
})

test("the database's menu backs it up, opens its settings and forgets it, for the role that may", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/backup": job("j5", "backup", "running"),
      "GET /jobs/j5": jobThatEnds("j5", "backup", { file: "shop_main-menu.dump", size: 10 }),
    },
  })
  await visit(page, "/databases/1/backups")
  await page.getByRole("button", { name: "Actions for shop" }).click()
  await expect(page.getByRole("menuitem", { name: "Settings" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Forget this connection" })).toBeVisible()
  await page.getByRole("menuitem", { name: "Back up now" }).click()
  // On the page that shows a dump in place, the dump begun from the menu is shown there.
  await expect(progress(page)).toHaveAttribute("aria-label", "Dump shop, succeeded")
  expect(server.sent.find((one) => one.request === "POST 1/backup")?.body).toEqual({})

  await page.getByRole("button", { name: "Actions for shop" }).click()
  await page.getByRole("menuitem", { name: "Forget this connection" }).click()
  await expect(dialog(page)).toContainText("Removes the saved connection")
  await dialog(page).getByRole("button", { name: "Cancel" }).click()
})

test("a viewer's menu holds nothing that dumps or forgets", async ({ page }) => {
  await mockOperate(page, { viewer: true })
  await visit(page, "/databases/1/backups")
  await page.getByRole("button", { name: "Actions for shop" }).click()
  await expect(page.getByRole("menuitem", { name: "Settings" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Back up now" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Forget this connection" })).toHaveCount(0)
})

// ---------------------------------------------------------------------------
// Access
// ---------------------------------------------------------------------------

const accounts = (page: Page) => page.getByRole("region", { name: "Accounts" })
const account = (page: Page, name: string) =>
  accounts(page)
    .locator("[data-slot=choice-row]")
    .filter({ has: page.getByRole("button", { name: `Open ${name}`, exact: true }) })
const panel = (page: Page) => page.getByRole("dialog")
const matrix = (page: Page) => page.locator("[data-slot=grants-matrix]")

test("accounts open on who can sign in, with the dashboard's own account marked", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/1/access")

  await expect(tile(page, "Accounts")).toContainText("4")
  await expect(tile(page, "Administrators")).toContainText("1")
  await expect(tile(page, "Signed in now")).toContainText("2")
  await expect(tile(page, "Cannot sign in")).toContainText("2")

  await expect(account(page, "app")).toContainText("this connection")
  await expect(account(page, "app")).toContainText("administrator")
  await expect(account(page, "app")).toContainText("Everything on the server")
  await expect(account(page, "reporting")).toContainText("Granted on 1 database, 2 tables")
  await expect(account(page, "readers")).toContainText("no login")
  await expect(account(page, "pg_monitor")).toContainText("system")

  // A reading narrows the list, and the address says which.
  await page.getByRole("button", { name: "Only accounts that cannot sign in" }).click()
  await expect(accounts(page).locator("[data-slot=choice-row]")).toHaveCount(2)
  await expect.poll(() => where(page)).toBe("/databases/1/access?show=blocked")
  await page.getByRole("button", { name: "Every account" }).click()
  await accounts(page).getByLabel("Filter the accounts").fill("report")
  await expect(accounts(page).locator("[data-slot=choice-row]")).toHaveCount(1)
})

test("a new account shows the grant the server plans, and ends on the string it connects with", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/server/roles/billing/grant": {
        preview: true,
        statements: [
          'GRANT CONNECT ON DATABASE "shop_main" TO "billing"',
          'GRANT SELECT ON ALL TABLES IN SCHEMA "public" TO "billing"',
        ],
        schemas: ["public"],
        skippedSchemas: [{ name: "cron", reason: "it belongs to the pg_cron extension" }],
      },
      "POST 1/server/roles": { ok: true, statements: [], schemas: ["public"] },
    },
  })
  await visit(page, "/databases/1/access")
  await accounts(page).getByRole("button", { name: "New account" }).click()

  const create = dialog(page).getByRole("button", { name: "Create account" })
  await expect(create).toBeDisabled()
  await dialog(page).getByLabel("Name").fill("app")
  await expect(dialog(page)).toContainText("An account of that name already exists")
  await dialog(page).getByLabel("Name").fill("billing")
  // The statements are the server's own, asked for without running anything.
  await expect(dialog(page)).toContainText(
    'GRANT SELECT ON ALL TABLES IN SCHEMA "public" TO "billing"',
  )
  await expect(dialog(page)).toContainText(
    "Leaves cron alone: it belongs to the pg_cron extension.",
  )
  const preview = server.sent.find((one) => one.request === "POST 1/server/roles/billing/grant")
  expect(preview?.query).toBe("?preview=1")
  expect(preview?.body).toEqual({ database: "shop_main", level: "read" })

  const password = await dialog(page)
    .getByRole("textbox", { name: "Password", exact: true })
    .inputValue()
  expect(password).toHaveLength(30)
  await create.click()

  // The last step: the password once, and the string a program connects with.
  await expect(dialog(page).locator("[data-slot=secret-shown]")).toContainText(password)
  await expect(dialog(page).locator("[data-slot=account-snippet]")).toContainText(
    `postgres://billing:${password}@127.0.0.1:5432/shop_main`,
  )
  expect(server.sent.find((one) => one.request === "POST 1/server/roles")?.body).toEqual({
    name: "billing",
    password,
    database: "shop_main",
    level: "read",
  })

  // A slip does not lose the only sight of the password.
  await page.keyboard.press("Escape")
  await expect(dialog(page)).toContainText("The password is not shown again")
  await dialog(page).getByRole("button", { name: "Keep editing" }).click()
  await dialog(page).getByRole("button", { name: "I have saved it" }).click()
  await expect(dialog(page)).toHaveCount(0)
})

test("a new password is the only thing sent, and the connection's own account says what became of it", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: { "PUT 1/server/roles/app": { ok: true, connectionUpdated: true } },
  })
  await visit(page, "/databases/1/access")
  await account(page, "app").getByRole("button", { name: "Actions for app" }).click()
  await page.getByRole("menuitem", { name: "Change password" }).click()
  await expect(dialog(page)).toContainText("shop, this connection")
  const password = await dialog(page).getByLabel("New password").inputValue()
  await dialog(page).getByRole("button", { name: "Change password" }).click()

  await expect(dialog(page).locator("[data-slot=secret-shown]")).toContainText(
    "its saved password was replaced with the new one",
  )
  // No attribute travels with it: nothing the account is can be reset by a password change.
  expect(server.sent.find((one) => one.request === "PUT 1/server/roles/app")?.body).toEqual({
    password,
  })
})

test("an account's attributes are saved one switch at a time, and its own account keeps what would lock it out", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: { "PUT 1/server/roles/reporting": { ok: true } },
  })
  await visit(page, "/databases/1/access?account=reporting")
  const form = panel(page).getByRole("form", { name: "Account attributes" })
  const save = form.getByRole("button", { name: "Save attributes" })
  await expect(save).toBeDisabled()
  await form.getByRole("switch", { name: "It can create databases" }).click()
  await expect(form).toContainText("Only this attribute is sent")
  await save.click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1/server/roles/reporting")?.body)
    .toEqual({ createDb: true })

  // The account this connection signs in with.
  await visit(page, "/databases/1/access?account=app")
  const own = panel(page).getByRole("form", { name: "Account attributes" })
  await expect(own.getByRole("switch", { name: "It can sign in" })).toBeDisabled()
  await expect(
    own.getByRole("switch", { name: "Administrator of the whole server" }),
  ).toBeDisabled()
  await expect(own).toContainText("This connection signs in with it.")
  await expect(own.getByRole("switch", { name: "It can create databases" })).toBeEnabled()
  // And it cannot be dropped from here.
  await expect(panel(page).getByRole("button", { name: "Drop account" })).toHaveCount(0)
})

test("grants are pressed on a matrix, read as the server's statements, and only then applied", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/server/roles/reporting/privileges": (_call: number, url: URL, body: unknown) => {
        const sent = body as { table: string; privileges: string[] }
        return {
          ...(url.searchParams.get("preview") ? { preview: true } : { ok: true }),
          statements: [
            `GRANT ${sent.privileges.join(", ")} ON TABLE "public"."${sent.table}" TO "reporting"`,
          ],
        }
      },
      "POST 1/server/roles/reporting/privileges/revoke": (
        _call: number,
        url: URL,
        body: unknown,
      ) => {
        const sent = body as { table: string; privileges: string[] }
        return {
          ...(url.searchParams.get("preview") ? { preview: true } : { ok: true }),
          statements: [
            `REVOKE ${sent.privileges.join(", ")} ON TABLE "public"."${sent.table}" FROM "reporting"`,
          ],
        }
      },
    },
  })
  await visit(page, "/databases/1/access?account=reporting")
  // It opens on the level the account holds the most at.
  await expect(matrix(page).getByRole("button", { name: /^Tables/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  const select = (table: string) => matrix(page).getByRole("button", { name: `SELECT on ${table}` })
  await expect(select("orders")).toHaveAttribute("aria-pressed", "true")
  await expect(select("products")).toHaveAttribute("aria-pressed", "false")

  await matrix(page).getByRole("button", { name: "UPDATE on orders" }).click()
  await select("customers").click()
  const pending = panel(page).locator("[data-slot=grants-pending]")
  await expect(pending).toContainText("2 changes not applied")
  // Nothing has been sent: a press only stages.
  expect(server.sent).toEqual([])

  await pending.getByRole("button", { name: "Review…" }).click()
  const review = page.getByRole("dialog", { name: "Change what reporting holds" })
  await expect(review).toContainText('REVOKE SELECT ON TABLE "public"."customers" FROM "reporting"')
  await expect(review).toContainText('GRANT UPDATE ON TABLE "public"."orders" TO "reporting"')
  await expect(review).toContainText("2 requests, 1 of them taking something away.")
  // Every statement shown came from the server's own preview.
  expect(server.sent.every((one) => one.query === "?preview=1")).toBe(true)

  await review.getByRole("button", { name: "Apply" }).click()
  await expect(review).toHaveCount(0)
  const applied = server.sent.filter((one) => one.query === "")
  expect(applied.map((one) => [one.request, one.body])).toEqual([
    [
      "POST 1/server/roles/reporting/privileges/revoke",
      { level: "table", schema: "public", table: "customers", privileges: ["SELECT"] },
    ],
    [
      "POST 1/server/roles/reporting/privileges",
      { level: "table", schema: "public", table: "orders", privileges: ["UPDATE"] },
    ],
  ])
})

test("a privilege pressed on every table is one request for the schema, and can cover the tables to come", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/server/roles/reporting/privileges": {
        preview: true,
        statements: ['GRANT INSERT ON ALL TABLES IN SCHEMA "public" TO "reporting"'],
        futureOwners: ["app"],
      },
    },
  })
  await visit(page, "/databases/1/access?account=reporting")
  await matrix(page).getByRole("button", { name: "INSERT on every table in public" }).click()
  await panel(page)
    .locator("[data-slot=grants-pending]")
    .getByRole("button", { name: "Review…" })
    .click()
  const review = page.getByRole("dialog", { name: "Change what reporting holds" })
  await expect(review).toContainText('GRANT INSERT ON ALL TABLES IN SCHEMA "public" TO "reporting"')
  await review.getByRole("switch", { name: "Also for what is created later in public" }).click()
  await expect(review).toContainText("Covers what app create later.")
  expect(server.sent.at(-1)?.body).toEqual({
    level: "table",
    schema: "public",
    table: "",
    privileges: ["INSERT"],
    future: true,
  })
})

test("dropping an account is asked with the account named, and never offered on the engine's own", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: { "DELETE 1/server/roles/reporting": { ok: true } },
  })
  await visit(page, "/databases/1/access")
  await account(page, "reporting").getByRole("button", { name: "Actions for reporting" }).click()
  await page.getByRole("menuitem", { name: "Drop account" }).click()
  await expect(dialog(page)).toContainText("reporting")
  await expect(dialog(page).getByRole("textbox")).toHaveCount(0)
  await dialog(page).getByRole("button", { name: "Drop", exact: true }).click()
  await expect
    .poll(() => server.sent.map((one) => one.request))
    .toContain("DELETE 1/server/roles/reporting")

  await account(page, "pg_monitor").getByRole("button", { name: "Actions for pg_monitor" }).click()
  await expect(page.getByRole("menuitem", { name: "Drop account" })).toHaveCount(0)
})

test("an engine whose accounts have hosts names each by both, and carries the host in what it sends", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "GET 2/server/roles": {
        supported: true,
        roles: [
          role("root", { host: "%", superuser: true }),
          role("root", { host: "localhost", superuser: true }),
          role("blogger", { host: "10.%" }),
        ],
      },
      "GET 2/server/privileges": {
        supported: true,
        editable: ["password", "login", "locked", "superuser"],
        levels: [],
      },
      "GET 2/server/grants": { supported: true, truncated: false, grants: [] },
      "DELETE 2/server/roles/blogger": { ok: true },
    },
  })
  await visit(page, "/databases/2/access")
  await expect(account(page, "root@%")).toBeVisible()
  await expect(account(page, "root@localhost")).toBeVisible()
  await account(page, "blogger@10.%")
    .getByRole("button", { name: "Actions for blogger@10.%" })
    .click()
  await page.getByRole("menuitem", { name: "Drop account" }).click()
  await dialog(page).getByRole("button", { name: "Drop", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "DELETE 2/server/roles/blogger")?.query)
    .toBe("?host=10.%25")
})

test("a viewer reads the accounts and what each holds, and is offered nothing that changes them", async ({
  page,
}) => {
  const server = await mockOperate(page, { viewer: true })
  await visit(page, "/databases/1/access?account=reporting")
  await expect(panel(page).getByRole("button", { name: "Change password" })).toHaveCount(0)
  await expect(panel(page).getByRole("button", { name: "Drop account" })).toHaveCount(0)
  await expect(panel(page).getByRole("button", { name: "Save attributes" })).toHaveCount(0)
  // What it holds is read; nothing on it can be pressed.
  await expect(matrix(page).locator("[data-object=orders]")).toContainText("SELECT")
  await expect(matrix(page).getByRole("button", { name: "SELECT on orders" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await expect(accounts(page).getByRole("button", { name: "New account" })).toHaveCount(0)
  await expect(accounts(page).getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
  expect(server.sent).toEqual([])
})

test("a protected connection reads its accounts and draws no way to change one", async ({
  page,
}) => {
  const server = await mockOperate(page, { rows: { 1: { readOnly: true } } })
  await visit(page, "/databases/1/access?account=reporting")
  await expect(matrix(page).locator("[data-object=orders]")).toContainText("SELECT")
  await expect(matrix(page).getByRole("button", { name: "SELECT on orders" })).toHaveCount(0)
  await expect(panel(page).getByRole("button", { name: "Change password" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await expect(accounts(page)).toContainText("This connection is protected")
  await expect(accounts(page).getByRole("button", { name: "New account" })).toHaveCount(0)
  // Not even a preview is asked: the server refuses those on a protected connection too.
  expect(server.sent).toEqual([])
})

test("accounts that cannot be read say so in the server's words; the figures are dashes", async ({
  page,
}) => {
  await mockOperate(page, {
    answers: {
      "GET 1/server/roles": refusal("query_failed", "SELECT command denied for table user", 502),
    },
  })
  await visit(page, "/databases/1/access")
  await expect(accounts(page).getByRole("status")).toContainText("Could not read the accounts")
  await expect(accounts(page)).toContainText("SELECT command denied for table user")
  await expect(accounts(page).getByRole("button", { name: "Try again" })).toBeVisible()
  await expect(tile(page, "Accounts")).toContainText("could not be read")
  await expect(tile(page, "Accounts")).not.toContainText("0")
})

test("a key–value server's users are rules, and the dashboard's own user keeps what would cut it off", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 4/redis/acl/worker": {
        user: ACL.users[1],
        persisted: false,
        notice: "This server keeps its users in memory rather than in an ACL file.",
      },
    },
  })
  await visit(page, "/databases/4/access")
  const users = page.getByRole("region", { name: "Users" })
  await expect(tile(page, "Any password")).toContainText("1")
  await expect(users).toContainText("Every command on every key")
  await expect(users).toContainText("-@all +@read +@write on ~jobs:*")
  // No SQL account route is asked of it.
  expect(server.seen.some((one) => one.includes("/server/roles"))).toBe(false)

  await users.getByRole("button", { name: "Open worker", exact: true }).click()
  await expect(panel(page)).toContainText("on ~jobs:* resetchannels -@all +@read +@write")
  await panel(page).getByLabel("Keys").fill("~jobs:*\n%R~shared:*")
  await panel(page).getByRole("button", { name: "Save rule" }).click()
  // Only the part of the rule that was edited is sent.
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 4/redis/acl/worker")?.body)
    .toEqual({
      keys: ["~jobs:*", "%R~shared:*"],
    })
  await expect(panel(page)).toContainText("keeps its users in memory")
  await page.keyboard.press("Escape")

  await users.getByRole("button", { name: "Open default", exact: true }).click()
  await expect(panel(page).locator("[data-slot=acl-held]")).toContainText(
    "The dashboard connects as this user",
  )
  await expect(
    panel(page).getByRole("switch", { name: "Switched on: it may connect" }),
  ).toBeDisabled()
  await expect(panel(page).getByLabel("Commands")).toBeDisabled()
  await expect(panel(page).getByLabel("Keys")).toBeDisabled()
  await expect(panel(page).getByRole("button", { name: "Delete user" })).toHaveCount(0)
})

test("a new key–value user is its rule and its password, and ends on its connection string", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: { "PUT 4/redis/acl/reader": { user: ACL.users[1], persisted: true } },
  })
  await visit(page, "/databases/4/access")
  await page.getByRole("button", { name: "New user" }).click()
  await dialog(page).getByLabel("Name").fill("reader")
  await dialog(page).getByLabel("Keys").fill("cache:*")
  await dialog(page).getByLabel("Commands").fill("+@read")
  const password = await dialog(page)
    .getByRole("textbox", { name: "Password", exact: true })
    .inputValue()
  await dialog(page).getByRole("button", { name: "Create user" }).click()
  await expect(dialog(page).locator("[data-slot=account-snippet]")).toContainText(
    `redis://reader:${password}@127.0.0.1:6379/0`,
  )
  expect(server.sent.find((one) => one.request === "PUT 4/redis/acl/reader")?.body).toEqual({
    create: true,
    enabled: true,
    password,
    keys: ["cache:*"],
    channels: [],
    commands: ["+@read"],
  })
})

test("a document database's users hold roles on databases, granted and taken one at a time", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 5/mongo/users/grant": { ok: true },
      "POST 5/mongo/users/revoke": { ok: true },
    },
  })
  await visit(page, "/databases/5/access")
  const list = page.getByRole("region", { name: "Accounts" })
  await expect(tile(page, "Users")).toContainText("2")
  await expect(list).toContainText("readWrite@app_main")
  await expect(list).toContainText("this connection")

  await list.getByRole("button", { name: "Open api of app_main" }).click()
  await panel(page).getByRole("combobox", { name: "The role" }).click()
  await page.getByRole("option", { name: "dbAdmin" }).click()
  await panel(page).getByRole("button", { name: "Add role" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 5/mongo/users/grant")?.body)
    .toEqual({
      database: "app_main",
      user: "api",
      roles: [{ role: "dbAdmin", db: "app_main" }],
    })
  await panel(page).getByRole("button", { name: "Take readWrite@app_main away" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 5/mongo/users/revoke")?.body)
    .toEqual({
      database: "app_main",
      user: "api",
      roles: [{ role: "readWrite", db: "app_main" }],
    })
  await page.keyboard.press("Escape")

  // The account the dashboard signs in with cannot be dropped from here.
  await list.getByRole("button", { name: "Actions for root" }).click()
  await expect(page.getByRole("menuitem", { name: "Change password" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Drop user" })).toHaveCount(0)
  await page.keyboard.press("Escape")

  // The other view of the page: what a role is.
  await page
    .getByRole("group", { name: "Accounts view" })
    .getByRole("button", { name: "Roles" })
    .click()
  await expect.poll(() => where(page)).toBe("/databases/5/access?view=roles")
  await expect(list).toContainText("auditors")
  await expect(list).toContainText("readWrite")
})

test("a document database with no user says what that means, not that the list is empty", async ({
  page,
}) => {
  await mockOperate(page, { answers: { "GET 5/mongo/users": { users: [] } } })
  await visit(page, "/databases/5/access")
  await expect(tile(page, "Users")).toContainText("the server asks nobody")
  await expect(page.getByRole("region", { name: "Accounts" })).toContainText(
    "A server with no user accepts every connection that reaches it",
  )
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
  // A table is as wide as the view it is in, at whatever width the rail leaves the page.
  const clipped = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("[data-slot=page] [data-slot=table-container]")]
      .filter((el) => el.scrollWidth > el.clientWidth + 1)
      .map((el) => `${el.scrollWidth} in ${el.clientWidth}: ${el.textContent?.slice(0, 60)}`),
  )
  expect(clipped, `tables wider than ${what}`).toEqual([])
  // A sheet or a dialog drawn over the page takes the page out of the accessibility tree.
  if (overlay) return
  const names = page.locator("[data-slot=page]").getByRole("heading", { level: 1 })
  await expect(names).toHaveCount(1)
  await expect(names).toHaveClass(/sr-only/)
}

const SURFACES: [string, string][] = [
  ["/databases/1/access", "Accounts"],
  ["/databases/4/access", "Users"],
  ["/databases/5/access", "Accounts"],
  ["/databases/1/backups", "Dumps"],
  ["/databases/5/backups", "Dumps"],
]

for (const [label, viewport] of [
  ["", { width: 1280, height: 900 }],
  [" at a wide window", { width: 1720, height: 1000 }],
  [" at 1024 wide", { width: 1024, height: 900 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the access, backups and settings pages keep the design system's rules${label}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport)
    await mockOperate(page)
    for (const [path, region] of SURFACES) {
      await visit(page, path)
      await expect(page.getByRole("region", { name: region })).toBeVisible()
      await expect(page.locator("[data-slot=stat-tile]").first()).not.toContainText("could not")
      await keepsTheRules(page, `${path}${label}`)
    }
    for (const path of [
      "/databases/1/settings",
      "/databases/4/settings",
      "/databases/7/settings",
    ]) {
      await visit(page, path)
      await expect(section(page, "danger")).toBeVisible()
      await expect(section(page, "parameters").getByRole("button").first()).toBeVisible()
      await keepsTheRules(page, `${path}${label}`)
    }
    // Settings with a search on, so the parameter rows themselves are drawn.
    await visit(page, "/databases/1/settings?q=e")
    await expect(section(page, "parameters").locator("[data-parameter]").first()).toBeVisible()
    await keepsTheRules(page, `/databases/1/settings?q=e${label}`)
  })

  test(`an account's panel and the dialogs keep the rules${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockOperate(page, {
      answers: {
        "GET 1/backups": { ...BACKUPS, job: job("j7", "backup", "running") },
        "GET /jobs/j7": {
          job: job("j7", "backup", "running"),
          lines: [line(1, "status", "Dumping shop")],
        },
      },
    })
    await visit(page, "/databases/1/access?account=reporting")
    await expect(matrix(page).locator("[data-object=orders]")).toBeVisible()
    await matrix(page).getByRole("button", { name: "UPDATE on orders" }).click()
    await keepsTheRules(page, `the account panel${label}`, true)

    await visit(page, "/databases/1/access")
    await accounts(page).getByRole("button", { name: "New account" }).click()
    await expect(dialog(page).getByLabel("Name")).toBeVisible()
    await keepsTheRules(page, `the new account dialog${label}`, true)

    // A transfer in flight, in place.
    await visit(page, "/databases/1/backups")
    await expect(progress(page)).toBeVisible()
    await keepsTheRules(page, `a running transfer${label}`)

    await visit(page, "/databases/1/settings")
    await section(page, "danger").getByRole("button", { name: "Delete…" }).click()
    await expect(dialog(page).getByRole("textbox")).toBeVisible()
    await keepsTheRules(page, `the delete confirmation${label}`, true)
  })
}

test("the restore dialog and the address editor keep the rules", async ({ page }) => {
  await mockOperate(page)
  await visit(page, "/databases/1/backups")
  await dumpRow(page, "shop_main-new.dump")
    .getByRole("button", { name: /^Restore/ })
    .click()
  await expect(dialog(page).getByRole("button", { name: /This database/ })).toBeVisible()
  await keepsTheRules(page, "the restore dialog", true)
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Back up now" }).click()
  await expect(dialog(page).getByLabel("Note")).toBeVisible()
  await keepsTheRules(page, "the dump dialog", true)

  await visit(page, "/databases/1/settings")
  await section(page, "connection").getByRole("button", { name: "Edit", exact: true }).click()
  await expect(dialog(page).locator("[data-slot=kept-options]")).toBeVisible()
  await keepsTheRules(page, "the address editor", true)
  await page.keyboard.press("Escape")
  await section(page, "parameters").getByLabel("Filter the parameters").fill("wal_level")
  await section(page, "parameters").getByRole("button", { name: "Change wal_level" }).click()
  await expect(dialog(page).getByRole("radio", { name: "logical" })).toBeVisible()
  await keepsTheRules(page, "the parameter dialog", true)
})
