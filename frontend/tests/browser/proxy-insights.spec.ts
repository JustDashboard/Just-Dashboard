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
 * The overview's Live traffic panel: nginx's stub_status counters with their
 * last hour, and the administrator's switch that puts the status server in
 * conf.d and takes it out again.
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
  return liveTraffic(page).locator("[data-slot='stat-tile']").filter({ hasText: label })
}

async function asReader(page: Page) {
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
}

test("live traffic draws the requests and connections with their last hour", async ({ page }) => {
  await mockProxy(page, { included: true })
  const samples = steady(metricSeries(60))
  await page.route(METRICS, (route) => json(route, metricsOn(samples)))
  await page.goto("/proxy")

  const panel = liveTraffic(page)
  await expect(panel.getByText("On", { exact: true })).toBeVisible()
  await expect(tile(page, "Requests")).toContainText("4.2")
  await expect(tile(page, "Requests")).toContainText("a second")
  // Five minutes of readings: the total says how far back it reaches rather
  // than claiming an hour.
  await expect(tile(page, "Requests")).toContainText(/36,914 since \d{2}:\d{2}/)
  await expect(tile(page, "Connections")).toContainText("31")
  await expect(tile(page, "Connections")).toContainText("1 reading · 5 writing · 25 idle")
  await expect(
    panel.getByRole("img", { name: "Requests a second over the last hour" }),
  ).toBeVisible()
  await expect(
    panel.getByRole("img", { name: "Open connections over the last hour" }),
  ).toBeVisible()
  await expect(panel).toContainText("127.0.0.1:19081/jd-status, read every 5 seconds")
  // A poll is not a socket: nothing here breathes as live.
  await expect(panel.locator(".animate-breathe")).toHaveCount(0)
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
  await expect(tile(page, "Connections")).toBeVisible()
  await expect.poll(() => asked.length, { timeout: 15_000 }).toBeGreaterThanOrEqual(2)
  expect(asked[0].searchParams.has("after")).toBe(false)
  expect(asked[1].searchParams.get("epoch")).toBe(String(metricsOn([]).epoch))
  expect(asked[1].searchParams.get("after")).toBe("12")
  // An empty update keeps the hour already drawn.
  await expect(liveTraffic(page).getByRole("img", { name: /Requests a second/ })).toBeVisible()
  await expect(tile(page, "Connections")).not.toContainText("—")
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

  const panel = liveTraffic(page)
  const toggle = panel.getByRole("switch", { name: "Live metrics" })
  await expect(panel.getByText("Off", { exact: true })).toBeVisible()
  await expect(panel).toContainText("Switching on adds a loopback-only status server to conf.d")
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
  await expect(tile(page, "Connections")).toContainText("31")
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

  const panel = liveTraffic(page)
  await expect(tile(page, "Requests")).toBeVisible()
  await panel.getByRole("switch", { name: "Live metrics" }).click()
  await expect(page.getByText("Live metrics off")).toBeVisible()
  expect(sent).toEqual([{ enabled: false }])
  await expect(panel.locator("[data-slot='stat-tile']")).toHaveCount(0)
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

test("readings that stopped keep their hour and lose their figure", async ({ page }) => {
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

  const panel = liveTraffic(page)
  await expect(panel.getByText("No answer", { exact: true })).toBeVisible()
  await expect(tile(page, "Requests")).toContainText("—")
  await expect(tile(page, "Connections")).toContainText("—")
  await expect(panel).toContainText(
    /No answer since .+: nothing is listening on 127\.0\.0\.1:19081/,
  )
  await expect(
    panel.getByRole("img", { name: "Open connections over the last hour" }),
  ).toBeVisible()
})

test("connections nginx turned away turn the tile to a warning", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route(METRICS, (route) =>
    json(route, metricsOn(metricSeries(20), { hourDropped: 12 })),
  )
  await page.goto("/proxy")
  await expect(tile(page, "Connections")).toContainText(
    /12 turned away since \d{2}:\d{2}: worker_connections is full/,
  )
  await expect(tile(page, "Connections").locator(".text-warning")).toHaveCount(1)
})

test("a reader sees the readings and no switch", async ({ page }) => {
  await mockProxy(page, { included: true })
  await asReader(page)
  let on = false
  await page.route(METRICS, (route) => json(route, on ? metricsOn(metricSeries(10)) : metricsOff()))
  await page.goto("/proxy")

  const panel = liveTraffic(page)
  await expect(panel).toContainText("Off. An administrator can switch live metrics on here.")
  await expect(panel.getByRole("switch")).toHaveCount(0)

  on = true
  await page.reload()
  await expect(tile(page, "Requests")).toBeVisible()
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

  const panel = liveTraffic(page)
  await expect(panel.getByRole("alert").filter({ hasText: "metrics went missing" })).toBeVisible()
  await expect(panel.getByText(/^Off\./)).toHaveCount(0)
  fail = false
  await panel.getByRole("button", { name: "Try again" }).click()
  await expect(panel.getByText(/^Off\./)).toBeVisible()
})

test("a host without nginx has no live traffic to show", async ({ page }) => {
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
  // Stacked, each the panel's width, so the connection states are not cut.
  expect(tiles).toHaveLength(2)
  for (const width of tiles) expect(width).toBeGreaterThan(300)
})
