import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { hold, mockDatabases, type DatabaseMock } from "./database-fixture"
import { risen } from "./risen"

/**
 * A PostgreSQL database's Pooling page, and the pooled string Connect offers
 * beside the direct one.
 *
 * The claims are the page's to its reader. A database with no pooler says what
 * one is and turns one on with the settings it shows; one with a pooler says
 * whether it is running, where it is reached, what its pools and statistics
 * read, and saves only what changed through the page's bar. Turning it off is
 * confirmed and says what it drops. A role that may only read sees the same
 * figures and no control, a stopped pooler is not asked for statistics, and an
 * engine with no pooler has no page for one.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

const SETTINGS = { poolMode: "transaction", poolSize: 20, maxClientConn: 200, authMode: "userlist" }

const OFF = {
  available: true,
  enabled: false,
  settings: SETTINGS,
  defaults: SETTINGS,
  limits: { poolSize: 500, maxClientConn: 10000 },
  image: "edoburu/pgbouncer:v1.25.2-p0",
  linkedHost: "db-1-pool.jd.internal",
  linkedPort: 6432,
  reference: "${{database.1.pooled}}",
  inSync: false,
}

const ON = {
  ...OFF,
  enabled: true,
  inSync: true,
  state: "running",
  container: "shop-db-pgbouncer",
  network: "jd-pool-1",
  exposure: "local",
  port: 6433,
  createdAt: minutesAgo(190),
}

const POOLS = [
  {
    database: "shop_main",
    user: "app",
    poolMode: "transaction",
    clActive: 14,
    clWaiting: 2,
    svActive: 6,
    svIdle: 3,
    svUsed: 1,
    svTested: 0,
    svLogin: 0,
    maxWaitSec: 1,
  },
  {
    database: "shop_main",
    user: "reports",
    poolMode: "transaction",
    clActive: 1,
    clWaiting: 0,
    svActive: 1,
    svIdle: 2,
    svUsed: 0,
    svTested: 0,
    svLogin: 0,
    maxWaitSec: 0,
  },
]

const STATS = {
  version: "1.25.2",
  at: minutesAgo(0),
  pools: POOLS,
  stats: [
    {
      database: "shop_main",
      totalXactCount: 482_113,
      totalQueryCount: 1_204_551,
      totalReceived: 91_204_113,
      totalSent: 640_113_552,
      totalXactTime: 1_020_000_000,
      totalQueryTime: 980_000_000,
      totalWaitTime: 190_000_000,
      avgXactCount: 38,
      avgQueryCount: 96,
      avgRecv: 12_400,
      avgSent: 88_100,
      avgXactTime: 2100,
      avgQueryTime: 1450,
      avgWaitTime: 380,
    },
  ],
}

const IDLE = { version: "1.25.2", at: minutesAgo(0), pools: [], stats: [] }

type Sent = { method: string; path: string; body: unknown }

type PoolingMock = DatabaseMock & {
  pooler?: Record<string, unknown>
  /** What `GET …/pooler/stats` answers, or a refusal. */
  stats?: unknown | { status: number; body: unknown }
  /** What `PUT …/pooler` answers about the firewall. */
  putExtras?: Record<string, unknown>
}

/**
 * The database fixture and the pooler's four routes, which keep their state:
 * a PUT turns it on or changes it, a DELETE turns it off, and each is
 * recorded with what it carried.
 */
async function mockPooling(page: Page, options: PoolingMock = {}) {
  const mock = await mockDatabases(page, options)
  let pooler: Record<string, unknown> = options.pooler ?? OFF
  const sent: Sent[] = []
  const stats: string[] = []
  await page.route("**/api/v1/databases/1/pooler**", async (route: Route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1\/databases\/1/, "")
    const method = request.method()
    if (path === "/pooler/stats") {
      stats.push(method)
      const answer = options.stats ?? STATS
      if (typeof answer === "object" && answer !== null && "status" in answer && "body" in answer) {
        const reply = answer as { status: number; body: unknown }
        return route.fulfill({ status: reply.status, json: reply.body })
      }
      return route.fulfill({ json: answer })
    }
    if (method === "GET") return route.fulfill({ json: pooler })
    const body = request.postDataJSON?.() ?? undefined
    sent.push({ method, path, body })
    if (method === "PUT") {
      const created = !pooler.enabled
      pooler = { ...ON, ...pooler, enabled: true, state: "running", inSync: true, settings: body }
      return route.fulfill({
        status: created ? 201 : 200,
        json: { pooler, warnings: [], ...options.putExtras },
      })
    }
    pooler = OFF
    return route.fulfill({ json: { removed: "shop-db-pgbouncer", warnings: [] } })
  })
  return {
    ...mock,
    sent,
    stats,
    /** Puts the pooler in another state without a request: what changed on the machine. */
    set: (next: Record<string, unknown>) => {
      pooler = next
    },
  }
}

