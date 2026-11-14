import { expect as baseExpect, test, type Page } from "@playwright/test"
import { hold, mockDatabases, type DatabaseMock } from "./database-fixture"
import { risen } from "./risen"
import { tallShot } from "./database-settings-shots"

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
 * would lock the dashboard out. Work that is staged or typed is not lost to a
 * slip — in a dialog or on an account's page — and a secret shown once has
 * one way out. On a server that numbers its databases a dump is of the
 * connection's own number unless the reader widens it, and a dump that
 * reaches other numbers is restored only by a press that says so.
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
  firewall: { backend: "ufw", active: true, open: false, allowed: [], editable: true },
  allow: [],
  gateway: { available: true, alsoAdmitted: [], enforced: false, refused: 0 },
  addresses: [],
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
  "GET 1/server/roles/readers": detail("readers"),
  "GET 1/server/roles/pg_monitor": detail("pg_monitor"),
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
const dumps = (page: Page) => page.getByRole("region", { name: "Dumps" })
const dumpRow = (page: Page, file: string) => dumps(page).getByRole("row").filter({ hasText: file })
/** A dump at any width: a row of the table, or its card where the table has no room. */
const dumpEntry = (page: Page, file: string) =>
  dumps(page).locator("tbody tr, [data-slot=dump-card]").filter({ hasText: file })
const progress = (page: Page) => page.locator("[data-slot=transfer-progress]")
/** The Dumps panel's menu: the commands beside Back up now that are not pressed daily. */
const moreMenu = (page: Page) => page.getByRole("button", { name: "More backup actions" })
async function more(page: Page, item: string) {
  await moreMenu(page).click()
  await page.getByRole("menuitem", { name: item }).click()
}
/** The menu's words; none where the role or the engine leaves nothing to put in one. */
async function moreItems(page: Page): Promise<string[]> {
  if ((await moreMenu(page).count()) === 0) return []
  await moreMenu(page).click()
  const items = await page.getByRole("menuitem").allTextContents()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("menu")).toHaveCount(0)
  return items.map((item) => item.trim())
}
/** What a dump's name opens: the rest of what is known about it. */
const about = (page: Page) => page.locator("[data-slot=popover-content]")
const section = (page: Page, id: string) => page.locator(`section#${id}`)
const saveBar = (page: Page) => page.getByRole("region", { name: "Save changes" })
const where = (page: Page) => new URL(page.url()).pathname + new URL(page.url()).search

// ---------------------------------------------------------------------------
// Backups
// ---------------------------------------------------------------------------

test("backups show their schedule action and every dump without metric cards", async ({ page }) => {
  await mockOperate(page)
  await visit(page, "/databases/1/backups")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Manage backup schedule" })).toHaveAttribute(
    "href",
    "/backups?database=1",
  )

  await expect(dumpRow(page, "shop_main-new.dump")).toContainText("before the migration")
  await expect(dumpRow(page, "shop_main-new.dump")).toContainText("Everything")
  // Who took it, the tool that wrote it and how long it took are behind its name.
  await expect(dumpRow(page, "shop_main-new.dump")).not.toContainText("pg_dump")
  await dumpRow(page, "shop_main-new.dump")
    .getByRole("button", { name: "About shop_main-new.dump" })
    .click()
  await expect(about(page)).toContainText("by operator")
  await expect(about(page)).toContainText(/pg_dump 16\.12, in under a second/)
  await expect(about(page)).toContainText("before the migration")
  await page.keyboard.press("Escape")
  await expect(dumpRow(page, "shop_main-safety.dump")).toContainText("safety dump")
  await expect(dumpRow(page, "categories.sql")).toContainText("uploaded")
  await expect(dumpRow(page, "shop_main-tables.dump")).toContainText("2 tables")
  // An uploaded file says what it is, not that it holds everything.
  await expect(dumpRow(page, "categories.sql")).not.toContainText("Everything")
  // Where they are kept is said once, on the picture.
  await expect(page.getByRole("list", { name: "Where the dumps of shop go" })).toContainText(
    "/var/backups/jd/databases/shop",
  )
  await expect(dumps(page)).not.toContainText("/var/backups/jd/databases/shop")
})

for (const width of [1280, 390]) {
  test(`at ${width} px every dump is one line of one height, with one command on the title line`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockOperate(page)
    await visit(page, "/databases/1/backups")
    await expect(dumpEntry(page, "shop_main-new.dump")).toBeVisible()
    await risen(page)
    // A dump with a note, a tag, neither, and one that is not described.
    const boxes = await dumps(page)
      .locator("tbody tr, [data-slot=dump-card]")
      .evaluateAll((rows) =>
        rows.map((row) => {
          const box = row.getBoundingClientRect()
          return { top: box.top, height: box.height }
        }),
      )
    expect(boxes).toHaveLength(4)
    const spread = (values: number[]) => Math.max(...values) - Math.min(...values)
    expect(spread(boxes.map((box) => box.height))).toBeLessThan(0.5)
    // Nothing hangs between two dumps: each starts where the last ended, plus the same gap.
    expect(spread(boxes.slice(1).map((box, at) => box.top - boxes[at].top))).toBeLessThan(0.5)
    // One brand-faced command beside the title, and the rest as words in a menu.
    const head = dumps(page).locator("[data-slot=panel-header]").first()
    await expect(head.getByRole("button")).toHaveCount(2)
    await expect(head.getByRole("button", { name: "Back up now" })).toBeVisible()
    expect(await moreItems(page)).toEqual(["Upload a dump", "Copy database"])
    await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  })
}

for (const width of [1280, 390]) {
  test(`at ${width} px the dumps and what is under them hold their place from loading to loaded`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await mockOperate(page, {
      answers: {
        "GET 1/backups": async () => {
          await held
          return BACKUPS
        },
      },
    })
    await visit(page, "/databases/1/backups")
    const head = dumps(page).locator("[data-slot=panel-header]").first()
    // The section under the dumps: it is what moved when the list landed at another height.
    const under = page.getByRole("region", { name: "Point-in-time recovery", exact: true })
    await expect(dumps(page).getByRole("status", { name: "Reading the dumps" })).toBeVisible()
    await expect(under).toBeVisible()
    await risen(page)
    const before = [await head.boundingBox(), await under.boundingBox()]
    release()
    await expect(dumpEntry(page, "shop_main-new.dump")).toBeVisible()
    await risen(page)
    const after = [await head.boundingBox(), await under.boundingBox()]
    expect(Math.round(after[0]!.y)).toBe(Math.round(before[0]!.y))
    // Four dumps are kept, which is what the silhouette draws.
    expect(Math.abs(after[1]!.y - before[1]!.y)).toBeLessThanOrEqual(1)
  })
}

test("the backup schedule keeps its state and next run beside its action", async ({ page }) => {
  let enabled = false
  await mockOperate(page, {
    answers: {
      "GET /backups/": () => [
        {
          id: 9,
          name: "Nightly database dump",
          databaseDumps: [1],
          enabled,
          nextRun: "2026-10-03T02:00:00Z",
        },
      ],
    },
  })
  await visit(page, "/databases/1/backups")
  await expect(page.getByText("Nightly database dump · Paused")).toBeVisible()
  await expect(page.getByRole("link", { name: "Manage backup schedule" })).toHaveAttribute(
    "href",
    "/backups?database=1",
  )
  enabled = true
  await page.reload()
  await expect(page.getByText(/Nightly database dump · Next/)).toBeVisible()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
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
  // The console went, and its Dismiss with it: the keyboard is on the page's command.
  await expect(page.getByRole("button", { name: "Back up now" })).toBeFocused()
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

/**
 * How far past its window's edge the page reaches at any point of the border
 * light's lap. The light is moved round the frame by one animation, which is
 * stepped through a lap here rather than waited for: at four seconds a lap, a
 * check that looked once saw the light on the right edge one time in ten.
 */
function widestDuringLap(page: Page, unclipped = false) {
  return progress(page).evaluate(async (el, bare) => {
    // The light's own box clips it (`ui/border-beam.tsx`), and so does what holds it here.
    if (bare)
      for (const node of el.querySelectorAll(".overflow-hidden"))
        node.classList.remove("overflow-hidden")
    const laps = el
      .getAnimations({ subtree: true })
      .filter((animation) =>
        (animation.effect as KeyframeEffect | null)
          ?.getKeyframes()
          .some((frame) => String(frame.transform ?? "").includes("translate(")),
      )
    const scrolls = [
      document.documentElement,
      ...document.querySelectorAll("[data-slot=page]"),
    ].map((node) => node.parentElement ?? node)
    let most = 0
    for (const lap of laps) {
      const duration = Number(lap.effect?.getComputedTiming().duration ?? 0)
      lap.pause()
      for (let at = 0; at < 1; at += 0.02) {
        lap.currentTime = at * duration
        await new Promise(requestAnimationFrame)
        most = Math.max(most, ...scrolls.map((node) => node.scrollWidth - node.clientWidth))
      }
      lap.play()
    }
    return { laps: laps.length, most }
  }, unclipped)
}

test("a running transfer's border light never reaches past the page on a phone", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockOperate(page, {
    answers: {
      "GET 1/backups": { ...BACKUPS, job: job("j7", "backup", "running") },
      "GET /jobs/j7": {
        job: job("j7", "backup", "running"),
        lines: [line(1, "status", "Dumping shop")],
      },
    },
  })
  await visit(page, "/databases/1/backups")
  await expect(progress(page)).toHaveAttribute("aria-label", "Dump shop, running")
  const clipped = await widestDuringLap(page)
  expect(clipped.laps).toBe(1)
  expect(clipped.most).toBeLessThanOrEqual(1)
  // The check sees what it is for: without the clip the light's box widens the page.
  expect((await widestDuringLap(page, true)).most).toBeGreaterThan(20)
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
  await more(page, "Copy database")
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

  await more(page, "Copy database")
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
  await more(page, "Upload a dump")

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
  expect(await moreItems(page)).toEqual(["Upload a dump"])
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
  await expect(moreMenu(page)).toHaveCount(0)
  // A dump's name still opens what is known about it: reading is the viewer's.
  await expect(dumps(page).getByRole("row").getByRole("button")).toHaveCount(4)
  await expect(
    dumps(page)
      .getByRole("row")
      .getByRole("button", { name: /^(Download|Restore|Delete) / }),
  ).toHaveCount(0)
})

test("a protected connection keeps its dumps and offers no restore and no copy", async ({
  page,
}) => {
  await mockOperate(page, { rows: { 1: { readOnly: true } } })
  await visit(page, "/databases/1/backups")
  await expect(page.getByRole("button", { name: "Back up now" })).toBeVisible()
  expect(await moreItems(page)).toEqual(["Upload a dump"])
  const row = dumpRow(page, "shop_main-new.dump")
  await expect(row.getByRole("button", { name: /^Download/ })).toBeVisible()
  await expect(row.getByRole("button", { name: /^Restore/ })).toHaveCount(0)
  await expect(dumps(page)).toContainText("This connection is protected")
})

test("dumps that could not be read say so and can be tried again", async ({ page }) => {
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
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  failing = false
  await dumps(page).getByRole("button", { name: "Try again" }).click()
  await expect(dumpRow(page, "shop_main-new.dump")).toBeVisible()
})

test("a database with no dump says what to do, and never backed up is a warning", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/5/backups")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(dumps(page)).toContainText("No dump of app is kept here")
  await expect(dumps(page).getByRole("button", { name: "Back up now" }).last()).toBeVisible()
})

test("an engine that keeps one database per connection offers no copy and no new database to restore into", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/7/backups")
  await expect(page.getByRole("button", { name: "Back up now" })).toBeVisible()
  expect(await moreItems(page)).not.toContain("Copy database")
  await dumpRow(page, "notes.sqlite")
    .getByRole("button", { name: /^Restore/ })
    .click()
  await expect(dialog(page)).toContainText("Restores into /srv/notes/notes.db")
  await expect(dialog(page).getByRole("button", { name: /A new database/ })).toHaveCount(0)
  await expect(dialog(page)).toContainText(".bak")
})

