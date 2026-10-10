import { expect, test, type Page } from "@playwright/test"
import { json, mockHost } from "./host-fixture"
import { deploymentRequests } from "./deploy-fixture"
import { POSTGRES_RUNS } from "./processes-logs-fixture"

/**
 * `/logs` with everything the service pages have, in one place: a source's
 * own page views — a container's Events, a unit's Runs, a saved database's
 * Queries, a site's Requests — in the same strip as Live, History and
 * Insights, and the Requests group of deployment and site request records
 * in the rail, read in the workbench's own column. Each view's own rules are
 * the service pages' specs; this checks that `/logs` offers them, asks the
 * server the same questions, and that what they open lands on `/logs`.
 */

const now = Date.now()
const iso = (ms: number) => new Date(now - ms).toISOString()

const DB_ID = "3f2a9c1d0b7e5a6f7081928374655647382910abcdefabcdefabcdef012345"
const APP_ID = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"
const ACCESS_LOG = "/var/log/nginx/shop.access.log"
const ERROR_LOG = "/var/log/nginx/shop.error.log"

const index = {
  sources: [
    {
      id: "file:/var/log/syslog",
      label: "syslog",
      kind: "system",
      path: "/var/log/syslog",
      size: 90_112,
      lens: "syslog",
      rotated: true,
    },
    {
      id: `docker:${DB_ID}`,
      label: "shop-db",
      kind: "docker",
      status: "running",
      detail: "shop · postgres:16",
      lens: "postgres",
      rotated: false,
    },
    {
      id: `docker:${APP_ID}`,
      label: "api-production-r20",
      kind: "docker",
      status: "running",
      detail: "ghcr.io/acme/api:20",
      lens: "app",
      rotated: false,
    },
    {
      id: "journal:",
      label: "systemd journal",
      kind: "journal",
      lens: "syslog",
      rotated: false,
    },
    {
      id: `file:${ACCESS_LOG}`,
      label: "shop.access.log",
      kind: "nginx",
      path: ACCESS_LOG,
      size: 4096,
      lens: "http-access",
      rotated: false,
    },
    {
      id: `file:${ERROR_LOG}`,
      label: "shop.error.log",
      kind: "nginx",
      path: ERROR_LOG,
      size: 1024,
      lens: "nginx-error",
      rotated: false,
    },
  ],
  units: [
    { name: "postgresql.service", description: "PostgreSQL RDBMS", active: "failed", lens: "app" },
  ],
  roots: ["/var/log"],
  missing: {},
}

const result = (lines: object[], extra: object = {}) => ({
  lines,
  scanned: lines.length * 40,
  matched: lines.length,
  truncated: false,
  complete: true,
  files: [],
  histogram: [],
  tookMillis: 3,
  ...extra,
})

/** The database container's last exit, as the event record keeps it. */
const exited = {
  time: iso(5 * 60_000),
  type: "container",
  action: "die",
  name: "shop-db",
  id: DB_ID,
  image: "postgres:16",
  exitCode: "1",
  message: "shop-db exited with status 1",
  level: "error",
  source: "daemon",
}

const connection = {
  id: 1,
  name: "shop",
  driver: "postgres",
  host: "shop-db",
  port: "5432",
  user: "app",
  database: "shop",
  createdAt: iso(86_400_000),
  ok: true,
  latencyMs: 2,
  bytes: 0,
  sizesKnown: true,
  objects: 12,
  objectWord: "tables",
  sessions: 3,
  source: "docker",
  container: "shop-db",
  exposure: "internal",
  consumers: 1,
}

const SLOW_SQL = "SELECT o.id FROM orders o JOIN customers c ON c.id = o.customer_id"

const deployment = {
  id: 7,
  name: "api-production",
  profile: "web",
  environmentId: 12,
  environmentName: "Production",
  environmentKind: "production",
  desiredRevision: 3,
  liveReleaseId: 20,
  strategy: "blue_green",
  expectedDowntime: false,
  sourceKind: "image",
  sourceRepository: "ghcr.io/acme/api",
  buildMethod: "none",
  endpoint: "api.example.com",
  health: "healthy",
  pendingChanges: false,
  updatedAt: iso(3_600_000),
}

