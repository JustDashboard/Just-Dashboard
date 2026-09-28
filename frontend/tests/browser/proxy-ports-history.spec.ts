import { expect, test, type Page } from "@playwright/test"
import { hostPorts } from "./fixtures/proxy/ports"
import {
  busyHistory,
  hostHistory,
  minutesBefore,
  quietHistory,
  stalledHistory,
  unstartedHistory,
} from "./fixtures/proxy/ports-history"
import { json, mockProxy, user } from "./proxy-fixtures"

/**
 * The ports history: which listening sockets are new, what opened and closed
 * and when, and a database that began answering off the machine today raised
 * on the overview. The browser's clock is held at noon UTC so a change an
 * hour ago is always today's and the minutes read the same on every run.
 */

test.use({ timezoneId: "UTC" })

const NOON = (() => {
  const today = new Date()
  return Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate(), 12)
})()

/**
 * The host's sockets, dated: Redis first seen ten minutes ago, caddy's IPv6
 * socket two hours ago (its IPv4 one was listening when recording began),
 * and the exporter three days ago.
 */
const datedPorts = hostPorts.map((socket) => {
  if (socket.process === "redis-server") return { ...socket, firstSeen: minutesBefore(NOON, 10) }
  if (socket.process === "caddy" && socket.family === "ipv6") {
    return { ...socket, firstSeen: minutesBefore(NOON, 120) }
  }
  if (socket.process === "node_exporter") {
    return { ...socket, firstSeen: minutesBefore(NOON, 3 * 24 * 60) }
  }
  return socket
})

async function mockHost(
  page: Page,
  history: (hours: number) => unknown = () => hostHistory(NOON),
  listeners: unknown[] = datedPorts,
) {
  await page.clock.setFixedTime(NOON)
  await mockProxy(page, { included: true })
  // Registered after the proxy tables, so these answer first.
  await page.route("**/api/v1/ports", (route) => json(route, listeners))
  await page.route("**/api/v1/ports/history**", (route) =>
    json(route, history(Number(new URL(route.request().url()).searchParams.get("hours")))),
  )
}

const changesPanel = (page: Page) => page.locator("section#changes")

test("a socket the history first saw open today is marked new, and can be listed alone", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/proxy/ports")
  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(7)

  const newMarks = page.getByRole("table").locator("[data-slot='tag']", { hasText: /^New/ })
  await expect(newMarks).toHaveCount(2)
  await expect(rows.filter({ hasText: "redis-server" }).locator("[data-slot='tag']")).toHaveText(
    "New, first seen at 11:50",
  )
  // A service is as new as its newer family.
  await expect(rows.filter({ hasText: "caddy" }).locator("[data-slot='tag']")).toHaveText(
    "New, first seen at 10:00",
  )
  // Three days ago is not new, and nor is a socket listening when recording began.
  for (const process of ["node_exporter", "sshd", "postgres"]) {
    await expect(rows.filter({ hasText: process }).locator("[data-slot='tag']")).toHaveCount(0)
  }

  const reach = page.getByRole("group", { name: "Reach", exact: true })
  await page.getByRole("button", { name: "New in 24 hours 2" }).click()
  await expect(rows).toHaveCount(2)
  // Every other count is taken among the new sockets.
  await expect(reach.getByRole("button", { name: "All 2" })).toBeVisible()
  await reach.getByRole("button", { name: "Internet-facing 1" }).click()
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText("redis-server")

  // Clearing the filters lets the old sockets back in.
  await page.getByPlaceholder("Port, process, user or address").fill("sshd")
  await expect(page.getByText("No sockets match")).toBeVisible()
  await page.getByRole("button", { name: "Clear filters" }).click()
  await expect(rows).toHaveCount(7)
  await expect(page.getByRole("button", { name: "New in 24 hours 2" })).toHaveAttribute(
    "aria-pressed",
    "false",
  )
})

test("a host with nothing new offers no New filter", async ({ page }) => {
  await mockHost(page, () => quietHistory(NOON), hostPorts)
  await page.goto("/proxy/ports")
  await expect(page.getByRole("table").locator("tbody tr")).toHaveCount(7)
  await expect(page.getByRole("button", { name: /^New in 24 hours/ })).toHaveCount(0)
  await expect(page.locator("[data-slot='tag']", { hasText: /^New/ })).toHaveCount(0)
})

