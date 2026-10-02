import type { Page, Route } from "@playwright/test"
import {
  APP,
  BLOG,
  CACHE,
  CONNECTIONS,
  NOTES,
  SHOP,
  mockDatabases,
  type DatabaseMock,
  type MockConnection,
} from "./database-fixture"

/**
 * The section-wide pages' API, mocked over the shell's fixture: the fleet
 * with where each server runs, what discovery found on the machine, the dump
 * directories, the map, and the routes that start, connect, stop and forget.
 *
 * The inventory here is a machine with Docker on it — a running container
 * that states its credentials, a stopped one, a compose service nobody
 * brought up, a native server that waits for a password, database files, an
 * engine the dashboard cannot open — because those are the states the pages
 * were built for and the development backend, which has no Docker, cannot
 * show.
 *
 * A spec changes what the server says in two ways: `options` for what is true
 * from the start, and `server.answer("POST /databases/1/power", …)` for one
 * route's reply. `server.sent` is every mutation the page made, with its body.
 */

const hoursAgo = (hours: number) => new Date(Date.now() - hours * 3_600_000).toISOString()

type FleetFields = Record<string, unknown>

const FLEET: Record<number, FleetFields> = {
  [SHOP.id]: {
    flavor: "postgres",
    versionNumber: "16.4",
    source: "docker",
    container: "shop-db",
    bytes: 24_509_463,
    objects: 13,
    objectWord: "tables",
    sessions: 4,
    consumers: 2,
  },
  [BLOG.id]: {
    flavor: "mariadb",
    versionNumber: "11.8.9",
    source: "docker",
    container: "blog-db-1",
    composeProject: "blog",
    bytes: 1_212_416,
    objects: 63,
    objectWord: "tables",
    sessions: 1,
  },
  [CACHE.id]: {
    flavor: "redis",
    versionNumber: "7.4.1",
    source: "docker",
    container: "cache",
    bytes: 8_441_036,
    objects: 28_600,
    objectWord: "keys",
    sessions: 3,
  },
  [APP.id]: {
    flavor: "mongodb",
    versionNumber: "8.0.4",
    source: "remote",
    host: "db.example.com",
    exposure: "remote",
    bytes: 782_336,
    objects: 4,
    objectWord: "collections",
    sessions: 6,
  },
  [NOTES.id]: {
    flavor: "sqlite",
    versionNumber: "3.46.0",
    source: "file",
    bytes: 0,
    sizesKnown: false,
    objects: 4,
    objectWord: "tables",
    sessions: 0,
  },
}

function fleetEntry(conn: MockConnection, over: FleetFields = {}) {
  return {
    ...conn,
    ok: !conn.broken,
    state: conn.broken ? "broken" : "running",
    error: conn.brokenReason,
    latencyMs: 3,
    bytes: 1_000_000,
    sizesKnown: true,
    objects: 1,
    objectWord: "tables",
    sessions: 0,
    source: "docker",
    exposure: "local",
    consumers: 0,
    flavor: conn.driver,
    ...FLEET[conn.id],
    ...over,
  }
}

/** One thing discovery found; the fields a spec reads back are named, the rest are the contract's. */
export type MockInstance = {
  key: string
  name: string
  driver: string
  source: string
  connections: number[]
  ignored?: boolean
  [field: string]: unknown
}

function instance(over: Partial<MockInstance> & { key: string; name: string }): MockInstance {
  return {
    kind: "server",
    engine: "postgres",
    driver: "postgres",
    flavor: "postgres",
    label: "PostgreSQL",
    source: "docker",
    state: "running",
    endpoints: [],
    credentials: "env",
    confidence: "image",
    evidence: [],
    connectable: true,
    connections: [],
    ...over,
  }
}

const container = (
  id: string,
  name: string,
  image: string,
  more: Record<string, unknown> = {},
) => ({
  id,
  name,
  image,
  dataVolumes: [],
  ...more,
})

