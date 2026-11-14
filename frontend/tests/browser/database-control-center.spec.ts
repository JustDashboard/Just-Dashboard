import { expect, test, type Page } from "@playwright/test"
import { hold } from "./database-fixture"
import { dockerInventory, dockerlessInventory, mockFleet, refusal } from "./database-fleet-fixture"
import { risen } from "./risen"

/**
 * The section-wide pages of Databases: the control center, the map and Add a
 * database, against a mocked machine that runs Docker.
 *
 * Each test is a claim one of the pages makes and a way it has been, or
 * could be, got wrong: a figure that could not be read drawn as a zero, a
 * second concern hidden behind the first, a stopped server offered a Stop, a
 * failed poll taking the fleet away, a new database published to the internet
 * by default, a connection saved without having been dialled.
 */

const where = (page: Page) => new URL(page.url()).pathname + new URL(page.url()).search
const card = (page: Page, name: string) =>
  page
    .locator("[data-card=database]")
    .filter({ has: page.getByRole("link", { name: `Open ${name}` }) })
const foundRow = (page: Page, name: string) =>
  page.locator("[data-slot=choice-row]").filter({ hasText: name }).first()
const scroller = (page: Page) => page.locator("[data-slot=page]").first().locator("xpath=..")
const identity = (page: Page) => page.locator("[data-slot=host-identity]")
/** The menu at the end of the identity line, which holds every command but the first. */
const more = (page: Page) => page.getByRole("button", { name: "More about the databases" })

/** A recorded hour of a SQL server whose open sessions move, as the history route answers it. */
function sqlHour(base: number) {
  const now = Date.now()
  return {
    samples: Array.from({ length: 12 }, (_, index) => ({
      at: now - (12 - index) * 300_000,
      gap: false,
      stats: {
        at: new Date(now - (12 - index) * 300_000).toISOString(),
        supported: true,
        databaseBytes: 23_400_000,
        connections: {
          total: base + (index % 4),
          active: 1,
          idle: 1,
          idleInTransaction: 0,
          waiting: 0,
          max: 100,
        },
        counters: {},
        gauges: {},
      },
    })),
    everySeconds: 30,
    retentionHours: 168,
  }
}

/** The same for a key-value store, which counts clients. */
function redisHour(base: number) {
  const now = Date.now()
  return {
    samples: Array.from({ length: 12 }, (_, index) => ({
      at: now - (12 - index) * 300_000,
      gap: false,
      stats: {
        server: {
          sampledAtMs: now - (12 - index) * 300_000,
          counters: { connected_clients: base + (index % 3) },
        },
      },
    })),
    everySeconds: 30,
    retentionHours: 168,
  }
}

/** A server with many databases, long names and awkward figures among them. */
function manyDatabases(count: number) {
  const engines = [
    ["postgres", "postgres", "tables"],
    ["mysql", "mariadb", "tables"],
    ["redis", "valkey", "keys"],
    ["mongodb", "mongodb", "collections"],
    ["clickhouse", "clickhouse", "tables"],
    ["sqlserver", "sqlserver", "tables"],
  ]
  return Array.from({ length: count }, (_, i) => {
    const [driver, flavor, objectWord] = engines[i % engines.length]
    const down = i % 11 === 0
    return {
      id: 100 + i,
      name: i % 7 === 0 ? `customer-facing-production-replica-eu-west-${i}` : `db-${i}`,
      driver,
      flavor,
      host: "127.0.0.1",
      port: String(5000 + i),
      user: "app",
      database: "main",
      createdAt: new Date().toISOString(),
      environment: i % 3 === 0 ? "production" : i % 3 === 1 ? "staging" : "",
      readOnly: i % 5 === 0,
      notes: "",
      origin: "",
      ok: !down,
      state: down ? "unreachable" : "running",
      error: down ? "dial tcp: connection refused" : undefined,
      latencyMs: 3,
      bytes: 1_000_000 * (i + 1) * 97,
      sizesKnown: true,
      objects: i === 4 ? 123_456_789_012 : 12 * i,
      objectWord,
      sessions: i === 5 ? 1_234_567 : i % 9,
      source: i % 4 === 0 ? "host" : "docker",
      container: i % 4 === 0 ? undefined : `a-rather-long-container-name-for-db-${i}-1`,
      unit: i % 8 === 0 ? "postgresql@16-main.service" : undefined,
      exposure: i % 6 === 0 ? "public" : "local",
      consumers: i % 2,
      versionNumber: "16.4.1-ubuntu0.24.04.1+build7",
      lastBackup: i % 2 ? new Date(Date.now() - 3_600_000 * i).toISOString() : undefined,
    }
  })
}

