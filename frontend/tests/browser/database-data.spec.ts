import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { hold, mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * The SQL table editor (`/databases/<id>/data`): a rail of what holds rows,
 * the grid with its filters, its foot and its staged change set, and the row
 * inspector.
 *
 * Most of what is checked here is a defect the editor this one replaces
 * shipped: an edit that wrote every column back from its display form, a
 * mutation reported as done when no row matched, a page past the end called
 * an empty table, a count drawn over the wrong table, a filter that could not
 * say `= ''` and scanned the table per keystroke, an export that failed as a
 * page of JSON, a table switch that Back could not undo. The API is mocked in
 * the browser; what the requests carry is what is asserted.
 */

// A step here is often a refetch and a redraw of a windowed grid, and the
// suite shares its machine: the default five seconds is the wait this spec
// lost to a busy runner, not to the page.
const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

type Cell = string | number | boolean | null

const COLUMNS = [
  {
    name: "id",
    type: "bigint",
    nullable: false,
    position: 1,
    default: "nextval('items_id_seq'::regclass)",
  },
  { name: "name", type: "text", nullable: false, position: 2 },
  { name: "note", type: "text", nullable: true, position: 3 },
  { name: "qty", type: "integer", nullable: false, position: 4, default: "0" },
]

function detailOf(name: string, over: Record<string, unknown> = {}) {
  return {
    schema: "public",
    name,
    type: "table",
    columns: COLUMNS,
    primaryKey: ["id"],
    indexes: [
      {
        name: `${name}_pkey`,
        columns: ["id"],
        unique: true,
        primary: true,
        method: "btree",
        constraint: `${name}_pkey`,
      },
    ],
    foreignKeys: [],
    constraints: [],
    referencedBy: [],
    facts: [],
    estimatedRows: 250,
    createSql: `CREATE TABLE "public"."${name}" (\n  "id" bigint NOT NULL\n);`,
    createSqlSource: "generated",
    ...over,
  }
}

/** A second table with a document in it: an integer past 2^53, an array, the keys in an order. */
const DOCUMENT = '{"theme": "dark", "big": 9007199254740993, "tags": ["a"]}'
const DOCS_COLUMNS = [
  { name: "id", type: "bigint", nullable: false, position: 1 },
  { name: "meta", type: "jsonb", nullable: false, position: 2 },
]

const rowsOf = (count: number): Cell[][] =>
  Array.from({ length: count }, (_, i) => [
    String(i + 1),
    `Item ${i + 1}`,
    i % 3 === 0 ? null : "",
    String((i + 1) * 2),
  ])

type EditorMock = DatabaseMock & {
  /** How many rows `items` holds. */
  total?: number
  /** Fields laid over a table's detail, by table name. */
  details?: Record<string, Record<string, unknown>>
  /** What `POST /changes` answers when it is not a dry run. */
  apply?: (body: Record<string, unknown>) => { status: number; body: unknown }
  /** The session's capabilities, when neither an admin's nor a viewer's. */
  capabilities?: string[]
  /** `GET /browse` of this table answers only once this settles. */
  browseHeld?: { table: string; until: Promise<unknown> }
  /** `GET /count` answers only once this settles. */
  countHeld?: Promise<unknown>
  /** What `GET /export` answers. */
  exported?: { status: number; body: string; state?: Record<string, unknown> }
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * The table editor's routes on connection 1, laid over the section's fixture.
 * Hands back what the page sent, by route.
 */
async function mockEditor(page: Page, options: EditorMock = {}) {
  const shell = await mockDatabases(page, options)
  const sent = {
    browse: [] as URL[],
    count: [] as URL[],
    changes: [] as Record<string, unknown>[],
    rowsSql: [] as Record<string, unknown>[],
    exports: [] as URL[],
    imports: [] as { options: Record<string, unknown>; file: string }[],
  }
  const total = options.total ?? 250
  const all = rowsOf(total)

  if (options.capabilities) {
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: options.capabilities,
        user: { id: 1, username: "operator", displayName: "Operator", role: "limited" },
      }),
    )
  }

  await page.route("**/api/v1/databases/1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const rest = url.pathname.replace(/^\/api\/v1\/databases\/1/, "")
    const table = url.searchParams.get("table") ?? ""

    if (rest === "/catalog") {
      return json(route, {
        schema: url.searchParams.get("schema") || "public",
        defaultSchema: "public",
        schemas: [
          { name: "public", default: true, tables: 3 },
          { name: "sales", tables: 0 },
          { name: "pg_catalog", system: true, tables: 140 },
        ],
        objects:
          (url.searchParams.get("schema") || "public") === "public"
            ? {
                tables: [
                  {
                    kind: "table",
                    schema: "public",
                    name: "items",
                    estimatedRows: 250,
                    size: 65536,
                  },
                  { kind: "table", schema: "public", name: "other", estimatedRows: 1_204_000 },
                ],
                views: [{ kind: "view", schema: "public", name: "item_names" }],
                materializedViews: [],
              }
            : { tables: [], views: [], materializedViews: [] },
        truncated: [],
        limit: 5000,
      })
    }
    if (rest === "/table") {
      if (table === "gone") {
        return json(
          route,
          { error: { code: "not_found", message: "no table or view named gone" } },
          404,
        )
      }
      const view = table === "item_names" ? { type: "view", primaryKey: [], indexes: [] } : {}
      const docs = table === "docs" ? { columns: DOCS_COLUMNS } : {}
      return json(route, detailOf(table, { ...view, ...docs, ...options.details?.[table] }))
    }
    if (rest === "/browse") {
      sent.browse.push(url)
      if (options.browseHeld?.table === table) await options.browseHeld.until
      if (table === "docs") {
        return json(route, {
          columns: ["id", "meta"],
          types: ["INT8", "JSONB"],
          kinds: ["integer", "json"],
          rows: [["1", DOCUMENT]],
          rowCount: 1,
          rowsAffected: 0,
          duration: "1ms",
          truncated: false,
          statement: 'SELECT * FROM "public"."docs" ORDER BY "id" ASC LIMIT $1 OFFSET $2',
          primaryKey: ["id"],
          estimatedRows: 1,
          sort: [{ column: "id", desc: false }],
          limit: 100,
          offset: 0,
        })
      }
      const limit = Number(url.searchParams.get("limit") ?? 100)
      const offset = Number(url.searchParams.get("offset") ?? 0)
      const filters = JSON.parse(url.searchParams.get("filters") ?? "[]") as {
        column: string
        op: string
        value?: string
      }[]
      if (filters.some((filter) => !COLUMNS.some((column) => column.name === filter.column))) {
        return json(
          route,
          { error: { code: "bad_request", message: 'ERROR: column "dropped" does not exist' } },
          400,
        )
      }
      const sort = JSON.parse(url.searchParams.get("sort") ?? "[]") as {
        column: string
        desc: boolean
      }[]
      let rows = all
      for (const filter of filters) {
        const at = COLUMNS.findIndex((column) => column.name === filter.column)
        if (filter.op === "eq") rows = rows.filter((row) => row[at] === filter.value)
        if (filter.op === "is_null") rows = rows.filter((row) => row[at] === null)
      }
      if (sort[0]?.column === "id" && sort[0].desc) rows = [...rows].reverse()
      const pageRows = rows.slice(offset, offset + limit)
      return json(route, {
        columns: COLUMNS.map((column) => column.name),
        types: ["INT8", "TEXT", "TEXT", "INT4"],
        kinds: ["integer", "text", "text", "integer"],
        rows: pageRows,
        rowCount: pageRows.length,
        rowsAffected: 0,
        duration: "1.5ms",
        truncated: offset + limit < rows.length,
        statement: `SELECT * FROM "public"."${table}" ORDER BY "id" ASC LIMIT $1 OFFSET $2`,
        primaryKey:
          (options.details?.[table]?.primaryKey as string[] | undefined) ??
          (table === "item_names" ? [] : ["id"]),
        estimatedRows: table === "other" ? 1_204_000 : total,
        sort: sort.length > 0 ? sort : [{ column: "id", desc: false }],
        limit,
        offset,
      })
    }
    if (rest === "/count") {
      sent.count.push(url)
      await options.countHeld
      return json(route, { count: table === "other" ? 200_000_000 : total })
    }
    if (rest === "/changes") {
      const body = request.postDataJSON() as Record<string, unknown>
      sent.changes.push(body)
      const changes = body.changes as { op: string }[]
      const answer = {
        applied: !body.dryRun,
        dryRun: Boolean(body.dryRun),
        keyColumns: ["id"],
        statements: changes.map((change, i) => `${change.op.toUpperCase()} statement ${i + 1};`),
        results: changes.map((change, index) => ({
          index,
          op: change.op,
          statement: "",
          rowsAffected: 1,
        })),
        attempts: body.dryRun ? 0 : 1,
        duration: "2ms",
      }
      if (!body.dryRun && options.apply) {
        const refused = options.apply(body)
        return json(route, refused.body, refused.status)
      }
      return json(route, answer)
    }
    if (rest === "/rows/sql") {
      sent.rowsSql.push(request.postDataJSON() as Record<string, unknown>)
      return json(route, {
        sql: 'INSERT INTO "public"."items" ("id", "name") VALUES (1, \'Item 1\');',
      })
    }
    if (rest === "/export") {
      sent.exports.push(url)
      const exported = options.exported ?? { status: 200, body: "id,name\n1,Item 1\n" }
      if (exported.status !== 200) {
        return route.fulfill({
          status: exported.status,
          contentType: "application/json",
          body: exported.body,
        })
      }
      return route.fulfill({
        status: 200,
        contentType: "text/csv",
        headers: { "Content-Disposition": 'attachment; filename="items.csv"' },
        body: exported.body,
      })
    }
    if (rest === "/export/status") {
      return json(route, {
        status: "complete",
        rows: 1,
        format: "csv",
        startedAt: "2026-10-01T09:00:00Z",
        ...options.exported?.state,
      })
    }
    if (rest === "/import/upload") {
      const raw = request.postData() ?? ""
      const optionsPart = /name="options"\r\n\r\n([\s\S]*?)\r\n--/.exec(raw)?.[1] ?? "{}"
      const filePart =
        /name="file"; filename="[^"]*"[\s\S]*?\r\n\r\n([\s\S]*?)\r\n--/.exec(raw)?.[1] ?? ""
      // The server refuses a file that comes before its options.
      if (raw.indexOf('name="options"') > raw.indexOf('name="file"')) {
        return json(
          route,
          {
            error: {
              code: "bad_request",
              message: "the options part has to come before the file part",
            },
          },
          400,
        )
      }
      const parsed = JSON.parse(optionsPart) as Record<string, unknown>
      sent.imports.push({ options: parsed, file: filePart })
      const mapping = (parsed.mapping as Record<string, string> | undefined) ?? {
        Name: "name",
        Quantity: "",
      }
      return json(route, {
        dryRun: Boolean(parsed.dryRun),
        schema: "public",
        table: "items",
        format: "csv",
        encoding: "utf-8",
        mode: parsed.mode ?? "insert",
        columns: [
          {
            source: "Name",
            index: 0,
            target: mapping.Name ?? "",
            inferred: "text",
            targetType: "text",
            examples: ["Imported one"],
          },
          {
            source: "Quantity",
            index: 1,
            target: mapping.Quantity ?? "",
            inferred: "integer",
            examples: ["7"],
          },
        ],
        rowsRead: 2,
        inserted: parsed.dryRun ? 0 : 2,
        updated: 0,
        skipped: 0,
        errors: [],
        errorsTruncated: false,
        preview: parsed.dryRun
          ? { columns: ["name"], rows: [["Imported one"], [null]] }
          : undefined,
        statement: 'INSERT INTO "public"."items" ("name") VALUES ($1)',
        atomic: true,
        warnings: [],
      })
    }
    return route.fallback()
  })

  return { ...shell, sent }
}

