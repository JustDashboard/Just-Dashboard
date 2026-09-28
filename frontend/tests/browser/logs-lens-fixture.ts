import type { Page } from "@playwright/test"
import { json, mockHost } from "./host-fixture"

/**
 * One host's logs read through their lenses: a Postgres server log whose
 * lines the server has already named (`event`), read (`attrs`) and joined
 * into records (`cont`), beside the files and the journal the rail lists.
 * The lines are the research's real ones (`research-db-logs.md`), so what
 * the console draws is what a Postgres 16 server writes.
 *
 * Every socket and every search is recorded, so a spec can say which
 * question a press sent to the server rather than only what came back.
 */

export const PG = "file:/var/log/postgresql/postgresql-16-main.log"

export const sources = {
  sources: [
    {
      id: "file:/var/log/syslog",
      label: "syslog",
      kind: "system",
      path: "/var/log/syslog",
      size: 90_112,
      modified: "2026-09-27T10:06:30Z",
      lens: "syslog",
      rotated: true,
    },
    {
      id: "file:/var/log/auth.log",
      label: "auth.log",
      kind: "system",
      path: "/var/log/auth.log",
      size: 10_240,
      modified: "2026-09-27T10:06:30Z",
      lens: "auth",
      rotated: false,
    },
    {
      id: PG,
      label: "postgresql-16-main.log",
      kind: "app",
      path: "/var/log/postgresql/postgresql-16-main.log",
      size: 40_038,
      modified: "2026-09-27T10:06:30Z",
      archives: 3,
      archiveBytes: 120_000,
      lens: "postgres",
      rotated: true,
    },
    {
      id: "stack:shop",
      label: "shop",
      kind: "stack",
      detail: "3 services",
      status: "running",
      images: ["postgres:16", "redis:7", "node:22-alpine"],
      rotated: false,
    },
    {
      id: "journal:",
      label: "systemd journal",
      kind: "journal",
      detail: "Every unit on the host — pick one below to narrow it",
      lens: "syslog",
      rotated: false,
    },
  ],
  units: [],
  roots: ["/var/log"],
  missing: {},
}

const at = (clock: string) => `2026-09-27T${clock}Z`
const pg = (clock: string, rest: string) => `2026-09-27 ${clock} UTC ${rest}`

const SLOW_QUERY =
  "SELECT o.id, o.total FROM orders o JOIN customers c ON c.id = o.customer_id WHERE c.region = 'eu'"

export const PG_LINES = [
  {
    text: pg("10:00:00.140", "[1] LOG:  database system is ready to accept connections"),
    timestamp: at("10:00:00.140"),
    level: "info",
    event: "ready",
    attrs: { pid: "1" },
  },
  {
    text: pg(
      "10:01:02.001",
      "[913] postgres@shop LOG:  connection authorized: user=postgres database=shop application_name=psql",
    ),
    timestamp: at("10:01:02.001"),
    level: "info",
    event: "authorized",
    attrs: { pid: "913", user: "postgres", db: "shop", app: "psql" },
  },
  {
    text: pg(
      "10:01:03.221",
      `[913] postgres@shop LOG:  duration: 1843.221 ms  statement: ${SLOW_QUERY}`,
    ),
    timestamp: at("10:01:03.221"),
    level: "info",
    event: "slow",
    attrs: {
      pid: "913",
      user: "postgres",
      db: "shop",
      duration_ms: "1843.221",
      query: SLOW_QUERY,
      fp: "3f2a9c1d0b7e",
    },
  },
  {
    text: pg("10:06:01.001", "[914] app@shop ERROR:  deadlock detected"),
    timestamp: at("10:06:01.001"),
    level: "error",
    event: "deadlock",
    attrs: { pid: "914", user: "app", db: "shop", code: "40P01" },
  },
  ...[
    "[914] app@shop DETAIL:  Process 914 waits for ShareLock on transaction 5871; blocked by process 915.",
    "\tProcess 915 waits for ShareLock on transaction 5870; blocked by process 914.",
    "[914] app@shop HINT:  See server log for query details.",
    '[914] app@shop CONTEXT:  while updating tuple (0,7) in relation "stock"',
    "[914] app@shop STATEMENT:  UPDATE stock SET qty = qty - 1 WHERE sku = 'A-1'",
    "\tand the transaction that held it rolled back",
  ].map((rest) => ({
    text: rest.startsWith("\t") ? rest : pg("10:06:01.001", rest),
    timestamp: rest.startsWith("\t") ? undefined : at("10:06:01.001"),
    level: "error",
    cont: true,
  })),
  {
    text: pg(
      "10:06:06.410",
      '[920] app@shop FATAL:  password authentication failed for user "app"',
    ),
    timestamp: at("10:06:06.410"),
    level: "error",
    event: "auth_failed",
    attrs: { pid: "920", user: "app", db: "shop", client: "172.18.0.5", code: "28P01" },
  },
  ...["10:06:10.000", "10:06:20.000", "10:06:30.000"].map((clock) => ({
    text: pg(clock, "[27] LOG:  checkpoint starting: time"),
    timestamp: at(clock),
    level: "info",
    event: "checkpoint",
    attrs: { pid: "27" },
  })),
  {
    text: pg(
      "10:06:31.500",
      '[921] LOG:  automatic vacuum of table "shop.public.orders": index scans: 1',
    ),
    timestamp: at("10:06:31.500"),
    level: "info",
    event: "autovacuum",
    attrs: { pid: "921", table: "shop.public.orders" },
  },
]

const facet = (values: [string, number, number?][], extra: object = {}) => ({
  values: values.map(([value, count, errors = 0]) => ({
    value,
    count,
    errors,
    first: at("10:00:00.140"),
    last: at("10:06:31.500"),
  })),
  distinct: values.length,
  other: 0,
  missing: 0,
  ...extra,
})

