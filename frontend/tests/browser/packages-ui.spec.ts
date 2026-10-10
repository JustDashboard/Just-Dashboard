import { expect, test, type Page } from "@playwright/test"
import { mockHostLogs } from "./host-logs-fixture"
import { mockHost, installed, inventory, report, user } from "./packages-fixture"

test("software filters include dependencies and Escape clears the selection", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.getByRole("button", { name: "Only Docker packages" }).click()
  await expect(page.getByRole("row", { name: /docker.io/ })).toBeVisible()
  await expect(page.getByRole("row", { name: /containerd/ })).toBeVisible()
  await expect(page.getByRole("row", { name: /nginx/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Only Docker packages" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await page.keyboard.press("Escape")
  await expect(page.getByRole("button", { name: "Clear software filter" })).toHaveCount(0)
  await expect(page.getByRole("row", { name: /nginx/ })).toBeVisible()
  await page.getByRole("button", { name: /^Security/ }).click()
  await expect(page.getByRole("row", { name: /openssl/ })).toBeVisible()
  await expect(page.getByRole("row", { name: /nginx/ })).toHaveCount(0)
  await page.getByRole("button", { name: /^Dependencies/ }).click()
  await expect(page.getByRole("row", { name: /libc6/ })).toBeVisible()
  await expect(page.getByRole("row", { name: /postgresql-16/ })).toHaveCount(0)
})

test("the sheet follows dependencies and reports failed usage reads", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/packages/libc6/usage", (route) =>
    route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "internal", message: "Package file list is not readable" },
      }),
    }),
  )
  await page.goto("/packages?package=nginx")
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("heading", { name: "Commands", exact: true })).toBeVisible()
  await sheet.getByRole("button", { name: "Inspect dependency libc6", exact: true }).click()
  await expect(page).toHaveURL(/package=libc6/)
  await expect(sheet.getByText("Package file list is not readable")).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Copy nginx", exact: true })).toHaveCount(0)
  await page.goBack()
  await expect(sheet.getByRole("button", { name: "Copy nginx", exact: true })).toBeVisible()
})

test("missing sizes and advisory data are stated without made-up readings", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/packages/", (route) =>
    route.fulfill({
      json: {
        ...inventory,
        manager: "pacman",
        explicitCount: 0,
        totalSize: undefined,
        packages: installed.map((p) => ({ ...p, size: undefined, explicit: false })),
      },
    }),
  )
  await page.route("**/api/v1/packages/updates", (route) =>
    route.fulfill({ json: { ...report, securityFiltering: false, securityCount: 0 } }),
  )
  await page.goto("/packages")
  await expect(page.getByText("Size not reported")).toBeVisible()
  await expect(page.getByText("pacman publishes no security advisory data.")).toBeVisible()
  await expect(page.getByRole("button", { name: /^Installed by hand/ })).toHaveCount(0)
  await expect(page.getByRole("row", { name: /libc6/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "Install security updates" })).toHaveCount(0)
})

test("an unreadable update report is an error in the queue and the Updates view", async ({
  page,
}) => {
  await mockHost(page)
  await page.route("**/api/v1/packages/updates", (route) =>
    route.fulfill({
      status: 500,
      json: { error: { code: "internal", message: "Repository index is locked" } },
    }),
  )
  await page.goto("/packages")
  await expect(page.getByText("Repository index is locked")).toBeVisible()
  await expect(page.getByRole("button", { name: /^Upgrade all/ })).toHaveCount(0)
  await page
    .getByRole("navigation", { name: "Package views" })
    .getByRole("button", { name: /^Updates/ })
    .click()
  await expect(page.getByText("Repository index is locked")).toHaveCount(2)
  await expect(page.getByText("Everything is up to date", { exact: true })).toHaveCount(0)
})

test("read-only accounts can inspect software but have no package mutations", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({ json: { ...user, capabilities: ["read"] } }),
  )
  await page.goto("/packages?package=nginx")
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("button", { name: "Copy nginx", exact: true })).toBeVisible()
  await expect(sheet.getByRole("button", { name: /^Remove|^Update to|^Install$/ })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await expect(
    page.getByRole("button", { name: /^Upgrade all|^Install security|^Refresh index/ }),
  ).toHaveCount(0)
})

