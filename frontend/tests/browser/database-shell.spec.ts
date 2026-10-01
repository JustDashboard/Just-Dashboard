import { expect, test, type Page } from "@playwright/test"
import { hold, mockDatabases } from "./database-fixture"

/**
 * The Databases section's shell: the frame every page of a database is drawn
 * in, checked without any of the pages.
 *
 * A database is an address (`/databases/<id>/…`), the rail drills into it a
 * level below Databases with the engine's own pages in the engine's own
 * words, and a strip over each page says which database this is. These are
 * the claims that makes — the ones the areas built inside it rely on and
 * none of them can check for itself: a page is never mounted on an engine
 * that lacks it, a stale or mistyped address never opens another database,
 * the reader's table survives a trip through a page that cannot hold it, and
 * nothing on the strip is drawn over anything else at any width.
 */

const rail = (page: Page) => page.getByRole("navigation", { name: "Sidebar" })
const strip = (page: Page) => page.locator("[data-slot=database-strip]")
const where = (page: Page) => new URL(page.url()).pathname + new URL(page.url()).search
const railPages = (page: Page) =>
  rail(page)
    .getByRole("link")
    .evaluateAll((links) => links.map((link) => link.textContent?.trim()))

test("a database is a level below Databases, with its engine's pages in its engine's words", async ({
  page,
}) => {
  await mockDatabases(page)
  await page.goto("/databases/1/data")

  await expect(rail(page).getByText("shop", { exact: true })).toBeVisible()
  expect(await railPages(page)).toEqual([
    "Home",
    "Data",
    "Query",
    "Search",
    "Schema",
    "Diagram",
    "Generate",
    "Performance",
    "Advisor",
    "Logs",
    "Access",
    "Backups",
    "Settings",
  ])
  for (const group of ["Work", "Schema", "Insights", "Operate"]) {
    await expect(rail(page).getByText(group, { exact: true }).first()).toBeVisible()
  }
  await expect(rail(page).getByRole("link", { name: "Data", exact: true })).toHaveAttribute(
    "data-active",
    "true",
  )
  // Home is `/databases/1`, a prefix of every other page: current on Home alone.
  await expect(rail(page).getByRole("link", { name: "Home", exact: true })).not.toHaveAttribute(
    "data-active",
    "true",
  )
  // No strip of route tabs came back with the rebuild.
  await expect(page.getByRole("navigation", { name: "Database section" })).toHaveCount(0)

  await page.getByRole("button", { name: "Back to Databases" }).click()
  await expect(page).toHaveURL(/\/databases\/1\/data$/)
  expect(await railPages(page)).toEqual(["Control center", "Map", "Add a database"])
})

test("Redis and MongoDB have only their own pages, under their own names", async ({ page }) => {
  await mockDatabases(page)

  await page.goto("/databases/4")
  await expect(rail(page).getByText("cache", { exact: true })).toBeVisible()
  expect(await railPages(page)).toEqual([
    "Home",
    "Keys",
    "Console",
    "Performance",
    "Logs",
    "Access",
    "Backups",
    "Settings",
  ])

  await page.goto("/databases/5")
  await expect(rail(page).getByText("app", { exact: true })).toBeVisible()
  expect(await railPages(page)).toEqual([
    "Home",
    "Documents",
    "Aggregations",
    "Schema",
    "Performance",
    "Logs",
    "Access",
    "Backups",
    "Settings",
  ])
})

test("a page the engine lacks says so and asks the server nothing", async ({ page }) => {
  const { asked } = await mockDatabases(page)
  await page.goto("/databases/4/schema")

  await expect(page.getByText("Redis has no schema")).toBeVisible()
  await expect(page.getByRole("link", { name: "Open Keys" })).toHaveAttribute(
    "href",
    "/databases/4/data",
  )
  await page.waitForLoadState("networkidle")
  // The shell's own reads and nothing of a schema browser's.
  expect(asked.filter((request) => request.includes("/databases/4/"))).toEqual([])
})