/** One of each thing discovery can find on a machine that runs Docker. */
export function dockerInventory() {
  return {
    detail: "full",
    checkedAt: new Date().toISOString(),
    ignored: ["docker:legacy-redis", "docker:long-gone"],
    scans: [
      { source: "docker", ok: true, count: 9, durationMs: 40, checkedAt: hoursAgo(0) },
      { source: "compose", ok: true, count: 2, durationMs: 12, checkedAt: hoursAgo(0) },
      { source: "listeners", ok: true, count: 31, durationMs: 9, checkedAt: hoursAgo(0) },
      { source: "units", ok: true, count: 180, durationMs: 700, checkedAt: hoursAgo(0) },
      {
        source: "files",
        ok: true,
        reason: "2 directories are outside the file roots and were not scanned",
        count: 4,
        durationMs: 900,
        checkedAt: hoursAgo(0),
      },
    ],
    instances: [
      instance({
        key: "docker:shop-db",
        name: "shop-db",
        version: "16.4",
        container: container("c-shop", "shop-db", "postgres:16-alpine"),
        endpoints: [
          { kind: "tcp", host: "127.0.0.1", port: 5432, scope: "loopback", primary: true },
        ],
        connections: [SHOP.id],
      }),
      instance({
        key: "docker:orders-db",
        name: "orders-db",
        version: "17.2",
        container: container("c-orders", "orders-db", "postgres:17-alpine"),
        endpoints: [
          { kind: "tcp", host: "127.0.0.1", port: 5440, scope: "loopback", primary: true },
        ],
        user: "orders",
        database: "orders",
      }),
      instance({
        key: "docker:sessions",
        name: "sessions",
        engine: "redis",
        driver: "redis",
        flavor: "valkey",
        label: "Valkey",
        version: "8.1",
        credentials: "args",
        container: container("c-sessions", "sessions", "valkey/valkey:8-alpine"),
        endpoints: [
          { kind: "tcp", host: "127.0.0.1", port: 6380, scope: "loopback", primary: true },
        ],
      }),
      instance({
        key: "host:postgresql@17-main.service",
        name: "postgresql@17-main",
        version: "17",
        source: "host",
        credentials: "peer",
        confidence: "socket",
        host: { unit: "postgresql@17-main.service", unitState: "active", user: "postgres" },
        endpoints: [
          { kind: "tcp", host: "127.0.0.1", port: 5438, scope: "loopback", primary: true },
          { kind: "unix", path: "/var/run/postgresql/.s.PGSQL.5438" },
        ],
        user: "postgres",
        database: "postgres",
      }),
      instance({
        key: "docker:old-mysql",
        name: "old-mysql",
        engine: "mysql",
        driver: "mysql",
        flavor: "mysql",
        label: "MySQL",
        state: "exited",
        connectable: false,
        reason: "the container is exited — start it to connect",
        container: container("c-old", "old-mysql", "mysql:8.0", {
          status: "Exited (0) 3 days ago",
        }),
      }),
      instance({
        key: "compose:shop/search-db",
        name: "search-db",
        source: "compose",
        state: "declared",
        connectable: false,
        reason: "no container exists for it — bring the stack up to connect",
        container: {
          name: "search-db",
          image: "postgres:16",
          composeProject: "shop",
          composeService: "search-db",
          dataVolumes: [],
        },
      }),
      instance({
        key: "host:redis-server.service",
        name: "redis-server",
        engine: "redis",
        driver: "redis",
        flavor: "redis",
        label: "Redis",
        source: "host",
        state: "inactive",
        credentials: "unknown",
        confidence: "unit",
        connectable: false,
        reason: "redis-server.service is not running — start it to connect",
        host: { unit: "redis-server.service", unitState: "inactive", enabled: false },
      }),
      instance({
        key: "docker:memcached",
        name: "memcached",
        engine: "memcached",
        driver: "",
        flavor: undefined,
        label: "Memcached",
        credentials: "unknown",
        connectable: false,
        reason: "this dashboard has no driver for Memcached yet",
        container: container("c-mc", "memcached", "memcached:1.6"),
        endpoints: [
          { kind: "tcp", host: "127.0.0.1", port: 11211, scope: "loopback", primary: true },
        ],
      }),
      instance({
        key: "docker:legacy-redis",
        name: "legacy-redis",
        engine: "redis",
        driver: "redis",
        flavor: "redis",
        label: "Redis",
        credentials: "open",
        ignored: true,
        container: container("c-legacy", "legacy-redis", "redis:6"),
        endpoints: [
          { kind: "tcp", host: "127.0.0.1", port: 6390, scope: "loopback", primary: true },
        ],
      }),
      instance({
        key: "file:/srv/app/data.db",
        kind: "file",
        name: "data.db",
        engine: "sqlite",
        driver: "sqlite",
        flavor: "sqlite",
        label: "SQLite",
        source: "file",
        state: "file",
        credentials: "open",
        confidence: "magic",
        endpoints: [{ kind: "file", path: "/srv/app/data.db", primary: true }],
        file: {
          path: "/srv/app/data.db",
          size: 335_872,
          modified: hoursAgo(2),
          holder: "application",
        },
      }),
      instance({
        key: "file:/srv/notes/notes.db",
        kind: "file",
        name: "notes.db",
        engine: "sqlite",
        driver: "sqlite",
        flavor: "sqlite",
        label: "SQLite",
        source: "file",
        state: "file",
        credentials: "open",
        confidence: "magic",
        endpoints: [{ kind: "file", path: "/srv/notes/notes.db", primary: true }],
        file: {
          path: "/srv/notes/notes.db",
          size: 319_488,
          modified: hoursAgo(5),
          holder: "application",
        },
        connections: [NOTES.id],
      }),
      instance({
        key: "file:/srv/reports/sales.duckdb",
        kind: "file",
        name: "sales.duckdb",
        engine: "duckdb",
        driver: "",
        flavor: undefined,
        label: "DuckDB",
        source: "file",
        state: "file",
        credentials: "unknown",
        confidence: "magic",
        connectable: false,
        reason: "this dashboard has no driver for DuckDB yet",
        endpoints: [{ kind: "file", path: "/srv/reports/sales.duckdb", primary: true }],
        file: {
          path: "/srv/reports/sales.duckdb",
          size: 9_437_184,
          modified: hoursAgo(30),
          holder: "application",
        },
      }),
      instance({
        key: "file:/var/lib/just-dashboard/vpsd.db",
        kind: "file",
        name: "vpsd.db",
        engine: "sqlite",
        driver: "sqlite",
        flavor: "sqlite",
        label: "SQLite",
        source: "file",
        state: "file",
        credentials: "open",
        confidence: "magic",
        connectable: false,
        reason: "this is the dashboard's own store — it is listed, and never connected",
        self: true,
        endpoints: [{ kind: "file", path: "/var/lib/just-dashboard/vpsd.db", primary: true }],
        file: {
          path: "/var/lib/just-dashboard/vpsd.db",
          size: 708_608,
          modified: hoursAgo(0),
          wal: true,
          holder: "self",
        },
      }),
    ],
  }
}

