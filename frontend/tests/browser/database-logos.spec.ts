import { expect, test, type Locator, type Page } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A database is drawn as the product that answered.
 *
 * A driver talks to more than its own product — TimescaleDB and CockroachDB
 * answer the `postgres` driver, TiDB the `mysql` one — and the inventory sees
 * servers the dashboard has no driver for. Each has its own mark where a
 * licensed collection draws one, and the database glyph where none does;
 * what it never has is another product's mark with its own name beside it.
 * These are the claims that makes, checked where the mark is drawn: the
 * identity tile a database's home opens on, the strip over its other pages,
 * the rail's panel head and the switcher's rows.
 *
 * The files themselves are checked too: that each is served as an image the
 * page's own origin may load, and that the dark ground shows it — a navy or a
 * black mark dropped in unlifted is a tile that looks empty.
 */

const rail = (page: Page) => page.getByRole("navigation", { name: "Sidebar" })
const strip = (page: Page) => page.locator("[data-slot=database-strip]")
const identity = (page: Page) => page.locator("[data-slot=host-identity]")
const logo = (scope: Page | Locator, file: string) => scope.locator(`img[src="/logos/${file}"]`)
const anyLogo = (scope: Page | Locator) => scope.locator("img[src^='/logos/']")

async function drawn(image: Locator) {
  await expect(image).toBeVisible()
  await expect
    .poll(() => image.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0))
    .toBe(true)
  return Math.round((await image.boundingBox())?.width ?? 0)
}

type Case = {
  flavor: string
  label: string
  /** The fixture's connection that answers as it, and the driver it is behind. */
  id: number
  driver: string
  /** The driver's own product: the mark that must not be drawn for this one. */
  driverFile: string
}

const mock = (c: Case): DatabaseMock => ({
  // The fixture has no SQL Server connection: the first one is made into one.
  rows: { [c.id]: { driver: c.driver } },
  summaries: { [c.id]: { flavor: c.flavor, flavorLabel: c.label, version: `${c.label} 1.0` } },
})

/** Flavours a licensed collection draws, and the file each is drawn with. */
const DRAWN: (Case & { file: string })[] = [
  {
    flavor: "timescaledb",
    label: "TimescaleDB",
    id: 1,
    driver: "postgres",
    driverFile: "postgresql.svg",
    file: "timescaledb.svg",
  },
  {
    flavor: "cockroachdb",
    label: "CockroachDB",
    id: 1,
    driver: "postgres",
    driverFile: "postgresql.svg",
    file: "cockroachdb.svg",
  },
  {
    flavor: "yugabytedb",
    label: "YugabyteDB",
    id: 1,
    driver: "postgres",
    driverFile: "postgresql.svg",
    file: "yugabytedb.svg",
  },
  {
    flavor: "tidb",
    label: "TiDB",
    id: 2,
    driver: "mysql",
    driverFile: "mysql.svg",
    file: "tidb.svg",
  },
  {
    flavor: "ferretdb",
    label: "FerretDB",
    id: 5,
    driver: "mongodb",
    driverFile: "mongodb.svg",
    file: "ferretdb.svg",
  },
]

/** Flavours no collection draws: the database glyph, never the driver's product. */
const UNDRAWN: Case[] = [
  { flavor: "percona", label: "Percona Server", id: 2, driver: "mysql", driverFile: "mysql.svg" },
  { flavor: "keydb", label: "KeyDB", id: 4, driver: "redis", driverFile: "redis.svg" },
  { flavor: "dragonfly", label: "Dragonfly", id: 4, driver: "redis", driverFile: "redis.svg" },
]

for (const c of DRAWN) {
  test(`${c.label} is drawn as itself at every size`, async ({ page }) => {
    await mockDatabases(page, mock(c))

    // Home: the identity tile, and the rail's panel head beside the name.
    await page.goto(`/databases/${c.id}`)
    expect(await drawn(logo(identity(page), c.file))).toBe(28)
    expect(await drawn(logo(rail(page), c.file))).toBe(16)
    await expect(identity(page).getByText(c.label, { exact: false }).first()).toBeVisible()

    // Any other page: the strip's tile.
    await page.goto(`/databases/${c.id}/settings`)
    expect(await drawn(logo(strip(page), c.file))).toBe(18)
    await expect(strip(page)).toContainText(c.label)

    // And nowhere as the product its driver is named after.
    await expect(logo(page, c.driverFile)).toHaveCount(0)
  })
}

