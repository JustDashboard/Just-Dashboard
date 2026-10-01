import { expect, test, type Page, type Route } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * Redis — and Valkey, KeyDB, Dragonfly: the key browser, the console and the
 * performance readings, against a server mocked in the browser.
 *
 * The mock is a small keyspace that answers the Redis routes the way their
 * contract writes them (cursors as opaque strings, members by presence, a
 * verdict before a command), and records every request. The tests are the
 * claims the pages make that a wrong page would silently break: which
 * database is on screen, that a save keeps the expiry, that a typo is never
 * "no expiry", that a cursor goes back with every digit, that nothing is
 * removed before it is counted and confirmed, and that a control the role or
 * the server cannot honour is not drawn.
 */

const KEYS = "/databases/4/data"
const CONSOLE = "/databases/4/query"
const PERFORMANCE = "/databases/4/performance"

/** An unsigned 64-bit cursor: every digit past 2^53 is lost in a JavaScript number. */
const BIG_CURSOR = "18446744073709551615"

type Bytes = string | { base64: string }

type FakeKey = {
  type: string
  ttl?: number
  /** A string's value. */
  value?: Bytes
  /** Hash fields, list elements, set members. */
  rows?: { field?: Bytes; value?: Bytes; score?: number | string; index?: number }[]
  entries?: { id: string; fields: [string, string][] }[]
  /** A JSON document, as text. */
  json?: string
  /** How many it really holds, where that is more than the rows given. */
  length?: number
}

type Call = { method: string; path: string; query: URLSearchParams; body: unknown }

type RedisMock = DatabaseMock & {
  /** `service.control` without `destructive`: a role that may write and not remove. */
  limited?: boolean
  keys?: Record<string, FakeKey>
  /** The database the connection string names. */
  db?: number
  features?: Record<string, boolean>
  server?: Record<string, unknown>
  /** Keys handed over per page of a scan. */
  pageSize?: number
  /** The walk of the tree stops short on its first call. */
  treeInTwo?: boolean
  /** Paths that answer 502 until `heal()` is called. */
  failing?: RegExp
  /** What `classify` says of a command, by its first word. */
  verdicts?: Record<string, Record<string, unknown>>
  slowlog?: unknown[]
  clients?: unknown[]
  documented?: boolean
}

const DEFAULT_KEYS: Record<string, FakeKey> = {
  "session:1": { type: "string", value: '{"user":1}', ttl: 3600 },
  "session:2": { type: "string", value: "plain", ttl: 3600 },
  "user:1:profile": {
    type: "hash",
    rows: [
      { field: "name", value: "Ada" },
      { field: "plan", value: "pro" },
      { field: "", value: "empty field" },
    ],
    length: 1200,
  },
  "user:1:roles": { type: "set", rows: [{ value: "admin" }, { value: "member" }] },
  "user:2:profile": { type: "hash", rows: [{ field: "name", value: "Grace" }] },
  leaderboard: {
    type: "zset",
    rows: [
      { value: "ann", score: 10 },
      { value: "bob", score: 10 },
      { value: "cyd", score: "inf" },
    ],
  },
  queue: { type: "list", rows: [{ value: "first" }, { value: "second" }, { value: "third" }] },
  "stream:orders": {
    type: "stream",
    entries: [
      { id: "1790882554510-0", fields: [["order", "1"]] },
      { id: "1790882554510-1", fields: [["order", "2"]] },
    ],
  },
  "binary:blob": { type: "string", value: { base64: "//4AAQ==" } },
}

const glob = (pattern: string) =>
  new RegExp(
    `^${pattern
      .replace(/[.+^${}()|\\]/g, "\\$&")
      .replace(/\*/g, ".*")
      .replace(/\?/g, ".")}$`,
  )