async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const section = (page: Page, id: string) => page.locator(`section#${id}`)
const saveBar = (page: Page) => page.getByRole("region", { name: "Save changes" })
const dialog = (page: Page) => page.getByRole("dialog")
const PAGE = "/databases/1/settings/pooling"

/** Waits out every animation that ends, so a shot never catches one half drawn. */
async function settle(page: Page) {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
        .map((animation) => animation.finished.catch(() => undefined)),
    ),
  )
}

test("a database with no pooler says what one is and turns it on with the settings shown", async ({
  page,
}) => {
  const server = await mockPooling(page)
  await visit(page, PAGE)

  const pooler = section(page, "pooler")
  await expect(pooler.getByRole("heading", { name: "Connection pooler" })).toBeVisible()
  await expect(pooler).toContainText("Off")
  await expect(pooler).toContainText("shares a few server connections between many clients")
  await expect(section(page, "addresses")).toHaveCount(0)
  await expect(section(page, "pools")).toHaveCount(0)

  // The form starts from what the server says is sensible.
  await expect(pooler.getByRole("radiogroup", { name: "Pool mode" })).toBeVisible()
  await expect(pooler.getByRole("radio", { name: "Transaction" })).toBeChecked()
  await expect(pooler.getByLabel("Pool size")).toHaveValue("20")
  await expect(pooler.getByLabel("Max clients")).toHaveValue("200")
  await expect(pooler.getByRole("radio", { name: "Userlist" })).toBeChecked()

  await pooler.getByRole("radio", { name: "Session" }).click()
  await pooler.getByLabel("Pool size").fill("8")
  await pooler.getByRole("button", { name: "Turn on" }).click()

  await expect
    .poll(() => server.sent[0])
    .toEqual({
      method: "PUT",
      path: "/pooler",
      body: { poolMode: "session", poolSize: 8, maxClientConn: 200, authMode: "userlist" },
    })
  await expect(pooler).toContainText("Running")
  await expect(page.getByText("Pooling is on for shop")).toBeVisible()
  await expect(section(page, "addresses")).toBeVisible()
})

test("numbers out of range hold Turn on and say what is allowed", async ({ page }) => {
  const server = await mockPooling(page)
  await visit(page, PAGE)
  const pooler = section(page, "pooler")
  await pooler.getByLabel("Pool size").fill("900")
  await expect(pooler).toContainText("A whole number from 1 to 500.")
  await expect(pooler.getByRole("button", { name: "Turn on" })).toBeDisabled()
  await pooler.getByLabel("Pool size").fill("20")
  await pooler.getByLabel("Max clients").fill("many")
  await expect(pooler).toContainText("A whole number from 1 to 10000.")
  await expect(pooler.getByRole("button", { name: "Turn on" })).toBeDisabled()
  expect(server.sent).toEqual([])
})

/** The way a connection goes, which carries the pooler's figures. */
const picture = (page: Page) =>
  section(page, "pooler").getByRole("list", { name: "How a connection goes through the pooler" })

