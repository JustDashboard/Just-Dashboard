import { expect, test, type Locator, type Page, type Route } from "@playwright/test"
import { host, snapshot, user } from "./host-fixture"
import { risen } from "./risen"

/**
 * What the pages outside the Docker section say where the thing they read is
 * not there: a server with no systemd on the Services list, a service's page
 * and Scheduled's Timers, and on the logs page an absent kind of source and
 * the journal's own line. Docker's own screen is in `module-missing.spec.ts`
 * and the server map's line in `server-map.spec.ts`.
 */

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const daemon =
  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"
const refusal = "System has not been booted with systemd as init system (PID 1). Can't operate."

type Routes = Record<string, (route: Route) => unknown>

/** A signed-in shell over a server where nothing answers but what a test names. */
async function mockShell(page: Page, routes: Routes = {}, session: unknown = user) {
  await page.routeWebSocket("**/api/v1/**", () => {})
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    const own = routes[path]
    if (own) return own(route)
    if (path === "/auth/session") return json(route, session)
    if (path === "/system/host") return json(route, host)
    if (path === "/system/metrics") return json(route, snapshot)
    return json(route, [])
  })
}

/** A server systemd does not run, as the three reads of it answer. */
const NO_SYSTEMD: Routes = {
  "/systemd/": (route) => json(route, { available: false, units: [] }),
  "/systemd/timers": (route) => json(route, { available: false, timers: [] }),
  "/systemd/nginx.service": (route) =>
    json(
      route,
      {
        error: {
          code: "systemd_not_running",
          message: "systemd is not running on this host",
          reason: "systemctl is installed, but /run/systemd/system is not there.",
          raw: refusal,
        },
      },
      503,
    ),
}

const missing = (page: Page) => page.locator("[data-slot=missing-thing]")

/** Inside the page's column, and not pushing it sideways. */
async function expectHeld(page: Page, block: Locator, width: number) {
  await risen(page)
  const box = (await block.boundingBox())!
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(width)
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    ),
  ).toBeLessThanOrEqual(0)
}

for (const width of [1280, 390]) {
  test(`Services without systemd says why and where to go at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    await mockShell(page, NO_SYSTEMD)
    await page.goto("/processes/services")
    const screen = missing(page)
    await expect(screen).toHaveAttribute("data-reason", "no-systemd")
    await expect(screen.getByRole("heading", { name: "No systemd on this server" })).toBeVisible()
    await expect(screen).toContainText("Services are systemd units")
    await expect(screen).toContainText("a container, or a host started by another init")
    await expect(screen.locator('img[src="/logos/systemd.svg"]')).toBeVisible()
    await expect(screen.getByRole("link", { name: "Open Processes" })).toHaveAttribute(
      "href",
      "/processes",
    )
    await expect(screen.getByRole("button", { name: "Copy the mount" })).toBeVisible()
    await expect(screen).toContainText("/run/systemd:/run/systemd")
    // What the old page said, and the tiles and table a server with systemd has.
    await expect(page.getByText("systemd is not available on this host")).toHaveCount(0)
    await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
    await expect(page.locator("[data-slot=empty-state]")).toHaveCount(0)
    await expectHeld(page, screen, width)
    await page.screenshot({
      path: testInfo.outputPath(`services-no-systemd-${width}.png`),
      fullPage: true,
    })
  })

  test(`a service's page without systemd is the same screen, not a red box, at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    await mockShell(page, NO_SYSTEMD)
    await page.goto("/processes/services/nginx.service")
    const screen = missing(page)
    await expect(screen.getByRole("heading", { name: "No systemd on this server" })).toBeVisible()
    await expect(screen).toContainText("A service is a systemd unit")
    // Neither the failure box nor a rail of services that does not exist.
    await expect(page.locator("[data-slot=error-state]")).toHaveCount(0)
    await expect(page.getByText("No service matches.")).toHaveCount(0)
    await expect(page.getByRole("navigation", { name: "Services" })).toHaveCount(0)
    // The way back to the list is still the page's first line.
    await expect(page.getByRole("link", { name: "Services" }).first()).toHaveAttribute(
      "href",
      "/processes/services",
    )
    await expectHeld(page, screen, width)
    await page.screenshot({
      path: testInfo.outputPath(`service-no-systemd-${width}.png`),
      fullPage: true,
    })
  })
}