test("the Changes list says what opened, closed and changed hands, and when", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")
  const changes = changesPanel(page)

  const today = changes.getByRole("region", { name: "Today" }).locator("li")
  await expect(today).toHaveCount(4)

  // Newest first. Redis in both families is one line, coloured as the list
  // colours the socket: the posture's level for a database.
  const redis = today.nth(0)
  await expect(redis.locator("time")).toHaveText("11:46")
  await expect(redis).toContainText("Opened")
  await expect(redis).toContainText("6379")
  await expect(redis).toContainText("0.0.0.0, ::")
  await expect(redis).toContainText("Redis · Every interface")
  await expect(redis).toContainText("redis-server · redis · PID 2000")
  await expect(redis.getByText("Opened", { exact: true })).toHaveClass(/text-destructive/)

  // A socket closed and opened in one sample passed to another program.
  const handed = today.nth(1)
  await expect(handed).toContainText("New owner")
  await expect(handed).toContainText("3000")
  await expect(handed).toContainText("python3 · app · PID 1500")
  await expect(handed).toContainText("Held before by node · app · PID 1400")
  await expect(handed).toContainText("This server only")

  // A closing says how long the socket listened, and draws no alarm.
  const caddy = today.nth(2)
  await expect(caddy).toContainText("Closed")
  await expect(caddy).toContainText("Tailnet only · tailscale0")
  await expect(caddy).toContainText("caddy · caddy · PID 3100 · open 1h")
  await expect(caddy.getByText("Closed", { exact: true })).not.toHaveClass(/text-destructive/)

  // Docker's two proxies are one line, and a change across a stretch nothing
  // was sampled in is dated to the stretch, not to the minute it was noticed.
  const docker = today.nth(3)
  await expect(docker).toContainText("docker-proxy · root · PIDs 501, 502")
  await expect(docker).toContainText("Sometime between 03:00 and 07:00, when nothing was sampled")

  const yesterday = changes.getByRole("region", { name: "Yesterday" }).locator("li")
  await expect(yesterday).toHaveCount(1)
  await expect(yesterday.first()).toContainText("open since before recording began")

  await expect(changes).toContainText("Compared once a minute, kept 30 days, recording since")
})

test("the changes split by who could reach the socket", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")
  const changes = changesPanel(page)
  const chips = changes.getByRole("group", { name: "Changes by reach" })
  await expect(chips.getByRole("button", { name: "All 5" })).toHaveAttribute("aria-pressed", "true")

  await chips.getByRole("button", { name: "This server 2" }).click()
  await expect(changes.locator("li")).toHaveCount(2)
  await chips.getByRole("button", { name: "Internet-facing 2" }).click()
  await expect(changes.locator("li")).toHaveCount(2)
  await expect(changes.locator("li").first()).toContainText("6379")
  await chips.getByRole("button", { name: "Private networks 1" }).click()
  await expect(changes.locator("li")).toHaveCount(1)
  await expect(changes.locator("li").first()).toContainText("caddy")
})

test("the changes follow the list's search, finding a port that changed hands by either owner", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/proxy/ports?q=port:6379")
  const changes = changesPanel(page)
  await expect(changes.locator("li")).toHaveCount(1)
  await expect(changes.locator("li").first()).toContainText("6379")
  await expect(changes.getByText("Matching the search port:6379")).toBeVisible()
  const chips = changes.getByRole("group", { name: "Changes by reach" })
  await expect(chips.getByRole("button", { name: "All 1" })).toBeVisible()

  const search = page.getByPlaceholder("Port, process, user or address")
  // The program that held the port before is a match as much as the one now.
  await search.fill("node")
  await expect(changes.locator("li")).toHaveCount(1)
  await expect(changes.locator("li").first()).toContainText("New owner")

  await search.fill("mysqld")
  await expect(changes.getByText("No changes match the search")).toBeVisible()
  await expect(
    changes.getByText("Nothing that opened or closed in the last day matches “mysqld”."),
  ).toBeVisible()
  await changes.getByRole("button", { name: "Clear the search" }).click()
  await expect(search).toHaveValue("")
  await expect(changes.locator("li")).toHaveCount(5)
  await expect(page.getByRole("table").locator("tbody tr")).toHaveCount(7)
})

