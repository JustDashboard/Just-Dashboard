import { chromium, type Page } from "../../../frontend/node_modules/playwright/index.mjs"
import { json, mockHost } from "../../../frontend/tests/browser/host-fixture"
import { PG, sources } from "../../../frontend/tests/browser/logs-lens-fixture"
import { deploymentRequests, mockProject } from "../../../frontend/tests/browser/deploy-fixture"
import { mockNetwork } from "../../../frontend/tests/browser/network-fixture"
import { overrides as dnsOverrides } from "../../../frontend/tests/browser/network-dns-fixture"

// Run against a production build of the tree whose appearance is being recorded:
//   JD_BROWSER_BASE_URL=http://127.0.0.1:<port> bun docs/audits/2026-10-11-log-filters-insights/capture.ts before|after
const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43117"
const phase = process.argv[2] ?? "after"
if (!["before", "after"].includes(phase)) throw new Error("Use before or after")
const directory = import.meta.dir
const browser = await chromium.launch()

/** A Postgres server's day, stamped relative to the moment the page asks. */
const MESSAGES: [string, string, string, Record<string, string>][] = [
  ["info", "authorized", "connection authorized: user=app database=shop application_name=api", { user: "app", db: "shop", app: "api" }],
  ["info", "statement", "statement: SELECT id, total FROM orders WHERE customer_id = 412", { user: "app", db: "shop" }],
  ["info", "checkpoint", "checkpoint starting: time", {}],
  ["warn", "slow", "duration: 1843.221 ms  statement: SELECT o.id FROM orders o JOIN customers c ON c.id = o.customer_id", { user: "app", db: "shop", duration_ms: "1843.221" }],
  ["info", "authorized", "connection authorized: user=postgres database=shop application_name=psql", { user: "postgres", db: "shop", app: "psql" }],
  ["error", "deadlock", "deadlock detected", { user: "app", db: "shop", code: "40P01" }],
  ["info", "disconnection", "disconnection: session time: 0:00:03.120 user=app database=shop", { user: "app", db: "shop" }],
  ["error", "auth_failed", 'password authentication failed for user "admin"', { user: "admin", db: "shop", code: "28P01", client: "203.0.113.55" }],
]

function pgLine(ms: number, i: number) {
  const [level, event, message, attrs] = MESSAGES[i % MESSAGES.length]
  const stamp = new Date(ms)
  const clock = stamp.toISOString().replace("T", " ").replace("Z", " UTC")
  const word = level === "error" ? "ERROR" : level === "warn" ? "WARNING" : "LOG"
  return {
    text: `${clock} [${900 + (i % 40)}] ${attrs.user ?? "postgres"}@shop ${word}:  ${message}`,
    timestamp: stamp.toISOString(),
    level,
    event,
    attrs: { pid: String(900 + (i % 40)), ...attrs },
  }
}

const wave = (i: number) => Math.round(18 + 14 * Math.sin(i / 5) + (i % 7) * 3)

function levelHistogram(now: number, buckets: number, width: number) {
  return Array.from({ length: buckets }, (_, i) => {
    const info = wave(i)
    const warn = i % 9 === 4 ? 4 : i % 5 === 0 ? 1 : 0
    const error = i === 41 ? 9 : i === 42 ? 6 : i % 13 === 0 ? 1 : 0
    return {
      start: new Date(now - (buckets - i) * width * 1000).toISOString(),
      total: info + warn + error,
      counts: { info, warn, error },
    }
  })
}

function eventHistogram(now: number, buckets: number, width: number) {
  return Array.from({ length: buckets }, (_, i) => {
    const authorized = wave(i)
    const slow = i % 9 === 4 ? 4 : i % 6 === 0 ? 1 : 0
    const deadlock = i === 41 ? 6 : 0
    const checkpoint = i % 10 === 0 ? 2 : 0
    return {
      start: new Date(now - (buckets - i) * width * 1000).toISOString(),
      total: authorized + slow + deadlock + checkpoint + 3,
      counts: { authorized, slow, deadlock, checkpoint, statement: 3 },
    }
  })
}

const facet = (values: [string, number, number?][], distinct = values.length) => ({
  values: values.map(([value, count, errors]) => ({
    value,
    count,
    errors: errors ?? 0,
    last: new Date().toISOString(),
  })),
  distinct,
  other: 0,
  missing: 0,
})