test("a new catalogue query hides old results while it is being read", async ({ page }) => {
  await mockHost(page)
  let release: (() => void) | undefined
  const waiting = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/packages/search?q=redis", async (route) => {
    await waiting
    await route.fulfill({
      json: [{ name: "redis-server", summary: "Key-value database", installed: false }],
    })
  })
  await page.goto("/packages")
  await page
    .getByRole("navigation", { name: "Package views" })
    .getByRole("button", { name: "Add software" })
    .click()
  const input = page.getByPlaceholder(/What do you need/)
  await input.fill("htop")
  await expect(page.getByText("2 matches")).toBeVisible()
  await input.fill("redis")
  await expect(page.getByRole("button", { name: "htop", exact: true })).toHaveCount(0)
  await expect(page.getByText("Searching the repositories…")).toBeVisible()
  release?.()
  await expect(page.getByRole("button", { name: "redis-server", exact: true })).toBeVisible()
})

test("the phone keeps identity controls and the installed table inside the screen", async ({
  page,
}) => {
  await mockHost(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/packages")
  const identity = page.locator("[data-slot='host-identity']")
  for (const name of ["Refresh index", "Re-read", "Packages shortcuts"]) {
    const control = identity.getByRole("button", { name, exact: true })
    await expect(control).toBeInViewport()
    const box = await control.boundingBox()
    expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  }
  await page.getByRole("button", { name: /^Everything/ }).click()
  const table = page.locator("[data-slot='table-container']")
  await expect(table).toBeVisible()
  expect(await table.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  await page
    .getByRole("row", { name: /nginx/ })
    .getByRole("button", { name: "nginx", exact: true })
    .click()
  const sheet = page.getByRole("dialog")
  await expect(
    sheet.getByRole("link", { name: "Open nginx.service", exact: true }),
  ).toHaveAttribute("href", "/processes/services?unit=nginx.service")
  await expect(
    sheet.getByRole("link", { name: "Open /etc/nginx/nginx.conf", exact: true }),
  ).toHaveAttribute("href", "/files?path=%2Fetc%2Fnginx%2Fnginx.conf")
  expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
})

test("package removal and upgrades still require confirmation", async ({ page }) => {
  await mockHost(page)
  const mutations: string[] = []
  page.on("request", (request) => {
    if (request.method() === "POST") mutations.push(new URL(request.url()).pathname)
  })
  await page.goto("/packages?package=nginx")
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Remove and purge", exact: true })
    .click()
  const confirm = page.getByRole("dialog", {
    name: "Remove nginx and its configuration",
    exact: true,
  })
  await expect(confirm).toContainText("and deletes the files it put in /etc")
  await expect(confirm.getByRole("textbox")).toHaveCount(0)
  expect(mutations).toEqual([])
  await confirm.getByRole("button", { name: "Cancel", exact: true }).click()
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Install security updates", exact: true }).click()
  await expect(
    page.getByRole("dialog", { name: "Install security updates", exact: true }),
  ).toContainText("restricted to the security pocket")
  expect(mutations).toEqual([])
})

const recordWorkspace = process.env.JD_WORKSPACE_VIDEO === "1"
test.use({ video: recordWorkspace ? "on" : "off" })

/**
 * The Packages page, checked in a browser against a mocked host.
 *
 * What these assert is what a type check cannot: that the page reads the way
 * the design system says (design-system.md §15) — the host's identity line
 * with its distribution as the mark and the manager and index age as facts,
 * a software-size band and an update queue, four views under
 * one underlined strip, and no framed block but a table anywhere on the page;
 * that every package is drawn as the software it is (§14) and an upgrade shows
 * the part of the version it changes; that the one decision worth making in a
 * hurry (security updates waiting) carries its own button above the fold; that
 * a row's fixed properties sit at its edge; that the search, the install
 * verb and the package sheet are all reachable from the strip; and that the
 * package manager's own log is read in place, through its lens. The
 * screenshots at 1280 and 1720 are the eyes the assertions do not have.
 */

/**
 * §2: the only block on a page that may draw a frame is a table. A grid owns a
 * scroll region, and an edge is what says where it ends — a row whose actions
 * sit past the boundary otherwise reads as a row with no actions. Everything
 * else in the page's flow stays plain, which is what the frame is read against.
 *
 * Asserted structurally rather than as a count, so the rule keeps holding as
 * pages gain and lose tables.
 */
async function framedNonTables(page: Page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"))
      .filter((el) => !el.querySelector("[data-slot=table-container]"))
      .map((el) => el.outerHTML.slice(0, 120)),
  )
}

test("the page opens on software and updates without summary cards", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")

  // The distribution as the mark, the manager beside its name, the index age
  // as a fact and what is owed as the verdict at the line's end.
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("Ubuntu 24.04")
  await expect(identity).toContainText("apt")
  await expect(identity).toContainText("index refreshed")
  await expect(identity).toContainText("1 security update")
  await expect(identity.locator("img[src='/logos/ubuntu.svg']")).toHaveCount(1)
  await expect(identity.getByRole("button", { name: "Refresh index" })).toBeVisible()

  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  const band = page.locator("[data-slot='software-band']")
  await expect(band.getByRole("heading", { name: "On disk" })).toBeVisible()
  await expect(band.getByRole("heading", { name: "Updates waiting" })).toBeVisible()
  await expect(band.getByRole("img", { name: /^Installed size:/ })).toBeVisible()
  await expect(band.getByRole("button", { name: "Only Linux packages" })).toBeVisible()
  await expect(band.locator("img[src='/logos/docker.svg']")).toHaveCount(1)
  await expect(band.getByRole("button", { name: "Inspect update for openssl" })).toContainText(
    "security",
  )
  await expect(band.getByRole("button", { name: "Inspect update for openssl" })).toBeVisible()

  // The decision, above the fold, with its own button.
  await expect(page.getByRole("button", { name: "Install security updates" })).toBeVisible()

  // Each view is a toolbar, a hairline and a framed table (§2) — and nothing
  // else on the page carries an edge.
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])
  // The pill-shaped tab list is gone; the strip is the product's underlined one.
  expect(await page.locator("[data-slot='tabs-list']").count()).toBe(0)
})

