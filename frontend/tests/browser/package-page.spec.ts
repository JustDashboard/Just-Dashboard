import { expect, test, type Page } from "@playwright/test"
import { mockHostLogs } from "./host-logs-fixture"
import { detail, mockHost, installed, inventory, user } from "./packages-fixture"
import { risen } from "./risen"

/**
 * A package's own page, checked in a browser against a mocked host.
 *
 * A package's detail was a sheet over the list. What these assert is what the
 * page promises in its place: the old address lands on it; it opens on the
 * package's identity line with its state and its one command, the rest behind
 * a menu, and no metric cards; its views are in the address and go with the
 * reader to the next package; the installed list stands beside it from `xl`
 * with the list's own filter, and Alt+↑ and Alt+↓ walk it; a name a path
 * cannot hold bare still opens; every verb the sheet had still runs, behind
 * the confirmation it had; and an account that cannot act sees no control it
 * cannot use.
 */

const job = (over: Record<string, unknown> = {}) => ({
  id: "job-9",
  kind: "packages.install",
  title: "Installing nginx",
  target: "nginx",
  status: "running",
  exitCode: 0,
  startedAt: new Date().toISOString(),
  lines: 0,
  ...over,
})

/** Records every mutation, so a spec can say what was and was not asked of the host. */
function mutations(page: Page) {
  const seen: { path: string; body: unknown }[] = []
  page.on("request", (request) => {
    if (request.method() === "GET") return
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    seen.push({ path, body: request.postDataJSON() })
  })
  return seen
}

const identity = (page: Page) => page.locator("[data-slot='package-identity']")
const rail = (page: Page) => page.locator("[data-slot='package-rail']")
/** The way back to the list, which the navigation also names. */
const back = (page: Page) =>
  page.locator("[data-slot='page-context']").getByRole("link", { name: "Packages", exact: true })

test("the address that opened the sheet lands on the package's page", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages?package=nginx")
  await expect(page).toHaveURL(/\/packages\/nginx$/)

  // The line it opens on: the package as the software it is, its name, its
  // state with the version it is at and the one it would move to, then what
  // it is in a sentence and its facts.
  const line = identity(page)
  await expect(line.locator("img[src='/logos/nginx.svg']")).toHaveCount(1)
  await expect(line).toContainText("nginx")
  await expect(line).toContainText("Installed")
  await expect(line).toContainText("1.24.0-2ubuntu7")
  await expect(line).toContainText("update to")
  await expect(line).toContainText("small, powerful, scalable web/proxy server")
  await expect(line).toContainText("1.5 MB on disk")
  await expect(line).toContainText("installed by hand")
  await expect(line.getByRole("button", { name: "Update", exact: true })).toBeVisible()
  await expect(back(page)).toHaveAttribute("href", "/packages")

  await expect(page.locator("[data-slot='stat-grid'], [data-slot='stat-tile']")).toHaveCount(0)
  await expect(page.getByRole("dialog")).toHaveCount(0)

  // What it needs, the package, what it gives: one picture.
  const picture = page.locator("[data-slot='wire-flow']")
  await expect(picture.getByRole("link", { name: "Open libc6", exact: true })).toHaveAttribute(
    "href",
    "/packages/libc6",
  )
  await expect(picture.getByRole("link", { name: "Open nginx.service" })).toHaveAttribute(
    "href",
    "/processes/services/nginx.service",
  )
  await picture.getByRole("button", { name: "Show its 2 commands" }).click()
  await expect(page).toHaveURL(/\/packages\/nginx\?tab=files$/)
  await expect(page.getByRole("button", { name: "Copy nginx-debug", exact: true })).toBeVisible()
})

