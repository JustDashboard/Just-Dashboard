import { expect, test } from "@playwright/test"
import { mockServices, SYSTEMD_LIST, UNITS } from "./services-fixture"

/**
 * The Services page against a host whose services carry their live readings:
 * the identity line and its verdict, the band of who is using the machine
 * and what just happened, the table's chips and readings, and the unit sheet
 * as a live readout — a running unit's figures, command and processes, a
 * failed one's story, a restart loop's. `processes-ui.spec.ts` keeps the
 * older backend's shape, without readings, opening the same page.
 */

const rowNames = (page: import("@playwright/test").Page) =>
  page
    .locator("[data-slot='table-row'] [data-slot='table-cell']:first-child button")
    .allInnerTexts()

test("the page opens on the host, who is busy and what changed, with no tiles", async ({
  page,
}) => {
  await mockServices(page)
  await page.goto("/processes/services")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("srv-1")
  await expect(identity).toContainText("systemd 257")
  await expect(identity).toContainText("26 of 38 services active")
  await expect(identity).toContainText("24 start on boot")
  await expect(identity).toContainText("booted 3d 4h ago")
  await expect(identity.getByRole("button", { name: "Reload unit files" })).toBeVisible()
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)

  // The busiest by processor and by memory, each a span of the machine's bar.
  const band = page.locator("[data-slot='service-band']")
  const cpu = band.getByLabel("Processor by service")
  await expect(cpu.getByRole("img", { name: /^Processor: mongod/ })).toBeVisible()
  await expect(cpu.getByRole("button").first()).toHaveAccessibleName("Open mongod.service")
  await expect(cpu.getByRole("button")).toHaveCount(5)
  const memory = band.getByLabel("Memory by service")
  await expect(memory.getByRole("button").first()).toContainText("1.2 GB")

  // What happened, newest first: the restart in progress, the failure and
  // why, the restart a reader made — and not the boot's own starts.
  const recent = band.getByLabel("Recent changes")
  const changes = recent.getByRole("button")
  await expect(changes.nth(0)).toContainText("redis-server")
  await expect(changes.nth(0)).toContainText("restarting")
  await expect(changes.nth(1)).toContainText("failed · start limit hit")
  await expect(changes.nth(1)).toContainText("12m ago")
  await expect(changes.nth(2)).toContainText("nginx")
  await expect(changes.nth(2)).toContainText("started")
  await expect(recent).not.toContainText("docker")
  await expect(recent).toContainText("9 changes in the last day")

  // A row there opens the unit.
  await recent.getByRole("button", { name: "Open nginx.service" }).click()
  await expect(page.getByRole("dialog")).toContainText("nginx.service")
  await expect(page).toHaveURL(/unit=nginx\.service/)
})