test("a running pooler lists where it is reached and carries its figures on the picture", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 1000 })
  await mockPooling(page, { pooler: ON })
  await visit(page, PAGE)

  const pooler = section(page, "pooler")
  await expect(pooler).toContainText("Running")
  await expect(pooler).toContainText("edoburu/pgbouncer:v1.25.2-p0")
  await expect(picture(page)).toContainText("shop-db-pgbouncer")

  // Who is connected and who waits are on the line in; the server's
  // connections on the line out; the wait for one, in words, at the pooler.
  await expect(pooler).toContainText("15 clients")
  await expect(pooler).toContainText("2 waiting")
  await expect(pooler).toContainText("7 active")
  await expect(pooler).toContainText("6 idle")
  await expect(picture(page)).toContainText("PgBouncer 1.25.2")
  const wait = picture(page).locator("[data-slot=pooler-wait]")
  await expect(wait).toContainText("waits 0.4 ms for a connection")
  await expect(wait).toContainText("the longest now 1 s")
  await expect(picture(page).locator("[data-slot=pooler-work]")).toContainText(
    "96 queries · 38 transactions a second",
  )
  await expect(picture(page).locator("[data-slot=pooler-cost]")).toContainText(
    "1.5 ms a query · 1,204,551 queries so far",
  )
  // No heading of figures, and no table in a settings page.
  await expect(section(page, "activity")).toHaveCount(0)
  await expect(page.getByRole("table")).toHaveCount(0)

  const addresses = section(page, "addresses")
  const list = addresses.getByRole("list", { name: "Pooler addresses" })
  await expect(list).toContainText("This server")
  await expect(list).toContainText("127.0.0.1:6433")
  await expect(list).toContainText("db-1-pool.jd.internal:6432")
  await expect(list).toContainText("${{database.1.pooled}}")
  // What the pooled string is, is the caption of the address it is made with.
  await expect(list).toContainText("the direct string with this address")
  await expect(addresses).toContainText("Published on this server only")
  await addresses.getByRole("button", { name: "Why it is published there" }).hover()
  await expect(page.getByRole("tooltip")).toContainText(
    "Opening the database to anywhere under Network moves the pooler with it",
  )

  // Each pool on a line of its own, under the section's count.
  const pools = section(page, "pools")
  await expect(pools).toContainText("2 pools")
  const rows = pools.getByRole("list", { name: "Pools" }).getByRole("listitem")
  await expect(rows).toHaveCount(2)
  await expect(rows.filter({ hasText: "reports" })).toContainText("1 active")
  await expect(rows.filter({ hasText: "app" }).first()).toContainText("2 waiting")
  await expect(rows.filter({ hasText: "app" }).first()).toContainText("6 active, 4 idle")
  await expect(rows.filter({ hasText: "app" }).first()).toContainText("1 s")
  // Inside its hairline, which the last row of a list does not have.
  const heights = await rows.evaluateAll((all) => all.map((row) => row.clientHeight))
  expect(new Set(heights).size, `pool rows: ${heights.join(", ")}`).toBe(1)
})

test("the figures landing move nothing: the picture has its room from the first paint", async ({
  page,
}) => {
  const read = hold()
  await page.setViewportSize({ width: 1280, height: 1000 })
  await mockPooling(page, { pooler: ON })
  await page.route("**/api/v1/databases/1/pooler/stats", async (route) => {
    await read.until
    return route.fallback()
  })
  await visit(page, PAGE)
  await expect(picture(page)).toContainText("shop-db-pgbouncer")
  await risen(page)
  const field = section(page, "pooler").getByRole("radiogroup", { name: "Pool mode" })
  const before = await field.boundingBox()
  read.release()
  await expect(picture(page).locator("[data-slot=pooler-wait]")).toBeVisible()
  await risen(page)
  expect((await field.boundingBox())?.y).toBe(before?.y)
})

test("on a phone the lines have no room for words, so the figures join the captions", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockPooling(page, { pooler: ON })
  await visit(page, PAGE)
  await expect(picture(page).locator("[data-slot=pooler-work]")).toContainText(
    "15 clients, 2 waiting · 96 queries",
  )
  await expect(picture(page).locator("[data-slot=pooler-cost]")).toContainText(
    "7 active, 6 idle · 1.5 ms a query",
  )
  // A pool says its three readings in words under its name: none is lost off the edge.
  const row = section(page, "pools").getByRole("listitem").first()
  await expect(row).toContainText("14 active, 2 waiting clients")
  await expect(row).toContainText("6 active, 4 idle servers")
  await expect(row).toContainText("waited 1 s")
  const box = await row.boundingBox()
  expect(box && box.x >= 0 && box.x + box.width <= 390).toBe(true)
})

