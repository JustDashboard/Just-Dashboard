import { expect as baseExpect, test, type Page } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"
import { mockFleet } from "./database-fleet-fixture"

/**
 * Statement statistics on a PostgreSQL server that never loaded the module,
 * and on one the dashboard creates.
 *
 * The claims are the ones the pages make. Where the module is not loaded,
 * the Statements view and the database's home offer one way to turn the
 * statistics on, to the role that may and never on a protected connection:
 * a dialog that shows the statement before it stores anything, asks for the
 * restart as its own step after saying what it does, and creates the
 * extension once the server answers again. A server the dashboard cannot
 * restart is told where it is restarted instead, and nothing is restarted. A
 * refusal is said in the dialog and the step can be tried again. A server
 * started from the add flow counts its statements from its first start, and
 * logs the plans of its slow ones only when asked to.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const NOW = "2026-10-17T09:00:00Z"

type Reply = { status: number; body: unknown }
const refused = (message: string, status = 400, code = "bad_request"): Reply => ({
  status,
  body: { error: { code, message } },
})
const isReply = (value: unknown): value is Reply =>
  typeof value === "object" && value !== null && "status" in value && "body" in value

// ---- what the server answers -----------------------------------------------

const PRELOAD = `ALTER SYSTEM SET "shared_preload_libraries" = 'auto_explain', 'pg_stat_statements'`

const notCounting = (enable: Record<string, unknown>) => ({
  supported: false,
  reason: "pg_stat_statements is not enabled in this database",
  sort: "total",
  resettable: false,
  totalMs: 0,
  statements: [],
  enable: { extension: "pg_stat_statements", available: true, preloaded: false, ...enable },
})

/** Never loaded: the library list does not name it. */
const UNLOADED = notCounting({
  configured: false,
  preload: PRELOAD,
  note: "Add pg_stat_statements to shared_preload_libraries and restart the server first; the extension only records once it is loaded at start.",
})
/** Stored, and the server has not restarted since. */
const CONFIGURED = notCounting({
  configured: true,
  note: "pg_stat_statements is in shared_preload_libraries and is loaded when the server next starts; restart it, then create the extension.",
})
/** Loaded: one statement is what is left. */
const LOADED = notCounting({
  preloaded: true,
  sql: "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;",
})
/** The list is on the server's command line, which outranks anything stored. */
const BLOCKED = notCounting({
  configured: false,
  note: "Add pg_stat_statements to shared_preload_libraries and restart the server first; the extension only records once it is loaded at start.",
  blocked:
    "shared_preload_libraries is set on the command line this server is started with, which outranks anything stored from here; add pg_stat_statements there.",
})

/** Counting, and nothing counted yet. */
const COUNTING = { supported: true, sort: "total", resettable: true, totalMs: 0, statements: [] }

const statement = (id: string, query: string, share: number, calls: number, meanMs: number) => ({
  id,
  query,
  calls,
  totalMs: share * 9_400,
  meanMs,
  maxMs: meanMs * 6,
  rows: calls * 3,
  hitRatio: 0.99,
  share,
})
/** A few minutes later, for the picture of the view it ends on. */
const COUNTED = {
  supported: true,
  sort: "total",
  resettable: true,
  totalMs: 9_400,
  since: NOW,
  statements: [
    statement(
      "1",
      "SELECT * FROM orders WHERE customer_id = $1 ORDER BY placed_at DESC",
      0.46,
      8_210,
      0.53,
    ),
    statement(
      "2",
      "UPDATE carts SET total = $1, updated_at = now() WHERE id = $2",
      0.27,
      3_904,
      0.65,
    ),
    statement(
      "3",
      "INSERT INTO order_items (order_id, sku, quantity) VALUES ($1, $2, $3)",
      0.15,
      2_118,
      0.67,
    ),
    statement("4", "SELECT count(*) FROM order_items WHERE order_id = $1", 0.12, 1_502, 0.75),
  ],
}

const PRELOADED_CHANGE = {
  name: "shared_preload_libraries",
  value: "auto_explain,pg_stat_statements",
  statements: [PRELOAD, "SELECT pg_reload_conf()"],
  persisted: true,
  restartRequired: true,
  note: "Stored in postgresql.auto.conf; it takes effect when the server is next restarted.",
}