test("systemctl's refusal is behind the fold when a unit's read carries it", async ({ page }) => {
  // The list answers, as it does for a moment while the host loses its
  // manager, and the unit's own read is the one that says there is none.
  await mockShell(page, {
    ...NO_SYSTEMD,
    "/systemd/": (route) => json(route, { available: true, units: [] }),
  })
  await page.goto("/processes/services/nginx.service")
  const screen = missing(page)
  await expect(screen.getByRole("heading", { name: "No systemd on this server" })).toBeVisible()
  await expect(screen.getByText(refusal)).toBeHidden()
  await screen.getByText("What the server said").click()
  await expect(screen.getByText(refusal)).toBeVisible()
})

for (const width of [1280, 390]) {
  test(`Scheduled keeps its cron jobs and says Timers need systemd at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    await mockShell(page, {
      ...NO_SYSTEMD,
      "/cron/users": (route) => json(route, ["root"]),
      "/cron/user/root": (route) =>
        json(route, {
          user: "root",
          source: "crontab -u root",
          raw: "0 3 * * * /usr/local/bin/backup\n",
          env: [],
          comments: [],
          timeZone: "UTC",
          revision: "r1",
          jobs: [
            {
              line: 1,
              schedule: "0 3 * * *",
              command: "/usr/local/bin/backup",
              raw: "0 3 * * * /usr/local/bin/backup",
              enabled: true,
              description: "At 03:00",
              nextRun: new Date(Date.now() + 3600_000).toISOString(),
              nextRuns: [],
            },
          ],
        }),
    })
    await page.goto("/processes/scheduled")
    const block = missing(page)
    await expect(block).toHaveAttribute("data-size", "block")
    await expect(block).toContainText("No systemd on this server")
    await expect(block).toContainText("Timers are systemd units")
    await expect(block.locator('img[src="/logos/systemd.svg"]')).toBeVisible()
    await expect(block.getByRole("link", { name: "Open Processes" })).toBeVisible()
    await expect(page.getByText("systemd is not available on this host.")).toHaveCount(0)
    // It is one section of the page, under Timers, and the rest is drawn.
    await expect(page.getByRole("heading", { name: "Timers" })).toBeVisible()
    await expect(page.getByText("/usr/local/bin/backup").first()).toBeVisible()
    await block.scrollIntoViewIfNeeded()
    await expectHeld(page, block, width)
    await page.screenshot({
      path: testInfo.outputPath(`scheduled-no-systemd-${width}.png`),
      fullPage: true,
    })
  })
}

/** A server with files to read and no Docker, PM2 or systemd behind the other kinds. */
const BARE_SOURCES = {
  sources: [
    {
      id: "file:/var/log/auth.log",
      label: "auth.log",
      kind: "system",
      path: "/var/log/auth.log",
      size: 2048,
      modified: new Date().toISOString(),
      rotated: false,
    },
  ],
  units: [],
  roots: ["/var/log"],
  missing: {
    docker: daemon,
    pm2: "PM2 is not installed on this host",
    journal: "systemd is not running on this host",
  },
}

for (const width of [1280, 390]) {
  test(`an absent kind of log source is one sentence with its way on at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    await mockShell(page, { "/logs/sources": (route) => json(route, BARE_SOURCES) })
    await page.goto("/logs?source=file:/var/log/auth.log")
    if (width === 390) await page.getByRole("tab", { name: "Sources" }).click()
    const rail = page.locator('[aria-label="Log sources"]')
    const lines = rail.locator("[data-slot=missing-line]")
    await expect(lines).toHaveCount(3)

    const docker = rail.locator('[data-slot=missing-line][data-thing="docker"]')
    await expect(docker).toContainText("Docker is not running")
    await expect(docker.locator('img[src="/logos/docker.svg"]')).toBeVisible()
    await expect(docker.getByRole("link", { name: "Open Docker" })).toHaveAttribute(
      "href",
      "/docker",
    )
    // The client's question to the reader is not on the page.
    await expect(page.getByText(/Is the docker daemon running/)).toHaveCount(0)

    const pm2 = rail.locator('[data-slot=missing-line][data-thing="pm2"]')
    await expect(pm2).toContainText("PM2 is not installed")
    await expect(pm2.getByRole("link", { name: "Open PM2" })).toHaveAttribute(
      "href",
      "/processes/pm2",
    )
    const journal = rail.locator('[data-slot=missing-line][data-thing="systemd"]')
    await expect(journal).toContainText("No systemd on this server")
    await expect(journal.getByRole("link", { name: "Open Services" })).toHaveAttribute(
      "href",
      "/processes/services",
    )
    // Each stays inside the rail.
    const pane = (await rail.boundingBox())!
    for (const line of await lines.all()) {
      const box = (await line.boundingBox())!
      expect(box.x).toBeGreaterThanOrEqual(pane.x)
      expect(box.x + box.width).toBeLessThanOrEqual(pane.x + pane.width + 0.5)
    }
    await page.screenshot({ path: testInfo.outputPath(`logs-missing-kinds-${width}.png`) })
  })
}

