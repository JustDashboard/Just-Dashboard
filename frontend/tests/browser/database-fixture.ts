import type { Page, Route } from "@playwright/test"

/**
 * The Databases section's API, mocked far enough to draw its shell: the
 * connection list, the driver catalogue, the fleet, one connection's summary,
 * and the reads the strip's controls make. Everything else under
 * `/databases/…` answers an empty list, which is what a page that has not
 * been built yet asks for and gets.
 *
 * Five connections stand for the engines the shell treats differently: a
 * PostgreSQL server, a MariaDB server behind the `mysql` driver (only its
 * summary says so), a Redis server, a MongoDB server and a SQLite file. A
 * spec changes what the server says through `options` rather than by routing
 * over this, so the order in which Playwright consults routes never decides
 * a test.
 */

const now = "2026-10-01T09:00:00Z"

export type MockConnection = {
  id: number
  name: string
  driver: string
  host: string
  port: string
  user: string
  database: string
  createdAt: string
  environment?: string
  readOnly?: boolean
  notes?: string
  broken?: true
  brokenReason?: string
}

function connection(
  id: number,
  name: string,
  driver: string,
  port: string,
  database: string,
  user = "app",
): MockConnection {
  return {
    id,
    name,
    driver,
    host: port ? "127.0.0.1" : "localhost",
    port,
    user,
    database,
    createdAt: "2026-09-01T00:00:00Z",
    environment: "",
    readOnly: false,
    notes: "",
  }
}

export const SHOP = connection(1, "shop", "postgres", "5432", "shop_main")
export const BLOG = connection(2, "blog", "mysql", "3306", "blog_main")
export const CACHE = connection(4, "cache", "redis", "6379", "0", "")
export const APP = connection(5, "app", "mongodb", "27017", "app_main", "")
export const NOTES = connection(7, "notes", "sqlite", "", "/srv/notes/notes.db", "")

export const CONNECTIONS = [APP, BLOG, CACHE, NOTES, SHOP]

/** What the server behind each connection says it is. */
const ANSWERS: Record<number, { flavor: string; flavorLabel: string; versionNumber: string }> = {
  1: { flavor: "postgres", flavorLabel: "PostgreSQL", versionNumber: "16.4" },
  2: { flavor: "mariadb", flavorLabel: "MariaDB", versionNumber: "11.8.9" },
  4: { flavor: "redis", flavorLabel: "Redis", versionNumber: "7.4.1" },
  5: { flavor: "mongodb", flavorLabel: "MongoDB", versionNumber: "8.0.4" },
  7: { flavor: "sqlite", flavorLabel: "SQLite", versionNumber: "3.46.0" },
}

const DRIVERS = [
  ["postgres", "PostgreSQL", "sql", true, true],
  ["mysql", "MySQL / MariaDB", "sql", true, true],
  ["sqlite", "SQLite", "sql", true, true],
  ["sqlserver", "SQL Server", "sql", true, true],
  ["clickhouse", "ClickHouse", "sql", true, false],
  ["oracle", "Oracle", "sql", true, true],
  ["mongodb", "MongoDB", "document", false, false],
  ["redis", "Redis", "keyvalue", false, false],
].map(([id, label, kind, sql, ddl]) => ({ id, label, kind, placeholder: "", sql, ddl }))

function session(admin: boolean) {
  return {
    authenticated: true,
    needsTotp: false,
    needsEnrollment: false,
    require2fa: false,
    capabilities: admin
      ? ["read", "service.control", "file.write", "terminal", "destructive", "system.admin"]
      : ["read"],
    user: {
      id: 1,
      username: "operator",
      displayName: "Operator",
      avatarVersion: 0,
      role: admin ? "admin" : "viewer",
      totpEnabled: true,
      disabled: false,
      mustChangePassword: false,
      lastLoginAt: now,
      createdAt: now,
    },
  }
}

/** `GET /databases/{id}` for one connection, as a backend with the route answers it. */
export function summaryOf(conn: MockConnection, over: Record<string, unknown> = {}) {
  const file = conn.driver === "sqlite"
  return {
    ...conn,
    ...ANSWERS[conn.id],
    version: `${ANSWERS[conn.id]?.flavorLabel} ${ANSWERS[conn.id]?.versionNumber}`,
    state: conn.broken ? "broken" : "running",
    ok: !conn.broken,
    error: conn.brokenReason,
    latencyMs: 3,
    source: file ? "file" : "docker",
    power: file
      ? { via: "", start: false, stop: false, restart: false, reason: "A file has no server." }
      : { via: "docker", start: false, stop: true, restart: true },
    exposure: "local",
    managed: !file,
    consumers: 0,
    capabilities: {},
    checkedAt: now,
    ...over,
  }
}

function fleetEntry(conn: MockConnection, over: Record<string, unknown> = {}) {
  const words: Record<string, string> = { mongodb: "collections", redis: "keys" }
  return {
    ...conn,
    ...ANSWERS[conn.id],
    ok: !conn.broken,
    state: conn.broken ? "broken" : "running",
    error: conn.brokenReason,
    latencyMs: 3,
    bytes: 4_200_000,
    sizesKnown: true,
    objects: 12,
    objectWord: words[conn.driver] ?? "tables",
    sessions: 2,
    source: conn.driver === "sqlite" ? "file" : "docker",
    exposure: "local",
    consumers: 0,
    lastBackup: now,
    ...over,
  }
}

