import { expect, test, type Page } from "@playwright/test"
import { availability, json, mockProxy, user } from "./proxy-fixtures"
import { risen } from "./risen"

/**
 * The Traffic page: the edge's last hour across every site, read as a
 * project's Logs page reads one deployment — an identity line, the hour as
 * readings in words, the hour drawn, then the sites that carried it and
 * nginx's error log as views in the address.
 */

/** A minute of a site's hour, `ago` minutes back. */
function minute(ago: number, total: number, failed = 0, refused = 0) {
  const start = new Date(Date.now() - ago * 60_000)
  start.setUTCSeconds(0, 0)
  return { start: start.toISOString(), total, refused, failed, bytes: total * 1_000 }
}

type Minute = ReturnType<typeof minute>

/** One site's hour, as GET /proxy/traffic answers it. */
function hour(site: string, points: Minute[], over: Record<string, unknown> = {}) {
  const requests = points.reduce((sum, p) => sum + p.total, 0)
  const failed = points.reduce((sum, p) => sum + p.failed, 0)
  return {
    site,
    file: `/etc/nginx/sites-available/${site}`,
    engine: "nginx",
    status: "available",
    requests,
    errorRate: requests ? failed / requests : 0,
    bytes: requests * 1_000,
    complete: true,
    points,
    ...over,
  }
}

function unlogged(site: string, reason = "access_log off") {
  return {
    site,
    file: `/etc/nginx/sites-available/${site}`,
    engine: "nginx",
    status: "unavailable",
    reason,
    requests: 0,
    errorRate: 0,
    bytes: 0,
    complete: true,
    points: [],
  }
}

const BUSY = [
  hour("app.example.com", [minute(3, 300), minute(2, 400, 8, 12), minute(1, 500, 4)], { p95: 180 }),
  hour("legacy.example.com", [minute(2, 50), minute(1, 10)], { p95: 412 }),
  unlogged("quiet.example.com"),
]

function report(groups: unknown[] = []) {
  return {
    scope: "main",
    path: "/var/log/nginx/error.log",
    exists: true,
    since: new Date().toISOString(),
    complete: true,
    lines: groups.length,
    groups,
  }
}

const TIMEOUT = {
  pattern: "upstream timed out",
  level: "error",
  count: 3,
  first: new Date().toISOString(),
  last: new Date().toISOString(),
  sample: "upstream timed out (110: Connection timed out) while reading response header",
  upstream: "http://127.0.0.1:3000",
  title: "The application took too long to answer",
  advice: "Look at what the application was doing.",
}

/** The page over a host with the given sites and error groups; returns the error log's asks. */
async function open(
  page: Page,
  sites: unknown[] = BUSY,
  { groups = [TIMEOUT], path = "/proxy/traffic" }: { groups?: unknown[]; path?: string } = {},
) {
  await mockProxy(page, { included: true })
  const asked: URL[] = []
  await page.route("**/api/v1/proxy/traffic", (route) =>
    json(route, { observedAt: new Date().toISOString(), sites }),
  )
  await page.route("**/api/v1/proxy/errors?**", (route) => {
    asked.push(new URL(route.request().url()))
    return json(route, report(groups))
  })
  await page.goto(path)
  return asked
}

async function asReader(page: Page) {
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
}

const identity = (page: Page) => page.locator("[data-slot='host-identity']")
const readings = (page: Page) => page.locator("[data-slot='reading-line']")
const reading = (page: Page, name: string) =>
  readings(page).filter({ has: page.locator(".eyebrow").getByText(name, { exact: true }) })