test.describe("the control center", () => {
  test("reads the fleet as cards in their engine's words, shelved by where they run", async ({
    page,
  }) => {
    await mockFleet(page, { dumps: { 4: null, 5: 60 } })
    await page.goto("/databases")
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
    await expect(card(page, "notes")).toContainText("312.0 KB")
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

    for (const [shelf, names] of [
      ["Containers", ["blog", "cache", "shop"]],
      ["Files", ["notes"]],
      ["Elsewhere", ["app"]],
    ] as const) {
      const list = page.getByRole("list", { name: shelf })
      for (const name of names) {
        await expect(list.getByRole("link", { name: `Open ${name}` })).toBeVisible()
      }
    }

    // A shelf is as wide as its cards: the two short ones stand side by side under their
    // own rules, below the one that fills its row.
    const top = async (shelf: string) =>
      (await page.getByRole("list", { name: shelf }).boundingBox())?.y ?? -1
    expect(await top("Files")).toBe(await top("Elsewhere"))
    expect(await top("Containers")).toBeLessThan(await top("Files"))

    // MariaDB behind the mysql driver is drawn and named as itself.
    await expect(card(page, "blog")).toContainText("MariaDB 11.8.9")
    await expect(card(page, "blog").locator("img").first()).toHaveAttribute("src", /mariadb/)
    // Each reads in its engine's own words: what it holds and who is connected, as words
    // on one line, where three figures stood under three labels.
    await expect(card(page, "cache")).toContainText(/28,600\s*keys/)
    await expect(card(page, "cache")).toContainText(/3\s*clients open/)
    await expect(card(page, "app")).toContainText(/4\s*collections/)
    await expect(card(page, "shop")).toContainText(/4\s*sessions open/)
    await expect(card(page, "shop")).toContainText("23.4 MB stored")
    await expect(page.locator("[data-card=database] .eyebrow")).toHaveCount(0)
    // The dump age is said. The live clock can add seconds while the preceding readings settle.
    await expect(card(page, "shop")).toContainText(/backed up 3h(?: \d+[ms])* ago/)
    await expect(card(page, "cache")).toContainText("never backed up")
    await expect(card(page, "shop")).toContainText("shop-db")

    // The two work pages of each engine, under its own names.
    await expect(card(page, "shop").getByRole("link", { name: "Data of shop" })).toHaveAttribute(
      "href",
      "/databases/1/data",
    )
    await expect(
      card(page, "cache").getByRole("link", { name: "Console of cache" }),
    ).toHaveAttribute("href", "/databases/4/query")
    await expect(
      card(page, "app").getByRole("link", { name: "Aggregations of app" }),
    ).toHaveAttribute("href", "/databases/5/query")

    // The whole card is the way in; a control on it is its own press.
    await card(page, "shop").getByText("23.4 MB stored").click()
    await expect(page).toHaveURL(/\/databases\/1$/)
  })

  for (const show of ["down", "stored", "busy", "unprotected"]) {
    test(`the removed metric filter ${show} cannot narrow the fleet`, async ({ page }) => {
      await mockFleet(page, { fleet: { 2: { ok: false, state: "stopped" } } })
      await page.goto(`/databases?show=${show}`)
      await expect(page.locator("[data-card=database]")).toHaveCount(5)
      await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
      await expect(
        page.getByRole("button", { name: "Show the databases that are not running" }),
      ).toHaveCount(0)
    })
  }

  test("engines and words narrow it too, and nothing left offers the way back", async ({
    page,
  }) => {
    await mockFleet(page)
    await page.goto("/databases")

    await page.getByRole("button", { name: /^MariaDB/ }).click()
    expect(where(page)).toBe("/databases?engine=mariadb")
    await expect(page.locator("[data-card=database]")).toHaveCount(1)
    await page.getByRole("button", { name: /^All engines/ }).click()

    const box = page.getByLabel("Filter databases")
    await box.fill("cache")
    await expect(page.locator("[data-card=database]")).toHaveCount(1)
    expect(where(page)).toBe("/databases?q=cache")
    // A letter typed into the middle of the text stays where it was typed:
    // the field owns its text and the address follows it.
    await box.press("Home")
    await box.press("ArrowRight")
    await page.keyboard.type("XY", { delay: 40 })
    await expect(box).toHaveValue("cXYache")
    // The address a card prints finds its database: the port, and host with port.
    await box.fill("5432")
    await expect(page.locator("[data-card=database]")).toHaveCount(1)
    await expect(card(page, "shop")).toBeVisible()
    // Back to an address with other words puts them in the field.
    await box.fill("")
    await page.goto("/databases?q=blog")
    await expect(page.getByLabel("Filter databases")).toHaveValue("blog")
    await expect(page.locator("[data-card=database]")).toHaveCount(1)
    await page.getByLabel("Filter databases").fill("nothing-like-this")
    await expect(page.getByText("No database matches")).toBeVisible()
    await page
      .locator("[data-slot=empty-state]")
      .getByRole("button", { name: "Show every database" })
      .click()
    await expect(page.locator("[data-card=database]")).toHaveCount(5)
    expect(where(page)).toBe("/databases")
  })

  test("the shelves are the reader's own arrangement, kept and not in the address, and the cards are the only way it is drawn", async ({
    page,
  }) => {
    await mockFleet(page, {
      rows: { 1: { environment: "production" }, 2: { environment: "staging" } },
    })
    await page.goto("/databases")

    await page.getByRole("radio", { name: "Engine" }).click()
    await expect(page.getByRole("list", { name: "MariaDB" })).toBeVisible()
    await page.getByRole("radio", { name: "Environment" }).click()
    await expect(page.getByRole("list", { name: "production" })).toBeVisible()
    await expect(page.getByRole("list", { name: "No environment" })).toBeVisible()
    expect(where(page)).toBe("/databases")

    // The table is gone, and so is the switch that drew it.
    await expect(page.getByRole("group", { name: "Draw as" })).toHaveCount(0)
    await expect(page.getByRole("radio", { name: "Table" })).toHaveCount(0)
    await expect(page.getByRole("table")).toHaveCount(0)

    await page.reload()
    await expect(page.getByRole("list", { name: "production" })).toBeVisible()
    await page.getByRole("radio", { name: "Where it runs" }).click()
  })

  test("the band reads where the bytes are, who is connected and how old each dump is, each database in its card's hue", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      dumps: { 4: null, 5: 200 },
      fleet: { 2: { ok: false, state: "stopped" } },
    })
    server.answer("GET /databases/1/stats/history", () => ({ body: sqlHour(3) }))
    server.answer("GET /databases/4/stats/history", () => ({ body: redisHour(2) }))
    await page.goto("/databases")
    const band = page.locator("[data-slot=fleet-band]")
    const names = (list: string) =>
      band
        .getByRole("list", { name: list })
        .getByRole("link")
        .evaluateAll((links) => links.map((link) => link.getAttribute("aria-label")))

    // Largest first, the file weighed on disk among them.
    await expect(band.getByRole("region", { name: "Storage" })).toContainText(/33\.6 MB\s*stored/)
    expect(await names("Largest databases")).toEqual([
      "Open shop",
      "Open cache",
      "Open blog",
      "Open app",
      "Open notes",
    ])

    // Busiest first, in the engine's own word, and a server that did not answer is
    // counted apart rather than drawn as an empty span.
    const sessions = band.getByRole("region", { name: "Sessions" })
    expect(await names("Busiest databases")).toEqual(["Open app", "Open shop", "Open cache"])
    await sessions.getByRole("link", { name: "Open cache" }).hover()
    await expect(sessions.locator("[data-slot=bar-reading-line]")).toContainText(/3\s*clients/)
    await expect(sessions).toContainText("1 server not answering")

    // Never dumped first, then the oldest; a standing opens the dumps of the
    // worst database in it, which its line names.
    const backups = band.getByRole("region", { name: "Backups" })
    await expect(backups).toContainText(/3\s*of 5 in the last day/)
    await expect(backups.getByRole("img", { name: /^Backups: 1 never, .*3 today$/ })).toBeVisible()
    expect(await names("Databases by last backup")).toEqual([
      "Backups of cache",
      "Backups of app",
      "Backups of blog",
    ])
    const never = backups.getByRole("link", { name: "Backups of cache" })
    await expect(never).toHaveAttribute("href", "/databases/4/backups")
    const reading = backups.locator("[data-slot=bar-reading-line]")
    await expect(reading).toContainText("cache")
    await expect(reading.getByText("never backed up")).toHaveClass(/text-warning/)
    await backups.getByRole("link", { name: "Backups of blog" }).hover()
    await expect(reading).toContainText("3 today")
    await expect(reading).toContainText("blog")
    // Each standing is marked with the engine of the database its line names, as the
    // other two blocks mark theirs: a bare count stood there and read as a stray digit.
    const marks = backups.getByRole("list", { name: "Databases by last backup" })
    await expect(marks.locator("img")).toHaveCount(3)
    await expect(marks.locator("img").first()).toHaveAttribute("src", /redis/)
    expect(await marks.getByRole("link").allTextContents()).toEqual(["", "", ""])

    // One database, one hue: its span in the band is the line of its last hour on
    // its card, and the card's edge carries none. The span holds the colour since the
    // band's marks were drawn bare, without a rule in their part's colour under each.
    const hue = (el: Element) => getComputedStyle(el).backgroundColor
    const edge = async (name: string) =>
      card(page, name)
        .locator("[data-slot=fleet-trend] path[stroke]")
        .evaluate((line) => getComputedStyle(line).stroke)
    await expect(
      card(page, "shop").getByRole("img", { name: "Sessions open on shop over the last hour" }),
    ).toBeVisible()
    await expect(
      card(page, "cache").getByRole("img", { name: "Clients open on cache over the last hour" }),
    ).toBeVisible()
    // A stopped server draws no hour, and one with none recorded keeps the line's place.
    await expect(card(page, "blog").locator("[data-slot=fleet-trend] svg")).toHaveCount(0)
    await expect(card(page, "app").locator("[data-slot=fleet-trend] svg")).toHaveCount(0)
    await expect(page.locator("[data-slot=fleet-size]")).toHaveCount(0)
    const fills = band
      .getByRole("region", { name: "Storage" })
      .locator("[data-slot=reading-span] > i > i")
    const key = (name: string) =>
      band
        .getByRole("region", { name: "Storage" })
        .locator(`[data-slot=reading-span][title^="${name} holds "] > i > i`)
        .evaluate(hue)
    expect(await key("shop")).toBe(await edge("shop"))
    expect(await key("cache")).toBe(await edge("cache"))
    await expect(page.locator("[data-slot=fleet-hue]")).toHaveCount(0)
    // Five databases are five hues.
    const edges = await fills.evaluateAll((spans) =>
      spans.map((span) => getComputedStyle(span).backgroundColor),
    )
    expect(edges).toHaveLength(5)
    expect(new Set(edges).size).toBe(5)

    await band.getByRole("list", { name: "Largest databases" }).getByRole("link").first().click()
    await expect(page).toHaveURL(/\/databases\/1$/)
  })

  test("every block of the band is one height, whatever it has to say", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    // Nothing connected: Sessions says so in a line, beside two blocks with bars.
    await mockFleet(page, {
      fleet: Object.fromEntries([1, 2, 3, 4, 5].map((id) => [id, { sessions: 0 }])),
    })
    await page.goto("/databases")
    const blocks = page.locator("[data-slot=fleet-band] [data-slot=bar-reading]")
    await expect(blocks).toHaveCount(3)
    await expect(page.getByRole("region", { name: "Sessions" })).toContainText(
      "Nothing is connected to any database right now.",
    )
    await risen(page)
    const heights = await blocks.evaluateAll((all) =>
      all.map((block) => block.getBoundingClientRect().height),
    )
    expect(new Set(heights).size).toBe(1)
  })

  test("what the attention list fixed is fixed from the database's own card", async ({ page }) => {
    const server = await mockFleet(page, {
      dumps: { 1: null },
      fleet: {
        1: { exposure: "public" },
        2: { ok: false, state: "stopped", consumers: 2 },
        4: { ok: false, state: "unreachable", error: "NOAUTH Authentication required" },
      },
    })
    await page.goto("/databases")

    // The server that does not answer is the verdict, and the first card on its shelf.
    await expect(identity(page)).toContainText("1 database not answering")
    await expect(
      page.getByRole("list", { name: "Containers" }).locator("[data-card=database]").first(),
    ).toContainText("cache")
    await expect(card(page, "cache")).toContainText("NOAUTH Authentication required")

    // Public, in a container of its own: restricted from its menu, asked first.
    await expect(card(page, "shop").getByText("open to the internet")).toHaveClass(/text-warning/)
    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await page.getByRole("menuitem", { name: "Restrict to this server" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Recreates shop-db")
    await dialog.getByRole("button", { name: "Restrict" }).click()
    await expect
      .poll(() => server.bodies("PUT /databases/1/access"))
      .toEqual([{ exposure: "local" }])

    // Never dumped: dumped from its menu, and the job is watched.
    await expect(card(page, "shop").getByText("never backed up")).toBeVisible()
    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await page.getByRole("menuitem", { name: "Back up now" }).click()
    await expect(page.getByText("shop backed up")).toBeVisible()
    expect(server.bodies("POST /databases/1/backup")).toEqual([{}])

    // Stopped while deployments use it: Start is on the card.
    await card(page, "blog").getByRole("button", { name: "Start blog" }).click()
    await expect.poll(() => server.bodies("POST /databases/2/power")).toEqual([{ action: "start" }])

    // A server found running that waits for a password is where found servers are.
    await expect(foundRow(page, "postgresql@17-main")).toBeVisible()
  })

  test("a MongoDB this kernel keeps from starting is fixed from its own card", async ({ page }) => {
    const problem = {
      message: "MongoDB cannot start on this kernel; apply the mitigation",
      detail:
        "MongoDB 8.0.34 refuses to start on Linux 7.0.0-1017-azure (MongoDB SERVER-121912). Applying the mitigation recreates its container with GLIBC_TUNABLES=glibc.pthread.rseq=1, on the same volumes.",
      kernel: "7.0.0-1017-azure",
      release: "8.0.34",
      fixable: true,
    }
    const server = await mockFleet(page, {
      fleet: {
        5: {
          ok: false,
          state: "unreachable",
          error: "server selection error: connection refused",
          kernelProblem: problem,
        },
      },
    })
    server.answer("POST /databases/5/kernel-mitigation", () => ({
      body: { container: "app-db", kernel: problem.kernel, release: problem.release, warnings: [] },
    }))
    await page.goto("/databases")

    // The reason where a failure is said, not the dial it refused, and the
    // fix in its foot where Start would be.
    const app = card(page, "app")
    await expect(app.locator("[data-slot=fleet-error]")).toHaveText(problem.message)
    await expect(app.locator("[data-slot=fleet-error]")).toHaveAttribute("title", problem.detail)
    await expect(app).not.toContainText("connection refused")
    // Only that card offers it, on its foot and in its menu.
    await expect(page.getByRole("button", { name: /Apply the kernel mitigation/ })).toHaveCount(1)
    await app.getByRole("button", { name: "Actions for app" }).click()
    await expect(page.getByRole("menuitem", { name: "Apply kernel mitigation" })).toBeVisible()
    await page.keyboard.press("Escape")
    await app.getByRole("button", { name: "Apply the kernel mitigation to app" }).click()

    // Asked first, in what it does, then carried out once.
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("GLIBC_TUNABLES=glibc.pthread.rseq=1")
    await expect(dialog).toContainText("Linux 7.0.14 and later do not need it")
    await dialog.getByRole("button", { name: "Apply" }).click()
    await expect.poll(() => server.bodies("POST /databases/5/kernel-mitigation")).toEqual([{}])
    await expect(page.getByText("app was recreated with the kernel mitigation")).toBeVisible()
    // And it says it is starting, not offering the fix again, until it answers.
    await expect(app).toContainText("Starting…")
    await expect(page.getByRole("button", { name: /Apply the kernel mitigation/ })).toHaveCount(0)
  })

  test("forty databases fit the page as cards", async ({ page }) => {
    const server = await mockFleet(page)
    server.answer("GET /databases/fleet", () => ({
      body: {
        connections: manyDatabases(40),
        unreachable: [],
        needsCredentials: [],
        checkedAt: new Date().toISOString(),
      },
    }))
    server.answer("GET /databases/backups/summary", () => ({ body: { connections: [] } }))
    await page.goto("/databases")
    await expect(page.locator("[data-card=database]")).toHaveCount(40)
    // The band names five of each and counts the rest.
    const band = page.locator("[data-slot=fleet-band]")
    await expect(
      band.getByRole("list", { name: "Largest databases" }).getByRole("link"),
    ).toHaveCount(5)
    await expect(band.getByRole("region", { name: "Storage" })).toContainText("35 more databases")

    // Nothing spills out of a card or the page, whatever the names and figures are.
    expect(
      await scroller(page).evaluate((el) => el.scrollWidth - el.clientWidth),
    ).toBeLessThanOrEqual(1)
  })

  test("a card says what is wrong in one weight, keeps its labels out of its readings, and cuts nothing it prints", async ({
    page,
  }) => {
    await mockFleet(page, {
      dumps: { 4: null, 5: 400 },
      rows: { 1: { environment: "staging" }, 5: { environment: "production", readOnly: true } },
      fleet: {
        1: {
          ok: false,
          state: "unreachable",
          error:
            'failed to connect to `user=app database=shop_main`: 127.0.0.1:5432 (127.0.0.1): failed SASL auth: FATAL: password authentication failed for user "app" (SQLSTATE 28P01)',
        },
        2: { ok: false, state: "stopped" },
      },
    })
    await page.goto("/databases")

    // What it holds is read whole at this width, in the engine's own noun.
    const holds = card(page, "app").locator("[data-slot=fleet-holds]")
    await expect(holds).toHaveText(/764\.0 KB stored\s*4 collections/)
    expect(
      await holds.evaluate((el) =>
        [el, ...el.children].every((part) => part.scrollWidth <= part.clientWidth),
      ),
    ).toBe(true)

    // Two whole lines of the server's answer and no part of a third.
    const said = card(page, "shop").locator("[data-slot=fleet-error]")
    const lines = await said.evaluate((el) => {
      const style = getComputedStyle(el)
      return el.getBoundingClientRect().height / parseFloat(style.lineHeight)
    })
    expect(lines).toBeCloseTo(2, 1)
    await expect(said).toHaveAttribute("title", /SQLSTATE 28P01/)

    // The card that says what the server answered is one press from where it is fixed.
    await expect(
      card(page, "shop").getByRole("link", { name: "Settings of shop" }),
    ).toHaveAttribute("href", "/databases/1/settings")

    // The operator's labels stand in the head; the line of facts holds readings only.
    await expect(card(page, "shop").locator("[data-slot=fleet-facts] [data-slot=tag]")).toHaveCount(
      0,
    )
    await expect(card(page, "app").locator("[data-slot=fleet-facts] [data-slot=tag]")).toHaveCount(
      0,
    )
    await expect(card(page, "shop").getByText("staging", { exact: true })).toBeVisible()
    await expect(card(page, "app").getByText("production", { exact: true })).toBeVisible()
    await expect(card(page, "app").getByText("protected", { exact: true })).toBeAttached()
    const facts = card(page, "app").locator("[data-slot=fleet-facts]")
    expect((await facts.boundingBox())?.height ?? 99).toBeLessThan(24)

    // A dump over a week old weighs the same on the card as none at all.
    const hue = (el: Element) => getComputedStyle(el).color
    const stale = await card(page, "app")
      .getByText(/^backed up/)
      .evaluate(hue)
    const never = await card(page, "cache").getByText("never backed up").evaluate(hue)
    expect(stale).toBe(never)

    // Start is the one fix a stopped database offers, and is not drawn stepped back.
    const start = card(page, "blog").getByRole("button", { name: "Start blog" })
    const name = card(page, "blog").getByRole("link", { name: "Open blog" })
    expect(await start.evaluate(hue)).toBe(await name.evaluate(hue))
  })

  test("a stopped server keeps its card with Start, and says it is starting until it has", async ({
    page,
  }) => {
    const started = hold()
    const server = await mockFleet(page, { fleet: { 2: { ok: false, state: "stopped" } } })
    server.answer("POST /databases/2/power", async () => {
      await started.until
    })
    await page.goto("/databases")

    await expect(card(page, "blog").locator("[data-slot=database-status]")).toHaveText("stopped")
    // Nothing to stop, nothing to open: Start is its verb.
    await expect(card(page, "blog").getByRole("link", { name: "Data of blog" })).toHaveCount(0)
    await card(page, "blog").getByRole("button", { name: "Actions for blog" }).click()
    await expect(page.getByRole("menuitem", { name: "Start" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Stop" })).toHaveCount(0)
    await page.keyboard.press("Escape")

    await card(page, "blog").getByRole("button", { name: "Start blog" }).click()
    await expect(card(page, "blog")).toContainText("Starting…")
    await expect(card(page, "blog").getByRole("button", { name: "Start blog" })).toHaveCount(0)
    started.release()
    await expect(page.getByText("blog started")).toBeVisible()
    expect(server.bodies("POST /databases/2/power")).toEqual([{ action: "start" }])
  })

  test("stopping asks first, names the container and what depends on it", async ({ page }) => {
    const server = await mockFleet(page)
    await page.goto("/databases")

    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await expect(page.getByRole("menuitem")).toHaveText([
      "Stop",
      "Restart",
      "Back up now",
      "Settings",
      "Forget",
    ])
    await page.getByRole("menuitem", { name: "Stop" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Stops shop-db")
    await expect(dialog).toContainText("2 deployment environments are bound to it")
    expect(server.sent).toEqual([])
    await dialog.getByRole("button", { name: "Stop" }).click()
    await expect(page.getByText("shop stopped")).toBeVisible()
    expect(server.bodies("POST /databases/1/power")).toEqual([{ action: "stop" }])

    // A server somewhere else has no power here, and a file none at all.
    await card(page, "app").getByRole("button", { name: "Actions for app" }).click()
    await expect(page.getByRole("menuitem")).toHaveText(["Back up now", "Settings", "Forget"])
  })

  test("forgetting a connection a deployment is linked to says what to undo first", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    server.answer("DELETE /databases/1", () =>
      refusal(
        409,
        "database_linked",
        "remove the deployment's managed database network before forgetting this linked connection",
      ),
    )
    await page.goto("/databases")

    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await page.getByRole("menuitem", { name: "Forget" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("The database itself and its data are not touched")
    // Before it is tried, a link is a possibility, not a refusal already given.
    await expect(dialog).toContainText(
      "If one of them is linked through a managed database network",
    )
    await dialog.getByRole("button", { name: "Forget" }).click()
    // The refusal is read in the dialog, beside the question, in plain words.
    await expect(
      dialog.getByText("It is linked to a deployment, so it was not forgotten"),
    ).toBeVisible()
    await expect(dialog).toContainText(
      "Remove the link under that deployment's Settings › Databases",
    )
    await expect(
      page.getByText("shop is linked to a deployment through a managed database network."),
    ).toBeVisible()
    await expect(page.getByText(/Error:/)).toHaveCount(0)
    await dialog.getByRole("button", { name: "Cancel" }).click()
    await expect(card(page, "shop")).toBeVisible()
    // The keyboard goes back to the database's own menu, not to the top of the page.
    await expect(card(page, "shop").getByRole("button", { name: "Actions for shop" })).toBeFocused()

    // One nothing is linked to goes, and its card with it.
    await card(page, "cache").getByRole("button", { name: "Actions for cache" }).click()
    await page.getByRole("menuitem", { name: "Forget" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Forget" }).click()
    await expect(page.getByText("Forgot cache")).toBeVisible()
    await expect(card(page, "cache")).toHaveCount(0)
  })

  test("forgetting promises the ignore list only where the server will use it", async ({
    page,
  }) => {
    await mockFleet(page, {
      rows: {
        1: { origin: "docker:shop-db" },
        2: { origin: "docker:blog-db-1" },
        4: { origin: "docker:blog-db-1" },
        7: { origin: "file:/srv/notes/notes.db" },
      },
    })
    await page.goto("/databases")
    const asks = async (name: string) => {
      await card(page, name)
        .getByRole("button", { name: `Actions for ${name}` })
        .click()
      await page.getByRole("menuitem", { name: "Forget" }).click()
      const text = await page.getByRole("dialog").innerText()
      await page.keyboard.press("Escape")
      await expect(page.getByRole("dialog")).toHaveCount(0)
      await expect(
        card(page, name).getByRole("button", { name: `Actions for ${name}` }),
      ).toBeFocused()
      return text
    }
    // The last connection to a found server: it is set aside with it.
    expect(await asks("shop")).toContain("put on the ignore list")
    // A file is never put there, nor a server another connection still points at,
    // nor a connection nobody found.
    expect(await asks("notes")).not.toContain("ignore list")
    expect(await asks("blog")).not.toContain("ignore list")
    expect(await asks("app")).not.toContain("ignore list")
  })

  test("a role that only reads is drawn no command it cannot use and asks for no inventory", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      viewer: true,
      fleet: { 2: { ok: false, state: "stopped" } },
    })
    await page.goto("/databases")

    await expect(card(page, "shop")).toBeVisible()
    await expect(page.getByRole("link", { name: "Add a database" })).toHaveCount(0)
    // The menu holds what any role may open, and no scan.
    await more(page).click()
    await expect(page.getByRole("menuitem")).toHaveText(["Metrics export", "MCP server"])
    await page.keyboard.press("Escape")
    await expect(page.getByRole("heading", { name: "Found on this server" })).toHaveCount(0)
    await expect(card(page, "blog").getByRole("button", { name: "Start blog" })).toHaveCount(0)
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
    // The size nobody reported to this role is not said, rather than said as nothing.
    await expect(card(page, "notes")).not.toContainText("stored")
    await expect(card(page, "notes")).toContainText(/4\s*tables/)
    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await expect(page.getByRole("menuitem")).toHaveText(["Settings"])
    expect(server.asked).not.toContain("GET /databases/inventory")
  })

  test("it waits in the shape it will have, says a failure with a way to ask again, and a failed poll takes nothing away", async ({
    page,
  }) => {
    const first = hold()
    let answer: "held" | "failing" | "given" = "held"
    const server = await mockFleet(page, {
      // One server is down, so the fleet is read again every five seconds.
      fleet: { 4: { ok: false, state: "unreachable", error: "connection refused" } },
    })
    server.answer("GET /databases/fleet", async () => {
      if (answer === "held") await first.until
      // Not marked as worth retrying, which is how most failures of a read arrive.
      if (answer === "failing") return refusal(500, "internal", "the fleet could not be read")
    })
    await page.goto("/databases")
    await expect(page.getByRole("status", { name: "Loading databases" })).toBeVisible()
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
    // The line it opens on is drawn before the fleet has answered, with its command
    // and its menu, where it will be once it has.
    await expect(identity(page)).toContainText("Reading the databases…")
    await expect(identity(page).getByRole("link", { name: "Add a database" })).toBeVisible()
    await risen(page)
    const waiting = (await identity(page).boundingBox())!

    answer = "failing"
    first.release()
    await expect(
      page.getByRole("alert").filter({ hasText: "the fleet could not be read" }),
    ).toBeVisible()
    // The page keeps its commands and says when it will ask by itself.
    await expect(page.getByRole("main").getByRole("link", { name: "Add a database" })).toBeVisible()
    await expect(page.getByText("It is asked again by itself every 30 seconds.")).toBeVisible()
    answer = "given"
    await expect(page.getByRole("button", { name: "Try again" })).toHaveCount(1)
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(card(page, "shop")).toBeVisible()
    await risen(page)
    const read = (await identity(page).boundingBox())!
    expect(read.y).toBe(waiting.y)
    expect(read.height).toBe(waiting.height)

    answer = "failing"
    await expect(page.getByText("the last check did not finish").first()).toBeVisible({
      timeout: 15_000,
    })
    await expect(card(page, "shop")).toBeVisible()
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  })

  test("leaves what the databases feed to the map, and reaches it from the readers it counts", async ({
    page,
  }) => {
    await mockFleet(page)
    await page.goto("/databases")
    await expect(card(page, "shop")).toBeVisible()
    await expect(page.getByRole("heading", { name: "What they feed" })).toHaveCount(0)
    await expect(page.getByText("Nothing seen reading")).toHaveCount(0)
    await expect(page.locator("[data-slot=wiring]")).toHaveCount(0)
    const readers = page.getByRole("link", { name: "read by 4" })
    await expect(readers).toHaveAttribute("href", "/databases/map")
    await readers.click()
    await expect(page.locator(".react-flow__node").first()).toBeVisible()
  })

  test("coming back to it draws the last reading at once while it asks again", async ({ page }) => {
    const server = await mockFleet(page)
    await page.goto("/databases")
    await expect(card(page, "shop")).toBeVisible()
    await page.getByRole("link", { name: "Map", exact: true }).click()
    await expect(page.locator(".react-flow__node").first()).toBeVisible()

    const held = hold()
    server.answer("GET /databases/fleet", async () => {
      await held.until
    })
    await page.getByRole("link", { name: "Control center", exact: true }).click()
    await expect(card(page, "shop")).toBeVisible()
    await expect(page.getByRole("status", { name: "Loading databases" })).toHaveCount(0)
    held.release()
  })

  test("an empty server says so, offers the way to add one, and lists what was found under it", async ({
    page,
  }) => {
    await mockFleet(page, { empty: true })
    await page.goto("/databases")

    await expect(page.getByText("No databases yet")).toBeVisible()
    await expect(
      page.locator("[data-slot=empty-state]").getByRole("link", { name: "Add a database" }),
    ).toHaveAttribute("href", "/databases/new")
    // The server keeps its line with nothing connected, and the page its one command.
    await expect(identity(page)).toContainText("No database is connected yet")
    await expect(page.getByRole("main").getByRole("link", { name: "Add a database" })).toHaveCount(
      1,
    )
    await more(page).click()
    await expect(page.getByRole("menuitem")).toHaveText([
      "Metrics export",
      "MCP server",
      "Scan this server again",
    ])
    await page.keyboard.press("Escape")
    await expect(page.getByRole("heading", { name: "Found on this server" })).toBeVisible()
    await expect(foundRow(page, "orders-db")).toBeVisible()
  })
})

test.describe("found on this server", () => {
  test("a MongoDB that never started on this kernel is recreated with the mitigation, asked first", async ({
    page,
  }) => {
    const inventory = dockerInventory()
    const mysql = inventory.instances.find((one) => one.key === "docker:old-mysql")!
    const problem = {
      message: "MongoDB cannot start on this kernel; apply the mitigation",
      detail:
        "MongoDB 8.0.34 refuses to start on Linux 7.0.0-1017-azure (MongoDB SERVER-121912). Applying the mitigation recreates its container with GLIBC_TUNABLES=glibc.pthread.rseq=1, on the same volumes.",
      kernel: "7.0.0-1017-azure",
      release: "8.0.34",
      fixable: true,
    }
    inventory.instances.push({
      ...mysql,
      key: "docker:orders-mongo",
      name: "orders-mongo",
      engine: "mongodb",
      driver: "mongodb",
      flavor: "mongodb",
      label: "MongoDB",
      state: "restarting",
      reason: "the container is restarting",
      container: {
        ...mysql.container!,
        id: "c-mongo",
        name: "orders-mongo",
        image: "mongo:8.0",
        status: "Restarting (1) 4 seconds ago",
      },
    })
    const server = await mockFleet(page, {
      inventory: Object.assign(inventory, { kernelProblems: { "docker:orders-mongo": problem } }),
    })
    server.answer("POST /databases/kernel-mitigation", () => ({
      body: {
        container: "orders-mongo",
        kernel: problem.kernel,
        release: problem.release,
        warnings: [],
      },
    }))
    await page.goto("/databases")

    // Said as the reason it does not start, with the fix as its one press.
    await expect(foundRow(page, "orders-mongo")).toContainText(problem.message)
    await page.getByRole("button", { name: "Apply the kernel mitigation to orders-mongo" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("GLIBC_TUNABLES=glibc.pthread.rseq=1")
    await expect(dialog).toContainText("mongo:8.0")
    await dialog.getByRole("button", { name: "Apply" }).click()
    await expect
      .poll(() => server.bodies("POST /databases/kernel-mitigation"))
      .toEqual([{ container: "c-mongo" }])
    await expect(
      page.getByText("orders-mongo was recreated with the kernel mitigation"),
    ).toBeVisible()
  })

  test("lists what is not connected with the one thing to do about each", async ({ page }) => {
    await mockFleet(page)
    await page.goto("/databases")
    const found = page.locator("section").filter({
      has: page.getByRole("heading", { name: "Found on this server" }),
    })

    // Connected ones are not listed again.
    await expect(found.getByText("shop-db")).toHaveCount(0)
    await expect(found.getByRole("button", { name: "Connect orders-db" })).toBeVisible()
    await expect(found.getByRole("button", { name: "Connect postgresql@17-main" })).toBeVisible()
    await expect(found.getByRole("button", { name: "Start old-mysql" })).toBeVisible()
    await expect(foundRow(page, "old-mysql")).toContainText(
      "The container is exited — start it to connect.",
    )
    // One that is up and waits says what it waits for.
    await expect(foundRow(page, "postgresql@17-main")).toContainText(
      "Its accounts are kept in its own catalogue",
    )
    await expect(found.getByRole("button", { name: "Start redis-server" })).toBeVisible()
    await expect(found.getByRole("button", { name: "Open data.db" })).toBeVisible()

    // Declared and never created: the stack that declares it is where to go.
    await expect(found.getByRole("link", { name: "Open stack search-db" })).toHaveAttribute(
      "href",
      "/docker/stacks/shop",
    )
    // Seen, and nothing here opens it: it says so and links to what runs it.
    await expect(foundRow(page, "memcached")).toContainText("no driver yet")
    await expect(found.getByRole("link", { name: "Open container memcached" })).toHaveAttribute(
      "href",
      "/docker/containers/c-mc",
    )
    await expect(foundRow(page, "sales.duckdb")).toContainText("no driver for DuckDB")
    await expect(found.getByText("2 directories are outside the file roots")).toBeVisible()

    // Servers stand in the order they can be connected: a press, a password,
    // a start, and last what nothing here can open.
    const rows = await found.locator("[data-slot=choice-row]").allTextContents()
    const at = (name: string) => rows.findIndex((text) => text.includes(name))
    expect(at("orders-db")).toBeLessThan(at("postgresql@17-main"))
    expect(at("postgresql@17-main")).toBeLessThan(at("old-mysql"))
    expect(at("old-mysql")).toBeLessThan(at("memcached"))
    // A file's second line is the folder it is in; its name is the title.
    await expect(foundRow(page, "data.db")).toContainText("/srv/app")
    await expect(foundRow(page, "data.db")).not.toContainText("/srv/app/data.db")

    // The dashboard's own store is counted behind the one fold of what is
    // set aside, with no way to connect it.
    await expect(found.getByText("1 kept by other programs", { exact: false })).toBeVisible()
    await found.getByText("Set aside", { exact: true }).click()
    await found.getByRole("button", { name: /^Other programs/ }).click()
    await expect(foundRow(page, "vpsd.db")).toContainText("never connected")
    await expect(found.getByRole("button", { name: "Open vpsd.db" })).toHaveCount(0)
  })

  test("connects by key, asks for a password only when the server refuses what it states", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    let tried = 0
    server.answer("POST /databases/inventory/connect", ({ body }) => {
      if (body.key !== "docker:orders-db") return
      tried += 1
      if (!body.password) {
        return refusal(
          409,
          "sign_in_failed",
          "orders-db did not accept the credentials found for it (password authentication failed); type the password it actually uses",
        )
      }
      if (body.password !== "right") {
        return refusal(400, "sign_in_failed", 'password authentication failed for user "orders"')
      }
    })
    await page.goto("/databases")

    await page.getByRole("button", { name: "Connect orders-db" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("did not accept the credentials found for it")
    await expect(dialog.getByLabel("User")).toHaveValue("orders")
    await dialog.getByLabel("Password").fill("wrong")
    await dialog.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(dialog).toContainText("It refused the sign-in")
    await expect(dialog).toContainText('password authentication failed for user "orders"')
    await dialog.getByLabel("Password").fill("right")
    await dialog.getByRole("button", { name: "Connect", exact: true }).click()

    await expect(page.getByText("Connected orders-db")).toBeVisible()
    await expect(card(page, "orders-db")).toBeVisible()
    expect(tried).toBe(3)
    expect(server.bodies("POST /databases/inventory/connect").at(-1)).toEqual({
      key: "docker:orders-db",
      user: "orders",
      password: "right",
      database: "orders",
    })
  })

  test("a container the fleet says already refused what it states is asked for its password, not tried again", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      reported: {
        unreachable: [
          {
            container: "orders-db",
            driver: "postgres",
            reason:
              'it did not accept the credentials its container states (password authentication failed for user "orders") — connect it with the password it actually uses',
          },
        ],
      },
    })
    await page.goto("/databases")
    await expect(foundRow(page, "orders-db")).toContainText(
      "It did not accept the credentials its container states",
    )
    const press = page.getByRole("button", { name: "Connect orders-db" })
    await press.click()
    const dialog = page.getByRole("dialog")
    await expect(dialog.getByLabel("Password")).toBeVisible()
    expect(server.bodies("POST /databases/inventory/connect")).toEqual([])
    // Closing it gives the keyboard back to the row it was opened from.
    await page.keyboard.press("Escape")
    await expect(dialog).toHaveCount(0)
    await expect(press).toBeFocused()
    // What was typed before a cancel is not what the next opening starts from.
    await press.click()
    await dialog.getByLabel("Password").fill("half-typed")
    await dialog.getByRole("button", { name: "Cancel" }).click()
    await expect(press).toBeFocused()
    await press.click()
    await expect(dialog.getByLabel("Password")).toHaveValue("")
  })

  test("when the inventory cannot be read, what the fleet reported is still listed and a host server connects by its address", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      reported: {
        unreachable: [
          {
            container: "orders-db",
            driver: "postgres",
            reason: "its container states no password — connect it with the one it uses",
          },
        ],
        needsCredentials: [
          {
            driver: "postgres",
            host: "127.0.0.1",
            port: 5438,
            name: "postgres 17 main on this host",
            user: "postgres",
            database: "postgres",
          },
        ],
      },
    })
    server.answer("GET /databases/inventory", () =>
      refusal(502, "bad_gateway", "the inventory did not answer"),
    )
    await page.goto("/databases")
    const found = page.locator("section").filter({
      has: page.getByRole("heading", { name: "Found on this server" }),
    })
    await expect(
      found.getByRole("alert").filter({ hasText: "the inventory did not answer" }),
    ).toBeVisible()
    await expect(found.getByRole("button", { name: "Try again" })).toBeVisible()
    await expect(found.getByText("Reported with the fleet")).toBeVisible()
    await expect(foundRow(page, "orders-db")).toContainText("Its container states no password")
    await found.getByRole("button", { name: "Connect postgres 17 main on this host" }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByRole("button", { name: "I know a password" }).click()
    await expect(dialog.getByLabel("User")).toHaveValue("postgres")
    await dialog.getByLabel("Password", { exact: true }).fill("s3cret")
    await dialog.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(page.getByText("Connected postgres on this host")).toBeVisible()
    expect(server.bodies("POST /databases/host")).toEqual([
      {
        driver: "postgres",
        host: "127.0.0.1",
        port: 5438,
        user: "postgres",
        password: "s3cret",
        database: "postgres",
        name: "",
      },
    ])
    expect(server.bodies("POST /databases/inventory/connect")).toEqual([])
  })

  test("a native server offers an account made from this machine, with its password shown before it is used", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases")

    await page.getByRole("button", { name: "Connect postgresql@17-main" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("postgresql@17-main.service · 127.0.0.1:5438")
    await expect(
      dialog.getByRole("button", { name: "Make an account from this server" }),
    ).toHaveAttribute("aria-pressed", "true")
    const generated = await dialog.getByLabel("Its password").inputValue()
    expect(generated).toMatch(/^[A-Za-z0-9]{30}$/)
    await dialog.getByRole("button", { name: "Make the account and connect" }).click()

    await expect(page.getByText("Connected postgres 17 main on this host")).toBeVisible()
    expect(server.bodies("POST /databases/host/grant")).toEqual([
      {
        driver: "postgres",
        host: "127.0.0.1",
        port: 5438,
        user: "just_dashboard",
        password: generated,
        database: "postgres",
        name: "",
        superuser: true,
      },
    ])
  })

  test("starts what is down, opens a file, ignores and brings back, and scans on request", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases")

    await page.getByRole("button", { name: "Start old-mysql" }).click()
    await expect
      .poll(() => server.sent.map((one) => one.call))
      .toContain("POST /docker/containers/c-old/start")
    await expect(foundRow(page, "old-mysql")).toContainText("Starting…")
    await page.getByRole("button", { name: "Start redis-server" }).click()
    await expect
      .poll(() => server.sent.map((one) => one.call))
      .toContain("POST /systemd/redis-server.service/start")

    await page.getByRole("button", { name: "Open data.db" }).click()
    await expect(page.getByText("Connected data.db")).toBeVisible()
    expect(server.bodies("POST /databases/inventory/connect")).toEqual([
      { key: "file:/srv/app/data.db" },
    ])

    await page.getByRole("button", { name: "Ignore sessions" }).click()
    await expect
      .poll(() => server.bodies("POST /databases/inventory/ignore"))
      .toEqual([{ key: "docker:sessions", ignored: true }])
    await page.getByText("Set aside", { exact: true }).click()
    await page.getByRole("button", { name: /^Ignored/ }).click()
    await expect(page.getByRole("button", { name: "Stop ignoring sessions" })).toBeVisible()
    // Keys whose server is gone are counted, not listed, and forgotten together.
    await expect(page.getByText("1 ignored item is no longer on this server.")).toBeVisible()
    await page.getByRole("button", { name: "Forget it" }).click()
    await expect
      .poll(() => server.bodies("POST /databases/inventory/ignore").at(-1))
      .toEqual({
        key: "docker:long-gone",
        ignored: false,
      })

    await more(page).click()
    await page.getByRole("menuitem", { name: "Scan this server again" }).click()
    await expect
      .poll(() => server.sent.map((one) => one.call))
      .toContain("POST /databases/inventory/scan")
  })

  test("a Docker that did not answer is a stated silence, drawn as Docker with the step that fixes it", async ({
    page,
  }) => {
    const server = await mockFleet(page, { inventory: dockerlessInventory() })
    let stopped = true
    server.answer("GET /docker/ping", () => ({
      body: stopped
        ? {
            available: false,
            error: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock.",
          }
        : { available: true },
    }))
    server.answer("GET /systemd/docker.service", () => ({
      body: {
        unit: {
          name: "docker.service",
          loadState: "loaded",
          activeState: stopped ? "inactive" : "active",
          subState: stopped ? "dead" : "running",
          description: "Docker Application Container Engine",
        },
      },
    }))
    await page.goto("/databases")

    const silence = page.locator("[data-slot=found-silence]")
    await expect(silence).toContainText("Not looked at: containers and compose services")
    const docker = silence.locator("[data-slot=missing-thing]")
    await expect(docker).toHaveAttribute("data-thing", "docker")
    await expect(docker.locator("img").first()).toHaveAttribute("src", /docker/)
    await expect(docker).toContainText("Docker is stopped")
    // The daemon's own sentence is folded away, never the headline.
    await expect(docker.locator("details")).not.toHaveAttribute("open", "")
    await expect(page.getByText("Cannot connect to the Docker daemon").first()).toBeHidden()
    // What the other collectors found is still listed, and nothing claims the list is whole.
    await expect(page.getByRole("button", { name: "Connect postgresql@17-main" })).toBeVisible()
    await expect(page.getByText("Everything found on this server is connected.")).toHaveCount(0)

    // The step is Docker's own: starting it, and the list is read again once it answers.
    const before = server.asked.filter((call) => call === "GET /databases/inventory").length
    stopped = false
    await docker.getByRole("button", { name: "Start Docker" }).click()
    await expect
      .poll(() => server.sent.map((one) => one.call))
      .toContain("POST /systemd/docker.service/start")
    await expect
      .poll(() => server.asked.filter((call) => call === "GET /databases/inventory").length)
      .toBeGreaterThan(before)
  })

  test("with nothing connected and Docker silent, the page says neither that all is connected", async ({
    page,
  }) => {
    const base = dockerlessInventory()
    await mockFleet(page, { empty: true, inventory: { ...base, instances: [] } })
    await page.goto("/databases")
    await expect(page.locator("[data-slot=found-silence]")).toBeVisible()
    await expect(page.getByText("Everything found on this server is connected.")).toHaveCount(0)
    await expect(identity(page)).toContainText("No database is connected yet")
  })
})

test.describe("the map", () => {
  const node = (page: Page, id: string) => page.locator(`.react-flow__node[data-id="${id}"]`)
  const frame = (page: Page, system: string) => node(page, `frame:${system}`)
  const rail = (page: Page) => page.getByRole("navigation", { name: "Databases" })
  const inspector = (page: Page) => page.getByRole("complementary", { name: "Inspector" })
  const at = async (page: Page, id: string) => {
    const box = await node(page, id).boundingBox()
    if (!box) throw new Error(`${id} is not drawn`)
    return box
  }

  async function open(page: Page, path = "/databases/map", options = {}) {
    await page.setViewportSize({ width: 1600, height: 1000 })
    const server = await mockFleet(page, options)
    await page.goto(path)
    await expect(page.locator(".react-flow__node").first()).toBeVisible()
    return server
  }

  test("every database is its own diagram on the server map's canvas, listed in the rail by engine", async ({
    page,
  }) => {
    await open(page)
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
    await expect(page.locator("[data-slot=wiring]")).toHaveCount(0)
    // The engine that answered, not the driver that dials it.
    for (const [engine, names] of [
      ["MariaDB", ["blog"]],
      ["MongoDB", ["app"]],
      ["PostgreSQL", ["shop"]],
      ["Redis", ["cache"]],
      ["SQLite", ["notes"]],
    ] as const) {
      const section = rail(page).locator("section", {
        has: page.getByRole("heading", { name: engine }),
      })
      for (const name of names) await expect(section.getByRole("button", { name })).toBeVisible()
    }
    for (const id of [1, 2, 4, 5, 7]) await expect(frame(page, `system:db:${id}`)).toHaveCount(1)
    await expect(rail(page).getByRole("button", { name: /notes/ })).toContainText(
      "nothing seen reading it",
    )
    await expect(frame(page, "system:db:1")).toContainText(
      "feeds 2 things · 3 sessions open · in shop-db",
    )
  })

  test("a database's diagram runs from what reads it, through it, to where it runs, keeps and is copied", async ({
    page,
  }) => {
    await open(page, "/databases/map?database=1")
    const s = "system:db:1"
    const reader = await at(page, `${s}/project:7`)
    const db = await at(page, `${s}/db:1`)
    const runs = await at(page, `${s}/container:c-shop`)
    const keeps = await at(page, `${s}/volume:shop-pg-data`)
    const copied = await at(page, `${s}/backup:2`)
    expect(reader.x).toBeLessThan(db.x)
    expect(db.x).toBeLessThan(runs.x)
    expect(runs.x).toBeLessThan(keeps.x)
    expect(keeps.x).toBeLessThan(copied.x)
    await expect(node(page, `${s}/db:1`)).toHaveClass(/react-flow__node-datastore/)
    await expect(node(page, `${s}/project:7`)).toHaveClass(/react-flow__node-hero/)
    // The worker shares its stack and network: it could reach it.
    await expect(node(page, `${s}/container:c-worker`)).toBeVisible()
    // What two databases are wired to is drawn in each, and says so.
    await expect(node(page, `${s}/project:7`)).toContainText("also in 1")
    // This server and another machine, by the sessions they hold.
    await expect(node(page, "system:db:4/host")).toContainText("Processes here")
    await expect(node(page, "system:db:5/remote:10.0.0.9")).toContainText("6 sessions open")
  })

  test("choosing a box names each of its links in the inspector, a broken one first in its colour", async ({
    page,
  }) => {
    await open(page, "/databases/map?database=2")
    await node(page, "system:db:2/db:2").click()
    const wired = inspector(page).locator("section", {
      has: page.getByRole("heading", { name: "Wired to" }),
    })
    await expect(wired.getByRole("button", { name: /worker/ })).toContainText("link broken")
    await expect(wired.getByRole("button", { name: /blog-db-1/ })).toContainText("serves")
    // The lit line says so too, in its colour.
    await expect(page.locator(".react-flow__edgelabel-renderer")).toContainText("link broken")
    // A row in the rail flies to its diagram.
    await rail(page).getByRole("button", { name: /shop/ }).click()
    await node(page, "system:db:1/db:1").click()
    const storefront = inspector(page).getByRole("button", { name: /storefront/ })
    await expect(storefront).toContainText("linked database")
    await expect(storefront).toContainText("3 sessions")
    await expect(inspector(page).getByRole("link", { name: "Open" })).toHaveAttribute(
      "href",
      "/databases/1",
    )
  })

  test("a diagram is carried by its head and kept under the databases map's own name", async ({
    page,
  }) => {
    const kept = {
      version: 2,
      positions: { "system:project:1/db:1": { x: 10, y: 20 } },
      frames: {},
    }
    const server = await open(page, "/databases/map?database=1", { layout: kept })
    const s = "system:db:1"
    const was = await at(page, `frame:${s}`)
    const grip = (await frame(page, s).locator(".map-frame-grip").boundingBox())!
    await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2)
    await page.mouse.down()
    await page.mouse.move(grip.x + grip.width / 2 + 120, grip.y + grip.height / 2 + 80, {
      steps: 8,
    })
    await page.mouse.up()
    const now = await at(page, `frame:${s}`)
    expect(now.x - was.x).toBeCloseTo(120, 0)

    await expect
      .poll(() => (server.layout() as { boards?: { databases?: { frames: object } } })?.boards)
      .toBeTruthy()
    const layout = server.layout() as {
      positions: object
      boards: { databases: { frames: Record<string, unknown> } }
    }
    expect(Object.keys(layout.boards.databases.frames)).toEqual([s])
    // The server map's own arrangement is where it was.
    expect(layout.positions).toEqual(kept.positions)

    await page.reload()
    await expect(frame(page, s)).toBeVisible()
    await expect(page.getByRole("button", { name: /Tidy up/ })).toBeVisible()
  })

  test("says what it could not read, and when there is nothing to map", async ({ page }) => {
    const server = await mockFleet(page)
    let failing = true
    server.answer("GET /databases/topology", () => {
      if (failing) return refusal(500, "internal", "the map could not be drawn")
    })
    server.answer("GET /map/", () => {
      if (failing) return refusal(500, "internal", "the server map could not be read")
    })
    await page.goto("/databases/map")
    await expect(
      page.getByRole("alert").filter({ hasText: "the map could not be drawn" }),
    ).toBeVisible()
    failing = false
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(page.locator(".react-flow__node").first()).toBeVisible()

    // The topology alone failing still draws every database from the server map.
    server.answer("GET /databases/topology", () =>
      refusal(500, "internal", "the sessions could not be read"),
    )
    await page.reload()
    await expect(frame(page, "system:db:1")).toBeVisible()
    await expect(
      page.getByText(/Sessions and links not read: the sessions could not be read/),
    ).toBeVisible()

    server.answer("GET /databases/topology", () => ({
      body: { nodes: [], edges: [], checkedAt: new Date().toISOString() },
    }))
    server.answer("GET /map/", () => ({
      body: { host: {}, kinds: [], links: [], checkedAt: new Date().toISOString() },
    }))
    await page.reload()
    await expect(page.getByText("Nothing to map yet")).toBeVisible()
    await expect(
      page.locator("[data-slot=server-map]").getByRole("link", { name: "Add a database" }),
    ).toHaveAttribute("href", "/databases/new")
  })

  test.describe("on a phone", () => {
    test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } })

    test("it is one pane at a time: the canvas, the databases and the inspector, picked from a bar at the foot", async ({
      page,
    }) => {
      await mockFleet(page)
      await page.goto("/databases/map?database=1")
      await expect(page.locator(".react-flow__node").first()).toBeVisible()
      const bar = page.locator("[data-slot=pane-bar]")
      await expect(bar.getByRole("tab")).toHaveText(["Databases", "Map", "Inspector"])
      await expect(bar.getByRole("tab", { name: "Map" })).toHaveAttribute("aria-selected", "true")
      // The canvas is the whole width and stands on the bar, which is at the very foot.
      await expect(rail(page)).toHaveCount(0)
      await expect(inspector(page)).toHaveCount(0)
      const canvas = (await page.locator("[data-slot=server-map]").boundingBox())!
      const foot = (await bar.boundingBox())!
      expect(canvas.width).toBe(390)
      expect(Math.round(canvas.y + canvas.height)).toBe(Math.round(foot.y))
      expect(Math.round(foot.y + foot.height)).toBe(844)
      await node(page, "system:db:1/db:1").tap()
      await expect(page.getByRole("link", { name: "Open shop" })).toHaveAttribute(
        "href",
        "/databases/1",
      )

      // The inspector names the links of what was tapped, as it does at a desk.
      await bar.getByRole("tab", { name: "Inspector" }).tap()
      await expect(inspector(page)).toBeVisible()
      await expect(inspector(page)).toContainText("shop")
      expect((await inspector(page).boundingBox())!.width).toBe(390)

      // The rail lists every database by engine; picking one shows it on the canvas.
      await bar.getByRole("tab", { name: "Databases" }).tap()
      await expect(rail(page)).toBeVisible()
      await expect(inspector(page)).toHaveCount(0)
      await expect(rail(page).getByRole("button")).toHaveCount(5)
      await rail(page).locator('[data-system="system:db:4"]').tap()
      await expect(rail(page)).toHaveCount(0)
      await expect(bar.getByRole("tab", { name: "Map" })).toHaveAttribute("aria-selected", "true")
      await expect(frame(page, "system:db:4")).toBeInViewport()
      // Nothing is wider than the phone in any of the three.
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)

      // The server map shares the canvas and did not ask for the bar: it is as it was.
      await page.goto("/map")
      await expect(page.locator(".react-flow__node").first()).toBeVisible()
      await expect(page.locator("[data-slot=pane-bar]")).toHaveCount(0)
      await expect(page.locator("[data-slot=map-pane]")).toHaveCount(0)
    })
  })
})