const histogram = ["10:00:00", "10:02:00", "10:04:00", "10:06:00"].map((clock, i) => ({
  start: at(clock),
  total: 10 + i * 4,
  counts: { slow: i, deadlock: i === 3 ? 1 : 0, auth_failed: i === 3 ? 1 : 0, checkpoint: 3 },
}))

/** The overview Insights asks for: every lens facet, the measure and the events over time. */
function overview(url: URL) {
  const keys = (url.searchParams.get("facets") ?? "").split(",")
  const all: Record<string, object> = {
    event: facet([
      ["checkpoint", 9],
      ["authorized", 6],
      ["slow", 4],
      ["deadlock", 1, 1],
      ["auth_failed", 1, 1],
    ]),
    level: facet([
      ["info", 20],
      ["error", 2, 2],
    ]),
    user: facet([
      ["postgres", 14],
      ["app", 5, 2],
    ]),
    db: facet([["shop", 19, 2]]),
    client: facet([
      ["172.18.0.5", 3, 1],
      ["10.0.0.4", 2],
    ]),
    app: facet([["psql", 6]]),
    code: facet([
      ["40P01", 1, 1],
      ["28P01", 1, 1],
    ]),
    fp: facet([["3f2a9c1d0b7e", 4]]),
    pattern: facet([
      ["checkpoint starting: time", 9],
      ["connection authorized: user=<*> database=<*> application_name=<*>", 6],
      ["duration: <*> ms  statement: SELECT <*>", 4],
      ["deadlock detected", 1, 1],
    ]),
  }
  return {
    lines: [],
    scanned: 4200,
    matched: 22,
    truncated: false,
    complete: true,
    files: [],
    histogram,
    bucketSeconds: 120,
    histogramBy: url.searchParams.get("histogramBy") ?? undefined,
    tookMillis: 12,
    lens: "postgres",
    facets: Object.fromEntries(keys.filter((key) => all[key]).map((key) => [key, all[key]])),
    measure: url.searchParams.get("measure")
      ? {
          key: "duration_ms",
          count: 4,
          p50: 320,
          p75: 610,
          p90: 1200,
          p95: 1843,
          p99: 1843,
          max: 1843,
          mean: 740,
        }
      : undefined,
  }
}

/** A group's ranking: one key, its values with the sample beside each. */
function group(url: URL) {
  const by = url.searchParams.get("facets") ?? ""
  const values =
    by === "fp"
      ? [
          {
            value: "3f2a9c1d0b7e",
            count: 4,
            errors: 0,
            sum: 2960,
            max: 1843,
            first: at("10:01:03.221"),
            last: at("10:05:40.000"),
            samples: { query: SLOW_QUERY },
          },
        ]
      : by === "client"
        ? [
            {
              value: "172.18.0.5",
              count: 3,
              errors: 3,
              last: at("10:06:06.410"),
              samples: { user: "app" },
            },
          ]
        : [{ value: "40P01", count: 1, errors: 1, last: at("10:06:01.001"), samples: {} }]
  return {
    lines: [],
    scanned: 4200,
    matched: values.reduce((n, v) => n + v.count, 0),
    truncated: false,
    complete: true,
    files: [],
    histogram: [],
    tookMillis: 4,
    facets: { [by]: { values, distinct: values.length, other: 0, missing: 0 } },
  }
}

export type LensMocks = {
  /** Each live socket's query, in the order they opened. */
  sockets: URLSearchParams[]
  /** Each `/logs/search`, in order. */
  searches: URLSearchParams[]
}

export async function mockLensLogs(page: Page): Promise<LensMocks> {
  const recorded: LensMocks = { sockets: [], searches: [] }
  await mockHost(page)
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith("/logs/sources")) return json(route, sources)
    if (url.pathname.endsWith("/logs/retention")) return json(route, {})
    if (url.pathname.endsWith("/logs/source")) {
      const id = url.searchParams.get("source")
      return json(route, sources.sources.find((s) => s.id === id) ?? {})
    }
    if (url.pathname.endsWith("/logs/search")) {
      recorded.searches.push(url.searchParams)
      const facets = url.searchParams.get("facets") ?? ""
      if (facets.split(",").includes("pattern")) return json(route, overview(url))
      if (facets && url.searchParams.get("facetLimit") === "20") return json(route, group(url))
      if (facets || url.searchParams.get("limit") === "1") {
        // A reading's count: the events it names, over the window.
        return json(route, {
          ...overview(url),
          matched: url.searchParams.get("levels") ? 2 : 6,
          histogram,
          histogramBy: url.searchParams.get("histogramBy") ?? undefined,
        })
      }
      // Lines around one: the record before the line and the ones after.
      const after = url.searchParams.get("order") === "asc"
      return json(route, {
        lines: after ? PG_LINES.slice(3, 5) : PG_LINES.slice(1, 3),
        scanned: 40,
        matched: 2,
        truncated: false,
        complete: true,
        files: [],
        histogram: [],
        tookMillis: 1,
      })
    }
    return json(route, {})
  })
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const params = new URL(socket.url()).searchParams
    recorded.sockets.push(params)
    const source = params.get("source")
    const lensed = source === PG
    socket.send(
      JSON.stringify({
        type: "meta",
        data: {
          kind: lensed ? "app" : "system",
          label: source,
          filtered: params.has("f") || params.has("q") || params.has("levels"),
          lens: lensed ? "postgres" : undefined,
        },
        ts: Date.now(),
      }),
    )
    socket.send(JSON.stringify({ type: "logs", data: lensed ? PG_LINES : [], ts: Date.now() }))
  })
  return recorded
}