test("the window asks the server for what it keeps, and an empty one says which it is", async ({
  page,
}) => {
  const asked: number[] = []
  await mockHost(page, (hours) => {
    asked.push(hours)
    return quietHistory(NOON, hours)
  })
  await page.goto("/proxy/ports")
  const changes = changesPanel(page)

  await expect(changes.getByText("Nothing opened or closed in the last day")).toBeVisible()
  await expect(changes.getByText(/compared once a minute while the dashboard ran/)).toBeVisible()
  await expect(changes.getByRole("radio", { name: "24 hours" })).toHaveAttribute(
    "aria-checked",
    "true",
  )

  await changes.getByRole("radio", { name: "7 days" }).click()
  await expect(changes.getByText("Nothing opened or closed in the last week")).toBeVisible()
  // Recording began three days ago, inside the week: what came before is unknown.
  await expect(
    changes.getByText(/^Recording began .* so nothing before then is known\.$/),
  ).toBeVisible()

  await changes.getByRole("radio", { name: "30 days" }).click()
  await expect(changes.getByText("Nothing opened or closed in the last thirty days")).toBeVisible()
  expect(asked).toEqual([24, 168, 720])

  // The window is remembered for the tab.
  await page.reload()
  await expect(changes.getByRole("radio", { name: "30 days" })).toHaveAttribute(
    "aria-checked",
    "true",
  )
})

test("a history that has not begun, or has stopped, says so", async ({ page }) => {
  await mockHost(page, () => unstartedHistory(NOON))
  await page.goto("/proxy/ports")
  const changes = changesPanel(page)
  await expect(changes.getByText("Recording has not started")).toBeVisible()
  await expect(changes.getByText(/Compared once a minute, kept/)).toHaveCount(0)

  await mockHost(page, () => stalledHistory(NOON))
  await page.reload()
  await expect(changes.getByText(/^No sample since .* at 11:00$/)).toBeVisible()
  await expect(changes.getByText(/nothing that opened or closed after it is here/)).toBeVisible()
})

test("a history that cannot be read fails alone, leaving the sockets listed", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/ports/history**", (route) =>
    route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "database is locked" } }),
    }),
  )
  await page.goto("/proxy/ports")
  await expect(page.getByRole("table").locator("tbody tr")).toHaveCount(7)
  await expect(changesPanel(page).getByRole("alert")).toContainText("database is locked")
})

test("a long history is drawn a page at a time and says when the window held more", async ({
  page,
}) => {
  await mockHost(page, () => busyHistory(NOON))
  await page.goto("/proxy/ports")
  const changes = changesPanel(page)
  await expect(changes.locator("li")).toHaveCount(60)
  await expect(changes.getByText("60 of 70 changes shown")).toBeVisible()
  await changes.getByRole("button", { name: "Show 10 more" }).click()
  await expect(changes.locator("li")).toHaveCount(70)
  await expect(changes.getByRole("button", { name: /^Show \d+ more$/ })).toHaveCount(0)
  await expect(changes.getByText("Only the newest 70 events of the day are listed")).toBeVisible()
})

test("the overview raises a database that began answering off the machine today", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/proxy")
  const finding = page.getByRole("button", {
    name: /^Redis began answering on 203\.0\.113\.5 at 11:50/,
  })
  await expect(finding.locator(".bg-destructive")).toHaveCount(1)
  await finding.click()
  await expect(
    page.getByText("6379/tcp redis-server on 203.0.113.5, first seen at 11:50"),
  ).toBeVisible()
  // The standing finding for the same database is still there beside it.
  await expect(page.getByRole("button", { name: /^Redis answers on 203\.0\.113\.5/ })).toBeVisible()
})

test("an account that only reads sees the history too", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto("/proxy/ports")
  await expect(changesPanel(page).getByRole("region", { name: "Today" }).locator("li")).toHaveCount(
    4,
  )
  await expect(
    page.getByRole("table").locator("[data-slot='tag']", { hasText: /^New/ }),
  ).toHaveCount(2)
})

test("the new marks and the changes fit a phone, with every control named", async ({ page }) => {
  await mockHost(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/proxy/ports")
  const list = page.getByRole("list", { name: "Listening sockets" })
  await expect(list.locator("[data-slot='tag']", { hasText: /^New/ })).toHaveCount(2)
  const changes = changesPanel(page)
  await expect(changes.locator("li")).toHaveCount(5)
  for (const line of await changes.locator("li").all()) {
    const box = await line.boundingBox()
    expect(box && box.x + box.width).toBeLessThanOrEqual(390)
  }
  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
    const unnamed = await page
      .locator("[data-slot='page'] button")
      .evaluateAll(
        (buttons) =>
          buttons.filter(
            (button) =>
              (button as HTMLElement).offsetWidth > 0 &&
              !button.textContent?.trim() &&
              !button.getAttribute("aria-label") &&
              !button.getAttribute("aria-labelledby"),
          ).length,
      )
    expect(unnamed).toBe(0)
  }
})
