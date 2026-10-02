import { expect, test, type Locator, type Page, type Route } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * MongoDB: documents behind a query bar, the aggregation builder with the
 * command console, schema analysis with indexes and validation, and the
 * server's performance — against a server mocked in the browser.
 *
 * The mock is a small database that answers the MongoDB routes the way their
 * contract writes them (documents as canonical Extended JSON text, a count
 * beside a page, a verdict before a command) and records every request. The
 * tests are the claims the pages make that a wrong page would silently
 * break: that the address decides which collection is on screen, that a
 * document saved is the document that was read, type for type, that nothing
 * is removed before it is counted and confirmed, that a refusal is never
 * shown as success, and that a control the role, the connection or the
 * server cannot honour is not drawn.
 */

const DATA = "/databases/5/data"
const QUERY = "/databases/5/query"
const SCHEMA = "/databases/5/schema"
const PERFORMANCE = "/databases/5/performance"
const DB = "app_main"

/** A 64-bit integer past 2^53: a JavaScript number would round its last digit. */
const BIG = "9007199254740993"

type Doc = {
  id: string
  canonical: string
  relaxed: Record<string, unknown>
  size: number
  digest: string
}

/** A document as the server sends one. Only `canonical` is read by the pages. */
function doc(canonical: string, digest = "a".repeat(64)): Doc {
  const id = /^\{"_id":(\{[^{}]*\}|"[^"]*"|[^,}]+)/.exec(canonical)?.[1] ?? ""
  return { id, canonical, relaxed: {}, size: canonical.length, digest }
}

const USERS = [
  doc(
    `{"_id":{"$oid":"6abe79972945ac11a3124bfc"},"zeta":"first after _id","email":"user1@example.com","age":{"$numberInt":"19"},"address":{"city":"Bucharest","zip":"10001"},"balance":{"$numberDecimal":"3.17"},"loginCount":{"$numberLong":"${BIG}"},"score":{"$numberDouble":"5.0"},"createdAt":{"$date":{"$numberLong":"1790864263204"}},"verified":true}`,
    "d".repeat(64),
  ),
  doc(
    '{"_id":{"$oid":"6abe79972945ac11a3124bfd"},"email":"user2@example.com","age":{"$numberInt":"20"},"address":{"city":"Berlin","zip":"10002"},"nickname":null}',
    "e".repeat(64),
  ),
]

const ORDERS = [
  doc('{"_id":{"$numberInt":"1"},"status":"paid","total":{"$numberDouble":"353.41"}}'),
  doc('{"_id":{"$numberInt":"2"},"status":"shipped","total":{"$numberDouble":"12.5"}}'),
  doc('{"_id":{"$numberInt":"3"},"status":"paid","total":{"$numberDouble":"99.0"}}'),
]

type FakeCollection = {
  type?: "collection" | "view" | "timeseries"
  capped?: boolean
  cappedSize?: number
  cappedMax?: number
  system?: boolean
  viewOn?: string
  pipeline?: string
  docs: Doc[]
  indexes?: Record<string, unknown>[]
  validator?: string
}

type Call = { method: string; path: string; query: URLSearchParams; body: Record<string, unknown> }

type MongoMock = DatabaseMock & {
  /** `service.control` without `destructive`: a role that may write and not remove. */
  limited?: boolean
  /** `read` alone: a role that may run nothing. */
  reader?: boolean
  /** What the server says to a find it refuses (a 400), by the request's body. */
  refusal?: (body: Record<string, unknown>) => string | undefined
  /** Finds the server cannot be reached for (a 502), by the request's body. */
  unreachable?: (body: Record<string, unknown>) => boolean
  /** Holds every find until it settles: a find that is still running. */
  findHeld?: Promise<void>
  /** Holds every classification until it settles. */
  classifyHeld?: Promise<void>
  /** A server that answers its status with no counters at all (one that only speaks the protocol). */
  silent?: boolean
  collections?: Record<string, FakeCollection>
  /** Paths that answer 502 until `heal()` is called. */
  failing?: RegExp
  /** A filter text the server refuses, with its words. */
  refused?: Record<string, string>
  /** What a find hands over as `hasMore`. */
  hasMore?: boolean
  /** What the next guarded replace answers instead of writing. */
  replace?: "changed" | "gone"
  /** How many documents an in-place update matches. */
  matched?: number
  /** What an insert refuses, by its place in the list. */
  insertErrors?: { index: number; code: number; message: string }[]
  /** What the export route answers with instead of a file. */
  exportRefused?: boolean
  topology?: "standalone" | "replicaset"
  profiler?: { level: 0 | 1 | 2; entries: unknown[] }
  operations?: unknown[]
  /** What `classify` says of a command, by its first key. */
  verdicts?: Record<string, Record<string, unknown>>
}

const INDEXES = [
  {
    name: "_id_",
    keys: [{ field: "_id", type: "asc" }],
    key: '{"_id":1}',
    kind: "regular",
    primary: true,
    unique: false,
    sparse: false,
    hidden: false,
    expireAfterSeconds: null,
    size: 32768,
    usage: { ops: 12, since: "2026-10-01T09:00:00Z" },
    building: false,
  },
  {
    name: "email_1",
    keys: [{ field: "email", type: "asc" }],
    key: '{"email":1}',
    kind: "regular",
    primary: false,
    unique: true,
    sparse: false,
    hidden: false,
    expireAfterSeconds: null,
    size: 53248,
    usage: { ops: 0, since: "2026-10-01T09:00:00Z" },
    building: false,
  },
]

const DEFAULT_COLLECTIONS: Record<string, FakeCollection> = {
  users: { docs: USERS, indexes: INDEXES },
  orders: { docs: ORDERS },
  sessions: { capped: true, docs: [] },
  active_users: { type: "view", viewOn: "users", docs: [USERS[0]] },
  "system.views": { system: true, docs: [] },
  products: {
    docs: [doc('{"_id":{"$numberInt":"1"},"sku":"A-1","price":{"$numberDouble":"9.5"}}')],
    validator: '{"$jsonSchema":{"required":["sku","price"]}}',
  },
}

const SERVER = (at: number, topology: "standalone" | "replicaset") => ({
  timestamp: 1_790_900_000_000 + at * 3000,
  host: "mongo-1",
  version: "8.0.4",
  process: "mongod",
  uptime: 69_000 + at * 3,
  storageEngine: "wiredTiger",
  topology,
  role: topology === "replicaset" ? "primary" : "standalone",
  setName: topology === "replicaset" ? "rs0" : undefined,
  opcounters: {
    insert: 10,
    query: 100 + at * 30,
    update: 5,
    delete: 1,
    getmore: 0,
    command: 500 + at * 60,
  },
  connections: { current: 12, available: 388, active: 2, totalCreated: 900 },
  network: { bytesIn: 1000 + at * 3000, bytesOut: 5000 + at * 9000, numRequests: 700 },
  memory: { resident: 291, virtual: 906 },
  cache: {
    bytes: 64 << 20,
    maxBytes: 256 << 20,
    dirtyBytes: 1 << 20,
    pagesRead: 1,
    pagesWritten: 1,
  },
  documents: { inserted: 10, returned: 400 + at * 30, updated: 5, deleted: 1 },
  scanned: { keys: 10, documents: 1200 + at * 90 },
  queue: { activeReaders: 0, activeWriters: 0, queuedReaders: 0, queuedWriters: 0 },
  latency: {
    readsMicros: 1000 + at * 3000,
    readsOps: 10 + at * 6,
    writesMicros: 500,
    writesOps: 5,
    commandsMicros: 800,
    commandsOps: 40,
  },
  cursors: { open: 1, timedOut: 0 },
  asserts: {},
})

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const refuse = (route: Route, status: number, code: string, message: string) =>
  json(route, { error: { code, message } }, status)

/** The documents a filter matches. The mock reads the two shapes the tests use: none, and one equality. */
function matching(docs: Doc[], filter: unknown): Doc[] {
  const text = typeof filter === "string" ? filter.trim() : ""
  if (!text || /^\{\s*\}$/.test(text)) return docs
  const equal = /^\{\s*"?([\w.]+)"?\s*:\s*("[^"]*"|\d+)\s*\}$/.exec(text)
  if (!equal) return docs
  const value = equal[2].startsWith('"') ? equal[2] : `{"$numberInt":"${equal[2]}"}`
  return docs.filter((entry) => entry.canonical.includes(`"${equal[1].split(".").pop()}":${value}`))
}

/**
 * Routes the MongoDB API of connection 5 over the section's fixture. Returns
 * the collections (the tests read them back to see what a write did) and
 * every request made, in order.
 */
async function mockMongo(page: Page, options: MongoMock = {}) {
  const collections: Record<string, FakeCollection> = structuredClone(
    options.collections ?? DEFAULT_COLLECTIONS,
  )
  const calls: Call[] = []
  const state = {
    failing: options.failing as RegExp | undefined,
    samples: 0,
    replace: options.replace,
    level: options.profiler?.level ?? 0,
    slowMs: 100,
  }

  await mockDatabases(page, options)
  if (options.limited || options.reader) {
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: options.reader ? ["read"] : ["read", "service.control"],
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

  const listed = () =>
    Object.entries(collections)
      .map(([name, entry]) => ({
        name,
        type: entry.type ?? "collection",
        system: Boolean(entry.system),
        readOnly: entry.type === "view",
        statsKnown: entry.type !== "view",
        count: entry.docs.length,
        size: entry.docs.reduce((sum, held) => sum + held.size, 0),
        avgObjSize: 100,
        storageSize: 4096,
        indexCount: entry.indexes?.length ?? 1,
        indexSize: 20480,
        capped: Boolean(entry.capped),
        cappedSize: entry.capped ? (entry.cappedSize ?? 1 << 20) : undefined,
        cappedMax: entry.cappedMax,
        clustered: false,
        viewOn: entry.viewOn,
        pipeline: entry.pipeline,
        validator: entry.validator,
      }))
      .sort((a, b) => a.name.localeCompare(b.name))

  // Registered after the fixture's catch-all, so it is consulted first; what
  // it does not know falls back to the fixture.
  await page.route("**/api/v1/databases/5/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1\/databases\/5/, "")
    const method = request.method()
    const query = url.searchParams
    const posted = request.postData()
    const body: Record<string, unknown> =
      posted && request.headers()["content-type"]?.includes("json") ? JSON.parse(posted) : {}
    calls.push({ method, path, query, body })
    if (state.failing?.test(path)) {
      return refuse(route, 502, "connect_failed", "the server did not answer")
    }
    const named = String(body.collection ?? query.get("collection") ?? "")
    const held = collections[named]
    const key = `${method} ${path}`

    switch (key) {
      case "GET /mongo/databases":
        return json(route, {
          databases: [
            {
              name: DB,
              collections: 5,
              views: 1,
              objects: 6,
              avgObjSize: 100,
              dataSize: 4096,
              storageSize: 8192,
              indexes: 6,
              indexSize: 4096,
              sizeOnDisk: 16384,
              empty: false,
              statsKnown: true,
            },
            {
              name: "reporting",
              collections: 2,
              views: 0,
              objects: 40,
              avgObjSize: 100,
              dataSize: 9000,
              storageSize: 8192,
              indexes: 2,
              indexSize: 4096,
              sizeOnDisk: 16384,
              empty: false,
              statsKnown: true,
            },
          ],
        })
      case "GET /mongo/collections": {
        const database = query.get("database") ?? DB
        return json(route, {
          database,
          collections:
            database === DB
              ? listed()
              : database === "admin"
                ? listed()
                    .slice(0, 1)
                    .map((entry) => ({ ...entry, name: "system.version", system: true }))
                : [],
          statsTruncated: false,
        })
      }
      case "PATCH /mongo/collections":
        if (typeof body.cappedSize === "number") held.cappedSize = body.cappedSize
        if (typeof body.cappedMax === "number") held.cappedMax = body.cappedMax
        if (typeof body.viewOn === "string") held.viewOn = body.viewOn
        if (typeof body.pipeline === "string") held.pipeline = body.pipeline
        return json(route, { ok: true })
      case "POST /mongo/collections":
        collections[named] = { docs: [], capped: Boolean(body.capped) }
        return json(route, { ok: true })
      case "POST /mongo/collections/rename":
        collections[String(body.to)] = held
        delete collections[named]
        return json(route, { ok: true })
      case "DELETE /mongo/collections":
        delete collections[named]
        return json(route, { ok: true })

      case "POST /mongo/find": {
        await options.findHeld
        const refusal = options.refused?.[String(body.filter ?? "")] ?? options.refusal?.(body)
        if (refusal) return refuse(route, 400, "bad_request", refusal)
        if (options.unreachable?.(body)) {
          return refuse(route, 502, "connect_failed", "the server could not be reached")
        }
        const found = matching(held?.docs ?? [], body.filter)
        const skip = Number(body.skip ?? 0)
        const limit = Number(body.limit ?? 50)
        const page_ = found.slice(skip, skip + limit)
        return json(route, {
          documents: page_,
          returned: page_.length,
          skip,
          limit,
          hasMore: options.hasMore ?? skip + limit < found.length,
          truncated: false,
          count: null,
          durationMs: 2,
          statement: `db.${named}.find({})`,
        })
      }
      case "POST /mongo/count":
        return json(route, {
          count: {
            value: matching(held?.docs ?? [], body.filter).length,
            exact: true,
            scope: "filter",
          },
        })
      case "POST /mongo/document": {
        const found = held?.docs.find((entry) => entry.id === body.id)
        return found
          ? json(route, { document: found })
          : refuse(route, 404, "document_not_found", "no document has that _id")
      }
      case "PUT /mongo/documents": {
        if (state.replace === "changed") {
          return refuse(
            route,
            409,
            "document_changed",
            "the stored document is no longer the one read",
          )
        }
        if (state.replace === "gone") {
          return refuse(route, 404, "document_not_found", "no document has that _id")
        }
        const at = held.docs.findIndex((entry) => entry.id === body.id)
        held.docs[at] = { ...held.docs[at], canonical: String(body.document) }
        return json(route, { dryRun: false, matched: 1, modified: 1, deleted: 0 })
      }
      case "PATCH /mongo/documents": {
        const matched = options.matched ?? matching(held.docs, body.filter).length
        return json(route, {
          dryRun: Boolean(body.dryRun),
          matched: body.many || body.dryRun ? matched : Math.min(matched, 1),
          modified: body.dryRun ? 0 : Math.min(matched, body.many ? matched : 1),
          deleted: 0,
        })
      }
      case "DELETE /mongo/documents": {
        const doomed = body.id
          ? held.docs.filter((entry) => entry.id === body.id)
          : matching(held.docs, body.filter)
        if (!body.dryRun) held.docs = held.docs.filter((entry) => !doomed.includes(entry))
        return json(route, {
          dryRun: Boolean(body.dryRun),
          matched: doomed.length,
          modified: 0,
          deleted: body.dryRun ? 0 : doomed.length,
        })
      }
      case "POST /mongo/documents": {
        const errors = options.insertErrors ?? []
        const inserted = String(body.documents).trimStart().startsWith("[") ? 3 - errors.length : 1
        held.docs.push(doc('{"_id":{"$numberInt":"77"},"new":true}'))
        return json(route, { inserted, insertedIds: ['{"$numberInt":"77"}'], errors })
      }
      case "POST /mongo/documents/clone": {
        const copy = doc('{"_id":{"$oid":"6abe79972945ac11a3124c00"},"email":"user1@example.com"}')
        held.docs.push(copy)
        return json(route, { document: copy })
      }

      case "GET /mongo/indexes":
        return json(route, { indexes: held?.indexes ?? [INDEXES[0]] })
      case "POST /mongo/indexes":
        held.indexes = [...(held.indexes ?? [INDEXES[0]]), { ...INDEXES[1], name: "made_1" }]
        return json(route, { name: "made_1" })
      case "PATCH /mongo/indexes":
        held.indexes = held.indexes?.map((index) =>
          index.name === body.name
            ? {
                ...index,
                hidden: body.hidden ?? index.hidden,
                expireAfterSeconds: body.expireAfterSeconds ?? index.expireAfterSeconds,
              }
            : index,
        )
        return json(route, { ok: true })
      case "DELETE /mongo/indexes":
        held.indexes = held.indexes?.filter((index) => index.name !== body.name)
        return json(route, { ok: true })

      case "GET /mongo/validation":
        return json(route, {
          validator: held?.validator ?? "",
          validatorRelaxed: held?.validator ?? "",
          level: "strict",
          action: "error",
        })
      case "PUT /mongo/validation":
        if (typeof body.validator === "string") held.validator = body.validator
        return json(route, {
          validator: held.validator ?? "",
          validatorRelaxed: held.validator ?? "",
          level: body.level ?? "strict",
          action: body.action ?? "error",
        })
      case "POST /mongo/validation/check":
        return json(route, {
          proposed: body.validator !== undefined,
          failing: 2,
          exact: true,
          total: 300,
          samples: [held.docs[0]],
          durationMs: 4,
        })

      case "POST /mongo/aggregate/preview": {
        const text = String(body.pipeline)
        const out = text.includes('"$out"')
        if (text.includes("$collStats")) {
          // What the server has counted on a collection since it started.
          const reads = named === "users" ? 620 : 40
          return json(route, {
            documents: options.silent
              ? []
              : [
                  doc(
                    `{"ns":"${DB}.${named}","localTime":{"$date":{"$numberLong":"1790900000000"}},"latencyStats":{"reads":{"latency":{"$numberLong":"9000"},"ops":{"$numberLong":"${reads}"}},"writes":{"latency":{"$numberLong":"500"},"ops":{"$numberLong":"10"}},"commands":{"latency":{"$numberLong":"1"},"ops":{"$numberLong":"1"}}}}`,
                  ),
                ],
            returned: options.silent ? 0 : 1,
            stage: 0,
            stages: 1,
            inputLimited: false,
            inputLimit: 0,
            durationMs: 1,
          })
        }
        if (text.includes("$nope")) {
          return refuse(route, 400, "bad_request", "pipeline: $nope is not one a preview can run")
        }
        return json(route, {
          documents: held?.docs.slice(0, 2) ?? [],
          returned: Math.min(held?.docs.length ?? 0, 2),
          stage: body.stage,
          stages: Number(body.stage) + 1,
          inputLimited: text.includes('"$group"'),
          inputLimit: 100_000,
          writeStage: out ? "$out" : undefined,
          durationMs: 3,
        })
      }
      case "POST /aggregate":
        return json(route, {
          result: { columns: [], rows: [] },
          writes: String(body.pipeline).includes('"$out"'),
          pipeline: { stages: [], writes: false, unknown: [] },
          documents: held?.docs ?? [],
          returned: held?.docs.length ?? 0,
          hasMore: true,
          truncated: false,
          durationMs: 6,
          statement: "",
        })
      case "POST /mongo/explain":
        return json(route, {
          summary: {
            namespace: `${DB}.${named}`,
            verbosity: body.verbosity,
            executed: body.verbosity !== "queryPlanner",
            engine: "classic",
            returned: body.verbosity === "queryPlanner" ? null : 2,
            keysExamined: body.verbosity === "queryPlanner" ? null : 0,
            docsExamined: body.verbosity === "queryPlanner" ? null : 1500,
            timeMs: body.verbosity === "queryPlanner" ? null : 5,
            indexesUsed: [],
            collectionScan: true,
            inMemorySort: true,
            usedDisk: false,
            rejectedPlans: 0,
          },
          plan: {
            stage: "SORT",
            returned: 2,
            docsExamined: null,
            keysExamined: null,
            timeMs: 1,
            children: [
              {
                stage: "COLLSCAN",
                direction: "forward",
                returned: 2,
                docsExamined: 1500,
                keysExamined: null,
                timeMs: 4,
                children: [],
              },
            ],
          },
          raw: '{"explainVersion":"1"}',
        })
      case "POST /mongo/schema":
        return json(route, {
          database: DB,
          collection: named,
          sampled: 200,
          requested: body.sample,
          total: 1500,
          truncated: false,
          durationMs: 9,
          fields: [
            {
              path: "_id",
              name: "_id",
              depth: 0,
              documents: 200,
              presence: 1,
              occurrences: 200,
              types: [{ type: "objectId", count: 200, share: 1 }],
              indexed: true,
              indexes: ["_id_"],
            },
            {
              path: "address",
              name: "address",
              depth: 0,
              documents: 200,
              presence: 1,
              occurrences: 200,
              types: [{ type: "object", count: 200, share: 1 }],
              indexed: false,
              indexes: [],
            },
            {
              path: "address.city",
              name: "city",
              depth: 1,
              documents: 200,
              presence: 1,
              occurrences: 200,
              types: [
                {
                  type: "string",
                  count: 200,
                  share: 1,
                  minLength: 6,
                  maxLength: 9,
                  avgLength: 7,
                  top: [
                    { value: "Berlin", count: 120 },
                    { value: "Bucharest", count: 80 },
                  ],
                  distinct: 2,
                },
              ],
              indexed: false,
              indexes: [],
            },
            {
              path: "legacyId",
              name: "legacyId",
              depth: 0,
              documents: 20,
              presence: 0.1,
              occurrences: 20,
              types: [
                { type: "int", count: 15, share: 0.75, min: "1", max: "90", avg: 40 },
                {
                  type: "string",
                  count: 5,
                  share: 0.25,
                  minLength: 1,
                  maxLength: 2,
                  avgLength: 1.5,
                },
              ],
              indexed: false,
              indexes: [],
            },
          ],
        })

      case "GET /mongo/export":
        if (options.exportRefused) {
          return refuse(route, 400, "bad_request", "filter: line 1, column 3: not a document")
        }
        return route.fulfill({
          status: 200,
          contentType: "application/x-ndjson",
          headers: {
            "Content-Disposition": `attachment; filename="${named}.ndjson"`,
            "X-Export-Truncated": "false",
          },
          body: `${held.docs.map((entry) => entry.canonical).join("\n")}\n`,
        })

      case "GET /mongo/server": {
        const server = SERVER(state.samples++, options.topology ?? "standalone")
        return json(
          route,
          options.silent
            ? {
                ...server,
                process: "ferretdb",
                storageEngine: "",
                opcounters: {},
                connections: { current: 0, available: 0, active: 0, totalCreated: 0 },
                network: { bytesIn: 0, bytesOut: 0, numRequests: 0 },
                cache: null,
              }
            : server,
        )
      }
      case "GET /mongo/ops":
        return json(route, { operations: options.operations ?? [] })
      case "POST /mongo/killop":
        return json(route, { ok: true })
      case "GET /mongo/profiler":
        return json(route, {
          database: query.get("database") ?? DB,
          level: state.level,
          slowMs: state.slowMs,
          sampleRate: 1,
          entries: state.level > 0 ? (options.profiler?.entries ?? []) : [],
        })
      case "PUT /mongo/profiler":
        if (typeof body.level === "number") state.level = body.level as 0 | 1 | 2
        if (typeof body.slowMs === "number") state.slowMs = body.slowMs
        return json(route, {
          database: DB,
          level: state.level,
          slowMs: state.slowMs,
          sampleRate: 1,
        })
      case "GET /mongo/replication":
        return json(route, {
          replicaSet: true,
          setName: "rs0",
          myState: "PRIMARY",
          members: [
            {
              id: 0,
              name: "mongo-1:27017",
              state: "PRIMARY",
              health: true,
              self: true,
              uptime: 69000,
              lagSeconds: 0,
              pingMs: 0,
            },
            {
              id: 1,
              name: "mongo-2:27017",
              state: "SECONDARY",
              health: true,
              self: false,
              uptime: 3600,
              lagSeconds: 42,
              pingMs: 3,
              syncSource: "mongo-1:27017",
            },
          ],
          oplog: { sizeBytes: 1 << 30, usedBytes: 1 << 28, windowSeconds: 86_400 },
        })

      case "GET /mongo/commands":
        return json(route, {
          commands: [
            { command: "collStats", class: "read", admin: false, known: true },
            {
              command: "drop",
              class: "destructive",
              admin: false,
              known: true,
              reason: "it removes a collection and everything in it",
            },
            {
              command: "shutdown",
              class: "blocked",
              admin: false,
              known: true,
              reason: "it stops the server",
            },
          ],
        })
      case "POST /mongo/command/classify": {
        await options.classifyHeld
        const first = /^\s*\{\s*"?(\w+)/.exec(String(body.command))?.[1] ?? ""
        const verdict = {
          command: first,
          class: "read",
          admin: false,
          known: true,
          ...options.verdicts?.[first],
        }
        return json(route, {
          verdict,
          requires:
            verdict.class === "destructive"
              ? ["service.control", "destructive"]
              : ["service.control"],
          allowed:
            verdict.class !== "blocked" && !(verdict.class === "destructive" && options.limited),
          database: body.database ?? DB,
        })
      }
      case "POST /mongo/command":
        return json(route, {
          verdict: { command: "ping", class: "read", admin: false, known: true },
          database: body.database ?? DB,
          reply: {
            canonical: '{"ok":{"$numberDouble":"1.0"}}',
            relaxed: { ok: 1 },
            size: 17,
            more: false,
            durationMs: 1,
          },
        })
    }
    return route.fallback()
  })

  return {
    collections,
    calls,
    asked: (key: string) => calls.filter((call) => `${call.method} ${call.path}` === key),
    /** The paths that were failing answer again. */
    heal: () => {
      state.failing = undefined
    },
    /** The next guarded replace is written. */
    settle: () => {
      state.replace = undefined
    },
  }
}

/** Something a test lets go of when it chooses: a response that has not come yet. */
function hold() {
  let release = () => {}
  const until = new Promise<void>((resolve) => {
    release = resolve
  })
  return { until, release }
}

/**
 * Presses one of a field's own controls. They are drawn on the row the
 * pointer is over (or the keyboard is on), and on no other.
 */
async function fieldAction(scope: Locator, field: string, action: string) {
  await scope.getByRole("treeitem", { name: new RegExp(`^${field}:`) }).hover()
  await scope.getByRole("button", { name: `${action} ${field}` }).click()
}

const rail = (page: Page) => page.locator("[data-slot=collection-rail]")
const documents = (page: Page) => page.locator("[data-slot=mongo-document]")
const where = (page: Page) => decodeURIComponent(new URL(page.url()).search.replace(/\+/g, " "))
const collection = (name: string, more = "") => `${DATA}?db=${DB}&collection=${name}${more}`

/** Puts text into the code editor on screen, as a paste would. */
async function type(page: Page, text: string) {
  await expect(page.locator(".monaco-editor").last()).toBeVisible({ timeout: 15_000 })
  await page.evaluate((value) => {
    const monaco = (
      window as unknown as { monaco: { editor: { getEditors(): { setValue(v: string): void }[] } } }
    ).monaco
    monaco.editor.getEditors().at(-1)?.setValue(value)
  }, text)
}

/** What the code editor holds once it has loaded its document. */
async function opened(page: Page) {
  await expect(page.locator(".monaco-editor").last()).toBeVisible({ timeout: 15_000 })
  await expect.poll(() => typed(page)).not.toBe("")
  return typed(page)
}

/** What the code editor on screen holds. */
function typed(page: Page) {
  return page.evaluate(() => {
    const monaco = (
      window as unknown as { monaco: { editor: { getEditors(): { getValue(): string }[] } } }
    ).monaco
    return monaco.editor.getEditors().at(-1)?.getValue() ?? ""
  })
}

test.describe("documents", () => {
  // M13: the old browser kept its collection in the tab's memory and ignored
  // the address, so a link to one collection opened whichever was last used.
  test("the address decides which collection is on screen, and a bare one opens none", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("orders"))
    await expect(rail(page).getByRole("link", { name: /orders/ })).toHaveAttribute(
      "aria-current",
      "page",
    )
    await expect(documents(page)).toHaveCount(3)
    expect(mongo.asked("POST /mongo/find").at(-1)?.body).toMatchObject({
      database: DB,
      collection: "orders",
    })

    // Choosing another is a step of history: Back is the one before.
    await rail(page).getByRole("link", { name: /users/ }).first().click()
    await expect(documents(page)).toHaveCount(2)
    expect(where(page)).toContain("collection=users")
    await page.goBack()
    await expect(documents(page)).toHaveCount(3)

    // No collection named: what the database holds, and no documents read for it.
    await page.goto(`${DATA}?db=${DB}`)
    await expect(page.locator("[data-slot=mongo-database]")).toContainText("By the data they hold")
    await expect(page.locator("[data-slot=mongo-documents]")).toHaveCount(0)
  })

  test("the rail lists collections by kind with their counts, and folds the server's own away", async ({
    page,
  }) => {
    await mockMongo(page)
    await page.goto(`${DATA}?db=${DB}`)
    await expect(
      rail(page).getByRole("region", { name: "Collections", exact: true }),
    ).toContainText("users")
    await expect(rail(page).getByRole("region", { name: "Capped" })).toContainText("sessions")
    await expect(rail(page).getByRole("region", { name: "Views" })).toContainText("active_users")
    await expect(rail(page).getByText("system.views")).toHaveCount(0)
    await rail(page)
      .getByRole("button", { name: /The server.s own/ })
      .click()
    await expect(rail(page).getByText("system.views")).toBeVisible()
    // A collection the address names and the database lacks is said, not drawn as empty.
    await page.goto(collection("nope"))
    await expect(page.getByText("No collection called nope")).toBeVisible()
  })

  // C2: the old editor loaded a grid row through JSON.parse and saved it back,
  // which rewrote ObjectIds, dates and 64-bit integers and re-ordered the fields.
  test("a document is edited as the text it is stored as, and saved only if it is still that document", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("users"))
    await documents(page).first().getByRole("button", { name: "Edit", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Edit document" })
    await expect(dialog).toBeVisible()
    const text = await opened(page)
    // Every type as it was stored, the fields in their stored order.
    expect(text).toContain(`"loginCount": {"$numberLong":"${BIG}"}`)
    expect(text).toContain('"score": {"$numberDouble":"5.0"}')
    expect(text).toContain('"balance": {"$numberDecimal":"3.17"}')
    expect(text.indexOf('"zeta"')).toBeLessThan(text.indexOf('"email"'))
    // Opened and untouched, there is nothing to save.
    await expect(dialog.getByRole("button", { name: "Save document" })).toBeDisabled()

    await type(page, text.replace("Bucharest", "Iasi"))
    await dialog.getByRole("button", { name: "Save document" }).click()
    await expect(dialog).toBeHidden()
    const put = mongo.asked("PUT /mongo/documents").at(-1)?.body
    expect(put).toMatchObject({
      collection: "users",
      id: '{"$oid":"6abe79972945ac11a3124bfc"}',
      // The guard: the digest of the document as it was read.
      expectedDigest: "d".repeat(64),
    })
    expect(String(put?.document)).toContain(`{"$numberLong":"${BIG}"}`)
    expect(String(put?.document)).toContain('"city": "Iasi"')
  })

  test("a document somebody else changed is not written over: it is said, and the reader chooses", async ({
    page,
  }) => {
    const mongo = await mockMongo(page, { replace: "changed" })
    await page.goto(collection("users"))
    await documents(page).first().getByRole("button", { name: "Edit", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Edit document" })
    const text = await opened(page)
    await type(page, text.replace("Bucharest", "Iasi"))
    await dialog.getByRole("button", { name: "Save document" }).click()
    await expect(dialog.getByText("This document changed after it was opened")).toBeVisible()
    // Nothing was reported as saved, and the editor still holds what was typed.
    expect(await typed(page)).toContain("Iasi")

    // Saving over it is the reader's own decision, and is sent without the guard.
    mongo.settle()
    await dialog.getByRole("button", { name: "Save mine over it" }).click()
    await expect(dialog).toBeHidden()
    expect(mongo.asked("PUT /mongo/documents").at(-1)?.body.expectedDigest).toBeUndefined()
  })

  // M18: Escape and a press outside closed the old editor with what was typed.
  test("closing the editor with changes in it asks first", async ({ page }) => {
    await mockMongo(page)
    await page.goto(collection("users"))
    await documents(page).first().getByRole("button", { name: "Edit", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Edit document" })
    await type(page, '{ "_id": {"$oid":"6abe79972945ac11a3124bfc"}, "typed": true }')
    await dialog.getByRole("button", { name: "Cancel" }).click()
    await expect(dialog.getByText("Close without saving what you typed?")).toBeVisible()
    await dialog.getByRole("button", { name: "Keep editing" }).click()
    expect(await typed(page)).toContain('"typed": true')
    await page.keyboard.press("Escape")
    await expect(dialog.getByText("Close without saving what you typed?")).toBeVisible()
    await dialog.getByRole("button", { name: "Discard" }).click()
    await expect(dialog).toBeHidden()
  })

  test("a value is drawn with its type, and edited in place it is sent as the update it is", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("users"))
    const first = documents(page).first()
    // A 64-bit integer keeps every digit and says what it is.
    await expect(first.locator("[data-type=long]")).toHaveText(`Long("${BIG}")`)
    await expect(first.locator("[data-type=objectId]")).toHaveText(
      'ObjectId("6abe79972945ac11a3124bfc")',
    )
    await expect(first.locator("[data-type=date]")).toHaveText(
      'ISODate("2026-10-01T14:17:43.204Z")',
    )
    await expect(first.locator("[data-type=double]")).toHaveText("5.0")

    await fieldAction(first, "loginCount", "Edit")
    // The editor opens on the value's own type: an Int64 stays one.
    await expect(first.getByRole("combobox", { name: "Type of loginCount" })).toHaveText("Int64")
    await first.getByLabel("Value of loginCount").fill("4")
    await page.keyboard.press("Enter")
    await fieldAction(first, "verified", "Remove")
    const pending = first.locator("[data-slot=mongo-pending]")
    await expect(pending).toContainText("1 changed, 1 removed")
    await expect(pending).toContainText('$set: { loginCount: Long("4") }')
    // Nothing is sent until the reader sends it.
    expect(mongo.asked("PATCH /mongo/documents")).toHaveLength(0)

    await pending.getByRole("button", { name: "Update" }).click()
    await expect(pending).toBeHidden()
    expect(mongo.asked("PATCH /mongo/documents").at(-1)?.body).toEqual({
      database: DB,
      collection: "users",
      // Held to what was read: the _id, and the old value of each field touched.
      filter: `{"_id":{"$oid":"6abe79972945ac11a3124bfc"},"loginCount":{"$numberLong":"${BIG}"},"verified":true}`,
      update: '{"$set":{"loginCount":{"$numberLong":"4"}},"$unset":{"verified":""}}',
      many: false,
    })
  })

  // H1: the old page toasted success whatever came back, including "0 matched".
  test("an update that matched nothing is not reported as done", async ({ page }) => {
    await mockMongo(page, { matched: 0 })
    await page.goto(collection("users"))
    const first = documents(page).first()
    await fieldAction(first, "age", "Edit")
    await first.getByLabel("Value of age").fill("21")
    await page.keyboard.press("Enter")
    await first.getByRole("button", { name: "Update" }).click()
    await expect(first.getByRole("alert")).toContainText("Nothing was written")
    // The edit is still staged: the reader has not lost it.
    await expect(first.locator("[data-slot=mongo-pending]")).toContainText("1 changed")
  })

  test("a value that does not fit its type is refused where it is typed", async ({ page }) => {
    await mockMongo(page)
    await page.goto(collection("users"))
    const first = documents(page).first()
    await fieldAction(first, "age", "Edit")
    await first.getByLabel("Value of age").fill("2147483648")
    await page.keyboard.press("Enter")
    await expect(first.getByRole("alert")).toContainText("Outside what an Int32 holds")
    await expect(first.locator("[data-slot=mongo-pending]")).toHaveCount(0)
    // _id is not a field an update can change.
    await expect(first.getByRole("button", { name: "Edit _id" })).toHaveCount(0)
  })

  test("the query bar checks what it can as it is typed, and the server's refusal lands on its field", async ({
    page,
  }) => {
    const mongo = await mockMongo(page, {
      refused: { "{ age: { $gtx: 30 } }": "(BadValue) unknown operator: $gtx" },
    })
    await page.goto(collection("users"))
    await expect(documents(page)).toHaveCount(2)
    const sent = () => mongo.asked("POST /mongo/find").length

    const filter = page.getByLabel("Filter", { exact: true })
    await filter.fill("{ age: { $gt: 30 }")
    await expect(page.locator("#mongo-query-filter-problem")).toContainText("never closed")
    await expect(page.getByRole("button", { name: "Find", exact: true })).toBeDisabled()
    const before = sent()
    await filter.press("Enter")
    expect(sent()).toBe(before)

    // The shell's spelling is the server's to read: it is sent as typed.
    await filter.fill("{ age: { $gtx: 30 } }")
    await filter.press("Enter")
    await expect(page.locator("#mongo-query-filter-problem")).toContainText(
      "unknown operator: $gtx",
    )
    expect(mongo.asked("POST /mongo/find").at(-1)?.body.filter).toBe("{ age: { $gtx: 30 } }")
    // What was on screen before the refusal is still there.
    await expect(documents(page)).toHaveCount(2)
  })

  test("a query is in the address, with its page, and Reset takes it out", async ({ page }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("orders"))
    await page.getByLabel("Filter", { exact: true }).fill('{ "status": "paid" }')
    await page.getByRole("button", { name: "Options" }).click()
    await page.getByLabel("Sort").fill("{ total: -1 }")
    await page.getByLabel("Limit").fill("10")
    await page.getByRole("button", { name: "Find", exact: true }).click()
    await expect(documents(page)).toHaveCount(2)
    await expect.poll(() => where(page)).toContain('filter={ "status": "paid" }')
    await expect.poll(() => where(page)).toContain("sort={ total: -1 }")
    expect(mongo.asked("POST /mongo/find").at(-1)?.body).toMatchObject({
      filter: '{ "status": "paid" }',
      sort: "{ total: -1 }",
      // The reader's limit bounds the query; the page asks for no more than that.
      limit: 10,
      skip: 0,
    })
    await expect(page.locator("[data-slot=mongo-documents]")).toContainText("1–2 of 2")

    // A pasted link opens on the same query.
    await page.goto(
      `${collection("orders")}&filter=${encodeURIComponent('{ "status": "shipped" }')}`,
    )
    await expect(page.getByLabel("Filter", { exact: true })).toHaveValue('{ "status": "shipped" }')
    await expect(documents(page)).toHaveCount(1)

    await page.getByRole("button", { name: "Reset" }).click()
    await expect(documents(page)).toHaveCount(3)
    await expect.poll(() => where(page)).not.toContain("filter=")
    // The query that was run is kept for the tab.
    await page.getByRole("button", { name: "Earlier queries" }).click()
    await expect(page.getByRole("menuitem").first()).toContainText("status")
  })

  // M1: the old pager enabled Next whenever a page was full, and called the
  // page past the last one an empty collection.
  test("Next follows the server's word, and a page past the end says so", async ({ page }) => {
    await mockMongo(page, { hasMore: false })
    await page.goto(collection("orders"))
    await expect(documents(page)).toHaveCount(3)
    await expect(page.getByRole("button", { name: "Next page" })).toBeDisabled()
    await expect(page.getByRole("button", { name: "Previous page" })).toBeDisabled()

    await page.goto(collection("orders", "&page=9"))
    await expect(page.getByText("No documents on this page")).toBeVisible()
    await page.getByRole("button", { name: "Go to the first page" }).click()
    await expect(documents(page)).toHaveCount(3)
  })

  test("the same documents read three ways", async ({ page }) => {
    await mockMongo(page)
    await page.goto(collection("users"))
    await page.getByRole("button", { name: "JSON", exact: true }).click()
    await expect(documents(page).first()).toContainText(`"loginCount": {"$numberLong":"${BIG}"}`)
    await expect.poll(() => where(page)).toContain("view=json")

    await page.getByRole("button", { name: "Table", exact: true }).click()
    const grid = page.getByRole("grid", { name: "users documents" })
    await expect(grid).toBeVisible()
    // The union of the top-level fields, `_id` first, then in the order met:
    // ten of the first document and `nickname`, which only the second has.
    for (const [at, name] of ["_id", "zeta", "email"].entries()) {
      // The first column of the grid is its row numbers.
      await expect(grid.locator(`[role=columnheader][aria-colindex="${at + 2}"]`)).toContainText(
        name,
      )
    }
    // (The grid counts its row-number column too.)
    await expect(grid).toHaveAttribute("aria-colcount", "12")
    await expect(grid).toHaveAttribute("aria-rowcount", "3")
  })

  // H3: Delete was offered to a role the server then refused.
  test("a document is deleted only after it is named and confirmed, by a role that may remove", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("orders"))
    await documents(page).first().getByRole("button", { name: "More actions for 1" }).click()
    await page.getByRole("menuitem", { name: "Delete" }).click()
    const confirm = page.getByRole("dialog", { name: "Delete document" })
    await expect(confirm).toContainText(`${DB}.orders`)
    expect(mongo.asked("DELETE /mongo/documents")).toHaveLength(0)
    await confirm.getByRole("button", { name: "Delete document" }).click()
    await expect(documents(page)).toHaveCount(2)
    expect(mongo.asked("DELETE /mongo/documents").at(-1)?.body).toMatchObject({
      collection: "orders",
      id: '{"$numberInt":"1"}',
    })
  })

  test("a role that may write and not remove is offered no removal", async ({ page }) => {
    await mockMongo(page, { limited: true })
    await page.goto(collection("orders"))
    await documents(page).first().getByRole("button", { name: "More actions for 1" }).click()
    await expect(page.getByRole("menuitem", { name: "Add a field" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Delete" })).toHaveCount(0)
    await page.keyboard.press("Escape")
    await page.getByRole("button", { name: "More actions for orders" }).click()
    await expect(page.getByRole("menuitem", { name: "Update documents…" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Delete documents…" })).toHaveCount(0)
    await page.keyboard.press("Escape")
    await rail(page).getByRole("button", { name: "Actions for orders" }).click()
    await expect(page.getByRole("menuitem", { name: "Rename…" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Drop…" })).toHaveCount(0)
  })

  test("a reader's role and a protected connection are shown no control that writes", async ({
    page,
  }) => {
    for (const options of [{ viewer: true }, { rows: { 5: { readOnly: true } } }]) {
      await mockMongo(page, options)
      await page.goto(collection("users"))
      await expect(documents(page)).toHaveCount(2)
      const main = page.locator("[data-slot=mongo-documents]")
      for (const name of ["Insert", "Import", "Edit", "Clone"]) {
        await expect(main.getByRole("button", { name, exact: true })).toHaveCount(0)
      }
      await expect(main.getByRole("button", { name: /^Edit /, includeHidden: true })).toHaveCount(0)
      await expect(rail(page).getByRole("button", { name: "New collection" })).toHaveCount(0)
      // The document can still be opened and copied.
      await expect(main.getByRole("button", { name: "Open", exact: true }).first()).toBeVisible()
      await expect(main.getByRole("button", { name: "Export" })).toBeVisible()
      await page.unrouteAll({ behavior: "ignoreErrors" })
    }
  })

  test("documents are removed in bulk only after they are counted, and the count is on the command", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${collection("orders")}&filter=${encodeURIComponent('{ "status": "paid" }')}`)
    await page.getByRole("button", { name: "More actions for orders" }).click()
    await page.getByRole("menuitem", { name: "Delete documents…" }).click()
    const dialog = page.getByRole("dialog", { name: "Delete documents" })
    // It opens on the filter the query bar holds.
    await expect(dialog.getByLabel("Filter")).toHaveValue('{ "status": "paid" }')
    await dialog.getByRole("button", { name: "Count the matches" }).click()
    await expect(dialog).toContainText("2 documents match")
    // Another filter is another count: the number on the button is never of the old one.
    await dialog.getByLabel("Filter").fill('{ "status": "shipped" }')
    await expect(dialog.getByRole("button", { name: "Count the matches" })).toBeVisible()
    await dialog.getByLabel("Filter").fill('{ "status": "paid" }')
    await dialog.getByRole("button", { name: /Delete 2 documents/ }).click()

    const confirm = page.getByRole("dialog", { name: "Delete 2 documents" })
    await expect(confirm).toContainText('{ "status": "paid" }')
    expect(mongo.asked("DELETE /mongo/documents").map((call) => call.body.dryRun)).toEqual([true])
    await confirm.getByRole("button", { name: "Delete 2 documents" }).click()
    await expect.poll(() => mongo.collections.orders.docs.length).toBe(1)
    expect(mongo.asked("DELETE /mongo/documents").at(-1)?.body).toMatchObject({
      filter: '{ "status": "paid" }',
      many: true,
    })
  })

  test("an update of many is counted by the server's own dry run before it is sent", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("orders"))
    await page.getByRole("button", { name: "More actions for orders" }).click()
    await page.getByRole("menuitem", { name: "Update documents…" }).click()
    const dialog = page.getByRole("dialog", { name: "Update documents" })
    await dialog.getByLabel("Filter").fill('{ "status": "paid" }')
    await dialog.getByLabel("Update").fill('{ "$set": { "seen": true } }')
    await dialog.getByRole("button", { name: "Count the matches" }).click()
    await dialog.getByRole("button", { name: "Update 2 documents" }).click()
    await expect(dialog).toBeHidden()
    expect(mongo.asked("PATCH /mongo/documents").map((call) => call.body.dryRun)).toEqual([
      true,
      undefined,
    ])
    expect(mongo.asked("PATCH /mongo/documents").at(-1)?.body).toMatchObject({
      filter: '{ "status": "paid" }',
      update: '{ "$set": { "seen": true } }',
      many: true,
    })
  })

  test("an insert that lands in part says how many are in and which were refused", async ({
    page,
  }) => {
    const mongo = await mockMongo(page, {
      insertErrors: [{ index: 1, code: 11000, message: "E11000 duplicate key error" }],
    })
    await page.goto(collection("orders"))
    await page.getByRole("button", { name: "Insert", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Insert documents" })
    // The starting text is an empty document: it is not what Insert means.
    await expect(dialog.getByRole("button", { name: "Insert", exact: true })).toBeDisabled()
    // The shell's spelling is read by the server, and is not marked as a mistake.
    await type(page, '[{ _id: 7, at: ISODate("2026-01-01") }, { _id: 7 }, { _id: 8 }]')
    await expect(dialog).toContainText("Shell syntax")
    await dialog.getByRole("button", { name: "Insert", exact: true }).click()
    await expect(dialog).toContainText("2 inserted, 1 refused")
    await expect(dialog).toContainText("Document 2: E11000 duplicate key error")
    expect(mongo.asked("POST /mongo/documents").at(-1)?.body).toMatchObject({
      collection: "orders",
      documents: '[{ _id: 7, at: ISODate("2026-01-01") }, { _id: 7 }, { _id: 8 }]',
      ordered: true,
    })
  })

  // M7: the old export was a link the browser followed, so a refusal was a
  // page of JSON in place of the dashboard.
  test("an export is read by the page: a file is saved, and a refusal is said in place", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${collection("orders")}&filter=${encodeURIComponent('{ "status": "paid" }')}`)
    await page.getByRole("button", { name: "Export", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Export orders" })
    await expect(dialog).toContainText("What the query matches")
    const download = page.waitForEvent("download")
    await dialog.getByRole("button", { name: "Export", exact: true }).click()
    expect((await download).suggestedFilename()).toBe("orders.ndjson")
    const asked = mongo.asked("GET /mongo/export").at(-1)?.query
    expect(asked?.get("filter")).toBe('{ "status": "paid" }')
    expect(asked?.get("format")).toBe("ndjson")
    // Canonical is the default: the file imports back as the same documents.
    expect(asked?.has("relaxed")).toBe(false)
    await page.unrouteAll({ behavior: "ignoreErrors" })

    await mockMongo(page, { exportRefused: true })
    await page.goto(collection("orders"))
    await page.getByRole("button", { name: "Export", exact: true }).click()
    await page
      .getByRole("dialog", { name: "Export orders" })
      .getByRole("button", { name: "Export", exact: true })
      .click()
    await expect(page.getByText("Could not export orders")).toBeVisible()
    expect(new URL(page.url()).pathname).toBe(DATA)
  })

  test("a collection is created, renamed and dropped from the rail", async ({ page }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${DATA}?db=${DB}`)
    await rail(page).getByRole("button", { name: "New collection" }).click()
    const create = page.getByRole("dialog", { name: "New collection" })
    await create.getByRole("button", { name: /^Capped/ }).click()
    await create.getByLabel("Name").fill("events")
    await create.getByRole("button", { name: "Create collection" }).click()
    await expect(create).toBeHidden()
    expect(mongo.asked("POST /mongo/collections").at(-1)?.body).toMatchObject({
      database: DB,
      collection: "events",
      capped: true,
      size: 64 * 1024 * 1024,
    })
    // It opens on what was made.
    await expect.poll(() => where(page)).toContain("collection=events")

    await rail(page).getByRole("button", { name: "Actions for events" }).click()
    await page.getByRole("menuitem", { name: "Rename…" }).click()
    const rename = page.getByRole("dialog", { name: "Rename events" })
    await rename.getByLabel("New name").fill("audit")
    await rename.getByRole("button", { name: "Rename", exact: true }).click()
    await expect.poll(() => where(page)).toContain("collection=audit")
    expect(mongo.asked("POST /mongo/collections/rename").at(-1)?.body).toMatchObject({
      collection: "events",
      to: "audit",
    })

    await rail(page).getByRole("button", { name: "Actions for audit" }).click()
    await page.getByRole("menuitem", { name: "Drop…" }).click()
    const drop = page.getByRole("dialog", { name: "Drop audit" })
    await expect(drop).toContainText(DB)
    expect(mongo.asked("DELETE /mongo/collections")).toHaveLength(0)
    await drop.getByRole("button", { name: "Drop collection" }).click()
    await expect(page.locator("[data-slot=mongo-database]")).toBeVisible()
    expect(Object.keys(mongo.collections)).not.toContain("audit")
  })

  // M6: a list that could not be read was drawn as an empty one.
  test("a read that fails is said, with the way to try again, and a page already read is kept", async ({
    page,
  }) => {
    const mongo = await mockMongo(page, { failing: /^\/mongo\/(collections|find|count)/ })
    await page.goto(collection("users"))
    await expect(rail(page).getByRole("alert")).toContainText("the server did not answer")
    mongo.heal()
    await rail(page).getByRole("button", { name: "Try again" }).click()
    await expect(rail(page).getByRole("link", { name: /users/ }).first()).toBeVisible()
    await page
      .locator("[data-slot=mongo-documents]")
      .getByRole("button", { name: "Try again" })
      .click()
    await expect(documents(page)).toHaveCount(2)
  })

  // The review's first finding: a refusal that was not about the filter was
  // shown nowhere, or was pinned on the filter.
  test("a refusal lands on the field it is about, or under the bar, and is said beside the documents it left", async ({
    page,
  }) => {
    await mockMongo(page, {
      refusal: (body) =>
        body.sort === "{ age: 5 }"
          ? "(Location15975) $sort key ordering must be 1 (for ascending) or -1 (for descending)"
          : body.sort === "{ $x: 1 }"
            ? "(Location16410) FieldPath field names may not start with '$'."
            : undefined,
    })
    await page.goto(collection("users"))
    await expect(documents(page)).toHaveCount(2)
    await page.getByRole("button", { name: "Options" }).click()
    const sort = page.getByLabel("Sort", { exact: true })
    await page.getByLabel("Filter", { exact: true }).fill("{ age: { $gt: 1 } }")
    await sort.fill("{ age: 5 }")
    await sort.press("Enter")
    // On the sort, in the server's words — and not on the filter, though one is typed.
    await expect(page.locator("#mongo-query-sort-problem")).toContainText("$sort key ordering")
    await expect(page.locator("#mongo-query-filter-problem")).toHaveCount(0)
    // The documents from before stay, and the line over them says why and asks again.
    const held = page.locator("[data-slot=mongo-read-failed]")
    await expect(held).toContainText("The server refused this query")
    await expect(held).toContainText("$sort key ordering")
    await expect(held.getByRole("button", { name: "Try again" })).toBeVisible()
    await expect(documents(page)).toHaveCount(2)

    // A refusal that names no field of the bar is the query's own.
    await sort.fill("{ $x: 1 }")
    await sort.press("Enter")
    await expect(page.locator("[data-slot=mongo-query-refusal]")).toContainText(
      "FieldPath field names may not start with",
    )
    await expect(page.locator("#mongo-query-sort-problem")).toHaveCount(0)
  })

  test("a page that could not be read says which, why, and whose documents are on screen", async ({
    page,
  }) => {
    const users = Array.from({ length: 60 }, (_, n) =>
      doc(`{"_id":{"$numberInt":"${n}"},"n":{"$numberInt":"${n}"}}`),
    )
    const mongo = await mockMongo(page, {
      collections: { many: { docs: users } },
      unreachable: (body) => body.skip === 50,
    })
    await page.goto(collection("many"))
    await expect(documents(page)).toHaveCount(50)
    await page.getByRole("button", { name: "Next page" }).click()
    const held = page.locator("[data-slot=mongo-read-failed]")
    await expect(held).toContainText("Page 2 could not be read")
    await expect(held).toContainText("the server could not be reached")
    // The page before is still there, and its range is said as that.
    await expect(documents(page)).toHaveCount(50)
    await expect(page.locator("[data-slot=mongo-documents]")).toContainText(
      "1–50 of 60 · from before",
    )
    const before = mongo.asked("POST /mongo/find").length
    await held.getByRole("button", { name: "Try again" }).click()
    await expect.poll(() => mongo.asked("POST /mongo/find").length).toBeGreaterThan(before)
  })

  test("a find that runs long says how long, and stopping it is not a failure", async ({
    page,
  }) => {
    const find = hold()
    await mockMongo(page, { findHeld: find.until })
    await page.goto(collection("users"))
    // After a second the command's place holds the clock and the way to stop.
    const stop = page.getByRole("button", { name: /^Stop/ })
    await expect(stop).toBeVisible({ timeout: 10_000 })
    await expect(stop).toContainText(/\d+ s/)
    await stop.click()
    await expect(page.getByText("The find was stopped before the server answered.")).toBeVisible()
    await expect(page.locator("[data-slot=mongo-documents]").getByRole("alert")).toHaveCount(0)
    find.release()
    await page.getByRole("button", { name: "Find again" }).click()
    await expect(documents(page)).toHaveCount(2)
  })

  // The review: staged edits were dropped without a word by a press on
  // another collection, or on the sidebar.
  test("edits staged on a document stay with it through another collection, another page and a re-read", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("users"))
    const first = documents(page).first()
    await fieldAction(first, "age", "Edit")
    await first.getByLabel("Value of age").fill("21")
    await page.keyboard.press("Enter")
    await expect(first.locator("[data-slot=mongo-pending]")).toContainText("1 changed")
    const unsent = page.locator("[data-slot=mongo-unsent]")
    await expect(unsent).toContainText("Unsent edits on one document")

    // Another collection opens at once; the one left behind is marked in the rail.
    await rail(page)
      .getByRole("link", { name: /orders/ })
      .click()
    await expect(documents(page)).toHaveCount(3)
    await expect(page.getByRole("dialog")).toHaveCount(0)
    await expect(
      rail(page)
        .getByRole("link", { name: /users/ })
        .first()
        .locator("[data-slot=mongo-unsent-mark]"),
    ).toBeVisible()
    // Another page of the database, by the sidebar, and back.
    await page
      .getByRole("navigation", { name: "Sidebar" })
      .getByRole("link", { name: "Schema" })
      .click()
    await expect(page.locator("[data-slot=mongo-schema]")).toBeVisible()
    await rail(page).getByRole("link", { name: /users/ }).first().click()
    await page
      .getByRole("navigation", { name: "Sidebar" })
      .getByRole("link", { name: "Documents" })
      .click()
    await expect(documents(page).first().locator("[data-slot=mongo-pending]")).toContainText(
      "1 changed",
    )

    // A query that leaves the document out says so, and brings it back.
    await page.getByLabel("Filter", { exact: true }).fill('{ "email": "user2@example.com" }')
    await page.getByLabel("Filter", { exact: true }).press("Enter")
    await expect(documents(page)).toHaveCount(1)
    await expect(unsent).toContainText("not on this page")
    await unsent.getByRole("button", { name: "Show it" }).click()
    await expect(page.getByLabel("Filter", { exact: true })).toHaveValue(
      '{ "_id": {"$oid":"6abe79972945ac11a3124bfc"} }',
    )

    // The update is still held to the value the field had when the edit was made.
    mongo.collections.users.docs[0] = doc(
      USERS[0].canonical.replace('"age":{"$numberInt":"19"}', '"age":{"$numberInt":"40"}'),
    )
    await page.getByRole("button", { name: "Read the documents again" }).click()
    const pending = documents(page).first().locator("[data-slot=mongo-pending]")
    await expect(pending).toContainText("has been written to since this edit was made")
    await pending.getByRole("button", { name: "Update" }).click()
    await expect
      .poll(() => mongo.asked("PATCH /mongo/documents").at(-1)?.body.filter)
      .toBe('{"_id":{"$oid":"6abe79972945ac11a3124bfc"},"age":{"$numberInt":"19"}}')

    // Letting them go is the reader's own word, and can be taken back.
    await page.goto(collection("users"))
    await fieldAction(documents(page).first(), "email", "Remove")
    await page
      .locator("[data-slot=mongo-unsent]")
      .getByRole("button", { name: "Let them go" })
      .click()
    await expect(page.locator("[data-slot=mongo-pending]")).toHaveCount(0)
    await page.getByRole("button", { name: "Undo" }).click()
    await expect(documents(page).first().locator("[data-slot=mongo-pending]")).toContainText(
      "1 removed",
    )
  })

  // The review: fifty documents were 2,300 stops for Tab, and an edit in
  // place left the keyboard on the page's body.
  test("the list is one stop for Tab, the arrows walk its rows, and an edit gives the keyboard back", async ({
    page,
  }) => {
    await mockMongo(page)
    await page.goto(collection("users"))
    await expect(documents(page)).toHaveCount(2)
    const list = page.getByRole("list", { name: "Documents" })
    const focused = () =>
      page.evaluate(() => document.activeElement?.getAttribute("aria-label") ?? "")

    await page.getByRole("button", { name: "Expand all" }).focus()
    await page.keyboard.press("Tab")
    expect(await focused()).toBe('_id: ObjectId("6abe79972945ac11a3124bfc")')
    // The rows of every document are walked with the arrows; none of them is a stop of its own.
    expect(await list.locator('[data-tree-row][tabindex="0"]').count()).toBe(1)
    await page.keyboard.press("ArrowDown")
    await page.keyboard.press("ArrowDown")
    await page.keyboard.press("ArrowDown")
    expect(await focused()).toBe("age: 19")
    await page.keyboard.press("PageDown")
    expect(await focused()).toBe('_id: ObjectId("6abe79972945ac11a3124bfd")')
    await page.keyboard.press("PageUp")
    await page.keyboard.press("ArrowDown")
    await page.keyboard.press("ArrowDown")
    await page.keyboard.press("ArrowDown")

    // Enter edits the value; Enter again stages it, and the row has the keyboard.
    await page.keyboard.press("Enter")
    await expect(documents(page).first().getByLabel("Value of age")).toBeFocused()
    await page.keyboard.type("23")
    await page.keyboard.press("Enter")
    expect(await focused()).toBe("age: 23, changed")
    // Escape out of the form does the same.
    await page.keyboard.press("Enter")
    await page.keyboard.press("Escape")
    expect(await focused()).toBe("age: 23, changed")

    // From the row, Tab reaches its own controls, then the document's, then leaves the list.
    await page.keyboard.press("Tab")
    expect(await focused()).toBe("Undo the change to age")
    let stops = 1
    while (
      stops < 20 &&
      (await page.evaluate(
        () => document.activeElement?.closest('[aria-label="Documents"]') !== null,
      ))
    ) {
      await page.keyboard.press("Tab")
      stops++
    }
    expect(stops).toBeLessThan(14)
    await expect(page.getByRole("combobox", { name: "Documents a page" })).toBeFocused()
  })

  test("a date is the moment it is wherever a document is read as text", async ({ page }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("users", "&view=json"))
    await expect(documents(page).first()).toContainText(
      '"createdAt": {"$date":"2026-10-01T14:17:43.204Z"}',
    )
    await documents(page).first().getByRole("button", { name: "Edit", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Edit document" })
    const text = await opened(page)
    expect(text).toContain('"createdAt": {"$date":"2026-10-01T14:17:43.204Z"}')
    // Unchanged, it is not something to save: the two spellings are one value.
    await expect(dialog.getByRole("button", { name: "Save document" })).toBeDisabled()
    await type(page, text.replace("2026-10-01T14:17:43.204Z", "2027-01-01T00:00:00.000Z"))
    await dialog.getByRole("button", { name: "Save document" }).click()
    await expect(dialog).toBeHidden()
    expect(String(mongo.asked("PUT /mongo/documents").at(-1)?.body.document)).toContain(
      '{"$date":"2027-01-01T00:00:00.000Z"}',
    )
  })

  test("a head of the table asks the server for the order, and says which fields are not in every document", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("users", "&view=table"))
    const grid = page.getByRole("grid", { name: "users documents" })
    await expect(grid).toBeVisible()
    // (The first column of the grid is its row numbers.)
    const head = (at: number) => grid.locator(`[role=columnheader][aria-colindex="${at + 2}"]`)
    // `zeta` is in one of the two documents: its head says so, and the rule is stated once.
    await expect(head(1)).toContainText("zeta")
    await expect(head(1)).toContainText("1 of 2")
    await expect(head(3)).toContainText("Int32")
    await expect(page.getByText("may be a field the document does not have")).toBeVisible()

    await head(3).getByText("age", { exact: true }).click()
    await expect.poll(() => mongo.asked("POST /mongo/find").at(-1)?.body.sort).toBe('{ "age": 1 }')
    await expect.poll(() => where(page)).toContain('sort={ "age": 1 }')
    // The bar holds the same sort the head asked for.
    await page.getByRole("button", { name: /^Options/ }).click()
    await expect(page.getByLabel("Sort", { exact: true })).toHaveValue('{ "age": 1 }')
  })

  test("a database with nothing of the reader's says which kind of nothing it is", async ({
    page,
  }) => {
    await mockMongo(page)
    await page.goto(`${DATA}?db=admin`)
    const pane = page.locator("[data-slot=mongo-database]")
    await expect(pane).toContainText("admin holds only the server's own collections")
    await expect(pane.getByRole("button", { name: "New collection" })).toHaveCount(0)
    // The list beside it shows them without being asked.
    await expect(rail(page).getByText("system.version")).toBeVisible()

    await page.goto(`${DATA}?db=gone_since`)
    await expect(pane).toContainText("The server has no database called gone_since")
    await pane.getByRole("link", { name: `Open ${DB}` }).click()
    await expect(pane).toContainText("By the data they hold")

    // The one the connection names is simply empty until it holds something.
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockMongo(page, { collections: {} })
    await page.goto(`${DATA}?db=${DB}`)
    await expect(pane).toContainText(`${DB} holds no collections`)
    await expect(pane.getByRole("button", { name: "New collection" }).first()).toBeVisible()
  })

  test("a collection's options are changed in place, and what removes documents is asked for by name", async ({
    page,
  }) => {
    const mongo = await mockMongo(page, {
      collections: {
        ...DEFAULT_COLLECTIONS,
        sessions: { capped: true, cappedSize: 2 << 20, cappedMax: 500, docs: [] },
        active_users: {
          type: "view",
          viewOn: "users",
          pipeline: '[{"$match":{"verified":true}}]',
          docs: [],
        },
      },
    })
    await page.goto(`${DATA}?db=${DB}`)
    // An ordinary collection has nothing of the kind to change.
    await rail(page).getByRole("button", { name: "Actions for orders" }).click()
    await expect(page.getByRole("menuitem", { name: "Options…" })).toHaveCount(0)
    await page.keyboard.press("Escape")

    await rail(page).getByRole("button", { name: "Actions for sessions" }).click()
    await page.getByRole("menuitem", { name: "Options…" }).click()
    const options = page.getByRole("dialog", { name: "Options of sessions" })
    await expect(options.getByLabel("Size")).toHaveValue("2")
    await options.getByLabel("Size").fill("1")
    await options.getByRole("button", { name: "Save options…" }).click()
    const confirm = page.getByRole("dialog", { name: "Resize sessions" })
    await expect(confirm).toContainText("removed at once, oldest first")
    expect(mongo.asked("PATCH /mongo/collections")).toHaveLength(0)
    await confirm.getByRole("button", { name: "Resize collection" }).click()
    // Only the field that changed is sent.
    await expect
      .poll(() => mongo.asked("PATCH /mongo/collections").at(-1)?.body)
      .toEqual({ database: DB, collection: "sessions", cappedSize: 1 << 20 })

    // A view is redefined without ceremony: nothing it reads is touched.
    await rail(page).getByRole("button", { name: "Actions for active_users" }).click()
    await page.getByRole("menuitem", { name: "Options…" }).click()
    const view = page.getByRole("dialog", { name: "Options of active_users" })
    await view.getByLabel("Pipeline").fill("[ { $match: { verified: false } } ]")
    await view.getByRole("button", { name: "Save options", exact: true }).click()
    await expect(view).toBeHidden()
    expect(mongo.asked("PATCH /mongo/collections").at(-1)?.body).toEqual({
      database: DB,
      collection: "active_users",
      viewOn: "users",
      pipeline: "[ { $match: { verified: false } } ]",
    })
    await page.unrouteAll({ behavior: "ignoreErrors" })

    // A role that may not remove is not offered a cap to change.
    await mockMongo(page, { limited: true })
    await page.goto(`${DATA}?db=${DB}`)
    await rail(page).getByRole("button", { name: "Actions for sessions" }).click()
    await expect(page.getByRole("menuitem", { name: "Rename…" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Options…" })).toHaveCount(0)
  })

  test("the explain is a tree of steps, and running it fills in what each step examined", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(collection("users"))
    await page.getByLabel("Filter", { exact: true }).fill("{ age: { $gt: 30 } }")
    await page.getByRole("button", { name: "Explain" }).click()
    const dialog = page.getByRole("dialog", { name: "Explain" })
    await expect(dialog.getByText("Scans the whole collection")).toBeVisible()
    await expect(dialog.getByRole("list", { name: /Plan steps/ })).toContainText("COLLSCAN")
    expect(mongo.asked("POST /mongo/explain").at(-1)?.body).toMatchObject({
      filter: "{ age: { $gt: 30 } }",
      verbosity: "queryPlanner",
    })
    await dialog.getByRole("radio", { name: "Run it" }).click()
    await expect(dialog.getByText("Documents examined")).toBeVisible()
    await expect(dialog).toContainText("1,500")
    expect(mongo.asked("POST /mongo/explain").at(-1)?.body.verbosity).toBe("executionStats")
  })
})

test.describe("aggregations", () => {
  const build = async (page: Page, text: string) => {
    await page
      .getByRole("button", { name: /Paste a pipeline|As text/ })
      .first()
      .click()
    const dialog = page.getByRole("dialog", { name: "The pipeline as text" })
    await dialog.locator("#mongo-pipeline-text").fill(text)
    await dialog.getByRole("button", { name: "Use these stages" }).click()
  }

  test("each stage previews what the pipeline holds after it, from the stages up to it alone", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await build(
      page,
      '[ { $match: { status: "paid" } }, { $group: { _id: "$status", n: { $sum: 1 } } } ]',
    )
    const stages = page.locator("[data-slot=mongo-stage]")
    await expect(stages).toHaveCount(2)
    await expect(stages.nth(1).locator("[data-slot=mongo-stage-preview]")).toContainText(
      "over the first 100,000 input documents",
    )
    const previews = mongo.asked("POST /mongo/aggregate/preview").map((call) => call.body)
    expect(previews.find((body) => body.stage === 0)).toMatchObject({
      pipeline: '[{ "$match": { status: "paid" } }]',
    })
    expect(previews.find((body) => body.stage === 1)).toMatchObject({
      pipeline:
        '[{ "$match": { status: "paid" } }, { "$group": { _id: "$status", n: { $sum: 1 } } }]',
    })

    // A stage that is switched off is skipped by what follows it.
    const sent = mongo.asked("POST /mongo/aggregate/preview").length
    await stages.nth(0).getByRole("button", { name: "Skip stage 1" }).click()
    await expect(stages.nth(0)).toContainText("Skipped")
    await expect
      .poll(() => mongo.asked("POST /mongo/aggregate/preview").slice(sent).at(-1)?.body)
      .toMatchObject({ stage: 0, pipeline: '[{ "$group": { _id: "$status", n: { $sum: 1 } } }]' })
  })

  test("a stage that cannot be read is said on the stage, and stops only the previews after it", async ({
    page,
  }) => {
    await mockMongo(page)
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await build(page, '[ { $match: { status: "paid" } }, { $sort: { total: -1 } } ]')
    const stages = page.locator("[data-slot=mongo-stage]")
    await stages.nth(0).getByRole("textbox").fill("{ status: ")
    await expect(stages.nth(0).getByRole("alert")).toContainText("where a value was expected")
    await expect(stages.nth(1)).toContainText("Shown once every stage up to here can be read")
    await expect(page.getByRole("button", { name: "Run", exact: true })).toBeDisabled()
  })

  // H7 and M13: the old page showed the first 200 documents as if they were
  // all of them, and kept one collection's result under the next one's name.
  test("a run says when there is more past its limit, and a pipeline is its collection's own", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await build(page, '[ { $match: { status: "paid" } } ]')
    await page.getByRole("button", { name: "Run", exact: true }).click()
    const result = page.getByRole("region", { name: "Result of the run" })
    await expect(result).toContainText("3 documents, and more past the limit")
    expect(mongo.asked("POST /aggregate").at(-1)?.body).toMatchObject({
      collection: "orders",
      pipeline: '[{ "$match": { status: "paid" } }]',
      limit: 100,
    })
    // The stages have moved on: the result says it is of the pipeline as it was run.
    await page
      .locator("[data-slot=mongo-stage]")
      .first()
      .getByRole("textbox")
      .fill('{ status: "x" }')
    await expect(result).toContainText("of the pipeline as it was run")

    // Another collection starts from its own stages, and has no result of this one.
    await rail(page).getByRole("link", { name: /users/ }).first().click()
    await expect(page.getByText("No stages yet")).toBeVisible()
    await expect(page.getByRole("region", { name: "Result of the run" })).toHaveCount(0)
    // Coming back, the stages are where they were left.
    await rail(page)
      .getByRole("link", { name: /orders/ })
      .click()
    await expect(page.locator("[data-slot=mongo-stage]").first().getByRole("textbox")).toHaveValue(
      '{ status: "x" }',
    )
  })

  test("a pipeline that writes is confirmed first; one that only mentions $merge inside a stage is not", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await build(page, '[ { $group: { _id: "$k", all: { $mergeObjects: "$doc" } } } ]')
    await page.getByRole("button", { name: "Run", exact: true }).click()
    await expect(page.getByRole("region", { name: "Result of the run" })).toBeVisible()
    expect(mongo.asked("POST /aggregate")).toHaveLength(1)

    await build(page, '[ { $match: {} }, { $out: "copy" } ]')
    await expect(page.getByText("This pipeline writes ($out)")).toBeVisible()
    // The preview of the writing stage is what reaches it: it is not run.
    await expect(
      page.locator("[data-slot=mongo-stage]").nth(1).locator("[data-slot=mongo-stage-preview]"),
    ).toContainText("What reaches $out")
    await page.getByRole("button", { name: "Run", exact: true }).click()
    const confirm = page.getByRole("dialog", { name: "Run a pipeline that writes" })
    await expect(confirm).toContainText("$match → $out")
    expect(mongo.asked("POST /aggregate")).toHaveLength(1)
    await confirm.getByRole("button", { name: "Run it" }).click()
    await expect.poll(() => mongo.asked("POST /aggregate").length).toBe(2)
  })

  test("a role that may not remove can preview a writing pipeline and is not offered its run", async ({
    page,
  }) => {
    await mockMongo(page, { limited: true })
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await build(page, '[ { $out: "copy" } ]')
    await expect(page.getByText(/needs the permission to remove data/)).toBeVisible()
    await expect(page.getByRole("button", { name: "Run", exact: true })).toBeDisabled()
    await expect(page.locator("[data-slot=mongo-stage-preview]")).toContainText("What reaches $out")
  })

  test("a pipeline is saved for its collection, exported as text, and opened again", async ({
    page,
  }) => {
    await mockMongo(page)
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await build(page, '[ { $match: { status: "paid" } }, { $limit: 5 } ]')
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await page.getByRole("dialog", { name: "Save pipeline" }).getByLabel("Name").fill("Paid orders")
    await page.keyboard.press("Enter")
    await expect(page.locator("[data-slot=mongo-pipeline]")).toContainText("Paid orders")

    await page.getByRole("button", { name: "As text" }).click()
    await expect(page.locator("#mongo-pipeline-text")).toHaveValue(
      '[\n  { $match: { status: "paid" } },\n  { $limit: 5 }\n]',
    )
    await page.keyboard.press("Escape")

    await page.getByRole("button", { name: "Clear the pipeline" }).click()
    // Saved pipelines are what the empty builder offers to take up.
    await page.getByRole("button", { name: "Open the pipeline Paid orders" }).click()
    await expect(page.locator("[data-slot=mongo-stage]")).toHaveCount(2)
    // Kept in the browser for this collection: a reload still has it.
    await page.reload()
    await page.getByRole("button", { name: /^Saved/ }).click()
    await expect(page.getByRole("menuitem", { name: /^Paid orders/ })).toBeVisible()
  })

  test("a pipeline that only reads becomes a view of its collection, with its stages as written", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await build(page, '[ { $match: { status: "paid" } }, { $limit: 5 } ]')
    await page.getByRole("button", { name: "Export" }).click()
    await page.getByRole("menuitem", { name: "Create a view from it…" }).click()
    const dialog = page.getByRole("dialog", { name: "New collection" })
    await dialog.getByLabel("Name").fill("paid_orders")
    await dialog.getByRole("button", { name: "Create view" }).click()
    await expect.poll(() => where(page)).toContain("collection=paid_orders")
    expect(mongo.asked("POST /mongo/collections").at(-1)?.body).toMatchObject({
      database: DB,
      collection: "paid_orders",
      viewOn: "orders",
      pipeline: '[\n  { $match: { status: "paid" } },\n  { $limit: 5 }\n]',
    })

    // A view is nothing to write into: a pipeline that writes is not offered as one.
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await build(page, '[ { $out: "copy" } ]')
    await page.getByRole("button", { name: "Export" }).click()
    await expect(page.getByRole("menuitem", { name: "Create a view from it…" })).toBeDisabled()
  })

  test("the console says what a command is before it runs, confirms a removal, and never runs a blocked one", async ({
    page,
  }) => {
    const mongo = await mockMongo(page, {
      verdicts: {
        drop: {
          class: "destructive",
          reason: "it removes a collection and everything in it",
          target: "orders",
        },
        shutdown: { class: "blocked", reason: "it stops the server" },
      },
    })
    // The console needs no collection.
    await page.goto(`${QUERY}?db=${DB}&view=command`)
    const prompt = page.getByLabel("Command", { exact: true })
    const run = page.getByRole("button", { name: "Run", exact: true })

    await prompt.fill("{ ping: 1 }")
    await expect(page.locator("#mongo-console-verdict")).toContainText("ping reads")
    await run.click()
    await expect(page.getByRole("log")).toContainText("ok")
    expect(mongo.asked("POST /mongo/command").at(-1)?.body).toEqual({
      database: DB,
      command: "{ ping: 1 }",
    })

    await prompt.fill('{ drop: "orders" }')
    await expect(page.locator("#mongo-console-verdict")).toContainText("removes or stops")
    await run.click()
    const confirm = page.getByRole("dialog", { name: "Run drop" })
    await expect(confirm).toContainText("it removes a collection and everything in it")
    expect(mongo.asked("POST /mongo/command")).toHaveLength(1)
    await confirm.getByRole("button", { name: "Cancel" }).click()

    await prompt.fill("{ shutdown: 1 }")
    await expect(page.locator("#mongo-console-verdict")).toContainText("It stops the server")
    await expect(run).toBeDisabled()
    await prompt.press("Enter")
    expect(mongo.asked("POST /mongo/command")).toHaveLength(1)
  })

  test("a stage the server refuses says so once, and the stages after it wait for it", async ({
    page,
  }) => {
    await mockMongo(page)
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    // What enters the pipeline is on screen before any stage is.
    await expect(page.locator("[data-slot=mongo-pipeline-input]")).toContainText(
      "of its 3 documents",
    )
    await build(
      page,
      '[ { $match: { status: "paid" } }, { $nope: {} }, { $sort: { total: -1 } }, { $limit: 5 } ]',
    )
    const previews = page.locator("[data-slot=mongo-stage-preview]")
    await expect(previews.nth(1).getByRole("alert")).toContainText("$nope is not one a preview can")
    await expect(previews.nth(2)).toContainText("Shown once stage 2 can be run")
    await expect(previews.nth(3)).toContainText("Shown once stage 2 can be run")
    // The refusal is printed on the stage it is about, and nowhere else.
    await expect(page.getByText("$nope is not one a preview can run")).toHaveCount(1)

    // With that stage skipped, the ones after it are read again.
    await page.getByRole("button", { name: "Skip stage 2" }).click()
    await expect(previews.nth(2)).toContainText("sample documents after $sort")
    await expect(previews.nth(3)).toContainText("sample documents after $limit")
  })

  // The review: Enter pressed before the classification answered did nothing.
  test("Enter pressed before a command is classed is kept, and runs it when the answer comes", async ({
    page,
  }) => {
    const classify = hold()
    const mongo = await mockMongo(page, { classifyHeld: classify.until })
    await page.goto(`${QUERY}?db=${DB}&view=command`)
    const prompt = page.getByLabel("Command", { exact: true })
    await prompt.fill("{ ping: 1 }")
    await prompt.press("Enter")
    // Nothing is run before the server has said what the command is.
    await page.waitForTimeout(500)
    expect(mongo.asked("POST /mongo/command")).toHaveLength(0)
    await expect(page.getByRole("button", { name: "Run", exact: true })).toBeDisabled()
    classify.release()
    await expect(page.getByRole("log")).toContainText("ok")
    expect(mongo.asked("POST /mongo/command")).toHaveLength(1)
    await expect(prompt).toHaveValue("")
  })

  test("a role that cannot run commands is given the reference, and no prompt", async ({
    page,
  }) => {
    await mockMongo(page, { reader: true })
    await page.goto(`${QUERY}?db=${DB}&view=command`)
    await expect(page.getByText("Your role cannot run commands")).toBeVisible()
    await expect(page.getByLabel("Command", { exact: true })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Run", exact: true })).toHaveCount(0)
    await expect(
      page.getByRole("complementary", { name: "Commands the console knows" }),
    ).toContainText("collStats")
  })

  test("a protected connection runs a command that reads and no other", async ({ page }) => {
    await mockMongo(page, {
      rows: { 5: { readOnly: true } },
      verdicts: { insert: { class: "write" } },
    })
    await page.goto(`${QUERY}?db=${DB}&view=command`)
    const prompt = page.getByLabel("Command", { exact: true })
    await prompt.fill("{ ping: 1 }")
    await expect(page.getByRole("button", { name: "Run", exact: true })).toBeEnabled()
    await prompt.fill('{ insert: "orders" }')
    await expect(page.locator("#mongo-console-verdict")).toContainText(
      "The connection is protected",
    )
    await expect(page.getByRole("button", { name: "Run", exact: true })).toBeDisabled()
  })
})

test.describe("schema", () => {
  test("a sample is read when asked for, and a value adds itself to the Documents filter", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${SCHEMA}?db=${DB}&collection=users`)
    // Never on arrival: a sample is a read of up to ten thousand documents.
    await expect(page.getByText("What users holds")).toBeVisible()
    expect(mongo.asked("POST /mongo/schema")).toHaveLength(0)
    await page.getByRole("button", { name: "Analyze", exact: true }).click()
    const fields = page.locator("[data-slot=mongo-schema-field]")
    await expect(fields).toHaveCount(4)
    await expect(page.getByText("Based on 200 documents of about 1,500")).toBeVisible()
    // A field of two types says both, with their shares.
    await expect(fields.nth(3)).toContainText("Int32")
    await expect(fields.nth(3)).toContainText("75%")
    await expect(fields.nth(3)).toContainText("10%")

    await page
      .getByRole("button", { name: "Find the documents where address.city is Berlin" })
      .click()
    await expect(documents(page)).toHaveCount(1)
    await expect.poll(() => where(page)).toContain('filter={ "address.city": "Berlin" }')

    // A second condition joins the first rather than replacing it.
    await page.goBack()
    await page.getByRole("button", { name: "Find the 180 documents without legacyId" }).click()
    await expect
      .poll(() => where(page))
      .toContain(
        'filter={ "$and": [{ "address.city": "Berlin" }, { "legacyId": { "$exists": false } }] }',
      )
  })

  test("indexes say how much they are used, and are hidden, dropped and created from their list", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${SCHEMA}?db=${DB}&collection=users&view=indexes`)
    const rows = page.locator("[data-slot=mongo-index]")
    await expect(rows).toHaveCount(2)
    await expect(rows.nth(0)).toContainText("12 uses")
    await expect(rows.nth(1)).toContainText("unique")
    // The _id index cannot be hidden or dropped, and is offered neither.
    await expect(rows.nth(0).getByRole("button")).toHaveCount(0)

    await rows.nth(1).getByRole("button", { name: "Hide", exact: true }).click()
    await expect(rows.nth(1)).toContainText("Hidden from the planner")
    expect(mongo.asked("PATCH /mongo/indexes").at(-1)?.body).toMatchObject({
      name: "email_1",
      hidden: true,
    })

    await page.getByRole("button", { name: "Create index" }).click()
    const dialog = page.getByRole("dialog", { name: "Create index" })
    await dialog.getByLabel("Field 1", { exact: true }).fill("createdAt")
    await dialog.getByRole("switch", { name: /unique/ }).click()
    await expect(dialog).toContainText("db.users.createIndex(")
    await dialog.getByRole("button", { name: "Create index" }).click()
    await expect(rows).toHaveCount(3)
    expect(mongo.asked("POST /mongo/indexes").at(-1)?.body).toMatchObject({
      collection: "users",
      keys: [{ field: "createdAt", type: "asc" }],
      unique: true,
    })

    await rows.nth(1).getByRole("button", { name: "Drop", exact: true }).click()
    const confirm = page.getByRole("dialog", { name: "Drop the index email_1" })
    expect(mongo.asked("DELETE /mongo/indexes")).toHaveLength(0)
    await confirm.getByRole("button", { name: "Drop index" }).click()
    await expect(rows).toHaveCount(2)
  })

  test("a view is shown whose indexes its reads use, and the server is not asked for its own", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${SCHEMA}?db=${DB}&collection=active_users&view=indexes`)
    await expect(page.getByText("A view has no indexes of its own")).toBeVisible()
    await expect(page.getByRole("button", { name: "Create index" })).toHaveCount(0)
    expect(mongo.asked("GET /mongo/indexes")).toHaveLength(0)
    await page.getByRole("link", { name: "Open the indexes of users" }).click()
    await expect(page.locator("[data-slot=mongo-index]")).toHaveCount(2)
  })

  test("a TTL index's limit is changed in place, after the deletion it causes is named", async ({
    page,
  }) => {
    const mongo = await mockMongo(page, {
      collections: {
        users: {
          docs: USERS,
          indexes: [
            INDEXES[0],
            {
              ...INDEXES[1],
              name: "createdAt_1",
              keys: [{ field: "createdAt", type: "asc" }],
              unique: false,
              expireAfterSeconds: 86_400,
            },
          ],
        },
      },
    })
    await page.goto(`${SCHEMA}?db=${DB}&collection=users&view=indexes`)
    const row = page.locator("[data-slot=mongo-index]").nth(1)
    await expect(row).toContainText("expires after 1d")
    await row.getByRole("button", { name: "More actions for createdAt_1" }).click()
    await page.getByRole("menuitem", { name: "Change expiry…" }).click()
    const dialog = page.getByRole("dialog", { name: "Change expiry" })
    await dialog.getByLabel("Delete a document after").fill("3600")
    await dialog.getByRole("button", { name: "Change expiry…" }).click()
    const confirm = page.getByRole("dialog", { name: "Change when users expires" })
    await expect(confirm).toContainText("more than 1h old is deleted")
    expect(mongo.asked("PATCH /mongo/indexes")).toHaveLength(0)
    await confirm.getByRole("button", { name: "Change expiry", exact: true }).click()
    await expect
      .poll(() => mongo.asked("PATCH /mongo/indexes").at(-1)?.body)
      .toEqual({ database: DB, collection: "users", name: "createdAt_1", expireAfterSeconds: 3600 })
    await expect(row).toContainText("expires after 1h")

    // Dropping it by the keyboard leaves the keyboard on the list, not on the page's body.
    await row.getByRole("button", { name: "Drop", exact: true }).focus()
    await page.keyboard.press("Enter")
    await page.getByRole("dialog").getByRole("button", { name: "Drop index" }).click()
    await expect(page.locator("[data-slot=mongo-index]")).toHaveCount(1)
    await expect(page.getByLabel("Indexes of users")).toBeFocused()
  })

  test("a rule is checked against what is stored before it is saved", async ({ page }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${SCHEMA}?db=${DB}&collection=products&view=validation`)
    await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 15_000 })
    expect(await typed(page)).toContain('"$jsonSchema"')
    await expect(page.getByRole("button", { name: "Save rule" })).toBeDisabled()

    await type(page, '{ "$jsonSchema": { "required": ["sku", "price", "name"] } }')
    await page.getByRole("button", { name: "Check", exact: true }).click()
    await expect(page.getByRole("region", { name: "Result of the check" })).toContainText(
      "2 existing documents fail this rule",
    )
    expect(mongo.asked("POST /mongo/validation/check").at(-1)?.body).toMatchObject({
      collection: "products",
      validator: '{ "$jsonSchema": { "required": ["sku", "price", "name"] } }',
    })

    await page.getByRole("radio", { name: "Moderate" }).click()
    await page.getByRole("button", { name: "Save rule" }).click()
    await expect
      .poll(() => mongo.asked("PUT /mongo/validation").at(-1)?.body)
      .toMatchObject({
        validator: '{ "$jsonSchema": { "required": ["sku", "price", "name"] } }',
        level: "moderate",
      })
    // A view has no rule of its own, and says where the rule is.
    await page.goto(`${SCHEMA}?db=${DB}&collection=active_users&view=validation`)
    await expect(page.getByText("A view has no rule of its own")).toBeVisible()
    expect(
      mongo
        .asked("GET /mongo/validation")
        .some((call) => call.query.get("collection") === "active_users"),
    ).toBe(false)
  })

  test("a server that lacks a view of the schema is not offered it", async ({ page }) => {
    // FerretDB speaks the protocol and has no validation rules, no index
    // usage and no plan to explain.
    await mockMongo(page, { summaries: { 5: { flavor: "ferretdb", flavorLabel: "FerretDB" } } })
    await page.goto(`${SCHEMA}?db=${DB}&collection=users`)
    const strip = page.getByRole("group", { name: "Schema views" })
    await expect(strip.getByRole("button", { name: "Analysis" })).toBeVisible()
    await expect(strip.getByRole("button", { name: "Validation" })).toHaveCount(0)
    await page.goto(collection("users"))
    await expect(documents(page)).toHaveCount(2)
    await expect(page.getByRole("button", { name: "Explain" })).toHaveCount(0)
  })
})

test.describe("performance", () => {
  test("the readings move with the page's own samples, and the home's link opens the profiler", async ({
    page,
  }) => {
    await mockMongo(page, {
      profiler: {
        level: 1,
        entries: [
          {
            time: "2026-10-01T08:59:00Z",
            op: "query",
            ns: `${DB}.orders`,
            millis: 1840,
            command: '{"find":"orders","filter":{"status":"paid"}}',
            commandTruncated: false,
            planSummary: "COLLSCAN",
            docsExamined: 6000,
            keysExamined: 0,
            returned: 12,
            modified: 0,
            deleted: 0,
            inserted: 0,
            responseLength: 900,
            numYields: 3,
            hasSortStage: true,
            usedDisk: false,
            client: "10.0.0.5",
            user: "app@admin",
            appName: "storefront",
          },
        ],
      },
    })
    await page.goto(PERFORMANCE)
    const tiles = page.locator("[data-slot=stat-tile]")
    await expect(tiles).toHaveCount(5)
    await expect(tiles.nth(1)).toContainText("12")
    await expect(tiles.nth(1)).toContainText("of 400 allowed")
    await expect(tiles.nth(2)).toContainText("25.0%")
    // A rate is the difference of two samples: 30 queries and 60 commands in 3 seconds.
    await expect(tiles.nth(0)).toContainText("30.0", { timeout: 15_000 })

    await page.goto(`${PERFORMANCE}?view=slow`)
    const slow = page.locator("[data-slot=mongo-slow-operation]")
    await expect(slow).toHaveCount(1)
    await expect(slow).toContainText(`query ${DB}.orders`)
    await expect(slow).toContainText("1.8 s")
    await slow.getByRole("button").click()
    await expect(page.getByText('{"find":"orders","filter":{"status":"paid"}}')).toBeVisible()
    await expect(page.getByText("sorted in memory")).toBeVisible()
  })

  test("the profiler is an administrator's to set, and says what its two settings reach", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(`${PERFORMANCE}?view=slow`)
    await expect(page.getByText(`The profiler of ${DB} is off`)).toBeVisible()
    await page.getByRole("button", { name: "Record slow operations" }).click()
    await expect
      .poll(() => mongo.asked("PUT /mongo/profiler").at(-1)?.body)
      .toEqual({
        database: DB,
        level: 1,
      })
    await expect(page.getByText("One threshold for the whole server")).toBeVisible()
    await page.unrouteAll({ behavior: "ignoreErrors" })

    await mockMongo(page, { limited: true })
    await page.goto(`${PERFORMANCE}?view=slow`)
    await expect(page.getByText(`The profiler of ${DB} is off`)).toBeVisible()
    await expect(page.getByRole("button", { name: "Record slow operations" })).toHaveCount(0)
    await expect(page.getByRole("radiogroup", { name: "Profiler level" })).toHaveCount(0)
  })

  test("an operation is stopped only after it is named, and a driver's heartbeat is not listed as work", async ({
    page,
  }) => {
    const operation = (over: Record<string, unknown>) => ({
      opId: "1",
      op: "command",
      ns: "admin.$cmd",
      command: '{"hello":1,"maxAwaitTimeMS":10000}',
      commandTruncated: false,
      secondsRunning: 2,
      client: "10.0.0.9:5000",
      appName: "",
      user: "",
      desc: "conn1",
      active: true,
      waitingForLock: false,
      planSummary: "",
      message: "",
      killPending: false,
      connectionId: 1,
      numYields: 0,
      ...over,
    })
    const mongo = await mockMongo(page, {
      operations: [
        operation({}),
        operation({
          opId: "981234",
          op: "query",
          ns: `${DB}.orders`,
          command: '{"find":"orders"}',
          secondsRunning: 42,
          appName: "storefront",
          planSummary: "COLLSCAN",
        }),
      ],
    })
    await page.goto(`${PERFORMANCE}?view=operations`)
    const rows = page.locator("[data-slot=mongo-operation]")
    await expect(rows).toHaveCount(1)
    await expect(rows).toContainText(`query ${DB}.orders`)
    await expect(rows).toContainText("42 s")
    await expect(page.getByText("Not listed: the heartbeat of one connected client.")).toBeVisible()

    await rows.getByRole("button", { name: `Stop query ${DB}.orders` }).click()
    const confirm = page.getByRole("dialog", { name: "Stop operation" })
    await expect(confirm).toContainText("981234")
    expect(mongo.asked("POST /mongo/killop")).toHaveLength(0)
    await confirm.getByRole("button", { name: "Stop operation" }).click()
    // The id goes back as the string it came as.
    await expect
      .poll(() => mongo.asked("POST /mongo/killop").at(-1)?.body)
      .toEqual({
        opId: "981234",
      })
  })

  // The engineer's own incident: Stop was offered on another client's heartbeat.
  test("a heartbeat that is listed on request is named as one, and cannot be stopped", async ({
    page,
  }) => {
    const base = {
      ns: "admin.$cmd",
      commandTruncated: false,
      secondsRunning: 2,
      client: "10.0.0.9:5000",
      appName: "",
      user: "",
      desc: "conn1",
      active: true,
      waitingForLock: false,
      planSummary: "",
      message: "",
      killPending: false,
      connectionId: 1,
      numYields: 0,
    }
    await mockMongo(page, {
      operations: [
        { ...base, opId: "1", op: "command", command: '{"hello":1,"maxAwaitTimeMS":10000}' },
        { ...base, opId: "2", op: "query", ns: `${DB}.orders`, command: '{"find":"orders"}' },
      ],
    })
    await page.goto(`${PERFORMANCE}?view=operations`)
    const rows = page.locator("[data-slot=mongo-operation]")
    await expect(rows).toHaveCount(1)
    await page.getByRole("switch").click()
    await expect(rows).toHaveCount(2)
    const heartbeat = rows.filter({ hasText: '"hello"' })
    await expect(heartbeat).toContainText("heartbeat")
    await expect(heartbeat.getByRole("button")).toHaveCount(0)
    await expect(rows.getByRole("button", { name: /^Stop / })).toHaveCount(1)
  })

  test("a profiler that records everything does not speak of a threshold while it is empty", async ({
    page,
  }) => {
    await mockMongo(page, { profiler: { level: 2, entries: [] } })
    await page.goto(`${PERFORMANCE}?view=slow`)
    await expect(page.getByText(`Nothing has been recorded in ${DB} yet`)).toBeVisible()
    await expect(page.getByText(/No operation has taken longer/)).toHaveCount(0)
    await expect(
      page.getByRole("button", { name: "Read the recorded operations again" }),
    ).toBeVisible()
  })

  test("the collections the work is on are listed by what the server has counted on each", async ({
    page,
  }) => {
    const mongo = await mockMongo(page)
    await page.goto(PERFORMANCE)
    const busiest = page.locator("[data-slot=mongo-busiest]")
    // Before a second reading there is no rate: the bars are what each has been asked in all.
    await expect(busiest).toContainText("since the server started")
    // Busiest first: 620 reads and 10 writes on users, 40 and 10 on each of the others.
    await expect(busiest.getByRole("listitem").first()).toContainText("users")
    await expect(busiest.getByRole("listitem").first()).toContainText("630")
    // Views and the server's own collections are not watched.
    const watched = mongo
      .asked("POST /mongo/aggregate/preview")
      .map((call) => String(call.body.collection))
    expect(new Set(watched)).toEqual(new Set(["users", "orders", "sessions", "products"]))
    await busiest.getByRole("button", { name: /users/ }).click()
    await expect.poll(() => where(page)).toContain("collection=users")
  })

  test("a server that reports no counters is said to have none, and is not drawn as one at rest", async ({
    page,
  }) => {
    await mockMongo(page, {
      silent: true,
      summaries: { 5: { flavor: "ferretdb", flavorLabel: "FerretDB" } },
    })
    await page.goto(PERFORMANCE)
    await expect(page.getByText("FerretDB has no performance counters")).toBeVisible()
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
    await expect(page.locator("[data-slot=mongo-busiest]")).toHaveCount(0)
    // What such a server does report is still there.
    await expect(page.getByText("Databases on this server")).toBeVisible()
    await expect(page.getByRole("group", { name: "Performance views" })).toBeHidden()
  })

  test("the replica set is a view only where the server is a member of one", async ({ page }) => {
    await mockMongo(page)
    await page.goto(PERFORMANCE)
    const strip = page.getByRole("group", { name: "Performance views" })
    await expect(strip.getByRole("button", { name: "Operations", exact: true })).toBeVisible()
    await expect(strip.getByRole("button", { name: "Replica set" })).toHaveCount(0)
    await page.unrouteAll({ behavior: "ignoreErrors" })

    await mockMongo(page, { topology: "replicaset" })
    await page.goto(`${PERFORMANCE}?view=replication`)
    const members = page.locator("[data-slot=mongo-member]")
    await expect(members).toHaveCount(2)
    await expect(members.nth(0)).toContainText("Primary")
    await expect(members.nth(1)).toContainText("42s")
    await expect(page.getByText("Reaches back")).toBeVisible()
  })

  test("a server that does not answer is said, with the way to try again", async ({ page }) => {
    const mongo = await mockMongo(page, { failing: /^\/mongo\/server/ })
    await page.goto(PERFORMANCE)
    await expect(
      page.getByRole("alert").filter({ hasText: "the server did not answer" }),
    ).toBeVisible()
    mongo.heal()
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
      // The code editor draws its own cursors and scrollbars.
      if (el.closest(".monaco-editor")) continue
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
      if (item.closest(".monaco-editor")) continue
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
async function expectTheRules(page: Page, where_: string) {
  await page.waitForLoadState("networkidle")
  expect(await unnamedControls(page), `unlabelled icon-only controls on ${where_}`).toEqual([])
  expect(await offCentreText(page), `text off its row's centre line on ${where_}`).toEqual([])
  expect(await filledPills(page), `fully rounded filled chips on ${where_}`).toEqual([])
  const registers = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("[data-slot='page']")].map(
      (el) => el.dataset.register ?? "(unset)",
    ),
  )
  expect(registers, `${where_} declares one register, and it is reading`).toEqual(["reading"])
  expect(
    await page.locator("[data-slot='flow-panel']").count(),
    `${where_} has no flow panel`,
  ).toBe(0)
  const sideways = await page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways, `${where_} scrolls sideways`).toBe(false)
  // A page's view strip is a group of pressed buttons. A navigation landmark
  // named for views would be the route-level strip the product does without.
  expect(
    await page.getByRole("navigation", { name: /views$/ }).count(),
    `${where_} has a view strip that is a landmark`,
  ).toBe(0)
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the MongoDB pages keep the design system's rules${label}`, async ({ page }) => {
    test.setTimeout(180_000)
    await page.setViewportSize(viewport)
    await mockMongo(page, {
      topology: "replicaset",
      rows: { 5: { environment: "production" } },
      profiler: { level: 1, entries: [] },
    })
    const phone = viewport.width < 500

    // Documents: no collection open, then one in each of its three views.
    await page.goto(`${DATA}?db=${DB}`)
    await expect(rail(page)).toBeVisible()
    await expectTheRules(page, "the database's pane")
    for (const view of ["list", "json", "table"]) {
      await page.goto(collection("users", `&view=${view}`))
      await expect(page.locator("[data-slot=mongo-documents]")).toBeVisible()
      await expectTheRules(page, `documents as ${view}`)
    }

    // A staged edit, the editor, and a confirmation over the documents.
    await page.goto(collection("users"))
    const first = documents(page).first()
    await fieldAction(first, "age", "Edit")
    await expectTheRules(page, "a field being edited")
    await first.getByLabel("Value of age").fill("21")
    await page.keyboard.press("Enter")
    await expectTheRules(page, "a staged edit")
    await first.getByRole("button", { name: "Discard" }).click()
    await first.getByRole("button", { name: "Edit", exact: true }).click()
    await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 15_000 })
    await expectTheRules(page, "the document editor")
    await page.keyboard.press("Escape")
    await page.getByRole("button", { name: "Options" }).click()
    await expectTheRules(page, "the query bar's options")
    await page.getByRole("button", { name: "More actions for users" }).click()
    await page.getByRole("menuitem", { name: "Delete documents…" }).click()
    await expectTheRules(page, "the bulk delete dialog")
    await page.keyboard.press("Escape")
    if (!phone) {
      await rail(page).getByRole("button", { name: "New collection" }).click()
      await expect(page.getByRole("dialog", { name: "New collection" })).toBeVisible()
      await expectTheRules(page, "the new collection dialog")
      await page.keyboard.press("Escape")
    }

    // Aggregations: empty, with stages and a result, and the console.
    await page.goto(`${QUERY}?db=${DB}&collection=orders`)
    await expect(page.getByText("No stages yet")).toBeVisible()
    await expectTheRules(page, "the empty builder")
    await page.getByRole("button", { name: "Paste a pipeline" }).click()
    await page
      .locator("#mongo-pipeline-text")
      .fill('[ { $match: { status: "paid" } }, { $out: "copy" } ]')
    await page.getByRole("button", { name: "Use these stages" }).click()
    await expect(page.locator("[data-slot=mongo-stage]")).toHaveCount(2)
    await expectTheRules(page, "the builder with stages")
    await page.goto(`${QUERY}?db=${DB}&view=command`)
    await page.getByLabel("Command", { exact: true }).fill("{ ping: 1 }")
    await page.getByRole("button", { name: "Run", exact: true }).click()
    await expect(page.getByRole("log")).toContainText("ok")
    await expectTheRules(page, "the console")

    // Schema: before and after a sample, the indexes, the rule.
    await page.goto(`${SCHEMA}?db=${DB}&collection=users`)
    await expectTheRules(page, "the schema before a sample")
    await page.getByRole("button", { name: "Analyze", exact: true }).click()
    await expect(page.locator("[data-slot=mongo-schema-field]")).toHaveCount(4)
    await expectTheRules(page, "the schema analysis")
    await page.goto(`${SCHEMA}?db=${DB}&collection=users&view=indexes`)
    await expect(page.locator("[data-slot=mongo-index]")).toHaveCount(2)
    await expectTheRules(page, "the indexes")
    await page.getByRole("button", { name: "Create index" }).click()
    await expectTheRules(page, "the create index dialog")
    await page.keyboard.press("Escape")
    await page.goto(`${SCHEMA}?db=${DB}&collection=products&view=validation`)
    await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 15_000 })
    await page.getByRole("button", { name: "Check", exact: true }).click()
    await expect(page.getByRole("region", { name: "Result of the check" })).toBeVisible()
    await expectTheRules(page, "the validation rule")

    for (const view of ["", "operations", "slow", "replication"]) {
      await page.goto(view ? `${PERFORMANCE}?view=${view}` : PERFORMANCE)
      await expect(page.locator("[data-slot=stat-tile]").first()).toBeVisible()
      await expectTheRules(page, `performance ${view || "overview"}`)
    }
  })
}