test("each package is drawn as the software it is", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")
  await page.getByRole("button", { name: /^Everything/ }).click()

  const logo = (name: string) => page.getByRole("row", { name }).locator("img[src^='/logos/']")
  await expect(logo("nginx")).toHaveAttribute("src", "/logos/nginx.svg")
  await expect(logo("postgresql-16")).toHaveAttribute("src", "/logos/postgresql.svg")
  await expect(logo("curl")).toHaveAttribute("src", "/logos/curl.svg")
  // A library nothing names keeps its section's glyph on the same tile.
  await expect(logo("libc6")).toHaveCount(0)
  await expect(page.getByRole("row", { name: /libc6/ }).locator("svg").first()).toBeVisible()
})

test("the installed view filters and puts a row's properties at its edge", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")

  const strip = page.getByRole("navigation", { name: "Package views" })
  await strip.getByRole("button", { name: /^Installed/ }).click()

  // Installed by hand is the default scope where the manager records it.
  await expect(page.getByRole("row", { name: /nginx/ })).toBeVisible()
  await expect(page.getByRole("row", { name: /libc6/ })).toHaveCount(0)

  await page.getByRole("button", { name: /^Everything/ }).click()
  const openssl = page.getByRole("row", { name: /openssl/ })
  await expect(openssl).toBeVisible()
  // The tags are the row's last cell, not part of its name.
  const lastCell = openssl.getByRole("cell").last()
  await expect(lastCell).toContainText("security")
  await expect(lastCell).toContainText("essential")

  await page.getByRole("button", { name: /^Behind/ }).click()
  await expect(page.getByRole("row", { name: /postgresql-16/ })).toHaveCount(0)
  await expect(page.getByRole("row", { name: /curl/ })).toBeVisible()

  // The readout opens directly on commands and files without a hidden usage tab.
  await page.getByRole("row", { name: /nginx/ }).getByRole("button", { name: "nginx" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet).toContainText("nginx")
  await expect(sheet.getByRole("tab")).toHaveCount(0)
  await expect(sheet).toContainText("systemctl start nginx.service")
  await expect(sheet).toContainText("/etc/nginx/nginx.conf")
  await expect(sheet.getByRole("heading", { name: "Commands", exact: true })).toBeVisible()
  await expect(sheet.getByRole("heading", { name: "Services", exact: true })).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Copy nginx", exact: true })).toBeVisible()
})

