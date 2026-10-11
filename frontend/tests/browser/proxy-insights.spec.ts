import { expect, test, type Page, type Route } from "@playwright/test"
import { availability, json, mockProxy, mockShowcase, user } from "./proxy-fixtures"
import {
  metricSeries,
  metricsOff,
  metricsOn,
  STATUS_FILE,
  type MetricsSample,
} from "./fixtures/proxy/insights"

/**
 * The overview's Live traffic panel: the edge's hour added up from every
 * site's access record, nginx's and the Docker Caddy ingress's alike, then
 * nginx's own stub_status counters under it, with the administrator's switch
 * that puts the status server in conf.d and takes it out again.
 */

const METRICS = "**/api/v1/proxy/metrics*"

/** A reading whose figures the tiles are asserted against. */
function steady(samples: MetricsSample[]): MetricsSample[] {
  const last = samples.at(-1)!
  return [
    ...samples.slice(0, -1),
    { ...last, requests: 4.2, active: 31, reading: 1, writing: 5, waiting: 25 },
  ]
}

function liveTraffic(page: Page) {
  return page.getByRole("region", { name: "Live traffic" })
}

function tile(page: Page, label: string) {
  return liveTraffic(page)
    .locator("[data-slot='stat-tile']")
    .filter({ has: page.locator(".eyebrow").getByText(label, { exact: true }) })
}

function counters(page: Page) {
  return liveTraffic(page).getByRole("region", { name: "nginx counters" })
}

/** A minute of a site's hour, `ago` minutes back. */
function minute(ago: number, total: number, failed = 0) {
  const start = new Date(Date.now() - ago * 60_000)
  start.setUTCSeconds(0, 0)
  return { start: start.toISOString(), total, refused: 0, failed, bytes: total * 1_000 }
}

/** One site's hour, as GET /proxy/traffic answers it. */
function hour(
  site: string,
  engine: "nginx" | "caddy-ingress",
  points: ReturnType<typeof minute>[],
) {
  const requests = points.reduce((sum, p) => sum + p.total, 0)
  const failed = points.reduce((sum, p) => sum + p.failed, 0)
  return {
    site,
    file: engine === "nginx" ? `/etc/nginx/sites-available/${site}` : "",
    engine,
    status: "available",
    requests,
    errorRate: requests ? failed / requests : 0,
    bytes: requests * 1_000,
    complete: true,
    points,
  }
}

async function asReader(page: Page) {
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
}

test("live traffic adds up every site's minutes, the ingress's routes included", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/traffic", (route) =>
    json(route, {
      observedAt: new Date().toISOString(),
      sites: [
        hour("app.example.com", "nginx", [minute(2, 30), minute(1, 40, 2), minute(0, 5)]),
        hour("just-dashboard-env-7.conf", "caddy-ingress", [
          minute(2, 10),
          minute(1, 20, 4),
          minute(0, 1),
        ]),
      ],
    }),
  )
  await page.goto("/proxy")

  const panel = liveTraffic(page)
  await expect(panel).toContainText("Last hour across 2 sites, by the minute")
  // The last whole minute, not the one still filling.
  await expect(tile(page, "Requests")).toContainText("60")
  await expect(tile(page, "This hour")).toContainText("106")
  await expect(tile(page, "Server errors")).toContainText("5.7%")
  await expect(tile(page, "Server errors")).toContainText("6 this hour")
  await expect(
    panel.getByRole("img", { name: "Requests a minute over the last hour" }),
  ).toBeVisible()
  await expect(panel.getByRole("heading", { name: "Requests a minute" })).toBeVisible()
  // A poll is not a socket: nothing here breathes as live.
  await expect(panel.locator(".animate-breathe")).toHaveCount(0)

  // The ingress's route is a site like any other, and opens its own page.
  const traffic = page.getByRole("region", { name: "Traffic", exact: true })
  await expect(traffic).toContainText("requests this hour")
  await traffic.getByRole("button", { name: "Open just-dashboard-env-7.conf's requests" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\/just-dashboard-env-7\.conf$/)
})

test("recent errors are the hour's 5xx by site", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/traffic", (route) =>
    json(route, {
      observedAt: new Date().toISOString(),
      sites: [hour("app.example.com", "nginx", [minute(1, 50, 5)])],
    }),
  )
  await page.goto("/proxy")
  const errors = page.getByRole("region", { name: "Recent errors" })
  await expect(errors).toContainText("server errors this hour")
  await expect(errors.getByRole("list", { name: "Sites answering 5xx" })).toContainText(
    "app.example.com",
  )
})