for (const c of UNDRAWN) {
  test(`${c.label} keeps the database glyph and is never drawn as its driver`, async ({ page }) => {
    await mockDatabases(page, mock(c))

    await page.goto(`/databases/${c.id}`)
    await expect(identity(page).getByText(c.label, { exact: false }).first()).toBeVisible()
    await expect(anyLogo(identity(page))).toHaveCount(0)
    // The tile is still there, with a glyph in it, so the name starts where
    // every other database's does.
    await expect(identity(page).locator("span[aria-hidden='true'] > svg").first()).toBeVisible()

    await page.goto(`/databases/${c.id}/settings`)
    await expect(strip(page)).toContainText(c.label)
    await expect(anyLogo(strip(page))).toHaveCount(0)
    await expect(anyLogo(rail(page))).toHaveCount(0)
    await expect(logo(page, c.driverFile)).toHaveCount(0)
  })
}

test("SQL Edge, which no collection draws, is the SQL Server engine under its own name", async ({
  page,
}) => {
  const edge: Case = {
    flavor: "azure-sql-edge",
    label: "Azure SQL Edge",
    id: 1,
    driver: "sqlserver",
    driverFile: "sqlserver.svg",
  }
  await mockDatabases(page, mock(edge))

  await page.goto("/databases/1")
  expect(await drawn(logo(identity(page), "sqlserver.svg"))).toBe(28)
  await expect(identity(page).getByText("Azure SQL Edge", { exact: false }).first()).toBeVisible()

  await page.goto("/databases/1/settings")
  expect(await drawn(logo(strip(page), "sqlserver.svg"))).toBe(18)
  await expect(strip(page)).toContainText("Azure SQL Edge")
})

test("the switcher draws every database as what answered", async ({ page }) => {
  await mockDatabases(page, {
    fleet: {
      1: { flavor: "timescaledb", flavorLabel: "TimescaleDB" },
      2: { flavor: "tidb", flavorLabel: "TiDB" },
      4: { flavor: "keydb", flavorLabel: "KeyDB" },
      5: { flavor: "ferretdb", flavorLabel: "FerretDB" },
    },
    summaries: { 1: { flavor: "timescaledb", flavorLabel: "TimescaleDB" } },
  })
  await page.goto("/databases/1/performance")
  await page.getByRole("button", { name: /^Database: shop/ }).click()

  const option = (name: string) => page.getByRole("option", { name: new RegExp(`^${name}\\b`) })
  await drawn(logo(option("shop"), "timescaledb.svg"))
  await drawn(logo(option("blog"), "tidb.svg"))
  await drawn(logo(option("app"), "ferretdb.svg"))
  // KeyDB has no mark of its own, and is not Redis.
  await expect(option("cache")).toBeVisible()
  await expect(anyLogo(option("cache"))).toHaveCount(0)
  for (const driver of ["postgresql.svg", "mysql.svg", "mongodb.svg", "redis.svg"]) {
    await expect(logo(page.getByRole("listbox"), driver)).toHaveCount(0)
  }
})

/**
 * The files this section's engines brought into the bundle. A flavour's or an
 * engine's id is its file's name, as the registry looks it up.
 */
const ENGINE_FILES = [
  "timescaledb",
  "cockroachdb",
  "yugabytedb",
  "tidb",
  "ferretdb",
  "memcached",
  "elasticsearch",
  "opensearch",
  "etcd",
  "cassandra",
  "scylladb",
  "neo4j",
  "couchdb",
  "duckdb",
  "nats",
  "kafka",
].map((id) => `${id}.svg`)