/** A machine whose Docker did not answer: the host and the files are still listed. */
export function dockerlessInventory() {
  const base = dockerInventory()
  const reason = "Docker did not answer: Cannot connect to the Docker daemon"
  return {
    ...base,
    scans: base.scans.map((scan) =>
      scan.source === "docker" || scan.source === "compose"
        ? { ...scan, ok: false, reason, count: 0 }
        : scan,
    ),
    instances: base.instances.filter((one) => one.source !== "docker" && one.source !== "compose"),
  }
}

export const TOPOLOGY = {
  checkedAt: new Date().toISOString(),
  nodes: [
    {
      id: "db:1",
      kind: "database",
      name: "shop",
      product: "postgres",
      detail: "shop_main",
      href: "/databases/1",
      connId: 1,
    },
    {
      id: "db:2",
      kind: "database",
      name: "blog",
      product: "mysql",
      detail: "blog_main",
      href: "/databases/2",
      connId: 2,
    },
    {
      id: "db:4",
      kind: "database",
      name: "cache",
      product: "redis",
      detail: "0",
      href: "/databases/4",
      connId: 4,
    },
    {
      id: "db:5",
      kind: "database",
      name: "app",
      product: "mongodb",
      detail: "app_main",
      href: "/databases/5",
      connId: 5,
    },
    {
      id: "db:7",
      kind: "database",
      name: "notes",
      product: "sqlite",
      detail: "/srv/notes/notes.db",
      href: "/databases/7",
      connId: 7,
    },
    {
      id: "deploy:7",
      kind: "deployment",
      name: "storefront",
      product: "nodejs",
      detail: "production",
      status: "running",
      href: "/deploy/7",
    },
    {
      id: "container:worker",
      kind: "container",
      name: "worker",
      product: "python",
      detail: "shop/worker",
      status: "running",
    },
    { id: "host", kind: "host", name: "This server", detail: "processes on the machine" },
    { id: "remote:10.0.0.9", kind: "remote", name: "10.0.0.9", detail: "another machine" },
  ],
  edges: [
    { from: "db:1", to: "deploy:7", via: ["binding", "session"], sessions: 3, status: "connected" },
    { from: "db:4", to: "deploy:7", via: ["env"], sessions: 2, status: "connected" },
    {
      from: "db:1",
      to: "container:worker",
      via: ["stack", "network"],
      sessions: 0,
      status: "observed",
    },
    { from: "db:2", to: "container:worker", via: ["binding"], sessions: 0, status: "broken" },
    { from: "db:4", to: "host", via: ["session"], sessions: 1, status: "observed" },
    { from: "db:5", to: "remote:10.0.0.9", via: ["session"], sessions: 6, status: "observed" },
  ],
}