test("updates and add software are reachable from the strip", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")

  const strip = page.getByRole("navigation", { name: "Package views" })
  await strip.getByRole("button", { name: /^Updates/ }).click()
  await expect(page.getByText("3 packages behind · 1 security")).toBeVisible()
  await expect(page.getByRole("button", { name: "Upgrade all 3" }).last()).toBeVisible()
  const security = page.getByRole("row", { name: /openssl/ })
  await expect(security.getByRole("cell").last()).toContainText("security")
  // The upgrade keeps "3.0.13-0ubuntu3" and changes ".4", in amber for a fix.
  await expect(security.getByText(".4", { exact: true })).toHaveClass(/text-warning/)
  // The origin is the archive that published it.
  await expect(security.locator("img[src='/logos/ubuntu.svg']")).toHaveCount(1)

  await strip.getByRole("button", { name: "Add software" }).click()
  // An empty search offers software to look for, each drawn as itself.
  const suggestion = page.getByRole("button", { name: "postgresql", exact: true })
  await expect(suggestion.locator("img[src='/logos/postgresql.svg']")).toHaveCount(1)
  await suggestion.click()
  await expect(page.getByPlaceholder(/What do you need/)).toHaveValue("postgresql")
  await page.getByPlaceholder(/What do you need/).fill("htop")
  await expect(page.getByText("2 matches")).toBeVisible()
  const htop = page.getByRole("listitem").filter({ hasText: "htop" }).first()
  await expect(htop.getByRole("button", { name: "Install" })).toBeVisible()
  // An installed result offers no install, and says so as a state.
  const btop = page.getByRole("listitem").filter({ hasText: "btop" }).first()
  await expect(btop.getByRole("button", { name: "Install" })).toHaveCount(0)
  await expect(btop).toContainText("Installed")

  await htop.getByRole("button", { name: "Install" }).click()
  await expect(htop).toContainText("Started")
  // The job's output opens at the top of the page.
  await expect(page.getByText("Install htop")).toBeVisible()
})

test("the Log view reads the package manager's own log in place", async ({ page }) => {
  await mockHost(page)
  const logs = await mockHostLogs(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")
  // Nothing of the log is asked for until the view is.
  expect(logs.requests).toEqual([])

  const strip = page.getByRole("navigation", { name: "Package views" })
  await strip.getByRole("button", { name: "Log", exact: true }).click()
  const lines = page.getByLabel("Log lines")
  await expect(lines.getByText("install", { exact: true })).toBeVisible()
  await expect(lines.getByText("upgrade", { exact: true })).toBeVisible()
  await expect(lines.getByText("remove", { exact: true })).toBeVisible()

  // History and Insights over apt's transactions, opened on everything the
  // file holds — a day of a package log is usually nothing — and no tail.
  const history = logs.searches[0]
  expect(history.get("source")).toBe("file:/var/log/apt/history.log")
  // Every package log is the packages lens's on the server, and the page
  // hands the pane the server's own description: no lens of its own, and no
  // file asked after twice.
  expect(history.has("lens")).toBe(false)
  await expect(page.getByRole("button", { name: "More", exact: true })).toBeVisible()
  expect(history.has("since")).toBe(false)
  expect(logs.requests).not.toContain("/logs/sources")
  // One for each of the seven files the view asks after (`package-logs.ts`).
  expect(logs.requests.filter((path) => path === "/logs/source")).toHaveLength(7)
  expect(logs.sockets).toHaveLength(0)
  await expect(page.getByRole("button", { name: "Live", exact: true })).toHaveCount(0)
  await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)

  await page.getByRole("combobox", { name: "Package log" }).click()
  await page.getByRole("option", { name: "dpkg" }).click()
  await expect(lines.getByText("configure", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Insights", exact: true }).click()
  await expect(page.getByRole("heading", { name: "By package" })).toBeVisible()
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])
})

test("the package log stays on the page when the inventory cannot be read", async ({ page }) => {
  await mockHost(page)
  await mockHostLogs(page)
  await page.route("**/api/v1/packages/", (route) =>
    route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "internal",
          message: "dpkg was interrupted, you must manually run dpkg --configure -a",
        },
      }),
    }),
  )
  await page.goto("/packages")
  await expect(page.getByText(/dpkg was interrupted/).first()).toBeVisible()
  // The views are gone with the inventory; apt's own record of what it was
  // doing when it stopped is not.
  await expect(page.getByRole("navigation", { name: "Package views" })).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Package log" })).toBeVisible()
  await expect(page.getByLabel("Log lines").getByText("install", { exact: true })).toBeVisible()
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])
})