const site = {
  name: "shop",
  kind: "nginx",
  path: "/etc/nginx/sites-available/shop",
  enabled: true,
  serverNames: ["shop.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  accessLogPath: ACCESS_LOG,
  errorLogPath: ERROR_LOG,
  modified: iso(86_400_000),
  size: 900,
}

/** A site's window as nginx's combined format records it: no host, no duration. */
function siteWindow(url: URL) {
  const window = deploymentRequests(url)
  return {
    ...window,
    driver: "nginx",
    format: "nginx-combined",
    latency: false,
    ingress: undefined,
    entries: window.entries.map((entry) => ({
      ...entry,
      status: entry.status === 500 ? 502 : entry.status,
      durationMs: undefined,
    })),
  }
}

type Mocks = { searches: URL[]; asked: URL[] }

async function mockViews(page: Page): Promise<Mocks> {
  const mocks: Mocks = { searches: [], asked: [] }
  await mockHost(page)
  await page.route("**/api/v1/**", (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path.startsWith("/logs/")) {
      if (path === "/logs/sources") return json(route, index)
      if (path === "/logs/search") {
        mocks.searches.push(url)
        if (url.searchParams.get("lens") === "systemd") {
          return json(route, result(POSTGRES_RUNS, { lens: "systemd" }))
        }
        if (url.searchParams.get("source") === `file:${ERROR_LOG}`) {
          return json(
            route,
            result(
              [
                {
                  text: '2026/09/03 11:59:31 [error] 812#812: *41 connect() failed (111: Connection refused) while connecting to upstream, client: 198.51.100.23, request: "POST /api/checkout HTTP/1.1"',
                  timestamp: "2026-09-03T11:59:31Z",
                  level: "error",
                  event: "upstream_refused",
                  attrs: { client: "198.51.100.23", method: "POST", path: "/api/checkout" },
                },
              ],
              { lens: "nginx-error" },
            ),
          )
        }
        return json(route, result([]))
      }
      return json(route, {})
    }
    mocks.asked.push(url)
    if (path === "/docker/events") {
      return json(route, { events: [exited], listening: true, since: iso(86_400_000), buffered: 1 })
    }
    if (path === `/docker/containers/${DB_ID}/raw`) return json(route, { State: {} })
    if (path === "/databases/fleet") {
      return json(route, {
        connections: [connection],
        unreachable: [],
        needsCredentials: [],
        checkedAt: iso(0),
      })
    }
    if (path === "/databases/1/querylog") {
      return json(route, {
        supported: true,
        source: "log",
        entries: [
          {
            at: iso(2 * 60_000),
            durationMs: 1843.221,
            query: SLOW_SQL,
            fp: "3f2a9c1d0b7e",
            user: "app",
            db: "shop",
          },
        ],
        truncated: false,
      })
    }
    if (path === "/deploy/" && url.searchParams.get("view") === "fleet") {
      return json(route, {
        deployments: [deployment],
        activeWork: [],
        slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
      })
    }
    if (path === "/deploy/traffic") {
      return json(route, {
        7: { status: "available", perMinute: 21.4, errorRate: 0.01, pages: 402, points: [] },
      })
    }
    if (path === "/proxy/vhosts") return json(route, [site])
    if (path === "/deploy/7/requests") return json(route, deploymentRequests(url))
    if (path === "/deploy/7") {
      return json(route, {
        project: { id: 7, name: "api-production" },
        running: true,
        deployment,
        runtime: {
          status: "available",
          observedAt: iso(0),
          services: [
            {
              containerId: APP_ID,
              name: "api-production-r20",
              releaseId: 20,
              liveRelease: true,
              state: "running",
              health: "healthy",
              imageId: "sha256:abc",
              image: "ghcr.io/acme/api:20",
            },
          ],
        },
      })
    }
    if (path === "/deploy/7/environments/12/releases") {
      return json(route, [
        {
          id: 20,
          projectId: 7,
          environmentId: 12,
          number: 20,
          runId: 84,
          state: "live",
          planRevision: 2,
          configDigest: "c",
          variablesDigest: "v",
          strategy: "blue_green",
          expectedDowntime: false,
          createdAt: "2026-09-03T09:00:00Z",
          activatedAt: "2026-09-03T10:00:00Z",
        },
      ])
    }
    if (path === "/proxy/sites/shop/requests") return json(route, siteWindow(url))
    if (path === "/proxy/sites/shop") {
      return json(route, {
        spec: {
          name: "shop",
          kind: "proxy",
          accessLog: true,
          accessLogPath: ACCESS_LOG,
          errorLogPath: ERROR_LOG,
        },
        managed: true,
        content: "",
        warnings: [],
      })
    }
    return route.fallback()
  })
  // Neither live stream has anything to say for these views.
  await page.routeWebSocket(
    /\/api\/v1\/(logs|docker\/events|deploy\/7\/requests|proxy\/sites\/shop\/requests)\/stream/,
    () => {},
  )
  return mocks
}

const strip = (page: Page) => page.getByRole("navigation", { name: "Log mode" })