const versions = (...list: [string, string][]) =>
  list.map(([version, image]) => ({ version, image }))

export const TEMPLATES = [
  {
    engine: "postgres",
    label: "PostgreSQL",
    image: "postgres:16-alpine",
    driver: "postgres",
    flavor: "postgres",
    versions: versions(
      ["17", "postgres:17-alpine"],
      ["16", "postgres:16-alpine"],
      ["15", "postgres:15-alpine"],
    ),
    defaultVersion: "16",
    port: 5432,
    defaultUser: "jd",
    database: true,
  },
  {
    engine: "timescaledb",
    label: "TimescaleDB",
    image: "timescale/timescaledb:latest-pg16",
    driver: "postgres",
    flavor: "timescaledb",
    versions: versions(["16", "timescale/timescaledb:latest-pg16"]),
    defaultVersion: "16",
    port: 5432,
    defaultUser: "jd",
    database: true,
  },
  {
    engine: "mariadb",
    label: "MariaDB",
    image: "mariadb:11",
    driver: "mysql",
    flavor: "mariadb",
    versions: versions(["12", "mariadb:12"], ["11", "mariadb:11"]),
    defaultVersion: "11",
    port: 3306,
    defaultUser: "jd",
    database: true,
  },
  {
    engine: "mongodb",
    label: "MongoDB",
    image: "mongo:7",
    driver: "mongodb",
    flavor: "mongodb",
    versions: versions(["8", "mongo:8"], ["7", "mongo:7"]),
    defaultVersion: "7",
    port: 27017,
    defaultUser: "jd",
    database: true,
  },
  {
    engine: "valkey",
    label: "Valkey",
    image: "valkey/valkey:8-alpine",
    driver: "redis",
    flavor: "valkey",
    versions: versions(["9", "valkey/valkey:9-alpine"], ["8", "valkey/valkey:8-alpine"]),
    defaultVersion: "8",
    port: 6379,
    defaultUser: "",
    database: false,
  },
  {
    engine: "clickhouse",
    label: "ClickHouse",
    image: "clickhouse/clickhouse-server:25.8",
    driver: "clickhouse",
    flavor: "clickhouse",
    versions: versions(["25.8", "clickhouse/clickhouse-server:25.8"]),
    defaultVersion: "25.8",
    port: 9000,
    defaultUser: "jd",
    database: true,
  },
]

export type FleetMock = DatabaseMock & {
  /** What discovery found. `dockerInventory()` unless given. */
  inventory?: ReturnType<typeof dockerInventory>
  /** No saved connection at all. */
  empty?: boolean
  /** The newest dump of a connection by id, as hours ago; `null` for none. Every one has a recent dump otherwise. */
  dumps?: Record<number, number | null>
  topology?: typeof TOPOLOGY
  /**
   * The two lists the fleet itself carries of servers found running and not
   * connected: containers that state no usable password, and servers
   * installed on the machine. Both empty unless given.
   */
  reported?: {
    unreachable?: { container: string; driver: string; reason: string }[]
    needsCredentials?: {
      driver: string
      host: string
      port: number
      name: string
      user?: string
      database?: string
    }[]
  }
}