const DATA = "/databases/1/data"
const ITEMS = `${DATA}?schema=public&table=items`
const grid = (page: Page) => page.locator("[data-slot=data-grid]")
/** The open table's name in the strip — read by slot: a dialog over the page hides its roles. */
const title = (page: Page) => page.locator("[data-slot=table-editor] h2")
const cell = (page: Page, row: number, col: number) =>
  page.locator(`[data-row="${row}"] [data-col="${col}"]`)
const bar = (page: Page) => page.locator("[data-slot=change-bar]")
const foot = (page: Page) => page.locator("[data-slot=data-grid-status]")
const field = (page: Page, name: string) =>
  page.locator("[data-slot=row-field]").filter({ has: page.locator(`label:text-is("${name}")`) })
const where = (page: Page) => page.evaluate(() => location.pathname + location.search)
const applied = (sent: { changes: Record<string, unknown>[] }) =>
  sent.changes.filter((body) => !body.dryRun)

async function edit(page: Page, row: number, col: number, text: string) {
  await cell(page, row, col).dblclick()
  await page.keyboard.press("ControlOrMeta+a")
  await page.keyboard.insertText(text)
  await page.keyboard.press("Enter")
}

/* ------------------------------------------------------------------ rail */

test("the rail lists what holds rows by kind, and a table is a link and a step of history", async ({
  page,
}) => {
  await mockEditor(page)
  await page.goto(`${DATA}?schema=public`)

  const rail = page.getByRole("navigation", { name: "tables of this schema" })
  await expect(rail.getByRole("region", { name: "Tables" })).toContainText("items")
  await expect(rail.getByRole("region", { name: "Views" })).toContainText("item_names")
  // An estimate at the rail's width, and nothing drawn for a view that has none.
  await expect(rail.getByRole("link", { name: /^items/ })).toContainText("250")
  await expect(rail.getByRole("link", { name: /^other/ })).toContainText("1.2M")
  await expect(rail.getByRole("link", { name: /^items/ })).toHaveAttribute(
    "href",
    "/databases/1/data?schema=public&table=items",
  )
  await expect(page.getByText("Pick a table")).toBeVisible()

  await rail.getByRole("link", { name: /^items/ }).click()
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  await expect(rail.getByRole("link", { name: /^items/ })).toHaveAttribute("aria-current", "page")
  await rail.getByRole("link", { name: /^other/ }).click()
  await expect(title(page)).toContainText("other")

  // Back is the table the reader came from, not whatever preceded the page.
  await page.goBack()
  await expect(title(page)).toContainText("items")
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=items")

  // The open table's own link opens it plain: the order it was given is let
  // go, not put back. (The section kept the last write to the address and
  // applied it again whenever the address came back to where it started.)
  await page.getByRole("button", { name: "Sort", exact: true }).click()
  await page.getByRole("menuitem", { name: /^qty/ }).click()
  await expect(page.getByRole("group", { name: "Sorted by qty, ascending" })).toBeVisible()
  await expect.poll(() => where(page)).toContain("sort=")
  await rail.getByRole("link", { name: /^items/ }).click()
  await expect(page.getByRole("group", { name: /^Sorted by/ })).toHaveCount(0)
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=items")

  // The search narrows by name and says so when nothing is called that.
  await rail.getByRole("textbox", { name: "Find a table" }).fill("zzz")
  await expect(rail.getByText("Nothing here is called that.")).toBeVisible()
  await rail.getByRole("button", { name: "Clear the search" }).click()
  await expect(rail.getByRole("link", { name: /^other/ })).toBeVisible()
})