test("the hour opens on what it is about and reads in words, with no tile row", async ({
  page,
}) => {
  await open(page)

  await expect(identity(page)).toContainText("Traffic")
  await expect(identity(page)).toContainText("last hour")
  await expect(identity(page).getByRole("link", { name: "3 sites" })).toHaveAttribute(
    "href",
    "/proxy/sites",
  )
  await expect(identity(page)).toContainText("1 with no access log")
  await expect(identity(page)).toContainText("Answering")

  await expect(reading(page, "Requests")).toContainText("requests a minute")
  await expect(reading(page, "Requests")).toContainText("1,260 in the last hour across 2 sites")
  await expect(reading(page, "Failing")).toContainText("1.0% of requests failed")
  await expect(reading(page, "Failing")).toContainText("12 of 1,260 answered 5xx")
  // Latency is said in words, never as p95.
  await expect(reading(page, "Response time")).toContainText("19 in 20 inside 412ms")
  await expect(reading(page, "Response time")).toContainText("legacy.example.com")
  await expect(reading(page, "Served")).toContainText("sent")
  await expect(page.getByText(/\bp95\b/)).toHaveCount(0)

  await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Requests" })).toBeVisible()
  await expect(page.locator("[data-slot='traffic-hour']")).toContainText("12 failed")
})

