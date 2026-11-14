import { expect, test, type Page } from "@playwright/test"
import { mockHostLogs } from "./host-logs-fixture"
import { mockHost, installed, inventory, report, user } from "./packages-fixture"
import { risen } from "./risen"

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
  await expect(
    page.locator("[title='pacman publishes no security advisory data.']").first(),
  ).toBeVisible()
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

test("read-only accounts can read the software and are offered no package mutation", async ({
  page,
}) => {
  await mockHost(page)
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({ json: { ...user, capabilities: ["read"] } }),
  )
  await page.goto("/packages")
  await expect(page.getByRole("row", { name: /nginx/ })).toBeVisible()
  await expect(
    page.getByRole("button", { name: /^Upgrade all|^Install security|^Refresh index/ }),
  ).toHaveCount(0)
  // The menu holds what is left: reading the host again.
  await page.getByRole("button", { name: "Package actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Re-read"])
  await page.keyboard.press("Escape")
  await page
    .getByRole("navigation", { name: "Package views" })
    .getByRole("button", { name: "Add software" })
    .click()
  await page.getByPlaceholder(/What do you need/).fill("htop")
  await expect(page.getByText("2 matches")).toBeVisible()
  await expect(page.getByRole("listitem").filter({ hasText: "htop" })).toBeVisible()
  await expect(page.getByRole("button", { name: /^(Retry install|Install)$/ })).toHaveCount(0)
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
  await expect(page.getByRole("link", { name: "htop", exact: true })).toHaveCount(0)
  await expect(page.getByText("Searching the repositories…")).toBeVisible()
  release?.()
  await expect(page.getByRole("link", { name: "redis-server", exact: true })).toHaveAttribute(
    "href",
    "/packages/redis-server",
  )
})

test("on a phone the line keeps its controls and each package is a card", async ({ page }) => {
  await mockHost(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/packages")
  const identity = page.locator("[data-slot='host-identity']")
  for (const name of ["Refresh index", "Package actions"]) {
    const control = identity.getByRole("button", { name, exact: true })
    await expect(control).toBeInViewport()
    const box = await control.boundingBox()
    expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  }
  await page.getByRole("button", { name: /^Everything/ }).click()
  // No table squeezed to two cut-off columns: a card a package, one height
  // whether it is behind, a security fix, or neither, each inside the screen.
  await expect(page.locator("[data-slot='table-container']")).toHaveCount(0)
  const cards = page.getByRole("list", { name: "Installed packages" }).getByRole("listitem")
  await expect(cards).toHaveCount(installed.length)
  await risen(page)
  const boxes = await cards.evaluateAll((items) =>
    items.map((item) => item.getBoundingClientRect().toJSON()),
  )
  expect(new Set(boxes.map((box) => box.height)).size).toBe(1)
  const step = boxes[1].y - boxes[0].y
  boxes.slice(1).forEach((box, i) => expect(box.y - boxes[i].y).toBe(step))
  for (const box of boxes) expect(box.x + box.width).toBeLessThanOrEqual(390)
  const openssl = cards.filter({ hasText: "openssl" })
  await expect(openssl).toContainText("3.0.13-0ubuntu3.4")
  await expect(openssl.getByText(".4", { exact: true })).toHaveClass(/text-warning/)
  await expect(cards.filter({ hasText: "postgresql-16" })).toContainText("45.8 MB")

  await page
    .getByRole("navigation", { name: "Package views" })
    .getByRole("button", { name: /^Updates/ })
    .click()
  const behind = page.getByRole("list", { name: "Packages behind" }).getByRole("listitem")
  await expect(behind).toHaveCount(3)
  await expect(behind.filter({ hasText: "openssl" })).toContainText("security")
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)

  await behind
    .filter({ hasText: "nginx" })
    .getByRole("link", { name: "nginx", exact: true })
    .click()
  await expect(page).toHaveURL(/\/packages\/nginx$/)
})