test("a schema that is not there says so instead of listing nothing", async ({ page }) => {
  await mockEditor(page)
  await page.goto(`${DATA}?schema=dropped`)
  await expect(page.getByText("No schema called dropped")).toBeVisible()
  await page.getByRole("link", { name: "Open public" }).click()
  await expect(page.getByRole("link", { name: /^items/ })).toBeVisible()

  // And a table that is gone says that, with the way back to what is there.
  await page.goto(`${DATA}?schema=public&table=gone`)
  await expect(page.getByText("No table called gone")).toBeVisible()
  await expect(page.getByRole("tab")).toHaveCount(0)
})

/* ------------------------------------------------------------ the address */

test("the view of a table is its address: filters, sort, page and which view", async ({ page }) => {
  const { sent } = await mockEditor(page)
  const filters = JSON.stringify([{ column: "note", op: "is_null" }])
  const sort = JSON.stringify([{ column: "id", desc: true }])
  await page.goto(
    `${ITEMS}&filters=${encodeURIComponent(filters)}&sort=${encodeURIComponent(sort)}`,
  )

  await expect(page.getByRole("group", { name: "Filter: note is NULL" })).toBeVisible()
  await expect(page.getByRole("group", { name: "Sorted by id, descending" })).toBeVisible()
  await expect(cell(page, 0, 0)).toHaveText("250")
  const asked = sent.browse.at(-1)!
  expect(asked.searchParams.get("filters")).toBe(filters)
  expect(asked.searchParams.get("sort")).toBe(sort)

  // The structure and the definition are views of the same address.
  await page.getByRole("tab", { name: "Structure" }).click()
  await expect.poll(() => where(page)).toContain("view=structure")
  await expect(page.getByRole("region", { name: "Columns" })).toContainText("bigint")
  await expect(page.getByRole("region", { name: "Indexes" })).toContainText("items_pkey")
  await expect(page.getByRole("link", { name: "Change it in Schema" })).toHaveAttribute(
    "href",
    "/databases/1/schema?schema=public&table=items",
  )
  await page.getByRole("tab", { name: "Definition" }).click()
  await expect(page.locator("[data-slot=code-view]")).toBeVisible()
  await expect(page.locator("[data-slot=code-view]")).toContainText("generated")

  // What the search page hands a hit over under is read as the filters.
  await page.goto(`${ITEMS}&where=${encodeURIComponent('[{"column":"id","op":"eq","value":"7"}]')}`)
  await expect(page.getByRole("group", { name: "Filter: id = 7" })).toBeVisible()
  await expect(grid(page).locator("[data-row]")).toHaveCount(1)
})

