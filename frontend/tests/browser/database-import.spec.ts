import { expect as baseExpect, test, type Page } from "@playwright/test"
import { CONNECTIONS, SHOP, mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A database brought over from another server: the Import page, and the copy
 * to another saved connection that the Backups page gained beside it.
 *
 * The claims are the ones the page makes to its reader. It asks one question at
 * a time and draws the sequence it is in, from the state the server keeps. The
 * source's address is typed in the tab and never drawn back: the password is a
 * password field, the preview is masked, and no answer repeats the string. What
 * stands in the way of a replication — a wal_level, a table with no key — is
 * said before the command, and the command waits for it. A replication shows
 * what each table has done and how far behind the target is, and the cutover is
 * offered only once every table is in, behind a statement that the writes have
 * stopped. A role that may only read sees all of it and can change none of it,
 * and a protected connection takes no import at all.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()
const PASSWORD = "s3cret-pass"

type Json = Record<string, unknown>

const record = (over: Json = {}): Json => ({
  id: 7,
  name: "jd_import_shop",
  mode: "replication",
  status: "replicating",
  database: "shop_copy",
  source: { address: "db.example.com:5432", user: "reader", database: "shop" },
  structureOnly: false,
  publicationMade: true,
  startedBy: "operator",
  createdAt: minutesAgo(95),
  updatedAt: minutesAgo(1),
  ...over,
})

const table = (name: string, state: string, rowsCopied = -1) => ({
  schema: "public",
  name,
  state,
  rowsCopied,
})

const subscription = (tables: ReturnType<typeof table>[], over: Json = {}) => ({
  exists: true,
  enabled: true,
  applying: true,
  errors: 0,
  ready: tables.filter((one) => one.state === "ready").length,
  tables,
  ...over,
})

const COPYING = [
  table("customers", "ready"),
  table("orders", "copying", 1_204_000),
  table("order_items", "waiting"),
  table("audit_log", "ready"),
  table("sessions", "synchronizing"),
]
const COPIED = [
  table("customers", "ready"),
  table("orders", "ready"),
  table("order_items", "ready"),
  table("audit_log", "ready"),
  table("sessions", "ready"),
]

const copying = (over: Json = {}) => ({
  import: record(),
  subscription: subscription(COPYING),
  lag: { exists: true, active: true, bytes: 2_516_582 },
  ...over,
})
const caughtUp = (over: Json = {}) => ({
  import: record(),
  subscription: subscription(COPIED),
  lag: { exists: true, active: true, bytes: 0 },
  ...over,
})

const inspection = (over: Json = {}) => ({
  reachable: true,
  forms: ["replication", "stream"],
  source: {
    address: "db.example.com:5432",
    user: "reader",
    database: "shop",
    version: "PostgreSQL 16.4",
    flavor: "postgres",
    flavorLabel: "PostgreSQL",
  },
  name: "jd_import_shop",
  ready: true,
  blockers: [],
  warnings: [],
  replicationReady: true,
  replication: {
    version: "PostgreSQL 16.4",
    versionNumber: 160004,
    database: "shop",
    walLevel: "logical",
    tables: [
      {
        schema: "public",
        name: "orders",
        estimatedRows: 2_400_000,
        size: 640_000_000,
        identity: "key",
      },
      {
        schema: "public",
        name: "customers",
        estimatedRows: 180_000,
        size: 42_000_000,
        identity: "key",
      },
      { schema: "public", name: "audit_log", estimatedRows: -1, size: 9_000_000, identity: "full" },
    ],
    sequences: 3,
    blockers: [],
    warnings: [
      "3 sequence(s) are not replicated; the cutover sets them to the source's positions.",
    ],
  },
  publicationSql:
    'CREATE PUBLICATION "jd_import_shop" FOR TABLE "public"."orders", "public"."customers", "public"."audit_log";',
  ...over,
})

const job = (id: string, kind: string, status: string, over: Json = {}) => ({
  id,
  kind: `database.transfer.1.${kind}`,
  title: kind === "import-cutover" ? "Cut over shop_copy" : "Import shop_copy",
  target: "shop",
  status,
  exitCode: 0,
  startedAt: minutesAgo(1),
  startedBy: "operator",
  lines: 2,
  ...over,
})
const line = (seq: number, stream: string, text: string) => ({
  seq,
  stream,
  text,
  at: minutesAgo(1),
})

type Mock = DatabaseMock & {
  imports?: Json[]
  /** What `GET /imports/{n}` answers, by id. */
  live?: Record<number, unknown>
  inspected?: unknown
  /** What `POST /imports` answers. */
  started?: Json
  jobs?: Record<string, unknown>
  /** A second PostgreSQL server the copy can go to. */
  replica?: boolean
  reads?: { list?: { status: number; body: unknown } }
}

type Sent = { request: string; body: unknown }

async function mockImports(page: Page, options: Mock = {}) {
  await mockDatabases(page, options)
  const sent: Sent[] = []
  const imports = options.imports ?? []
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    const answer = (body: unknown, status = 200) => route.fulfill({ status, json: body })
    const write = () => {
      let body: unknown
      try {
        body = route.request().postDataJSON()
      } catch {
        body = undefined
      }
      sent.push({ request: `${method} ${path}`, body })
    }

    if (path === "/databases/" && options.replica) {
      return answer([
        ...CONNECTIONS,
        {
          ...SHOP,
          id: 8,
          name: "shop-replica",
          host: "10.0.0.8",
          database: "replica_main",
        },
      ])
    }
    if (path === "/databases/1/imports" && method === "GET") {
      return options.reads?.list
        ? answer(options.reads.list.body, options.reads.list.status)
        : answer({ imports })
    }
    if (path === "/databases/1/imports/inspect") {
      write()
      return answer(options.inspected ?? inspection())
    }
    if (path === "/databases/1/imports" && method === "POST") {
      write()
      const started = options.started ?? {
        import: record({ status: "running" }),
        job: job("j7", "import", "running"),
      }
      imports.push(started.import as Json)
      return answer(started, 202)
    }
    const one = /^\/databases\/1\/imports\/(\d+)(\/cutover)?$/.exec(path)
    if (one) {
      const id = Number(one[1])
      if (method === "GET") {
        const found = options.live?.[id] ?? { import: imports.find((entry) => entry.id === id) }
        return answer(found)
      }
      write()
      if (one[2]) return answer(job("j8", "import-cutover", "running"), 202)
      return answer({ warnings: [] })
    }
    const read = /^\/jobs\/(\w+)$/.exec(path)
    if (read && method === "GET") {
      return answer(
        options.jobs?.[read[1]] ?? { job: job(read[1], "import", "running"), lines: [] },
      )
    }
    if (path === "/databases/1/server/databases/connect") {
      write()
      return answer({ ...SHOP, id: 9, name: "shop_copy", database: "shop_copy" })
    }
    if (path === "/databases/1/copy") {
      write()
      return answer(job("j9", "copy", "running", { title: "Copy shop to shop-replica" }), 202)
    }
    if (path === "/databases/1/backups") {
      return answer({
        dir: "/var/backups/jd/databases/shop",
        files: [],
        options: {
          schemaOnly: true,
          dataOnly: true,
          tables: true,
          compression: true,
          databases: false,
          newDatabase: true,
        },
      })
    }
    return route.fallback()
  })
  return { sent }
}