test("the chips count and narrow, the verdict narrows to the failed, and Escape lets go", async ({
  page,
}) => {
  await mockServices(page)
  await page.goto("/processes/services")

  const states = page.locator("[aria-label='State']")
  await expect(states.getByRole("button", { name: /Active/ })).toContainText("26")
  await expect(states.getByRole("button", { name: /Failed/ })).toContainText("2")
  await expect(states.getByRole("button", { name: /Starting/ })).toContainText("1")
  await expect(states.getByRole("button", { name: /Inactive/ })).toContainText("9")

  // Failed first, and a unit no file defines is counted rather than listed.
  const names = await rowNames(page)
  expect(names.slice(0, 2)).toEqual(["postgresql.service", "certbot.service"])
  expect(names).not.toContain("display-manager.service")
  await expect(page.locator("[data-slot='panel-footer']")).toContainText(
    "1 referenced but not installed",
  )

  await page
    .locator("[data-slot='host-identity']")
    .getByRole("button", { name: /2 services failed/ })
    .click()
  expect(await rowNames(page)).toEqual(["postgresql.service", "certbot.service"])
  await expect(states.getByRole("button", { name: /Failed/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  // The startup chips narrow within it, and Escape lets go of one filter at a time.
  await page
    .locator("[aria-label='Startup']")
    .getByRole("button", { name: /Static/ })
    .click()
  expect(await rowNames(page)).toEqual(["certbot.service"])
  await page.locator("[data-slot='table-row']").first().focus()
  await page.keyboard.press("Escape")
  expect(await rowNames(page)).toEqual(["postgresql.service", "certbot.service"])
  await page.keyboard.press("Escape")
  await expect.poll(async () => (await rowNames(page)).length).toBe(UNITS.length - 1)

  // A link from elsewhere that asks for the failed opens on them.
  await page.goto("/processes/services?state=failed")
  await expect.poll(() => rowNames(page)).toEqual(["postgresql.service", "certbot.service"])
})

test("each row says how long it has been in its state and what it is using", async ({ page }) => {
  await mockServices(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/processes/services")

  const row = (name: string) => page.getByRole("row", { name: new RegExp(`^${name}`) })
  await expect(row("postgresql.service")).toContainText("start limit hit · 12m ago")
  await expect(row("certbot.service")).toContainText("exit 1 · 3h 12m ago")
  await expect(row("redis-server.service")).toContainText("7 restarts so far")
  await expect(row("docker.service")).toContainText(/up 3d/)
  await expect(row("docker.service")).toContainText("3.4%")
  await expect(row("docker.service")).toContainText("412.0 MB")
  await expect(row("grafana-server.service")).toContainText("1 restart")
  await expect(row("apt-daily.service")).toContainText("finished 41m ago")
  await expect(row("mysql.service")).toContainText("stopped 6h 30m ago")

  // The verbs the list never offered because the list had no unit file.
  await row("nginx.service").hover()
  await row("nginx.service").getByRole("button", { name: "More actions" }).click()
  await expect(page.getByRole("menuitem", { name: /Open unit file/ })).toBeVisible()
  await page.keyboard.press("Escape")
})

test("a running unit's sheet is a live readout of it", async ({ page }) => {
  const mocks = await mockServices(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/processes/services?unit=nginx.service")
  const sheet = page.getByRole("dialog")

  await expect(sheet).toContainText("A high performance web server")
  await expect(sheet).toContainText("starts on boot")
  await expect(sheet.getByRole("link", { name: /PID 812/ })).toHaveAttribute(
    "href",
    "/processes?pid=812",
  )
  const tiles = sheet.locator("[data-slot='stat-tile']")
  await expect(tiles).toHaveCount(4)
  await expect(tiles.nth(0)).toContainText("CPU")
  await expect(tiles.nth(0)).toContainText("of a core")
  await expect(tiles.nth(0).locator("svg[role='img']")).toBeVisible()
  await expect(tiles.nth(1)).toContainText("peak 61.0 MB")
  await expect(tiles.nth(2)).toContainText("of 4915")
  await expect(tiles.nth(3)).toContainText("on failure, after 5s")

  // The command as it was run, not systemd's record of it.
  const command = sheet.locator("[data-slot='panel']").filter({ hasText: "Command" })
  await expect(command).toContainText("/usr/sbin/nginx -g daemon on; master_process on;")
  await expect(command).not.toContainText("ignore_errors")
  await expect(command).toContainText("-s reload")

  // Its processes, from the table narrowed to its cgroup, each opening Live's sheet.
  await expect
    .poll(() => mocks.inventories.some((q) => q.get("group") === "systemd:nginx.service"))
    .toBe(true)
  const processes = sheet.locator("[data-slot='panel']").filter({ hasText: "Processes" })
  await expect(processes).toContainText("5 processes")
  await expect(processes.getByRole("link")).toHaveCount(5)
  await expect(processes.getByRole("link").first()).toHaveAttribute("href", "/processes?pid=812")

  const how = sheet.locator("[data-slot='panel']").filter({ hasText: "How it runs" })
  await expect(how).toContainText("Started when its process forks and the parent exits")
  await expect(how).toContainText("/etc/systemd/system/nginx.service.d/override.conf")
  await expect(how).toContainText("wanted by multi-user.target")

  // Reload is offered here, where systemd has said the unit takes one.
  await sheet.getByRole("button", { name: "More actions" }).click()
  await expect(page.getByRole("menuitem", { name: /Reload configuration/ })).toBeVisible()
  await page.keyboard.press("Escape")
})

test("a failed unit's sheet says why, and leads to its journal", async ({ page }) => {
  const mocks = await mockServices(page)
  await page.goto("/processes/services?unit=postgresql.service")
  const sheet = page.getByRole("dialog")

  await expect(sheet.getByText("Failed: start limit hit")).toBeVisible()
  await expect(sheet).toContainText("Its main process exited with status 1, 12m ago")
  await expect(sheet).toContainText("restarted 4 times in quick succession")
  // Not running: its last run in place of live figures, and no processes.
  await expect(sheet.locator("[data-slot='stat-tile']")).toHaveCount(0)
  const last = sheet.locator("[data-slot='panel']").filter({ hasText: "Last run" })
  await expect(last).toContainText("exit 1")
  await expect(last).toContainText("1.2 GB")
  expect(mocks.inventories).toEqual([])

  await sheet.getByRole("button", { name: "Read its journal" }).click()
  await expect(sheet.getByRole("tab", { name: "Journal", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect.poll(() => mocks.sockets.at(-1)?.get("source")).toBe("journal:postgresql.service")
})

test("a unit in a restart loop says so, with what it is waiting on", async ({ page }) => {
  await mockServices(page)
  await page.goto("/processes/services?unit=redis-server.service")
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("Restarting after it exited")).toBeVisible()
  await expect(sheet).toContainText("systemd starts it again after 5s")
  await expect(sheet).toContainText("7 automatic restarts so far")
  await expect(sheet).toContainText("512.0 MB of memory")
})

test("before a second read, the band says it is measuring", async ({ page }) => {
  await mockServices(page, {
    ...SYSTEMD_LIST,
    ratesReady: false,
    units: UNITS.map((u) => ({ ...u, cpuReady: false })),
  })
  await page.goto("/processes/services")
  await expect(page.getByText("Measuring the first interval…")).toBeVisible()
  await expect(page.locator("[data-slot='panel-footer']")).toContainText(
    "processor shares arrive with the second read",
  )
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await mockServices(page)
    await page.setViewportSize({ width, height: 1000 })
    await page.goto("/processes/services")
    await page.waitForLoadState("networkidle")
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    )
    expect(overflow, `services scrolls sideways at ${width}`).toBe(false)
    // The band's three blocks share a top, so their heads' hairlines meet.
    const heads = await page
      .locator("[data-slot='service-band'] [data-slot='panel-header']")
      .evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().bottom)))
    expect(new Set(heads).size, `band heads at ${heads.join(", ")}`).toBe(1)
    await page.screenshot({ path: `test-results/services-${width}.png`, fullPage: true })
  })
}

test.describe("on a phone", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  test("nothing scrolls sideways and every row's verbs are reachable", async ({ page }) => {
    await mockServices(page)
    await page.goto("/processes/services")
    await page.waitForLoadState("networkidle")
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    )
    expect(overflow).toBe(false)
    const hidden = await page.evaluate(() =>
      Array.from(document.querySelectorAll<HTMLElement>("li button"))
        .filter((el) => parseFloat(getComputedStyle(el).opacity) < 0.1)
        .map((el) => el.getAttribute("aria-label") ?? el.outerHTML.slice(0, 120)),
    )
    expect(hidden).toEqual([])
  })
})