test("a page past the last row says so, and a table of one page offers no second", async ({
  page,
}) => {
  await mockEditor(page, { total: 100 })
  await page.goto(ITEMS)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  // Exactly one page of rows: the server said there is no other.
  await expect(page.getByRole("button", { name: "Next page" })).toBeDisabled()
  await expect(foot(page)).toContainText("1–100 of ~100")

  await page.goto(`${ITEMS}&page=3`)
  await expect(page.getByText("No rows on this page")).toBeVisible()
  await expect(page.getByText("has no rows")).toHaveCount(0)
  await page.getByRole("button", { name: "Back to the first page" }).click()
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  await expect.poll(() => where(page)).not.toContain("page=")
})

test("an exact count belongs to the table it was taken of", async ({ page }) => {
  const count = hold()
  const { sent } = await mockEditor(page, { countHeld: count.until })
  await page.goto(`${DATA}?schema=public&table=other`)
  await expect(foot(page)).toContainText("of ~1,204,000")
  await page.getByRole("button", { name: "Count exactly" }).click()
  await expect.poll(() => sent.count.length).toBe(1)

  // The reader moves on before the count comes back.
  await page.getByRole("link", { name: /^items/ }).click()
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  count.release()
  await page.waitForLoadState("networkidle")
  await expect(foot(page)).toContainText("of ~250")
  await expect(foot(page)).not.toContainText("200,000,000")

  // Counted where it stands, it is said without the tilde and gives a last page.
  await page.getByRole("button", { name: "Count exactly" }).click()
  await expect(foot(page)).toContainText("1–100 of 250")
  await expect(foot(page)).toContainText("of 3")
})

/* ---------------------------------------------------------------- filters */

test("a filter is written in a form and sent when it is applied, the empty string included", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  const before = sent.browse.length

  await page.getByRole("button", { name: "Filter", exact: true }).click()
  await page.getByRole("combobox", { name: "Column" }).click()
  await page.getByRole("option", { name: "note", exact: true }).click()
  await page.getByRole("combobox", { name: "Condition" }).click()
  await page.getByRole("option", { name: "is", exact: true }).click()
  await page.getByRole("textbox", { name: "Value" }).fill("abc")
  await page.waitForLoadState("networkidle")
  // Typing asks the server nothing.
  expect(sent.browse.length).toBe(before)
  await page.getByRole("textbox", { name: "Value" }).fill("")
  await page.getByRole("button", { name: "Apply", exact: true }).click()

  // `note = ""` is a condition, not a condition with nothing typed.
  await expect(page.getByRole("group", { name: 'Filter: note = ""' })).toBeVisible()
  await expect.poll(() => sent.browse.length).toBe(before + 1)
  expect(JSON.parse(sent.browse.at(-1)!.searchParams.get("filters")!)).toEqual([
    { column: "note", op: "eq", value: "" },
  ])
  await expect(foot(page)).not.toContainText("~250")

  await page.getByRole("button", { name: 'Remove the filter note = ""' }).click()
  await expect(page.getByRole("group", { name: /^Filter:/ })).toHaveCount(0)
})

test("a filter on a column that is gone can be cleared from where it failed", async ({ page }) => {
  await mockEditor(page)
  await page.goto(
    `${ITEMS}&filters=${encodeURIComponent('[{"column":"dropped","op":"eq","value":"1"}]')}`,
  )
  await expect(grid(page).getByRole("alert")).toContainText('column "dropped" does not exist')
  await page.getByRole("button", { name: "Clear sort and filters" }).click()
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=items")
})

/* ------------------------------------------------------------------ edits */

test("an edit sends only what changed, keyed and guarded by what was read, once", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  await edit(page, 1, 1, "Renamed")
  await expect(bar(page)).toContainText("1 change — 1 edited")

  // Review asks the server for its statements and writes nothing.
  await bar(page).getByRole("button", { name: "Review", exact: true }).click()
  const review = page.getByRole("dialog", { name: "Apply 1 change to items" })
  await expect(review).toContainText("UPDATE statement 1;")
  expect(sent.changes).toHaveLength(1)
  expect(sent.changes[0].dryRun).toBe(true)

  await review.getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)).toEqual([
    {
      schema: "public",
      table: "items",
      changes: [
        // The key is the primary key as read, plus the edited column as read;
        // the values are the edited column alone.
        { op: "update", key: { id: "2", name: "Item 2" }, values: { name: "Renamed" } },
      ],
    },
  ])
  // The rows are read again after a set is applied.
  await expect.poll(() => sent.browse.length).toBeGreaterThan(1)
})