for (const width of [390, 768, 1024, 1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await mockHost(page)
    await mockHostLogs(page)
    await page.setViewportSize({ width, height: 1000 })
    await page.goto("/packages")
    await page.waitForLoadState("networkidle")
    const strip = page.getByRole("navigation", { name: "Package views" })
    for (const [name, label] of [
      ["installed", "Installed"],
      ["updates", "Updates"],
      ["install", "Add software"],
      ["log", "Log"],
    ] as const) {
      await strip.getByRole("button", { name: new RegExp(`^${label}`) }).click()
      if (name === "installed") await page.getByRole("button", { name: /^Everything/ }).click()
      if (name === "installed" || name === "updates") {
        const table = page.locator("[data-slot='table-container']")
        if (await table.count())
          expect(
            await table.evaluate((el) => el.scrollWidth <= el.clientWidth),
            `${name} table overflows at ${width}`,
          ).toBe(true)
      }
      await expect(page.locator("[data-slot='host-identity']")).toContainText("Ubuntu 24.04")
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow, `${name} scrolls sideways at ${width}`).toBe(false)
      const unnamed = await page.evaluate(() => {
        const bad: string[] = []
        for (const el of document.querySelectorAll<HTMLElement>("button")) {
          if (el.offsetParent === null) continue
          if ((el.textContent ?? "").trim().length > 0) continue
          if (!el.getAttribute("aria-label") && !el.querySelector(".sr-only")) {
            bad.push(el.outerHTML.slice(0, 120))
          }
        }
        return bad
      })
      expect(unnamed, `unlabelled icon-only controls on ${name}`).toEqual([])
      await page.screenshot({ path: `test-results/packages-${name}-${width}.png`, fullPage: true })
    }
  })
}

test("workspace: package links, Back, keyboard movement and local commands preserve context", async ({
  page,
}, testInfo) => {
  await mockHost(page)
  await page.goto("/packages")
  await expect(page.locator("[data-workspace-item]").first()).toBeVisible()
  const first = page.locator("[data-workspace-item]").first()
  await first.focus()
  await page.keyboard.press("ArrowDown")
  const second = page.locator("[data-workspace-item]").nth(1)
  await expect(second).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("dialog")).toBeVisible()
  await expect(page).toHaveURL(/package=/)
  if (recordWorkspace) {
    await page.waitForTimeout(700)
    await page.screenshot({ path: testInfo.outputPath("package-inspector.png") })
  }
  const shared = page.url()
  await page.goBack()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(second).toBeFocused()
  if (recordWorkspace) await page.waitForTimeout(700)
  await page.goto(shared)
  await expect(page.getByRole("dialog")).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await page.keyboard.press("Control+f")
  await expect(page.locator("[data-page-search]")).toBeFocused()
  await page.locator("[data-page-search]").fill("nginx")
  await page.keyboard.press("Escape")
  await expect(page.locator("[data-page-search]")).toHaveValue("")
  await page.keyboard.press("?")
  await expect(page.getByRole("dialog", { name: "Packages shortcuts" })).toBeVisible()
  if (recordWorkspace) {
    await page.waitForTimeout(700)
    await page.screenshot({ path: testInfo.outputPath("package-shortcuts.png") })
  }
  await page.keyboard.press("Escape")
  await page.keyboard.press("Control+k")
  await page.getByRole("option", { name: /Find in Packages/ }).click()
  await expect(page.locator("[data-page-search]")).toBeFocused()
})

test("workspace: Add software arrows inspect results without starting an installation", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/packages")
  await page
    .getByRole("navigation", { name: "Package views" })
    .getByRole("button", { name: "Add software" })
    .click()
  await page.getByPlaceholder(/What do you need/).fill("htop")
  await expect(page.getByText("2 matches")).toBeVisible()
  await page.getByPlaceholder(/What do you need/).press("Tab")
  await page.locator("[data-workspace-item]").first().focus()
  await page.keyboard.press("ArrowDown")
  await expect(page.locator("[data-workspace-item]").nth(1)).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("dialog")).toContainText("btop")
  await expect(page.getByText("Started", { exact: true })).toHaveCount(0)
})