test("the sites are a ranked list, busiest first, and a row opens its page", async ({ page }) => {
  await open(page)
  const sites = page.locator("[data-slot='traffic-sites']")
  const rows = sites.locator("[data-slot='rank-row']")
  await expect(rows).toHaveCount(3)
  await expect(rows.nth(0)).toContainText("app.example.com")
  await expect(rows.nth(0)).toContainText("1.2k/h")
  await expect(rows.nth(0)).toContainText("1.0% failing")
  await expect(rows.nth(2)).toContainText("no log")
  await expect(sites.getByRole("img", { name: "Requests this hour by site" })).toBeVisible()

  // Rows with a failing share, with none and with no log are one height.
  await risen(page)
  const heights = await rows.evaluateAll((all) => all.map((r) => r.getBoundingClientRect().height))
  expect(new Set(heights).size).toBe(1)

  await sites.getByRole("button", { name: "Open app.example.com's requests" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\/app\.example\.com$/)
})

test("the opening block stands where it will be while the hour is read", async ({ page }) => {
  await mockProxy(page, { included: true })
  let land: () => void = () => {}
  const held = new Promise<void>((resolve) => (land = resolve))
  await page.route("**/api/v1/proxy/traffic", async (route) => {
    await held
    await json(route, { observedAt: new Date().toISOString(), sites: BUSY })
  })
  await page.route("**/api/v1/proxy/errors?**", (route) => json(route, report()))
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/proxy/traffic")

  await expect(identity(page)).toContainText("Reading the sites' access logs")
  await expect(readings(page)).toHaveCount(4)
  await risen(page)
  const block = page.locator("[data-slot='traffic-hour']")
  const before = await block.boundingBox()
  const lines = await readings(page).evaluateAll((all) =>
    all.map((r) => r.getBoundingClientRect().height),
  )

  land()
  await expect(reading(page, "Requests")).toContainText("requests a minute")
  await risen(page)
  const after = await block.boundingBox()
  expect(after?.y).toBeCloseTo(before?.y ?? 0, 0)
  expect(after?.height).toBeCloseTo(before?.height ?? 0, 0)
  expect(
    await readings(page).evaluateAll((all) => all.map((r) => r.getBoundingClientRect().height)),
  ).toEqual(lines)
})

test("a site's old address lands on the site's page, at its requests", async ({ page }) => {
  await open(page, BUSY, { path: "/proxy/traffic?site=app.example.com" })
  // The site's page reads its requests in its Logs view, which the address names.
  await expect(page).toHaveURL(/\/proxy\/sites\/app\.example\.com\?tab=logs$/)
  await expect(page.locator("[data-slot='host-identity']")).toContainText("app.example.com")
})

test("the error log is a view in the address, and ?view=errors still opens it", async ({
  page,
}) => {
  const asked = await open(page, BUSY, { path: "/proxy/traffic?view=errors" })
  const errors = page.locator("[data-slot='traffic-errors']")
  await expect(page.getByRole("tab", { name: /Errors/ })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("tab", { name: /Errors/ })).toContainText("3")
  await expect(errors).toContainText("The application took too long to answer")
  await expect(errors).toContainText("/var/log/nginx/error.log")
  // A local upstream's failure points at whoever holds its port.
  await expect(errors.getByRole("link", { name: "Inspect upstream owner" })).toBeVisible()
  expect(asked.at(-1)?.searchParams.get("window")).toBe("1h")

  await errors.getByRole("radio", { name: "24h" }).click()
  await expect.poll(() => asked.at(-1)?.searchParams.get("window")).toBe("24h")

  await page.getByRole("tab", { name: /Sites/ }).click()
  await expect(page).toHaveURL(/\/proxy\/traffic$/)
  await page.getByRole("tab", { name: /Errors/ }).click()
  await expect(page).toHaveURL(/\/proxy\/traffic\?tab=errors$/)
  await page.reload()
  await expect(page.getByRole("tab", { name: /Errors/ })).toHaveAttribute("aria-selected", "true")
})

test("a site's error log is the Errors view narrowed to it", async ({ page }) => {
  const asked = await open(page, BUSY, {
    path: "/proxy/traffic?site=legacy.example.com&view=errors",
  })
  const errors = page.locator("[data-slot='traffic-errors']")
  await expect(errors.getByRole("combobox", { name: "Site" })).toContainText("legacy.example.com")
  await expect.poll(() => asked.at(-1)?.searchParams.get("site")).toBe("legacy.example.com")
  await expect(errors.getByRole("link", { name: "Inspect site" })).toHaveAttribute(
    "href",
    "/proxy/sites/legacy.example.com",
  )

  await errors.getByRole("combobox", { name: "Site" }).click()
  await page.getByRole("option", { name: "Every site" }).click()
  await expect.poll(() => asked.at(-1)?.searchParams.get("site")).toBeNull()
  await expect(page).toHaveURL(/\/proxy\/traffic\?tab=errors$/)
})

test("an hour that is failing says so at the line's end and on the reading", async ({ page }) => {
  await open(page, [hour("app.example.com", [minute(2, 100, 10), minute(1, 100, 10)])])
  await expect(identity(page)).toContainText("10.0% failing")
  await expect(
    reading(page, "Failing").getByText("10.0% of requests failed", { exact: true }),
  ).toHaveClass(/text-destructive/)
})

test("a log with no timing says so rather than a figure", async ({ page }) => {
  await open(page, [hour("app.example.com", [minute(2, 30), minute(1, 40)])])
  await expect(reading(page, "Response time")).toContainText("No timing recorded")
  await expect(reading(page, "Response time")).toContainText("nginx's log format records no timing")
})

test("a host with no sites says so once, with the way to add one", async ({ page }) => {
  await open(page, [])
  await expect(page.getByText("No sites to count")).toBeVisible()
  await expect(page.getByRole("link", { name: "New site" })).toHaveAttribute(
    "href",
    "/proxy/sites?new=1",
  )
  await expect(readings(page)).toHaveCount(0)
  await expect(page.getByText("0 requests a minute")).toHaveCount(0)
  // Nothing to rank, so no empty Sites view: nginx's own log is what is left.
  await expect(page.getByRole("tab")).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Error log" })).toBeVisible()
  await expect(page.locator("[data-slot='traffic-errors']")).toContainText(
    "The application took too long to answer",
  )
})

test("sites that keep no access log claim no zero and point at the fix", async ({ page }) => {
  await open(page, [unlogged("legacy.example.com", "This site keeps no access log of its own.")])
  await expect(page.getByText("No access log to read")).toBeVisible()
  await expect(page.getByText("This site keeps no access log of its own.")).toBeVisible()
  await expect(page.getByRole("link", { name: "Turn its access log on" })).toHaveAttribute(
    "href",
    "/proxy/sites/legacy.example.com",
  )
  await expect(readings(page)).toHaveCount(0)
  await expect(identity(page)).toContainText("none keeps an access log")

  await open(page, [unlogged("a.example.com"), unlogged("b.example.com")])
  await expect(page.getByText("None of the 2 sites keeps an access log")).toBeVisible()
  await expect(page.getByRole("link", { name: "Open Sites" })).toHaveAttribute(
    "href",
    "/proxy/sites",
  )
})

test("an unreadable summary is an error with a retry, not zeros", async ({ page }) => {
  await mockProxy(page, { included: true })
  let fail = true
  await page.route("**/api/v1/proxy/traffic", (route) =>
    fail
      ? route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "internal", message: "logs went missing", retryable: true },
          }),
        })
      : json(route, { observedAt: new Date().toISOString(), sites: BUSY }),
  )
  await page.route("**/api/v1/proxy/errors?**", (route) => json(route, report()))
  await page.goto("/proxy/traffic")
  await expect(page.getByRole("alert").filter({ hasText: "logs went missing" })).toBeVisible()
  await expect(identity(page)).toContainText("couldn't read the sites' access logs")
  await expect(readings(page)).toHaveCount(0)
  fail = false
  await page.getByRole("button", { name: "Try again" }).first().click()
  await expect(reading(page, "Requests")).toContainText("requests a minute")
})