test("a pooler nothing has used says so on the picture and lists no pool", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 1000 })
  await mockPooling(page, { pooler: ON, stats: IDLE })
  await visit(page, PAGE)
  await expect(picture(page)).toContainText("nothing has gone through it yet")
  await expect(section(page, "pooler")).toContainText("no client yet")
  await expect(section(page, "pools")).toHaveCount(0)
})

test("statistics that cannot be read say so and can be asked for again", async ({ page }) => {
  await mockPooling(page, {
    pooler: ON,
    stats: {
      status: 502,
      body: {
        error: { code: "pooler_unreachable", message: "the console refused the connection" },
      },
    },
  })
  await visit(page, PAGE)
  const pooler = section(page, "pooler")
  await expect(pooler).toContainText("Could not read the pooler's statistics")
  await expect(pooler).toContainText("the console refused the connection")
  await expect(pooler.getByRole("button", { name: "Try again" })).toBeVisible()
  // The rest of the page is still there, and its form still works.
  await expect(section(page, "addresses")).toBeVisible()
  await expect(pooler.getByLabel("Pool size")).toHaveValue("20")
})

test("a stopped pooler says it starts with its database and is not asked for statistics", async ({
  page,
}) => {
  const server = await mockPooling(page, { pooler: { ...ON, state: "exited" } })
  await visit(page, PAGE)
  await expect(section(page, "pooler")).toContainText("Stopped")
  await expect(section(page, "pooler")).toContainText("starts with its database")
  await expect(section(page, "pools")).toHaveCount(0)
  expect(server.stats).toEqual([])
})

test("changed settings are saved through the page's bar, and only what changed", async ({
  page,
}) => {
  const server = await mockPooling(page, { pooler: ON })
  await visit(page, PAGE)
  const pooler = section(page, "pooler")
  await expect(saveBar(page)).toHaveCount(0)

  await pooler.getByLabel("Pool size").fill("35")
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await pooler.getByRole("radio", { name: "Session" }).click()
  await expect(saveBar(page)).toContainText("2 unsaved changes")

  await pooler.getByLabel("Pool size").fill("0")
  await expect(saveBar(page)).toContainText("A value is out of range")
  await expect(saveBar(page).getByRole("button", { name: "Save", exact: true })).toBeDisabled()
  await pooler.getByLabel("Pool size").fill("35")

  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect
    .poll(() => server.sent[0])
    .toEqual({
      method: "PUT",
      path: "/pooler",
      body: { poolMode: "session", poolSize: 35, maxClientConn: 200, authMode: "userlist" },
    })
  await expect(saveBar(page)).toContainText("Saved")
})

test("changing how clients authenticate warns that it restarts the pooler", async ({ page }) => {
  await mockPooling(page, { pooler: ON })
  await visit(page, PAGE)
  const pooler = section(page, "pooler")
  await expect(page.getByText("restarts the pooler")).toHaveCount(0)
  await pooler.getByRole("radio", { name: "auth_query" }).click()
  await expect(page.getByText("restarts the pooler, which drops the connections")).toBeVisible()
  // What each way of authenticating does is behind the label's ⓘ, not under the field.
  await expect(pooler).not.toContainText("a function this adds")
  await pooler
    .getByText("Authentication", { exact: true })
    .locator("xpath=following-sibling::button")
    .hover()
  await expect(page.getByRole("tooltip")).toContainText("a function this adds")
  await pooler.getByRole("radio", { name: "Userlist" }).click()
  await expect(page.getByText("restarts the pooler, which drops")).toHaveCount(0)
})

test("turning it off is confirmed, says what it drops, and leaves the database", async ({
  page,
}) => {
  const server = await mockPooling(page, { pooler: ON })
  await visit(page, PAGE)
  await section(page, "pooler").getByRole("button", { name: "Turn off" }).click()

  await expect(dialog(page)).toContainText("Turn off pooling for shop")
  await expect(dialog(page)).toContainText("shop-db-pgbouncer")
  await expect(dialog(page)).toContainText("Every connection going through it is dropped")
  await expect(dialog(page)).toContainText("${{database.1.pooled}}")
  await expect(dialog(page)).toContainText("The database, its data and this connection are left")
  await dialog(page).getByRole("button", { name: "Turn off", exact: true }).click()

  await expect
    .poll(() => server.sent[0])
    .toEqual({ method: "DELETE", path: "/pooler", body: undefined })
  await expect(section(page, "pooler")).toContainText("Off")
  await expect(section(page, "activity")).toHaveCount(0)
})