test("on a production database Apply shows the statements before it writes", async ({ page }) => {
  const { sent } = await mockEditor(page, { rows: { 1: { environment: "production" } } })
  await page.goto(ITEMS)
  await edit(page, 1, 1, "Renamed")
  await bar(page).getByRole("button", { name: "Apply…", exact: true }).click()
  const review = page.getByRole("dialog", { name: "Apply 1 change to items" })
  await expect(review).toContainText("UPDATE statement 1;")
  await expect(review).toContainText("A production database.")
  // Nothing was written by that press.
  expect(applied(sent)).toEqual([])
  await review.getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)).toHaveLength(1)
})

test("NULL, the empty string and the default are three things, each asked for by name", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  // Row 1 holds NULL in `note`, row 2 the empty string: drawn as two words.
  await expect(cell(page, 0, 2)).toHaveText("NULL")
  await expect(cell(page, 1, 2)).toHaveText('""')

  await cell(page, 0, 2).click()
  await page.keyboard.press("Space")
  const row = page.getByRole("complementary", { name: "Row" })
  await expect(row).toContainText("Row 1")
  await expect(field(page, "note").getByRole("textbox")).toHaveAttribute("placeholder", "NULL")
  // A field left as it was opened stages nothing.
  await field(page, "note").getByRole("textbox").focus()
  await page.keyboard.press("Enter")
  await expect(bar(page)).toHaveCount(0)

  await field(page, "note").getByRole("button", { name: "Set note to…" }).click()
  await page.getByRole("menuitem", { name: "Empty string" }).click()
  await field(page, "qty").getByRole("button", { name: "Set qty to…" }).click()
  await page.getByRole("menuitem", { name: /^Default/ }).click()
  // Row 2: the empty string to NULL.
  await row.getByRole("button", { name: "Next row" }).click()
  await expect(row).toContainText("Row 2")
  await field(page, "note").getByRole("button", { name: "Set note to…" }).click()
  await page.getByRole("menuitem", { name: "NULL" }).click()

  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect((applied(sent)[0].changes as unknown[]).length).toBe(2)
  expect(applied(sent)[0].changes).toEqual([
    {
      op: "update",
      key: { id: "1", note: null, qty: "2" },
      values: { note: "", qty: { $default: true } },
    },
    { op: "update", key: { id: "2", note: "" }, values: { note: null } },
  ])
})

test("a new row sends only what was set, and a 64-bit integer keeps every digit", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  await page.getByRole("button", { name: "Insert row" }).click()
  await page.getByRole("button", { name: "Show the row" }).click()
  await expect(page.getByRole("complementary", { name: "Row" })).toContainText("New row")

  await field(page, "id").getByRole("textbox").fill("9007199254740993")
  await page.keyboard.press("Enter")
  await field(page, "name").getByRole("textbox").fill("precision")
  await page.keyboard.press("Enter")
  await expect(bar(page)).toContainText("1 change — 1 new")
  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)

  // `note` and `qty` were never set: they are absent, and take their defaults.
  expect(applied(sent)[0].changes).toEqual([
    { op: "insert", values: { id: "9007199254740993", name: "precision" } },
  ])
  // The body carries the digits as text, not a number a float would round.
  expect(JSON.stringify(applied(sent)[0])).toContain('"id":"9007199254740993"')
})

test("a document is edited in its tree, and what was not touched goes back as it came", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(`${DATA}?schema=public&table=docs`)
  await cell(page, 0, 1).click()
  await page.keyboard.press("Space")
  const tree = field(page, "meta").locator("[data-slot=json-doc-tree]")
  // The digits as stored: a tree of JavaScript's numbers would print …992.
  await expect(tree).toContainText("9007199254740993")

  await tree.getByRole("button", { name: "Edit theme" }).click()
  await page.keyboard.insertText("light")
  await page.keyboard.press("Enter")
  await tree.getByRole("button", { name: "Add an item to tags" }).click()
  await page.getByRole("textbox", { name: "New value" }).fill("42")
  await page.keyboard.press("Enter")
  await tree.getByRole("button", { name: "Remove 0", exact: true }).click()
  await expect(tree).toContainText("1 item")

  // A key the object already has is refused where it is typed.
  await tree.getByRole("button", { name: "Add a key to meta" }).click()
  await page.getByRole("textbox", { name: "New key" }).fill("big")
  await page.keyboard.press("Enter")
  await expect(field(page, "meta").getByRole("alert")).toContainText("already has a key called big")
  await page.keyboard.press("Escape")

  await expect(bar(page)).toContainText("1 change — 1 edited")
  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)[0].changes).toEqual([
    {
      op: "update",
      key: { id: "1", meta: DOCUMENT },
      // One node rewritten at a time: the big integer and the order of the
      // keys are the characters that were read.
      values: { meta: '{"theme": "light", "big": 9007199254740993, "tags": [42]}' },
    },
  ])
})

test("a refused set says which change, that nothing was written, and stays staged", async ({
  page,
}) => {
  const { sent } = await mockEditor(page, {
    apply: () => ({
      status: 409,
      body: {
        error: {
          code: "change_conflict",
          message: "change 2 (update) matched 0 rows",
          field: "changes[1]",
          operation: "update",
          reason: "matched 0 rows",
        },
      },
    }),
  })
  await page.goto(ITEMS)
  await edit(page, 0, 1, "First")
  await edit(page, 2, 1, "Second")
  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()

  const refusal = grid(page).getByRole("alert")
  await expect(refusal).toContainText("Nothing was written.")
  await expect(refusal).toContainText("Change 2 of 2")
  await expect(refusal).toContainText("the row was changed or deleted after it was read")
  // The set is still there, and the refused row is marked where it stands.
  await expect(bar(page)).toContainText("2 changes — 2 edited")
  await expect(page.locator('[data-row="2"]')).toContainText("Second")
  expect(applied(sent)).toHaveLength(1)

  await refusal.getByRole("button", { name: "Take that change out" }).click()
  await expect(bar(page)).toContainText("1 change — 1 edited")
  await expect(grid(page).getByRole("alert")).toHaveCount(0)
})