async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const plan = (page: Page) => page.getByRole("region", { name: "Plan" })
const question = (page: Page, name: string | RegExp) =>
  page.getByRole("heading", { level: 1, name })
const noSideways = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)

async function typeSource(page: Page) {
  await page.getByLabel("Host").fill("db.example.com")
  await page.getByLabel("User").fill("reader")
  await page.getByLabel("Password").fill(PASSWORD)
  await page.getByLabel("Database", { exact: true }).fill("shop")
}

// ---------------------------------------------------------------------------
// The source
// ---------------------------------------------------------------------------

test("the page opens on a question, in the flow register, with the sequence it is in", async ({
  page,
}) => {
  await mockImports(page)
  await visit(page, "/databases/1/import")
  await expect(question(page, "Where is the database coming from?")).toBeVisible()
  await expect(page.locator("[data-slot=page]")).toHaveAttribute("data-register", "flow")
  await expect(page.getByRole("list", { name: "Progress" }).getByRole("listitem")).toHaveText([
    "Source",
    "Plan",
    "Copy",
    "Cutover",
  ])
  // Nothing is sent until the source has been typed and asked for.
  await expect(page.getByRole("button", { name: "Check the source" })).toBeDisabled()
  await expect(
    page.getByRole("list", { name: "Progress" }).getByRole("listitem").first(),
  ).toHaveAttribute("aria-current", "step")
  expect(await noSideways(page)).toBe(true)
})