const NUMBERED_OPTIONS = {
  ...DUMP_OPTIONS,
  schemaOnly: false,
  dataOnly: false,
  tables: false,
  compression: false,
  databases: true,
}

/** A transfer of the key–value connection: a job is shown only by the connection it is of. */
const CACHE_JOB = { kind: "database.transfer.4.backup", title: "Dump cache", target: "cache" }
const CACHE_JOB_RESTORE = {
  kind: "database.transfer.4.restore",
  title: "Restore cache",
  target: "cache",
}

const keyDump = (file: string, database: string, over: Record<string, unknown> = {}) =>
  dump(file, {
    format: "JSON Lines",
    tool: "built-in",
    toolVersion: "0.7.0",
    database,
    summary: `${database.split(",").length * 6} keys in ${database.split(",").length} databases`,
    ...over,
  })

test("a key–value dump is of the connection's own numbered database unless the reader widens it", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 4/backup": job("j9", "backup", "running", CACHE_JOB),
      "GET /jobs/j9": jobThatEnds("j9", "backup", {}, CACHE_JOB),
    },
  })
  await visit(page, "/databases/4/backups")
  const open = () => page.getByRole("button", { name: "Back up now" }).first().click()
  const numbered = dialog(page).getByRole("group", { name: "Numbered databases in the dump" })
  const run = dialog(page).getByRole("button", { name: "Back up now" })

  await open()
  // Its own number, the others that hold keys, and the way to all of them.
  await expect(numbered.getByRole("button")).toHaveCount(3)
  await expect(numbered.getByRole("button", { name: /db 0/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(numbered.getByRole("button", { name: /db 3/ })).toHaveAttribute(
    "aria-pressed",
    "false",
  )
  await expect(dialog(page)).toContainText("Only db 0, the one this connection is on.")
  // With none pressed there is nothing to dump: an empty choice never means every database.
  await numbered.getByRole("button", { name: /db 0/ }).click()
  await expect(run).toBeDisabled()
  await numbered.getByRole("button", { name: /db 0/ }).click()
  await numbered.getByRole("button", { name: /db 3/ }).click()
  await expect(dialog(page)).toContainText("1 of them is not this connection")
  await run.click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 4/backup")?.body)
    .toEqual({ databases: [0, 3] })

  // Untouched, the form sends the connection's own number and no other.
  await progress(page).getByRole("button", { name: "Dismiss" }).click()
  await open()
  await run.click()
  await expect
    .poll(() => server.sent.filter((one) => one.request === "POST 4/backup").at(-1)?.body)
    .toEqual({ databases: [0] })

  // Every numbered database is a press the reader makes, and the request then names none.
  await progress(page).getByRole("button", { name: "Dismiss" }).click()
  await open()
  await numbered.getByRole("button", { name: "Every database" }).click()
  await expect(dialog(page)).toContainText("Every numbered database of the server")
  await run.click()
  await expect
    .poll(() => server.sent.filter((one) => one.request === "POST 4/backup").at(-1)?.body)
    .toEqual({})
})

test("a dump that reaches other numbered databases is restored only by a press that says so", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "GET 4/backups": {
        dir: "/var/backups/jd/databases/cache",
        options: NUMBERED_OPTIONS,
        files: [keyDump("cache-all.jsonl.gz", "0,1,3"), keyDump("cache-own.jsonl.gz", "0")],
      },
      "POST 4/restore": job("j6", "restore", "running", CACHE_JOB_RESTORE),
      "GET /jobs/j6": jobThatEnds("j6", "restore", { database: "0,1,3" }, CACHE_JOB_RESTORE),
    },
  })
  await visit(page, "/databases/4/backups")
  // What a dump holds is said by number, never as "everything".
  await expect(dumpRow(page, "cache-all.jsonl.gz")).toContainText("3 numbered databases")
  await expect(dumpRow(page, "cache-own.jsonl.gz")).toContainText("db 0")

  await dumpRow(page, "cache-all.jsonl.gz")
    .getByRole("button", { name: /^Restore/ })
    .click()
  const back = dialog(page).getByRole("button", { name: /Back where it came from/ })
  const run = dialog(page).getByRole("button", { name: "Restore…" })
  // Nothing is chosen for the reader, and nothing runs until they choose.
  await expect(back).toHaveAttribute("aria-pressed", "false")
  await expect(run).toBeDisabled()
  await expect(dialog(page)).toContainText(
    "This dump reaches past db 0, the database this connection is on",
  )
  await expect(dialog(page)).toContainText("It also holds databases 1, 3")
  // Several numbers cannot be loaded into one other number.
  await expect(dialog(page).getByRole("button", { name: /Another numbered database/ })).toHaveCount(
    0,
  )
  await back.click()
  await expect(dialog(page)).toContainText("What databases 0, 1, 3 hold now is replaced")
  await run.click()

  const confirm = page.getByRole("dialog", { name: "Restore over databases 0, 1, 3" })
  await expect(confirm).toContainText(
    "It writes into databases 1, 3, which are not the database this connection is on.",
  )
  await confirm.getByRole("button", { name: "Restore", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 4/restore")?.body)
    .toEqual({ file: "cache-all.jsonl.gz", target: "this", dumpFirst: true })
  await progress(page).getByRole("button", { name: "Dismiss" }).click()

  // A dump of the connection's own number goes back to it, as any restore does.
  await dumpRow(page, "cache-own.jsonl.gz")
    .getByRole("button", { name: /^Restore/ })
    .click()
  await expect(
    dialog(page).getByRole("button", { name: /Back where it came from/ }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect(dialog(page)).toContainText("What db 0 holds now is replaced")
  await expect(
    dialog(page).getByRole("button", { name: /Another numbered database/ }),
  ).toBeVisible()
})

test("a transfer of another connection named in the address is not shown as this one's", async ({
  page,
}) => {
  await mockOperate(page, {
    answers: {
      "GET /jobs/j2x": {
        job: {
          ...job("j2x", "restore", "succeeded", { endedAt: NOW }),
          kind: "database.transfer.2.restore",
        },
        lines: [line(1, "stdout", "restored blog")],
      },
    },
  })
  await visit(page, "/databases/1/backups?job=j2x")
  await expect(dumps(page)).toBeVisible()
  await expect(
    page.getByText("No dump, restore or copy of shop is kept under that id"),
  ).toBeVisible()
  await expect(progress(page)).toHaveCount(0)
  await expect(page.getByText("restored blog")).toHaveCount(0)
})

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

/** The rail's panel of this database: the one that lists its Settings pages. */
const rail = (page: Page) => page.getByRole("navigation").filter({ hasText: "Danger zone" })

test("settings are pages in the rail, each a form in sections, with only the pages the engine has", async ({
  page,
}) => {
  const server = await mockOperate(page)
  const listed = (names: string[]) =>
    expect(rail(page).getByRole("link")).toContainText(names, { useInnerText: true })

  await visit(page, "/databases/1/settings")
  await listed([
    "General",
    "Network",
    "Resources",
    "Parameters",
    "Extensions",
    "Version",
    "Danger zone",
  ])
  for (const id of ["address", "connection", "protection"]) {
    await expect(section(page, id)).toBeVisible()
  }
  for (const [path, id] of [
    ["network", "reachability"],
    ["resources", "resources"],
    ["parameters", "parameters"],
    ["extensions", "extensions"],
    ["danger", "danger"],
  ]) {
    await visit(page, `/databases/1/settings/${path}`)
    await expect(section(page, id)).toBeVisible()
  }
  await visit(page, "/databases/1/settings/parameters")
  await expect(section(page, "parameters").getByRole("heading", { level: 2 })).toHaveText(
    "Server parameters",
  )

  // A file has no server: no network, no extensions.
  await visit(page, "/databases/7/settings")
  await listed(["General", "Parameters", "Danger zone"])
  await expect(rail(page).getByRole("link", { name: "Network", exact: true })).toHaveCount(0)
  await visit(page, "/databases/7/settings/parameters")
  await expect(section(page, "parameters").getByRole("heading", { level: 2 })).toHaveText(
    "Parameters",
  )
  // Its address all the same says so, without a form the server would refuse.
  await visit(page, "/databases/7/settings/network")
  await expect(page.getByText("SQLite has no network to configure")).toBeVisible()
  await expect(section(page, "reachability")).toHaveCount(0)

  // A document database answers the parameters route with status counters:
  // those are Performance's, and Settings has no page for them.
  await visit(page, "/databases/5/settings")
  await expect(section(page, "address")).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Parameters", exact: true })).toHaveCount(0)
  expect(server.seen).not.toContain("GET 5/settings")
})

test("an address from the one-page Settings lands on the page that holds its section", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/1/settings#danger")
  await expect.poll(() => where(page)).toBe("/databases/1/settings/danger")
  await expect(section(page, "danger")).toBeVisible()

  await visit(page, "/databases/1/settings#reachability")
  await expect.poll(() => where(page)).toBe("/databases/1/settings/network")

  // What else the server holds is Home's now.
  await visit(page, "/databases/1/settings#databases")
  await expect.poll(() => where(page)).toBe("/databases/1")

  // A link to one parameter shows the parameters, searched for it.
  await visit(page, "/databases/1/settings?q=work_mem")
  await expect.poll(() => where(page)).toBe("/databases/1/settings/parameters?q=work_mem")
  await expect(section(page, "parameters").locator("[data-parameter=work_mem]")).toBeVisible()
  // The rail marks the page being read.
  await expect(rail(page).getByRole("link", { name: "Parameters", exact: true })).toHaveAttribute(
    "data-active",
    "true",
  )
})

test("saving the connection's labels sends only what changed", async ({ page }) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 1": (_call: number, _url: URL, body: unknown) => ({ id: 1, ...(body as object) }),
    },
  })
  await visit(page, "/databases/1/settings")
  const form = page.getByRole("form", { name: "Connection labels" })
  // A clean page has nothing to save, and no button for it.
  await expect(saveBar(page)).toHaveCount(0)

  await form.getByLabel("Notes").fill("the shop's orders")
  await expect(section(page, "connection")).toContainText("Unsaved changes")
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await expect(saveBar(page)).toContainText("Connection labels · Applies immediately")
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
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
  await expect(saveBar(page).getByRole("button", { name: "Save", exact: true })).toBeDisabled()
  await form.getByLabel("Name").fill("")
  await expect(form.getByRole("alert")).toContainText("A connection needs a name")
  await saveBar(page).getByRole("button", { name: "Discard" }).click()
  await expect(form.getByLabel("Name")).toHaveValue("shop")
})

test("protecting a connection is one switch of the same form", async ({ page }) => {
  const server = await mockOperate(page, { answers: { "PUT 1": { id: 1 } } })
  await visit(page, "/databases/1/settings")
  const form = page.getByRole("form", { name: "Connection labels" })
  await form.getByRole("switch", { name: /Refuse every change/ }).click()
  await expect(section(page, "protection")).toContainText("Unsaved changes")
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1")?.body)
    .toEqual({
      readOnly: true,
    })
})