function lengthOf(key: FakeKey) {
  if (key.length !== undefined) return key.length
  if (key.type === "string") return typeof key.value === "string" ? key.value.length : 4
  if (key.type === "stream") return key.entries?.length ?? 0
  return key.rows?.length ?? 0
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const refuse = (route: Route, status: number, code: string, message: string) =>
  json(route, { error: { code, message } }, status)

/**
 * Routes the Redis API of connection 4 over the section's fixture. Returns the
 * keyspace (the tests read it back to see what a write did) and every request
 * made, in order.
 */
async function mockRedis(page: Page, options: RedisMock = {}) {
  const keys: Record<string, FakeKey> = structuredClone(options.keys ?? DEFAULT_KEYS)
  const calls: Call[] = []
  const state = { failing: options.failing as RegExp | undefined, polls: 0 }
  const db = options.db ?? 0

  await mockDatabases(page, options)
  if (options.limited) {
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read", "service.control"],
        user: {
          id: 2,
          username: "limited",
          displayName: "Limited",
          avatarVersion: 0,
          role: "limited",
          totpEnabled: true,
          disabled: false,
          mustChangePassword: false,
          lastLoginAt: "2026-10-01T09:00:00Z",
          createdAt: "2026-10-01T09:00:00Z",
        },
      }),
    )
  }

  const listed = (pattern: string, type: string | null) =>
    Object.entries(keys)
      .filter(([name, key]) => glob(pattern).test(name) && (!type || key.type === type))
      .map(([name, key]) => ({
        key: name,
        type: key.type,
        ttl: key.ttl ?? -1,
        size: lengthOf(key),
      }))

  // Registered after the fixture's catch-all, so it is consulted first; what
  // it does not know falls back to the fixture.
  await page.route("**/api/v1/databases/4/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1\/databases\/4/, "")
    const method = request.method()
    const query = url.searchParams
    const body = request.postData() ? JSON.parse(request.postData() ?? "null") : undefined
    calls.push({ method, path, query, body })
    if (state.failing?.test(path)) {
      return refuse(route, 502, "connect_failed", "the server did not answer")
    }

    const named = () => query.get("key") ?? ""
    const total = Object.keys(keys).length

    if (method === "GET" && path === "/redis/server") {
      return json(route, {
        flavor: "redis",
        version: "7.4.1",
        redisVersion: "7.4.1",
        mode: "standalone",
        features: {
          databases: true,
          scanType: true,
          memoryUsage: true,
          objectEncoding: true,
          objectIdleTime: true,
          objectFreq: false,
          unlink: true,
          copy: true,
          keepTtl: true,
          hashFieldTtl: false,
          streams: true,
          json: false,
          search: false,
          timeSeries: false,
          commandDocs: true,
          slowlog: true,
          latency: true,
          config: true,
          acl: true,
          clientList: true,
          monitor: true,
          pubsub: true,
          functions: true,
          aof: true,
          ...options.features,
        },
        modules: [],
        role: "primary",
        db,
        databases: 16,
        uptimeSeconds: 86_400,
        sampledAtMs: Date.now(),
        keyspace: [
          { db, keys: total, expires: 2, avgTtlMs: 3_600_000 },
          { db: 7, keys: 41, expires: 0, avgTtlMs: 0 },
        ],
        memory: { used: 8_000_000, rss: 12_000_000, peak: 9_000_000, max: 0, policy: "noeviction" },
        persistence: {
          loading: false,
          dir: "/data",
          file: "dump.rdb",
          rdb: {
            schedule: "3600 1 300 100",
            lastSaveAt: "2026-10-01T08:00:00Z",
            changesSinceSave: 12,
            inProgress: false,
            lastStatus: "ok",
            lastDurationSeconds: 1,
          },
          aof: { supported: true, enabled: true, rewriteInProgress: false, fsync: "everysec" },
        },
        replication: { role: "primary", offset: 0, replicas: [] },
        sections: [],
        ...options.server,
      })
    }

    if (method === "GET" && path === "/stats") {
      state.polls += 1
      return json(route, {
        server: {
          counters: {
            total_commands_processed: state.polls * 600,
            instantaneous_ops_per_sec: 200,
            keyspace_hits: state.polls * 90,
            keyspace_misses: state.polls * 10,
            used_memory: 8_000_000,
            used_memory_rss: 12_000_000,
            maxmemory: 0,
            connected_clients: 3,
            blocked_clients: 0,
            keys: total,
            expires: 2,
            total_net_input_bytes: state.polls * 1000,
            total_net_output_bytes: state.polls * 4000,
            expired_keys: 0,
            evicted_keys: 0,
          },
          flavor: "redis",
          version: "7.4.1",
          role: "primary",
          mode: "standalone",
          sampledAtMs: Date.now(),
        },
      })
    }

    if (method === "GET" && path === "/keys") {
      const all = listed(query.get("pattern") ?? "*", query.get("type"))
      const size = options.pageSize ?? 200
      const cursor = query.get("cursor") ?? "0"
      if (cursor !== "0" && cursor !== BIG_CURSOR) {
        return refuse(route, 400, "bad_request", `unknown cursor ${cursor}`)
      }
      const from = cursor === "0" ? 0 : size
      const page = cursor === "0" ? all.slice(0, size) : all.slice(from)
      const more = cursor === "0" && all.length > size
      return json(route, {
        keys: page,
        cursor: more ? BIG_CURSOR : "0",
        done: !more,
        db: Number(query.get("db") ?? db),
        total,
      })
    }

    if (method === "GET" && path === "/keys/tree") {
      const prefix = query.get("prefix") ?? ""
      const all = listed(query.get("pattern") ?? "*", query.get("type")).filter((entry) =>
        entry.key.startsWith(prefix),
      )
      const second = query.get("cursor") === BIG_CURSOR
      const half = Math.ceil(all.length / 2)
      const part =
        options.treeInTwo && !prefix ? (second ? all.slice(half) : all.slice(0, half)) : all
      const folders = new Map<
        string,
        { count: number; keyCount: number; types: Record<string, number> }
      >()
      const direct: typeof all = []
      const types: Record<string, number> = {}
      for (const entry of part) {
        types[entry.type] = (types[entry.type] ?? 0) + 1
        const rest = entry.key.slice(prefix.length)
        const cut = rest.indexOf(":")
        if (cut < 0) {
          direct.push(entry)
          continue
        }
        const name = rest.slice(0, cut)
        const folder = folders.get(name) ?? { count: 0, keyCount: 0, types: {} }
        folder.count += 1
        if (!rest.slice(cut + 1).includes(":")) folder.keyCount += 1
        folder.types[entry.type] = (folder.types[entry.type] ?? 0) + 1
        folders.set(name, folder)
      }
      const complete = !(options.treeInTwo && !prefix) || second
      return json(route, {
        db: Number(query.get("db") ?? db),
        delimiter: ":",
        prefix,
        folders: [...folders.entries()].map(([name, folder]) => ({
          name,
          prefix: `${prefix}${name}:`,
          pattern: `${prefix}${name}:*`,
          count: folder.count,
          keyCount: folder.keyCount,
          folders: folder.count - folder.keyCount,
          types: folder.types,
        })),
        keys: direct,
        keyCount: direct.length,
        count: part.length,
        types,
        scanned: part.length,
        cursor: complete ? "0" : BIG_CURSOR,
        complete,
        total,
        elapsedMs: 2,
      })
    }

    if (method === "GET" && path === "/keys/meta") {
      const key = keys[named()]
      if (!key) return refuse(route, 404, "key_not_found", "that key does not exist")
      const ttl = key.ttl ?? -1
      return json(route, {
        key: named(),
        db: Number(query.get("db") ?? db),
        type: key.type,
        ttl,
        pttl: ttl < 0 ? -1 : ttl * 1000,
        length: lengthOf(key),
        memory: 136,
        ...(options.features?.objectEncoding === false
          ? { unavailable: { encoding: "Dragonfly 2.0.0 has no OBJECT ENCODING." } }
          : { encoding: "listpack" }),
        idleSeconds: 4,
      })
    }

    if (method === "GET" && path === "/keys/members") {
      const key = keys[named()]
      if (!key) return refuse(route, 404, "key_not_found", "that key does not exist")
      const ttl = key.ttl ?? -1
      const base = {
        key: named(),
        db: Number(query.get("db") ?? db),
        type: key.type,
        ttl,
        pttl: ttl < 0 ? -1 : ttl * 1000,
        length: lengthOf(key),
        rows: [] as unknown[],
        cursor: "0",
        done: true,
      }
      if (key.type === "string") {
        return json(route, { ...base, string: { value: key.value, offset: 0 } })
      }
      if (key.type === "ReJSON-RL") {
        // As the server sends it: the document is part of the answer's own text.
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: `{"key":${JSON.stringify(named())},"db":${db},"type":"ReJSON-RL","ttl":-1,"pttl":-1,"length":0,"rows":[],"json":{"path":"$","matches":[${key.json}],"bytes":${key.json?.length ?? 0}},"cursor":"0","done":true}`,
        })
      }
      if (key.type === "stream") {
        return json(route, { ...base, rows: key.entries ?? [] })
      }
      const match = query.get("match")
      const rows = (key.rows ?? [])
        .map((row, index) => (key.type === "list" ? { ...row, index } : row))
        .filter((row) => !match || glob(match).test(String(row.field ?? row.value)))
      return json(route, { ...base, rows })
    }

    if (method === "POST" && path === "/keys/value") {
      const write = body as Record<string, unknown>
      const name = write.key as string
      const type = (write.type as string) ?? "string"
      if (write.create && keys[name]) {
        return refuse(route, 409, "key_exists", "a key of that name already exists")
      }
      const key = (keys[name] ??= { type: type === "json" ? "ReJSON-RL" : type, rows: [] })
      if (typeof write.ttl === "number") key.ttl = write.ttl
      if (type === "string") key.value = write.value as Bytes
      if (type === "hash") {
        key.rows = (key.rows ?? []).filter(
          (row) => row.field !== write.field && row.field !== write.replace,
        )
        key.rows.push({ field: write.field as string, value: write.value as string })
      }
      if (type === "list") {
        const rows = key.rows ?? []
        const index = write.index as number | undefined
        if (
          index !== undefined &&
          write.expect !== undefined &&
          rows[index]?.value !== write.expect
        ) {
          return refuse(route, 409, "conflict", "the list changed since it was read")
        }
        if (index === undefined) {
          if (write.position === "head") rows.unshift({ value: write.value as string })
          else rows.push({ value: write.value as string })
        } else if (write.insert) rows.splice(index, 0, { value: write.value as string })
        else rows[index] = { value: write.value as string }
        key.rows = rows
      }
      if (type === "json") key.json = write.value as string
      return json(route, { ok: true })
    }
    if (method === "POST" && path === "/keys/expire") {
      const write = body as { key: string; ttl?: number }
      if (keys[write.key]) keys[write.key].ttl = write.ttl
      return json(route, { ok: true, pttl: (write.ttl ?? 0) * 1000 })
    }
    if (method === "POST" && path === "/keys/persist") {
      const write = body as { key: string }
      if (keys[write.key]) keys[write.key].ttl = -1
      return json(route, { ok: true, pttl: -1 })
    }
    if (method === "POST" && path === "/keys/rename") {
      const write = body as { key: string; to: string; overwrite?: boolean }
      if (keys[write.to] && !write.overwrite) {
        return refuse(route, 409, "key_exists", "a key of that name already exists")
      }
      keys[write.to] = keys[write.key]
      delete keys[write.key]
      return json(route, { ok: true })
    }
    if (method === "DELETE" && path === "/keys") {
      const gone = body as { key?: string; member?: Bytes }
      if (gone.key && "member" in gone) {
        const key = keys[gone.key]
        key.rows = (key.rows ?? []).filter(
          (row) => (key.type === "hash" ? row.field : row.value) !== gone.member,
        )
        if (key.length) key.length -= 1
      } else if (gone.key) delete keys[gone.key]
      return json(route, { removed: 1 })
    }
    if (method === "POST" && path === "/keys/bulk") {
      const bulk = body as { pattern: string; type?: string; action: string; dryRun?: boolean }
      const hit = listed(bulk.pattern, bulk.type ?? null)
      if (!bulk.dryRun && bulk.action === "delete") for (const entry of hit) delete keys[entry.key]
      return json(route, {
        action: bulk.action,
        dryRun: Boolean(bulk.dryRun),
        db,
        pattern: bulk.pattern,
        matched: hit.length,
        affected: bulk.dryRun ? 0 : hit.length,
        complete: true,
        cursor: "0",
        sample: hit.slice(0, 20).map((entry) => entry.key),
        total,
        elapsedMs: 3,
      })
    }
    if (method === "GET" && path === "/keys/stream") {
      return json(route, { key: named(), db, length: 2, groups: [] })
    }

    if (method === "POST" && path === "/redis/classify") {
      const command = String((body as { command: string }).command)
      const word = command.trim().split(/\s+/)[0].toUpperCase()
      const verdict = {
        name: word,
        class: "read",
        admin: false,
        slow: false,
        known: true,
        reasons: [] as string[],
        requires: ["service.control"],
        allowed: true,
        ...options.verdicts?.[word],
      }
      return json(route, verdict)
    }
    if (method === "POST" && path === "/redis/command") {
      const command = String((body as { command: string }).command)
      const [word, name] = command.trim().split(/\s+/)
      const reply =
        word.toUpperCase() === "GET"
          ? keys[name]
            ? { type: "string", value: keys[name].value }
            : { type: "nil" }
          : word.toUpperCase() === "DEL"
            ? { type: "integer", value: 1 }
            : { type: "status", value: "OK" }
      if (word.toUpperCase() === "DEL") delete keys[name]
      return json(route, {
        name: word.toUpperCase(),
        class: options.verdicts?.[word.toUpperCase()]?.class ?? "read",
        admin: false,
        slow: false,
        known: true,
        reasons: [],
        db,
        ms: 0.4,
        reply,
        truncated: false,
      })
    }
    if (method === "GET" && path === "/redis/commands") {
      const ref = (name: string, group: string, kind: string, summary: string, syntax: string) => ({
        name,
        group,
        ...(options.documented === false ? {} : { summary, syntax, since: "1.0.0" }),
        arity: -2,
        flags: [],
        categories: [],
        class: kind,
        admin: false,
      })
      return json(route, {
        flavor: "redis",
        version: "7.4.1",
        documented: options.documented !== false,
        commands: [
          ref("GET", "string", "read", "Returns the string value of a key.", "key"),
          ref("GETDEL", "string", "dangerous", "Returns a key's value and deletes it.", "key"),
          ref("HGETALL", "hash", "read", "Returns all fields and values in a hash.", "key"),
          ref(
            "SET",
            "string",
            "write",
            "Sets the string value of a key.",
            "key value [EX seconds]",
          ),
          ref("DEL", "generic", "dangerous", "Deletes one or more keys.", "key [key ...]"),
          ref("SUBSCRIBE", "pubsub", "blocked", "Listens for messages.", "channel [channel ...]"),
        ],
      })
    }

    if (method === "GET" && path === "/redis/commandstats") {
      return json(route, {
        commands: [
          { command: "SET", calls: 60_000, usec: 90_000, usecPerCall: 1.5, failed: 0, rejected: 0 },
          { command: "KEYS", calls: 3, usec: 30_000, usecPerCall: 10_000, failed: 0, rejected: 0 },
          { command: "GET", calls: 90_000, usec: 45_000, usecPerCall: 0.5, failed: 2, rejected: 0 },
        ],
        totalCalls: 150_003,
        totalUsec: 165_000,
        sampledAtMs: Date.now(),
      })
    }
    if (method === "GET" && path === "/redis/latency") {
      return json(route, {
        supported: true,
        enabled: false,
        thresholdMs: 0,
        events: [],
        reason: "The latency monitor is off.",
      })
    }
    if (method === "GET" && path === "/redis/clients") {
      return json(route, {
        clients: options.clients ?? [
          {
            id: 7,
            addr: "10.0.0.5:51000",
            name: "worker",
            user: "default",
            ageSeconds: 600,
            idleSeconds: 2,
            db: 0,
            command: "BLPOP",
            flags: "b",
            subscriptions: 0,
            inTransaction: false,
            outputMemory: 0,
            totalMemory: 20_000,
            self: false,
          },
          {
            id: 9,
            addr: "10.0.0.1:40000",
            name: "",
            user: "default",
            ageSeconds: 0,
            idleSeconds: 0,
            db: 0,
            command: "CLIENT LIST",
            flags: "N",
            subscriptions: 0,
            inTransaction: false,
            outputMemory: 0,
            totalMemory: 30_000,
            self: true,
          },
        ],
      })
    }
    if (method === "POST" && path === "/redis/clients/kill") return json(route, { ok: true })
    if (method === "GET" && path === "/redis/slowlog") {
      return json(route, {
        supported: true,
        thresholdUs: 10_000,
        maxLen: 128,
        length: (options.slowlog ?? SLOW).length,
        entries: options.slowlog ?? SLOW,
      })
    }
    if (method === "POST" && path === "/redis/slowlog/reset") return json(route, { ok: true })
    if (method === "POST" && path === "/redis/save") {
      return json(route, { ok: true, status: "Background saving started" })
    }
    if (method === "GET" && path === "/redis/analysis") {
      const group = (name: string, n: number, memory: number) => ({
        name,
        keys: n,
        memory,
        estimatedKeys: n * 2,
        estimatedMemory: memory * 2,
      })
      return json(route, {
        db: Number(query.get("db") ?? db),
        delimiter: ":",
        sampledAtMs: Date.now(),
        elapsedMs: 40,
        total: 2000,
        sampled: Number(query.get("sample")),
        complete: false,
        scale: 2,
        timedOut: false,
        memory: 400_000,
        estimatedMemory: 800_000,
        usedMemory: 8_000_000,
        types: [group("hash", 300, 250_000), group("string", 700, 150_000)],
        namespaces: [group("user", 600, 300_000), group("session", 400, 100_000)],
        topKeys: [
          {
            key: "user:1:profile",
            type: "hash",
            memory: 90_000,
            ttl: -1,
            size: 1200,
            encoding: "hashtable",
          },
        ],
        expiry: [
          group("none", 600, 300_000),
          group("hour", 0, 0),
          group("day", 400, 100_000),
          group("week", 0, 0),
          group("later", 0, 0),
        ],
        encodings: [group("hash:hashtable", 1, 90_000)],
      })
    }
    if (method === "GET" && path === "/redis/pubsub") {
      return json(route, { channels: [{ name: "orders.created", subscribers: 2 }], patterns: 0 })
    }
    if (method === "POST" && path === "/redis/publish") return json(route, { receivers: 2 })

    return route.fallback()
  })

  return {
    keys,
    calls,
    /** The requests made to one path, as `METHOD path?query`. */
    asked: (path: string) => calls.filter((call) => call.path === path),
    /** The paths that were failing answer again. */
    heal: () => {
      state.failing = undefined
    },
  }
}