const STATS = {
  supported: true,
  at: NOW,
  driver: "postgres",
  version: "PostgreSQL 16.4",
  uptimeSeconds: 46_000,
  role: "standalone",
  database: "shop_main",
  databaseBytes: 24_517_655,
  connections: { total: 6, active: 1, idle: 5, idleInTransaction: 0, waiting: 0, max: 100 },
  counters: {
    transactionsCommitted: 1_000,
    transactionsRolledBack: 0,
    blocksHit: 900_000,
    blocksRead: 2_700,
    rowsRead: 400_000,
    rowsWritten: 90_000,
    deadlocks: 0,
  },
  gauges: { longestQuerySeconds: 0, oldestTransactionSeconds: 0, replicas: 0 },
  pool: {
    open: 1,
    inUse: 0,
    idle: 1,
    waitCount: 0,
    waitDuration: "0s",
    maxOpen: 5,
    maxIdleClosed: 0,
    maxLifetimeClosed: 0,
  },
}

/** The shop database runs in a container the dashboard can restart. */
const IN_DOCKER = {
  container: {
    id: "c0ffee",
    name: "shop-db",
    image: "postgres:16-alpine",
    state: "running",
    status: "Up 3 days",
  },
}
/** And on another machine, where it is restarted there. */
const ELSEWHERE = {
  source: "remote",
  host: "db.internal",
  power: {
    via: "",
    start: false,
    stop: false,
    restart: false,
    reason: "The server is on another machine (db.internal) and is started and stopped there.",
  },
}

type Answer = unknown | ((body: unknown) => unknown)
type Sent = { request: string; body: unknown }

/**
 * The section's fixture with one database's operations answered over it.
 * `sent` is every request that was not a read, in order, with its body.
 */
async function mockSetup(page: Page, answers: Record<string, Answer>, options: DatabaseMock = {}) {
  await mockDatabases(page, { ...options, summaries: { 1: IN_DOCKER, ...options.summaries } })
  const sent: Sent[] = []
  const all: Record<string, Answer> = {
    "GET 1/stats": STATS,
    "GET 1/activity": { supported: true, sessions: [] },
    ...answers,
  }
  await page.route("**/api/v1/databases/*/**", async (route) => {
    const url = new URL(route.request().url())
    const match = /^\/api\/v1\/databases\/(\d+)(\/.+)$/.exec(url.pathname)
    if (!match) return route.fallback()
    const method = route.request().method()
    const key = `${method} ${match[1]}${match[2]}`
    if (!Object.hasOwn(all, key)) return route.fallback()
    const body = method === "GET" ? undefined : route.request().postDataJSON()
    if (method !== "GET") sent.push({ request: key, body })
    const answer = all[key]
    const value = await (typeof answer === "function"
      ? (answer as (body: unknown) => unknown)(body)
      : answer)
    if (isReply(value)) return route.fulfill({ status: value.status, json: value.body })
    return route.fulfill({ json: value }).catch(() => undefined)
  })
  return { sent }
}

/**
 * The server as the setup moves it along: the statements route answers by
 * where the server is, and each step's route moves it to the next.
 */
function serverThatSetsUp(start: "unloaded" | "configured" = "unloaded") {
  const at = { state: start as "unloaded" | "configured" | "loaded" | "counting" }
  const reading = { unloaded: UNLOADED, configured: CONFIGURED, loaded: LOADED, counting: COUNTING }
  return {
    at,
    answers: {
      "GET 1/statements": () => reading[at.state],
      "POST 1/statements/preload": () => {
        at.state = "configured"
        return PRELOADED_CHANGE
      },
      "POST 1/power": () => {
        at.state = "loaded"
        return { action: "restart", via: "docker", target: "shop-db", state: "running" }
      },
      "POST 1/server/extensions": () => {
        at.state = "counting"
        return { ok: true }
      },
    } as Record<string, Answer>,
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

const STATEMENTS_VIEW = "/databases/1/performance?view=statements"
const region = (page: Page, name: string) => page.getByRole("region", { name, exact: true })
const setup = (page: Page) => page.getByRole("dialog", { name: "Turn on statement statistics" })
const stage = (page: Page, name: string) =>
  setup(page).getByRole("list", { name: "Stages" }).getByRole("listitem").filter({ hasText: name })

// ---------------------------------------------------------------------------

test("a server that never loaded the module is set up in three steps, each one asked for", async ({
  page,
}) => {
  const server = serverThatSetsUp()
  const ops = await mockSetup(page, server.answers)
  await visit(page, STATEMENTS_VIEW)

  const statements = region(page, "Statements")
  await expect(statements).toContainText("This server is not counting its statements")
  await expect(statements).toContainText("Add pg_stat_statements to shared_preload_libraries")
  await statements.getByRole("button", { name: "Turn on statement statistics" }).click()

  // The plan, and the one statement the first step runs, before anything runs.
  const dialog = setup(page)
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole("list", { name: "Stages" }).getByRole("listitem")).toHaveText([
    /Load the module at start/,
    /Restart the server/,
    /Create the extension/,
  ])
  await expect(dialog).toContainText(PRELOAD)
  await expect(dialog).toContainText("Nothing restarts yet.")
  await expect(dialog).toContainText("shop-db")
  expect(ops.sent).toEqual([])

  await dialog.getByRole("button", { name: "Add pg_stat_statements" }).click()
  // Stored; the restart is a question of its own, with what it does to the
  // sessions on the server.
  await expect(dialog.getByText("Restart shop to load it")).toBeVisible()
  await expect(dialog).toContainText("Every session on it is disconnected")
  await expect(stage(page, "Load the module at start")).toContainText("Done")
  expect(ops.sent.map((sent) => sent.request)).toEqual(["POST 1/statements/preload"])

  await dialog.getByRole("button", { name: "Restart and turn on" }).click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText("Statement statistics are on")).toBeVisible()
  await expect(statements).toContainText("No statement has been counted yet.")
  expect(ops.sent).toEqual([
    { request: "POST 1/statements/preload", body: {} },
    { request: "POST 1/power", body: { action: "restart" } },
    { request: "POST 1/server/extensions", body: { name: "pg_stat_statements" } },
  ])
})

