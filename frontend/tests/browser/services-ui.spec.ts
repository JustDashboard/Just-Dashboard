import { expect, test } from "@playwright/test"
import { mockServices, SYSTEMD_LIST, UNITS } from "./services-fixture"
import { risen } from "./risen"

/**
 * The Services page against a host whose services carry their live readings:
 * the identity line and its verdict, the band of who is using the machine
 * and what just happened, the table's chips and readings, and each unit's own
 * page: a running unit's figures, picture, command
 * and processes, a failed one's story, a restart loop's, every view, Control,
 * and the rail of the table beside it. `processes-ui.spec.ts` keeps the
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
  await expect(memory.locator("[data-slot=reading-line]")).toContainText("1.2 GB")

  // What happened, newest first: the restart in progress, the failure and
  // why, the restart a reader made — and not the boot's own starts.
  const recent = band.getByLabel("Recent changes")
  const changes = recent.getByRole("button")
  const line = recent.locator("[data-slot=reading-line]")
  await expect(changes.nth(0)).toHaveAccessibleName(/redis-server/)
  await expect(line).toContainText("redis-server")
  await expect(line).toContainText("restarting")
  await changes.nth(1).hover()
  await expect(line).toContainText("failed · start limit hit")
  await expect(line).toContainText("12m ago")
  await changes.nth(2).hover()
  await expect(line).toContainText("nginx")
  await expect(line).toContainText("started")
  await expect(recent.getByRole("button", { name: /docker/ })).toHaveCount(0)
  await expect(recent).toContainText("9 changes in the last day")

  // Every block one height, what it has to say aside.
  await risen(page)
  const heights = await band
    .locator("[data-slot=bar-reading]")
    .evaluateAll((all) => all.map((block) => block.getBoundingClientRect().height))
  expect(new Set(heights).size).toBe(1)

  // A mark there opens the unit.
  await recent.getByRole("button", { name: "Open nginx.service" }).click()
  await expect(page).toHaveURL(/\/processes\/services\/nginx\.service$/)
  await expect(page.locator("[data-slot='host-identity']")).toContainText("nginx")
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

test("a running unit's page is a live readout of it", async ({ page }) => {
  const mocks = await mockServices(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/processes/services/nginx.service")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("A high performance web server")
  await expect(identity).toContainText("starts on boot")
  await expect(identity).toContainText("runs as root")
  await expect(identity.getByRole("link", { name: /main PID 812/ })).toHaveAttribute(
    "href",
    "/processes/812",
  )
  // How exposed it is, in its tone, opening the view that says why.
  await expect(identity.getByRole("button", { name: /exposure 9\.2 · unsafe/ })).toBeVisible()

  const tiles = page.locator("[data-testid='service-readings'] [data-slot='stat-tile']")
  await expect(tiles).toHaveCount(4)
  await expect(tiles.nth(0)).toContainText("of a core")
  await expect(tiles.nth(0).locator("svg[role='img']")).toBeVisible()
  await expect(tiles.nth(1)).toContainText("peak 61.0 MB")
  await expect(tiles.nth(2)).toContainText("of 4915")
  await expect(tiles.nth(3)).toContainText("on failure, after 5s")

  // The command as it was run, not systemd's record of it, and every step around it.
  const command = page.getByTestId("service-command")
  await expect(command).toContainText("/usr/sbin/nginx -g daemon on; master_process on;")
  await expect(command).not.toContainText("ignore_errors")
  await expect(command).toContainText("-t -q")
  await expect(command).toContainText("start-stop-daemon")

  const how = page.locator("section").filter({ hasText: "How it runs" })
  await expect(how).toContainText("Started when its process forks and the parent exits")
  await expect(how).toContainText("gives up after 5 starts in 10s")
  await expect(how).toContainText("SIGTERM to the main process, then the rest")
  await expect(how).toContainText("with 1 override")
  const used = page.locator("section").filter({ hasText: "Since it started" })
  await expect(used).toContainText("6m 52s")
  await expect(used).toContainText("212.0 MB")
  await expect(used).toContainText("not counted")

  // A unit file changed on disk is said first, with the way to re-read it.
  await expect(page.getByText("Its unit file changed on disk")).toBeVisible()
  await page.getByRole("button", { name: "Reload unit files" }).first().click()
  await expect.poll(() => mocks.actions).toContain("daemon-reload")

  // Reload is offered in the header, where systemd has said the unit takes one.
  await page.getByRole("button", { name: "More actions" }).first().click()
  await expect(page.getByRole("menuitem", { name: /Reload configuration/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Freeze/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Mask/ })).toBeVisible()
  await page.keyboard.press("Escape")
})

test("the picture says what starts it, what it needs and what it runs", async ({ page }) => {
  const mocks = await mockServices(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/processes/services/nginx.service")
  const picture = page.getByRole("group", { name: "Where it runs" })
  const before = picture.getByRole("list", { name: "Before it" })
  await expect(before).toContainText("Needs")
  await expect(before).toContainText("sysinit.target")
  await expect(before).toContainText("Wanted by")
  await expect(before).toContainText("multi-user.target")

  // Its processes, from the table narrowed to its cgroup: the master, its
  // four workers as one node, the ports the master listens on.
  await expect
    .poll(() => mocks.inventories.some((q) => q.get("group") === "systemd:nginx.service"))
    .toBe(true)
  const runs = picture.getByRole("list", { name: "What it runs" })
  await expect(
    runs.getByRole("link", { name: /Open its main process, nginx \(812\)/ }),
  ).toHaveAttribute("href", "/processes/812")
  await expect(runs).toContainText("nginx × 4")
  await expect(runs).toContainText(":80 :443")
  await expect(runs).toContainText("every interface · 14 open connections")
  await expect(runs).toContainText("The journal")

  await runs.getByRole("button", { name: "Show its 4 nginx processes" }).click()
  await expect(page.getByRole("tab", { name: /Processes/ })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect(page).toHaveURL(/\/processes\/services\/nginx\.service\?tab=processes$/)
  const list = page.getByRole("list", { name: "Processes of nginx.service" })
  await expect(list.getByRole("link")).toHaveCount(5)
  await expect(list.getByRole("link").first()).toHaveAttribute("href", "/processes/812")
  await expect(list.getByRole("link").first()).toContainText("main")
})

test("a failed unit's page says why, and leads to its journal", async ({ page }) => {
  const mocks = await mockServices(page)
  await page.goto("/processes/services/postgresql.service")

  await expect(page.getByText("Failed: start limit hit")).toBeVisible()
  await expect(page.getByRole("main")).toContainText(
    "Its main process exited with status 1, 12m ago",
  )
  await expect(page.getByRole("main")).toContainText("restarted 4 times in quick succession")
  // Not running: its last run in place of live figures, and no processes read.
  await expect(page.getByTestId("service-readings")).toHaveCount(0)
  const last = page.locator("section").filter({ hasText: "Last run" })
  await expect(last).toContainText("exit 1")
  await expect(last).toContainText("1.2 GB")
  expect(mocks.inventories).toEqual([])

  // The notice's button; the picture's journal node goes to the same place.
  await page.getByRole("button", { name: "Read its journal" }).first().click()
  await expect(page.getByRole("tab", { name: "Journal", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect.poll(() => mocks.sockets.at(-1)?.get("source")).toBe("journal:postgresql.service")
})

test("a unit in a restart loop says so, with what it is waiting on", async ({ page }) => {
  await mockServices(page)
  await page.goto("/processes/services/redis-server.service")
  await expect(page.getByText("Restarting after it exited")).toBeVisible()
  await expect(page.getByRole("main")).toContainText("systemd starts it again after 5s")
  await expect(page.getByRole("main")).toContainText("7 automatic restarts so far")
  await page.getByRole("tab", { name: "Limits and security" }).click()
  await expect(page.locator("section").filter({ hasText: "What it may use" })).toContainText(
    "512.0 MB",
  )
  // systemd-analyze could not say; the page says so rather than inventing a score.
  await expect(page.getByText("systemd-analyze is not installed")).toBeVisible()
})

test("the table opens a unit's page, and the rail beside it walks the list", async ({ page }) => {
  await mockServices(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/processes/services")
  await page
    .locator("[data-slot='table-row'] [data-slot='table-cell']:first-child button")
    .filter({ hasText: "postgresql.service" })
    .click()
  await expect(page).toHaveURL(/\/processes\/services\/postgresql\.service$/)

  // The table's own list beside it, failed first, the one on screen filled.
  const rail = page.getByRole("navigation", { name: "Services" })
  await expect(rail).toBeVisible()
  const rows = rail.getByRole("list", { name: "Services to open" }).getByRole("button")
  await expect(rows.first()).toHaveAccessibleName("postgresql.service")
  await expect(rows.first()).toHaveAttribute("aria-current", "page")
  await expect(rows.nth(1)).toContainText("exit 1")

  // The view on screen goes with you to the next unit.
  await page.getByRole("tab", { name: "Unit file" }).click()
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/processes\/services\/certbot\.service\?tab=file$/)
  await expect(rail.locator("[aria-current='page']")).toHaveAccessibleName("certbot.service")
  await page.getByRole("button", { name: "Previous service" }).click()
  await expect(page).toHaveURL(/\/processes\/services\/postgresql\.service\?tab=file$/)

  // The rail searches as the table does.
  await rail.getByLabel("Search the services").fill("nginx")
  await expect(rows).toHaveCount(2)
  await rows.filter({ hasText: "nginx" }).click()
  await expect(page).toHaveURL(/\/processes\/services\/nginx\.service\?tab=file$/)
})

test("an old link to a unit lands on its page", async ({ page }) => {
  await mockServices(page)
  await page.goto("/processes/services?unit=nginx.service")
  await expect(page).toHaveURL(/\/processes\/services\/nginx\.service$/)
  await expect(page.locator("[data-slot='host-identity']")).toContainText("nginx")
})

test("a unit that is not on the host says so", async ({ page }) => {
  await mockServices(page)
  await page.goto("/processes/services/nothing-here.service")
  await expect(page.getByText("No unit named nothing-here.service on this host")).toBeVisible()
})

test("every view of a unit", async ({ page }) => {
  await mockServices(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/processes/services/nginx.service?tab=dependencies")

  // Dependencies, under the question each answers, each with its state.
  const needs = page.locator("section").filter({ hasText: "What it needs" })
  await expect(needs.getByRole("list", { name: "Requires" })).toContainText("sysinit.target")
  await expect(needs.getByRole("list", { name: "Wants" })).toContainText("Network is Online")
  const conflicts = page.locator("section").filter({ hasText: "What it cannot run with" })
  await expect(conflicts).toContainText("shutdown.target")
  await expect(conflicts).toContainText("inactive")

  // The unit file and its drop-in, as systemd reads them, each opening in Files.
  await page.getByRole("tab", { name: /Unit file/ }).click()
  await expect(page.getByText("Changed on disk since systemd read it")).toBeVisible()
  const texts = page.getByTestId("unit-file-text")
  await expect(texts).toHaveCount(2)
  await expect(texts.nth(0)).toContainText(
    "ExecStart=/usr/sbin/nginx -g 'daemon on; master_process on;'",
  )
  await expect(texts.nth(1)).toContainText("Restart=on-failure")
  await expect(page.getByText("override", { exact: true })).toBeVisible()
  await expect(
    page.getByRole("link", {
      name: "Open /etc/systemd/system/nginx.service.d/override.conf in Files",
    }),
  ).toHaveAttribute(
    "href",
    "/files?path=%2Fetc%2Fsystemd%2Fsystem%2Fnginx.service.d%2Foverride.conf",
  )

  // Its environment, in the container page's table, a credential hidden until revealed.
  await page.getByRole("tab", { name: /Environment/ }).click()
  await expect(page.getByRole("tab", { name: /Environment/ })).toContainText("3")
  await expect(page.getByRole("main")).toContainText("NGINX_WORKERS")
  await expect(page.getByRole("main")).toContainText("Europe/Paris")
  await expect(page.getByRole("main")).not.toContainText("tk_live_7f3a9c21")
  await expect(page.getByRole("link", { name: "/etc/default/nginx" })).toBeVisible()
  await expect(page.getByText("skipped if missing")).toBeVisible()
  await expect(
    page.getByRole("link", { name: /Its main process's environment, PID 812/ }),
  ).toHaveAttribute("href", "/processes/812?tab=environment")

  // How exposed it is, most exposed setting first, and what it may use.
  await page.getByRole("tab", { name: "Limits and security" }).click()
  const exposure = page.getByTestId("service-exposure")
  await expect(exposure).toContainText("9.2")
  await expect(exposure).toContainText("unsafe")
  const settings = page.getByRole("list", { name: "Security settings" }).locator("li")
  await expect(settings.first()).toContainText("PrivateNetwork=")
  await expect(settings.first()).toContainText("+0.5")
  await expect(settings).toHaveCount(10)
  await page.getByRole("button", { name: "Show all 12 settings" }).click()
  await expect(settings).toHaveCount(12)
  const each = page.locator("section").filter({ hasText: "Each of its processes" })
  await expect(each).toContainText("524288")
})

test("Control says what each verb and signal does, and sends them through systemd", async ({
  page,
}) => {
  const mocks = await mockServices(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/processes/services/nginx.service?tab=control")

  const now = page.getByRole("list", { name: "Run it" })
  await expect(now).toContainText("Restart")
  await expect(now).toContainText("Reload configuration")
  await expect(now).toContainText("Freeze")
  const boot = page.getByRole("list", { name: "Startup" })
  await expect(boot).toContainText("Disable on boot")
  await expect(boot).toContainText("Mask")
  await expect(boot).toContainText("Reload unit files")

  // A hang-up to nginx is handled — it reloads — read from its main process's masks.
  const signals = page.getByRole("list", { name: "Signals" })
  const hup = signals.locator("li").filter({ hasText: "SIGHUP" })
  await expect(hup).toContainText("handled")
  await hup.getByRole("button", { name: "Send SIGHUP to nginx.service" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Send" }).click()
  await expect
    .poll(() => mocks.bodies["nginx.service/kill"])
    .toEqual({
      signal: "SIGHUP",
      whom: "main",
    })

  // To every process in it, not only the main one.
  await page.getByRole("switch", { name: "Send to every process in it" }).click()
  await signals.getByRole("button", { name: "Send SIGUSR1 to nginx.service" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Send" }).click()
  await expect
    .poll(() => mocks.bodies["nginx.service/kill"])
    .toEqual({
      signal: "SIGUSR1",
      whom: "all",
    })

  // Limits, set through systemd until the next boot.
  const limits = page.getByRole("form", { name: "Resource limits" })
  await limits.getByLabel("Memory").fill("512M")
  await limits.getByLabel("Processor, % of a core").fill("150")
  await limits.getByRole("switch", { name: "Keep after a reboot" }).click()
  await limits.getByRole("button", { name: "Set limits" }).click()
  await expect
    .poll(() => mocks.bodies["nginx.service/limits"])
    .toEqual({
      persistent: false,
      memoryMax: String(512 * 1024 * 1024),
      cpuQuota: "150%",
    })

  // Freezing asks first, then goes to its own route.
  await now.getByRole("button", { name: "Freeze" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Freeze" }).click()
  await expect.poll(() => mocks.actions).toContain("nginx.service/freeze")
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

  test("a unit's page is one pane, and no view of it scrolls sideways", async ({ page }) => {
    await mockServices(page)
    await page.goto("/processes/services/nginx.service")
    await expect(page.locator("[data-slot='host-identity']")).toContainText("nginx")
    // No rail on a phone: the page is the one pane, with the way back.
    await expect(page.getByRole("navigation", { name: "Services" })).toBeHidden()
    await expect(page.getByRole("link", { name: "Services" }).first()).toBeVisible()
    for (const tab of [
      "Overview",
      "Processes",
      "Dependencies",
      "Unit file",
      "Environment",
      "Limits and security",
      "Control",
    ]) {
      await page.getByRole("tab", { name: new RegExp(`^${tab}`) }).click()
      await page.waitForTimeout(150)
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow, `${tab} scrolls sideways`).toBe(false)
    }
  })
})