test("the source's password is a password field and is never drawn back", async ({ page }) => {
  const { sent } = await mockImports(page)
  await visit(page, "/databases/1/import")
  await typeSource(page)
  await expect(page.getByLabel("Password")).toHaveAttribute("type", "password")
  // The preview is the string with its password hidden.
  await expect(page.locator("[data-slot=connection-preview]")).toContainText("db.example.com")
  await expect(page.locator("[data-slot=connection-preview]")).not.toContainText(PASSWORD)

  await page.getByRole("button", { name: "Check the source" }).click()
  await expect(plan(page)).toBeVisible()
  const asked = sent.find((one) => one.request === "POST /databases/1/imports/inspect")
  expect((asked?.body as { source: string }).source).toContain(PASSWORD)
  await expect(page.locator("body")).not.toContainText(PASSWORD)
})

test("a source that does not answer is said beside the fields that caused it", async ({ page }) => {
  await mockImports(page, {
    inspected: { ...inspection(), reachable: false, error: "connection refused", forms: [] },
  })
  await visit(page, "/databases/1/import")
  await typeSource(page)
  await page.getByRole("button", { name: "Check the source" }).click()
  await expect(page.getByText("The source could not be read")).toBeVisible()
  await expect(page.getByText("connection refused")).toBeVisible()
  // Still on the first step, with what was typed.
  await expect(page.getByLabel("Host")).toHaveValue("db.example.com")
  await expect(plan(page)).toHaveCount(0)
})

// ---------------------------------------------------------------------------
// The plan
// ---------------------------------------------------------------------------