/** The address, as the form the page's bar saves. */
const address = (page: Page) => page.getByRole("form", { name: "Address" })

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

  // The fields are on the page, showing what the connection's row says, and the
  // sealed string has not been asked for to draw them.
  await expect(address(page).getByLabel("Host")).toHaveValue("127.0.0.1")
  await expect(address(page).getByLabel("Port")).toHaveValue("5432")
  await expect(address(page).getByLabel("Account")).toHaveValue("app")
  await expect(address(page).getByLabel("shop_main", { exact: false })).toHaveCount(0)
  // The password is not put into the page.
  await expect(address(page).getByLabel("Password")).toHaveValue("")
  await expect(saveBar(page)).toHaveCount(0)
  expect(server.seen).not.toContain("GET 1/url")

  // The first change reads it, and what the fields do not cover is listed as kept.
  await address(page).getByLabel("Password").fill("wrong")
  const kept = address(page).locator("[data-slot=kept-options]")
  await expect(kept).toContainText("sslmode=verify-full")
  await expect(kept).toContainText("connect_timeout=5")
  expect(server.seen.filter((one) => one === "GET 1/url")).toHaveLength(1)
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await expect(saveBar(page)).toContainText("Address · Applies immediately")

  // Test is the section's own verb, and dials what is typed.
  const test = section(page, "address").getByRole("button", { name: "Test" })
  await test.click()
  await expect(address(page)).toContainText("It refused the connection")
  await expect(address(page)).toContainText("password authentication failed")

  await address(page).getByLabel("Password").fill("n3w p@ss")
  // What will be saved, with the password hidden.
  await expect(address(page).locator("[data-slot=connection-preview]")).toHaveText(
    "postgres://app:••••••@127.0.0.1:5432/shop_main?sslmode=verify-full&connect_timeout=5",
  )
  await test.click()
  await expect(address(page).locator("[data-slot=test-result]")).toContainText(
    "Answered: PostgreSQL 16.4",
  )
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()

  const patched =
    "postgres://app:n3w%20p%40ss@127.0.0.1:5432/shop_main?sslmode=verify-full&connect_timeout=5"
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1")?.body)
    .toEqual({
      dsn: patched,
    })
  // A string that has just answered is not dialled a second time to be saved.
  const dialled = server.sent.filter((one) => one.request === "POST /databases/test")
  expect(dialled.map((one) => one.body)).toEqual([
    { driver: "postgres", dsn: patched.replace("n3w%20p%40ss", "wrong") },
    { driver: "postgres", dsn: patched },
  ])
  // Saved, the page is clean again, and the string is not read a second time.
  await expect(saveBar(page).getByRole("button", { name: "Save", exact: true })).toHaveCount(0)
  await expect(address(page).getByLabel("Password")).toHaveValue("")
  expect(server.seen.filter((one) => one === "GET 1/url")).toHaveLength(1)
})

test("a save dials the new address first, and one that does not answer is saved only when the reader says so", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST /databases/test": { ok: false, error: "dial tcp 10.0.0.9:5432: connection refused" },
      "PUT 1": { id: 1 },
    },
  })
  await visit(page, "/databases/1/settings")
  await address(page).getByLabel("Host").fill("db.internal")
  await expect(section(page, "address")).toContainText("Unsaved changes")
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()

  // It was dialled, it refused, and nothing was written.
  await expect(address(page)).toContainText("It refused the connection")
  await expect(address(page)).toContainText("connection refused")
  const moved =
    "postgres://app:s3cret@db.internal:5432/shop_main?sslmode=verify-full&connect_timeout=5"
  expect(server.sent.map((one) => [one.request, one.body])).toEqual([
    ["POST /databases/test", { driver: "postgres", dsn: moved }],
  ])
  // The form is as the reader left it, and still to be saved.
  await expect(address(page).getByLabel("Host")).toHaveValue("db.internal")
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await expect(section(page, "address")).toContainText("Not saved")

  await address(page).getByRole("button", { name: "Save it untested" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1")?.body)
    .toEqual({ dsn: moved })
  await expect(saveBar(page).getByRole("button", { name: "Save", exact: true })).toHaveCount(0)
})

test("a refused save of the address keeps what was typed, with the server's words", async ({
  page,
}) => {
  await mockOperate(page, {
    answers: {
      "POST /databases/test": { ok: true, version: "PostgreSQL 16.4" },
      "PUT 1": refusal("invalid_input", "the connection string names no database"),
    },
  })
  await visit(page, "/databases/1/settings")
  await address(page).getByLabel("Port").fill("5433")
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect(address(page).getByRole("alert")).toContainText(
    "It was not saved. the connection string names no database",
  )
  await expect(address(page).getByLabel("Port")).toHaveValue("5433")
  await expect(saveBar(page)).toContainText("1 unsaved change")
})

test("the two editors of the address are one string, and Discard puts the saved one back", async ({
  page,
}) => {
  const server = await mockOperate(page)
  await visit(page, "/databases/1/settings")
  const fields = address(page).getByRole("radio", { name: "Fields" })
  const whole = address(page).getByRole("radio", { name: "Whole string" })
  const string = address(page).getByLabel("Connection string")
  const options = "?sslmode=verify-full&connect_timeout=5"

  await address(page).getByLabel("Host").fill("localhost")
  await whole.click()
  await expect(string).toHaveValue(`postgres://app:s3cret@localhost:5432/shop_main${options}`)
  // It is drawn as a password is: a connection string is one with an address round it.
  await expect(string).toHaveAttribute("type", "password")
  // Back to the fields and on: the whole string follows every field, not only the first visit.
  await fields.click()
  await address(page).getByLabel("Port").fill("5433")
  await whole.click()
  await expect(string).toHaveValue(`postgres://app:s3cret@localhost:5433/shop_main${options}`)

  // A string retyped whole is what the fields are then laid over.
  await string.fill("postgres://app:s3cret@db.internal:6000/shop_main?sslmode=require")
  await fields.click()
  await expect(address(page).getByLabel("Host")).toHaveValue("db.internal")
  await expect(address(page).getByLabel("Port")).toHaveValue("6000")
  await expect(address(page).locator("[data-slot=kept-options]")).toContainText("sslmode=require")
  await expect(address(page).locator("[data-slot=kept-options]")).not.toContainText(
    "connect_timeout",
  )
  await expect(address(page).locator("[data-slot=connection-preview]")).toHaveText(
    "postgres://app:••••••@db.internal:6000/shop_main?sslmode=require",
  )

  // One the fields cannot take apart stays whole, and says why.
  await whole.click()
  await string.fill("not an address")
  await fields.click()
  await expect(whole).toBeChecked()
  await expect(address(page)).toContainText("The fields cannot take this string apart")

  await saveBar(page).getByRole("button", { name: "Discard" }).click()
  await expect(saveBar(page).getByRole("button", { name: "Save", exact: true })).toHaveCount(0)
  await expect(fields).toBeChecked()
  await expect(address(page).getByLabel("Host")).toHaveValue("127.0.0.1")
  await expect(address(page).getByLabel("Port")).toHaveValue("5432")
  // Nothing was sent by any of it.
  expect(server.sent).toEqual([])
})

test("a row that cannot be opened is repaired by typing its string whole", async ({ page }) => {
  const server = await mockOperate(page, {
    rows: { 1: { broken: true, brokenReason: "The saved string cannot be unsealed." } },
    answers: {
      "POST /databases/test": { ok: true, version: "PostgreSQL 16.4" },
      "PUT 1": { id: 1 },
    },
  })
  await visit(page, "/databases/1/settings")
  await expect(section(page, "address")).toContainText("The saved string cannot be unsealed.")
  await expect(address(page).getByRole("radio", { name: "Whole string" })).toBeChecked()
  await address(page).getByLabel("Connection string").fill("postgres://app:pw@127.0.0.1:5432/shop")
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1")?.body)
    .toEqual({ dsn: "postgres://app:pw@127.0.0.1:5432/shop" })
  // There was no string to read, and none was asked for.
  expect(server.seen).not.toContain("GET 1/url")
})

test("publishing a server is an answer saved by the bar, confirmed with the rule named", async ({
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
  await visit(page, "/databases/1/settings/network")
  const reach = section(page, "reachability")
  // What the firewall does about the port is the caption of whoever it keeps out.
  await expect(reach.getByRole("list", { name: "Who can reach it" })).toContainText(
    "ufw keeps port 5432 closed",
  )
  const group = reach.getByRole("radiogroup", { name: "Where it is reachable from" })
  await expect(group.getByRole("radio", { name: "This server" })).toBeChecked()
  await group.getByRole("radio", { name: "Anywhere" }).click()

  // Choosing is an edit: nothing is asked and nothing is sent until it is saved.
  await expect(dialog(page)).toHaveCount(0)
  await expect(saveBar(page)).toContainText("1 unsaved change")
  expect(server.sent).toEqual([])
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()

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
  await visit(page, "/databases/1/settings/network")
  const reach = section(page, "reachability")
  await expect(reach.locator("[data-slot=reach-refusal]")).toContainText(
    "belongs to the compose project stack",
  )
  await expect(reach.getByRole("radiogroup", { name: "Where it is reachable from" })).toHaveCount(0)
  // A published port is a reading of state, said on the picture in amber.
  await expect(reach.locator("[data-slot=reach-warning]")).toContainText(
    "only this machine's networks get through, for now",
  )
  await expect(reach.getByRole("list", { name: "Who can reach it" })).toContainText(
    "compose project stack",
  )
})

/** The strip at the head of a settings column: what the server has stored and not yet read. */
const strip = (page: Page) => page.getByRole("region", { name: "Waiting for a restart" })

test("parameters are grouped, searchable, and changed in their rows with what ran shown afterwards", async ({
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
  await visit(page, "/databases/1/settings/parameters")
  const parameters = section(page, "parameters")
  // Its headings are the engine's own, each with how many it holds.
  await expect(parameters.getByRole("button", { name: /Resource Usage/ })).toBeVisible()
  await expect(parameters.getByRole("button", { name: /Write-Ahead Log/ })).toBeVisible()
  // What waits for a restart is said once, at the head of the column, by name.
  await expect(strip(page)).toContainText("1 waiting for a restart")
  await expect(strip(page)).toContainText("shared_buffers")
  await expect(parameters).not.toContainText("stored and not yet in effect")

  await parameters.getByLabel("Filter the parameters").fill("bgwriter")
  const row = parameters.locator("[data-parameter=bgwriter_delay]")
  const value = row.getByLabel("Change bgwriter_delay")
  await expect(value).toHaveValue("210")
  await expect(row).toContainText("changed")
  await expect(row).toContainText("default 200")
  await expect(parameters.locator("[data-parameter]")).toHaveCount(1)
  // The search is in the address, so a link can name a parameter.
  await expect.poll(() => where(page)).toBe("/databases/1/settings/parameters?q=bgwriter")

  // The value is the control. One out of range is said under it, and holds Save.
  const save = saveBar(page).getByRole("button", { name: "Save", exact: true })
  await value.fill("5")
  await expect(row.getByRole("alert")).toHaveText(/Between 10 and 10000 ms\./)
  await expect(save).toBeDisabled()
  await value.fill("250")
  await expect(row).toContainText("unsaved")
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await expect(saveBar(page)).toContainText("Server parameters · Applies immediately")
  await save.click()
  await expect(parameters.locator("[data-slot=parameter-change]")).toContainText(
    `ALTER SYSTEM SET "bgwriter_delay" = '250'`,
  )
  await expect(parameters.locator("[data-slot=parameter-change]")).toContainText("In effect now.")
  expect(server.sent.find((one) => one.request === "PUT 1/settings")?.body).toEqual({
    name: "bgwriter_delay",
    value: "250",
  })
  // An engine with a default to reset to offers that, in the row, and no second way back.
  await expect(
    parameters.locator("[data-slot=parameter-change]").getByRole("button", { name: /^Put back/ }),
  ).toHaveCount(0)

  // One that is not the default can be put back to it, by the same Save.
  await row.getByRole("button", { name: "Reset bgwriter_delay to its default" }).click()
  await expect(value).toHaveValue("200")
  await expect(row).toContainText("resets")
  await save.click()
  await expect(parameters.locator("[data-slot=parameter-change]")).toContainText(
    'ALTER SYSTEM RESET "bgwriter_delay"',
  )
  expect(server.sent.filter((one) => one.request === "PUT 1/settings").at(-1)?.body).toEqual({
    name: "bgwriter_delay",
    reset: true,
  })
})

test("several parameters are saved by one press, one request each, and a refused one keeps its value", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 1/settings": (_call: number, _url: URL, body: unknown) => {
        const sent = body as { name: string; value: string }
        if (sent.name === "work_mem") {
          return refusal("invalid_input", "work_mem is outside the range the server takes")
        }
        return {
          name: sent.name,
          value: sent.value,
          statements: [`ALTER SYSTEM SET "${sent.name}" = '${sent.value}'`],
          persisted: true,
          restartRequired: sent.name === "wal_level",
        }
      },
    },
  })
  await visit(page, "/databases/1/settings/parameters")
  const parameters = section(page, "parameters")
  const search = parameters.getByLabel("Filter the parameters")
  // Each is edited where it is listed; the edits are kept while the list is searched.
  await search.fill("bgwriter")
  await parameters.getByLabel("Change bgwriter_delay").fill("250")
  await search.fill("work_mem")
  await parameters.getByLabel("Change work_mem").fill("8192")
  await search.fill("wal_level")
  await parameters
    .locator("[data-parameter=wal_level]")
    .getByRole("radio", { name: "logical" })
    .click()
  await expect(saveBar(page)).toContainText("3 unsaved changes")
  // What is typed joins what waits, on the strip, by name.
  await expect(strip(page)).toContainText("1 waiting for a restart · 3 unsaved changes")
  await expect(strip(page)).toContainText("wal_level")

  await search.fill("")
  // A closed heading says it holds unsaved work.
  await expect(parameters.getByRole("button", { name: /Resource Usage/ })).toContainText(
    "2 unsaved",
  )
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect
    .poll(() => server.sent.filter((one) => one.request === "PUT 1/settings").length)
    .toBe(3)
  // In the order the server lists them, each with only its own name and value.
  expect(
    server.sent.filter((one) => one.request === "PUT 1/settings").map((one) => one.body),
  ).toEqual([
    { name: "work_mem", value: "8192" },
    { name: "bgwriter_delay", value: "250" },
    { name: "wal_level", value: "logical" },
  ])
  const change = parameters.locator("[data-slot=parameter-change]")
  await expect(change).toContainText("bgwriter_delay: Set to 250 ms. In effect now.")
  await expect(change).toContainText(
    "wal_level: Set to logical. Stored. It takes effect when the server is restarted.",
  )
  // The one the server refused is still to be saved, as it was typed, with the server's words.
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await expect(parameters).toContainText("Not saved")
  await search.fill("work_mem")
  const refused = parameters.locator("[data-parameter=work_mem]")
  await expect(refused.getByLabel("Change work_mem")).toHaveValue("8192")
  await expect(refused.getByRole("alert")).toContainText("outside the range the server takes")
  // Discard puts it back.
  await saveBar(page).getByRole("button", { name: "Discard" }).click()
  await expect(refused.getByLabel("Change work_mem")).toHaveValue("4096")
})