const FACETS: Record<string, object> = {
  level: facet([
    ["info", 1162],
    ["warn", 41],
    ["error", 37, 37],
  ]),
  event: facet([
    ["authorized", 702],
    ["statement", 380],
    ["slow", 41],
    ["checkpoint", 40],
    ["deadlock", 22, 22],
    ["auth_failed", 15, 15],
  ]),
  user: facet([
    ["app", 1040, 22],
    ["postgres", 185],
    ["admin", 15, 15],
  ]),
  db: facet([["shop", 1240, 37]]),
  client: facet(
    [
      ["172.18.0.5", 980, 22],
      ["10.0.0.4", 245],
      ["203.0.113.55", 15, 15],
    ],
    7,
  ),
  app: facet([
    ["api", 980],
    ["psql", 185],
  ]),
  code: facet([
    ["40P01", 22, 22],
    ["28P01", 15, 15],
  ]),
  pattern: facet([
    ["connection authorized: user=<*> database=<*> application_name=<*>", 702],
    ["statement: SELECT <*> FROM orders WHERE customer_id = <*>", 380],
    ["duration: <*> ms  statement: SELECT <*>", 41],
    ["checkpoint starting: time", 40],
    ["deadlock detected", 22, 22],
  ]),
}

/** What a reading's count search answers: its own question's lines, not the window's. */
function matchedOf(url: URL) {
  if (url.searchParams.get("limit") !== "1" || url.searchParams.get("facets")) return 1240
  if (url.searchParams.get("levels")) return 37
  const asked = url.searchParams.getAll("f").join(" ")
  for (const [word, n] of [
    ["auth_failed", 15],
    ["deadlock", 22],
    ["slow", 41],
    ["authorized", 702],
    ["checkpoint", 40],
  ] as const) {
    if (asked.includes(word)) return n
  }
  return asked ? 6 : 1240
}

async function mockLogs(page: Page) {
  await mockHost(page)
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    const now = Date.now()
    if (url.pathname.endsWith("/logs/sources")) return json(route, sources)
    if (url.pathname.endsWith("/logs/retention")) return json(route, {})
    if (url.pathname.endsWith("/logs/source")) {
      const id = url.searchParams.get("source")
      return json(route, sources.sources.find((s) => s.id === id) ?? {})
    }
    if (url.pathname.endsWith("/logs/search")) {
      const keys = (url.searchParams.get("facets") ?? "").split(",").filter(Boolean)
      const by = url.searchParams.get("histogramBy") ?? undefined
      const histogram =
        by === "event" ? eventHistogram(now, 60, 60) : levelHistogram(now, 60, 60)
      const lines = Array.from({ length: 120 }, (_, i) => pgLine(now - (120 - i) * 29_000, i))
      return json(route, {
        lines: url.searchParams.get("limit") === "1" ? [] : lines,
        scanned: 48_213,
        matched: matchedOf(url),
        truncated: false,
        complete: true,
        files: [],
        histogram,
        bucketSeconds: 60,
        histogramBy: by,
        tookMillis: 38,
        lens: "postgres",
        facets: Object.fromEntries(keys.filter((k) => FACETS[k]).map((k) => [k, FACETS[k]])),
        measure: url.searchParams.get("measure")
          ? {
              key: "duration_ms",
              count: 41,
              p50: 320,
              p75: 610,
              p90: 1200,
              p95: 1843,
              p99: 2410,
              max: 2410,
              mean: 640,
            }
          : undefined,
      })
    }
    return json(route, {})
  })
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const now = Date.now()
    socket.send(
      JSON.stringify({
        type: "meta",
        data: { kind: "app", label: PG, lens: "postgres" },
        ts: now,
      }),
    )
    // The last minute, then a few lines every second or so: a busy server.
    const tail = Array.from({ length: 90 }, (_, i) => pgLine(now - (90 - i) * 650, i))
    socket.send(JSON.stringify({ type: "logs", data: tail, ts: now }))
    let n = 90
    const timer = setInterval(() => {
      const batch = Array.from({ length: 1 + (n % 3) }, () => pgLine(Date.now(), n++))
      try {
        socket.send(JSON.stringify({ type: "logs", data: batch, ts: Date.now() }))
      } catch {
        clearInterval(timer)
      }
    }, 900)
    socket.onClose(() => clearInterval(timer))
  })
}

const AGENTS = [
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36",
  "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36 Edg/140.0",
  "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Mobile Safari/537.36",
  "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0",
  "Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
]