test("the pages wait for what the server is, and are never mounted on its driver's product", async ({
  page,
}) => {
  // A MariaDB server is dialled with the `mysql` driver; only its summary
  // says what answered, and this one has no dumps.
  const summary = hold()
  const { asked } = await mockDatabases(page, {
    summaryHeld: summary.until,
    summaries: { 2: { capabilities: { dump: false, roles: false } } },
  })
  await page.goto("/databases/2/backups")

  // Until the summary answers there is no strip and no page of MySQL's.
  await expect.poll(() => asked).toContain("GET /databases/2")
  await expect(page.locator("[data-slot=page]").first()).toBeVisible()
  await expect(strip(page)).toHaveCount(0)
  await expect(rail(page).getByRole("link", { name: "Backups", exact: true })).toHaveCount(0)

  summary.release()
  await expect(page.getByText("MariaDB has no dumps")).toBeVisible()
  await expect(strip(page)).toContainText("MariaDB 11.8.9")
  expect(await railPages(page)).not.toContain("Backups")
  expect(await railPages(page)).not.toContain("Access")
  expect(asked.filter((request) => request.includes("/databases/2/"))).toEqual([])

  // The next arrival in this tab draws the rail as the engine it learned.
  await page.goto("/databases/2/logs")
  await expect(rail(page).getByText("blog", { exact: true })).toBeVisible()
  expect(await railPages(page)).not.toContain("Backups")
})

test("a backend with no summary route still opens a database, on a ping", async ({ page }) => {
  await mockDatabases(page, { summaries: { 1: null } })
  await page.goto("/databases/1/performance")

  await expect(strip(page)).toContainText("shop")
  await expect(strip(page).locator("[data-slot=database-status]")).toHaveText("connected")
})

test("an address that names no database says so and opens no other", async ({ page }) => {
  await mockDatabases(page)

  for (const path of ["/databases/999/data", "/databases/007/data", "/databases/constructor"]) {
    await page.goto(path)
    await expect(page.getByRole("heading", { name: "Database not found" })).toBeVisible()
    await expect(page.getByRole("link", { name: "All databases" })).toHaveAttribute(
      "href",
      "/databases",
    )
    await expect(strip(page)).toHaveCount(0)
    // The rail stays on the section: no pages of a database that is not there.
    expect(await railPages(page), path).toEqual(["Control center", "Map", "Add a database"])
    expect(where(page)).toBe(path)
  }
})

test("an address under a database that is none of its pages stays inside it", async ({ page }) => {
  await mockDatabases(page)
  await page.goto("/databases/1/no-such-page")

  await expect(page.getByText("Page not found").last()).toBeVisible()
  await expect(strip(page)).toContainText("shop")
  await expect(page.getByRole("link", { name: "Open Home" })).toHaveAttribute(
    "href",
    "/databases/1",
  )
  await expect(rail(page).getByRole("link", { name: "Settings", exact: true })).toBeVisible()
})

test("addresses from before the connection moved into the path land on the page that took their work", async ({
  page,
}) => {
  await mockDatabases(page)

  await page.goto("/databases")
  await page.goto("/databases/browse?conn=1&schema=public&table=orders")
  await expect(page).toHaveURL(/\/databases\/1\/data\?schema=public&table=orders$/)
  await expect(strip(page)).toContainText("shop")
  // Replaced, not pushed: Back does not return to the address that redirects.
  await page.goBack()
  await expect(page).toHaveURL(/\/databases$/)

  await page.goto("/databases/logs?conn=1&view=queries")
  await expect(page).toHaveURL(/\/databases\/1\/logs\?view=queries$/)
  await page.goto("/databases/connection?conn=4")
  await expect(page).toHaveURL(/\/databases\/4\/settings$/)
  await page.goto("/databases/topology")
  await expect(page).toHaveURL(/\/databases\/map$/)
  // An old page with no connection named opens the list, not a guess.
  await page.goto("/databases/overview")
  await expect(page).toHaveURL(/\/databases$/)
})