test("a setup left before the restart resumes at the restart, and Not now changes nothing", async ({
  page,
}) => {
  const server = serverThatSetsUp("configured")
  const ops = await mockSetup(page, server.answers)
  await visit(page, STATEMENTS_VIEW)

  const statements = region(page, "Statements")
  await expect(statements).toContainText("is loaded when the server next starts")
  await statements.getByRole("button", { name: "Turn on statement statistics" }).click()
  const dialog = setup(page)
  await expect(stage(page, "Load the module at start")).toContainText("Done")
  await expect(dialog).not.toContainText(PRELOAD)
  await expect(dialog.getByRole("button", { name: "Restart and turn on" })).toBeVisible()

  await dialog.getByRole("button", { name: "Not now" }).click()
  await expect(dialog).toHaveCount(0)
  // The keyboard is back on the control it was pressed from.
  await expect(
    statements.getByRole("button", { name: "Turn on statement statistics" }),
  ).toBeFocused()
  expect(ops.sent).toEqual([])
})

test("a server the dashboard cannot restart is told where it is restarted, and nothing restarts", async ({
  page,
}) => {
  const server = serverThatSetsUp()
  const ops = await mockSetup(page, server.answers, { summaries: { 1: ELSEWHERE } })
  await visit(page, STATEMENTS_VIEW)

  await region(page, "Statements")
    .getByRole("button", { name: "Turn on statement statistics" })
    .click()
  const dialog = setup(page)
  await expect(dialog.getByText("The restart is yours to do")).toBeVisible()
  await expect(dialog).toContainText("The server is on another machine (db.internal)")

  await dialog.getByRole("button", { name: "Add pg_stat_statements" }).click()
  await expect(dialog.getByText("Restart it where it runs")).toBeVisible()
  await expect(dialog.getByRole("button", { name: /Restart/ })).toHaveCount(0)
  // The footer's Close; the dialog's own close mark comes after it.
  await dialog.getByRole("button", { name: "Close" }).first().click()
  await expect(dialog).toHaveCount(0)
  expect(ops.sent.map((sent) => sent.request)).toEqual(["POST 1/statements/preload"])
})