test("the plan leads with what the source holds, then the one choice and the names", async ({
  page,
}) => {
  const { sent } = await mockImports(page)
  await visit(page, "/databases/1/import")
  await typeSource(page)
  await page.getByRole("button", { name: "Check the source" }).click()

  await expect(question(page, "How should it come over?")).toBeVisible()
  await expect(plan(page)).toContainText("shop on db.example.com:5432")
  await expect(plan(page)).toContainText("PostgreSQL 16.4")
  const tiles = plan(page).locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(4)
  await expect(tiles.nth(0)).toContainText("Tables")
  await expect(tiles.nth(0)).toContainText("3")
  // One table was never counted, so the rows are a floor and not a total.
  await expect(tiles.nth(1)).toContainText("≥ 2,580,000")
  await expect(plan(page).getByRole("button", { name: "Keep it current" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(plan(page).getByLabel("New database on this server")).toHaveValue("shop")

  // The publication is folded away with its statement inside.
  await plan(page).getByText("The publication, and who makes it").click()
  await expect(
    plan(page).getByLabel("Name of the publication, the subscription and the slot"),
  ).toHaveValue("jd_import_shop")
  await expect(plan(page)).toContainText('CREATE PUBLICATION "jd_import_shop"')

  await plan(page).getByRole("button", { name: "Start the import" }).click()
  await expect
    .poll(() => sent.some((one) => one.request === "POST /databases/1/imports"))
    .toBe(true)
  const body = sent.find((one) => one.request === "POST /databases/1/imports")?.body as Json
  expect(body).toMatchObject({ mode: "replication", database: "shop", name: "jd_import_shop" })
  expect(String(body.source)).toContain("db.example.com")
  // The page moves on to the import it began.
  await expect.poll(() => new URL(page.url()).searchParams.get("import")).toBe("7")
})

test("copying once is a choice, and sends no replication name", async ({ page }) => {
  const { sent } = await mockImports(page)
  await visit(page, "/databases/1/import")
  await typeSource(page)
  await page.getByRole("button", { name: "Check the source" }).click()
  await plan(page).getByRole("button", { name: "Copy it once" }).click()
  await expect(plan(page).getByRole("button", { name: "Copy it once" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(plan(page).locator("[data-slot=stat-tile]")).toHaveCount(0)
  await plan(page).getByRole("button", { name: "Start the import" }).click()
  await expect
    .poll(() => sent.some((one) => one.request === "POST /databases/1/imports"))
    .toBe(true)
  const body = sent.find((one) => one.request === "POST /databases/1/imports")?.body as Json
  expect(body.mode).toBe("stream")
  expect(body).not.toHaveProperty("name")
})

test("what stands in the way is said before the command, and the command waits", async ({
  page,
}) => {
  const { sent } = await mockImports(page, {
    inspected: inspection({
      ready: true,
      replicationReady: false,
      replication: {
        ...inspection().replication,
        walLevel: "replica",
        blockers: [
          "The source's wal_level is replica. Set it to logical (ALTER SYSTEM SET wal_level = 'logical') and restart that server.",
          'Run on the source: ALTER TABLE "public"."tags" REPLICA IDENTITY FULL;',
        ],
      },
    }),
  })
  await visit(page, "/databases/1/import")
  await typeSource(page)
  await page.getByRole("button", { name: "Check the source" }).click()

  await expect(plan(page).getByText("Not yet")).toBeVisible()
  await expect(plan(page)).toContainText("wal_level is replica")
  await expect(plan(page)).toContainText("REPLICA IDENTITY FULL")
  await expect(plan(page).getByRole("button", { name: "Start the import" })).toBeDisabled()
  // A dump has none of those conditions.
  await plan(page).getByRole("button", { name: "Copy it once" }).click()
  await expect(plan(page).getByText("Not yet")).toHaveCount(0)
  await expect(plan(page).getByRole("button", { name: "Start the import" })).toBeEnabled()
  await plan(page).getByRole("button", { name: "Keep it current" }).click()
  await plan(page).getByRole("button", { name: "Check again" }).click()
  await expect
    .poll(() => sent.filter((one) => one.request === "POST /databases/1/imports/inspect").length)
    .toBe(2)
})

test("a name the server would refuse is refused on the page", async ({ page }) => {
  await mockImports(page)
  await visit(page, "/databases/1/import")
  await typeSource(page)
  await page.getByRole("button", { name: "Check the source" }).click()
  const database = plan(page).getByLabel("New database on this server")
  await database.fill("shop_main")
  await expect(plan(page)).toContainText("That is the database this connection is on.")
  await expect(plan(page).getByRole("button", { name: "Start the import" })).toBeDisabled()
  await database.fill("bad name!")
  await expect(plan(page)).toContainText("Letters, digits and underscores")
})

// ---------------------------------------------------------------------------
// An import under way
// ---------------------------------------------------------------------------

test("a replication shows each table, how far behind it is, and no cutover until all are in", async ({
  page,
}) => {
  await mockImports(page, { imports: [record()], live: { 7: copying() } })
  await visit(page, "/databases/1/import")

  // With one still going, the page opens on it.
  await expect(question(page, "Ready to cut over?")).toBeVisible()
  const run = page.getByRole("region", { name: /shop on db.example.com:5432 → shop_copy/ })
  await expect(run).toContainText("replicating")
  const tiles = run.locator("[data-slot=stat-tile]")
  await expect(tiles.nth(0)).toContainText("2 of 5")
  await expect(tiles.nth(1)).toContainText("2.4 MB behind")
  await expect(tiles.nth(2)).toContainText("Yes")
  const tables = run.getByRole("list", { name: "Tables" }).getByRole("listitem")
  await expect(tables).toHaveCount(5)
  await expect(tables.filter({ hasText: "orders" }).first()).toContainText("1,204,000 rows")
  await expect(tables.filter({ hasText: "order_items" })).toContainText("waiting")
  await expect(run.getByRole("button", { name: "Cut over…" })).toBeDisabled()
  await expect(run).toContainText("3 tables are still being copied")
  await expect(run.getByRole("button", { name: "Stop replicating" })).toBeEnabled()
  // The spine is on the copy.
  await expect(
    page.getByRole("list", { name: "Progress" }).locator("[aria-current=step]"),
  ).toHaveText("Copy")
})

test("the cutover is offered once every table is in, and asks that the writes have stopped", async ({
  page,
}) => {
  const { sent } = await mockImports(page, {
    imports: [record()],
    live: { 7: caughtUp() },
    jobs: {
      j8: {
        job: job("j8", "import-cutover", "running"),
        lines: [
          line(1, "status", "Waiting for shop_copy to apply everything the source has written"),
        ],
      },
    },
  })
  await visit(page, "/databases/1/import")
  const run = page.getByRole("region", { name: /→ shop_copy/ })
  await expect(run.locator("[data-slot=stat-tile]").nth(1)).toContainText("caught up")
  await expect(
    page.getByRole("list", { name: "Progress" }).locator("[aria-current=step]"),
  ).toHaveText("Cutover")
  await run.getByRole("button", { name: "Cut over…" }).click()

  const dialog = page.getByRole("dialog", { name: "Cut over" })
  await expect(dialog).toContainText("What the source writes after that is not copied")
  const command = dialog.getByRole("button", { name: "Cut over", exact: true })
  await expect(command).toBeDisabled()
  await dialog.getByRole("switch", { name: "Nothing is writing to the source any more" }).click()
  await expect(command).toBeEnabled()
  await command.click()

  await expect.poll(() => sent.some((one) => one.request.endsWith("/cutover"))).toBe(true)
  expect(sent.find((one) => one.request.endsWith("/cutover"))?.body).toEqual({
    writesStopped: true,
  })
  await expect(dialog).toHaveCount(0)
  await expect.poll(() => new URL(page.url()).searchParams.get("job")).toBe("j8")
})

test("stopping a replication is confirmed and takes it away", async ({ page }) => {
  const { sent } = await mockImports(page, { imports: [record()], live: { 7: copying() } })
  await visit(page, "/databases/1/import")
  await page.getByRole("button", { name: "Stop replicating" }).click()
  const dialog = page.getByRole("dialog", { name: "Stop replicating" })
  await expect(dialog).toContainText("shop_copy stays as it is")
  await dialog.getByRole("button", { name: "Stop replicating" }).click()
  await expect
    .poll(() => sent.some((one) => one.request === "DELETE /databases/1/imports/7"))
    .toBe(true)
})

test("a stream shows its progress while the job runs", async ({ page }) => {
  await mockImports(page, {
    imports: [
      record({
        id: 8,
        mode: "stream",
        status: "running",
        name: "shop_copy",
        job: job("j7", "import", "running"),
      }),
    ],
    live: {
      8: {
        import: record({
          id: 8,
          mode: "stream",
          status: "running",
          job: job("j7", "import", "running"),
        }),
        progress: {
          phase: "copying",
          tables: 12,
          tablesDone: 5,
          rows: 1_200_000,
          estimatedRows: 3_000_000,
          bytes: 412_000_000,
          table: "public.orders",
        },
      },
    },
    jobs: {
      j7: {
        job: job("j7", "import", "running"),
        lines: [
          line(
            1,
            "status",
            "Reading shop on db.example.com:5432 and loading it into shop_copy as it is read",
          ),
        ],
      },
    },
  })
  await visit(page, "/databases/1/import")
  await expect(question(page, "How far has it got?")).toBeVisible()
  const run = page.getByRole("region", { name: /→ shop_copy/ })
  await expect(run.getByRole("meter", { name: "Copied" })).toHaveAttribute("aria-valuenow", "40")
  await expect(run).toContainText("5 of 12 tables")
  await expect(run).toContainText("1,200,000 rows")
  await expect(run).toContainText("Last finished: public.orders")
  await expect(run.getByRole("button", { name: "Stop" })).toBeEnabled()
  await expect(page.locator("[data-slot=transfer-progress]")).toContainText(
    "loading it into shop_copy",
  )
})

test("a finished import leads to its database", async ({ page }) => {
  const { sent } = await mockImports(page, {
    imports: [
      record({
        status: "done",
        detail: "shop_copy is a copy of shop on db.example.com:5432, and no longer follows it",
      }),
    ],
  })
  await visit(page, "/databases/1/import")
  await expect(question(page, "Is it all here?")).toBeVisible()
  const run = page.getByRole("region", { name: /→ shop_copy/ })
  await expect(run).toContainText("shop_copy is on this server")
  await expect(run).toContainText("It no longer follows the source.")
  await run.getByRole("button", { name: "Open shop_copy" }).click()
  await expect
    .poll(() => sent.some((one) => one.request === "POST /databases/1/server/databases/connect"))
    .toBe(true)
  expect(sent.find((one) => one.request.endsWith("/server/databases/connect"))?.body).toEqual({
    database: "shop_copy",
  })
  await expect.poll(() => new URL(page.url()).pathname).toBe("/databases/9")
})

test("a failed import says why, and offers another", async ({ page }) => {
  await mockImports(page, {
    imports: [
      record({
        status: "failed",
        mode: "stream",
        detail: 'loading the target failed: statement 41 failed: relation "orders" already exists',
      }),
    ],
  })
  await visit(page, "/databases/1/import?import=7")
  const run = page.getByRole("region", { name: /→ shop_copy/ })
  await expect(run).toContainText("It stopped")
  await expect(run).toContainText('relation "orders" already exists')
  await expect(run.getByRole("button", { name: "Start another" })).toBeEnabled()
  await expect(
    page.getByRole("list", { name: "Progress" }).locator("[aria-current=step]"),
  ).toHaveText("Copy")
})

test("an import that was stopped says what it left", async ({ page }) => {
  await mockImports(page, {
    imports: [
      record({
        status: "cancelled",
        detail: "The slot jd_import_shop could not be removed from the source.",
      }),
    ],
  })
  await visit(page, "/databases/1/import?import=7")
  const run = page.getByRole("region", { name: /→ shop_copy/ })
  await expect(run).toContainText("It was stopped")
  await expect(run).toContainText("shop_copy still holds what was copied")
  await expect(run).toContainText("The slot jd_import_shop could not be removed")
})

test("the imports of a connection are listed and each opens", async ({ page }) => {
  await mockImports(page, {
    imports: [
      record({ id: 9, status: "done", database: "shop_old", name: "jd_import_old" }),
      record({ id: 8, status: "failed", mode: "stream", database: "shop_try" }),
    ],
  })
  await visit(page, "/databases/1/import")
  // None is going, so the page opens on a new one.
  await expect(question(page, "Where is the database coming from?")).toBeVisible()
  const list = page.getByRole("region", { name: "Imports" })
  await expect(list).toContainText("2")
  await list.getByRole("button", { name: "Open the import of shop_try" }).click()
  await expect.poll(() => new URL(page.url()).searchParams.get("import")).toBe("8")
  await expect(page.getByRole("region", { name: /→ shop_try/ })).toContainText("failed")
})

// ---------------------------------------------------------------------------
// Who may, and where
// ---------------------------------------------------------------------------

test("a role that may only read sees an import and can change none of it", async ({ page }) => {
  await mockImports(page, { viewer: true, imports: [record()], live: { 7: caughtUp() } })
  await visit(page, "/databases/1/import")
  const run = page.getByRole("region", { name: /→ shop_copy/ })
  await expect(run.locator("[data-slot=stat-tile]").nth(1)).toContainText("caught up")
  await expect(run.getByRole("button", { name: "Cut over…" })).toBeDisabled()
  await expect(run.getByRole("button", { name: "Stop replicating" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "New import" })).toHaveCount(0)
})

test("a role that may only read is told a new import needs an administrator", async ({ page }) => {
  await mockImports(page, { viewer: true })
  await visit(page, "/databases/1/import")
  await expect(page.getByText("Bringing a database over needs an administrator")).toBeVisible()
  await expect(page.getByRole("button", { name: "Check the source" })).toHaveCount(0)
})

test("a protected connection takes no import", async ({ page }) => {
  await mockImports(page, { rows: { 1: { readOnly: true } } })
  await visit(page, "/databases/1/import")
  await expect(page.getByText("shop is protected")).toBeVisible()
  await expect(page.getByRole("button", { name: "Check the source" })).toHaveCount(0)
})

test("the page is a page of the engines that can restore under another name, and no others", async ({
  page,
}) => {
  await mockImports(page)
  // Redis and MongoDB have it, with three steps and no cutover.
  await visit(page, "/databases/4/import")
  await expect(question(page, "Where is the database coming from?")).toBeVisible()
  await expect(page.getByRole("list", { name: "Progress" }).getByRole("listitem")).toHaveText([
    "Source",
    "Plan",
    "Copy",
  ])
  // SQLite has no second database to bring one into.
  await page.goto("/databases/7/import")
  await expect(page.getByText(/does not offer it|has none/).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Check the source" })).toHaveCount(0)
})

test("a list that cannot be read is an error with a way to ask again", async ({ page }) => {
  await mockImports(page, {
    reads: { list: { status: 500, body: { error: { code: "internal", message: "boom" } } } },
  })
  await visit(page, "/databases/1/import")
  await expect(page.getByText(/boom/).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Check the source" })).toHaveCount(0)
})

test("the pages fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockImports(page, { imports: [record()], live: { 7: copying() } })
  await visit(page, "/databases/1/import")
  await expect(page.getByRole("region", { name: /→ shop_copy/ })).toBeVisible()
  expect(await noSideways(page)).toBe(true)
  await page.goto("/databases/1/import?import=new")
  await expect(page.getByLabel("Host")).toBeVisible()
  expect(await noSideways(page)).toBe(true)
  await typeSource(page)
  await page.getByRole("button", { name: "Check the source" }).click()
  await expect(plan(page)).toBeVisible()
  expect(await noSideways(page)).toBe(true)
})

// ---------------------------------------------------------------------------
// A copy to another server
// ---------------------------------------------------------------------------

test("a copy can go to another saved connection of the same engine", async ({ page }) => {
  const { sent } = await mockImports(page, {
    replica: true,
    jobs: {
      j9: {
        job: job("j9", "copy", "succeeded", {
          title: "Copy shop to shop-replica",
          endedAt: minutesAgo(0),
        }),
        lines: [
          line(
            1,
            "result",
            JSON.stringify({
              database: "shop_copy",
              summary: "12 tables, 4000 rows",
              connection: 8,
              connectionName: "shop-replica",
            }),
          ),
        ],
      },
    },
  })
  await visit(page, "/databases/1/backups")
  await page.getByRole("button", { name: "Copy database" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("combobox", { name: "Copy into" }).click()
  await page.getByRole("option", { name: /shop-replica/ }).click()
  await dialog.getByLabel("Name of the copy").fill("shop_copy")
  await dialog.getByRole("button", { name: "Copy", exact: true }).click()
  await expect.poll(() => sent.some((one) => one.request === "POST /databases/1/copy")).toBe(true)
  expect(sent.find((one) => one.request === "POST /databases/1/copy")?.body).toEqual({
    name: "shop_copy",
    to: 8,
  })
  // The database was made on the other server, so that is where it is opened.
  await page.getByRole("button", { name: "Connect shop_copy on shop-replica" }).click()
  await expect
    .poll(() => sent.some((one) => one.request === "POST /databases/8/server/databases/connect"))
    .toBe(true)
})

test("a copy offers no other server where there is none", async ({ page }) => {
  await mockImports(page)
  await visit(page, "/databases/1/backups")
  await page.getByRole("button", { name: "Copy database" }).click()
  await expect(page.getByRole("dialog").getByRole("combobox", { name: "Copy into" })).toHaveCount(0)
})

// ---------------------------------------------------------------------------
// Pictures for the pull request
// ---------------------------------------------------------------------------

test("screenshots of each state @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to take them")
  test.setTimeout(180_000)
  const out = process.env.JD_SHOTS as string
  const shot = (name: string) => page.screenshot({ path: `${out}/${name}.png` })
  const settle = async () => {
    await page.waitForLoadState("networkidle")
    await page.evaluate(() =>
      Promise.all(
        document.getAnimations().map((animation) => animation.finished.catch(() => undefined)),
      ),
    )
  }
  const state: Mock = {
    replica: true,
    imports: [
      record({
        id: 9,
        status: "done",
        database: "shop_2025",
        name: "jd_import_2025",
        updatedAt: minutesAgo(60 * 30),
      }),
      record(),
    ],
    live: { 7: copying() },
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockImports(page, state)

  // The source, empty and then filled.
  await visit(page, "/databases/1/import?import=new")
  await settle()
  await shot("source-1440")
  await typeSource(page)
  await page.getByLabel("Port").fill("5432")
  await shot("source-typed-1440")

  // The plan, with what the source holds.
  await page.getByRole("button", { name: "Check the source" }).click()
  await expect(plan(page)).toBeVisible()
  await plan(page).getByText("The publication, and who makes it").click()
  await settle()
  await shot("plan-1440")

  // The replication copying, then caught up.
  await page.goto("/databases/1/import?import=7")
  await expect(page.getByRole("region", { name: /→ shop_copy/ })).toContainText(
    "3 tables are still being copied",
  )
  await settle()
  await shot("replicating-1440")

  const caught = await page.context().newPage()
  await mockImports(caught, { ...state, live: { 7: caughtUp() } })
  await caught.setViewportSize({ width: 1440, height: 900 })
  await visit(caught, "/databases/1/import?import=7")
  await expect(caught.getByRole("button", { name: "Cut over…" })).toBeEnabled()
  await caught.getByRole("button", { name: "Cut over…" }).click()
  const dialog = caught.getByRole("dialog", { name: "Cut over" })
  await dialog.getByRole("switch", { name: "Nothing is writing to the source any more" }).click()
  await dialog.evaluate((el) =>
    Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)),
  )
  await caught.screenshot({ path: `${out}/cutover-1440.png` })
  await caught.close()

  // A dump read as it loads.
  const stream = await page.context().newPage()
  await mockImports(stream, {
    replica: true,
    imports: [
      record({ id: 8, mode: "stream", status: "running", job: job("j7", "import", "running") }),
    ],
    live: {
      8: {
        import: record({
          id: 8,
          mode: "stream",
          status: "running",
          job: job("j7", "import", "running"),
        }),
        progress: {
          phase: "copying",
          tables: 12,
          tablesDone: 5,
          rows: 1_200_000,
          estimatedRows: 3_000_000,
          bytes: 412_000_000,
          table: "public.orders",
        },
      },
    },
    jobs: {
      j7: {
        job: job("j7", "import", "running"),
        lines: [
          line(1, "status", "Creating database shop_copy"),
          line(
            2,
            "status",
            "Reading shop on db.example.com:5432 and loading it into shop_copy as it is read",
          ),
          line(3, "stdout", "public.customers: 180000 rows"),
          line(4, "status", "public.customers done (1 of 12 tables)"),
        ],
      },
    },
  })
  await stream.setViewportSize({ width: 1440, height: 900 })
  await visit(stream, "/databases/1/import?import=8")
  await expect(stream.getByRole("meter", { name: "Copied" })).toBeVisible()
  await stream.waitForLoadState("networkidle")
  await stream.screenshot({ path: `${out}/stream-1440.png` })
  await stream.close()

  // A finished one.
  const done = await page.context().newPage()
  await mockImports(done, {
    imports: [
      record({
        status: "done",
        detail: "shop_copy is a copy of shop on db.example.com:5432, and no longer follows it",
      }),
    ],
  })
  await done.setViewportSize({ width: 1440, height: 900 })
  await visit(done, "/databases/1/import?import=7")
  await expect(done.getByRole("button", { name: "Open shop_copy" })).toBeVisible()
  await done.waitForLoadState("networkidle")
  await done.screenshot({ path: `${out}/done-1440.png` })
  await done.close()

  // The copy dialog with another server to go to.
  await page.goto("/databases/1/backups")
  await page.getByRole("button", { name: "Copy database" }).click()
  const copy = page.getByRole("dialog")
  await copy.getByRole("combobox", { name: "Copy into" }).click()
  await page.getByRole("option", { name: /shop-replica/ }).click()
  await copy.getByLabel("Name of the copy").fill("shop_copy")
  await copy.evaluate((el) =>
    Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)),
  )
  await shot("copy-to-server-1440")

  // The phone.
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/databases/1/import?import=7")
  await expect(page.getByRole("region", { name: /→ shop_copy/ })).toBeVisible()
  await settle()
  await page.screenshot({ path: `${out}/replicating-390.png`, fullPage: true })
})