test.describe("adding a database", () => {
  test("the way in and the engine are in the address, and Back steps out of each", async ({
    page,
  }) => {
    await mockFleet(page)
    await page.goto("/databases")
    await page.getByRole("link", { name: "Add a database" }).last().click()
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Where is the database?")
    const way = (name: string) => page.getByRole("button", { name, exact: true })
    await expect(way("Start a new one here")).toHaveAttribute("aria-pressed", "true")

    await way("Connect to one").click()
    expect(where(page)).toBe("/databases/new?mode=connect")
    await page.getByRole("button", { name: /^Redis/ }).click()
    expect(where(page)).toBe("/databases/new?mode=connect&engine=redis")
    await expect(page.locator("[data-slot=flow-panel]")).toContainText("Redis")
    await page.goBack()
    await expect(page.locator("[data-slot=flow-panel]")).toHaveCount(0)
    await page.goBack()
    await expect(way("Start a new one here")).toHaveAttribute("aria-pressed", "true")

    // A link to one way in opens on it.
    await page.goto("/databases/new?mode=found")
    await expect(way("Found on this server")).toHaveAttribute("aria-pressed", "true")
    await expect(page.getByRole("button", { name: "Connect orders-db" })).toBeVisible()
  })

  // On a phone the three ways in are all in view under their short names: the
  // strip had scrolled with the third cut at the screen's edge and nothing to
  // say it went on.
  test("a phone shows every way in at once", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockFleet(page)
    await page.goto("/databases/new")
    const strip = page.getByRole("group", { name: "Where the database is" })
    const ways = strip.getByRole("button")
    // What is drawn, not what the markup holds: each way carries both names
    // and shows the one for the width.
    await expect(ways).toHaveText(["Start new", "Connect", "Found here"], { useInnerText: true })
    expect(await strip.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true)
    const last = (await ways.last().boundingBox())!
    expect(last.x + last.width).toBeLessThanOrEqual(390)
    await ways.last().click()
    await expect(ways.last()).toHaveAttribute("aria-pressed", "true")
    // At a laptop's width each keeps its whole name.
    await page.setViewportSize({ width: 1280, height: 900 })
    await expect(ways).toHaveText(
      ["Start a new one here", "Connect to one", "Found on this server"],
      { useInnerText: true },
    )
  })

  test("a new server answers on this server only unless the reader says otherwise, and lands on its home", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    let adoptions = 0
    server.answer("POST /databases/adopt", () => {
      adoptions += 1
      // The container is up before the engine inside it answers.
      if (adoptions === 1)
        return refusal(409, "sign_in_failed", "the database system is starting up")
    })
    await page.goto("/databases/new")

    for (const shelf of ["SQL", "Documents", "Key–value", "Analytics"]) {
      await expect(page.getByRole("group", { name: shelf })).toBeVisible()
    }
    await expect(page.getByRole("group", { name: "Analytics" })).toContainText("ClickHouse")
    await expect(page.getByRole("group", { name: "SQL" })).toContainText("TimescaleDB")

    await page.getByRole("button", { name: /^PostgreSQL/ }).click()
    const panel = page.locator("[data-slot=flow-panel]")
    await expect(panel).toContainText("postgres:16-alpine")
    await panel.getByRole("radio", { name: "17" }).click()
    await expect(panel).toContainText("postgres:17-alpine")
    await expect(panel).toContainText("It will answer on this server only.")
    await panel.getByLabel("Name").fill("orders db")
    await expect(panel.getByRole("alert")).toContainText("Letters, digits, dots")
    await expect(panel.getByRole("button", { name: "Create" })).toBeDisabled()
    await panel.getByLabel("Name").fill("orders-db")
    await panel.getByLabel("Database").fill("orders")
    await panel.getByRole("button", { name: "Create" }).click()

    await expect(panel.locator("[data-slot=database-progress]")).toBeVisible()
    await expect(page).toHaveURL(/\/databases\/20$/, { timeout: 15_000 })
    expect(server.bodies("POST /databases/provision")).toEqual([
      {
        engine: "postgres",
        name: "orders-db",
        version: "17",
        database: "orders",
        exposure: "local",
      },
    ])
    expect(server.bodies("POST /databases/adopt")).toEqual([
      { container: "orders-db" },
      { container: "orders-db" },
    ])
  })

  test("publishing a new server is an explicit choice, an engine with no accounts is sent none, and a refusal is shown where it was asked", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    server.answer("POST /databases/provision", () =>
      refusal(409, "container_exists", "a container named jd-valkey already exists"),
    )
    await page.goto("/databases/new?mode=start&engine=valkey")
    const panel = page.locator("[data-slot=flow-panel]")

    await expect(panel.getByLabel("Database")).toHaveCount(0)
    await panel.getByText("Account", { exact: true }).click()
    await expect(panel.getByLabel("User")).toHaveCount(0)
    const publish = panel.getByRole("switch", { name: /^Publish its port/ })
    await expect(publish).not.toBeChecked()
    await publish.click()
    await expect(panel).toContainText("It will answer on every address this server has.")
    await panel.getByRole("button", { name: "Create" }).click()

    await expect(panel).toContainText("It was not started")
    await expect(panel).toContainText("a container named jd-valkey already exists")
    expect(server.bodies("POST /databases/provision")).toEqual([
      { engine: "valkey", version: "8", exposure: "public" },
    ])
    // The answers are still there to change.
    await expect(publish).toBeChecked()
  })

  test("a server with no Docker says so before anything is asked of it", async ({ page }) => {
    await mockFleet(page, { inventory: dockerlessInventory() })
    await page.goto("/databases/new?mode=start&engine=postgres")
    await page.getByRole("heading", { name: "PostgreSQL", exact: true }).waitFor()
    const panel = page.locator("[data-slot=flow-panel]")
    await expect(panel).toContainText("There is no Docker to start it in")
    await expect(panel.getByRole("button", { name: "Create" })).toBeDisabled()
  })

  test("a connection typed by hand is saved only once the string on screen has answered", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    server.answer("POST /databases/test", ({ body }) => {
      if (!String(body.dsn).includes(":right@")) {
        return { body: { ok: false, error: 'password authentication failed for user "app"' } }
      }
    })
    await page.goto("/databases/new?mode=connect&engine=postgres")
    const panel = page.locator("[data-slot=flow-panel]")
    const connect = panel.getByRole("button", { name: "Connect", exact: true })

    await expect(panel.getByRole("button", { name: "Test", exact: true })).toBeDisabled()
    await panel.getByLabel("Host").fill("db.example.com")
    await panel.getByLabel("User").fill("app")
    await panel.getByLabel("Password").fill("wrong")
    await panel.getByLabel("Database", { exact: true }).fill("shop")
    // What will be saved is shown with the password hidden.
    await expect(panel.locator("[data-slot=connection-preview]")).toHaveText(
      "postgres://app:••••••@db.example.com:5432/shop?sslmode=disable",
    )
    await expect(connect).toBeDisabled()
    await panel.getByRole("button", { name: "Test", exact: true }).click()
    await expect(panel).toContainText("It refused the connection")
    await expect(connect).toBeDisabled()

    await panel.getByLabel("Password").fill("right")
    await panel.getByRole("radio", { name: "Verified" }).click()
    await panel.getByRole("button", { name: "Test", exact: true }).click()
    await expect(panel.locator("[data-slot=test-result]")).toHaveText("Answered: PostgreSQL 16.4")
    await expect(connect).toBeEnabled()
    // Changing the address after the test is a string nobody has dialled.
    await panel.getByLabel("Port").fill("6543")
    await expect(connect).toBeDisabled()
    await panel.getByRole("button", { name: "Test", exact: true }).click()
    await expect(connect).toBeEnabled()

    await panel.getByRole("button", { name: "production", exact: true }).click()
    await expect(panel.getByLabel("Environment")).toHaveValue("production")
    await panel.getByRole("switch", { name: /Protect it/ }).click()
    await connect.click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/")).toEqual([
      {
        name: "shop-2",
        driver: "postgres",
        dsn: "postgres://app:right@db.example.com:6543/shop?sslmode=verify-full",
        probe: true,
        environment: "production",
        readOnly: true,
      },
    ])
  })

  test("a string can be pasted whole, a server that is down saved untested, and the server's refusal stays on the form", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    server.answer("POST /databases/", ({ body }) => {
      if (body.name === "taken")
        return refusal(400, "bad_request", "a connection named taken already exists")
    })
    await page.goto("/databases/new?mode=connect&engine=mysql")
    const panel = page.locator("[data-slot=flow-panel]")

    await panel.getByRole("radio", { name: "URL" }).click()
    await panel.getByLabel("Connection string").fill("redis://:pw@cache.example.com:6379/0")
    await expect(panel.getByRole("alert")).toContainText("That is a Redis address.")
    await panel.getByLabel("Connection string").fill("mysql://app:p%40ss@db.example.com:3307/blog")
    await expect(panel.locator("[data-slot=connection-preview]")).toHaveText(
      "app:••••••@tcp(db.example.com:3307)/blog",
    )
    await panel.getByLabel("Name").fill("taken")
    await panel.getByRole("switch", { name: /Save it without testing/ }).click()
    await panel.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(panel).toContainText("It was not saved")
    await expect(panel).toContainText("a connection named taken already exists")
    expect(where(page)).toBe("/databases/new?mode=connect&engine=mysql")

    await panel.getByLabel("Name").fill("blog-remote")
    await panel.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/").at(-1)).toEqual({
      name: "blog-remote",
      driver: "mysql",
      dsn: "app:p@ss@tcp(db.example.com:3307)/blog",
      probe: false,
    })
  })

  test("an environment is a word of the operator's own, held to the server's rule, and fits a phone", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases/new?mode=connect&engine=postgres")
    const panel = page.locator("[data-slot=flow-panel]")
    const connect = panel.getByRole("button", { name: "Connect", exact: true })
    await panel.getByLabel("Host").fill("db.example.com")
    await panel.getByLabel("Database", { exact: true }).fill("shop")
    await panel.getByRole("button", { name: "Test", exact: true }).click()
    await expect(connect).toBeEnabled()

    const field = panel.getByLabel("Environment")
    await field.fill("eu/west")
    await expect(panel.getByRole("alert")).toContainText("Up to 32 letters, digits")
    await expect(connect).toBeDisabled()
    await panel.getByRole("button", { name: "staging", exact: true }).click()
    await expect(field).toHaveValue("staging")
    await expect(panel.getByRole("button", { name: "staging", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    // A second press takes the word away again.
    await panel.getByRole("button", { name: "staging", exact: true }).click()
    await expect(field).toHaveValue("")
    await field.fill("eu-west qa")

    // Nothing of the form runs off its panel at a phone's width.
    await page.setViewportSize({ width: 390, height: 844 })
    expect(
      await panel.evaluate((el) => {
        const edge = el.getBoundingClientRect().right
        return [...el.querySelectorAll("*")].filter(
          (child) => child.getBoundingClientRect().right > edge + 1,
        ).length
      }),
    ).toBe(0)
    await page.setViewportSize({ width: 1280, height: 720 })

    await connect.click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/").at(-1)).toMatchObject({ environment: "eu-west qa" })
  })

  test("a pasted string is shown with every password in it hidden, and follows the reader to the engine it names", async ({
    page,
  }) => {
    await mockFleet(page)
    await page.goto("/databases/new?mode=connect&engine=sqlserver")
    let panel = page.locator("[data-slot=flow-panel]")
    await panel.getByRole("radio", { name: "URL" }).click()
    const preview = panel.locator("[data-slot=connection-preview]")

    await panel
      .getByLabel("Connection string")
      .fill(
        "sqlserver://127.0.0.1:51433?database=shop_a1&user id=sa&password=Sup3rSecret&encrypt=disable",
      )
    await expect(preview).toContainText("password=••••••&encrypt=disable")
    await expect(preview).not.toContainText("Sup3rSecret")
    // The name offered is read off the string, not off the empty fields.
    await expect(panel.getByLabel("Name")).toHaveAttribute("placeholder", "shop_a1")

    await page.goto("/databases/new?mode=connect&engine=postgres")
    panel = page.locator("[data-slot=flow-panel]")
    await panel.getByRole("radio", { name: "URL" }).click()
    await panel
      .getByLabel("Connection string")
      .fill("postgres://jdtest:p@ss:w0rd@127.0.0.1:55432/shop_a1")
    await expect(panel.locator("[data-slot=connection-preview]")).toHaveText(
      "postgres://jdtest:••••••@127.0.0.1:55432/shop_a1",
    )

    // Another engine's address: switching to it takes the string along.
    const pasted = "mysql://jdtest:jdtest@127.0.0.1:53307/inventory_a1"
    await panel.getByLabel("Connection string").fill(pasted)
    await expect(panel.getByRole("alert")).toContainText("That is a MySQL address.")
    await panel.getByRole("button", { name: "Switch engine" }).click()
    expect(where(page)).toBe("/databases/new?mode=connect&engine=mysql")
    panel = page.locator("[data-slot=flow-panel]")
    await expect(panel.getByLabel("Connection string")).toHaveValue(pasted)
    await expect(panel.getByLabel("Name")).toHaveAttribute("placeholder", "inventory_a1")
  })

  test("a number names nothing, and every engine that has a transport says how it is encrypted", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases/new?mode=connect&engine=redis")
    let panel = page.locator("[data-slot=flow-panel]")
    await panel.getByLabel("Host").fill("10.255.255.1")
    await panel.getByLabel("Database index").fill("1")
    await expect(panel.getByLabel("Name")).toHaveAttribute("placeholder", "redis-10.255.255.1")
    await panel.getByLabel("Host").fill("127.0.0.1")
    await expect(panel.getByLabel("Name")).toHaveAttribute("placeholder", "redis")
    await panel.getByRole("radio", { name: "Required" }).click()
    await panel.getByRole("button", { name: "Test", exact: true }).click()
    await expect
      .poll(() => server.bodies("POST /databases/test").at(-1))
      .toEqual({
        driver: "redis",
        dsn: "rediss://127.0.0.1:6379/1?skip_verify=true",
      })

    await page.goto("/databases/new?mode=connect&engine=oracle")
    panel = page.locator("[data-slot=flow-panel]")
    await panel.getByLabel("Host").fill("ora.example.com")
    await panel.getByLabel("Service name").fill("FREEPDB1")
    await expect(
      panel.getByRole("radiogroup", { name: "Encryption" }).getByRole("radio"),
    ).toHaveText(["Off", "Required", "Verified"])
    await panel.getByRole("radio", { name: "Required" }).click()
    await panel.getByRole("button", { name: "Test", exact: true }).click()
    await expect
      .poll(() => server.bodies("POST /databases/test").at(-1))
      .toEqual({
        driver: "oracle",
        dsn: "oracle://ora.example.com:1521/FREEPDB1?SSL=true&SSL%20VERIFY=false",
      })
  })

  test("a file-based engine asks for a path and says where it may be", async ({ page }) => {
    const server = await mockFleet(page)
    await page.goto("/databases/new?mode=connect&engine=sqlite")
    const panel = page.locator("[data-slot=flow-panel]")

    await expect(panel.getByLabel("Host")).toHaveCount(0)
    await expect(panel.getByRole("radio", { name: "URL" })).toHaveCount(0)
    await expect(panel).toContainText("inside /srv or /home")
    await panel.getByLabel("Database file").fill("/srv/app/reports.db")
    await panel.getByRole("button", { name: "Test", exact: true }).click()
    await expect(panel.locator("[data-slot=test-result]")).toBeVisible()
    await panel.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/")).toEqual([
      { name: "reports", driver: "sqlite", dsn: "/srv/app/reports.db", probe: true },
    ])
  })

  test("a found server connected from here lands on its home, and a link that names one opens on its password", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto(
      `/databases/new?mode=found&key=${encodeURIComponent("host:postgresql@17-main.service")}`,
    )
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Connect postgresql@17-main")
    await dialog.getByRole("button", { name: "Cancel" }).click()
    await expect(dialog).toHaveCount(0)
    // The keyboard is left on the server the link was for.
    await expect(page.getByRole("button", { name: "Connect postgresql@17-main" })).toBeFocused()

    await page.getByRole("button", { name: "Connect sessions" }).click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/inventory/connect")).toEqual([{ key: "docker:sessions" }])
  })

  test("every server that states its credentials can be connected in one request", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases/new?mode=found")
    await page.getByRole("button", { name: "Connect all 2" }).click()
    await expect(page.getByText("Connected 2 databases")).toBeVisible()
    expect(server.sent.map((one) => one.call)).toEqual(["POST /databases/sync"])
  })

  test("a role that cannot add one is told so and is sent nowhere", async ({ page }) => {
    const server = await mockFleet(page, { viewer: true })
    await page.goto("/databases/new?mode=found")
    await expect(page.getByText("Adding a database needs an administrator")).toBeVisible()
    expect(server.asked).not.toContain("GET /databases/inventory")
    expect(server.asked).not.toContain("GET /databases/provision/options")
  })
})