test("every engine's mark is an image this origin serves, with nothing in it but a drawing", async ({
  page,
}) => {
  for (const file of ENGINE_FILES) {
    const response = await page.request.get(`/logos/${file}`)
    expect(response.status(), file).toBe(200)
    expect(response.headers()["content-type"], file).toContain("image/svg+xml")
    const body = await response.text()
    expect(body.startsWith("<svg"), `${file} opens on its svg element`).toBe(true)
    // An image is fetched under `img-src 'self'`: a script could not run and a
    // remote reference could not load, so either is a mark drawn in part.
    expect(/<script|<foreignObject|<image|\son[a-z]+=|href="(?!#)/i.test(body), file).toBe(false)
  }
})

/**
 * The share of a square of the page's ground that a mark covers with a colour
 * standing 3:1 or more against it, at the three sizes a mark is drawn: bare
 * in a line (14px), on a row's tile (18px) and on the identity tile (28px).
 *
 * MySQL's dolphin, a thin line, covers six per cent of the smallest; a mark
 * left in its navy or its black covers none, which is the failure this is
 * for. The floor is half the thinnest mark here (ScyllaDB's outline).
 */
test("the dark ground shows every engine's mark at the sizes it is drawn", async ({ page }) => {
  await mockDatabases(page)
  await page.goto("/databases")
  await expect(page.locator("[data-slot=page]").first()).toBeVisible()

  const shares = await page.evaluate(async (files) => {
    const probe = document.createElement("span")
    probe.style.color = "var(--background)"
    document.body.append(probe)
    const ground = getComputedStyle(probe).color
    probe.remove()

    const canvas = document.createElement("canvas")
    const context = canvas.getContext("2d", { willReadFrequently: true })
    if (!context) throw new Error("no 2d context")
    const channel = (c: number) =>
      c / 255 <= 0.04045 ? c / 255 / 12.92 : ((c / 255 + 0.055) / 1.055) ** 2.4
    const luminance = (r: number, g: number, b: number) =>
      0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)

    const out: Record<string, number[]> = {}
    for (const file of files) {
      const image = new Image()
      image.src = `/logos/${file}`
      await image.decode()
      out[file] = [14, 18, 28].map((size) => {
        canvas.width = canvas.height = size
        context.fillStyle = ground
        context.fillRect(0, 0, size, size)
        const [r, g, b] = context.getImageData(0, 0, 1, 1).data
        const base = luminance(r, g, b)
        // `object-contain`, as the tile draws it.
        const scale = Math.min(size / image.naturalWidth, size / image.naturalHeight)
        const width = image.naturalWidth * scale
        const height = image.naturalHeight * scale
        context.drawImage(image, (size - width) / 2, (size - height) / 2, width, height)
        const pixels = context.getImageData(0, 0, size, size).data
        let shown = 0
        for (let i = 0; i < pixels.length; i += 4) {
          const l = luminance(pixels[i], pixels[i + 1], pixels[i + 2])
          if ((Math.max(l, base) + 0.05) / (Math.min(l, base) + 0.05) >= 3) shown++
        }
        return shown / (size * size)
      })
    }
    return out
  }, ENGINE_FILES)

  for (const [file, sizes] of Object.entries(shares)) {
    for (const [index, size] of [14, 18, 28].entries()) {
      expect(sizes[index], `${file} at ${size}px`).toBeGreaterThanOrEqual(0.028)
    }
  }
})

/**
 * The design system's structural rules, on the pages a mark is drawn on. The
 * helpers are `design-system.spec.ts`'s: that file walks these addresses with
 * the fixture's own engines, and this walks them with a flavour that has a
 * mark and one that has none, where the tile's content differs.
 */

/** Every visible button with no text of its own and no name from anywhere else. */
function unnamedControls(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
      if (el.offsetParent === null && el.getAttribute("aria-hidden") !== "true") continue
      const text = (el.textContent ?? "").trim()
      if (text.length > 0) continue
      const named =
        el.getAttribute("aria-label") ||
        el.getAttribute("aria-labelledby") ||
        el.querySelector(".sr-only")
      if (!named) bad.push(el.outerHTML.slice(0, 160))
    }
    return bad
  })
}