test("the nginx counters read requests and connections", async ({ page }) => {
  await mockProxy(page, { included: true })
  const samples = steady(metricSeries(60))
  await page.route(METRICS, (route) => json(route, metricsOn(samples)))
  await page.goto("/proxy")

  const strip = counters(page)
  await expect(strip.getByText("On", { exact: true })).toBeVisible()
  await expect(strip).toContainText("4.2 requests a second")
  await expect(strip).toContainText("31 connections open (1 reading · 5 writing · 25 idle)")
  await expect(
    strip.locator("[title*='127.0.0.1:19081/jd-status, read every 5 seconds']"),
  ).toHaveCount(1)
})

// The server keeps the hour; each poll after the first asks only for what
// the page lacks.
test("the page asks only for the readings it does not hold", async ({ page }) => {
  await mockProxy(page, { included: true })
  const samples = metricSeries(12)
  const asked: URL[] = []
  await page.route(METRICS, (route) => {
    const url = new URL(route.request().url())
    asked.push(url)
    const after = Number(url.searchParams.get("after") ?? 0)
    return json(route, metricsOn(after ? [] : samples, { current: samples.at(-1) }))
  })
  await page.goto("/proxy")
  await expect(counters(page)).toContainText("connections open")
  await expect.poll(() => asked.length, { timeout: 15_000 }).toBeGreaterThanOrEqual(2)
  expect(asked[0].searchParams.has("after")).toBe(false)
  expect(asked[1].searchParams.get("epoch")).toBe(String(metricsOn([]).epoch))
  expect(asked[1].searchParams.get("after")).toBe("12")
  // An empty update keeps the reading already drawn.
  await expect(counters(page)).not.toContainText("—")
})

test("switching on sends the switch, says it is under way and draws the readings", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let on = false
  let release: () => void = () => {}
  const sent: unknown[] = []
  await page.route(METRICS, async (route: Route) => {
    if (route.request().method() === "PUT") {
      sent.push(route.request().postDataJSON())
      await new Promise<void>((resolve) => (release = resolve))
      on = true
      return json(route, metricsOn(metricSeries(1)))
    }
    return json(route, on ? metricsOn(steady(metricSeries(3))) : metricsOff())
  })
  await page.goto("/proxy")

  const panel = counters(page)
  const toggle = panel.getByRole("switch", { name: "Live metrics" })
  await expect(panel.getByText("Off", { exact: true })).toBeVisible()
  await expect(
    panel.locator("[title^='Switching on adds a loopback-only status server to conf.d']"),
  ).toHaveCount(1)
  await expect(toggle).not.toBeChecked()

  await toggle.click()
  await expect(panel.getByText("Switching on…")).toBeVisible()
  await expect(toggle).toBeDisabled()
  expect(sent).toEqual([{ enabled: true }])
  release()

  await expect(page.getByText("Live metrics on")).toBeVisible()
  await expect(page.getByText("nginx answers on 127.0.0.1:19081/jd-status")).toBeVisible()
  await expect(toggle).toBeChecked()
  await expect(panel.getByText("On", { exact: true })).toBeVisible()
  await expect(panel).toContainText("31 connections open")
})

test("switching off takes the readings away", async ({ page }) => {
  await mockProxy(page, { included: true })
  let on = true
  const sent: unknown[] = []
  await page.route(METRICS, (route) => {
    if (route.request().method() === "PUT") {
      sent.push(route.request().postDataJSON())
      on = false
      return json(route, metricsOff({ epoch: 2 }))
    }
    return json(route, on ? metricsOn(metricSeries(30)) : metricsOff({ epoch: 2 }))
  })
  await page.goto("/proxy")

  const panel = counters(page)
  await expect(panel).toContainText("requests a second")
  await panel.getByRole("switch", { name: "Live metrics" }).click()
  await expect(page.getByText("Live metrics off")).toBeVisible()
  expect(sent).toEqual([{ enabled: false }])
  await expect(panel).not.toContainText("requests a second")
  await expect(panel.getByText("Off", { exact: true })).toBeVisible()
  await expect(panel.getByRole("switch", { name: "Live metrics" })).not.toBeChecked()
})

test("a switch the server refused says why and leaves the metrics off", async ({ page }) => {
  await mockProxy(page, { included: true })
  const reason =
    "nginx does not read conf.d/*.conf, so a status server there would never answer; nothing was changed. Include conf.d/*.conf inside the http block of nginx.conf to use live metrics"
  await page.route(METRICS, (route) =>
    route.request().method() === "PUT"
      ? route.fulfill({
          status: 409,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "metrics_unavailable", message: reason } }),
        })
      : json(route, metricsOff()),
  )
  await page.goto("/proxy")

  const toggle = liveTraffic(page).getByRole("switch", { name: "Live metrics" })
  await toggle.click()
  await expect(page.getByText("Live metrics not switched on")).toBeVisible()
  await expect(page.getByText("nginx does not read conf.d/*.conf")).toBeVisible()
  await expect(toggle).not.toBeChecked()
  await expect(toggle).toBeEnabled()
  await expect(liveTraffic(page).getByText("Off", { exact: true })).toBeVisible()
})