function richRequests(url: URL) {
  const window = deploymentRequests(url)
  const start = Date.parse("2026-09-03T11:00:00Z")
  const minute = (m: number) => new Date(start + m * 60_000).toISOString()
  window.summary = {
    ...window.summary,
    pages: 402,
    paths: [
      { value: "/", count: 212, errors: 0, p95: 29, bytes: 4_240_000 },
      { value: "/pricing", count: 96, errors: 0, p95: 41, bytes: 1_920_000 },
      { value: "/api/items", count: 402, errors: 0, p95: 61, bytes: 7_396_800 },
      { value: "/docs/getting-started", count: 61, errors: 0, p95: 38, bytes: 960_000 },
      { value: "/api/checkout", count: 92, errors: 13, p95: 2840, bytes: 37_904 },
      { value: "/signup", count: 33, errors: 0, p95: 52, bytes: 330_000 },
    ],
    referers: [
      { value: "www.google.com", count: 88, errors: 0 },
      { value: "github.com", count: 41, errors: 0 },
      { value: "news.ycombinator.com", count: 26, errors: 0 },
      { value: "t.co", count: 9, errors: 0 },
    ],
    agents: [
      { value: "Chrome", count: 612, errors: 9 },
      { value: "Safari", count: 288, errors: 2 },
      { value: "Edge", count: 96, errors: 0 },
      { value: "Firefox", count: 61, errors: 0 },
      { value: "Googlebot", count: 56, errors: 0 },
      { value: "curl", count: 31, errors: 0 },
    ],
    audience: {
      visitors: 87,
      bots: 9,
      humanRequests: 1097,
      botRequests: 131,
      devices: [
        { value: "desktop", count: 52, errors: 1 },
        { value: "phone", count: 29, errors: 0 },
        { value: "tablet", count: 6, errors: 0 },
      ],
      browsers: [
        { value: "Chrome", count: 44, errors: 1 },
        { value: "Safari", count: 27, errors: 0 },
        { value: "Edge", count: 9, errors: 0 },
        { value: "Firefox", count: 7, errors: 0 },
      ],
      systems: [
        { value: "macOS", count: 31, errors: 0 },
        { value: "iOS", count: 21, errors: 0 },
        { value: "Windows", count: 18, errors: 1 },
        { value: "Android", count: 10, errors: 0 },
        { value: "Ubuntu", count: 7, errors: 0 },
      ],
      top: [
        ["89.28.56.43", 0, 14, 41, ["/", "/pricing", "/docs/getting-started", "/signup"], "www.google.com", 0],
        ["216.244.66.235", 1, 9, 22, ["/", "/pricing", "/signup"], "github.com", 0],
        ["17.166.24.113", 2, 7, 30, ["/docs/getting-started", "/api-reference"], "news.ycombinator.com", 1],
        ["57.131.21.87", 3, 5, 18, ["/", "/blog/launch"], "t.co", 0],
        ["100.101.7.12", 4, 4, 12, ["/", "/dashboard"], undefined, 0],
        ["81.196.4.20", 5, 3, 9, ["/pricing", "/"], "www.google.com", 0],
      ].map(([client, agent, pages, requests, journey, referer, errors], i) => ({
        client,
        agent: AGENTS[agent as number],
        requests,
        pages,
        errors,
        refused: 0,
        bytes: (requests as number) * 24_000,
        first: minute(8 + i * 5),
        last: minute(56 - i * 3),
        landing: (journey as string[])[0],
        referer,
        journey,
      })),
    },
    buckets: Array.from({ length: 60 }, (_, i) => {
      const total = 10 + wave(i)
      return {
        start: minute(i),
        total,
        counts: { "2xx": total - 2 - (i === 52 ? 3 : 0), "4xx": 2, "5xx": i === 52 ? 3 : 0 },
        p95: 40 + (i % 11) * 9 + (i === 52 ? 600 : 0),
        bytes: total * 32_000,
        visitors: Math.max(1, Math.round(total / 6 + Math.sin(i / 3) * 2)),
      }
    }),
    first: minute(0),
    last: minute(59),
  }
  return window
}

async function shot(page: Page, name: string, width: number) {
  await page.waitForTimeout(700)
  await page.screenshot({ path: `${directory}/${phase}-${name}-${width}.png` })
}

