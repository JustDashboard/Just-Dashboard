import { expect, test, type Page } from "@playwright/test"
import {
  INGRESS_CONTAINER,
  deploymentRequests,
  healthyOperations,
  json,
  mockProject,
  now,
  showcaseRuntime,
  steps,
  user,
} from "./deploy-fixture"
import type { DeploymentRuntimeServices, LogLine } from "../../src/lib/types"

/**
 * The project's Logs page, after it stopped being one pane of container output.
 *
 * The page it replaced was not broken — it was answering a question nobody had.
 * A Next.js production server prints a startup banner and then nothing, so a
 * deployment serving a thousand requests a minute showed thirty-nine lines
 * ending at "Ready in 236ms" and never changed. These tests hold the
 * readings that fixed it: what the ingress served, what the container printed,
 * what the builds said, and what Docker did to it — each on this page.
 */

/** The live release's two services, one stack, and a container of the release before it. */
const WEB = "abc123".padEnd(64, "0")
const DB = "db4567".padEnd(64, "0")
const OLD = "0ld789".padEnd(64, "0")
const stackRuntime: DeploymentRuntimeServices = {
  status: "available",
  observedAt: now,
  services: [
    {
      containerId: WEB,
      name: "api-production-r20",
      releaseId: 20,
      liveRelease: true,
      state: "running",
      health: "healthy",
      imageId: `sha256:${"a".repeat(64)}`,
      image: "ghcr.io/acme/api:2",
      stack: "api-production",
      service: "web",
    },
    {
      containerId: DB,
      name: "api-production-postgres",
      releaseId: 20,
      liveRelease: true,
      state: "running",
      health: "healthy",
      imageId: `sha256:${"d".repeat(64)}`,
      image: "postgres:16-alpine",
      stack: "api-production",
      service: "postgres",
    },
    {
      containerId: OLD,
      name: "api-production-r19",
      releaseId: 19,
      liveRelease: false,
      state: "exited",
      health: "unavailable",
      imageId: `sha256:${"e".repeat(64)}`,
      image: "ghcr.io/acme/api:1",
      stack: "api-production-r19",
      service: "web",
    },
  ],
}

/** What the application printed around the failing checkout, through the app lens. */
const APP_LINES: LogLine[] = [
  {
    text: "Error: connect ECONNREFUSED 10.0.4.7:5432",
    timestamp: "2026-09-03T11:59:30.900Z",
    level: "error",
    stream: "stderr",
    event: "exception",
    attrs: { error: "Error: connect ECONNREFUSED 10.0.4.7:5432" },
  },
  {
    text: "    at TCPConnectWrap.afterConnect [as oncomplete] (node:net:1606:16)",
    timestamp: "2026-09-03T11:59:30.900Z",
    level: "error",
    stream: "stderr",
    cont: true,
  },
  {
    text: "POST /api/checkout 500 in 2811ms",
    timestamp: "2026-09-03T11:59:31.100Z",
    level: "error",
    stream: "stdout",
    event: "request",
    attrs: { method: "POST", path: "/api/checkout", status: "500", class: "5xx" },
  },
]

/** Caddy's own line about the same request, through the caddy lens. */
const CADDY_LINE: LogLine = {
  text: '{"level":"error","ts":1788436771.2,"logger":"http.log.error","msg":"dial tcp 172.18.0.5:3000: connect: connection refused","request":{"host":"api.example.com","uri":"/api/checkout"},"status":502}',
  timestamp: "2026-09-03T11:59:31.200Z",
  level: "error",
  event: "upstream_refused",
  message: "dial tcp 172.18.0.5:3000: connect: connection refused",
  attrs: {
    upstream: "172.18.0.5:3000",
    host: "api.example.com",
    error: "dial tcp 172.18.0.5:3000: connect: connection refused",
  },
}

const searchResult = (lines: LogLine[], extra: Record<string, unknown> = {}) => ({
  lines,
  scanned: lines.length,
  matched: lines.length,
  truncated: false,
  complete: true,
  files: [],
  histogram: [],
  tookMillis: 2,
  ...extra,
})

/** A release of project 7, as the environment's list gives it. */
const release = (id: number, number: number) => ({
  id,
  projectId: 7,
  environmentId: 12,
  number,
  runId: 80 + number,
  state: id === 20 ? "live" : "retained",
  planRevision: number,
  configDigest: `sha256:${"b".repeat(64)}`,
  variablesDigest: `sha256:${"c".repeat(64)}`,
  strategy: "blue_green",
  expectedDowntime: false,
  createdAt: "2026-09-02T12:00:00Z",
  pinned: false,
})

/**
 * The host's log reads, as this page makes them: every search and every live
 * socket recorded, so a test can say which question a press sent.
 */
async function mockOutput(page: Page) {
  const searches: URLSearchParams[] = []
  const sockets: URLSearchParams[] = []
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    if (!url.pathname.endsWith("/logs/search")) return json(route, {})
    searches.push(url.searchParams)
    const source = url.searchParams.get("source")
    if (url.searchParams.get("facets") === "error")
      return json(
        route,
        searchResult([], {
          lens: "app",
          facets: {
            error: {
              values: [
                {
                  value: "Error: connect ECONNREFUSED 10.0.4.7:5432",
                  count: 13,
                  errors: 13,
                  last: "2026-09-03T11:59:30.900Z",
                  samples: { component: "checkout" },
                },
              ],
              distinct: 1,
              other: 0,
              missing: 0,
            },
          },
        }),
      )
    if (url.searchParams.get("facets") === "event")
      return json(route, searchResult([], { lens: "app" }))
    if (source === `docker:${INGRESS_CONTAINER}`)
      return json(route, searchResult([CADDY_LINE], { lens: "caddy" }))
    return json(route, searchResult(APP_LINES, { lens: "app" }))
  })
  await page.routeWebSocket(/\/api\/v1\/logs\/stream/, (socket) => {
    const params = new URL(socket.url()).searchParams
    sockets.push(params)
    socket.onClose(() => {})
    socket.send(
      JSON.stringify({ type: "meta", data: { kind: "docker", lens: "app" }, ts: Date.now() }),
    )
    socket.send(JSON.stringify({ type: "logs", data: APP_LINES, ts: Date.now() }))
  })
  return { searches, sockets }
}