test("a container that is gone can be built again from the page", async ({ page }) => {
  const server = await mockPooling(page, { pooler: { ...ON, state: "missing" } })
  await visit(page, PAGE)
  await expect(section(page, "pooler")).toContainText("Container missing")
  await expect(section(page, "pooler")).toContainText("Something removed shop-db-pgbouncer")
  await section(page, "pooler").getByRole("button", { name: "Build it again" }).click()
  await expect.poll(() => server.sent[0]?.method).toBe("PUT")
})

test("a password changed since the pooler was given it is said, with a way to write it now", async ({
  page,
}) => {
  const server = await mockPooling(page, { pooler: { ...ON, inSync: false } })
  await visit(page, PAGE)
  await expect(section(page, "pooler")).toContainText("The pooler holds an old password")
  await section(page, "pooler").getByRole("button", { name: "Write it now" }).click()
  await expect.poll(() => server.sent[0]?.method).toBe("PUT")
})

test("a pooler the access switch published says so beside its addresses", async ({ page }) => {
  await mockPooling(page, { pooler: { ...ON, exposure: "public" } })
  await visit(page, PAGE)
  const addresses = section(page, "addresses")
  await expect(addresses).toContainText("Published to anywhere")
  await expect(addresses).toContainText("This server and anywhere")
  await addresses.getByRole("button", { name: "Why it is published there" }).hover()
  await expect(page.getByRole("tooltip")).toContainText(
    "Restricting it there brings the pooler back to this server",
  )
})

test("a protected connection shows its pooler and offers no way to change it", async ({ page }) => {
  const server = await mockPooling(page, { pooler: ON, rows: { 1: { readOnly: true } } })
  await visit(page, PAGE)
  const pooler = section(page, "pooler")
  await expect(pooler).toContainText("This connection is protected")
  await expect(pooler).toContainText("the protection is taken off under General")
  await expect(pooler).toContainText("Running")
  await expect(pooler.getByRole("button")).toHaveCount(0)
  await expect(pooler.getByRole("radio")).toHaveCount(0)
  await expect(pooler.getByRole("textbox")).toHaveCount(0)
  await expect(picture(page).locator("[data-slot=pooler-work]")).toContainText("96 queries")
  expect(server.sent).toEqual([])
})

test("a protected connection with no pooler offers no Turn on", async ({ page }) => {
  await mockPooling(page, { rows: { 1: { readOnly: true } } })
  await visit(page, PAGE)
  const pooler = section(page, "pooler")
  await expect(pooler).toContainText("This connection is protected")
  await expect(pooler.getByRole("button", { name: "Turn on" })).toHaveCount(0)
})

test("opening the database to anywhere says the pooler goes with it", async ({ page }) => {
  await mockPooling(page, { pooler: ON })
  await visit(page, "/databases/1/settings/network")
  await page.getByRole("radio", { name: "Anywhere" }).click()
  await saveBar(page).getByRole("button", { name: "Save", exact: true }).click()
  await expect(dialog(page)).toContainText(
    "shop-db-pgbouncer (PgBouncer) is published with it, on port 6433",
  )
})

test("a firewall that could not be changed is reported as a warning, not as success", async ({
  page,
}) => {
  await mockPooling(page, {
    putExtras: { firewall: "failed", firewallError: "ufw refused the rule" },
  })
  await visit(page, PAGE)
  await section(page, "pooler").getByRole("button", { name: "Turn on" }).click()
  await expect(page.getByText("Pooling is on, and the firewall was not changed")).toBeVisible()
  await expect(page.getByText("ufw refused the rule")).toBeVisible()
})