type Answer = { status?: number; body?: unknown } | void
type Handler = (request: { body: Record<string, unknown>; url: URL }) => Answer | Promise<Answer>

async function json(route: Route, body: unknown, status = 200) {
  if (status === 204) return route.fulfill({ status })
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/** The house error envelope. `retryable` is what makes an error state offer to ask again. */
export function refusal(status: number, code: string, message: string, retryable = false) {
  return { status, body: { error: { code, message, retryable } } }
}

export async function mockFleet(page: Page, options: FleetMock = {}) {
  await mockDatabases(page, options)
  const asked: string[] = []
  const sent: { call: string; body: Record<string, unknown> }[] = []
  const handlers = new Map<string, Handler>()
  const inventory = options.inventory ?? dockerInventory()
  const added: MockConnection[] = []
  const forgotten = new Set<number>()
  let nextId = 20

  const list = () =>
    (options.empty ? [] : CONNECTIONS)
      .map((conn) => ({ ...conn, ...options.rows?.[conn.id] }))
      .concat(added)
      .filter((conn) => !forgotten.has(conn.id))
      .sort((a, b) => a.name.localeCompare(b.name))
  const dumpOf = (id: number) => {
    const hours = options.dumps && id in options.dumps ? options.dumps[id] : 3
    return hours === null || hours === undefined ? undefined : hoursAgo(hours)
  }
  const connect = (name: string, driver: string, more: Partial<MockConnection> = {}) => {
    const conn: MockConnection = {
      id: nextId++,
      name,
      driver,
      host: "127.0.0.1",
      port: "5440",
      user: "app",
      database: name,
      createdAt: new Date().toISOString(),
      environment: "",
      readOnly: false,
      notes: "",
      origin: "",
      ...more,
    }
    added.push(conn)
    return conn
  }

  const builtin: Handler = ({ body, url }) => {
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const call = `${url.searchParams.get("__method")} ${path}`
    switch (call) {
      case "GET /databases/":
        return { body: list() }
      case "GET /databases/fleet":
        return {
          body: {
            connections: list().map((conn) =>
              fleetEntry(conn, { lastBackup: dumpOf(conn.id), ...options.fleet?.[conn.id] }),
            ),
            unreachable: options.reported?.unreachable ?? [],
            needsCredentials: options.reported?.needsCredentials ?? [],
            checkedAt: new Date().toISOString(),
          },
        }
      case "GET /databases/backups/summary":
        return {
          body: {
            connections: list().map((conn) => {
              const takenAt = dumpOf(conn.id)
              return {
                id: conn.id,
                name: conn.name,
                count: takenAt ? 1 : 0,
                totalSize: takenAt ? 1_092_500 : 0,
                newest: takenAt
                  ? {
                      file: `${conn.name}.dump`,
                      size: 1_092_500,
                      takenAt,
                      format: "pg_dump archive",
                    }
                  : null,
              }
            }),
          },
        }
      case "GET /databases/topology":
        return { body: options.topology ?? TOPOLOGY }
      case "GET /databases/inventory":
      case "POST /databases/inventory/scan":
        return { body: inventory }
      case "POST /databases/inventory/ignore": {
        const key = String(body.key)
        const found = inventory.instances.find((one) => one.key === key)
        if (found) found.ignored = Boolean(body.ignored)
        inventory.ignored = body.ignored
          ? [...new Set([...inventory.ignored, key])]
          : inventory.ignored.filter((one) => one !== key)
        return { body: { key, ignored: Boolean(body.ignored) } }
      }
      case "POST /databases/inventory/connect": {
        const found = inventory.instances.find((one) => one.key === body.key)
        if (!found) return refusal(404, "not_found", "nothing answers to that key any more")
        const conn = connect(String(body.name || found.name), found.driver, {
          origin: String(body.key),
        })
        found.connections = [conn.id]
        return { status: 201, body: conn }
      }
      case "POST /databases/sync":
        return {
          body: {
            added: ["orders-db", "sessions"],
            already: [],
            unreachable: [],
            needsCredentials: [],
            ignored: ["docker:legacy-redis"],
            scans: [],
          },
        }
      case "GET /databases/provision/options":
        return { body: TEMPLATES }
      case "POST /databases/provision":
        return {
          status: 202,
          body: {
            container: String(body.name || `jd-${body.engine}`),
            engine: body.engine,
            driver: "postgres",
            flavor: "postgres",
            version: body.version,
            image: "postgres:16-alpine",
            host: "127.0.0.1",
            port: 5441,
            user: "jd",
            database: "app",
            exposure: body.exposure ?? "local",
            firewall: "none",
          },
        }
      case "POST /databases/adopt":
        return { status: 201, body: connect(String(body.container), "postgres") }
      case "POST /databases/test":
        return {
          body: {
            ok: true,
            version: "PostgreSQL 16.4",
            versionNumber: "16.4",
            flavor: "postgres",
            flavorLabel: "PostgreSQL",
          },
        }
      case "POST /databases/":
        return {
          status: 201,
          body: connect(String(body.name), String(body.driver), {
            environment: String(body.environment ?? ""),
            readOnly: Boolean(body.readOnly),
          }),
        }
      case "POST /databases/host":
        return {
          status: 201,
          body: connect(String(body.name || "postgres on this host"), String(body.driver)),
        }
      case "POST /databases/host/grant":
        return { status: 201, body: connect("postgres 17 main on this host", "postgres") }
      case "GET /files/places":
        return { body: { home: "/srv", roots: ["/srv", "/home"], places: [], bookmarks: [] } }
      case "GET /jobs/job-1":
        return {
          body: {
            job: { id: "job-1", kind: "database.backup", status: "succeeded", lines: 3 },
            lines: [],
          },
        }
    }

    const one = /^(\w+) \/databases\/(\d+)(\/.*)?$/.exec(call)
    if (one) {
      const [, method, id, rest = ""] = one
      const conn = list().find((entry) => entry.id === Number(id))
      if (!conn) return
      if (method === "POST" && rest === "/power") {
        const target = String(FLEET[conn.id]?.container ?? conn.name)
        return { body: { action: body.action, via: "docker", target, state: "running" } }
      }
      if (method === "POST" && rest === "/backup") {
        return {
          status: 202,
          body: {
            id: "job-1",
            kind: "database.backup",
            title: "Dump",
            status: "running",
            lines: 0,
          },
        }
      }
      if (method === "PUT" && rest === "/access") {
        return { body: { access: { exposure: "local", managed: true }, firewall: "closed" } }
      }
      if (method === "DELETE" && rest === "") {
        forgotten.add(conn.id)
        return { status: 204 }
      }
      if (method === "GET" && rest === "/ping") return { body: { ok: true } }
    }
    if (/^POST \/(docker\/containers|systemd)\/[^/]+\/start$/.test(call)) return { status: 204 }
  }

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const call = `${method} ${path}`
    let body: Record<string, unknown> = {}
    try {
      body = (request.postDataJSON() as Record<string, unknown> | null) ?? {}
    } catch {
      // Not a JSON body: nothing these pages send.
    }
    if (path.startsWith("/databases")) asked.push(call)
    if (method !== "GET") sent.push({ call, body })
    url.searchParams.set("__method", method)
    const answer = (await handlers.get(call)?.({ body, url })) ?? (await builtin({ body, url }))
    if (!answer) return route.fallback()
    return json(route, answer.body, answer.status ?? 200)
  })

  return {
    /** Every request under `/databases`, in order, with its method. */
    asked,
    /** Every mutation the page sent, in order, with its JSON body. */
    sent,
    /** The bodies sent to one route. */
    bodies: (call: string) => sent.filter((one) => one.call === call).map((one) => one.body),
    /** Answer one route (`"POST /databases/1/power"`) another way. Returning nothing leaves it as it was. */
    answer: (call: string, handler: Handler) => handlers.set(call, handler),
    inventory,
  }
}