/** Fully rounded, filled labels; a person's avatar is the one identity exception. */
function filledPills(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
      if (el.dataset.slot === "user-avatar") continue
      const s = getComputedStyle(el)
      const r = parseFloat(s.borderTopLeftRadius)
      const h = el.getBoundingClientRect().height
      if (!h || h > 32 || r < h / 2) continue
      const filled = s.backgroundColor !== "rgba(0, 0, 0, 0)" && s.backgroundColor !== "transparent"
      const text = (el.textContent ?? "").trim()
      if (filled && text.length > 0) bad.push(el.outerHTML.slice(0, 140))
    }
    return bad
  })
}

/** Text a centred row sets off its own centre line. */
function offCentreText(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const item of document.querySelectorAll<HTMLElement>("body *")) {
      const row = item.parentElement
      if (!row) continue
      const rowStyle = getComputedStyle(row)
      if (!rowStyle.display.endsWith("flex") || !rowStyle.flexDirection.startsWith("row")) continue
      const style = getComputedStyle(item)
      const align = ["auto", "normal"].includes(style.alignSelf)
        ? rowStyle.alignItems
        : style.alignSelf
      if (align !== "center" || style.display !== "block" || style.position === "absolute") continue
      const box = item.getBoundingClientRect()
      if (box.height < 2 || box.width < 2) continue
      const blocks = [...item.children].some((child) => {
        const display = getComputedStyle(child).display
        return display !== "none" && !display.startsWith("inline")
      })
      if (blocks) continue
      const range = document.createRange()
      range.selectNodeContents(item)
      const ink = [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0)
      if (ink.length === 0) continue
      const top = Math.min(...ink.map((r) => r.top))
      const bottom = Math.max(...ink.map((r) => r.bottom))
      const offset = (top + bottom) / 2 - (box.top + box.bottom) / 2
      if (Math.abs(offset) >= 1.5) {
        bad.push(`${offset.toFixed(1)}px ${item.outerHTML.slice(0, 140)}`)
      }
    }
    return bad
  })
}

/** The registers the page elements declare, and how many flow panels are drawn. */
async function registers(page: Page) {
  await page.waitForSelector("[data-slot='page']", { timeout: 5_000 }).catch(() => undefined)
  return page.evaluate(() => {
    const pages = [...document.querySelectorAll<HTMLElement>("[data-slot='page']")]
    return {
      registers: pages.map((el) => el.dataset.register ?? "(unset)"),
      panels: document.querySelectorAll("[data-slot='flow-panel']").length,
    }
  })
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  for (const c of [DRAWN[0], UNDRAWN[1]]) {
    test(`the pages of a ${c.label} database keep the rules${label}`, async ({ page }) => {
      await page.setViewportSize(viewport)
      await mockDatabases(page, mock(c))

      for (const path of [`/databases/${c.id}`, `/databases/${c.id}/settings`, "/databases"]) {
        await page.goto(path)
        await page.waitForLoadState("networkidle")
        await expect(page.locator("[data-slot=page]").first()).toBeVisible({ timeout: 15_000 })

        expect(await unnamedControls(page), `unlabelled icon-only controls on ${path}`).toEqual([])
        expect(await offCentreText(page), `text off its row's centre line on ${path}`).toEqual([])
        expect(await filledPills(page), `fully rounded filled chips on ${path}`).toEqual([])
        const seen = await registers(page)
        expect(seen.registers, `${path} declares one reading page`).toEqual(["reading"])
        expect(seen.panels, `a reading page drew a flow panel on ${path}`).toBe(0)
        // A mark is decoration beside a name that says the same thing: it is
        // hidden from the accessibility tree, or it would be read twice.
        const announced = await page.evaluate(() =>
          [...document.querySelectorAll<HTMLImageElement>("img[src^='/logos/']")]
            .filter((img) => img.alt !== "" && !img.closest("[aria-hidden='true']"))
            .map((img) => img.outerHTML.slice(0, 120)),
        )
        expect(announced, `a mark read aloud beside its name on ${path}`).toEqual([])
        const sideways = await page.evaluate(() =>
          [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
            .map((el) => el.parentElement ?? el)
            .some((el) => el.scrollWidth > el.clientWidth + 1),
        )
        expect(sideways, `${path} scrolls sideways`).toBe(false)
      }
    })
  }
}