test("a delete is staged, shown as a statement, and needs the role that may destroy", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  await cell(page, 3, 1).click()
  await page.keyboard.press("Shift+Space")
  await page.keyboard.press("Delete")
  await expect(bar(page)).toContainText("1 change — 1 deleted")
  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)[0].changes).toEqual([{ op: "delete", key: { id: "4" } }])
})

test("a role that may change rows and not destroy is offered no delete", async ({ page }) => {
  await mockEditor(page, { capabilities: ["read", "service.control"] })
  await page.goto(ITEMS)
  await expect(page.getByRole("button", { name: "Insert row" })).toBeVisible()
  await cell(page, 3, 1).click()
  await page.keyboard.press("Shift+Space")
  await page.keyboard.press("Delete")
  await expect(bar(page)).toHaveCount(0)
  await cell(page, 3, 1).click({ button: "right" })
  await expect(page.getByRole("menuitem", { name: /Delete/ })).toHaveCount(0)
  await page.keyboard.press("Escape")
  // The menu hands the keyboard back as it closes; the next key waits for that.
  await expect(page.getByRole("menu")).toHaveCount(0)
  await cell(page, 3, 1).click()
  await page.keyboard.press("Space")
  await expect(page.getByRole("complementary", { name: "Row" })).toContainText("Row 4")
  await expect(
    page.getByRole("complementary", { name: "Row" }).getByRole("button", { name: "Delete" }),
  ).toHaveCount(0)
  // Nor the rail's verbs that destroy.
  await page.getByRole("button", { name: "Actions for items" }).click()
  await expect(page.getByRole("menuitem", { name: "Empty…" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Drop…" })).toHaveCount(0)
})

/* ----------------------------------------------------- when it cannot edit */

for (const [name, options, path, reason] of [
  ["a view", {}, `${DATA}?schema=public&table=item_names`, "A view is read through its query"],
  [
    "an engine without change sets",
    { summaries: { 1: { capabilities: { changeSets: false, rowIdentity: "none" } } } },
    ITEMS,
    "PostgreSQL cannot change one row in place",
  ],
  [
    "a protected connection",
    { rows: { 1: { readOnly: true } } },
    ITEMS,
    "This connection is protected",
  ],
  ["a role that only reads", { viewer: true }, ITEMS, "Your role reads this table"],
] as const) {
  test(`${name} cannot be edited, and says why`, async ({ page }) => {
    const { sent } = await mockEditor(page, options)
    await page.goto(path)
    await expect(cell(page, 0, 1)).toHaveText("Item 1")
    await expect(grid(page)).toContainText(reason)
    await expect(page.getByRole("button", { name: "Insert row" })).toHaveCount(0)
    await cell(page, 0, 1).dblclick()
    await page.keyboard.insertText("x")
    await page.keyboard.press("Enter")
    await expect(bar(page)).toHaveCount(0)
    expect(sent.changes).toEqual([])
    if (name !== "a view" && name !== "an engine without change sets") {
      // No control that writes is drawn at all.
      await expect(page.getByRole("button", { name: "Import", exact: true })).toHaveCount(0)
      await expect(page.getByRole("link", { name: "New table" })).toHaveCount(0)
    }
  })
}

test("a table without a key is edited by its whole row, and says so", async ({ page }) => {
  const { sent } = await mockEditor(page, { details: { items: { primaryKey: [] } } })
  await page.goto(ITEMS)
  await expect(grid(page)).toContainText("A row is found by all of its values")
  await edit(page, 0, 1, "Renamed")
  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)[0].changes).toEqual([
    {
      op: "update",
      key: { id: "1", name: "Item 1", note: null, qty: "2" },
      values: { name: "Renamed" },
    },
  ])
})

/* -------------------------------------------------------- unsaved changes */

test("staged edits are asked about before the table, its rows or the editor are left", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  await edit(page, 0, 1, "Staged")
  const guard = page.getByRole("dialog", { name: "Unapplied changes" })

  // Another table.
  await page.getByRole("link", { name: /^other/ }).click()
  await expect(guard).toContainText("Opening other")
  await expect(title(page)).toContainText("items")
  await guard.getByRole("button", { name: "Keep editing" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=items")
  await expect(bar(page)).toContainText("1 change")

  // Another order of the same rows.
  const browses = sent.browse.length
  await page.getByRole("button", { name: "Sort", exact: true }).click()
  await page.getByRole("menuitem", { name: /^qty/ }).click()
  await expect(guard).toContainText("Sorting the rows")
  await page.keyboard.press("Escape")
  await expect(guard).toHaveCount(0)
  expect(sent.browse.length).toBe(browses)

  // A link out of the editor.
  await page
    .getByRole("navigation", { name: "Sidebar" })
    .getByRole("link", { name: "Settings" })
    .click()
  await expect(guard).toContainText("Leaving the table editor")
  await expect.poll(() => where(page)).toContain("/data")
  await guard.getByRole("button", { name: "Keep editing" }).click()

  // Other rows of the same table, asked for by the address alone: a link that
  // names the table with a condition, as a key pointing back into its own
  // table does. The rows under the staged set are not read again unasked.
  const held = sent.browse.length
  await page.evaluate(() =>
    window.history.pushState(
      null,
      "",
      "/databases/1/data?schema=public&table=items&filters=" +
        encodeURIComponent('[{"column":"id","op":"eq","value":"7"}]'),
    ),
  )
  await expect(guard).toContainText("Showing other rows")
  expect(sent.browse.length).toBe(held)
  await expect(cell(page, 0, 1)).toHaveText("Staged")
  await guard.getByRole("button", { name: "Keep editing" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=items")
  await expect(bar(page)).toContainText("1 change")

  // Letting them go carries on to where the reader was heading.
  await page.getByRole("link", { name: /^other/ }).click()
  await guard.getByRole("button", { name: "Discard and go on" }).click()
  await expect(title(page)).toContainText("other")
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)).toEqual([])
})

/* ------------------------------------------------------ selection, loading */

test("a ticked row is that row under another sort, and nothing of one table is drawn under another", async ({
  page,
}) => {
  const other = hold()
  const { sent } = await mockEditor(page, { browseHeld: { table: "other", until: other.until } })
  await page.goto(ITEMS)
  await cell(page, 0, 1).click()
  await page.keyboard.press("Shift+Space")
  await expect(page.locator('[data-row="0"]')).toHaveAttribute("aria-selected", "true")

  // Newest first: row 1 of the page is now id 250, and the tick is not on it.
  await page.getByRole("button", { name: "Sort", exact: true }).click()
  await page.getByRole("menuitem", { name: /^id/ }).click()
  await page.getByRole("button", { name: "Sort id descending instead" }).click()
  await expect(cell(page, 0, 0)).toHaveText("250")
  await expect(page.locator('[data-row="0"]')).not.toHaveAttribute("aria-selected", "true")

  // A table whose rows have not come: none of the last table's rows, no tick.
  await page.getByRole("link", { name: /^other/ }).click()
  await expect(title(page)).toContainText("other")
  await expect
    .poll(() => sent.browse.filter((url) => url.searchParams.get("table") === "other").length)
    .toBeGreaterThan(0)
  await expect(grid(page).locator("[data-row]")).toHaveCount(0)
  await expect(grid(page).getByText("Item 250")).toHaveCount(0)
  await page.keyboard.press("Delete")
  await expect(bar(page)).toHaveCount(0)
  other.release()
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  await expect(grid(page).locator("[data-row][aria-selected=true]")).toHaveCount(0)
  expect(sent.changes).toEqual([])
})

test("a large page costs what is on screen", async ({ page }) => {
  await mockEditor(page, { total: 1000 })
  await page.goto(ITEMS)
  await page.evaluate(() =>
    localStorage.setItem("jd.view.state.entry.databases.1.data.pageSize", "1000"),
  )
  await page.reload()
  await expect(foot(page)).toContainText("1,000 rows")
  // A thousand rows are on the page and a screenful of them is in the document.
  expect(await grid(page).locator("[data-row]").count()).toBeLessThan(120)
})

/* ------------------------------------------------------- export and import */

test("an export is saved by the page, which says when it is not the whole table", async ({
  page,
}) => {
  const { sent } = await mockEditor(page, {
    exported: {
      status: 200,
      body: "id,name\n1,Item 1\n",
      state: { status: "truncated", rows: 100000 },
    },
  })
  const filters = JSON.stringify([{ column: "note", op: "is_null" }])
  await page.goto(`${ITEMS}&filters=${encodeURIComponent(filters)}`)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")

  const download = page.waitForEvent("download")
  await page.getByRole("button", { name: "Export", exact: true }).click()
  await expect(page.getByRole("menu")).toContainText("The rows these filters match, up to 100,000")
  await page.getByRole("menuitem", { name: /^CSV/ }).click()
  expect((await download).suggestedFilename()).toBe("items.csv")

  // The export asks for the rows on screen and names itself, so its ending can be read.
  const asked = sent.exports[0]
  expect(asked.searchParams.get("filters")).toBe(filters)
  expect(asked.searchParams.get("format")).toBe("csv")
  expect(asked.searchParams.get("exportId")).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
  await expect(page.getByText("The export of items is not the whole table")).toBeVisible()
  await expect(page.getByRole("button", { name: "Export up to 1,000,000" })).toBeVisible()
})

test("an export the server refuses is an error on the page, not a page of JSON", async ({
  page,
}) => {
  await mockEditor(page, {
    exported: {
      status: 400,
      body: JSON.stringify({ error: { code: "bad_request", message: "the table cannot be read" } }),
    },
  })
  await page.goto(ITEMS)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  await page.getByRole("button", { name: "Export", exact: true }).click()
  await page.getByRole("menuitem", { name: /^CSV/ }).click()
  await expect(page.getByText("Could not export items")).toBeVisible()
  await expect(page.getByText("the table cannot be read")).toBeVisible()
  // The reader is where they were.
  expect(await where(page)).toBe("/databases/1/data?schema=public&table=items")
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
})

test("an import is previewed from the start of the file, mapped, and sent once", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  const browses = sent.browse.length
  await page.getByRole("button", { name: "Import", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Import into items" })
  await dialog.locator("input[type=file]").setInputFiles({
    name: "items.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("Name,Quantity\nImported one,7\n,9\n"),
  })

  // The dry run: what the server matched, the rows as they would be written.
  await expect(dialog.getByRole("combobox", { name: "Where Name goes" })).toContainText("name")
  await expect(dialog.getByRole("combobox", { name: "Where Quantity goes" })).toContainText(
    "Not imported",
  )
  await expect(dialog).toContainText("Imported one")
  await expect(dialog).toContainText("NULL")
  await expect(dialog).toContainText('INSERT INTO "public"."items"')
  expect(sent.imports.every((upload) => upload.options.dryRun === true)).toBe(true)

  // Mapping a column asks again, with the mapping spelled out.
  await dialog.getByRole("combobox", { name: "Where Quantity goes" }).click()
  await page.getByRole("option", { name: "qty", exact: true }).click()
  await expect
    .poll(() => sent.imports.at(-1)?.options.mapping)
    .toEqual({ Name: "name", Quantity: "qty" })

  await dialog.getByRole("button", { name: "Import", exact: true }).click()
  await expect(dialog).toContainText("Added")
  const real = sent.imports.filter((upload) => upload.options.dryRun !== true)
  expect(real).toHaveLength(1)
  expect(real[0].options).toMatchObject({
    schema: "public",
    table: "items",
    mode: "insert",
    mapping: { Name: "name", Quantity: "qty" },
  })
  expect(real[0].file).toContain("Imported one,7")
  await dialog.getByRole("button", { name: "Done" }).click()
  // The grid reads the rows again.
  await expect.poll(() => sent.browse.length).toBeGreaterThan(browses)
})

/* ------------------------------------------------------------ copy as SQL */

test("copy as INSERT asks the server for the statement of whole rows", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  await cell(page, 0, 1).click({ button: "right" })
  await page.getByRole("menuitem", { name: "Copy as" }).hover()
  await page.getByRole("menuitem", { name: /SQL INSERT/ }).click()
  await expect.poll(() => sent.rowsSql.length).toBe(1)
  // Every column of the row, by name, as the values the server sent.
  expect(sent.rowsSql[0]).toEqual({
    schema: "public",
    table: "items",
    rows: [{ id: "1", name: "Item 1", note: null, qty: "2" }],
  })
  await expect
    .poll(() => page.evaluate(() => navigator.clipboard.readText()))
    .toContain('INSERT INTO "public"."items"')
})

/* --------------------------------------------------- the design system */

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

async function keepsTheRules(page: Page, what: string) {
  expect(await unnamedControls(page), `unlabelled icon-only controls: ${what}`).toEqual([])
  expect(await offCentreText(page), `text off its row's centre line: ${what}`).toEqual([])
  expect(await filledPills(page), `a filled pill on the page: ${what}`).toEqual([])
  const registers = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("[data-slot='page']")].map(
      (el) => el.dataset.register ?? "(unset)",
    ),
  )
  expect(registers, `the register of the page: ${what}`).toEqual(["reading"])
  expect(
    await page.locator("[data-slot='flow-panel']").count(),
    `a reading page drew a flow panel: ${what}`,
  ).toBe(0)
  const sideways = await page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways, `scrolls sideways: ${what}`).toBe(false)
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the table editor keeps the design system's rules${label}`, async ({ page }) => {
    test.setTimeout(90_000)
    await page.setViewportSize(viewport)
    await mockEditor(page)
    const filters = JSON.stringify([
      { column: "note", op: "is_null" },
      { column: "qty", op: "eq", value: "2" },
    ])
    const sort = JSON.stringify([{ column: "id", desc: true }])

    await page.goto(`${DATA}?schema=public`)
    await expect(page.getByText("Pick a table")).toBeVisible()
    await keepsTheRules(page, "no table open")

    await page.goto(
      `${ITEMS}&filters=${encodeURIComponent(filters)}&sort=${encodeURIComponent(sort)}`,
    )
    await expect(cell(page, 0, 1)).toHaveText("Item 1")
    await keepsTheRules(page, "rows, filtered and sorted")

    await page.getByRole("button", { name: "Add filter" }).click()
    await expect(page.getByRole("combobox", { name: "Column" })).toBeVisible()
    await keepsTheRules(page, "the filter form")
    await page.keyboard.press("Escape")

    await cell(page, 0, 1).click()
    await page.getByRole("button", { name: "Show the row" }).click()
    await expect(page.getByRole("complementary", { name: "Row" })).toContainText("Row 1")
    await keepsTheRules(page, "the row inspector")
    await page
      .getByRole("complementary", { name: "Row" })
      .getByRole("button", { name: "Hide the row" })
      .click()

    await edit(page, 0, 1, "Staged")
    await expect(bar(page)).toBeVisible()
    await keepsTheRules(page, "the change bar")
    await bar(page).getByRole("button", { name: "Review", exact: true }).click()
    await expect(page.getByRole("dialog", { name: "Apply 1 change to items" })).toBeVisible()
    await keepsTheRules(page, "the review")
    await page.keyboard.press("Escape")
    await bar(page).getByRole("button", { name: "Discard", exact: true }).click()

    await page.getByRole("tab", { name: "Structure" }).click()
    await expect(page.getByRole("region", { name: "Columns" })).toBeVisible()
    await keepsTheRules(page, "the structure")
    await page.getByRole("tab", { name: "Definition" }).click()
    // The editor's own "loading" mark is not this page's to answer for.
    await expect(page.locator("[data-slot=code-view] .monaco-editor").first()).toBeVisible({
      timeout: 20_000,
    })
    await keepsTheRules(page, "the definition")

    await page.goto(`${DATA}?schema=public&table=item_names`)
    await expect(grid(page)).toContainText("A view is read through its query")
    await keepsTheRules(page, "a read-only view")

    await page.goto(ITEMS)
    await page.getByRole("button", { name: "Import", exact: true }).click()
    await expect(page.getByRole("dialog", { name: "Import into items" })).toBeVisible()
    await keepsTheRules(page, "the import dialog")

    // A document in the row panel, with the row a new key is typed in.
    await page.goto(`${DATA}?schema=public&table=docs`)
    await cell(page, 0, 1).click()
    await page.getByRole("button", { name: "Show the row" }).click()
    const tree = field(page, "meta").locator("[data-slot=json-doc-tree]")
    await expect(tree).toContainText("9007199254740993")
    await keepsTheRules(page, "a document as a tree")
    await tree.getByRole("button", { name: "Add a key to meta" }).click()
    await expect(page.getByRole("textbox", { name: "New key" })).toBeVisible()
    await keepsTheRules(page, "a key being added to a document")
  })
}