test("the views are in the address and hold everything the sheet did", async ({ page }) => {
  await mockHost(page)
  await mockHostLogs(page)
  await page.goto("/packages/nginx?tab=files")
  const tabs = page.getByRole("tablist", { name: "Views of nginx" })
  await expect(tabs.getByRole("tab", { name: /^Files/ })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("heading", { name: "Commands", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy nginx", exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Open nginx.service", exact: true })).toHaveAttribute(
    "href",
    "/processes/services/nginx.service",
  )
  await expect(
    page.getByRole("button", { name: "Copy systemctl start nginx.service", exact: true }),
  ).toBeVisible()
  await expect(
    page.getByRole("link", { name: "Open /etc/nginx/nginx.conf", exact: true }),
  ).toHaveAttribute("href", "/files?path=%2Fetc%2Fnginx%2Fnginx.conf")

  await tabs.getByRole("tab", { name: "Manual", exact: true }).click()
  await expect(page).toHaveURL(/\?tab=manual$/)
  await expect(page.getByRole("heading", { name: "man nginx", exact: true })).toBeVisible()
  await expect(page.locator("pre")).toContainText("NGINX(8)")

  await tabs.getByRole("tab", { name: /^Depends/ }).click()
  await expect(page).toHaveURL(/\?tab=depends$/)
  const needs = page.getByRole("list", { name: "What nginx depends on" })
  await expect(needs.getByRole("listitem")).toHaveCount(3)
  await expect(needs.getByRole("listitem").filter({ hasText: "libc6" })).toContainText(
    "2.39-0ubuntu8",
  )
  await expect(needs.getByRole("listitem").filter({ hasText: "libpcre2-8-0" })).toContainText(
    "not installed",
  )

  // The package manager's own record, narrowed to this package: dpkg's
  // per-package log first, searched for the name.
  await tabs.getByRole("tab", { name: "Log", exact: true }).click()
  await expect(page).toHaveURL(/\?tab=log$/)
  await expect(page.getByRole("combobox", { name: "Package log" })).toContainText("dpkg")
  await expect(page.getByRole("button", { name: "Live", exact: true })).toHaveCount(0)

  await tabs.getByRole("tab", { name: "Overview", exact: true }).click()
  await expect(page).toHaveURL(/\/packages\/nginx$/)
  await expect(page.getByRole("heading", { name: "About", exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: /example\.org\/nginx/ })).toHaveAttribute(
    "href",
    "https://example.org/nginx",
  )
})

test("a package's log is the manager's, searched for its name", async ({ page }) => {
  await mockHost(page)
  const logs = await mockHostLogs(page)
  await page.goto("/packages/openssl?tab=log")
  const lines = page.getByLabel("Log lines")
  await expect(lines.getByText("upgrade", { exact: true })).toBeVisible()
  const search = logs.searches.at(-1)!
  expect(search.get("source")).toBe("file:/var/log/dpkg.log")
  expect(search.get("q")).toBe("openssl")
  // The list's own Log view is another reading: it is not left narrowed.
  await page.goto("/packages")
  await page
    .getByRole("navigation", { name: "Package views" })
    .getByRole("button", { name: "Log", exact: true })
    .click()
  await expect(page.getByLabel("Log lines").getByText("install", { exact: true })).toBeVisible()
  expect(logs.searches.at(-1)!.get("q")).toBeNull()
})

test("a dependency opens on the view the reader was on, and a failed read says so", async ({
  page,
}) => {
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
  await page.goto("/packages/nginx?tab=depends")
  await page.getByRole("link", { name: "Open libc6", exact: true }).click()
  await expect(page).toHaveURL(/\/packages\/libc6\?tab=depends$/)
  await expect(page.getByText("This package depends on no other package.")).toBeVisible()

  await page.getByRole("tab", { name: /^Files/ }).click()
  await expect(page.getByText("Package file list is not readable")).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy nginx", exact: true })).toHaveCount(0)
  await page.getByRole("tab", { name: "Overview", exact: true }).click()
  await expect(page.locator("[data-slot='wire-flow']")).toContainText("Not read")

  await page.goBack()
  await expect(page).toHaveURL(/\/packages\/nginx\?tab=depends$/)
  await expect(page.getByRole("list", { name: "What nginx depends on" })).toBeVisible()
})