test("a refusal is said in the dialog, on the step it stopped at, and can be tried again", async ({
  page,
}) => {
  const server = serverThatSetsUp()
  let refuse = true
  const ops = await mockSetup(page, {
    ...server.answers,
    "POST 1/statements/preload": () => {
      if (refuse) {
        return refused(
          "Only a superuser can change shared_preload_libraries, and the account this connection signs in with is not one.",
        )
      }
      server.at.state = "configured"
      return PRELOADED_CHANGE
    },
    "POST 1/power": refused(
      "the container shop-db is not running, so there is nothing to restart. Start it instead.",
      409,
      "power_unavailable",
    ),
  })
  await visit(page, STATEMENTS_VIEW)
  await region(page, "Statements")
    .getByRole("button", { name: "Turn on statement statistics" })
    .click()
  const dialog = setup(page)

  await dialog.getByRole("button", { name: "Add pg_stat_statements" }).click()
  await expect(dialog.getByRole("alert")).toContainText("Only a superuser can change")
  await expect(stage(page, "Load the module at start")).toContainText("Failed")
  refuse = false
  await dialog.getByRole("button", { name: "Try again" }).click()
  await expect(dialog.getByRole("button", { name: "Restart and turn on" })).toBeVisible()
  await expect(dialog.getByRole("alert")).toHaveCount(0)

  await dialog.getByRole("button", { name: "Restart and turn on" }).click()
  await expect(dialog.getByRole("alert")).toContainText("nothing to restart")
  await expect(stage(page, "Restart the server")).toContainText("Failed")
  await expect(dialog.getByRole("button", { name: "Restart again" })).toBeVisible()
  expect(ops.sent.map((sent) => sent.request)).toEqual([
    "POST 1/statements/preload",
    "POST 1/statements/preload",
    "POST 1/power",
  ])
})

test("a library list set on the command line is explained, and no setup is offered", async ({
  page,
}) => {
  await mockSetup(page, { "GET 1/statements": BLOCKED })
  await visit(page, STATEMENTS_VIEW)
  const statements = region(page, "Statements")
  await expect(statements).toContainText("is set on the command line this server is started with")
  await expect(
    statements.getByRole("button", { name: "Turn on statement statistics" }),
  ).toHaveCount(0)
})

for (const [who, options] of [
  ["a role that does not administer", { viewer: true }],
  ["a protected connection", { rows: { 1: { readOnly: true } } }],
] as const) {
  test(`${who} is told what turns the statistics on and offered no setup`, async ({ page }) => {
    const ops = await mockSetup(page, { "GET 1/statements": UNLOADED }, options)
    await visit(page, STATEMENTS_VIEW)
    const statements = region(page, "Statements")
    await expect(statements).toContainText("Add pg_stat_statements to shared_preload_libraries")
    await expect(
      statements.getByRole("button", { name: "Turn on statement statistics" }),
    ).toHaveCount(0)
    expect(ops.sent).toEqual([])
  })
}

test("the database's home offers the same setup in its busiest-statements block", async ({
  page,
}) => {
  const server = serverThatSetsUp()
  const ops = await mockSetup(page, server.answers)
  await visit(page, "/databases/1")
  const busiest = region(page, "Busiest statements")
  await expect(busiest).toContainText("This server is not counting its statements.")
  await busiest.getByRole("button", { name: "Turn on statement statistics" }).click()
  const dialog = setup(page)
  await dialog.getByRole("button", { name: "Add pg_stat_statements" }).click()
  await dialog.getByRole("button", { name: "Restart and turn on" }).click()
  await expect(dialog).toHaveCount(0)
  await expect(busiest).toContainText("No statement has been counted yet.")
  expect(ops.sent.map((sent) => sent.request)).toEqual([
    "POST 1/statements/preload",
    "POST 1/power",
    "POST 1/server/extensions",
  ])
})

test("the setup fits a phone, without a sideways scroll", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const server = serverThatSetsUp()
  await mockSetup(page, server.answers)
  await visit(page, STATEMENTS_VIEW)
  await region(page, "Statements")
    .getByRole("button", { name: "Turn on statement statistics" })
    .click()
  const dialog = setup(page)
  await expect(dialog.getByRole("button", { name: "Add pg_stat_statements" })).toBeInViewport()
  const wide = () =>
    page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
  expect(await wide()).toBe(false)
  await dialog.getByRole("button", { name: "Add pg_stat_statements" }).click()
  await expect(dialog.getByRole("button", { name: "Restart and turn on" })).toBeInViewport()
  expect(await wide()).toBe(false)
})

// ---- a server started from the add flow ------------------------------------

test("a PostgreSQL server is created counting its statements, and logs slow plans only when asked", async ({
  page,
}) => {
  await mockFleet(page)
  const requests: unknown[] = []
  // Held: what was sent is the claim, not what happens after it.
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/databases/provision", async (route) => {
    requests.push(route.request().postDataJSON())
    await held
    await route.abort().catch(() => undefined)
  })
  await page.goto("/databases/new")
  await page.getByRole("button", { name: /^PostgreSQL/ }).click()
  const panel = page.locator("[data-slot=flow-panel]")
  const plans = panel.getByRole("switch", { name: /plan of every statement/ })
  await expect(plans).not.toBeChecked()
  await expect(panel).toContainText("It counts its statements from the first start")
  await plans.click()
  await panel.getByRole("button", { name: "Create", exact: true }).click()
  await expect
    .poll(() => requests)
    .toEqual([{ engine: "postgres", version: "16", exposure: "local", autoExplain: true }])

  // An engine with no plans to log is offered neither.
  await page.goto("/databases/new")
  await page.getByRole("button", { name: /^MariaDB/ }).click()
  await expect(panel.getByRole("switch", { name: /plan of every statement/ })).toHaveCount(0)
  await expect(panel).not.toContainText("counts its statements from the first start")
  release()
})