test("what waits for a restart is restarted from the head of the page, after being asked", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 1/power": { action: "restart", via: "docker", target: "shop-db", state: "running" },
    },
  })
  // The same strip is at the head of every settings page, with the way to the parameter.
  await visit(page, "/databases/1/settings")
  await expect(strip(page)).toContainText("1 waiting for a restart")
  await strip(page)
    .getByRole("link", { name: /shared_buffers/ })
    .click()
  await expect.poll(() => where(page)).toBe("/databases/1/settings/parameters?q=shared_buffers")

  const reads = () => server.seen.filter((one) => one === "GET 1/settings").length
  await expect(section(page, "parameters").locator("[data-parameter]")).toHaveCount(1)
  const before = reads()
  await page.getByRole("button", { name: "Restart", exact: true }).click()
  await expect(dialog(page)).toContainText("Restart shop")
  await expect(dialog(page)).toContainText("Every session on it is disconnected")
  expect(server.sent).toEqual([])
  await dialog(page).getByRole("button", { name: "Restart", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 1/power")?.body)
    .toEqual({ action: "restart" })
  // Restarted, what waited is in force: the page reads what the server holds again.
  await expect.poll(reads).toBeGreaterThan(before)
})

test("an edit never makes the strip arrive, and a server with nothing waiting has none", async ({
  page,
}) => {
  await mockOperate(page, {
    answers: {
      "GET 1/settings": {
        ...SETTINGS,
        settings: SETTINGS.settings.map((one) => ({ ...one, pendingRestart: false })),
      },
    },
  })
  // Tall enough that typing in the field scrolls nothing.
  await page.setViewportSize({ width: 1280, height: 1400 })
  await visit(page, "/databases/1/settings")
  const name = page.getByRole("form", { name: "Connection labels" }).getByLabel("Name")
  await expect(name).toHaveValue("shop")
  await risen(page)
  const before = await name.boundingBox()
  await name.fill("shop two")
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await expect(strip(page)).toHaveCount(0)
  // The field being typed in is where it was.
  expect((await name.boundingBox())?.y).toBe(before?.y)
})

test("the strip is in its place when the next settings page opens, before that page has read anything", async ({
  page,
}) => {
  const read = hold()
  await page.setViewportSize({ width: 1280, height: 1100 })
  await mockOperate(page, {
    answers: {
      // The first page's read answers; every later one waits to be let go.
      "GET 1/settings": async (call: number) => {
        if (call > 0) await read.until
        return SETTINGS
      },
    },
  })
  await visit(page, "/databases/1/settings")
  await expect(strip(page)).toContainText("shared_buffers")
  await rail(page).getByRole("link", { name: "Resources", exact: true }).click()
  const resources = section(page, "resources")
  await expect(resources).toBeVisible()
  // Drawn from what General learnt, while this page's own read is still out.
  await expect(strip(page)).toContainText("shared_buffers")
  await risen(page)
  const before = await resources.boundingBox()
  read.release()
  await page.waitForLoadState("networkidle")
  await risen(page)
  expect((await resources.boundingBox())?.y).toBe(before?.y)
  await expect(strip(page)).toContainText("1 waiting for a restart")
})

test("a chip pressed right after typing keeps the search that was typed", async ({ page }) => {
  await mockOperate(page)
  await visit(page, "/databases/1/settings/parameters")
  const parameters = section(page, "parameters")
  const search = parameters.getByLabel("Filter the parameters")
  await search.fill("bgwriter")
  await expect.poll(() => where(page)).toBe("/databases/1/settings/parameters?q=bgwriter")
  // Typed, and narrowed before the address has caught up with the typing.
  await search.fill("shared")
  await parameters.getByRole("button", { name: /^Changed/ }).click()
  await expect
    .poll(() => where(page))
    .toBe("/databases/1/settings/parameters?q=shared&only=changed")
  await expect(parameters.locator("[data-parameter]")).toHaveCount(1)
  await expect(parameters.locator("[data-parameter=shared_buffers]")).toBeVisible()
  await page.waitForTimeout(700)
  expect(where(page)).toBe("/databases/1/settings/parameters?q=shared&only=changed")
})

test("a parameter says what it is: waiting for a restart, withheld, or not the reader's to change", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/1/settings/parameters?only=pending")
  const parameters = section(page, "parameters")
  const buffers = parameters.locator("[data-parameter=shared_buffers]")
  await expect(buffers).toContainText("waiting for a restart")
  await expect(buffers).toContainText("256.0 MB")
  await expect(parameters.locator("[data-parameter]")).toHaveCount(1)

  await parameters.getByRole("button", { name: /^All/ }).click()
  await parameters.getByLabel("Filter the parameters").fill("conninfo")
  // A withheld value is said to be hidden: it is never drawn as empty.
  await expect(parameters.locator("[data-parameter=primary_conninfo]")).toContainText("hidden")
  await expect(parameters.getByLabel("Change primary_conninfo")).toHaveCount(0)
  await parameters.getByLabel("Filter the parameters").fill("server_version")
  await expect(parameters.locator("[data-parameter=server_version]")).toBeVisible()
  await expect(parameters.getByLabel("Change server_version")).toHaveCount(0)
})

test("a parameter's row is one height, whatever it has to say", async ({ page }) => {
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await mockOperate(page)
    await visit(page, "/databases/1/settings/parameters?q=e")
    const rows = section(page, "parameters").locator("[data-parameter]")
    await expect(rows.first()).toBeVisible()
    await risen(page)
    // One with a description, a unit, a default and a tag beside one with none of them.
    // Inside its hairline, which the last row under a heading does not have.
    const heights = await rows.evaluateAll((all) => all.map((row) => row.clientHeight))
    expect(heights.length).toBeGreaterThan(4)
    expect(new Set(heights).size, `rows at ${width}: ${heights.join(", ")}`).toBe(1)
    // Editing one, and its value being out of range, moves nothing under it.
    const edited = section(page, "parameters").locator("[data-parameter=bgwriter_delay]")
    const next = edited.locator("xpath=following-sibling::li[1]")
    // Where typing will not scroll the page: the row is already in view.
    await edited.getByLabel("Change bgwriter_delay").focus()
    const before = await next.boundingBox()
    await edited.getByLabel("Change bgwriter_delay").fill("not a number")
    await expect(edited.getByRole("alert")).toBeVisible()
    expect((await next.boundingBox())?.y).toBe(before?.y)
  }
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
  await visit(page, "/databases/1/settings/parameters?q=wal_level")
  const parameters = section(page, "parameters")
  const row = parameters.locator("[data-parameter=wal_level]")
  // That it waits for a restart is said on the row before anything is changed.
  await expect(row).toContainText("restart to apply")
  await expect(row.getByRole("radio", { name: "replica" })).toBeChecked()
  await row.getByRole("radio", { name: "logical" }).click()
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect(parameters.locator("[data-slot=parameter-change]")).toContainText(
    "It takes effect when the server is restarted.",
  )
  expect(server.sent.find((one) => one.request === "PUT 1/settings")?.body).toEqual({
    name: "wal_level",
    value: "logical",
  })
  // A yes/no is the engine's own two words.
  await parameters.getByLabel("Filter the parameters").fill("fsync")
  const fsync = parameters.locator("[data-parameter=fsync]")
  await expect(fsync.getByRole("radio", { name: "on" })).toBeChecked()
  await expect(fsync.getByRole("radio", { name: "off" })).toBeVisible()
})