test("the list stands beside the package with its own filter, and Alt and the arrows walk it", async ({
  page,
}) => {
  await mockHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/packages/nginx?tab=files")
  const list = rail(page)
  await expect(list).toBeVisible()
  // The list as it was left: what was installed by hand, by name.
  await expect(list.getByText("9 of 14 · installed by hand")).toBeVisible()
  await expect(list.getByRole("link")).toHaveCount(9)
  await expect(list.getByRole("link", { name: "nginx", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await expect(list.getByRole("link", { name: "postgresql-16", exact: true })).toHaveAttribute(
    "href",
    "/packages/postgresql-16?tab=files",
  )

  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/packages\/postgresql-16\?tab=files$/)
  await expect(identity(page)).toContainText("postgresql-16")
  await expect(page.getByRole("tab", { name: /^Files/ })).toHaveAttribute("aria-selected", "true")
  await expect(list.getByRole("link", { name: "postgresql-16", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await page.keyboard.press("Alt+ArrowUp")
  await expect(page).toHaveURL(/\/packages\/nginx\?tab=files$/)
  await expect(page.getByRole("button", { name: "Previous package" })).toBeDisabled()
  await page.getByRole("button", { name: "Next package" }).click()
  await expect(page).toHaveURL(/\/packages\/postgresql-16\?tab=files$/)

  // A dependency is not on a list of what was installed by hand: it is drawn
  // first, filled, and the list is still the list.
  await page.goto("/packages/libc6")
  await expect(list.getByRole("link")).toHaveCount(10)
  await expect(list.getByRole("link").first()).toHaveAttribute("aria-current", "page")
  await expect(list.getByRole("link").first()).toContainText("libc6")

  await list.getByRole("button", { name: "Clear filters" }).click()
  await expect(list.getByText("14 packages")).toBeVisible()
  await expect(list.getByRole("link")).toHaveCount(14)
  await list.getByLabel("Search the installed packages").fill("database")
  await expect(list.getByRole("link")).toHaveCount(3)
  await expect(list.getByText("2 of 14")).toBeVisible()

  // One filter, two places: the list page opens on what the rail was left on.
  await back(page).click()
  await expect(page).toHaveURL(/\/packages$/)
  await expect(page.getByLabel("Find installed packages")).toHaveValue("database")
  await expect(page.getByRole("row", { name: /redis-server/ })).toBeVisible()
  await expect(page.getByRole("row", { name: /nginx/ })).toHaveCount(0)
})

test("a name a path cannot hold bare opens its page", async ({ page }) => {
  await mockHost(page)
  const compiler = {
    name: "g++-13",
    version: "13.2.0-23ubuntu4",
    summary: "GNU C++ compiler",
    size: 37_000_000,
    section: "devel",
    explicit: true,
  }
  await page.route(/\/api\/v1\/packages\/$/, (route) =>
    route.fulfill({
      json: {
        ...inventory,
        packages: [compiler, ...installed],
        explicitCount: inventory.explicitCount + 1,
      },
    }),
  )
  await page.route(/\/api\/v1\/packages\/g%2B%2B-13$/, (route) =>
    route.fulfill({
      json: {
        ...detail("g++-13"),
        ...compiler,
        description: compiler.summary,
        installed: true,
        installedVersion: compiler.version,
      },
    }),
  )
  const asked: string[] = []
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname
    if (path.startsWith("/api/v1/packages/g")) asked.push(path)
  })
  await page.goto("/packages")
  const link = page.getByRole("row", { name: /g\+\+-13/ }).getByRole("link")
  await expect(link).toHaveAttribute("href", "/packages/g%2B%2B-13")
  await link.click()
  await expect(page).toHaveURL(/\/packages\/g%2B%2B-13$/)
  await expect(identity(page)).toContainText("g++-13")
  await expect(identity(page)).toContainText("GNU C++ compiler")
  expect(asked).toContain("/api/v1/packages/g%2B%2B-13")
  expect(asked).toContain("/api/v1/packages/g%2B%2B-13/usage")

  // Its views keep the name whole, and the way on from it too.
  await page.getByRole("tab", { name: /^Depends/ }).click()
  await expect(page).toHaveURL(/\/packages\/g%2B%2B-13\?tab=depends$/)
  await page.reload()
  await expect(identity(page)).toContainText("g++-13")
  await expect(page.getByRole("link", { name: "Open gcc-13", exact: true })).toHaveAttribute(
    "href",
    "/packages/gcc-13?tab=depends",
  )
})

test("updating runs where it is read, and the page says what happened", async ({ page }) => {
  await mockHost(page)
  const seen = mutations(page)
  let upgraded = false
  await page.route("**/api/v1/packages/nginx", (route) =>
    route.fulfill({
      json: {
        name: "nginx",
        installed: true,
        installedVersion: upgraded ? "1.24.0-2ubuntu7.1" : "1.24.0-2ubuntu7",
        version: "1.24.0-2ubuntu7.1",
        upgradable: upgraded ? undefined : "1.24.0-2ubuntu7.1",
        summary: "small, powerful, scalable web/proxy server",
        section: "httpd",
        size: 1_540_000,
        dependencies: ["libc6"],
      },
    }),
  )
  await page.route("**/api/v1/packages/install", (route) => route.fulfill({ json: job() }))
  let finish: (() => void) | undefined
  await page.routeWebSocket("**/api/v1/jobs/job-9/stream**", (socket) => {
    finish = () => {
      upgraded = true
      socket.send(
        JSON.stringify({ type: "job", data: job({ status: "succeeded" }), ts: Date.now() }),
      )
    }
  })
  await page.goto("/packages/nginx")
  const line = identity(page)
  await line.getByRole("button", { name: "Update", exact: true }).click()
  await expect
    .poll(() => seen)
    .toEqual([{ path: "/packages/install", body: { packages: ["nginx"] } }])
  // The output opens under the line, the line says what is in flight, and the
  // command cannot be pressed a second time.
  await expect(page.getByText("Installing nginx", { exact: true })).toBeVisible()
  await expect(line).toContainText("Updating…")
  await expect(line.getByRole("button", { name: "Update", exact: true })).toBeDisabled()

  await expect.poll(() => Boolean(finish)).toBe(true)
  finish!()
  await expect(line).not.toContainText("update to")
  await expect(line).toContainText("1.24.0-2ubuntu7.1")
  await expect(line.getByRole("button", { name: "Update", exact: true })).toHaveCount(0)
  expect(seen).toHaveLength(1)
})

test("a package that is not installed offers Install, here and in its views", async ({ page }) => {
  await mockHost(page)
  const seen = mutations(page)
  await page.routeWebSocket("**/api/v1/jobs/*/stream**", () => {})
  await page.goto("/packages/htop")
  const line = identity(page)
  await expect(line).toContainText("Not installed")
  await expect(line).toContainText("once installed")
  await expect(page.getByRole("button", { name: "Actions on htop" })).toHaveCount(0)
  await expect(page.locator("[data-slot='wire-flow']")).toContainText("Not installed")
  await page.getByRole("tab", { name: /^Files/ }).click()
  await expect(page.getByText("Not installed yet")).toBeVisible()
  await page.getByRole("tabpanel").getByRole("button", { name: "Install", exact: true }).click()
  await expect
    .poll(() => seen)
    .toEqual([{ path: "/packages/install", body: { packages: ["htop"] } }])
  await expect(line).toContainText("Installing…")
})

test("a run started elsewhere is this package's run", async ({ page }) => {
  await mockHost(page)
  const running = job({ id: "job-4", title: "Installing htop", target: "htop" })
  await page.route("**/api/v1/jobs/", (route) => route.fulfill({ json: [running] }))
  await page.routeWebSocket("**/api/v1/jobs/*/stream**", () => {})
  const seen = mutations(page)
  await page.goto("/packages/htop")
  await expect(identity(page)).toContainText("Installing…")
  await expect(page.getByText("Installing htop", { exact: true })).toBeVisible()
  await expect(identity(page).getByRole("button", { name: "Install", exact: true })).toBeDisabled()
  expect(seen).toEqual([])
})

test("removing a package still asks first, each way", async ({ page }) => {
  await mockHost(page)
  const seen = mutations(page)
  await page.route("**/api/v1/packages/remove", (route) =>
    route.fulfill({ json: job({ kind: "packages.remove", title: "Removing nginx" }) }),
  )
  await page.routeWebSocket("**/api/v1/jobs/*/stream**", () => {})
  await page.goto("/packages/nginx")

  await page.getByRole("button", { name: "Actions on nginx" }).click()
  await page.getByRole("menuitem", { name: "Remove and purge", exact: true }).click()
  const purge = page.getByRole("dialog", {
    name: "Remove nginx and its configuration",
    exact: true,
  })
  await expect(purge).toContainText("and deletes the files it put in /etc")
  await expect(purge.getByRole("textbox")).toHaveCount(0)
  expect(seen).toEqual([])
  await purge.getByRole("button", { name: "Cancel", exact: true }).click()
  expect(seen).toEqual([])

  await page.getByRole("button", { name: "Actions on nginx" }).click()
  await page.getByRole("menuitem", { name: "Remove", exact: true }).click()
  const remove = page.getByRole("dialog", { name: "Remove nginx", exact: true })
  await expect(remove).toContainText("Configuration in /etc is left where it is")
  expect(seen).toEqual([])
  await remove.getByRole("button", { name: "Remove", exact: true }).click()
  await expect
    .poll(() => seen)
    .toEqual([{ path: "/packages/remove", body: { packages: ["nginx"], purge: false } }])
  await expect(identity(page)).toContainText("Removing…")
  await expect(page.getByText("Removing nginx", { exact: true })).toBeVisible()
})

test("a manager that keeps configuration whatever it is asked offers no purge", async ({
  page,
}) => {
  await mockHost(page)
  await page.route(/\/api\/v1\/packages\/$/, (route) =>
    route.fulfill({ json: { ...inventory, manager: "dnf", canPurge: false } }),
  )
  await page.goto("/packages/nginx")
  await page.getByRole("button", { name: "Actions on nginx" }).click()
  await expect(page.getByRole("menuitem", { name: "Remove", exact: true })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Remove and purge" })).toHaveCount(0)
})

test("a protected package says why it stays and offers no removal", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/packages/libc6", (route) =>
    route.fulfill({
      json: {
        name: "libc6",
        installed: true,
        installedVersion: "2.39-0ubuntu8",
        version: "2.39-0ubuntu8",
        essential: true,
        section: "libs",
        size: 13_000_000,
        protected: "The system does not run without it.",
      },
    }),
  )
  await page.goto("/packages/libc6")
  await expect(page.getByText("This one cannot be removed from here")).toBeVisible()
  await expect(page.getByText("The system does not run without it.")).toBeVisible()
  await expect(identity(page)).toContainText("essential")
  await expect(identity(page)).toContainText("a dependency")
  await expect(page.getByRole("button", { name: "Actions on libc6" })).toHaveCount(0)
})

for (const [capabilities, sees] of [
  [["read"], { update: false, remove: false }],
  // Removing is a loss as well as a run: the route asks for both capabilities.
  [["read", "system.admin"], { update: true, remove: false }],
] as const) {
  test(`an account with ${capabilities.join(" and ")} sees only what it can use`, async ({
    page,
  }) => {
    await mockHost(page)
    await page.route("**/api/v1/auth/session", (route) =>
      route.fulfill({ json: { ...user, capabilities } }),
    )
    await page.goto("/packages/nginx")
    const line = identity(page)
    await expect(line).toContainText("update to")
    await expect(line.getByRole("button", { name: "Update", exact: true })).toHaveCount(
      sees.update ? 1 : 0,
    )
    await expect(page.getByRole("button", { name: "Actions on nginx" })).toHaveCount(
      sees.remove ? 1 : 0,
    )
    // Reading is not a privilege: every view is still there.
    await page.getByRole("tab", { name: /^Files/ }).click()
    await expect(page.getByRole("button", { name: "Copy nginx", exact: true })).toBeVisible()
    await page.goto("/packages/htop?tab=files")
    await expect(page.getByText("Not installed yet")).toBeVisible()
    await expect(page.getByRole("button", { name: "Install", exact: true })).toHaveCount(
      sees.update ? 2 : 0,
    )
  })
}

test("a name nothing knows is said plainly, with the way to look for it", async ({ page }) => {
  await mockHost(page)
  await page.route(/\/api\/v1\/packages\/nosuch(\/usage)?$/, (route) =>
    route.fulfill({
      status: 404,
      json: { error: { code: "not_found", message: "no package by that name" } },
    }),
  )
  await page.goto("/packages/nosuch")
  await expect(page.getByText("No package named nosuch")).toBeVisible()
  await expect(page.getByText("no package by that name")).toHaveCount(0)
  await page.getByRole("link", { name: "Search for it" }).click()
  await expect(page).toHaveURL(/\/packages$/)
  await expect(page.getByPlaceholder(/What do you need/)).toHaveValue("nosuch")
})

test("a read that fails is an error with a way to ask again", async ({ page }) => {
  await mockHost(page)
  let fail = true
  await page.route(/\/api\/v1\/packages\/nginx$/, (route) =>
    fail
      ? route.fulfill({
          status: 500,
          json: { error: { code: "internal", message: "Package database is locked" } },
        })
      : route.fallback(),
  )
  await page.goto("/packages/nginx")
  await expect(page.getByText(/Package database is locked/).first()).toBeVisible()
  fail = false
  await page.getByRole("button", { name: /try again|retry/i }).click()
  await expect(identity(page)).toContainText("Installed")
})

for (const width of [1280, 390]) {
  test(`the line and the views are in one place loading and loaded at ${width}`, async ({
    page,
  }) => {
    await mockHost(page)
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route(/\/api\/v1\/packages\/nginx(\/usage)?$/, async (route) => {
      await held
      await route.fallback()
    })
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/packages/nginx")
    const line = identity(page)
    const tabs = page.getByRole("tablist", { name: "Views of nginx" })
    const picture = page.locator("[data-slot='wire-flow']")
    await expect(line).toContainText("Reading the package…")
    await expect(picture).toHaveAttribute("data-loading", "true")
    await risen(page)
    const before = {
      line: await line.boundingBox(),
      tabs: await tabs.boundingBox(),
      picture: await picture.boundingBox(),
    }
    release()
    await expect(line).toContainText("Installed")
    await expect(picture).not.toHaveAttribute("data-loading", "true")
    await risen(page)
    expect((await line.boundingBox())?.y).toBe(before.line?.y)
    expect(await tabs.boundingBox()).toEqual(before.tabs)
    expect((await picture.boundingBox())?.y).toBe(before.picture?.y)
    if (width >= 1024) {
      // Wide, what is under the picture does not move either.
      expect((await picture.boundingBox())?.height).toBe(before.picture?.height)
    }
  })

  test(`what a package needs is one height a row at ${width}`, async ({ page }) => {
    await mockHost(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/packages/nginx?tab=depends")
    const rows = page.getByRole("list", { name: "What nginx depends on" }).getByRole("listitem")
    await expect(rows).toHaveCount(3)
    await risen(page)
    const boxes = await rows.evaluateAll((items) =>
      items.map((item) => item.getBoundingClientRect().toJSON()),
    )
    // Installed with a version asked of it, installed, and not installed with
    // an alternative: the same row.
    expect(new Set(boxes.map((box) => box.height)).size).toBe(1)
    const step = boxes[1].y - boxes[0].y
    expect(boxes[2].y - boxes[1].y).toBe(step)
    for (const box of boxes) expect(box.x + box.width).toBeLessThanOrEqual(width)
  })
}

test("on a phone the package is the one pane, with the way back and the arrows", async ({
  page,
}) => {
  await mockHost(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/packages/nginx")
  await expect(identity(page)).toContainText("Installed")
  await expect(rail(page)).toBeHidden()
  for (const control of [
    back(page),
    page.getByRole("button", { name: "Next package" }),
    identity(page).getByRole("button", { name: "Update", exact: true }),
    page.getByRole("button", { name: "Actions on nginx" }),
  ]) {
    await expect(control).toBeInViewport()
    const box = await control.boundingBox()
    expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  }
  for (const tab of ["Files", "Manual", "Depends", "Overview"]) {
    await page.getByRole("tab", { name: new RegExp(`^${tab}`) }).click()
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
      `${tab} scrolls sideways`,
    ).toBe(false)
  }
  await page.getByRole("button", { name: "Next package" }).click()
  await expect(page).toHaveURL(/\/packages\/postgresql-16$/)
  await expect(identity(page)).toContainText("postgresql-16")
})