test.describe("a deployment's traffic", () => {
  // The readings count up on an overdamped spring that takes seconds to
  // settle, longer than an assertion waits on a loaded machine; reduced
  // motion draws the figure itself, which is what these tests read.
  test.use({ timezoneId: "UTC", contextOptions: { reducedMotion: "reduce" } })

  test("the page opens on requests, with the readings that say whether anything is wrong", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    // Requests first: it is the only one of the three that answers "is it
    // working" without the reader knowing what to look for.
    await expect(page.getByRole("button", { name: "Requests", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )

    // A rate rather than a count: "1,284" means nothing without the window it
    // was counted over.
    await expect(page.getByText("per minute")).toBeVisible()
    await expect(page.getByText("21.4")).toBeVisible()
    // 13 of 1284 is about 1%, and the reading is the share, not the count.
    await expect(page.getByText("13 of 1,284 answered 5xx")).toBeVisible()
    // The tail, not the mean: a p50 of 24ms hides a p99 of 2.8 seconds.
    await expect(page.getByText("p95 · half answered inside 24ms")).toBeVisible()

    // Each reading carries its hour: the failures as a strip of minutes, the
    // rate and the p95 as lines, and what asked drawn as itself after the name.
    await expect(
      page.getByRole("img", { name: /^Last hour by minute: 1 minute with server errors/ }),
    ).toBeVisible()
    await expect(page.getByRole("img", { name: "Requests over the last hour" })).toBeVisible()
    await expect(page.getByRole("img", { name: "The p95 over the last hour" })).toBeVisible()
    await expect(page.locator('[data-slot="stat-tile"] img[src="/logos/chrome.svg"]')).toBeVisible()
    // Four readings, none of them the container: what it did is the Events
    // tab's, which counts the exit.
    await expect(page.locator('[data-slot="stat-tile"]')).toHaveCount(4)
    await expect(
      page.locator('[data-slot="stat-tile"]').filter({ hasText: "Container" }),
    ).toHaveCount(0)
    await expect(page.getByTitle("1 exit or restart in the last hour")).toBeVisible()
  })

  test("a request row carries its status, its timing and its path, and opens for the rest", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    const failing = page.getByText("/api/checkout").first()
    await expect(failing).toBeVisible()
    await expect(page.getByText("2.84s").first()).toBeVisible()

    // The row opens in place rather than into a drawer over the list: the
    // question is asked while scanning, and a panel that covers the rows makes
    // you close it to ask it again about the next one.
    await failing.click()
    // The agent is a title attribute on the row and text only in the detail,
    // so matching it proves the disclosure opened rather than the row's own
    // client column being visible at this width.
    await expect(page.getByText("Mozilla/5.0 Chrome/140.0")).toBeVisible()
    await expect(page.getByText("Referer")).toBeVisible()
    await expect(
      page.getByRole("button", { name: "Show every request to this path" }),
    ).toBeVisible()
    // The same request, coloured the way the host's log console colours it:
    // the code by its family, a write in the method hue, the path in the path
    // hue — and a failed request's row washed.
    await expect(page.getByText("500", { exact: true }).first()).toHaveClass(/text-destructive/)
    await expect(page.getByText("200", { exact: true }).first()).toHaveClass(/text-success/)
    const row = page.getByRole("button", { name: /\/api\/checkout/ }).first()
    await expect(row.getByText("POST", { exact: true })).toHaveClass(/tag-blue/)
    // The method chips above the rows stay plain: selection is the chip's
    // fill, and one blue chip in a row of grey ones read as the chosen one.
    await expect(
      page.getByRole("button", { name: "POST", exact: true }).getByText("POST"),
    ).not.toHaveClass(/tag-blue/)
    await expect(failing).toHaveClass(/tag-cyan/)
    // Opened, it says who asked in the account pages' words, where the time
    // sits in the window, and the code's word; the rest of its verbs are one
    // menu, each with its sentence.
    await expect(page.getByText("Chrome", { exact: true })).toBeVisible()
    await expect(page.getByText("in the slowest tenth")).toBeVisible()
    await expect(page.getByText("Server error", { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "More actions for this request" }).click()
    await expect(page.getByRole("menuitem", { name: /Copy as curl/ })).toBeVisible()
    await page.getByRole("menuitem", { name: /Show every request from this client/ }).click()
    await expect(page.getByRole("button", { name: "Clear the client filter" })).toBeVisible()

    // The Colour switch is the log console's own: off, a row is drawn as it
    // was written, with only a failure and a refusal still coloured.
    await page.getByRole("button", { name: "Colour" }).click()
    await expect(page.getByRole("button", { name: "Colour" })).toHaveAttribute(
      "aria-pressed",
      "false",
    )
    await expect(page.getByText("200", { exact: true }).first()).not.toHaveClass(/text-success/)
    await page.getByRole("button", { name: "Colour" }).click()
  })

  test("narrowing to a status family is one press, and the chips carry their counts", async ({
    page,
  }) => {
    await mockProject(page)
    const asked: URL[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      asked.push(new URL(route.request().url()))
      await route.fallback()
    })
    await page.goto("/deploy/7/logs")

    const serverErrors = page.getByRole("button", { name: /5xx/ })
    await expect(serverErrors).toBeVisible()
    await serverErrors.click()

    await expect
      .poll(() => asked.some((url) => url.searchParams.get("classes") === "5xx"))
      .toBe(true)
  })

  test("insights say which page is failing, which client is scanning, and where visitors came from", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Insights", exact: true }).click()

    // The failing path carries its own 5xx count and its own p95, so "p95 is
    // 412ms" becomes "p95 is 412ms because of /api/checkout".
    await expect(page.getByText("13 × 5xx · p95 2.84s")).toBeVisible()
    // A client that tried a dozen doors is named as a scanner, not as a visitor.
    await expect(page.getByText("scanner · 12 probes")).toBeVisible()
    await expect(page.getByText("Scanners", { exact: true })).toBeVisible()
    await expect(page.getByText("www.google.com")).toBeVisible()
    // Bots are a share, not a guess.
    await expect(page.getByText("12% bots")).toBeVisible()
    // An admin can block the scanner from the row.
    await expect(page.getByRole("button", { name: "Block" }).first()).toBeVisible()

    // Browsers, crawlers and referrers are drawn as themselves, and the
    // agents reading counts what is a browser as well as what is a bot.
    await expect(page.getByText("12% bots · 88% browsers")).toBeVisible()
    await expect(
      page.locator('[data-slot="bar-list"] img[src="/logos/google.svg"]').first(),
    ).toBeVisible()
    // The distribution is a ladder rather than a line of footer text.
    await expect(page.getByText("Response times", { exact: true })).toBeVisible()
    await expect(page.getByText("mean 78ms")).toBeVisible()

    // Clicking a page narrows the rows to it. Two rows name this path — the
    // Pages list and the Slowest list — and either does.
    await page.getByRole("button", { name: "Show every request to /api/checkout" }).first().click()
    await expect(page.getByLabel("Filter requests by path")).toHaveValue("/api/checkout")
  })

  test("a code, a latency and a client on Insights each narrow the window through the API's own filters", async ({
    page,
  }) => {
    await mockProject(page)
    const asked: URL[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      asked.push(new URL(route.request().url()))
      await route.fallback()
    })
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Insights", exact: true }).click()

    await page.getByRole("button", { name: "Show every 404" }).click()
    await expect
      .poll(() => asked.some((url) => url.searchParams.get("status") === "404"))
      .toBe(true)
    await page.getByRole("button", { name: "Show the requests slower than p95 (412ms)" }).click()
    await expect.poll(() => asked.some((url) => url.searchParams.get("minMs") === "412")).toBe(true)

    // Each narrowing is a chip with its own way back.
    await expect(page.getByRole("button", { name: "Clear the 404 filter" })).toBeVisible()
    await page.getByRole("button", { name: "Clear the slow filter" }).click()
    await expect(page.getByRole("button", { name: "Clear the slow filter" })).toHaveCount(0)
  })

  test("pages only hides what a page load drags in, and the export carries the same window", async ({
    page,
  }) => {
    await mockProject(page)
    const asked: URL[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      asked.push(new URL(route.request().url()))
      await route.fallback()
    })
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Pages only", exact: true }).click()
    await expect
      .poll(() => asked.some((url) => url.searchParams.get("pages") === "true"))
      .toBe(true)
    // The export link is the window's own query, so a download is what the
    // page shows and not the whole record.
    const href = await page.getByRole("link", { name: "Export" }).getAttribute("href")
    expect(href).toContain("/api/v1/deploy/7/requests/export?")
    expect(href).toContain("pages=true")
  })

  test("a failing request shows what its container and its proxy wrote, and opens Output there", async ({
    page,
  }) => {
    await mockProject(page, {
      runtime: {
        status: "available",
        observedAt: new Date().toISOString(),
        services: [
          {
            containerId: "abc123",
            name: "api-production-r20",
            releaseId: 20,
            liveRelease: true,
            state: "running",
            health: "healthy",
            imageId: `sha256:${"a".repeat(64)}`,
          },
        ],
      },
    })
    const { searches } = await mockOutput(page)
    await page.goto("/deploy/7/logs")
    await page.getByText("/api/checkout").first().click()

    // The container's own lines while the request was in flight, in the row
    // that asked — from the moment it arrived (2.84s before it was answered)
    // to a second after, and said to be approximate, since nothing ties a
    // line to a request but the time.
    const lines = page.getByRole("region", { name: "Lines from this request" })
    await expect(lines.getByText("approximate")).toBeVisible()
    await expect(lines.getByText("POST /api/checkout 500 in 2811ms")).toBeVisible()
    const own = searches.find((q) => q.get("source") === "docker:abc123")!
    expect(own.get("since")).toBe("2026-09-03T11:59:27.160Z")
    expect(own.get("until")).toBe("2026-09-03T11:59:32.000Z")
    expect(own.get("order")).toBe("asc")

    // A 5xx also gets what the proxy said: Caddy's error line for the same
    // host while it was in flight and a second either side, read off the
    // ingress through the caddy lens.
    const proxy = page.getByRole("region", { name: "Proxy said" })
    await expect(proxy.getByText(/connection refused/).first()).toBeVisible()
    const caddy = searches.find((q) => q.get("source") === `docker:${INGRESS_CONTAINER}`)!
    expect(caddy.get("lens")).toBe("caddy")
    expect(caddy.getAll("f")).toEqual([
      "event:upstream_refused",
      "event:upstream_timeout",
      "event:error",
      "host:api.example.com",
    ])
    expect(caddy.get("since")).toBe("2026-09-03T11:59:27.160Z")
    expect(caddy.get("until")).toBe("2026-09-03T11:59:32.000Z")

    // The redirect to the host Logs page is gone: the full output is a view
    // of this page, on History at the minute either side.
    await expect(page.getByRole("link", { name: /Container output/ })).toHaveCount(0)
    await page.getByRole("button", { name: "Open in Output", exact: true }).click()
    await expect(page.getByRole("button", { name: "Output", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect(page.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect
      .poll(() =>
        searches.some(
          (q) =>
            q.get("source") === "docker:abc123" &&
            q.get("since") === "2026-09-03T11:58:31.000Z" &&
            q.get("until") === "2026-09-03T12:00:31.000Z",
        ),
      )
      .toBe(true)
    const url = new URL(page.url())
    expect(url.searchParams.get("view")).toBe("output")
    expect(url.searchParams.get("service")).toBe("abc123")
    expect(url.searchParams.get("moment")).toBe("2026-09-03T11:59:31Z")

    // The output's own quick view for its request lines is "HTTP", so the
    // page's Requests view is the one button of that name.
    await expect(page.getByRole("button", { name: /^HTTP/ })).toBeVisible()
    await expect(page.getByRole("button", { name: "Requests", exact: true })).toHaveCount(1)

    // The other question a failure raises: what happened to the container then.
    await page
      .getByRole("navigation", { name: "Log view" })
      .getByRole("button", { name: "Requests", exact: true })
      .click()
    await page.getByText("/api/checkout").first().click()
    await page.getByRole("button", { name: "Container events around this moment" }).click()
    await expect(page.getByRole("button", { name: "Events", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect(page.getByText("two minutes either side")).toBeVisible()
    await page.getByRole("button", { name: "Show everything" }).click()
    await expect(page.getByText("api-production-r20 exited with status 137")).toBeVisible()
  })

  test("the readings say whether anybody will be told, and the chart marks the release", async ({
    page,
  }) => {
    await mockProject(page)
    // A container exit inside the chart's window, so there is a mark to draw;
    // the fixture's own events sit at "now", outside a window fixed in the past.
    await page.route("**/api/v1/deploy/7/lifecycle*", (route) =>
      json(route, {
        status: "available",
        watching: true,
        events: [
          {
            time: "2026-09-03T11:52:00Z",
            type: "container",
            action: "die",
            name: "api-production-r20",
            exitCode: "137",
            message: "api-production-r20 exited with status 137",
            level: "error",
            source: "daemon",
            owner: { "environment-id": "12" },
          },
        ],
      }),
    )
    await page.goto("/deploy/7/logs")
    // A rule is firing: the line says so with its reading, the window that
    // reading is over (not the tiles' hour), and how long.
    await expect(page.getByText("1 firing")).toBeVisible()
    await expect(page.getByText(/4\.2% failing over the last 5 min · began/)).toBeVisible()
    await expect(page.getByRole("link", { name: "Alerts" })).toHaveAttribute(
      "href",
      "/deploy/7/settings/automation#alerts",
    )
    // …and who it tells, in Automation's own words: a rule naming no channel
    // reaches every one of them, and with none added yet nobody hears it.
    await expect(page.getByText("no channels yet — nobody is told")).toHaveClass(/text-warning/)
    // Page views ride on the requests tile, and served bytes have their own.
    await expect(page.getByText("402 page views in the last hour")).toBeVisible()
    await expect(page.getByText("Served", { exact: true })).toBeVisible()
    // The chart is the house one — stacked by status family, with the live
    // release's activation drawn as a mark inside the window.
    await expect(page.getByText("Requests", { exact: true }).first()).toBeVisible()
    await expect(page.getByText("5xx server error").first()).toBeVisible()
    // The p95 chart lives on Insights, where the readings are, not over the rows.
    await expect(page.getByText("Slowest tenth", { exact: true })).toHaveCount(1)
    await page.getByRole("button", { name: "Insights", exact: true }).click()
    await expect(page.getByText("Slowest tenth", { exact: true })).toHaveCount(2)
    await expect.poll(() => page.locator(".recharts-reference-line").count()).toBeGreaterThan(0)
  })

  test("a clean exit is a stop, not a failure", async ({ page }) => {
    await mockProject(page)
    // A routine stop: the server calls an exit with status 0 a notice.
    await page.route("**/api/v1/deploy/7/lifecycle*", (route) =>
      json(route, {
        status: "available",
        watching: true,
        since: new Date(Date.now() - 6 * 3_600_000).toISOString(),
        events: [
          {
            time: new Date(Date.now() - 5 * 60_000).toISOString(),
            type: "container",
            action: "die",
            name: "api-production-r20",
            exitCode: "0",
            message: "api-production-r20 exited cleanly",
            level: "notice",
            source: "docker",
            owner: { "environment-id": "12" },
          },
        ],
      }),
    )
    await page.goto("/deploy/7/logs")

    await page.getByRole("button", { name: "Events", exact: true }).click()
    await expect(page.getByText("api-production-r20 exited cleanly")).toBeVisible()
    // Nothing for the Events tab to count: nothing failed.
    await expect(page.getByTitle(/exits? or restarts? in the last hour/)).toHaveCount(0)
  })

  test("a role that cannot add a rule is pointed at Automation instead of a form", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/alerts", (route) =>
      route.request().method() === "GET"
        ? json(route, { alerts: [], kinds: ["error_rate", "latency", "silence"] })
        : route.fallback(),
    )
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        ...user,
        capabilities: ["read"],
        user: { ...user.user, role: "viewer" },
      }),
    )
    await page.goto("/deploy/7/logs")

    await expect(page.getByText("No alerts", { exact: true })).toBeVisible()
    // The API refuses anyone but an admin, so the form is not offered.
    await expect(page.getByRole("button", { name: "Add alert" })).toHaveCount(0)
    await expect(page.getByRole("link", { name: "Alerts" })).toHaveAttribute(
      "href",
      "/deploy/7/settings/automation#alerts",
    )
  })

  test("a chosen method keeps its chip, which is the way back", async ({ page }) => {
    await mockProject(page)
    // The window's methods are counted after the filter: asked for POST, the
    // server says POST is all there is.
    await page.route("**/api/v1/deploy/7/requests*", (route) => {
      const url = new URL(route.request().url())
      if (url.searchParams.get("methods") !== "POST") return route.fallback()
      return json(route, {
        status: "available",
        latency: true,
        complete: true,
        observedAt: new Date().toISOString(),
        entries: [],
        summary: {
          total: 92,
          scanned: 92,
          classes: { "2xx": 79, "5xx": 13 },
          errorRate: 13 / 92,
          clientErrorRate: 0,
          bytes: 37_000,
          perMinute: 1.5,
          pages: 0,
          methods: [{ value: "POST", count: 92, errors: 13 }],
          statuses: [],
          paths: [],
          hosts: [],
          clients: [],
          agents: [],
          referers: [],
          probes: [],
          scanners: [],
          buckets: [],
          bucketSeconds: 60,
          truncated: false,
        },
        coverage: {
          exists: true,
          held: 92,
          complete: true,
          cursor: 9917,
          refreshedAt: new Date().toISOString(),
        },
      })
    })
    await page.goto("/deploy/7/logs")

    await page.getByRole("button", { name: "POST", exact: true }).click()
    const chosen = page.getByRole("button", { name: "POST", exact: true })
    await expect(chosen).toHaveAttribute("aria-pressed", "true")
    await chosen.click()
    await expect(page.getByRole("button", { name: "POST", exact: true })).toHaveAttribute(
      "aria-pressed",
      "false",
    )
  })

  test("a request row opens from the keyboard", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    const row = page.getByRole("button", { name: /\/api\/checkout/ }).first()
    await expect(row).toHaveAttribute("aria-expanded", "false")
    await row.focus()
    await page.keyboard.press("Enter")
    await expect(row).toHaveAttribute("aria-expanded", "true")
    await expect(
      page.getByRole("button", { name: "Show every request to this path" }),
    ).toBeVisible()
  })

  test("Output reads each service in the page, and the Runtime's Logs lands on its service", async ({
    page,
  }) => {
    await mockProject(page, { runtime: stackRuntime })
    const { sockets } = await mockOutput(page)
    // What the Runtime page's and the game console's "Logs" hand over: a
    // container, and nothing else — which is a request for its output.
    await page.goto(`/deploy/7/logs?service=${DB}`)

    await expect(page.getByRole("button", { name: "Output", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    const picker = page.getByRole("combobox", { name: "Service" })
    await expect(picker).toHaveText(/api-production-postgres/)
    await expect.poll(() => sockets.map((q) => q.get("source"))).toContain(`docker:${DB}`)
    await expect(page.getByText("Error: connect ECONNREFUSED 10.0.4.7:5432")).toBeVisible()

    // Live and History inside the page's pane, and no second Insights: the
    // page has one, the requests', with the output's ranking at its end.
    await expect(page.getByRole("button", { name: "Live", exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Insights", exact: true })).toHaveCount(1)
    await expect(
      page.locator('[data-slot="pane"] [data-slot="pane"]:not([data-flush])'),
    ).toHaveCount(0)

    // The live release's services one at a time, all of them merged, and the
    // release before it after them; the address follows the choice.
    await picker.click()
    const options = page.getByRole("option")
    await expect(options).toHaveText([
      "api-production-r20",
      "api-production-postgres",
      "All services",
      "api-production-r19",
    ])
    await page.getByRole("option", { name: "All services" }).click()
    await expect.poll(() => sockets.map((q) => q.get("source"))).toContain("stack:api-production")
    await expect.poll(() => new URL(page.url()).searchParams.get("service")).toBe("all")

    // A container that is no longer there is said to be gone, never swapped.
    await page.goto("/deploy/7/logs?view=output&service=removed")
    await expect(page.getByText("This log source is no longer available")).toBeVisible()
  })

  test("the Runtime page's Logs opens this page's Output on that container", async ({ page }) => {
    await mockProject(page, { runtime: stackRuntime })
    const { sockets } = await mockOutput(page)
    await page.routeWebSocket(/\/api\/v1\/docker\/containers\/.*\/stats\/stream/, () => {})
    await page.goto("/deploy/7/runtime")
    await page.getByRole("button", { name: "Actions for api-production-postgres" }).click()
    await page.getByRole("menuitem", { name: "Logs", exact: true }).click()

    await expect(page).toHaveURL(new RegExp(`/deploy/7/logs\\?service=${DB}`))
    await expect(page.getByRole("button", { name: "Output", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect.poll(() => sockets.map((q) => q.get("source"))).toContain(`docker:${DB}`)
  })

  test("a deployment nobody routes to opens on its output", async ({ page }) => {
    await mockProject(page, {
      runtime: stackRuntime,
      operations: {
        ...healthyOperations,
        domains: { status: "available", domains: [] },
      },
    })
    await mockOutput(page)
    await page.goto("/deploy/7/logs")
    await expect(page.getByRole("button", { name: "Output", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    // The default is not written into the address: the page at rest is its bare URL.
    await expect(page).toHaveURL(/\/deploy\/7\/logs$/)
  })

  test("Builds lists the runs and shows the chosen one's transcript on the page", async ({
    page,
  }) => {
    await mockProject(page)
    await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
      socket.onClose(() => {})
      socket.send(
        JSON.stringify({
          type: "snapshot",
          data: {
            run: {
              id: 84,
              runNumber: 1,
              projectId: 7,
              environmentId: 12,
              state: "failed",
              operation: "deploy",
              trigger: "manual",
              actor: "operator",
              requestedAt: now,
              claimedAt: now,
              endedAt: now,
              planRevision: 3,
              metadata: {},
            },
            steps,
          },
          ts: Date.now(),
        }),
      )
      socket.send(
        JSON.stringify({
          type: "events",
          data: [
            {
              seq: 20,
              type: "step.log",
              runId: 84,
              stepId: 105,
              ts: now,
              data: {
                stream: "stderr",
                text: "npm ERR! code ELIFECYCLE\nBuild failed: exit status 1\n",
                truncated: false,
              },
            },
          ],
          ts: Date.now(),
        }),
      )
    })
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Builds", exact: true }).click()

    const rail = page.getByRole("navigation", { name: "Builds" })
    await expect(rail.getByRole("button", { name: /#1 Deploy/ })).toHaveAttribute(
      "aria-current",
      "true",
    )
    // The transcript, inline, in the run page's own console.
    await expect(page.getByText("Build failed: exit status 1")).toBeVisible()
    await expect(page.getByRole("textbox", { name: "Search build logs" })).toBeVisible()
    await expect(page.getByRole("link", { name: "Open the run" })).toHaveAttribute(
      "href",
      "/deploy/7/runs/84",
    )
    await expect.poll(() => new URL(page.url()).searchParams.get("view")).toBe("builds")
  })

  test("a crash loop is one row, and an exit carries its last lines", async ({ page }) => {
    // The container is still there, so what it printed is there to read.
    await mockProject(page, { runtime: showcaseRuntime })
    const { searches } = await mockOutput(page)
    const at = (minute: number, second = 0) =>
      `2026-09-03T11:${String(minute).padStart(2, "0")}:${String(second).padStart(2, "0")}Z`
    const event = (action: string, time: string, exitCode?: string) => ({
      time,
      type: "container",
      action,
      name: "api-production-r20",
      id: "c0ffee",
      exitCode,
      message:
        action === "die"
          ? `api-production-r20 exited with status ${exitCode}`
          : "api-production-r20 started",
      level: action === "die" ? "error" : "info",
      source: "daemon",
      owner: { "environment-id": "12" },
    })
    await page.route("**/api/v1/deploy/7/lifecycle*", (route) =>
      json(route, {
        status: "available",
        watching: true,
        since: "2026-09-03T06:00:00Z",
        events: [
          event("die", at(9), "1"),
          event("start", at(8)),
          event("die", at(7, 50), "1"),
          event("start", at(6)),
          event("die", at(5, 55), "1"),
          event("start", at(4)),
          event("die", at(3), "1"),
        ],
      }),
    )
    await page.goto("/deploy/7/logs?view=events")

    const feed = page.getByRole("region", { name: "Container events" })
    // Three exits and three restarts with one code are one incident.
    await expect(feed.getByText("Restarted ×3 in 5 min")).toBeVisible()
    await expect(feed.getByText("· exit 1")).toBeVisible()
    await expect(feed.getByText("api-production-r20 started")).toHaveCount(0)
    await feed.getByRole("button", { name: "6 events" }).click()
    await expect(feed.getByText("api-production-r20 started")).toHaveCount(3)

    // The newest exit opens on the minute before it, read from the container.
    const last = feed.getByRole("region", { name: "Last lines" }).first()
    await expect(last.getByText("Error: connect ECONNREFUSED 10.0.4.7:5432")).toBeVisible()
    const read = searches.find((q) => q.get("source") === "docker:c0ffee")!
    expect(read.get("since")).toBe("2026-09-03T11:08:00.000Z")
    expect(read.get("until")).toBe("2026-09-03T11:09:00Z")
  })

  test("events name the exit code, and say whether the dashboard or Docker did it", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    await page.getByRole("button", { name: "Events", exact: true }).click()
    await expect(page.getByText("api-production-r20 exited with status 137")).toBeVisible()
    // The exit code stays on the row at every width: it is the one fact that
    // changes what you do next.
    const feed = page.getByRole("region", { name: "Container events" })
    await expect(feed.getByText("exit 137")).toBeVisible()
    // The feed reads under the hour it happened in, each event on the thing
    // it happened to with what happened in the corner. The events are minutes
    // old, so for the hour after midnight they span two days and the heading
    // leads with the date ("Oct 5 · 00:00"): this failed every run in that hour.
    await expect(feed.getByText(/(^|· )\d{2}:00$/).first()).toBeVisible()
    // Docker records what happened and never who asked, so "the daemon did
    // this on its own" is the distinction worth drawing.
    await expect(page.getByText("docker itself").first()).toBeVisible()

    // And the other half of that distinction, which the server can only make
    // by laying the event against the audit log. It was unreachable for as
    // long as the deployment feed did not correlate: every row said "docker
    // itself" or nothing, whoever had pressed the button.
    const trigger = page.getByRole("link", { name: "this dashboard" })
    await expect(trigger).toBeVisible()
    await expect(trigger).toHaveAttribute("href", "/audit?action=deploy.run")

    // The release is the reader's next move, so it is a link to the run that
    // put it there rather than a piece of text.
    await expect(page.getByRole("link", { name: "· release 20" }).first()).toHaveAttribute(
      "href",
      "/deploy/7/runs/84",
    )
  })

  test("the events feed can be searched, narrowed to a kind, and followed", async ({ page }) => {
    await mockProject(page)
    const sockets: string[] = []
    page.on("websocket", (socket) => sockets.push(socket.url()))
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Events", exact: true }).click()

    // A deployment owns more than its containers: a database network vanishing
    // under a running release is exactly this feed's business.
    await expect(page.getByText("deleted network jd-db-e12")).toBeVisible()
    await page.getByRole("button", { name: /^Containers/ }).click()
    await expect(page.getByText("deleted network jd-db-e12")).toHaveCount(0)
    await expect(page.getByText("api-production-r20 exited with status 137")).toBeVisible()
    await page.getByRole("button", { name: /^All/ }).click()

    await page.getByLabel("Filter container events").fill("exited")
    await expect(page.getByText("api-production-r20 exited with status 137")).toBeVisible()
    await expect(page.getByText("api-production-r20 started")).toHaveCount(0)

    await page.getByLabel("Filter container events").fill("nothing here")
    await expect(page.getByText("Nothing matches that filter")).toBeVisible()
    await page.getByLabel("Filter container events").fill("")

    // Followed rather than polled: a feed that learns about the restart ten
    // seconds after the chart beside it has drawn the 502s is not on the same
    // timeline as the chart.
    await expect
      .poll(() => sockets.some((url) => url.includes("/deploy/7/lifecycle/stream")))
      .toBe(true)
  })

  test("an empty feed says the record began when the dashboard did", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/lifecycle*", (route) =>
      json(route, {
        status: "available",
        watching: true,
        // The buffer lives in the backend's memory, so this is a restart three
        // minutes ago rather than a deployment that has been steady all week.
        since: new Date(Date.now() - 3 * 60_000).toISOString(),
        events: [],
      }),
    )
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Events", exact: true }).click()

    await expect(page.getByText("Nothing has happened since the dashboard started")).toBeVisible()
    await expect(
      page.getByText(/not yet evidence that the deployment has been steady/),
    ).toBeVisible()
  })

  test("the view and the moment it is scoped to survive a reload", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    await page.getByRole("button", { name: "Events", exact: true }).click()
    await expect.poll(() => new URL(page.url()).searchParams.get("view")).toBe("events")

    await page.getByRole("button", { name: "Requests", exact: true }).click()
    await page.getByText("/api/checkout").first().click()
    await page.getByRole("button", { name: "Container events around this moment" }).click()
    const scoped = new URL(page.url())
    expect(scoped.searchParams.get("view")).toBe("events")
    expect(scoped.searchParams.get("moment")).toBeTruthy()

    // The point of putting it there: the link somebody pastes into a chat
    // lands on the same two minutes it was taken from.
    await page.goto(scoped.toString())
    await expect(page.getByRole("button", { name: "Events", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect(page.getByText("two minutes either side")).toBeVisible()
  })

  test("a moment sent to Events is not Output's, and Output's own comes back with it", async ({
    page,
  }) => {
    await mockProject(page, { runtime: stackRuntime })
    const { searches } = await mockOutput(page)
    await page.goto("/deploy/7/logs")
    await page.getByText("/api/checkout").first().click()
    await page.getByRole("button", { name: "Container events around this moment" }).click()
    await expect(page.getByText("two minutes either side")).toBeVisible()

    // Output was never asked about that minute: it opens live.
    await page.getByRole("button", { name: "Output", exact: true }).click()
    await expect(page.getByRole("button", { name: "Live", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    expect(new URL(page.url()).searchParams.get("moment")).toBeNull()

    // A moment Output was sent is its own, and stays with it for the tab —
    // History on those minutes, still named, after a trip away and back.
    await page
      .getByRole("navigation", { name: "Log view" })
      .getByRole("button", { name: "Requests", exact: true })
      .click()
    await page.getByText("/api/checkout").first().click()
    await page.getByRole("button", { name: "Open in Output", exact: true }).click()
    await expect(page.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await page.goto("/deploy/7")
    await page.goto("/deploy/7/logs?view=output")
    await expect(page.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect
      .poll(() => new URL(page.url()).searchParams.get("moment"))
      .toBe("2026-09-03T11:59:31Z")
    expect(
      searches.some(
        (q) => q.get("since") === "2026-09-03T11:58:31.000Z" && q.get("source") === `docker:${WEB}`,
      ),
    ).toBe(true)
  })

  test("only an address worth blocking is offered the verb", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Insights", exact: true }).click()

    const row = (ip: string) =>
      page
        .locator("li")
        .filter({ has: page.getByRole("button", { name: `Show every request from ${ip}` }) })

    // A scanner is the reason the verb exists.
    await expect(row("203.0.113.55").getByRole("button", { name: "Block" })).toBeVisible()
    // 172.217 is Google. It differs from RFC 1918 space by one octet, and a
    // prefix test on "172." hid the verb for the whole of the public half.
    await expect(row("172.217.0.1").getByRole("button", { name: "Block" })).toBeVisible()
    // These two are inside the network the firewall stands at the edge of, so
    // a deny rule against them is a rule that does nothing.
    await expect(row("172.16.4.9").getByRole("button", { name: "Block" })).toHaveCount(0)
    await expect(row("127.0.0.1").getByRole("button", { name: "Block" })).toHaveCount(0)
  })

  test("a deployment with no request record explains which nothing this is", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/requests*", (route) =>
      json(route, {
        status: "unavailable",
        reason: "This deployment has no public route, so nothing records the requests it serves.",
        latency: false,
        complete: true,
        observedAt: new Date().toISOString(),
        entries: [],
        summary: {
          total: 0,
          scanned: 0,
          classes: {},
          errorRate: 0,
          clientErrorRate: 0,
          bytes: 0,
          perMinute: 0,
          methods: [],
          statuses: [],
          paths: [],
          hosts: [],
          clients: [],
          agents: [],
          buckets: [],
          bucketSeconds: 60,
          truncated: false,
        },
        coverage: {
          exists: false,
          held: 0,
          complete: true,
          cursor: 0,
          refreshedAt: new Date().toISOString(),
        },
      }),
    )
    await page.goto("/deploy/7/logs")

    // "Nothing asked for it" and "nothing is recording" are different
    // sentences, and a page that renders both as an empty table teaches the
    // reader to distrust it.
    await expect(page.getByText("No request record for this deployment")).toBeVisible()
    await expect(page.getByText("no public route")).toBeVisible()
  })

  test("live continues from the window's cursor rather than from a timestamp", async ({ page }) => {
    await mockProject(page)
    const sockets: string[] = []
    page.on("websocket", (socket) => sockets.push(socket.url()))
    await page.goto("/deploy/7/logs")
    await expect(page.getByText("21.4")).toBeVisible()

    await page.getByRole("button", { name: "Live", exact: true }).click()
    // The fixture's window ends at sequence 9917; the socket asks for what
    // comes after it, so nothing between the window read and the socket
    // opening is missed or sent twice.
    await expect
      .poll(() =>
        sockets.some(
          (url) => url.includes("/deploy/7/requests/stream") && url.includes("after=9917"),
        ),
      )
      .toBe(true)
  })

  test("the Agents and Came from lists narrow like every other, and the output's failures close Insights", async ({
    page,
  }) => {
    await mockProject(page, { runtime: stackRuntime })
    const { searches } = await mockOutput(page)
    const asked: URL[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      asked.push(new URL(route.request().url()))
      await route.fallback()
    })
    await page.goto("/deploy/7/logs?view=insights")

    await page.getByRole("button", { name: "Show every request from Chrome" }).click()
    await expect
      .poll(() => asked.some((url) => url.searchParams.get("agent") === "Chrome"))
      .toBe(true)
    await expect(page.getByRole("button", { name: "Clear the agent filter" })).toBeVisible()
    await page
      .getByRole("button", { name: "Show every request that came from www.google.com" })
      .click()
    await expect
      .poll(() => asked.some((url) => url.searchParams.get("referer") === "www.google.com"))
      .toBe(true)
    await page.getByRole("button", { name: "Clear the referer filter" }).click()
    await page.getByRole("button", { name: "Clear the agent filter" }).click()

    // At the end, what the live release's services threw over the same
    // window, ranked by what the exception says.
    const output = page.getByRole("region", { name: "Output" })
    await expect(output.getByText("Error: connect ECONNREFUSED 10.0.4.7:5432")).toBeVisible()
    const ranked = searches.find((q) => q.get("facets") === "error")!
    expect(ranked.get("source")).toBe("stack:api-production")
    expect(ranked.getAll("f")).toEqual(["event:exception"])
    expect(ranked.get("since")).toBeTruthy()
    // A row opens Output around the last time it was thrown.
    // Each row is named by what it says, so a reader hears which exception
    // it is — and the tooltip is where a line longer than the column is read.
    await output
      .getByRole("button", {
        name: 'Read the output around the last "Error: connect ECONNREFUSED 10.0.4.7:5432"',
      })
      .click()
    await expect(page.getByRole("button", { name: "Output", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect
      .poll(() => new URL(page.url()).searchParams.get("moment"))
      .toBe("2026-09-03T11:59:30.900Z")
  })

  test("a band of response times is asked for, and the question survives a reload", async ({
    page,
  }) => {
    await mockProject(page)
    const asked: URL[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      asked.push(new URL(route.request().url()))
      await route.fallback()
    })
    await page.goto("/deploy/7/logs")

    // The chip's name is its word at every width (on a phone only the glyph shows).
    await page.getByRole("button", { name: "Took", exact: true }).click()
    await page.getByLabel("At least").fill("200")
    await page.getByLabel("At most").fill("1000")
    await page.getByRole("button", { name: "Apply" }).click()
    await expect
      .poll(() =>
        asked.some(
          (url) =>
            url.searchParams.get("minMs") === "200" && url.searchParams.get("maxMs") === "1000",
        ),
      )
      .toBe(true)
    await expect(page.getByRole("button", { name: "Clear the fast filter" })).toBeVisible()
    await page.getByLabel("Filter requests by path").fill("/api")

    // The question is in the address, so a link is the same question…
    await expect.poll(() => new URL(page.url()).searchParams.get("path")).toBe("/api")
    const link = new URL(page.url())
    expect(link.searchParams.get("maxMs")).toBe("1000")
    // …and in the tab, so a trip to another view and back keeps it.
    await page.getByRole("button", { name: "Events", exact: true }).click()
    await expect.poll(() => new URL(page.url()).searchParams.get("path")).toBeNull()
    await page.getByRole("button", { name: "Requests", exact: true }).click()
    await expect(page.getByLabel("Filter requests by path")).toHaveValue("/api")
    await page.goto(link.toString())
    await expect(page.getByLabel("Filter requests by path")).toHaveValue("/api")
    await expect(page.getByRole("button", { name: "Clear the slow filter" })).toBeVisible()
  })

  test("the last hour moves with the clock while the page is open", async ({ page }) => {
    await page.clock.install({ time: new Date("2026-09-03T12:00:00Z") })
    await mockProject(page)
    const asked: string[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      const url = new URL(route.request().url())
      if (url.searchParams.get("limit") === "500") asked.push(url.searchParams.get("since") ?? "")
      await route.fallback()
    })
    await page.goto("/deploy/7/logs")
    await expect.poll(() => asked.length).toBeGreaterThan(0)
    // The hour before the page opened (the installed clock keeps running).
    const opened = Date.parse(asked[0]) - Date.parse("2026-09-03T11:00:00Z")
    expect(opened).toBeGreaterThanOrEqual(0)
    expect(opened).toBeLessThan(60_000)
    // Half an hour on the page: "the last hour" is the half hour since as well.
    await page.clock.fastForward("30:00")
    await expect
      .poll(() => Date.parse(asked.at(-1)!) - Date.parse(asked[0]))
      .toBeGreaterThanOrEqual(30 * 60_000)
  })

  test("an alert's link opens the requests around the moment it fired", async ({ page }) => {
    await mockProject(page)
    const asked: URL[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      asked.push(new URL(route.request().url()))
      await route.fallback()
    })
    await page.goto("/deploy/7/logs?view=requests&moment=2026-09-03T11:50:00Z")
    await expect
      .poll(() =>
        asked.some(
          (url) =>
            url.searchParams.get("since") === "2026-09-03T11:20:00.000Z" &&
            url.searchParams.get("until") === "2026-09-03T12:20:00.000Z",
        ),
      )
      .toBe(true)
    // The moment became the window: the address says so, and a reload keeps it.
    await expect.poll(() => new URL(page.url()).searchParams.get("range")).toBe("custom")
    const url = new URL(page.url())
    expect(url.searchParams.get("moment")).toBeNull()
    expect(url.searchParams.get("since")).toBe("2026-09-03T11:20:00.000Z")
    asked.length = 0
    await page.reload()
    await expect
      .poll(() =>
        asked.some(
          (url) =>
            url.searchParams.get("since") === "2026-09-03T11:20:00.000Z" &&
            url.searchParams.get("until") === "2026-09-03T12:20:00.000Z",
        ),
      )
      .toBe(true)
    await expect(page.getByRole("combobox", { name: "Request window" })).toHaveText(/11:20.*12:20/)
    expect(new URL(page.url()).searchParams.get("since")).toBe("2026-09-03T11:20:00.000Z")
  })

  test("a request whose release has since been removed says so, rather than reading another container", async ({
    page,
  }) => {
    await mockProject(page, {
      runtime: {
        status: "available",
        observedAt: now,
        services: [
          {
            containerId: "abc123",
            name: "api-production-r20",
            releaseId: 20,
            liveRelease: true,
            state: "running",
            health: "healthy",
            imageId: `sha256:${"a".repeat(64)}`,
          },
        ],
      },
    })
    // Release #1 went live at eleven and #2 at noon: the failing checkout at
    // 11:59 was #1's, whose containers are gone.
    await page.route("**/api/v1/deploy/7/environments/12/releases*", (route) =>
      json(route, [
        { ...release(20, 2), activatedAt: now },
        { ...release(19, 1), activatedAt: "2026-09-03T11:00:00Z" },
      ]),
    )
    const { searches } = await mockOutput(page)
    await page.goto("/deploy/7/logs")
    await page.getByText("/api/checkout").first().click()

    const lines = page.getByRole("region", { name: "Lines from this request" })
    await expect(lines.getByText(/Release #1 was live then/)).toBeVisible()
    await expect(page.getByRole("region", { name: "Proxy said" })).toBeVisible()
    // The live container did not exist then: its lines would answer another question.
    expect(searches.some((q) => q.get("source") === "docker:abc123")).toBe(false)
    await expect(page.getByRole("button", { name: "Open in Output", exact: true })).toHaveCount(0)
  })

  test("behind nginx, a failure reads the site's error log, and the lines are the second around the answer", async ({
    page,
  }) => {
    await mockProject(page, {
      runtime: {
        status: "available",
        observedAt: now,
        services: [
          {
            containerId: "abc123",
            name: "api-production-r20",
            releaseId: 20,
            liveRelease: true,
            state: "running",
            health: "healthy",
            imageId: `sha256:${"a".repeat(64)}`,
          },
        ],
      },
    })
    const errorLog = "/var/log/nginx/just-dashboard-env-12.error.log"
    await page.route("**/api/v1/deploy/7/requests*", (route) => {
      const body = deploymentRequests(new URL(route.request().url()))
      // nginx's combined format: no host, no duration, and the site's own error file.
      return json(route, {
        ...body,
        driver: "nginx",
        format: "combined",
        latency: false,
        ingress: undefined,
        errorLog,
        entries: body.entries.map((entry) => ({ ...entry, durationMs: undefined })),
        slowest: [],
        summary: { ...body.summary, latency: undefined },
      })
    })
    const { searches } = await mockOutput(page)
    const own: URLSearchParams[] = []
    await page.route("**/api/v1/logs/search*", (route) => {
      const url = new URL(route.request().url())
      if (url.searchParams.get("source") !== "docker:abc123") return route.fallback()
      own.push(url.searchParams)
      return json(route, searchResult([]))
    })
    await page.goto("/deploy/7/logs")
    await page.getByText("/api/checkout").first().click()

    const proxy = page.getByRole("region", { name: "Proxy said" })
    await expect(proxy.getByText("the site's nginx error log")).toBeVisible()
    const read = searches.find((q) => q.get("source") === `file:${errorLog}`)!
    expect(read.get("lens")).toBe("nginx-error")
    // The file is the site's own, and its lines carry no host to match.
    expect(read.getAll("f")).toEqual([])
    expect(read.get("since")).toBe("2026-09-03T11:59:30.000Z")
    expect(read.get("until")).toBe("2026-09-03T11:59:32.000Z")

    // No duration, so no arrival: the second either side of the answer, said as that.
    const lines = page.getByRole("region", { name: "Lines from this request" })
    await expect(
      lines.getByText("The container wrote nothing in the second either side of the answer."),
    ).toBeVisible()
    expect(own[0].get("since")).toBe("2026-09-03T11:59:30.000Z")
    expect(own[0].get("until")).toBe("2026-09-03T11:59:32.000Z")
  })

  test("a deployment nobody routes to still has its output's failures on Insights", async ({
    page,
  }) => {
    await mockProject(page, {
      runtime: stackRuntime,
      operations: { ...healthyOperations, domains: { status: "available", domains: [] } },
    })
    await page.route("**/api/v1/deploy/7/requests*", (route) =>
      json(route, {
        ...deploymentRequests(new URL(route.request().url())),
        status: "unavailable",
        reason: "This deployment has no public route, so nothing records the requests it serves.",
        entries: [],
      }),
    )
    const { searches } = await mockOutput(page)
    await page.goto("/deploy/7/logs?view=insights")

    // Which nothing this is, and the owner's way out of it, above the section.
    await expect(page.getByText("No request record for this deployment")).toBeVisible()
    await expect(page.getByRole("link", { name: "Add a domain" })).toHaveAttribute(
      "href",
      "/deploy/7/settings/domains",
    )
    const output = page.getByRole("region", { name: "Output" })
    await expect(output.getByText("Error: connect ECONNREFUSED 10.0.4.7:5432")).toBeVisible()
    await expect(output.getByRole("heading", { level: 2, name: "Output" })).toBeVisible()
    expect(searches.find((q) => q.get("facets") === "error")!.get("source")).toBe(
      "stack:api-production",
    )
  })

  test("an OOM loop folds, a clean loop is not a failure, and a removed container's last lines are said to be gone", async ({
    page,
  }) => {
    // Nothing of this deployment runs any more, and Docker holds no container of it.
    await mockProject(page, { runtime: { status: "available", observedAt: now, services: [] } })
    const { searches } = await mockOutput(page)
    const at = (minute: number, second = 0) =>
      `2026-09-03T11:${String(minute).padStart(2, "0")}:${String(second).padStart(2, "0")}Z`
    const event = (
      action: string,
      time: string,
      exitCode?: string,
      name = "api-production-r20",
    ) => ({
      time,
      type: "container",
      action,
      name,
      id: name === "api-production-r20" ? "c0ffee" : "b00c1e",
      exitCode,
      message:
        action === "die"
          ? `${name} exited with status ${exitCode}`
          : action === "oom"
            ? `${name} ran out of memory`
            : `${name} started`,
      level: action === "start" || exitCode === "0" ? "info" : "error",
      source: "daemon",
      owner: { "environment-id": "12" },
    })
    await page.route("**/api/v1/deploy/7/lifecycle*", (route) =>
      json(route, {
        status: "available",
        watching: true,
        since: "2026-09-03T06:00:00Z",
        events: [
          // Docker sends the reaper's note and the exit in the same second.
          event("start", at(9)),
          event("die", at(8, 59), "137"),
          event("oom", at(8, 59)),
          event("start", at(7)),
          event("oom", at(6, 59)),
          event("die", at(6, 59), "137"),
          event("start", at(5)),
          event("die", at(4, 59), "137"),
          event("oom", at(4, 59)),
          // A job its restart policy runs again, exiting cleanly each time.
          event("start", at(3), undefined, "report-job"),
          event("die", at(2), "0", "report-job"),
          event("start", at(1), undefined, "report-job"),
          event("die", at(0), "0", "report-job"),
        ],
      }),
    )
    await page.goto("/deploy/7/logs?view=events")

    const feed = page.getByRole("region", { name: "Container events" })
    await expect(feed.getByText("Restarted ×3 in 4 min")).toBeVisible()
    await expect(feed.getByText("· OOM-killed")).toBeVisible()
    await expect(feed.getByText("· exit 137")).toBeVisible()
    await expect(feed.getByText("Restarted ×2 in 3 min")).toBeVisible()
    await expect(feed.getByText("· each exit clean")).toBeVisible()
    await expect(feed.getByText("· exit 0")).toHaveCount(0)

    // The container is gone, so the newest failure's last lines are said to
    // be gone with it rather than asked for and refused.
    const last = feed.getByRole("region", { name: "Last lines" })
    await expect(last).toHaveCount(1)
    await expect(last.getByText(/has since been removed/)).toBeVisible()
    await expect(feed.getByRole("button", { name: "Last lines" })).toHaveCount(0)
    expect(searches.some((q) => q.get("source")?.startsWith("docker:"))).toBe(false)
  })

  test("the page fits without scrolling sideways, at a phone and at a laptop", async ({
    page,
  }, testInfo) => {
    await mockProject(page)
    for (const width of [390, 1280]) {
      await page.setViewportSize({ width, height: 900 })
      await page.goto("/deploy/7/logs")
      await expect(page.getByRole("button", { name: "Requests", exact: true })).toBeVisible()
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)
      await page.screenshot({
        path: testInfo.outputPath(`deploy-requests-${width}.png`),
        fullPage: true,
      })
      await page.getByRole("button", { name: "Insights", exact: true }).click()
      await expect(page.getByText("Pages", { exact: true })).toBeVisible()
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)
      await page.screenshot({
        path: testInfo.outputPath(`deploy-insights-${width}.png`),
        fullPage: true,
      })
    }
    await page.goto("/deploy/7/settings/automation")
    await expect(page.getByText("Traffic alerts")).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath("deploy-alerts-1280.png"), fullPage: true })
  })
})