test("readings that stopped lose their figures and say why", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route(METRICS, (route) =>
    json(
      route,
      metricsOn(metricSeries(40), {
        error: "nothing is listening on 127.0.0.1:19081",
        failingSince: new Date(Date.now() - 60_000).toISOString(),
      }),
    ),
  )
  await page.goto("/proxy")

  const panel = counters(page)
  await expect(panel.getByText("No answer", { exact: true })).toBeVisible()
  await expect(panel).toContainText(
    /No answer since .+: nothing is listening on 127\.0\.0\.1:19081/,
  )
  await expect(panel).not.toContainText("requests a second")
})

test("connections nginx turned away are said as a warning", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route(METRICS, (route) =>
    json(route, metricsOn(metricSeries(20), { hourDropped: 12 })),
  )
  await page.goto("/proxy")
  const warning = counters(page).locator(".text-warning")
  await expect(warning).toHaveText("12 turned away: worker_connections is full")
})

test("a reader sees the readings and no switch", async ({ page }) => {
  await mockProxy(page, { included: true })
  await asReader(page)
  let on = false
  await page.route(METRICS, (route) => json(route, on ? metricsOn(metricSeries(10)) : metricsOff()))
  await page.goto("/proxy")

  const panel = counters(page)
  await expect(panel).toContainText("Off. An administrator can switch these counters on.")
  await expect(panel.getByRole("switch")).toHaveCount(0)

  on = true
  await page.reload()
  await expect(panel).toContainText("requests a second")
  await expect(panel.getByRole("switch")).toHaveCount(0)
})

test("somebody else's status file is left alone", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route(METRICS, (route) => json(route, metricsOff({ foreign: true })))
  await page.goto("/proxy")

  const panel = liveTraffic(page)
  await expect(panel.getByText("The status file is not the dashboard's")).toBeVisible()
  await expect(panel).toContainText(STATUS_FILE)
  await expect(panel.getByRole("switch", { name: "Live metrics" })).toBeDisabled()
})

test("a host where the metrics cannot go says why and offers no switch to press", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const reason =
    "/etc/nginx has no conf.d directory, which is where live metrics put nginx's status server"
  await page.route(METRICS, (route) => json(route, metricsOff({ supported: false, reason })))
  await page.goto("/proxy")

  await expect(liveTraffic(page)).toContainText(reason)
  await expect(liveTraffic(page).getByRole("switch", { name: "Live metrics" })).toBeDisabled()
})

test("metrics that cannot be read are an error, not an empty panel", async ({ page }) => {
  await mockProxy(page, { included: true })
  let fail = true
  await page.route(METRICS, (route) =>
    fail
      ? route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "internal", message: "metrics went missing", retryable: true },
          }),
        })
      : json(route, metricsOff()),
  )
  await page.goto("/proxy")

  const panel = counters(page)
  await expect(panel.getByRole("alert").filter({ hasText: "metrics went missing" })).toBeVisible()
  await expect(panel.getByText(/^Off —/)).toHaveCount(0)
  fail = false
  await panel.getByRole("button", { name: "Try again" }).click()
  await expect(panel.getByText(/^Off —/)).toBeVisible()
})

test("a host with neither nginx nor the ingress has no live traffic to show", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, nginx: false, nginxVersion: "", caddy: true }),
  )
  await page.goto("/proxy")
  await expect(page.getByRole("heading", { name: "Routes" })).toBeVisible()
  await expect(liveTraffic(page)).toHaveCount(0)
})

test("live traffic fits a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockShowcase(page)
  await page.route("**/api/v1/proxy/traffic", (route) =>
    json(route, {
      observedAt: new Date().toISOString(),
      sites: [hour("app.example.com", "nginx", [minute(2, 30), minute(1, 40, 2), minute(0, 5)])],
    }),
  )
  await page.goto("/proxy")

  const panel = liveTraffic(page)
  await expect(tile(page, "Requests")).toBeVisible()
  const toggle = panel.getByRole("switch", { name: "Live metrics" })
  await toggle.scrollIntoViewIfNeeded()
  await expect(toggle).toBeInViewport()
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  const tiles = await panel
    .locator("[data-slot='stat-tile']")
    .evaluateAll((els) => els.map((el) => el.getBoundingClientRect().width))
  // Stacked, each the panel's width, so no reading is cut.
  expect(tiles).toHaveLength(4)
  for (const width of tiles) expect(width).toBeGreaterThan(300)
})