test("a host with no reverse proxy shows the section's install screen", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, nginx: false, nginxVersion: "", caddy: false }),
  )
  await page.route("**/api/v1/proxy/traffic", (route) =>
    json(route, { observedAt: new Date().toISOString(), sites: [] }),
  )
  await page.goto("/proxy/traffic")
  await expect(
    page.getByRole("heading", { name: "Put nginx in front of this server" }),
  ).toBeVisible()
  await expect(identity(page)).toContainText("No reverse proxy on this host")
  await expect(page.getByRole("tab")).toHaveCount(0)
})

test("the verbs are in the menu, and a reader gets none it cannot use", async ({ page }) => {
  await open(page, BUSY, { path: "/proxy/traffic?tab=errors" })
  await page.getByRole("button", { name: "More actions for traffic" }).click()
  await expect(page.getByRole("menuitem", { name: "Refresh" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Open Sites" })).toBeVisible()
  await page.getByRole("menuitem", { name: "Alert rules" }).click()
  await expect(page).toHaveURL(/\/proxy\/alerts$/)

  await asReader(page)
  await page.goto("/proxy/traffic?tab=errors")
  const errors = page.locator("[data-slot='traffic-errors']")
  await expect(errors).toContainText("The application took too long to answer")
  await page.getByRole("button", { name: "More actions for traffic" }).click()
  await expect(page.getByRole("menuitem", { name: "Open Sites" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Alert rules" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await expect(errors.getByRole("link", { name: "Inspect nginx configuration" })).toHaveCount(0)
})

test("on a phone the page fits and the rows keep one height", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page)
  await expect(reading(page, "Requests")).toContainText("requests a minute")
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  const rows = page.locator("[data-slot='traffic-sites'] [data-slot='rank-row']")
  await expect(rows).toHaveCount(3)
  await risen(page)
  const heights = await rows.evaluateAll((all) => all.map((r) => r.getBoundingClientRect().height))
  expect(new Set(heights).size).toBe(1)
  for (const name of ["Requests", "Failing", "Response time", "Served"]) {
    const box = await reading(page, name).boundingBox()
    expect(box?.width ?? 0).toBeLessThanOrEqual(390)
  }

  await page.getByRole("tab", { name: /Errors/ }).click()
  await expect(page.locator("[data-slot='traffic-errors']")).toContainText(
    "The application took too long to answer",
  )
  const after = await page
    .locator("[data-slot='page']")
    .evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(after).toBeLessThanOrEqual(1)
})