const SLOW = [
  {
    id: 3,
    at: "2026-10-01T08:59:00Z",
    durationUs: 25_000,
    command: "KEYS",
    args: ["*"],
    client: "10.0.0.5:51000",
    clientName: "worker",
  },
]

const rail = (page: Page) => page.locator("[data-slot=redis-key-rail]")
const pane = (page: Page) => page.locator("[data-slot=redis-key]")
const where = (page: Page) => new URL(page.url()).pathname + new URL(page.url()).search

test.describe("keys", () => {
  test("the picker starts on the database the connection string names, and no request names one until the reader does", async ({
    page,
  }) => {
    const redis = await mockRedis(page, { db: 3 })
    await page.goto(KEYS)

    // The old browser showed db0 selected over the keys of whichever database
    // the connection was made to.
    await expect(page.getByRole("combobox", { name: "Logical database" })).toHaveText("db3")
    await expect(rail(page).getByRole("button", { name: /^session/ })).toBeVisible()
    for (const call of redis.asked("/keys/tree")) expect(call.query.has("db")).toBe(false)

    // Database 0 can be reached, and is asked for by its number.
    await page.getByRole("combobox", { name: "Logical database" }).click()
    await expect(page.getByRole("option", { name: /^db3/ })).toContainText("connects here")
    await expect(page.getByRole("option", { name: /^db7/ })).toContainText("41 keys")
    await page.getByRole("option", { name: /^db0/ }).click()
    await expect(page).toHaveURL(/[?&]db=0(&|$)/)
    await expect.poll(() => redis.asked("/keys/tree").at(-1)?.query.get("db")).toBe("0")
  })

  test("the tree is the server's namespaces, read a level at a time, and a walk that stopped says how far it got", async ({
    page,
  }) => {
    const redis = await mockRedis(page, { treeInTwo: true })
    await page.goto(KEYS)

    const progress = rail(page).locator("[data-slot=redis-scan-progress]")
    await expect(progress).toHaveText("Scanned 5 of about 9")
    // Nothing below the top has been read: a level is asked for when opened.
    expect(redis.asked("/keys/tree").every((call) => !call.query.has("prefix"))).toBe(true)

    await rail(page).getByRole("button", { name: "Scan more" }).click()
    await expect(progress).toHaveText("Scanned all 9 keys")
    // The cursor of the walk went back as it came.
    expect(redis.asked("/keys/tree").at(-1)?.query.get("cursor")).toBe(BIG_CURSOR)
    // The two calls' counts were added, namespace by namespace.
    await expect(rail(page).getByRole("button", { name: /^user/ })).toContainText("3")

    await rail(page).getByRole("button", { name: /^user/ }).click()
    await expect.poll(() => redis.asked("/keys/tree").at(-1)?.query.get("prefix")).toBe("user:")
    await rail(page).getByRole("button", { name: /^1/ }).click()
    await expect(rail(page).getByRole("button", { name: "Open user:1:profile" })).toBeVisible()
    // The type chips carry the walk's counts.
    await expect(rail(page).getByRole("button", { name: /^Hash/ })).toContainText("2")
  })

  test("Redis scan cursors retain all unsigned 64-bit digits in requests", async ({ page }) => {
    const redis = await mockRedis(page, { pageSize: 4 })
    await page.goto(KEYS)
    // "List" is a type of key too: the arrangement is the one in its own group.
    await rail(page)
      .getByRole("group", { name: "Arrangement" })
      .getByRole("button", { name: "List" })
      .click()

    const cursors = () => redis.asked("/keys").map((call) => call.query.get("cursor"))
    await expect(rail(page).getByRole("button", { name: /^Open / })).toHaveCount(4)
    await expect.poll(cursors).toEqual(["0"])
    await expect(rail(page).locator("[data-slot=redis-scan-progress]")).toHaveText(
      "4 of about 9 keys",
    )

    // 2^64 − 1 as a JavaScript number is 18446744073709552000: another cursor.
    await rail(page).getByRole("button", { name: "Scan more" }).click()
    await expect.poll(cursors).toEqual(["0", BIG_CURSOR])
    await expect(rail(page).getByRole("button", { name: /^Open / })).toHaveCount(9)
    await expect(rail(page).getByRole("button", { name: "Scan more" })).toHaveCount(0)

    // Refresh starts the scan again from its beginning.
    await rail(page).getByRole("button", { name: "Refresh the keys" }).click()
    await expect.poll(cursors).toEqual(["0", BIG_CURSOR, "0"])
  })

  test("the key and its database are in the address: a reload opens the same key and Back the one before", async ({
    page,
  }) => {
    await mockRedis(page, { db: 3 })
    await page.goto(KEYS)
    await rail(page).getByRole("button", { name: "Open leaderboard" }).click()
    await expect(pane(page).getByRole("heading", { name: "leaderboard" })).toBeVisible()
    // The address names the database too, so the link is the same key anywhere.
    await expect(page).toHaveURL(/db=3/)
    await expect(page).toHaveURL(/key=leaderboard/)

    await rail(page).getByRole("button", { name: "Open queue" }).click()
    await expect(pane(page).getByRole("heading", { name: "queue" })).toBeVisible()

    await page.reload()
    await expect(pane(page).getByRole("heading", { name: "queue" })).toBeVisible()
    await page.goBack()
    await expect(pane(page).getByRole("heading", { name: "leaderboard" })).toBeVisible()
    await page.goForward()
    await expect(pane(page).getByRole("heading", { name: "queue" })).toBeVisible()
  })

  test("saving a string sends the value and no expiry, and what was typed survives opening another key", async ({
    page,
  }) => {
    const redis = await mockRedis(page)
    await page.goto(`${KEYS}?key=session%3A2`)

    const value = pane(page).getByRole("textbox", { name: "The value, as text" })
    await expect(value).toHaveValue("plain")
    await expect(pane(page).getByRole("button", { name: "Save", exact: true })).toBeDisabled()
    await value.fill("changed")
    await expect(pane(page).getByText("Unsaved changes")).toBeVisible()

    // Another key and back: the draft was not dropped.
    await rail(page).getByRole("button", { name: "Open queue" }).click()
    await expect(pane(page).getByRole("heading", { name: "queue" })).toBeVisible()
    await page.goBack()
    await expect(pane(page).getByRole("textbox", { name: "The value, as text" })).toHaveValue(
      "changed",
    )

    await pane(page).getByRole("button", { name: "Save", exact: true }).click()
    await expect(pane(page).getByText("Unsaved changes")).toHaveCount(0)
    // No `ttl` in the body: the server keeps the key's expiry across a write.
    expect(redis.asked("/keys/value").at(-1)?.body).toEqual({
      key: "session:2",
      type: "string",
      value: "changed",
    })
    expect(redis.keys["session:2"].ttl).toBe(3600)
  })

  test("a value that is not text opens as bytes and is never offered to a text box", async ({
    page,
  }) => {
    const redis = await mockRedis(page)
    await page.goto(`${KEYS}?key=binary%3Ablob`)

    await expect(pane(page).getByText("Bytes, not text")).toBeVisible()
    await expect(pane(page).getByRole("button", { name: "Hex", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect(pane(page).getByRole("button", { name: "Text", exact: true })).toBeDisabled()
    await expect(pane(page).getByRole("button", { name: "JSON", exact: true })).toBeDisabled()
    await expect(pane(page).getByRole("group", { name: "The value's bytes" })).toContainText(
      "ff fe 00 01",
    )

    // Edited as bytes, it goes back as bytes.
    await pane(page).getByRole("button", { name: "Edit bytes" }).click()
    await pane(page).getByRole("textbox", { name: "The value's bytes, as hex" }).fill("ff fe 00 02")
    await pane(page).getByRole("button", { name: "Save", exact: true }).click()
    await expect
      .poll(() => redis.asked("/keys/value").at(-1)?.body)
      .toEqual({
        key: "binary:blob",
        type: "string",
        value: { base64: "//4AAg==" },
      })
  })

  test("text a box would rewrite is read as bytes, and lines that end in CR LF are saved as they were", async ({
    page,
  }) => {
    const redis = await mockRedis(page, {
      keys: {
        ctl: { type: "string", value: "\u0000\u0001\u0002" },
        crlf: { type: "string", value: "one\r\ntwo\r\n" },
      },
    })
    // Valid UTF-8, and nothing a text box would show or hand back.
    await page.goto(`${KEYS}?key=ctl`)
    await expect(pane(page).getByText("Control characters, read as bytes")).toBeVisible()
    await expect(pane(page).getByRole("button", { name: "Text", exact: true })).toBeDisabled()
    await expect(pane(page).getByRole("group", { name: "The value's bytes" })).toContainText(
      "00 01 02",
    )

    // A box turns every CR LF into a line feed; the save puts them back.
    await page.goto(`${KEYS}?key=crlf`)
    const value = pane(page).getByRole("textbox", { name: "The value, as text" })
    await expect(value).toHaveValue("one\ntwo\n")
    await expect(pane(page).getByText("CR LF line endings")).toBeVisible()
    await expect(pane(page).getByRole("button", { name: "Save", exact: true })).toBeDisabled()
    await value.fill("one\ntwo\nthree\n")
    await pane(page).getByRole("button", { name: "Save", exact: true }).click()
    await expect
      .poll(() => redis.asked("/keys/value").at(-1)?.body)
      .toEqual({ key: "crlf", type: "string", value: "one\r\ntwo\r\nthree\r\n" })
  })

  test("an expiry typed wrong is refused and nothing is sent; removing one is its own control", async ({
    page,
  }) => {
    const redis = await mockRedis(page)
    await page.goto(`${KEYS}?key=session%3A1`)

    await pane(page)
      .getByRole("button", { name: /^Expiry: / })
      .click()
    const editor = page.locator("[data-slot=popover-content]")
    // Each of these cleared the expiry in the old box: `Number(text) || -1`.
    for (const typed of ["soon", "0", "60 sec"]) {
      await editor.getByLabel("Time to live").fill(typed)
      await editor.getByRole("button", { name: "Set expiry" }).click()
      await expect(editor.getByText(/Seconds, or a span|at least one second/)).toBeVisible()
    }
    await editor.getByLabel("Time to live").fill("")
    await editor.getByRole("button", { name: "Set expiry" }).click()
    await expect(editor.getByText("Type how long the key should live.")).toBeVisible()
    expect(redis.asked("/keys/expire")).toEqual([])
    expect(redis.asked("/keys/persist")).toEqual([])

    await editor.getByLabel("Time to live").fill("5m")
    await editor.getByRole("button", { name: "Set expiry" }).click()
    await expect
      .poll(() => redis.asked("/keys/expire").at(-1)?.body)
      .toEqual({
        key: "session:1",
        ttl: 300,
      })
    // The reading is the server's, read again — not the number that was typed.
    await expect(pane(page).getByRole("button", { name: /^Expiry: (5m|4m)/ })).toBeVisible()

    await pane(page)
      .getByRole("button", { name: /^Expiry: / })
      .click()
    await page
      .locator("[data-slot=popover-content]")
      .getByRole("button", { name: "Remove expiry" })
      .click()
    await expect.poll(() => redis.asked("/keys/persist").at(-1)?.body).toEqual({ key: "session:1" })
    await expect(pane(page).getByRole("button", { name: /^Expiry: No expiry/ })).toBeVisible()
  })

  test("a role that may write and not remove is offered no removal", async ({ page }) => {
    await mockRedis(page, { limited: true })
    await page.goto(`${KEYS}?key=user%3A1%3Aprofile`)

    await expect(pane(page).getByRole("button", { name: "Rename", exact: true })).toBeVisible()
    await expect(pane(page).getByRole("button", { name: "Add field" })).toBeVisible()
    // `DELETE /keys` asks for the destructive capability, for a key and for a member.
    await expect(pane(page).getByRole("button", { name: "Delete key" })).toHaveCount(0)
    await expect(pane(page).getByRole("button", { name: /^Remove / })).toHaveCount(0)
    // Bulk delete and expire ask for it too; removing expiries does not.
    await rail(page).getByRole("button", { name: "More key actions" }).click()
    await page.getByRole("menuitem", { name: "Bulk actions…" }).click()
    await expect(
      page.getByRole("dialog").getByRole("radio", { name: "Remove expiries" }),
    ).toBeVisible()
    await expect(page.getByRole("dialog").getByRole("radio", { name: "Delete" })).toHaveCount(0)
  })

  for (const [what, options] of [
    ["a reader", { viewer: true }],
    ["a protected connection", { rows: { 4: { readOnly: true } } }],
  ] as const) {
    test(`${what} is drawn nothing that writes`, async ({ page }) => {
      await mockRedis(page, options)
      await page.goto(`${KEYS}?key=user%3A1%3Aprofile`)

      await expect(pane(page).getByRole("heading", { name: "user:1:profile" })).toBeVisible()
      await expect(pane(page).getByText("Ada")).toBeVisible()
      for (const name of ["New key", "Add field", "Rename", "Delete key"]) {
        await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0)
      }
      await expect(page.getByRole("button", { name: /^(Edit|Remove) / })).toHaveCount(0)
      // The expiry is a reading, not a control.
      await expect(pane(page).getByRole("button", { name: /^Expiry: / })).toHaveCount(0)
      await expect(pane(page).getByText("No expiry")).toBeVisible()
      await rail(page).getByRole("button", { name: "More key actions" }).click()
      await expect(page.getByRole("menuitem", { name: "Bulk actions…" })).toHaveCount(0)
    })
  }

  test("a hash says how many fields it has, renames in one step and removes the empty field as a member", async ({
    page,
  }) => {
    const redis = await mockRedis(page)
    await page.goto(`${KEYS}?key=user%3A1%3Aprofile`)

    // Three rows are loaded; the key holds 1,200.
    await expect(pane(page).locator("[data-slot=redis-member-count]")).toHaveText("1,200 fields")
    await expect(pane(page).getByText("1,200 fields").first()).toBeVisible()

    // A value edited on its row.
    await pane(page).getByRole("button", { name: "Edit the value of plan" }).click()
    const value = pane(page).getByRole("textbox", { name: "the value of plan" })
    await value.fill("enterprise")
    await value.press("Enter")
    await expect
      .poll(() => redis.asked("/keys/value").at(-1)?.body)
      .toEqual({
        key: "user:1:profile",
        type: "hash",
        field: "plan",
        value: "enterprise",
      })

    // A field's name changed: the old one is replaced, not left beside it.
    await pane(page).getByRole("row").filter({ hasText: "Ada" }).hover()
    await pane(page).getByRole("button", { name: "Edit name" }).click()
    await page.getByRole("dialog").getByLabel("Field").fill("full name")
    await page.getByRole("dialog").getByRole("button", { name: "Save field" }).click()
    await expect
      .poll(() => redis.asked("/keys/value").at(-1)?.body)
      .toEqual({
        key: "user:1:profile",
        type: "hash",
        field: "full name",
        value: "Ada",
        replace: "name",
      })
    await expect(pane(page).getByRole("row").filter({ hasText: "full name" })).toBeVisible()

    // The field whose name is empty is a member: its removal names it, and
    // can never be read as "remove the key".
    await pane(page).getByRole("row").filter({ hasText: "empty field" }).hover()
    await pane(page).getByRole("button", { name: "Remove the empty field" }).click()
    await expect(page.getByRole("dialog")).toContainText("user:1:profile")
    await page.getByRole("dialog").getByRole("button", { name: "Remove field" }).click()
    await expect
      .poll(() => redis.asked("/keys").at(-1)?.body)
      .toEqual({
        key: "user:1:profile",
        type: "hash",
        member: "",
      })
    expect(redis.keys["user:1:profile"]).toBeDefined()
  })

  test("a dialog put away with Escape opens again on what was typed; Cancel is what discards it", async ({
    page,
  }) => {
    const redis = await mockRedis(page)
    await page.goto(`${KEYS}?key=user%3A1%3Aprofile`)
    const dialog = page.getByRole("dialog")

    await pane(page).getByRole("button", { name: "Add field" }).click()
    await dialog.getByLabel("Field", { exact: true }).fill("email")
    await dialog.getByLabel("Value", { exact: true }).fill("ada@example.com")
    // The old dialogs dropped their fields on Escape and on a click outside.
    await page.keyboard.press("Escape")
    await expect(dialog).toHaveCount(0)
    await pane(page).getByRole("button", { name: "Add field" }).click()
    await expect(dialog.getByLabel("Field", { exact: true })).toHaveValue("email")
    await expect(dialog.getByLabel("Value", { exact: true })).toHaveValue("ada@example.com")

    await dialog.getByRole("button", { name: "Cancel" }).click()
    await expect(dialog).toHaveCount(0)
    await pane(page).getByRole("button", { name: "Add field" }).click()
    await expect(dialog.getByLabel("Field", { exact: true })).toHaveValue("")
    expect(redis.asked("/keys/value")).toEqual([])
  })

  test("a list edit carries what was seen, and adding at a position inserts", async ({ page }) => {
    const redis = await mockRedis(page)
    await page.goto(`${KEYS}?key=queue`)

    await pane(page).getByRole("button", { name: "Edit element 1" }).first().click()
    const field = pane(page).getByRole("textbox", { name: "element 1" })
    await field.fill("2nd")
    await field.press("Enter")
    await expect
      .poll(() => redis.asked("/keys/value").at(-1)?.body)
      .toEqual({
        key: "queue",
        type: "list",
        index: 1,
        value: "2nd",
        expect: "second",
      })

    await pane(page).getByRole("row").filter({ hasText: "first" }).hover()
    await pane(page).getByRole("button", { name: "Insert an element before element 0" }).click()
    await page.getByRole("dialog").getByLabel("Element", { exact: true }).fill("zeroth")
    await page.getByRole("dialog").getByRole("button", { name: "Add element" }).click()
    // `insert`, so nothing is overwritten; `expect`, so a list that moved refuses.
    await expect
      .poll(() => redis.asked("/keys/value").at(-1)?.body)
      .toEqual({
        key: "queue",
        type: "list",
        index: 0,
        insert: true,
        value: "zeroth",
        expect: "first",
      })
    await expect(pane(page).getByRole("row")).toHaveCount(5)
    expect(redis.keys.queue.rows?.map((row) => row.value)).toEqual([
      "zeroth",
      "first",
      "2nd",
      "third",
    ])
  })

  test("sorted-set members that share a score are two rows", async ({ page }) => {
    await mockRedis(page)
    await page.goto(`${KEYS}?key=leaderboard`)
    // Rows used to be keyed by score, and these two were one.
    await expect(pane(page).getByRole("row").filter({ hasText: "ann" })).toBeVisible()
    await expect(pane(page).getByRole("row").filter({ hasText: "bob" })).toBeVisible()
    await expect(pane(page).getByRole("button", { name: "Edit the score of cyd" })).toHaveText(
      "inf",
    )
  })

  test("a read that fails says so and tries again; a key that is gone says it is gone", async ({
    page,
  }) => {
    const redis = await mockRedis(page, { failing: /^\/keys\/tree$/ })
    await page.goto(KEYS)
    await expect(rail(page).getByRole("alert")).toContainText("the server did not answer")
    redis.heal()
    await rail(page).getByRole("button", { name: "Try again" }).click()
    await expect(rail(page).getByRole("button", { name: "Open leaderboard" })).toBeVisible()

    await page.goto(`${KEYS}?key=expired%3Akey`)
    await expect(page.getByText("This key is gone")).toBeVisible()
    await page.getByRole("button", { name: "Back to the keys" }).click()
    await expect(page.getByText("No key is open")).toBeVisible()
    // The page says so at once; the address follows it.
    await expect.poll(() => where(page)).not.toContain("key=")
  })

  test("a failed re-read leaves the keys on screen", async ({ page }) => {
    const redis = await mockRedis(page)
    await page.goto(KEYS)
    await expect(rail(page).getByRole("button", { name: "Open leaderboard" })).toBeVisible()

    await page.route("**/api/v1/databases/4/keys/tree**", (route) =>
      route.fulfill({
        status: 502,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "connect_failed", message: "gone away" } }),
      }),
    )
    await rail(page).getByRole("button", { name: "Refresh the keys" }).click()
    await expect(rail(page).locator("[data-slot=redis-scan-progress]")).toContainText(
      "the last read failed",
    )
    await expect(rail(page).getByRole("button", { name: "Open leaderboard" })).toBeVisible()
    expect(redis.asked("/keys/tree").length).toBeGreaterThan(0)
  })

  test("bulk delete counts first and names the count; every key is not a bulk action", async ({
    page,
  }) => {
    const redis = await mockRedis(page)
    await page.goto(KEYS)
    await rail(page).getByRole("button", { name: "More key actions" }).click()
    await page.getByRole("menuitem", { name: "Bulk actions…" }).click()
    const dialog = page.getByRole("dialog")

    // The whole database is emptied from Settings, with its name typed.
    await dialog.getByLabel("Pattern").fill("*")
    await expect(dialog.getByText("That pattern is every key")).toBeVisible()
    await dialog.getByRole("button", { name: "Count the keys" }).click()
    await expect(dialog.locator("[data-slot=redis-bulk-count]")).toContainText("9 keys match")
    await expect(dialog.getByRole("button", { name: /^Delete/ })).toHaveCount(0)

    await dialog.getByLabel("Pattern").fill("session:*")
    // A changed pattern has not been counted: there is nothing to delete yet.
    await expect(dialog.getByRole("button", { name: /^Delete/ })).toHaveCount(0)
    await dialog.getByRole("button", { name: "Count the keys" }).click()
    await expect(dialog.locator("[data-slot=redis-bulk-count]")).toContainText("2 keys match")
    await expect(dialog.locator("[data-slot=redis-bulk-count]")).toContainText("session:1")
    expect(
      redis.asked("/keys/bulk").every((call) => (call.body as { dryRun?: boolean }).dryRun),
    ).toBe(true)
    expect(Object.keys(redis.keys)).toContain("session:1")

    await dialog.getByRole("button", { name: "Delete 2 keys…" }).click()
    // The bulk dialog is still in the page while it closes: the confirmation is the one named for the count.
    const confirm = page.getByRole("dialog", { name: "Delete 2 keys" })
    await expect(confirm).toContainText("Delete 2 keys")
    await expect(confirm).toContainText("session:*")
    await confirm.getByRole("button", { name: "Delete 2 keys", exact: true }).click()
    await expect(page.getByText("2 keys deleted")).toBeVisible()
    expect(redis.asked("/keys/bulk").at(-1)?.body).toEqual({
      pattern: "session:*",
      action: "delete",
    })
    expect(Object.keys(redis.keys)).not.toContain("session:1")
    await expect(rail(page).getByRole("button", { name: /^session/ })).toHaveCount(0)
  })

  test("a new key is made by its type and never over an existing one", async ({ page }) => {
    const redis = await mockRedis(page)
    await page.goto(KEYS)
    await rail(page).getByRole("button", { name: "New key" }).click()
    const dialog = page.getByRole("dialog")
    // JSON is not offered: this server has no JSON commands.
    await expect(dialog.getByRole("button", { name: /^JSON/ })).toHaveCount(0)

    await dialog.getByRole("button", { name: /^Hash/ }).click()
    await dialog.getByLabel("Key name").fill("queue")
    await dialog.getByLabel("First field").fill("a")
    await dialog.getByLabel("Its value").fill("1")
    await dialog.getByLabel("Expires in").fill("never")
    await expect(dialog.getByRole("button", { name: "Create key" })).toBeDisabled()
    await dialog.getByLabel("Expires in").fill("1h")
    await dialog.getByRole("button", { name: "Create key" }).click()
    await expect(dialog.getByText("A key named queue already exists")).toBeVisible()
    expect(redis.keys.queue.type).toBe("list")

    await dialog.getByLabel("Key name").fill("user:9:profile")
    await dialog.getByRole("button", { name: "Create key" }).click()
    await expect(pane(page).getByRole("heading", { name: "user:9:profile" })).toBeVisible()
    expect(redis.asked("/keys/value").at(-1)?.body).toEqual({
      key: "user:9:profile",
      type: "hash",
      create: true,
      ttl: 3600,
      field: "a",
      value: "1",
    })
  })

  test("renaming onto a name that is taken is refused, as the hint says", async ({ page }) => {
    const redis = await mockRedis(page)
    await page.goto(`${KEYS}?key=queue`)
    await pane(page).getByRole("button", { name: "Rename", exact: true }).click()
    const dialog = page.getByRole("dialog")
    // The old hint said the existing key "is overwritten"; the server refuses.
    await expect(dialog.getByText("Refused if a key of that name exists.")).toBeVisible()
    await dialog.getByLabel("New name").fill("leaderboard")
    await dialog.getByRole("button", { name: "Rename", exact: true }).click()
    await expect(dialog.getByText("A key named leaderboard already exists")).toBeVisible()
    expect(redis.keys.leaderboard.type).toBe("zset")

    await dialog.getByLabel("New name").fill("queue:jobs")
    await dialog.getByRole("button", { name: "Rename", exact: true }).click()
    await expect(pane(page).getByRole("heading", { name: "queue:jobs" })).toBeVisible()
    expect(redis.asked("/keys/rename").at(-1)?.body).toEqual({ key: "queue", to: "queue:jobs" })
  })

  test("JSON is offered where the server has it, and a number keeps every digit", async ({
    page,
  }) => {
    await mockRedis(page, {
      features: { json: true },
      keys: {
        "doc:1": { type: "ReJSON-RL", json: '{"id":12345678901234567890,"tags":["a"]}' },
      },
    })
    await page.goto(`${KEYS}?key=doc%3A1`)
    await expect(rail(page).getByRole("button", { name: /^JSON/ })).toBeVisible()
    // 12345678901234567890 read through JSON.parse is 12345678901234567000.
    await expect(pane(page).locator("[data-slot=redis-json-tree]")).toContainText(
      "12345678901234567890",
    )
  })

  test("a fact the server cannot give says why instead of reading as nothing", async ({ page }) => {
    await mockRedis(page, {
      features: { objectEncoding: false },
      summaries: { 4: { flavor: "dragonfly", flavorLabel: "Dragonfly", versionNumber: "2.0.0" } },
    })
    await page.goto(`${KEYS}?key=queue`)
    await expect(pane(page).getByRole("heading", { name: "queue" })).toBeVisible()
    await pane(page).getByRole("button", { name: "Why encoding is not known" }).hover()
    await expect(page.getByRole("tooltip")).toContainText("Dragonfly 2.0.0 has no OBJECT ENCODING.")
  })
})