test("the reader's table survives a trip through a page that cannot hold it", async ({ page }) => {
  await mockDatabases(page)
  await page.goto("/databases/1/data?schema=public&table=orders")
  const link = (name: string) => rail(page).getByRole("link", { name, exact: true })

  // Query can hold the schema; Settings can hold neither.
  await expect(link("Query")).toHaveAttribute("href", "/databases/1/query?schema=public")
  await expect(link("Settings")).toHaveAttribute("href", "/databases/1/settings")
  await link("Settings").click()
  await expect(page).toHaveURL(/\/databases\/1\/settings$/)
  await expect(link("Data")).toHaveAttribute("href", "/databases/1/data?schema=public&table=orders")
  await link("Data").click()
  await expect(page).toHaveURL(/\/databases\/1\/data\?schema=public&table=orders$/)

  // A bare link — the palette's, a bookmark of the page — is completed.
  await page.goto("/databases/1/schema")
  await expect(page).toHaveURL(/\/databases\/1\/schema\?schema=public&table=orders$/)

  // A table does not follow the reader into another schema.
  await page.goto("/databases/1/query?schema=sales")
  await expect(link("Data")).toHaveAttribute("href", "/databases/1/data?schema=sales")

  // And one database's place is not another's.
  await page.goto("/databases/2/data")
  await expect(strip(page)).toContainText("blog")
  expect(where(page)).toBe("/databases/2/data")
})

test("two changes to the address made by one press both land", async ({ page }) => {
  // "Server log around this" changes the source and the view together. Each
  // write used to start from the address as rendered, so the second undid
  // the first; the context's writer makes one change of the two.
  const file = "/var/log/postgresql/postgresql-17-main.log"
  const journal = "journal:postgresql@17-main.service"
  const base = { kind: "journal", lens: "postgres", rotated: false }
  await mockDatabases(page, {
    logSources: {
      sources: [
        {
          ...base,
          id: `file:${file}`,
          label: "postgresql-17-main.log",
          kind: "app",
          primary: true,
        },
        { ...base, id: journal, label: "postgresql@17-main.service" },
      ],
    },
    queryLog: {
      supported: true,
      source: "log",
      truncated: false,
      entries: [
        {
          at: "2026-09-27T10:01:03.221Z",
          durationMs: 1843.2,
          query: "SELECT o.id, o.total FROM orders o JOIN customers c ON c.id = o.customer_id",
          fp: "3f2a9c1d0b7e",
          user: "postgres",
          db: "shop",
        },
      ],
    },
  })
  await page.goto(`/databases/1/logs?source=${encodeURIComponent(journal)}&view=queries`)

  await page.getByRole("button", { name: /SELECT o\.id, o\.total FROM orders/ }).click()
  await page.getByRole("button", { name: "Server log around this" }).click()

  await expect(page).toHaveURL(/source=file%3A%2Fvar%2Flog%2Fpostgresql/)
  await expect(page).toHaveURL(/view=search/)
  await expect(page.getByRole("combobox", { name: "Server log" })).toContainText(
    "postgresql-17-main.log",
  )
})