test("upgrades still require confirmation, from the band and from the menu", async ({ page }) => {
  await mockHost(page)
  const mutations: string[] = []
  page.on("request", (request) => {
    if (request.method() === "POST") mutations.push(new URL(request.url()).pathname)
  })
  await page.goto("/packages")
  await page.getByRole("button", { name: "Install security updates", exact: true }).click()
  const security = page.getByRole("dialog", { name: "Install security updates", exact: true })
  await expect(security).toContainText("restricted to the security pocket")
  expect(mutations).toEqual([])
  await security.getByRole("button", { name: "Cancel", exact: true }).click()

  await page.getByRole("button", { name: "Package actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText([
    "Re-read",
    "Install security updates",
    "Upgrade all 3",
  ])
  await page.getByRole("menuitem", { name: "Upgrade all 3", exact: true }).click()
  const all = page.getByRole("dialog", { name: "Upgrade all packages", exact: true })
  await expect(all).toContainText("Affected services may restart")
  expect(mutations).toEqual([])
  await all.getByRole("button", { name: "Upgrade", exact: true }).click()
  await expect.poll(() => mutations).toEqual(["/api/v1/packages/upgrade"])
})

test("the index is refreshed from the line, without a confirmation", async ({ page }) => {
  await mockHost(page)
  const mutations: string[] = []
  page.on("request", (request) => {
    if (request.method() === "POST") mutations.push(new URL(request.url()).pathname)
  })
  let reads = 0
  page.on("request", (request) => {
    if (/\/api\/v1\/packages\/$/.test(request.url())) reads++
  })
  await page.goto("/packages")
  const identity = page.locator("[data-slot='host-identity']")
  await identity.getByRole("button", { name: "Refresh index", exact: true }).click()
  await expect.poll(() => mutations).toEqual(["/api/v1/packages/refresh"])
  await expect(page.getByRole("dialog")).toHaveCount(0)
  const before = reads
  await page.getByRole("button", { name: "Package actions" }).click()
  await page.getByRole("menuitem", { name: "Re-read", exact: true }).click()
  await expect.poll(() => reads).toBeGreaterThan(before)
})

test("a search handed over in the address opens Add software on it", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages?q=htop")
  await expect(page.getByPlaceholder(/What do you need/)).toHaveValue("htop")
  await expect(page.getByText("2 matches")).toBeVisible()
  // The address is the page's again, and the strip still switches views.
  await expect(page).toHaveURL(/\/packages$/)
  const strip = page.getByRole("navigation", { name: "Package views" })
  await expect(strip.getByRole("button", { name: "Add software" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await strip.getByRole("button", { name: /^Installed/ }).click()
  await expect(page.getByRole("row", { name: /nginx/ })).toBeVisible()
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
 * a row is one line with its fixed properties at its edge and its size a
 * figure in a column; that the search and the install verb are reachable
 * from the strip and a package's name is the way to its page
 * (`package-page.spec.ts`); and that the package manager's own log is read in
 * place, through its lens. The screenshots at 1280 and 1720 are the eyes the
 * assertions do not have.
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
  // Laid out as a project's header: one command, and the rest behind a menu.
  await expect(identity).toHaveAttribute("data-layout", "project")
  await expect(identity.getByRole("button", { name: "Refresh index" })).toBeVisible()
  await expect(identity.getByRole("button", { name: "Package actions" })).toBeVisible()
  await expect(identity.getByRole("button", { name: "Re-read" })).toHaveCount(0)

  await expect(page.locator("[data-slot='stat-tile'], [data-slot='stat-grid']")).toHaveCount(0)
  const band = page.locator("[data-slot='software-band']")
  await expect(band.getByRole("heading", { name: "On disk" })).toBeVisible()
  await expect(band.getByRole("heading", { name: "Updates waiting" })).toBeVisible()
  await expect(band.getByRole("img", { name: /^On disk:/ })).toBeVisible()
  await expect(band.getByRole("button", { name: "Only Linux packages" })).toBeVisible()
  await expect(band.locator("img[src='/logos/docker.svg']")).toHaveCount(1)
  const updates = band.getByLabel("Updates waiting")
  await updates.getByRole("button", { name: "Inspect update for openssl" }).hover()
  const line = updates.locator("[data-slot=reading-line]")
  await expect(line).toContainText("openssl")
  await expect(line).toContainText("security")
  await page.mouse.move(0, 0)

  // The decision, above the fold, with its own button.
  await expect(page.getByRole("button", { name: "Install security updates" })).toBeVisible()

  // Each view is a toolbar, a hairline and a framed table (§2) — and nothing
  // else on the page carries an edge.
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])
  // The pill-shaped tab list is gone; the strip is the product's underlined one.
  expect(await page.locator("[data-slot='tabs-list']").count()).toBe(0)
})