for (const width of [1440, 390]) {
  const viewport = { width, height: width === 390 ? 844 : 960 }

  // The logs page: Live, History and Insights on a Postgres log.
  for (const [mode, name] of [
    ["live", "logs-live"],
    ["search", "logs-history"],
    ["insights", "logs-insights"],
  ] as const) {
    const page = await browser.newPage({ viewport })
    await mockLogs(page)
    await page.goto(`${base}/logs?source=${encodeURIComponent(PG)}&mode=${mode}`)
    await page.waitForLoadState("networkidle")
    await page.waitForTimeout(mode === "live" ? 3500 : 900)
    await shot(page, name, width)
    if (phase === "after" && width === 1440 && mode === "search") {
      await page.getByRole("button", { name: /^Time window/ }).click()
      await shot(page, "logs-window", width)
      await page.keyboard.press("Escape")
      await page.getByRole("button", { name: "Options" }).click()
      await shot(page, "logs-options", width)
      await page.keyboard.press("Escape")
      await page.getByRole("button", { name: /^error/ }).first().click()
      await page.waitForTimeout(600)
      await shot(page, "logs-errors", width)
    }
    await page.close()
  }

  // A deployment's request Insights.
  const page = await browser.newPage({ viewport, reducedMotion: "reduce" })
  await mockProject(page, { showcase: true })
  await page.route("**/api/v1/deploy/7/requests?*", (route) =>
    json(route, richRequests(new URL(route.request().url()))),
  )
  await page.goto(`${base}/deploy/7/logs?view=insights`)
  await page.waitForLoadState("networkidle")
  await page.waitForTimeout(1200)
  await shot(page, "deploy-insights", width)
  await page.screenshot({
    path: `${directory}/${phase}-deploy-insights-${width}-full.png`,
    fullPage: true,
  })
  if (width === 1440) {
    for (const [heading, name] of [
      ["Visitors", "deploy-visitors"],
      ["Most active", "deploy-people"],
      ["Pages", "deploy-lists"],
    ]) {
      const title = page.getByText(heading, { exact: true }).last()
      if ((await title.count()) === 0) continue
      await title.evaluate((node) => node.scrollIntoView({ block: "start" }))
      await shot(page, name, width)
    }
  }
  await page.close()
}

// The DNS page's own log, at its end: the resolvers it lists, read in the shared pane.
if (phase === "after") {
  const page = await browser.newPage({ viewport: { width: 1440, height: 960 } })
  await mockNetwork(page, [], {
    overrides: { ...dnsOverrides, "/network/traffic/live": { now: 0, series: {} } },
  })
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    const now = Date.now()
    if (url.pathname.endsWith("/logs/source")) return json(route, {})
    if (url.pathname.endsWith("/logs/sources")) return json(route, { sources: [], units: [] })
    if (url.pathname.endsWith("/logs/retention")) return json(route, {})
    return json(route, {
      lines: [],
      scanned: 0,
      matched: 0,
      truncated: false,
      complete: true,
      files: [],
      histogram: levelHistogram(now, 60, 60),
      tookMillis: 3,
    })
  })
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const now = Date.now()
    const words = [
      ["info", "Using degraded feature set UDP instead of UDP+EDNS0 for DNS server 1.1.1.1."],
      ["info", "Positive Trust Anchors: . IN DS 20326 8 2 e06d44b80b8f1d39a95c0b0d7c65d08458e880409bbc683457104237c7f8ec8d"],
      ["warn", "Server returned error NXDOMAIN, mitigating potential DNS violation DVE-2018-0001, retrying transaction with reduced feature level UDP."],
      ["info", "Clock change detected. Flushing caches."],
      ["error", "Failed to send hostname reply: Transport endpoint is not connected"],
    ]
    socket.send(JSON.stringify({ type: "meta", data: { kind: "journal", lens: "systemd" }, ts: now }))
    socket.send(
      JSON.stringify({
        type: "logs",
        data: Array.from({ length: 40 }, (_, i) => ({
          text: words[i % words.length][1],
          level: words[i % words.length][0],
          timestamp: new Date(now - (40 - i) * 1400).toISOString(),
          source: "systemd-resolved",
          program: "systemd-resolved",
        })),
        ts: now,
      }),
    )
  })
  await page.goto(`${base}/network/dns`)
  await page.waitForLoadState("networkidle")
  const title = page.getByRole("heading", { name: "DNS log" }).first()
  if ((await title.count()) > 0) {
    await title.evaluate((node) => node.scrollIntoView({ block: "start" }))
    await page.waitForTimeout(2500)
    await page.screenshot({ path: `${directory}/after-network-dns-log-1440.png` })
  }
  await page.close()
}

await browser.close()