test("the switcher groups by engine, reads a stopped server as stopped and keeps the place on the one already open", async ({
  page,
}) => {
  await mockDatabases(page, {
    fleet: {
      2: { ok: false, state: "stopped", error: undefined },
      4: { ok: false, state: "unreachable", error: "dial tcp 127.0.0.1:6379: connection refused" },
    },
  })
  await page.goto("/databases/1/data?schema=public&table=orders")
  await page.getByRole("button", { name: /^Database: shop/ }).click()

  const row = (name: string) => page.getByRole("option", { name: new RegExp(name) })
  await expect(row("blog").getByRole("img", { name: "stopped" })).toBeVisible()
  // Somebody stopped it: that is a fact, not a failure, and takes no red.
  await expect(row("blog").getByRole("img").locator("span")).toHaveClass(/bg-muted-foreground/)
  await expect(row("cache").getByRole("img", { name: "unreachable" })).toBeVisible()
  await expect(row("cache").getByRole("img").locator("span")).toHaveClass(/bg-destructive/)
  await expect(row("shop").getByRole("img", { name: "connected" })).toBeVisible()
  await expect(page.locator("[cmdk-group-heading]")).toHaveText([
    "MariaDB",
    "MongoDB",
    "PostgreSQL",
    "Redis",
    "SQLite",
  ])
  await expect(page.getByRole("link", { name: "All databases" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Add a database" })).toBeVisible()

  // Choosing where you already are changes nothing, the table included.
  await row("shop").click()
  await expect(page.getByRole("option")).toHaveCount(0)
  expect(where(page)).toBe("/databases/1/data?schema=public&table=orders")

  // Another database opens on the same page where its engine has one, and
  // the keyboard's place goes with it.
  await page.getByRole("button", { name: /^Database: shop/ }).click()
  await page.keyboard.type("blog")
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/\/databases\/2\/data$/)
  await expect(page.getByRole("button", { name: /^Database: blog/ })).toBeFocused()

  // The control center reads the same server the same way.
  await page.goto("/databases")
  const card = page.locator("[data-slot=choice-row]", { hasText: "blog" })
  await expect(card.locator("[data-slot=database-status]")).toHaveText("stopped")
})

test("a password asked for is not shown once the popover it was asked in has closed", async ({
  page,
}) => {
  const read = hold()
  const { asked } = await mockDatabases(page, { urlHeld: read.until })
  await page.goto("/databases/1/performance")
  const reads = () => asked.filter((request) => request === "GET /databases/1/url").length
  const string = page.locator("[data-slot=connection-string]")
  const show = page.getByRole("button", { name: "Show the password" })

  await page.getByRole("button", { name: "Connect" }).click()
  await expect(string).toContainText("••••••")
  // Drawing the masked string reads nothing audited.
  expect(reads()).toBe(0)

  // Asked for, and closed while the answer is still on its way.
  await show.click()
  await expect.poll(reads).toBe(1)
  await page.keyboard.press("Escape")
  await expect(string).toHaveCount(0)
  const answered = page.waitForResponse((response) => response.url().includes("/databases/1/url"))
  read.release()
  await answered

  await page.getByRole("button", { name: "Connect" }).click()
  // The control is held while a read is out, so once it can be pressed again
  // the late answer has been dealt with — and it is still the one that shows.
  await expect(show).toBeEnabled()
  await expect(string).toContainText("••••••")
  await expect(string).not.toContainText("s3cret")
  await expect(page.getByRole("button", { name: "Hide the password" })).toHaveCount(0)
  expect(reads()).toBe(1)

  // Asked for and waited for, it is shown — and quoted so a shell reads none of it.
  await show.click()
  await expect(string).toContainText("s3cret")
  await page.getByRole("button", { name: "psql", exact: true }).click()
  await expect(string).toHaveText(
    "psql 'postgres://app:s3cret@127.0.0.1:5432/shop_main?sslmode=disable'",
  )
})

test("a role that may not read the password is not offered it", async ({ page }) => {
  const { asked } = await mockDatabases(page, { viewer: true })
  await page.goto("/databases/1/performance")
  await page.getByRole("button", { name: "Connect" }).click()

  await expect(page.locator("[data-slot=connection-string]")).toContainText("••••••")
  await expect(page.getByRole("button", { name: "Show the password" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Copy" })).toHaveCount(0)
  expect(asked.filter((request) => /\/(url|access)$/.test(request))).toEqual([])
})

/** Every pair of the strip's marks and controls that is drawn over one another. */
function stripOverlaps(page: Page) {
  return page.evaluate(() => {
    const row = document.querySelector("[data-slot=database-strip]")?.firstElementChild
    if (!row) return ["no strip"]
    const parts = [
      ...row.querySelectorAll<HTMLElement>(
        "button, [data-slot=tag], [data-slot=database-status], [data-slot=product-logo]",
      ),
    ].filter((el) => el.getBoundingClientRect().width > 0)
    const bad: string[] = []
    const name = (el: HTMLElement) => (el.getAttribute("aria-label") ?? el.textContent ?? "").trim()
    for (const [index, a] of parts.entries()) {
      const one = a.getBoundingClientRect()
      if (one.right > window.innerWidth + 1) bad.push(`${name(a)} runs past the window`)
      for (const b of parts.slice(index + 1)) {
        if (a.contains(b) || b.contains(a)) continue
        const other = b.getBoundingClientRect()
        const across = Math.min(one.right, other.right) - Math.max(one.left, other.left)
        const down = Math.min(one.bottom, other.bottom) - Math.max(one.top, other.top)
        if (across > 1 && down > 1) bad.push(`${name(a)} over ${name(b)}`)
      }
    }
    return bad
  })
}

for (const width of [390, 800, 1024, 1280]) {
  test(`nothing on the strip is drawn over anything else at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 })
    // The longest of everything at once: a name that has to give way, both
    // labels, and the longest word a status has.
    const labels = {
      name: "customer-orders-production-eu-west-1",
      environment: "production",
      readOnly: true,
    }
    await mockDatabases(page, {
      rows: {
        1: { ...labels, broken: true, brokenReason: "the sealed DSN no longer opens" },
      },
    })
    await page.goto("/databases/1/performance")

    await expect(strip(page).locator("[data-slot=database-status]")).toHaveText("cannot be opened")
    await expect(strip(page).getByText("production", { exact: true })).toBeVisible()
    await expect(strip(page).getByText("protected", { exact: true })).toBeVisible()
    expect(await stripOverlaps(page)).toEqual([])

    // The page under it is not pushed sideways either. The shell's own
    // scroll container hides an overflow, so it is the one measured.
    const fits = await strip(page).evaluate((el) => {
      const region = el.parentElement!
      return region.scrollWidth <= region.clientWidth
    })
    expect(fits, "the strip is wider than the page").toBe(true)
  })
}

test("an environment is printed as the operator typed it", async ({ page }) => {
  await mockDatabases(page, { rows: { 1: { environment: "eu-west qa" } } })
  await page.goto("/databases/1/performance")

  const tag = strip(page).locator("[data-slot=tag]").first()
  await expect(tag).toHaveText("eu-west qa")
  await expect(tag).toHaveCSS("text-transform", "none")
})

test("a workbench is exactly the window that the strip leaves", async ({ page }) => {
  await mockDatabases(page, { rows: { 1: { environment: "production", readOnly: true } } })

  for (const [width, height] of [
    [1280, 800],
    [390, 844],
  ]) {
    await page.setViewportSize({ width, height })
    for (const path of ["/databases/1/data", "/databases/1/query", "/databases/1/schema"]) {
      await page.goto(path)
      await expect(strip(page)).toBeVisible()
      const surface = page.locator("[data-slot=page]").first()
      await expect(surface).toBeVisible()
      const bottom = await surface.evaluate((el) => Math.round(el.getBoundingClientRect().bottom))
      expect(bottom, `${path} at ${width}`).toBe(height)
    }
  }
})

test("the home's switcher shows its focus ring whole", async ({ page }) => {
  await mockDatabases(page)
  await page.goto("/databases/1")
  const name = page.getByRole("button", { name: /^Database: shop/ })
  await expect(name).toBeVisible()
  await name.focus()

  // The title's box clips what leaves it, so the ring is drawn inside.
  await expect(name).toHaveCSS("outline-offset", "-2px")
})

test("the palette opens any database, and the pages of the one being looked at", async ({
  page,
}) => {
  await mockDatabases(page)
  await page.goto("/databases/4/data")
  await expect(strip(page)).toContainText("cache")
  await page.keyboard.press("ControlOrMeta+k")

  const palette = page.getByRole("dialog")
  await palette.getByRole("combobox").fill("cache")
  await expect(palette.getByRole("option", { name: "cache Keys" })).toBeVisible()
  await expect(palette.getByRole("option", { name: "cache Console" })).toBeVisible()
  await expect(palette.getByRole("option", { name: /Schema/ })).toHaveCount(0)

  await palette.getByRole("combobox").fill("open shop")
  await palette.getByRole("option", { name: "Open shop" }).click()
  await expect(page).toHaveURL(/\/databases\/1$/)
})

test("what the tab remembers of the databases is the tab's, and nothing is left in the browser", async ({
  page,
}) => {
  await mockDatabases(page)
  await page.goto("/databases/1/data")
  await expect(strip(page)).toContainText("shop")

  const stores = await page.evaluate(() => ({
    local: Object.keys(localStorage).filter((key) => key.includes("databases")),
    session: Object.keys(sessionStorage).filter((key) => key.includes("databases.known")),
  }))
  // Connection names are the server's data: they must go with the session.
  expect(stores.local).toEqual([])
  expect(stores.session).toHaveLength(1)
})