for (const width of [1440, 390]) {
  test(`the band is there before the inventory, and keeps its height when it lands at ${width}`, async ({
    page,
  }) => {
    await mockHost(page)
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route(/\/api\/v1\/packages\/$/, async (route) => {
      await held
      await route.fallback()
    })
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/packages")
    const band = page.locator("[data-slot='software-band']")
    const identity = page.locator("[data-slot='host-identity']")
    const strip = page.getByRole("navigation", { name: "Package views" })
    await expect(band.getByText("Reading the installed packages…")).toBeVisible()
    await risen(page)
    const before = await band.boundingBox()
    const line = await identity.boundingBox()
    const views = await strip.boundingBox()
    release()
    await expect(band.getByRole("button", { name: "Only Linux packages" })).toBeVisible()
    await risen(page)
    const after = await band.boundingBox()
    expect(after?.height).toBe(before?.height)
    expect(after?.y).toBe(before?.y)
    // The line above it and the strip of views under it were already there.
    expect(await identity.boundingBox()).toEqual(line)
    expect(await strip.boundingBox()).toEqual(views)
  })
}

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

  // The name is the way to the package's own page; nothing opens over the list.
  const name = page.getByRole("row", { name: /nginx/ }).getByRole("link", { name: "nginx" })
  await expect(name).toHaveAttribute("href", "/packages/nginx")
  await name.click()
  await expect(page).toHaveURL(/\/packages\/nginx$/)
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

test("a row is one line, and its size is a figure in a column", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.getByRole("button", { name: /^Everything/ }).click()
  const rows = page.locator("[data-slot='table-body'] [data-slot='table-row']")
  await expect(rows).toHaveCount(installed.length)
  await risen(page)
  // Behind, a security fix with two tags, essential, and none of them: one
  // height. The last row has no rule under it, which is its only difference.
  const heights = await rows.evaluateAll((items) =>
    items.map((item) => item.getBoundingClientRect().height),
  )
  expect(new Set(heights.slice(0, -1)).size).toBe(1)
  expect(heights[0] - heights.at(-1)!).toBeLessThanOrEqual(1)
  expect(heights[0]).toBeLessThan(60)
  // The description is the row's title, not a second line under the name.
  const nginx = page.getByRole("row", { name: /nginx/ })
  await expect(nginx).not.toContainText("small, powerful")
  await expect(nginx.locator("[title='small, powerful, scalable web/proxy server']")).toHaveCount(1)
  // No bar under a size; the figures share an edge.
  await expect(page.locator("[data-slot='table'] [style*='width']")).toHaveCount(0)
  const size = (row: string) =>
    page.getByRole("row", { name: row }).getByRole("cell").nth(3).boundingBox()
  const [small, large] = [await size("nginx"), await size("postgresql-16")]
  expect(small!.x + small!.width).toBe(large!.x + large!.width)
  await expect(
    page
      .getByRole("row", { name: /postgresql-16/ })
      .getByRole("cell")
      .nth(3),
  ).toHaveText("45.8 MB")
  // Largest first re-orders the rows and moves nothing else.
  const table = await page.locator("[data-slot='table-container']").boundingBox()
  await page.getByRole("button", { name: "By name" }).click()
  await expect(rows.first()).toContainText("linux-image")
  expect(await page.locator("[data-slot='table-container']").boundingBox()).toEqual(table)
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
  await expect(htop).toContainText("Installing…")
  // The job's output opens at the top of the page.
  await expect(page.getByText("Install htop", { exact: true })).toBeVisible()
})

for (const outcome of ["failed", "cancelled"] as const) {
  test(`an install that is ${outcome} offers retry from its result row`, async ({ page }) => {
    await mockHost(page)
    let status: "running" | "failed" | "cancelled" = "running"
    let attempts = 0
    const job = (id: string) => ({
      id,
      kind: "packages.install",
      title: "Install htop",
      target: "htop",
      status,
      exitCode: status === "failed" ? 1 : 0,
      startedAt: new Date().toISOString(),
      lines: 0,
    })
    await page.route("**/api/v1/packages/install", (route) => {
      attempts++
      status = "running"
      return route.fulfill({ json: job(`job-${attempts}`) })
    })
    await page.route(/\/api\/v1\/jobs\/job-\d+$/, (route) =>
      route.fulfill({
        json: { job: job(new URL(route.request().url()).pathname.split("/").at(-1)!), lines: [] },
      }),
    )
    // No stream completion: result rows also read their own jobs, including
    // an earlier install after the output console has switched to another.
    await page.routeWebSocket("**/api/v1/jobs/*/stream**", () => {})
    await page.goto("/packages")
    await page
      .getByRole("navigation", { name: "Package views" })
      .getByRole("button", { name: "Add software" })
      .click()
    await page.getByPlaceholder(/What do you need/).fill("htop")
    const htop = page.getByRole("listitem").filter({ hasText: "htop" }).first()
    await htop.getByRole("button", { name: "Install", exact: true }).click()
    await expect(htop).toContainText("Installing…")
    status = outcome
    await expect(htop.getByRole("button", { name: "Retry install" })).toBeVisible()
    await expect(htop).toContainText(outcome === "failed" ? "Failed" : "Cancelled")
    await htop.getByRole("button", { name: "Retry install" }).click()
    await expect(htop).toContainText("Installing…")
    await expect(htop.getByRole("button", { name: "Retry install" })).toHaveCount(0)
    expect(attempts).toBe(2)
  })
}