test("a key–value server's parameters are its own configuration, and a secret is write-only", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "PUT 4/redis/config": { name: "slowlog-max-len", secret: false, rewritten: true },
    },
  })
  await visit(page, "/databases/4/settings/parameters")
  const parameters = section(page, "parameters")
  await expect(parameters.getByRole("button", { name: /Memory/ })).toBeVisible()
  // Read from the server's own configuration route, not the SQL engines' list.
  expect(server.seen).toContain("GET 4/redis/config")
  expect(server.seen).not.toContain("GET 4/settings")

  await parameters.getByLabel("Filter the parameters").fill("requirepass")
  // A secret is never sent back: its field is empty and says only that one is set.
  const secret = parameters.getByLabel("Change requirepass")
  await expect(secret).toHaveValue("")
  await expect(secret).toHaveAttribute("placeholder", "set")
  await expect(secret).toHaveAttribute("type", "password")
  await parameters.getByLabel("Filter the parameters").fill("slowlog")
  await parameters.getByLabel("Change slowlog-max-len").fill("256")
  // Written to the configuration file too, unless the reader says otherwise.
  await expect(
    parameters.getByRole("switch", { name: /Write changes to the configuration file/ }),
  ).toBeChecked()
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 4/redis/config")?.body)
    .toEqual({
      name: "slowlog-max-len",
      value: "256",
      rewrite: true,
    })
  // What ran is shown with the parameter named as it is written, not in the label's capitals.
  const change = parameters.locator("[data-slot=parameter-change]")
  await expect(change).toContainText("CONFIG SET slowlog-max-len 256")
  await expect(change.locator("p.eyebrow")).toHaveText("What ran")
  // The server publishes no default to reset to: the change is put back to what it was.
  await change.getByRole("button", { name: "Put back 128" }).click()
  await expect
    .poll(() => server.sent.filter((one) => one.request === "PUT 4/redis/config").at(-1)?.body)
    .toEqual({ name: "slowlog-max-len", value: "128", rewrite: true })
  await expect(change).toContainText("CONFIG SET slowlog-max-len 128")
  await expect(change).toContainText("Put back to 128.")
  await expect(change.getByRole("button", { name: /^Put back/ })).toHaveCount(0)

  // It has modules where a SQL engine has extensions.
  await visit(page, "/databases/4/settings/extensions")
  await expect(section(page, "extensions").getByRole("heading", { level: 2 })).toHaveText("Modules")
  await expect(section(page, "extensions")).toContainText("ReJSON")
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
  await visit(page, "/databases/1/settings/danger")
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
  expect(where(page)).toBe("/databases/1/settings/danger")
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
  await visit(page, "/databases/1/settings/danger")
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
  await visit(page, "/databases/1/settings/danger")
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
  await visit(page, "/databases/7/settings/danger")
  await expect(section(page, "danger")).toContainText("Typed to confirm: notes.db")
  await expect(section(page, "danger")).toContainText("The database file is deleted")
  await visit(page, "/databases/4/settings/danger")
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
  await visit(page, "/databases/1/settings/danger")
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
  // The address is read as its picture and tested; its fields are not drawn.
  await expect(page.getByRole("form", { name: "Address" })).toHaveCount(0)
  await expect(section(page, "address").getByRole("textbox")).toHaveCount(0)
  await expect(section(page, "address").getByRole("button", { name: "Test" })).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Danger zone" })).toHaveCount(0)
  await visit(page, "/databases/1/settings/danger")
  await expect(section(page, "danger")).toHaveCount(0)
  await expect(page.getByText("The danger zone is an administrator's")).toBeVisible()
  await visit(page, "/databases/1/settings/extensions")
  await expect(
    section(page, "extensions").getByRole("button", { name: /^(Enable|Update|More for) / }),
  ).toHaveCount(0)
  await visit(page, "/databases/1/settings/parameters")
  await section(page, "parameters").getByLabel("Filter the parameters").fill("work_mem")
  await expect(section(page, "parameters").locator("[data-parameter=work_mem]")).toContainText(
    "4096",
  )
  await expect(section(page, "parameters").getByLabel(/^Change /)).toHaveCount(0)
  // What waits is said to every role; restarting is for one that may interrupt the server.
  await expect(strip(page)).toContainText("1 waiting for a restart")
  await expect(page.getByRole("button", { name: "Restart", exact: true })).toHaveCount(0)
  // The addresses and the firewall are an administrator's read: it is not asked.
  await visit(page, "/databases/1/settings/network")
  await expect(section(page, "reachability")).toContainText("an administrator")
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
      .getByRole("switch", { name: /Refuse every change/ }),
  ).toBeChecked()
  await visit(page, "/databases/1/settings/parameters")
  await section(page, "parameters").getByLabel("Filter the parameters").fill("work_mem")
  await expect(section(page, "parameters").locator("[data-parameter=work_mem]")).toBeVisible()
  await expect(section(page, "parameters").getByLabel(/^Change /)).toHaveCount(0)
  await visit(page, "/databases/1/settings/extensions")
  await expect(
    section(page, "extensions").getByRole("button", { name: /^(Enable|Update|More for) / }),
  ).toHaveCount(0)
  await visit(page, "/databases/1/settings/danger")
  await expect(section(page, "danger")).toContainText("This connection is protected")
  await expect(section(page, "danger").getByRole("button", { name: "Delete…" })).toHaveCount(0)
  // Forgetting touches nothing in the database, and stays.
  await expect(section(page, "danger").getByRole("button", { name: "Forget…" })).toBeVisible()
})

test("parameters that cannot be read say so and are tried again", async ({ page }) => {
  let failing = true
  await mockOperate(page, {
    answers: {
      "GET 1/settings": () =>
        failing ? refusal("query_failed", "permission denied for view pg_settings", 502) : SETTINGS,
    },
  })
  await visit(page, "/databases/1/settings/parameters")
  const parameters = section(page, "parameters")
  await expect(parameters.getByRole("status")).toContainText("Could not read the parameters")
  await expect(parameters).toContainText("permission denied for view pg_settings")
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

test("on a server that numbers its databases the menu's dump is of the connection's own number", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "POST 4/backup": job("j5", "backup", "running", CACHE_JOB),
      "GET /jobs/j5": jobThatEnds("j5", "backup", { file: "cache.jsonl.gz", size: 10 }, CACHE_JOB),
    },
  })
  await visit(page, "/databases/4/backups")
  await page.getByRole("button", { name: "Actions for cache" }).click()
  await page.getByRole("menuitem", { name: "Back up now" }).click()
  // One press in a menu never dumps the server's other tenants.
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 4/backup")?.body)
    .toEqual({ databases: [0] })
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
    .filter({ has: page.getByRole("link", { name: `Open ${name}`, exact: true }) })
/** An account's own page: the line it opens on, its views, and the list beside it. */
const identity = (page: Page) => page.locator("[data-slot=account-identity]")
const accountRail = (page: Page) => page.locator("[data-slot=account-rail]")
const view = (page: Page, name: string | RegExp) => page.getByRole("tab", { name })
const matrix = (page: Page) => page.locator("[data-slot=grants-matrix]")
const pending = (page: Page) => page.locator("[data-slot=grants-pending]")
const leaving = (page: Page) => page.getByRole("dialog", { name: "Unsaved changes" })
/** The way back to the list, over the account's line: not the rail's own link to the page. */
const back = (page: Page) =>
  page.locator("[data-slot=page-context]").getByRole("link", { name: "Access" })