test("a role that may only read sees the figures and no control", async ({ page }) => {
  await mockPooling(page, { viewer: true, pooler: ON })
  await visit(page, PAGE)
  const pooler = section(page, "pooler")
  await expect(pooler).toContainText("Transaction")
  await expect(pooler).toContainText("20 per database and account")
  await expect(pooler).toContainText("administrator")
  await expect(pooler.getByRole("button")).toHaveCount(0)
  await expect(pooler.getByRole("radio")).toHaveCount(0)
  await expect(pooler.getByRole("textbox")).toHaveCount(0)
  await expect(picture(page).locator("[data-slot=pooler-work]")).toContainText("96 queries")
  await expect(section(page, "pools").getByRole("listitem")).toHaveCount(2)
})

test("a role that may not destroy can change the pooler but not turn it off", async ({ page }) => {
  await mockPooling(page, { pooler: ON })
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      json: {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read", "service.control", "system.admin"],
        user: {
          id: 2,
          username: "operator",
          displayName: "Operator",
          avatarVersion: 0,
          role: "admin",
          totpEnabled: true,
          disabled: false,
          mustChangePassword: false,
          lastLoginAt: minutesAgo(5),
          createdAt: minutesAgo(5000),
        },
      },
    }),
  )
  await visit(page, PAGE)
  const pooler = section(page, "pooler")
  await expect(pooler.getByLabel("Pool size")).toBeEditable()
  await expect(pooler.getByRole("button", { name: "Turn off" })).toHaveCount(0)
  await expect(pooler).toContainText("which your role may not do")
})

test("an engine with no pooler has no Pooling page, and its address says so", async ({ page }) => {
  await mockPooling(page)
  await visit(page, "/databases/2/settings")
  await expect(page.getByRole("link", { name: "Pooling", exact: true })).toHaveCount(0)
  await visit(page, "/databases/2/settings/pooling")
  await expect(page.getByText("connection pooling", { exact: false }).first()).toBeVisible()
  await expect(page.getByLabel("Pool size")).toHaveCount(0)
})

test("a PostgreSQL database lists Pooling among its settings pages", async ({ page }) => {
  await mockPooling(page)
  await visit(page, "/databases/1/settings/network")
  await page.getByRole("link", { name: "Pooling", exact: true }).click()
  await expect(page).toHaveURL(/\/databases\/1\/settings\/pooling$/)
  await expect(section(page, "pooler")).toBeVisible()
})

test("a fork the capability table excludes has no Pooling page", async ({ page }) => {
  await mockPooling(page, {
    summaries: { 1: { flavor: "cockroachdb", capabilities: { pooler: false } } },
  })
  await visit(page, "/databases/1/settings/network")
  await expect(page.getByRole("link", { name: "Pooling", exact: true })).toHaveCount(0)
})

test("Connect offers the pooled string beside the direct one and reads it as pooled", async ({
  page,
}) => {
  const urls: string[] = []
  await mockPooling(page, { pooler: ON })
  await page.route("**/api/v1/databases/1/url**", (route) => {
    const url = new URL(route.request().url())
    urls.push(url.search)
    const pooled = url.searchParams.get("pooled") === "1"
    return route.fulfill({
      json: {
        id: 1,
        name: "shop",
        driver: "postgres",
        reference: "",
        url: `postgres://app:s3cret@127.0.0.1:${pooled ? 6433 : 5432}/shop_main?sslmode=disable`,
      },
    })
  })
  await visit(page, "/databases/1")
  await page.getByRole("button", { name: "Connect" }).click()
  const popover = page.locator("[data-slot=connect-popover]")
  await expect(popover).toContainText("127.0.0.1:5432")

  await popover.getByRole("radio", { name: "The pooler" }).click()
  await expect(popover.locator("[data-slot=connection-string]")).toContainText("127.0.0.1:6433")
  await popover.getByRole("button", { name: "Show the password" }).click()
  await expect(popover.locator("[data-slot=connection-string]")).toContainText(
    "s3cret@127.0.0.1:6433",
  )
  expect(urls.at(-1)).toContain("pooled=1")

  await popover.getByRole("radio", { name: "Inside Docker" }).click()
  await expect(popover.locator("[data-slot=connection-string]")).toContainText(
    "db-1-pool.jd.internal:6432",
  )
  await popover.getByRole("radio", { name: "Anywhere" }).click()
  await expect(popover).toContainText("Opening the database to anywhere under Settings")
})