test("/logs offers a container's Events and, for a saved database's server, its Queries", async ({
  page,
}) => {
  const mocks = await mockViews(page)
  await page.goto(`/logs?source=docker:${DB_ID}`)

  // The container's own views, in the strip the log's readings are in.
  const events = strip(page).getByRole("button", { name: "Events", exact: true })
  await expect(events).toBeVisible()
  await events.click()
  await expect(events).toHaveAttribute("aria-pressed", "true")
  const feed = page.getByRole("region", { name: "Container events" })
  await expect(feed.getByText("shop-db exited with status 1")).toBeVisible()
  const asked = mocks.asked.find((url) => url.pathname.endsWith("/docker/events"))!
  expect(asked.searchParams.get("container")).toBe(DB_ID)
  await expect(page).toHaveURL(/[?&]mode=events/)

  // The fleet names the container as a saved connection's server, so its
  // statements are a view of the same log.
  const queries = strip(page).getByRole("button", { name: "Queries", exact: true })
  await queries.click()
  await expect(page.getByText(SLOW_SQL).first()).toBeVisible()
  expect(mocks.asked.some((url) => url.pathname.endsWith("/databases/1/querylog"))).toBe(true)
  await expect(page).toHaveURL(/[?&]mode=queries/)

  // A view is a place in the address like Live and History are.
  await page.reload()
  await expect(strip(page).getByRole("button", { name: "Queries", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  // A source with no such view reads as Live rather than as an empty pane.
  await page
    .locator('[aria-label="Log sources"]')
    .getByRole("button", { name: /^syslog/ })
    .click()
  await expect(strip(page).getByRole("button", { name: "Live", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(strip(page).getByRole("button", { name: "Queries", exact: true })).toHaveCount(0)
})

test("/logs offers a unit's Runs, and a run opens its own lines in History", async ({ page }) => {
  const mocks = await mockViews(page)
  await page.goto("/logs?source=journal:postgresql.service&mode=runs")

  const runs = page.getByRole("list", { name: "Runs" })
  await expect(runs.locator(":scope > li")).toHaveCount(3)
  const read = mocks.searches.find((url) => url.searchParams.get("lens") === "systemd")!
  expect(read.searchParams.get("source")).toBe("journal:postgresql.service")
  await expect(runs.locator(":scope > li").first()).toContainText("start limit hit")

  const before = mocks.searches.length
  await runs.locator(":scope > li").first().getByRole("button").first().click()
  await expect(strip(page).getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect
    .poll(() =>
      mocks.searches
        .slice(before)
        .find((url) => url.searchParams.getAll("f").includes("invocation:c4")),
    )
    .toBeTruthy()
  await expect(page).toHaveURL(/source=journal%3Apostgresql\.service/)
})

test("/logs lists the request records, and reads a deployment's in the workbench", async ({
  page,
}) => {
  const mocks = await mockViews(page)
  await page.goto("/logs?source=file:/var/log/syslog")

  const rail = page.locator('[aria-label="Log sources"]')
  await expect(rail.getByText("Requests", { exact: true })).toBeVisible()
  // A deployment by the address it answers at, a site by its name's.
  const record = rail.getByRole("button").filter({ hasText: "api.example.com" })
  await expect(record).toContainText("21.4/min")
  await expect(rail.getByRole("button").filter({ hasText: "shop.example.com" })).toBeVisible()
  await record.click()
  await expect(record).toHaveAttribute("aria-current", "true")

  // Its own column, in the workbench's strip: Requests and Insights.
  await expect(strip(page).getByRole("button", { name: "Requests" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByText("/api/checkout", { exact: true }).first()).toBeVisible()
  await expect(page).toHaveURL(/[?&]requests=deploy%3A7/)
  await expect(page).not.toHaveURL(/[?&]source=/)

  // A failed request opens on what its container and its proxy wrote.
  await page.getByText("/api/checkout", { exact: true }).first().click()
  await expect(page.getByRole("region", { name: "Proxy said" })).toBeVisible()
  await expect(page.getByRole("region", { name: "Lines from this request" })).toBeVisible()
  const lines = mocks.searches.find((url) => url.searchParams.get("source") === `docker:${APP_ID}`)!
  expect(lines.searchParams.get("order")).toBe("asc")

  // And the container's log opens beside it, on History around the moment.
  const before = mocks.searches.length
  await page.getByRole("button", { name: "Open in Output" }).click()
  await expect(rail.getByRole("button", { name: /^api-production-r20/ })).toHaveAttribute(
    "aria-current",
    "true",
  )
  await expect(strip(page).getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect
    .poll(() =>
      mocks.searches
        .slice(before)
        .find((url) => url.searchParams.get("source") === `docker:${APP_ID}`),
    )
    .toBeTruthy()
  const around = mocks.searches
    .slice(before)
    .find((url) => url.searchParams.get("source") === `docker:${APP_ID}`)!
  const t = Date.parse("2026-09-03T11:59:31Z")
  expect(Date.parse(around.searchParams.get("since")!)).toBe(t - 60_000)
  expect(Date.parse(around.searchParams.get("until")!)).toBe(t + 60_000)

  // Insights is the record's own, and a link carries it.
  await page.goto("/logs?requests=deploy:7&mode=insights")
  await expect(strip(page).getByRole("button", { name: "Insights" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByRole("heading", { name: "Output" })).toBeVisible()
})

test("/logs reads a site's record, and its access log's Requests view, with nginx's errors beside a failure", async ({
  page,
}) => {
  const mocks = await mockViews(page)
  await page.goto("/logs?requests=site:shop")

  await expect(page.getByText("/api/checkout", { exact: true }).first()).toBeVisible()
  expect(mocks.asked.some((url) => url.pathname.endsWith("/proxy/sites/shop/requests"))).toBe(true)
  await page.getByText("/api/checkout", { exact: true }).first().click()
  const said = page.getByRole("region", { name: "Proxy said" })
  await expect(said.getByText("upstream refused")).toBeVisible()

  // The error log opens in the workbench on the minute around the failure.
  await said.getByRole("button", { name: "Open in the error log" }).click()
  const rail = page.locator('[aria-label="Log sources"]')
  await expect(rail.getByRole("button", { name: /^shop\.error\.log/ })).toHaveAttribute(
    "aria-current",
    "true",
  )
  await expect(strip(page).getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page).toHaveURL(/source=file%3A%2Fvar%2Flog%2Fnginx%2Fshop\.error\.log/)
  await expect(page).not.toHaveURL(/[?&]requests=/)

  // The site's access log offers the same record as a view of its own, with
  // the record's export and not a second one for the lines.
  await page.goto(`/logs?source=file:${ACCESS_LOG}&mode=requests`)
  await expect(strip(page).getByRole("button", { name: "Requests" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByText("/api/checkout", { exact: true }).first()).toBeVisible()
  await expect(page.getByRole("link", { name: "Export" })).toHaveCount(1)
  await expect(page.getByRole("button", { name: "Export" })).toHaveCount(0)
})

test("workspace: Back returns from a unit run's question to its Runs view", async ({ page }) => {
  await mockViews(page)
  let reads = 0
  await page.route("**/api/v1/logs/search**", (route) => {
    reads++
    return json(route, result(POSTGRES_RUNS, { lens: "systemd" }))
  })
  await page.goto("/logs?source=journal:postgresql.service&mode=runs")
  const runs = page.getByRole("list", { name: "Runs" })
  await expect(runs.locator(":scope > li")).toHaveCount(3)
  const beforeRefresh = reads
  await page.keyboard.press("F5")
  await expect.poll(() => reads).toBeGreaterThan(beforeRefresh)
  await expect(runs.locator(":scope > li")).toHaveCount(3)
  await runs.locator(":scope > li").first().getByRole("button").first().click()
  await expect(strip(page).getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  const first = page.getByLabel("Log lines").locator("[data-workspace-item]").first()
  await expect(first).toBeVisible()
  const identity = await first.getAttribute("data-workspace-item")
  await first.click()
  await expect(page.locator("[data-line-detail]")).toHaveCount(1)
  await page.goBack()
  await expect(strip(page).getByRole("button", { name: "Runs", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(runs.locator(":scope > li")).toHaveCount(3)
  await page.goForward()
  await expect(strip(page).getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.locator(`[data-workspace-item="${identity}"]`)).toBeFocused()
  await expect(page.locator("[data-line-detail]")).toHaveCount(1)
})

test("workspace: match navigation wraps and Escape closes the inspected record", async ({
  page,
}) => {
  await mockViews(page)
  await page.route("**/api/v1/logs/search**", (route) =>
    json(
      route,
      result(
        [0, 1, 2].map((index) => ({
          ...POSTGRES_RUNS[0],
          text: `needle in record ${index}`,
          timestamp: new Date(now - index * 1000).toISOString(),
          attrs: {},
        })),
      ),
    ),
  )
  await page.goto("/logs?source=journal:postgresql.service&mode=search&q=needle&lens=none")
  const rows = page.getByLabel("Log lines").locator("[data-workspace-item]")
  await expect(rows).toHaveCount(3)
  await page.getByRole("button", { name: "Previous match", exact: true }).click()
  await expect(rows.last()).toBeFocused()
  await page.keyboard.press("F3")
  await expect(rows.first()).toBeFocused()
  await page.keyboard.press("F3")
  await expect(rows.nth(1)).toBeFocused()
  await page.keyboard.press("Shift+F3")
  await expect(rows.first()).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page.locator("[data-line-detail]")).toHaveCount(1)
  await page.keyboard.press("Escape")
  await expect(page.locator("[data-line-detail]")).toHaveCount(0)
  await expect(rows.first()).toBeFocused()
})