test("accounts open on who can sign in, with the dashboard's own account marked", async ({
  page,
}) => {
  await mockOperate(page)
  await visit(page, "/databases/1/access")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)

  await expect(account(page, "app")).toContainText("this connection")
  await expect(account(page, "app")).toContainText("administrator")
  await expect(account(page, "app")).toContainText("Everything on the server")
  await expect(account(page, "reporting")).toContainText("Granted on 1 database, 2 tables")
  await expect(account(page, "readers")).toContainText("no login")
  await expect(account(page, "pg_monitor")).toContainText("system")

  // A reading narrows the list, and the address says which.
  await page.getByRole("button", { name: "Cannot sign in" }).click()
  await expect(accounts(page).locator("[data-slot=choice-row]")).toHaveCount(2)
  await expect.poll(() => where(page)).toBe("/databases/1/access?show=blocked")
  await page.getByRole("button", { name: "All accounts" }).click()
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

  // The step has one command. Nothing beside it closes the dialog without asking.
  await expect(dialog(page).getByRole("button", { name: "Cancel" })).toHaveCount(0)
  await expect(dialog(page).getByRole("button", { name: "Close", exact: true })).toHaveCount(1)
  // A slip does not lose the only sight of the password: Escape asks, and so does the corner's close.
  await page.keyboard.press("Escape")
  await expect(dialog(page)).toContainText("The password is not shown again")
  await dialog(page).getByRole("button", { name: "Go back" }).click()
  await dialog(page).getByRole("button", { name: "Close", exact: true }).first().click()
  await expect(dialog(page)).toContainText("The password is not shown again")
  await dialog(page).getByRole("button", { name: "Go back" }).click()
  await expect(dialog(page).locator("[data-slot=secret-shown]")).toContainText(password)
  await dialog(page).getByRole("button", { name: "I have saved it" }).click()
  await expect(dialog(page)).toHaveCount(0)
  // The keyboard is back on the control that opened the dialog.
  await expect(accounts(page).getByRole("button", { name: "New account" })).toBeFocused()
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

test("an account is a page of its own: its line, its views in the address, the list beside it", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: { "PUT 1/server/roles/reporting": { ok: true } },
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await visit(page, "/databases/1/access")
  await account(page, "reporting").getByRole("link", { name: "Open reporting" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access/reporting")

  // It opens on what it is about, with no sheet over the list and no row of figures.
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  await expect(identity(page)).toContainText("reporting")
  await expect(identity(page)).toContainText("1 session now")
  await expect(identity(page)).toContainText("Granted on 1 database, 2 tables")
  // One command, in the brand's face: the rest are behind the menu.
  await expect(identity(page).getByRole("button", { name: "Change password" })).toBeVisible()
  await identity(page).getByRole("button", { name: "Actions for reporting" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Drop account"])
  await page.keyboard.press("Escape")

  // The views are in the address, and the first is the bare one.
  await expect(view(page, /^Grants/)).toHaveAttribute("aria-selected", "true")
  await view(page, /^Settings/).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access/reporting?tab=settings")
  await expect(page.getByRole("form", { name: "Account attributes" })).toBeVisible()

  // The list is beside it, the one on screen filled; the view goes with the reader.
  await expect(accountRail(page).getByRole("link")).toHaveText([
    /app/,
    /reporting/,
    /readers/,
    /pg_monitor/,
  ])
  await expect(accountRail(page).getByRole("link", { name: "reporting" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await accountRail(page).getByRole("link", { name: "app", exact: true }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access/app?tab=settings")
  await expect(identity(page)).toContainText("this connection")
  // Alt+↓ and Alt+↑ walk the list.
  await page.keyboard.press("Alt+ArrowDown")
  await expect.poll(() => where(page)).toBe("/databases/1/access/reporting?tab=settings")
  await page.getByRole("button", { name: "Previous account" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access/app?tab=settings")

  // The way back is the list.
  await back(page).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access")
  expect(server.sent).toEqual([])
})

test("the list beside an account is the list as it was filtered, and the way back keeps the filter", async ({
  page,
}) => {
  await mockOperate(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await visit(page, "/databases/1/access")
  await page.getByRole("button", { name: "Cannot sign in" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access?show=blocked")
  await account(page, "readers").getByRole("link", { name: "Open readers" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access/readers")

  await expect(accountRail(page).getByRole("link")).toHaveCount(2)
  await expect(accountRail(page)).toContainText("2 of 4 · cannot sign in")
  await expect(page.getByRole("button", { name: "Previous account" })).toBeDisabled()
  // Back is the list, as it was left.
  await page.goBack()
  await expect.poll(() => where(page)).toBe("/databases/1/access?show=blocked")
  await expect(accounts(page).locator("[data-slot=choice-row]")).toHaveCount(2)
  await page.goForward()
  await back(page).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access?show=blocked")

  // The filter is taken off from the rail too, and an account outside it stays on its own list.
  await visit(page, "/databases/1/access/app")
  await expect(accountRail(page).getByRole("link")).toHaveText([/app/, /readers/, /pg_monitor/])
  await accountRail(page).getByRole("button", { name: "Clear filters" }).click()
  await expect(accountRail(page).getByRole("link")).toHaveCount(4)
  await accountRail(page).getByLabel("Search the accounts").fill("report")
  await expect(accountRail(page).getByRole("link")).toHaveText([/app/, /reporting/])
})

test("an address that opened an account's sheet lands on the account's page", async ({ page }) => {
  await mockOperate(page, {
    answers: {
      "GET 2/server/roles": {
        supported: true,
        roles: [role("root", { host: "%", superuser: true }), role("blogger", { host: "10.%" })],
      },
      "GET 2/server/privileges": { supported: true, editable: ["password"], levels: [] },
      "GET 2/server/grants": { supported: true, truncated: false, grants: [] },
      "GET 2/server/roles/blogger": detail("blogger", { host: "10.%", editable: ["password"] }),
    },
  })
  await visit(page, "/databases/1/access?account=reporting")
  await expect.poll(() => where(page)).toBe("/databases/1/access/reporting")
  await expect(identity(page)).toContainText("1 session now")
  await expect(page.getByRole("dialog")).toHaveCount(0)

  // What narrowed the list is not the account's to carry; the host is.
  await visit(page, "/databases/2/access?show=admins&account=blogger&host=10.%25")
  await expect.poll(() => where(page)).toBe("/databases/2/access/blogger?host=10.%25")
  await expect(identity(page)).toContainText("blogger@10.%")

  await visit(page, "/databases/5/access?account=api&host=app_main")
  await expect.poll(() => where(page)).toBe("/databases/5/access/api?host=app_main")
  await expect(identity(page)).toContainText("kept in app_main")
})

test("an account's attributes are saved one switch at a time, and its own account keeps what would lock it out", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: { "PUT 1/server/roles/reporting": { ok: true } },
  })
  await visit(page, "/databases/1/access/reporting?tab=settings")
  const form = page.getByRole("form", { name: "Account attributes" })
  // One save for the page, and none while nothing is edited: no button per section.
  await expect(form.getByRole("switch", { name: "It can create databases" })).toBeVisible()
  await expect(saveBar(page)).toHaveCount(0)
  await expect(form.getByRole("button", { name: /save/i })).toHaveCount(0)
  await form.getByRole("switch", { name: "It can create databases" }).click()
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await saveBar(page).getByRole("button", { name: "Save" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1/server/roles/reporting")?.body)
    .toEqual({ createDb: true })

  // The account this connection signs in with.
  await visit(page, "/databases/1/access/app?tab=settings")
  const own = page.getByRole("form", { name: "Account attributes" })
  await expect(own.getByRole("switch", { name: "It can sign in" })).toBeDisabled()
  await expect(
    own.getByRole("switch", { name: "Administrator of the whole server" }),
  ).toBeDisabled()
  await expect(own).toContainText("This connection signs in with it.")
  await expect(own.getByRole("switch", { name: "It can create databases" })).toBeEnabled()
  // And it cannot be dropped from here: its line has a new password and no menu.
  await expect(identity(page).getByRole("button", { name: "Change password" })).toBeVisible()
  await expect(identity(page).getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
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
  await visit(page, "/databases/1/access/reporting")
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
  await expect(pending(page)).toContainText("2 changes not applied")
  // Nothing has been sent: a press only stages.
  expect(server.sent).toEqual([])

  await pending(page).getByRole("button", { name: "Review…" }).click()
  const review = page.getByRole("dialog", { name: "Change what reporting holds" })
  await expect(review).toContainText('REVOKE SELECT ON TABLE "public"."customers" FROM "reporting"')
  await expect(review).toContainText('GRANT UPDATE ON TABLE "public"."orders" TO "reporting"')
  await expect(review).toContainText("2 requests, 1 of them taking something away.")
  // Every statement shown came from the server's own preview.
  expect(server.sent.every((one) => one.query === "?preview=1")).toBe(true)

  await review.getByRole("button", { name: "Apply" }).click()
  await expect(review).toHaveCount(0)
  await expect(pending(page)).toHaveCount(0)
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

test("staged grants and edited attributes are kept across the views, and the page asks before it is left over them", async ({
  page,
}) => {
  const server = await mockOperate(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await visit(page, "/databases/1/access/reporting")
  await expect(matrix(page).locator("[data-object=orders]")).toBeVisible()

  // Nothing staged: the way back is taken at once.
  await back(page).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access")
  await account(page, "reporting").getByRole("link", { name: "Open reporting" }).click()

  await matrix(page).getByRole("button", { name: "UPDATE on orders" }).click()
  await matrix(page).getByRole("button", { name: "SELECT on customers" }).click()
  // A look at another view loses nothing, and each view says it holds work.
  await view(page, /^Settings/).click()
  await page.getByRole("switch", { name: "It can create databases" }).click()
  await expect(view(page, /^Grants/)).toContainText("unsaved changes")
  await expect(view(page, /^Settings/)).toContainText("unsaved changes")
  // Each view's own bar is the one on screen: the grants' is not drawn over the settings.
  await expect(pending(page)).toBeHidden()
  await expect(saveBar(page)).toContainText("1 unsaved change")

  // The way back, another account in the rail and the arrows all ask first.
  await back(page).click()
  await expect(leaving(page)).toContainText("3 changes")
  await leaving(page).getByRole("button", { name: "Keep editing" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access/reporting?tab=settings")
  await accountRail(page).getByRole("link", { name: "readers" }).click()
  await expect(leaving(page)).toContainText("3 changes")
  // A slip while it is asking means "no".
  await page.keyboard.press("Escape")
  await expect(leaving(page)).toHaveCount(0)
  await expect(page.getByRole("switch", { name: "It can create databases" })).toBeChecked()
  await view(page, /^Grants/).click()
  await expect(pending(page)).toContainText("2 changes not applied")
  await expect(saveBar(page)).toHaveCount(0)

  await page.keyboard.press("Alt+ArrowDown")
  await leaving(page).getByRole("button", { name: "Discard and go on" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access/readers")
  await expect(pending(page)).toHaveCount(0)
  // Nothing was sent.
  expect(server.sent).toEqual([])
})

test("memberships list the roles an account is in, and another is added from a menu", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: {
      "GET 1/server/roles/reporting": detail("reporting", {
        grants: REPORTING_GRANTS,
        memberOf: ["readers"],
      }),
      "POST 1/server/roles/reporting/privileges": (_call: number, url: URL, body: unknown) => ({
        ...(url.searchParams.get("preview") ? { preview: true } : { ok: true }),
        statements: [`GRANT "${(body as { memberOf: string }).memberOf}" TO "reporting"`],
      }),
      "POST 1/server/roles/reporting/privileges/revoke": (_call: number, url: URL) => ({
        ...(url.searchParams.get("preview") ? { preview: true } : { ok: true }),
        statements: ['REVOKE "readers" FROM "reporting"'],
      }),
    },
  })
  await visit(page, "/databases/1/access/reporting")
  const members = page.locator("[data-slot=memberships]")
  // Only what it is in: the server's other roles are not drawn as if it were a member of each.
  await expect(members.getByRole("button", { name: "readers" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(members.getByRole("group").getByRole("button")).toHaveCount(1)
  await expect(members).not.toContainText("app")

  // The section's one verb is on its title line.
  await members.getByRole("button", { name: "Add to a role" }).click()
  // The engine's own roles are not offered, and the one it is in is not offered again.
  await expect(page.getByRole("menuitem")).toHaveText(["app"])
  await page.getByRole("menuitem", { name: "app" }).click()
  await members.getByRole("button", { name: "readers" }).click()
  await expect(pending(page)).toContainText("2 changes not applied")

  await pending(page).getByRole("button", { name: "Review…" }).click()
  const review = page.getByRole("dialog", { name: "Change what reporting holds" })
  await expect(review).toContainText('GRANT "app" TO "reporting"')
  await expect(review).toContainText('REVOKE "readers" FROM "reporting"')
  // A role's name is drawn as it is written, beside the label's small capitals.
  await expect(review.locator("p.eyebrow span.normal-case")).toHaveText(["readers", "app"])
  await review.getByRole("button", { name: "Apply" }).click()
  await expect(review).toHaveCount(0)
  expect(server.sent.filter((one) => one.query === "").map((one) => one.body)).toEqual([
    { level: "role", memberOf: "readers" },
    { level: "role", memberOf: "app" },
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
  await visit(page, "/databases/1/access/reporting")
  await matrix(page).getByRole("button", { name: "INSERT on every table in public" }).click()
  await pending(page).getByRole("button", { name: "Review…" }).click()
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

test("an account's sessions are the ones Performance lists for it, each a way to its panel there", async ({
  page,
}) => {
  const session = (pid: string, user: string, over: Record<string, unknown> = {}) => ({
    pid,
    user,
    database: "shop_main",
    status: "idle",
    seconds: 0,
    idleSeconds: 40,
    application: "storefront",
    client: "10.0.0.7",
    ...over,
  })
  const server = await mockOperate(page, {
    answers: {
      "GET 1/activity": {
        supported: true,
        sessions: [
          session("4101", "app"),
          session("4102", "reporting", {
            status: "active",
            seconds: 12,
            query: "SELECT count(*) FROM orders",
            application: "metabase",
          }),
          session("4103", "app", { application: "" }),
        ],
      },
    },
  })
  await visit(page, "/databases/1/access/reporting?tab=sessions")
  const sessions = page.getByRole("region", { name: "Sessions" })
  await expect(sessions.locator("[data-slot=choice-row]")).toHaveCount(1)
  await expect(sessions).toContainText("metabase")
  await expect(sessions).toContainText("SELECT count(*) FROM orders")
  await expect(sessions).toContainText("Active")
  await expect(
    sessions.getByRole("link", { name: "Open session 4102 in Performance" }),
  ).toHaveAttribute("href", "/databases/1/performance?view=sessions&session=4102")
  // A session with and without a statement is one height.
  await visit(page, "/databases/1/access/app?tab=sessions")
  await expect(sessions.locator("[data-slot=choice-row]")).toHaveCount(2)
  const heights = await sessions
    .locator("[data-slot=choice-row]")
    .evaluateAll((rows) => rows.map((row) => Math.round(row.getBoundingClientRect().height)))
  expect(new Set(heights).size).toBe(1)
  // Nothing is stopped from here: the page that owns the sessions does that.
  await expect(sessions.getByRole("button")).toHaveCount(0)
  expect(server.sent).toEqual([])
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
  await page.keyboard.press("Escape")

  // From the account's own page it is the same question, and it ends on the list.
  await visit(page, "/databases/1/access/reporting")
  await identity(page).getByRole("button", { name: "Actions for reporting" }).click()
  await page.getByRole("menuitem", { name: "Drop account" }).click()
  await expect(dialog(page)).toContainText("Sessions now")
  await dialog(page).getByRole("button", { name: "Drop", exact: true }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/access")
  expect(
    server.sent.filter((one) => one.request === "DELETE 1/server/roles/reporting"),
  ).toHaveLength(2)
  // The engine's own account has no menu on its page either.
  await visit(page, "/databases/1/access/pg_monitor")
  await expect(identity(page)).toContainText("system")
  await expect(identity(page).getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
})

test("an engine whose accounts have hosts names each by both, and carries the host in what it sends", async ({
  page,
}) => {
  // A name the path cannot hold as written, at a host that is a pattern.
  const odd = role("50%@shop", { host: "10.%" })
  const server = await mockOperate(page, {
    answers: {
      "GET 2/server/roles": {
        supported: true,
        roles: [
          role("root", { host: "%", superuser: true }),
          role("root", { host: "localhost", superuser: true }),
          role("blogger", { host: "10.%" }),
          odd,
        ],
      },
      "GET 2/server/privileges": {
        supported: true,
        editable: ["password", "login", "locked", "superuser"],
        levels: [],
      },
      "GET 2/server/grants": { supported: true, truncated: false, grants: [] },
      "GET 2/server/roles/root": (_call: number, url: URL) =>
        detail("root", {
          host: url.searchParams.get("host"),
          superuser: true,
          editable: ["password", "locked"],
        }),
      // The request's own path, as the mock reads it: the name written into it once.
      "GET 2/server/roles/50%25%40shop": detail("50%@shop", {
        host: "10.%",
        editable: ["password", "locked"],
      }),
      "PUT 2/server/roles/50%25%40shop": { ok: true },
      "DELETE 2/server/roles/blogger": { ok: true },
    },
  })
  await page.setViewportSize({ width: 1440, height: 900 })
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

  // Its page is at its name, with the host beside it; both come back as they were.
  await account(page, "50%@shop@10.%").getByRole("link", { name: "Open 50%@shop@10.%" }).click()
  await expect.poll(() => where(page)).toBe("/databases/2/access/50%25%40shop?host=10.%25")
  await expect(identity(page)).toContainText("50%@shop@10.%")
  await expect(accountRail(page).getByRole("link", { name: "50%@shop@10.%" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await view(page, /^Settings/).click()
  await expect
    .poll(() => where(page))
    .toBe("/databases/2/access/50%25%40shop?host=10.%25&tab=settings")
  await page.getByRole("switch", { name: /^Locked/ }).click()
  await saveBar(page).getByRole("button", { name: "Save" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 2/server/roles/50%25%40shop")?.body)
    .toEqual({ locked: true, host: "10.%" })
  // A reload opens the same account.
  await page.reload()
  await expect(identity(page)).toContainText("50%@shop@10.%")

  // Two accounts of one name are two pages, told apart by the host.
  await accountRail(page).getByRole("link", { name: "root@localhost" }).click()
  await expect.poll(() => where(page)).toBe("/databases/2/access/root?host=localhost&tab=settings")
  await expect(identity(page)).toContainText("root@localhost")
  await accountRail(page).getByRole("link", { name: "root@%" }).click()
  await expect.poll(() => where(page)).toBe("/databases/2/access/root?host=%25&tab=settings")
  await expect(identity(page)).toContainText("root@%")
})

test("a viewer reads the accounts and what each holds, and is offered nothing that changes them", async ({
  page,
}) => {
  const server = await mockOperate(page, { viewer: true })
  await visit(page, "/databases/1/access/reporting")
  await expect(identity(page)).toContainText("1 session now")
  await expect(identity(page).getByRole("button")).toHaveCount(0)
  // What it holds is read; nothing on it can be pressed.
  await expect(matrix(page).locator("[data-object=orders]")).toContainText("SELECT")
  await expect(matrix(page).getByRole("button", { name: "SELECT on orders" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Add to a role" })).toHaveCount(0)
  // Its attributes are said in words: a switch that cannot be thrown is not drawn.
  await view(page, /^Settings/).click()
  const settings = page.getByRole("form", { name: "Account attributes" })
  await expect(settings).toContainText("It can create databases")
  await expect(settings.getByRole("switch")).toHaveCount(0)
  await expect(settings.getByRole("textbox")).toHaveCount(0)
  await expect(saveBar(page)).toHaveCount(0)

  await visit(page, "/databases/1/access")
  await expect(account(page, "reporting")).toBeVisible()
  await expect(accounts(page).getByRole("button", { name: "New account" })).toHaveCount(0)
  await expect(accounts(page).getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
  expect(server.sent).toEqual([])
})

test("a protected connection reads its accounts and draws no way to change one", async ({
  page,
}) => {
  const server = await mockOperate(page, { rows: { 1: { readOnly: true } } })
  await visit(page, "/databases/1/access/reporting")
  await expect(matrix(page).locator("[data-object=orders]")).toContainText("SELECT")
  await expect(matrix(page).getByRole("button", { name: "SELECT on orders" })).toHaveCount(0)
  await expect(identity(page)).toContainText("protected")
  await expect(identity(page).getByRole("button")).toHaveCount(0)
  await view(page, /^Settings/).click()
  await expect(
    page.getByRole("form", { name: "Account attributes" }).getByRole("switch"),
  ).toHaveCount(0)
  await visit(page, "/databases/1/access")
  await expect(accounts(page)).toContainText("This connection is protected")
  await expect(accounts(page).getByRole("button", { name: "New account" })).toHaveCount(0)
  // Not even a preview is asked: the server refuses those on a protected connection too.
  expect(server.sent).toEqual([])
})

test("accounts that cannot be read say so in the server's words without metric cards", async ({
  page,
}) => {
  await mockOperate(page, {
    answers: {
      "GET 1/server/roles": refusal("query_failed", "SELECT command denied for table user", 502),
      "GET 1/server/roles/ghost": refusal("not_found", "not found", 404),
      "GET 1/server/roles/reporting": refusal(
        "query_failed",
        "permission denied for pg_authid",
        502,
      ),
    },
  })
  await visit(page, "/databases/1/access")
  await expect(accounts(page).getByRole("status")).toContainText("Could not read the accounts")
  await expect(accounts(page)).toContainText("SELECT command denied for table user")
  await expect(accounts(page).getByRole("button", { name: "Try again" })).toBeVisible()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  // An account that is not there is said calmly, with the way back; one that could not be read, with a retry.
  await visit(page, "/databases/1/access/ghost")
  const gone = page.locator("[data-slot=empty-state]:visible")
  await expect(gone).toContainText("No account named ghost on this server")
  await expect(gone.getByRole("link", { name: "Back to the accounts" })).toHaveAttribute(
    "href",
    "/databases/1/access",
  )
  await visit(page, "/databases/1/access/reporting")
  const failed = page.locator("[data-slot=page] [role=status]:visible")
  await expect(failed).toContainText("Could not read the account")
  await expect(failed).toContainText("permission denied for pg_authid")
  await expect(failed.getByRole("button", { name: "Try again" })).toBeVisible()
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
  await page.setViewportSize({ width: 1440, height: 900 })
  await visit(page, "/databases/4/access")
  const users = page.getByRole("region", { name: "Users" })
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(users).toContainText("Every command on every key")
  await expect(users).toContainText("-@all +@read +@write on ~jobs:*")
  // No SQL account route is asked of it.
  expect(server.seen.some((one) => one.includes("/server/roles"))).toBe(false)

  await users.getByRole("link", { name: "Open worker", exact: true }).click()
  await expect.poll(() => where(page)).toBe("/databases/4/access/worker")
  // The reading: the rule as the server holds it, and its parts.
  const rule = page.getByRole("region", { name: "Rule" })
  await expect(rule).toContainText("on ~jobs:* resetchannels -@all +@read +@write")
  await expect(identity(page)).toContainText("switched on")
  await expect(identity(page)).toContainText("1 password set")

  await view(page, "Settings").click()
  await expect.poll(() => where(page)).toBe("/databases/4/access/worker?tab=settings")
  const form = page.getByRole("form", { name: "The user's rule" })
  await form.getByLabel("Keys").fill("~jobs:*\n%R~shared:*")
  await saveBar(page).getByRole("button", { name: "Save" }).click()
  // Only the part of the rule that was edited is sent.
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 4/redis/acl/worker")?.body)
    .toEqual({
      keys: ["~jobs:*", "%R~shared:*"],
    })
  await expect(page.locator("[data-slot=page]")).toContainText("keeps its users in memory")
  // A rule being rewritten is not lost to a press on the way out either.
  await form.getByLabel("Channels").fill("jobs:*")
  await accountRail(page).getByRole("link", { name: "default" }).click()
  await expect(leaving(page)).toContainText("1 change")
  await leaving(page).getByRole("button", { name: "Keep editing" }).click()
  await expect(form.getByLabel("Channels")).toHaveValue("jobs:*")
  await accountRail(page).getByRole("link", { name: "default" }).click()
  await leaving(page).getByRole("button", { name: "Discard and go on" }).click()
  await expect.poll(() => where(page)).toBe("/databases/4/access/default?tab=settings")

  await expect(page.locator("[data-slot=acl-held]")).toContainText(
    "The dashboard connects as this user",
  )
  const own = page.getByRole("form", { name: "The user's rule" })
  await expect(own.getByRole("switch", { name: "Switched on: it may connect" })).toBeDisabled()
  await expect(own.getByLabel("Commands")).toBeDisabled()
  await expect(own.getByLabel("Keys")).toBeDisabled()
  await expect(identity(page).getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
})

test("a key–value user is deleted from its page, with its rule named, and that ends on the list", async ({
  page,
}) => {
  const server = await mockOperate(page, {
    answers: { "DELETE 4/redis/acl/worker": { user: ACL.users[1], persisted: true } },
  })
  await visit(page, "/databases/4/access/worker")
  await identity(page).getByRole("button", { name: "Actions for worker" }).click()
  await page.getByRole("menuitem", { name: "Delete user" }).click()
  await expect(dialog(page)).toContainText("-@all +@read +@write on ~jobs:*")
  await dialog(page).getByRole("button", { name: "Delete", exact: true }).click()
  await expect.poll(() => where(page)).toBe("/databases/4/access")
  expect(server.sent.map((one) => one.request)).toEqual(["DELETE 4/redis/acl/worker"])
})

test("a reader who may not change a key–value user has its rule to read and nothing else", async ({
  page,
}) => {
  const viewer = await mockOperate(page, { viewer: true })
  await visit(page, "/databases/4/access/worker")
  await expect(page.getByRole("region", { name: "Rule" })).toContainText("resetchannels")
  await expect(page.getByRole("tab")).toHaveCount(0)
  await expect(identity(page).getByRole("button")).toHaveCount(0)
  await expect(page.locator("[data-slot=page]").getByRole("textbox")).toHaveCount(0)
  expect(viewer.sent).toEqual([])
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
      "PUT 5/mongo/users": { ok: true },
      "DELETE 5/mongo/users": { ok: true },
    },
  })
  await visit(page, "/databases/5/access")
  const list = page.getByRole("region", { name: "Accounts" })
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(list).toContainText("readWrite@app_main")
  await expect(list).toContainText("this connection")

  await list.getByRole("link", { name: "Open api of app_main" }).click()
  await expect.poll(() => where(page)).toBe("/databases/5/access/api?host=app_main")
  await expect(identity(page)).toContainText("kept in app_main")
  await expect(identity(page)).toContainText("SCRAM-SHA-256")
  // One view, so no strip.
  await expect(page.getByRole("tab")).toHaveCount(0)
  const roles = page.getByRole("region", { name: "Roles" })
  await roles.getByRole("combobox", { name: "The role" }).click()
  await page.getByRole("option", { name: "dbAdmin" }).click()
  await roles.getByRole("button", { name: "Add role" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 5/mongo/users/grant")?.body)
    .toEqual({
      database: "app_main",
      user: "api",
      roles: [{ role: "dbAdmin", db: "app_main" }],
    })
  await roles.getByRole("button", { name: "Take readWrite@app_main away" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "POST 5/mongo/users/revoke")?.body)
    .toEqual({
      database: "app_main",
      user: "api",
      roles: [{ role: "readWrite", db: "app_main" }],
    })

  // Its one command is a new password; dropping it is in the menu and ends on the list.
  await identity(page).getByRole("button", { name: "Change password" }).click()
  const password = await dialog(page).getByLabel("New password").inputValue()
  await dialog(page).getByRole("button", { name: "Change password" }).click()
  await expect(dialog(page).locator("[data-slot=secret-shown]")).toContainText(password)
  expect(server.sent.find((one) => one.request === "PUT 5/mongo/users")?.body).toEqual({
    database: "app_main",
    user: "api",
    password,
  })
  await dialog(page).getByRole("button", { name: "I have saved it" }).click()
  await identity(page).getByRole("button", { name: "Actions for api" }).click()
  await page.getByRole("menuitem", { name: "Drop user" }).click()
  await dialog(page).getByRole("button", { name: "Drop", exact: true }).click()
  await expect.poll(() => where(page)).toBe("/databases/5/access")
  expect(server.sent.find((one) => one.request === "DELETE 5/mongo/users")?.body).toEqual({
    database: "app_main",
    user: "api",
  })

  // The account the dashboard signs in with cannot be dropped: not from the list, not from its page.
  await list.getByRole("button", { name: "Actions for root" }).click()
  await expect(page.getByRole("menuitem", { name: "Change password" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Drop user" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await visit(page, "/databases/5/access/root?host=admin")
  await expect(page.getByRole("region", { name: "Roles" })).toContainText("root on admin")
  await expect(identity(page).getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Take root@admin away" })).toHaveCount(0)

  // The other view of the list: what a role is.
  await visit(page, "/databases/5/access")
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
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(page.getByRole("region", { name: "Accounts" })).toContainText(
    "A server with no user accepts every connection that reaches it",
  )
  // A user that is not there is said on its page, with the way back.
  await visit(page, "/databases/5/access/ghost?host=admin")
  await expect(page.locator("[data-slot=empty-state]:visible")).toContainText(
    "No user named ghost@admin on this server",
  )
})

// What the overhaul promises of these pages: they hold still, and a row is one height.

/** A read that waits until the test lets it go, so a page can be measured before and after it lands. */
function held<T>(value: T) {
  let release = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  return { answer: () => gate.then(() => value), release }
}

const box = async (page: Page, selector: string) => {
  await risen(page)
  const rect = await page.locator(selector).first().boundingBox()
  return rect && { y: Math.round(rect.y), height: Math.round(rect.height) }
}

for (const width of [1280, 390]) {
  test(`an account's row is one height with and without what is optional, and does not move when it lands, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const grants = held({ supported: true, truncated: false, grants: REPORTING_GRANTS })
    await mockOperate(page, {
      answers: {
        "GET 1/server/roles": {
          supported: true,
          roles: [
            // Every tag there is, and none at all.
            role("app", { superuser: true, createDb: true, createRole: true, connections: 3 }),
            role("plain"),
            role("reporting", { connections: 1 }),
            role("makers", { createDb: true, createRole: true, system: true, login: false }),
          ],
        },
        "GET 1/server/grants": grants.answer,
        "GET 5/mongo/users": {
          users: [...MONGO_USERS.users, { ...MONGO_USERS.users[1], user: "bare", mechanisms: [] }],
        },
        "GET 4/redis/acl": {
          ...ACL,
          users: [...ACL.users, { ...ACL.users[1], name: "off", enabled: false }],
        },
      },
    })
    const rows = (region: string) =>
      page
        .getByRole("region", { name: region })
        .locator("[data-slot=choice-row]")
        .evaluateAll((list) =>
          list.map((row) => {
            const rect = row.getBoundingClientRect()
            return { y: Math.round(rect.y), height: Math.round(rect.height) }
          }),
        )

    await visit(page, "/databases/1/access")
    await expect(account(page, "plain")).toBeVisible()
    await risen(page)
    const before = await rows("Accounts")
    expect(new Set(before.map((row) => row.height)).size, "one height while reading").toBe(1)
    grants.release()
    await expect(account(page, "reporting")).toContainText("Granted on 1 database, 2 tables")
    await risen(page)
    // What each holds has landed, and no row is taller or lower than it was.
    expect(await rows("Accounts")).toEqual(before)

    await visit(page, "/databases/4/access")
    await expect(page.getByRole("region", { name: "Users" })).toContainText("worker")
    await risen(page)
    expect(new Set((await rows("Users")).map((row) => row.height)).size).toBe(1)

    await visit(page, "/databases/5/access")
    await expect(page.getByRole("region", { name: "Accounts" })).toContainText("bare")
    await risen(page)
    expect(new Set((await rows("Accounts")).map((row) => row.height)).size).toBe(1)
  })

  test(`an account's page has its shape before the account is read, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const read = held(detail("reporting", { grants: REPORTING_GRANTS }))
    await mockOperate(page, { answers: { "GET 1/server/roles/reporting": read.answer } })
    await visit(page, "/databases/1/access/reporting")
    await expect(identity(page)).toContainText("Reading the account")
    const loading = {
      identity: await box(page, "[data-slot=account-identity]"),
      views: await box(page, "[data-slot=page] [role=tablist]"),
    }
    read.release()
    await expect(identity(page)).toContainText("1 session now")
    await expect(matrix(page).locator("[data-object=orders]")).toBeVisible()
    // The line it opens on and the strip under it are where they were.
    expect({
      identity: await box(page, "[data-slot=account-identity]"),
      views: await box(page, "[data-slot=page] [role=tablist]"),
    }).toEqual(loading)
    await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)

    if (width < 1280) {
      // One pane that scrolls as a whole: no list beside it, and nothing off the side.
      await expect(accountRail(page)).toBeHidden()
      const sideways = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
      )
      expect(sideways).toBe(false)
      // The two arrows still walk the list.
      await page.getByRole("button", { name: "Next account" }).click()
      await expect.poll(() => where(page)).toBe("/databases/1/access/readers")
    } else {
      await expect(accountRail(page)).toBeVisible()
    }
  })
}

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
  // An account's own page, on each kind of engine, and its other views.
  ["/databases/1/access/reporting", "Grants"],
  ["/databases/1/access/reporting?tab=settings", "Account attributes"],
  ["/databases/4/access/worker", "Rule"],
  ["/databases/4/access/worker?tab=settings", "The user's rule"],
  ["/databases/5/access/api?host=app_main", "Roles"],
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
    // Eighteen pages are opened one after another: on a busy machine that is minutes.
    test.setTimeout(300_000)
    await page.setViewportSize(viewport)
    await mockOperate(page)
    for (const [path, region] of SURFACES) {
      await visit(page, path)
      await expect(
        page.getByRole("region", { name: region }).or(page.getByRole("form", { name: region })),
      ).toBeVisible()
      await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
      await keepsTheRules(page, `${path}${label}`)
    }
    // Every page of Settings, on each kind of engine that has it.
    for (const [path, id] of [
      ["/databases/1/settings", "address"],
      ["/databases/7/settings", "address"],
      ["/databases/1/settings/network", "reachability"],
      ["/databases/1/settings/parameters", "parameters"],
      ["/databases/4/settings/parameters", "parameters"],
      ["/databases/7/settings/parameters", "parameters"],
      ["/databases/4/settings/extensions", "extensions"],
      ["/databases/1/settings/danger", "danger"],
      ["/databases/4/settings/danger", "danger"],
    ]) {
      await visit(page, path)
      await expect(section(page, id)).toBeVisible()
      // A settings page is a form you read: no figures in tiles, anywhere on it.
      await expect(page.locator("[data-slot=stat-grid], [data-slot=stat-tile]")).toHaveCount(0)
      await keepsTheRules(page, `${path}${label}`)
    }
    // The catalogue of what could be enabled, open.
    await visit(page, "/databases/1/settings/extensions")
    await section(page, "extensions")
      .getByRole("button", { name: /^Available/ })
      .click()
    await expect(section(page, "extensions").locator("[data-extension=pgcrypto]")).toBeVisible()
    await keepsTheRules(page, `/databases/1/settings/extensions${label}`)
    // A search on, so the parameter rows themselves are drawn.
    await visit(page, "/databases/1/settings/parameters?q=e")
    await expect(section(page, "parameters").locator("[data-parameter]").first()).toBeVisible()
    await keepsTheRules(page, `/databases/1/settings/parameters?q=e${label}`)
  })

  test(`an account's page with work staged and the dialogs keep the rules${label}`, async ({
    page,
  }) => {
    test.setTimeout(120_000)
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
    await visit(page, "/databases/1/access/reporting")
    await expect(matrix(page).locator("[data-object=orders]")).toBeVisible()
    await matrix(page).getByRole("button", { name: "UPDATE on orders" }).click()
    await expect(pending(page)).toBeVisible()
    await keepsTheRules(page, `the account page with a grant staged${label}`)
    // On a phone a privilege is a finger's height.
    const chip = await matrix(page).getByRole("button", { name: "UPDATE on orders" }).boundingBox()
    expect(chip?.height ?? 0).toBeGreaterThanOrEqual(viewport.width < 640 ? 36 : 24)
    // The question the page asks before it is left over staged work.
    await back(page).click()
    await expect(leaving(page).getByRole("button", { name: "Discard and go on" })).toBeVisible()
    await keepsTheRules(page, `the account page asking${label}`, true)
    await leaving(page).getByRole("button", { name: "Keep editing" }).click()
    // The settings with an edit held, under the page's one save bar.
    await view(page, /^Settings/).click()
    await page.getByRole("switch", { name: "It can create databases" }).click()
    await expect(saveBar(page)).toBeVisible()
    await keepsTheRules(page, `the account settings with an edit${label}`)

    await visit(page, "/databases/1/access")
    await accounts(page).getByRole("button", { name: "New account" }).click()
    await expect(dialog(page).getByLabel("Name")).toBeVisible()
    await keepsTheRules(page, `the new account dialog${label}`, true)

    // A transfer in flight, in place.
    await visit(page, "/databases/1/backups")
    await expect(progress(page)).toBeVisible()
    await keepsTheRules(page, `a running transfer${label}`)

    await visit(page, "/databases/1/settings/danger")
    await section(page, "danger").getByRole("button", { name: "Delete…" }).click()
    await expect(dialog(page).getByRole("textbox")).toBeVisible()
    await keepsTheRules(page, `the delete confirmation${label}`, true)
  })
}

test("the restore dialog and the settings forms mid-edit keep the rules", async ({ page }) => {
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

  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 })
    // The address with its string read, an edit held and the bar up.
    await visit(page, "/databases/1/settings")
    await address(page).getByLabel("Host").fill("db.internal")
    await expect(address(page).locator("[data-slot=connection-preview]")).toBeVisible()
    await expect(saveBar(page)).toBeVisible()
    await keepsTheRules(page, `the address being edited at ${width}`)
    // A parameter being changed, with one out of range.
    await visit(page, "/databases/1/settings/parameters?q=e")
    await section(page, "parameters").getByLabel("Change work_mem").fill("1")
    await expect(
      section(page, "parameters").locator("[data-parameter=work_mem]").getByRole("alert"),
    ).toBeVisible()
    await keepsTheRules(page, `a parameter being edited at ${width}`)
  }
})

test("the settings pages, for the record @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to take the screenshots")
  test.setTimeout(300_000)
  const shots = process.env.JD_SHOTS
  const shot = async (name: string, width: number) => {
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    await risen(page)
    await tallShot(page, `${shots}/after-${name}-${width}.png`)
  }
  const calm = {
    ...SETTINGS,
    settings: SETTINGS.settings.map((one) => ({ ...one, pendingRestart: false })),
  }
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })

    // General, with nothing waiting: the address as fields under its picture.
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockOperate(page, {
      answers: {
        "GET 1/settings": calm,
        "POST /databases/test": { ok: false, error: "dial tcp 10.0.0.9:5432: connection refused" },
      },
    })
    await visit(page, "/databases/1/settings")
    await expect(address(page).getByLabel("Host")).toHaveValue("127.0.0.1")
    await shot("general-full", width)
    // An edit held: the string read, what is kept listed, the bar up.
    await address(page).getByLabel("Host").fill("db.internal")
    await page.getByRole("form", { name: "Connection labels" }).getByLabel("Notes").fill("Orders")
    await expect(address(page).locator("[data-slot=connection-preview]")).toBeVisible()
    await shot("general-unsaved", width)
    await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
    await expect(address(page)).toContainText("It refused the connection")
    await shot("general-refused", width)

    // The same page on a server with a value waiting for a restart, and for a role that reads.
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockOperate(page)
    await visit(page, "/databases/1/settings")
    await expect(strip(page)).toBeVisible()
    await shot("general-waiting", width)
    await visit(page, "/databases/7/settings")
    await expect(section(page, "address")).toBeVisible()
    await shot("general-file", width)
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockOperate(page, { capabilities: LIMITED })
    await visit(page, "/databases/1/settings")
    await expect(section(page, "connection")).toContainText("shop")
    await shot("general-readonly", width)

    // Parameters: the headings, the rows, an edit, and what ran.
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockOperate(page, {
      answers: {
        "PUT 1/settings": (_call: number, _url: URL, body: unknown) => {
          const sent = body as { name: string; value: string }
          return {
            name: sent.name,
            value: sent.value,
            statements: [`ALTER SYSTEM SET "${sent.name}" = '${sent.value}'`],
            persisted: true,
            restartRequired: sent.name === "wal_level",
          }
        },
      },
    })
    await visit(page, "/databases/1/settings/parameters")
    const parameters = section(page, "parameters")
    await expect(parameters.getByRole("button", { name: /Resource Usage/ })).toBeVisible()
    await shot("parameters-full", width)
    await visit(page, "/databases/1/settings/parameters?q=e")
    await expect(parameters.locator("[data-parameter]").first()).toBeVisible()
    await shot("parameters-rows", width)
    await parameters.getByLabel("Change bgwriter_delay").fill("250")
    await parameters.getByLabel("Change work_mem").fill("1")
    await parameters
      .locator("[data-parameter=wal_level]")
      .getByRole("radio", { name: "logical" })
      .click()
    await expect(saveBar(page)).toContainText("3 unsaved changes")
    await shot("parameters-unsaved", width)
    await parameters.getByLabel("Change work_mem").fill("8192")
    await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
    await expect(parameters.locator("[data-slot=parameter-change]")).toBeVisible()
    await expect(saveBar(page).getByRole("button", { name: "Save", exact: true })).toHaveCount(0)
    await shot("parameters-saved", width)

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockOperate(page, { capabilities: LIMITED })
    await visit(page, "/databases/1/settings/parameters?q=e")
    await expect(parameters.locator("[data-parameter]").first()).toBeVisible()
    await shot("parameters-readonly", width)
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockOperate(page, {
      answers: {
        "GET 1/settings": refusal("query_failed", "permission denied for view pg_settings", 502),
      },
    })
    await visit(page, "/databases/1/settings/parameters")
    await expect(parameters).toContainText("Could not read the parameters")
    await shot("parameters-failed", width)
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockOperate(page)
    await visit(page, "/databases/4/settings/parameters?q=m")
    await expect(parameters.locator("[data-parameter]").first()).toBeVisible()
    await shot("parameters-keyvalue", width)

    // The two pages with nothing of their own: under the same strip.
    await visit(page, "/databases/1/settings/danger")
    await expect(section(page, "danger")).toBeVisible()
    await shot("danger-full", width)
  }
})