test("Connect offers one string where the database has no pooler", async ({ page }) => {
  await mockPooling(page)
  await visit(page, "/databases/1")
  await page.getByRole("button", { name: "Connect" }).click()
  const popover = page.locator("[data-slot=connect-popover]")
  await expect(popover).toContainText("127.0.0.1:5432")
  await expect(popover.getByRole("radio", { name: "The pooler" })).toHaveCount(0)
})

for (const [label, viewport] of [
  ["a phone", { width: 390, height: 844 }],
  ["a laptop", { width: 1440, height: 900 }],
] as const) {
  test(`the page does not scroll sideways on ${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockPooling(page, { pooler: ON })
    await visit(page, PAGE)
    await expect(section(page, "pools").getByRole("listitem")).toHaveCount(2)
    const sideways = await page.evaluate(() =>
      [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
        .map((el) => el.parentElement ?? el)
        .some((el) => el.scrollWidth > el.clientWidth + 1),
    )
    expect(sideways, `the page scrolls sideways on ${label}`).toBe(false)
    const clipped = await page.evaluate(() =>
      [...document.querySelectorAll<HTMLElement>("[data-slot=page] [data-slot=table-container]")]
        .filter((el) => el.scrollWidth > el.clientWidth + 1)
        .map((el) => `${el.scrollWidth} in ${el.clientWidth}`),
    )
    expect(clipped, `tables wider than ${label}`).toEqual([])
  })
}

test("every state of the Pooling page, for the pull request @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to save the screenshots")
  test.setTimeout(240_000)
  const shots = process.env.JD_SHOTS as string
  const shot = async (name: string, width: number) => {
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    await settle(page)
    await page.screenshot({ path: `${shots}/after-pooling-${name}-${width}.png`, fullPage: true })
  }
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    await page.unrouteAll({ behavior: "ignoreErrors" })
    const server = await mockPooling(page)

    // Off: what it is, and the settings it would start with.
    await visit(page, PAGE)
    await expect(section(page, "pooler")).toContainText("Off")
    await shot("off", width)

    // On, with a busy database behind it.
    await section(page, "pooler").getByRole("button", { name: "Turn on" }).click()
    await expect(section(page, "pools").getByRole("listitem")).toHaveCount(2)
    await page.locator("[data-sonner-toast]").first().waitFor({ state: "detached", timeout: 15000 })
    await shot("full", width)

    // A setting changed and not yet saved.
    const pooler = section(page, "pooler")
    await pooler.getByLabel("Pool size").fill("35")
    await pooler.getByRole("radio", { name: "Session" }).click()
    await expect(saveBar(page)).toContainText("2 unsaved changes")
    await shot("unsaved", width)
    await saveBar(page).getByRole("button", { name: "Discard" }).click()

    // Turning it off asks first.
    await page.getByRole("button", { name: "Turn off" }).click()
    await expect(dialog(page)).toContainText("Turn off pooling for shop")
    await dialog(page).evaluate((el) =>
      Promise.all(
        el
          .getAnimations({ subtree: true })
          .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
          .map((a) => a.finished),
      ),
    )
    await page.screenshot({ path: `${shots}/after-pooling-turn-off-${width}.png` })
    await page.keyboard.press("Escape")

    // Nothing has gone through it; stopped with its database; its figures unreadable.
    server.set({ ...ON, state: "exited" })
    await visit(page, PAGE)
    await expect(section(page, "pooler")).toContainText("Stopped")
    await shot("stopped", width)

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockPooling(page, { pooler: ON, stats: IDLE })
    await visit(page, PAGE)
    await expect(picture(page)).toContainText("nothing has gone through it yet")
    await shot("empty", width)

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockPooling(page, {
      pooler: ON,
      stats: {
        status: 502,
        body: { error: { code: "pooler_unreachable", message: "the console refused" } },
      },
    })
    await visit(page, PAGE)
    await expect(section(page, "pooler")).toContainText("Could not read")
    await shot("failed", width)

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockPooling(page, { viewer: true, pooler: ON })
    await visit(page, PAGE)
    await expect(section(page, "pools").getByRole("listitem")).toHaveCount(2)
    await shot("readonly", width)
  }
})