test("a successful install refreshes the search result's installed metadata", async ({ page }) => {
  await mockHost(page)
  let installed = false
  let searchReads = 0
  const job = {
    id: "job-1",
    kind: "packages.install",
    title: "Install htop",
    target: "htop",
    status: "running",
    exitCode: 0,
    startedAt: new Date().toISOString(),
    lines: 0,
  }
  await page.route("**/api/v1/packages/search**", (route) => {
    searchReads++
    return route.fulfill({
      json: [{ name: "htop", version: "3.3.0", summary: "Process viewer", installed }],
    })
  })
  await page.route("**/api/v1/packages/install", (route) => route.fulfill({ json: job }))
  await page.route("**/api/v1/jobs/job-1", (route) =>
    route.fulfill({
      json: {
        job: { ...job, status: installed ? "succeeded" : "running" },
        lines: [],
      },
    }),
  )
  await page.routeWebSocket("**/api/v1/jobs/*/stream**", () => {})
  await page.goto("/packages")
  await page
    .getByRole("navigation", { name: "Package views" })
    .getByRole("button", { name: "Add software" })
    .click()
  await page.getByPlaceholder(/What do you need/).fill("htop")
  const htop = page.getByRole("listitem").filter({ hasText: "htop" }).first()
  await htop.getByRole("button", { name: "Install", exact: true }).click()
  await expect(htop).toContainText("Installing…")
  installed = true
  await expect(htop).toContainText("Installed")
  await expect(htop.getByRole("button", { name: "Install", exact: true })).toHaveCount(0)
  expect(searchReads).toBeGreaterThan(1)
})

for (const [manager, text] of [
  ["apt", "holds back updates that require new packages or removals"],
  ["dnf", "may install new dependencies and replace obsolete packages"],
  ["yum", "may install new dependencies and replace obsolete packages"],
  ["zypper", "may add or replace packages"],
  ["pacman", "upgrades the full system"],
  ["apk", "fresh repository index"],
]) {
  test(`the ${manager} upgrade confirmation states that manager's transaction scope`, async ({
    page,
  }) => {
    await mockHost(page)
    await page.route("**/api/v1/packages/", (route) =>
      route.fulfill({ json: { ...inventory, manager } }),
    )
    await page.goto("/packages")
    await page
      .getByRole("button", { name: /^Upgrade all/ })
      .first()
      .click()
    const dialog = page.getByRole("dialog", { name: "Upgrade all packages", exact: true })
    await expect(dialog).toContainText(text)
    await expect(dialog).not.toContainText(
      "New packages are never installed and nothing is removed",
    )
    await expect(dialog).toContainText("Affected services may restart")
  })
}

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
  await expect(page.getByRole("button", { name: "Options", exact: true })).toBeVisible()
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
  await expect(page).toHaveURL(/\/packages\/postgresql-16$/)
  await expect(page.locator("[data-slot='package-identity']")).toContainText("postgresql-16")
  if (recordWorkspace) {
    await page.waitForTimeout(700)
    await page.screenshot({ path: testInfo.outputPath("package-page.png") })
  }
  await page.goBack()
  await expect(page).toHaveURL(/\/packages$/)
  await expect(page.getByRole("row", { name: /postgresql-16/ })).toBeVisible()
  if (recordWorkspace) await page.waitForTimeout(700)
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
  const posts: string[] = []
  page.on("request", (request) => {
    if (request.method() === "POST") posts.push(new URL(request.url()).pathname)
  })
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/\/packages\/btop$/)
  await expect(page.locator("[data-slot='package-identity']")).toContainText("btop")
  expect(posts).toEqual([])
})