export type DatabaseMock = {
  /** Sign in without `system.admin`. */
  viewer?: boolean
  /** Fields laid over a connection's saved row, by id. */
  rows?: Record<number, Partial<MockConnection>>
  /**
   * Fields laid over a connection's summary, by id. `null` is a backend from
   * before the route: it answers 405 and the shell falls back to a ping.
   */
  summaries?: Record<number, Record<string, unknown> | null>
  /** How long each summary takes, in milliseconds. */
  summaryDelay?: number
  /** Fields laid over a connection's fleet entry, by id. */
  fleet?: Record<number, Record<string, unknown>>
  /** How long the audited connection-string read takes, in milliseconds. */
  urlDelay?: number
  /** What `GET /databases/{id}/logs/sources` answers. */
  logSources?: unknown
  /** What `GET /databases/{id}/querylog` answers. */
  queryLog?: unknown
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

/**
 * Routes the API for one page. Hands back every path under `/databases` the
 * page asked for, in order, with its method — which is how a spec says "no
 * SQL request was sent at a key–value store".
 */
export async function mockDatabases(page: Page, options: DatabaseMock = {}) {
  const asked: string[] = []
  const list = () => CONNECTIONS.map((conn) => ({ ...conn, ...options.rows?.[conn.id] }))

  // A live tail that opens and has nothing to say.
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const source = new URL(socket.url()).searchParams.get("source")
    socket.send(JSON.stringify({ type: "meta", data: { label: source }, ts: Date.now() }))
  })
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (path.startsWith("/databases")) asked.push(`${method} ${path}`)

    if (path === "/auth/session") return json(route, session(!options.viewer))
    if (path === "/updates/self") return json(route, { current: "0.7.1", latest: "0.7.1" })
    // A server log read on a database's page: described, and with no lines.
    if (path === "/logs/source" || path === "/logs/retention") return json(route, {})
    if (path === "/logs/search") {
      return json(route, {
        lines: [],
        scanned: 0,
        matched: 0,
        truncated: false,
        complete: true,
        files: [],
        histogram: [],
        tookMillis: 1,
      })
    }
    if (path === "/databases/") return json(route, list())
    if (path === "/databases/drivers") return json(route, DRIVERS)
    if (path === "/databases/fleet") {
      return json(route, {
        connections: list().map((conn) => fleetEntry(conn, options.fleet?.[conn.id])),
        unreachable: [],
        needsCredentials: [],
        checkedAt: now,
      })
    }

    const one = /^\/databases\/(\d+)(\/.*)?$/.exec(path)
    const conn = one && list().find((c) => c.id === Number(one[1]))
    if (!one || !conn) {
      return path.startsWith("/databases/") && one
        ? json(route, { error: { code: "not_found", message: "no such connection" } }, 404)
        : json(route, [])
    }
    const rest = one[2] ?? ""
    if (rest === "") {
      const over = options.summaries?.[conn.id]
      if (over === null) {
        return json(route, { error: { code: "method_not_allowed", message: "" } }, 405)
      }
      if (options.summaryDelay) await wait(options.summaryDelay)
      return json(route, summaryOf(conn, over))
    }
    if (rest === "/ping") return json(route, { ok: true })
    if (rest === "/access") {
      return json(route, {
        detected: true,
        container: `${conn.name}-db`,
        managed: true,
        exposure: "local",
        port: Number(conn.port),
        publicAddresses: ["203.0.113.7"],
        firewall: { active: true, open: false, editable: true },
      })
    }
    if (rest === "/url") {
      if (options.urlDelay) await wait(options.urlDelay)
      return json(route, {
        id: conn.id,
        name: conn.name,
        driver: conn.driver,
        reference: "",
        url: `postgres://${conn.user}:s3cret@${conn.host}:${conn.port}/${conn.database}?sslmode=disable`,
      })
    }
    if (rest === "/logs/sources") {
      return json(
        route,
        options.logSources ?? {
          sources: [],
          reason: "This server runs on another machine, so its log is not here.",
        },
      )
    }
    if (rest === "/querylog") {
      return json(
        route,
        options.queryLog ?? { supported: true, source: "log", entries: [], truncated: false },
      )
    }
    if (rest === "/graph") {
      return json(route, { schema: "public", tables: [], edges: [], truncated: false })
    }
    if (rest === "/diagram") return json(route, { layout: null })
    return json(route, [])
  })

  return { asked }
}

/** A database's pages, by the kind of engine, for a walk over all of them. */
export const DATABASE_SURFACES = [
  "/databases",
  "/databases/map",
  "/databases/new",
  "/databases/1",
  "/databases/1/data",
  "/databases/1/query",
  "/databases/1/search",
  "/databases/1/schema",
  "/databases/1/diagram",
  "/databases/1/generate",
  "/databases/1/performance",
  "/databases/1/advisor",
  "/databases/1/logs",
  "/databases/1/access",
  "/databases/1/backups",
  "/databases/1/settings",
  "/databases/4",
  "/databases/4/data",
  "/databases/4/query",
  "/databases/4/schema",
  "/databases/5/data",
  "/databases/5/schema",
  "/databases/7/access",
  "/databases/1/no-such-page",
  "/databases/999",
] as const