// ---- pictures --------------------------------------------------------------

test("the states of the setup, for the record @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go to")
  const shots = process.env.JD_SHOTS
  await page.setViewportSize({ width: 1440, height: 900 })

  // The view before, the plan, and the restart asked for.
  let server = serverThatSetsUp()
  let release = () => {}
  const restarted = new Promise<void>((resolve) => (release = resolve))
  await mockSetup(page, {
    ...server.answers,
    "POST 1/power": async () => {
      await restarted
      server.at.state = "loaded"
      return { action: "restart", via: "docker", target: "shop-db", state: "running" }
    },
    "POST 1/server/extensions": () => {
      server.at.state = "counting"
      return { ok: true }
    },
  })
  await visit(page, STATEMENTS_VIEW)
  const statements = region(page, "Statements")
  await expect(
    statements.getByRole("button", { name: "Turn on statement statistics" }),
  ).toBeVisible()
  await page.screenshot({ path: `${shots}/statements-off-1440.png` })

  await statements.getByRole("button", { name: "Turn on statement statistics" }).click()
  const dialog = setup(page)
  await expect(dialog).toContainText(PRELOAD)
  await page.screenshot({ path: `${shots}/setup-preload-1440.png` })

  await dialog.getByRole("button", { name: "Add pg_stat_statements" }).click()
  await expect(dialog.getByRole("button", { name: "Restart and turn on" })).toBeVisible()
  await page.screenshot({ path: `${shots}/setup-restart-1440.png` })

  await dialog.getByRole("button", { name: "Restart and turn on" }).click()
  await expect(dialog.getByRole("status")).toContainText("Restarting shop")
  await page.screenshot({ path: `${shots}/setup-restarting-1440.png` })
  release()
  await expect(dialog).toHaveCount(0)
  await expect(statements).toContainText("No statement has been counted yet.")

  // A few minutes on, the list it opens on.
  await page.unrouteAll({ behavior: "ignoreErrors" })
  await mockSetup(page, { "GET 1/statements": COUNTED })
  await visit(page, STATEMENTS_VIEW)
  await expect(statements).toContainText("SELECT * FROM orders WHERE customer_id = $1")
  await page.screenshot({ path: `${shots}/setup-done-1440.png` })

  // A server the dashboard cannot restart.
  await page.unrouteAll({ behavior: "ignoreErrors" })
  server = serverThatSetsUp()
  await mockSetup(page, server.answers, { summaries: { 1: ELSEWHERE } })
  await visit(page, STATEMENTS_VIEW)
  await statements.getByRole("button", { name: "Turn on statement statistics" }).click()
  await expect(dialog.getByText("The restart is yours to do")).toBeVisible()
  await page.screenshot({ path: `${shots}/setup-elsewhere-1440.png` })

  // The add flow's PostgreSQL settings, with slow plans chosen.
  await page.unrouteAll({ behavior: "ignoreErrors" })
  await mockFleet(page)
  await page.goto("/databases/new")
  await page.getByRole("button", { name: /^PostgreSQL/ }).click()
  const panel = page.locator("[data-slot=flow-panel]")
  await panel.getByRole("switch", { name: /plan of every statement/ }).click()
  await expect(panel.getByRole("switch", { name: /plan of every statement/ })).toBeChecked()
  await page.screenshot({ path: `${shots}/create-postgres-1440.png` })

  // The plan on a phone.
  await page.unrouteAll({ behavior: "ignoreErrors" })
  await page.setViewportSize({ width: 390, height: 844 })
  server = serverThatSetsUp()
  await mockSetup(page, server.answers)
  await visit(page, STATEMENTS_VIEW)
  await statements.getByRole("button", { name: "Turn on statement statistics" }).click()
  await expect(dialog).toContainText(PRELOAD)
  await page.screenshot({ path: `${shots}/setup-preload-390.png` })
})