test.describe("console", () => {
  const prompt = (page: Page) => page.getByRole("combobox", { name: "Command" })
  const log = (page: Page) => page.locator("[data-slot=redis-transcript]")
  const VERDICTS = {
    DEL: {
      class: "dangerous",
      reasons: ["deletes keys"],
      requires: ["service.control", "destructive"],
    },
    SET: { class: "write" },
    SUBSCRIBE: {
      class: "blocked",
      allowed: false,
      reasons: ["turns the connection into a subscription; use the Pub/Sub page"],
    },
  }

  test("the server's verdict decides: a read runs, a removal is confirmed first, a blocked command is refused", async ({
    page,
  }) => {
    const redis = await mockRedis(page, { verdicts: VERDICTS, db: 3 })
    await page.goto(CONSOLE)

    await prompt(page).fill("GET session:2")
    await prompt(page).press("Enter")
    await expect(log(page)).toContainText('"plain"')
    // The command runs in the database on screen, which nothing has named yet.
    expect(redis.asked("/redis/command").at(-1)?.body).toEqual({ command: "GET session:2" })

    await prompt(page).fill("SUBSCRIBE news")
    await prompt(page).press("Enter")
    await expect(log(page)).toContainText(
      "Not run: SUBSCRIBE turns the connection into a subscription",
    )
    expect(redis.asked("/redis/command").length).toBe(1)

    await prompt(page).fill("DEL session:2")
    await prompt(page).press("Enter")
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("DEL session:2")
    await expect(dialog).toContainText("DEL deletes keys")
    // Nothing has been sent while the question stands.
    expect(redis.asked("/redis/command").length).toBe(1)
    await dialog.getByRole("button", { name: "Cancel" }).click()
    expect(redis.keys["session:2"]).toBeDefined()

    await prompt(page).fill("DEL session:2")
    await prompt(page).press("Enter")
    await page.getByRole("dialog").getByRole("button", { name: "Run command" }).click()
    await expect(log(page)).toContainText("(integer) 1")
    expect(redis.keys["session:2"]).toBeUndefined()
  })

  test("a role without the capability a command wants is told which, and nothing is sent", async ({
    page,
  }) => {
    const redis = await mockRedis(page, {
      limited: true,
      verdicts: { DEL: { ...VERDICTS.DEL, allowed: false } },
    })
    await page.goto(CONSOLE)
    await prompt(page).fill("DEL session:2")
    await prompt(page).press("Enter")
    await expect(log(page)).toContainText("needs service control and the destructive capability")
    expect(redis.asked("/redis/command")).toEqual([])
  })

  test("a protected connection runs what reads and refuses the rest before sending it", async ({
    page,
  }) => {
    const redis = await mockRedis(page, { verdicts: VERDICTS, rows: { 4: { readOnly: true } } })
    await page.goto(CONSOLE)
    await prompt(page).fill("GET session:2")
    await prompt(page).press("Enter")
    await expect(log(page)).toContainText('"plain"')
    await prompt(page).fill("SET session:2 x")
    await prompt(page).press("Enter")
    await expect(log(page)).toContainText("Not run: this connection is protected")
    expect(redis.asked("/redis/command").length).toBe(1)
  })

  test("a reader has no prompt, and still has the reference", async ({ page }) => {
    await mockRedis(page, { viewer: true })
    await page.goto(CONSOLE)
    await expect(page.getByText("Your role cannot run commands")).toBeVisible()
    await expect(prompt(page)).toHaveCount(0)
    await expect(page.locator("[data-slot=redis-command-helper]")).toContainText("string")
  })

  test("Tab completes a command from the server's reference, the syntax is shown, and Up walks the history", async ({
    page,
  }) => {
    await mockRedis(page)
    await page.goto(CONSOLE)

    await prompt(page).pressSequentially("hg")
    await expect(page.getByRole("option", { name: /HGETALL/ })).toBeVisible()
    await prompt(page).press("Tab")
    await expect(prompt(page)).toHaveValue("HGETALL ")
    await expect(page.locator("[data-slot=redis-syntax]")).toContainText("HGETALL key")
    await prompt(page).pressSequentially("user:1:profile")
    await prompt(page).press("Enter")
    await expect(log(page)).toContainText("HGETALL user:1:profile")

    await expect(prompt(page)).toHaveValue("")
    await prompt(page).press("ArrowUp")
    await expect(prompt(page)).toHaveValue("HGETALL user:1:profile")
    await prompt(page).press("ArrowDown")
    await expect(prompt(page)).toHaveValue("")

    // The reference pane puts a command in the prompt.
    const helper = page.locator("[data-slot=redis-command-helper]")
    await helper.getByRole("textbox", { name: "Find a command" }).fill("deletes")
    await helper.getByRole("button", { name: /^DEL/ }).click()
    await expect(helper).toContainText("Deletes one or more keys.")
    await helper.getByRole("button", { name: "Put it in the prompt" }).click()
    await expect(prompt(page)).toHaveValue("DEL ")
  })

  test("a server that does not describe its commands still lists them", async ({ page }) => {
    await mockRedis(page, { documented: false })
    await page.goto(CONSOLE)
    const helper = page.locator("[data-slot=redis-command-helper]")
    await expect(helper).toContainText("lists its commands without describing them")
    await helper.getByRole("button", { name: /^string/ }).click()
    await expect(helper.getByRole("button", { name: /^GETDEL/ })).toBeVisible()
  })

  test("the live feeds are an administrator's; the channel list and publishing are not", async ({
    page,
  }) => {
    await mockRedis(page, { limited: true })
    await page.goto(CONSOLE)
    await expect(page.getByRole("button", { name: "Monitor", exact: true })).toHaveCount(0)
    await page.getByRole("button", { name: "Pub/Sub", exact: true }).click()
    await expect(page).toHaveURL(/view=pubsub/)
    await expect(page.getByText("orders.created")).toBeVisible()
    await expect(page.getByRole("button", { name: "Publish", exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Listen" })).toHaveCount(0)
    await expect(page.getByText("shown to administrators only")).toBeVisible()
  })

  test("the monitor records over its socket, ends on its own and does not start again", async ({
    page,
  }) => {
    let opened = 0
    await mockRedis(page)
    await page.routeWebSocket("**/api/v1/databases/4/redis/monitor**", (socket) => {
      opened += 1
      const send = (type: string, data: unknown) =>
        socket.send(JSON.stringify({ type, data, ts: Date.now() }))
      send("meta", { seconds: 10, max: 5000, flavor: "redis", version: "7.4.1" })
      send("commands", [
        { at: 1_790_882_554.5, db: 0, client: "10.0.0.5:51000", command: "SET", args: ["k", "v"] },
        { at: 1_790_882_554.6, db: 0, client: "10.0.0.5:51000", command: "GET", args: ["k"] },
      ])
      send("end", { reason: "duration", count: 2, dropped: 0 })
      void socket.close({ code: 1000 })
    })
    await page.goto(`${CONSOLE}?view=monitor`)

    // Nothing is recorded until it is asked for, and the cost is said first.
    await expect(page.getByText("A run shows every client's commands")).toBeVisible()
    expect(opened).toBe(0)
    await page.getByRole("radio", { name: "10 s" }).click()
    await page.getByRole("button", { name: "Start recording" }).click()

    await expect(page.getByRole("log", { name: "Commands the server ran" })).toContainText("SET")
    await expect(page.getByText("The run reached its time")).toBeVisible()
    await expect(page.getByText("2 commands")).toBeVisible()
    // The socket hook reconnects a socket that drops; a run that ended is not one.
    await page.waitForTimeout(2500)
    expect(opened).toBe(1)
  })
})

test.describe("performance", () => {
  test("the readings head the page, and a rate is drawn from two samples", async ({ page }) => {
    await mockRedis(page)
    await page.goto(PERFORMANCE)

    const tiles = page.locator("[data-slot=stat-tile]")
    await expect(tiles).toHaveCount(5)
    await expect(tiles.nth(1)).toContainText("7.6 MB")
    await expect(tiles.nth(2)).toContainText("90.0%")
    await expect(tiles.nth(3)).toContainText("3")
    await expect(tiles.nth(4)).toContainText("9")
    // 600 commands between two readings three seconds apart: about 200 a second.
    await expect(tiles.nth(0)).toContainText(/^Commands a second(19\d|20\d|21\d)/, {
      timeout: 15_000,
    })
    await expect(page.getByRole("heading", { name: "Throughput" })).toBeVisible()
    await expect(page.getByText("since this page was opened")).toBeVisible()
    // The keyspace, database by database.
    await expect(page.getByRole("row").filter({ hasText: "db7" })).toContainText("41")
  })

  test("memory is analysed when asked, and says how much was measured", async ({ page }) => {
    const redis = await mockRedis(page)
    await page.goto(`${PERFORMANCE}?view=memory`)
    await expect(page.getByText("has not been analysed")).toBeVisible()
    expect(redis.asked("/redis/analysis")).toEqual([])

    await page.getByRole("button", { name: "Analyse memory" }).click()
    const report = page.locator("[data-slot=redis-analysis]")
    await expect(report).toContainText("10,000 of 2,000 keys")
    await expect(report).toContainText("scaled ×2.00 to the database")
    await expect(report.getByRole("img", { name: /Hash .* String/ })).toBeVisible()
    expect(redis.asked("/redis/analysis").at(-1)?.query.get("sample")).toBe("10000")

    // A namespace opens the keys under it; the report is still there on the way back.
    await report.getByRole("button", { name: "Open the keys of user" }).click()
    await expect(page).toHaveURL(/\/databases\/4\/data\?.*pattern=user%3A\*/)
    await page.goBack()
    await expect(page.locator("[data-slot=redis-analysis]")).toContainText("10,000 of 2,000 keys")
    expect(redis.asked("/redis/analysis").length).toBe(1)
  })

  test("emptying the slow log and disconnecting a client are confirmed, and neither is offered to a role that may not remove", async ({
    page,
  }) => {
    const redis = await mockRedis(page)
    await page.goto(`${PERFORMANCE}?view=slowlog`)
    await expect(page.getByRole("row").filter({ hasText: "KEYS" })).toContainText("25 ms")
    await page.getByRole("button", { name: "Empty the log" }).click()
    await expect(page.getByRole("dialog")).toContainText("cache")
    expect(redis.asked("/redis/slowlog/reset")).toEqual([])
    await page.getByRole("dialog").getByRole("button", { name: "Empty the slow log" }).click()
    await expect.poll(() => redis.asked("/redis/slowlog/reset").length).toBe(1)

    await page.getByRole("button", { name: /^Clients/ }).click()
    await expect(page).toHaveURL(/view=clients/)
    // The connection this request used is marked and cannot be cut.
    await expect(page.getByRole("row").filter({ hasText: "this dashboard" })).toBeVisible()
    await expect(page.getByRole("button", { name: /^Disconnect / })).toHaveCount(1)
    await page.getByRole("button", { name: "Disconnect worker" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Disconnect", exact: true }).click()
    await expect.poll(() => redis.asked("/redis/clients/kill").at(-1)?.body).toEqual({ id: 7 })
  })

  test("a role that may not remove is offered neither", async ({ page }) => {
    await mockRedis(page, { limited: true })
    await page.goto(`${PERFORMANCE}?view=slowlog`)
    await expect(page.getByRole("row").filter({ hasText: "KEYS" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Empty the log" })).toHaveCount(0)
    await page.getByRole("button", { name: /^Clients/ }).click()
    await expect(page.getByRole("row").filter({ hasText: "worker" })).toBeVisible()
    await expect(page.getByRole("button", { name: /^Disconnect / })).toHaveCount(0)
  })

  test("commands are ranked by the time they cost, and a save is asked of the server", async ({
    page,
  }) => {
    const redis = await mockRedis(page)
    await page.goto(`${PERFORMANCE}?view=commands`)
    const names = () =>
      page
        .getByRole("row")
        .locator("td:first-child > span:first-child")
        .evaluateAll((cells) => cells.map((cell) => cell.textContent))
    await expect.poll(names).toEqual(["SET", "GET", "KEYS"])
    await page.getByRole("button", { name: "Time per call" }).click()
    await expect.poll(names).toEqual(["KEYS", "SET", "GET"])

    await page.getByRole("button", { name: "Persistence & replication" }).click()
    await expect(page.getByText("after 1h if 1 key changed")).toBeVisible()
    await page.getByRole("button", { name: "Save now" }).click()
    await expect.poll(() => redis.asked("/redis/save").at(-1)?.body).toEqual({ mode: "bgsave" })
    await expect(page.getByRole("button", { name: "Rewrite now" })).toBeVisible()
  })

  test("a flavour without a feature draws no control for it", async ({ page }) => {
    await mockRedis(page, {
      summaries: {
        4: {
          flavor: "dragonfly",
          flavorLabel: "Dragonfly",
          versionNumber: "2.0.0",
          capabilities: { aofRewrite: false },
        },
      },
    })
    await page.goto(`${PERFORMANCE}?view=persistence`)
    await expect(page.getByRole("button", { name: "Save now" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Rewrite now" })).toHaveCount(0)
  })

  test("a server that answers nothing is an error with a way to try again", async ({ page }) => {
    const redis = await mockRedis(page, { failing: /^\/stats$/ })
    await page.goto(PERFORMANCE)
    // The framework keeps an empty alert of its own, for route changes.
    await expect(
      page.getByRole("alert").filter({ hasText: "the server did not answer" }),
    ).toBeVisible()
    redis.heal()
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(5)
  })
})

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

/** Fully rounded, filled labels. */
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

/** The design system's structural rules, held on what is on screen now. */
async function expectTheRules(page: Page, where: string) {
  await page.waitForLoadState("networkidle")
  expect(await unnamedControls(page), `unlabelled icon-only controls on ${where}`).toEqual([])
  expect(await offCentreText(page), `text off its row's centre line on ${where}`).toEqual([])
  expect(await filledPills(page), `fully rounded filled chips on ${where}`).toEqual([])
  const registers = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("[data-slot='page']")].map(
      (el) => el.dataset.register ?? "(unset)",
    ),
  )
  expect(registers, `${where} declares one register, and it is reading`).toEqual(["reading"])
  expect(await page.locator("[data-slot='flow-panel']").count(), `${where} has no flow panel`).toBe(
    0,
  )
  const sideways = await page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways, `${where} scrolls sideways`).toBe(false)
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the Redis pages keep the design system's rules${label}`, async ({ page }) => {
    test.setTimeout(120_000)
    await page.setViewportSize(viewport)
    await mockRedis(page, {
      features: { json: true },
      keys: {
        ...DEFAULT_KEYS,
        "doc:1": { type: "ReJSON-RL", json: '{"id":1,"tags":["a"]}' },
      },
      rows: { 4: { environment: "production" } },
    })

    // The keys, with none open and with one of each kind of editor.
    await page.goto(KEYS)
    await expect(rail(page)).toBeVisible()
    await expectTheRules(page, "the key browser")
    for (const key of [
      "session:1",
      "binary:blob",
      "user:1:profile",
      "queue",
      "user:1:roles",
      "leaderboard",
      "stream:orders",
      "doc:1",
      "expired:key",
    ]) {
      await page.goto(`${KEYS}?key=${encodeURIComponent(key)}`)
      await expect(page.locator("[data-slot=page]")).toBeVisible()
      await expectTheRules(page, `the key ${key}`)
    }

    // A dialog and a confirmation over the key browser.
    await page.goto(`${KEYS}?key=queue`)
    await pane(page).getByRole("button", { name: "Rename", exact: true }).click()
    await expect(page.getByRole("dialog")).toBeVisible()
    await expectTheRules(page, "the rename dialog")
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()
    await pane(page).getByRole("button", { name: "Delete key" }).click()
    await expect(page.getByRole("dialog")).toBeVisible()
    await expectTheRules(page, "the delete confirmation")

    for (const path of [CONSOLE, `${CONSOLE}?view=pubsub`, `${CONSOLE}?view=monitor`]) {
      await page.goto(path)
      await expect(page.locator("[data-slot=page]")).toBeVisible()
      await expectTheRules(page, path)
    }
    for (const view of ["", "memory", "slowlog", "clients", "commands", "persistence"]) {
      await page.goto(view ? `${PERFORMANCE}?view=${view}` : PERFORMANCE)
      await expect(page.locator("[data-slot=stat-tile]").first()).toBeVisible()
      await expectTheRules(page, `performance ${view || "overview"}`)
    }
  })
}