const oldest = new Date(Date.now() - 21 * 86_400_000).toISOString()
const JOURNAL_SOURCES = {
  sources: [
    {
      id: "journal:",
      label: "systemd journal",
      kind: "journal",
      detail: "Every unit on the host — pick one below to narrow it",
      rotated: false,
    },
  ],
  units: [
    { name: "nginx.service", description: "A high performance web server", active: "active" },
    { name: "ssh.service", description: "OpenBSD Secure Shell server", active: "active" },
    { name: "cron.service", description: "Regular background program", active: "active" },
  ],
  roots: ["/var/log"],
  missing: {},
}

for (const width of [1280, 390]) {
  test(`the journal opens on what it holds, not on an instruction, at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    // Held back, so the line is measured before and after the facts land.
    let answer: () => void = () => undefined
    const asked = new Promise<void>((resolve) => (answer = resolve))
    await mockShell(page, {
      "/logs/sources": (route) => json(route, JOURNAL_SOURCES),
      "/logs/source": async (route) => {
        await asked
        await json(route, {
          id: "journal:",
          label: "systemd journal",
          kind: "journal",
          rotated: false,
          size: 1288490188,
          oldest,
        })
      },
    })
    await page.goto("/logs?source=journal:")
    const identity = page.locator("[data-slot=host-identity]")
    await expect(identity).toContainText("systemd journal")
    await expect(identity.locator('img[src="/logos/systemd.svg"]')).toBeVisible()
    await expect(identity).toContainText("3 units")
    await expect(page.getByText(/pick one below/)).toHaveCount(0)
    await risen(page)
    const before = (await identity.boundingBox())!

    answer()
    await expect(identity).toContainText("1.2 GB on disk")
    await expect(identity).toContainText(/oldest line 2?1?\d?d|oldest line 3w/)
    // The facts landed on the line that was there: nothing under it moved.
    const after = (await identity.boundingBox())!
    expect(after.y).toBe(before.y)
    expect(after.height).toBe(before.height)
    await page.screenshot({ path: testInfo.outputPath(`logs-journal-${width}.png`) })
  })
}

test("the journal's row in the rail says how many units it holds", async ({ page }) => {
  await mockShell(page, { "/logs/sources": (route) => json(route, JOURNAL_SOURCES) })
  await page.goto("/logs?source=journal:")
  const row = page.locator('[aria-label="Log sources"]').getByRole("button", {
    name: /systemd journal/,
  })
  await expect(row).toContainText("3 units")
  await expect(row.locator('img[src="/logos/systemd.svg"]')).toBeVisible()
})