/**
 * The design system's structural rules, held on these pages in the states
 * that draw the most: the full control center, its dialogs, the
 * map, and each way of adding a database with its form open.
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

/**
 * Fully rounded, filled labels. A box on the map's canvas is measured at the
 * zoom it is drawn at — a card zoomed out to a quarter is a few pixels high
 * with its corners unchanged — so the canvas's own shapes are not read as
 * chips; the map's chrome around them still is.
 */
function filledPills(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
      if (el.dataset.slot === "user-avatar") continue
      if (el.closest(".react-flow__viewport")) continue
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
      // A glyph that turns has no centre line to be off: its ink is wherever the
      // turn has it at that instant, and under load this caught a spinner mid-turn.
      if (style.animationName !== "none" && style.animationIterationCount === "infinite") continue
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

async function keepsTheRules(page: Page, what: string, register: "reading" | "flow") {
  await page.waitForLoadState("networkidle")
  await expect(page.locator("[data-slot=page]").first()).toBeVisible()
  expect(await unnamedControls(page), `unlabelled icon-only controls on ${what}`).toEqual([])
  expect(await offCentreText(page), `text off its row's centre line on ${what}`).toEqual([])
  expect(await filledPills(page), `fully rounded filled chips on ${what}`).toEqual([])
  const registers = await page
    .locator("[data-slot=page]")
    .evaluateAll((pages) => pages.map((el) => (el as HTMLElement).dataset.register))
  expect(registers, `the register of ${what}`).toEqual([register])
  const panels = await page.locator("[data-slot=flow-panel]").count()
  expect(panels, `flow panels on ${what}`).toBeLessThanOrEqual(register === "flow" ? 1 : 0)
  const sideways = await scroller(page).evaluate((el) => el.scrollWidth > el.clientWidth + 1)
  expect(sideways, `${what} scrolls sideways`).toBe(false)
}

const BUSY = {
  dumps: { 4: null, 5: 400 },
  rows: { 1: { environment: "production", readOnly: true }, 2: { environment: "eu-west staging" } },
  fleet: {
    1: { exposure: "public" },
    2: { ok: false, state: "stopped", consumers: 1 },
    4: {
      ok: false,
      state: "unreachable",
      error: "dial tcp 127.0.0.1:6379: connect: connection refused",
    },
  },
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 900 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the control center, the map and Add a database keep the design system's rules${label}`, async ({
    page,
  }) => {
    test.setTimeout(120_000)
    await page.setViewportSize(viewport)
    await mockFleet(page, BUSY)

    await page.goto("/databases")
    await expect(card(page, "shop")).toBeVisible()
    await keepsTheRules(page, "/databases", "reading")
    await page.getByText("Set aside", { exact: true }).click()
    await keepsTheRules(page, "/databases with what is set aside open", "reading")
    await page.getByRole("button", { name: /^Ignored/ }).click()
    await keepsTheRules(page, "/databases with the ignored ones shown", "reading")

    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await page.getByRole("menuitem", { name: "Forget" }).click()
    await keepsTheRules(page, "the forget confirmation", "reading")
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()
    await page.getByRole("button", { name: "Connect postgresql@17-main" }).click()
    await keepsTheRules(page, "the password form of a found server", "reading")
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()

    // The cards are the one way the fleet is drawn, at every width.
    await expect(page.getByRole("radio", { name: "Table" })).toHaveCount(0)

    await page.goto("/databases/map")
    await expect(page.locator(".react-flow__node").first()).toBeVisible()
    await keepsTheRules(page, "/databases/map", "reading")

    await page.goto("/databases/new")
    await expect(page.getByRole("group", { name: "SQL" })).toBeVisible()
    await keepsTheRules(page, "/databases/new", "flow")
    await page.goto("/databases/new?mode=start&engine=postgres")
    await expect(page.locator("[data-slot=flow-panel]")).toBeVisible()
    await page.locator("[data-slot=flow-panel]").getByText("Account", { exact: true }).click()
    await keepsTheRules(page, "starting a PostgreSQL", "flow")
    await page.goto("/databases/new?mode=connect&engine=mongodb")
    await expect(page.locator("[data-slot=flow-panel]")).toBeVisible()
    await page.locator("[data-slot=flow-panel]").getByLabel("Host").fill("db.example.com")
    await keepsTheRules(page, "connecting a MongoDB", "flow")
    await page.goto("/databases/new?mode=found")
    await expect(page.getByRole("button", { name: "Connect orders-db" })).toBeVisible()
    await keepsTheRules(page, "the found servers", "flow")
  })
}
