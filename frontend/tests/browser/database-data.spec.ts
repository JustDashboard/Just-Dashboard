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

/** A table of the types the row panel has to open as what the column holds. */
const TYPED_COLUMNS = [
  { name: "id", type: "integer", nullable: false, position: 1 },
  { name: "bits", type: "bit(8)", nullable: true, position: 2 },
  { name: "seen", type: "timestamp without time zone", nullable: true, position: 3 },
]

type EditorMock = DatabaseMock & {
  /** How many rows `items` holds. */
  total?: number
  /** The rows `items` holds, in place of the generated ones. */
  held?: Cell[][]
  /** What `POST /import/upload` answers when it is not a dry run. */
  imported?: { status: number; body: unknown }
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
  const all = options.held ?? rowsOf(options.total ?? 250)
  const total = all.length

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
      const typed = table === "typed" ? { columns: TYPED_COLUMNS } : {}
      return json(
        route,
        detailOf(table, { ...view, ...docs, ...typed, ...options.details?.[table] }),
      )
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
      if (table === "typed") {
        return json(route, {
          columns: ["id", "bits", "seen"],
          types: ["INT4", "BIT", "TIMESTAMP"],
          kinds: ["integer", "binary", "datetime"],
          rows: [["1", "10101010", "2026-01-02T03:04:05.5Z"]],
          rowCount: 1,
          rowsAffected: 0,
          duration: "1ms",
          truncated: false,
          statement: 'SELECT * FROM "public"."typed" ORDER BY "id" ASC LIMIT $1 OFFSET $2',
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
      // "Any of these": the first condition alone stands for the set here.
      if (url.searchParams.get("match") === "any" && filters[0]?.op === "icontains") {
        const word = (filters[0].value ?? "").toLowerCase()
        rows = all.filter((row) => String(row[1]).toLowerCase().includes(word))
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
      if (!parsed.dryRun && options.imported) {
        return json(route, options.imported.body, options.imported.status)
      }
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
        // A table the import makes: named as asked, of the columns that go in.
        create: parsed.createTable
          ? {
              statement: `CREATE TABLE "public"."${String(parsed.table)}" (\n  ${Object.values(
                mapping,
              )
                .filter(Boolean)
                .map((name) => `"${name}" text`)
                .join(",\n  ")}\n)`,
              columns: [],
              created: !parsed.dryRun,
            }
          : undefined,
        atomic: true,
        warnings: [],
      })
    }
    if (rest.startsWith("/ddl/")) {
      // What a structure change would run, and that it ran.
      const body = request.postDataJSON() as Record<string, unknown>
      const statement = `TRUNCATE TABLE "public"."${String(body.table)}"`
      return json(route, { statement, statements: [statement] })
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
  // Said once, with the way on — and nothing is offered to be made in it: the
  // page used to call it an empty schema and offer "New table" there.
  await expect(page.getByText("No schema called dropped")).toHaveCount(1)
  await expect(page.getByText("No tables yet")).toHaveCount(0)
  await expect(page.getByRole("link", { name: "New table" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Import a file as a new table" })).toHaveCount(0)
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
  // Nor the rail's verbs that destroy. (At this width the rail stood aside
  // for the row; asking for it back puts the row away.)
  await page.getByRole("button", { name: "Show the tables" }).click()
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

/* ------------------------------------------------- narrow frames, a phone */

/** Counts the times the document itself was asked for again: a link the router did not handle. */
function documentReads(page: Page) {
  const read = { count: 0 }
  page.on("request", (request) => {
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) read.count++
  })
  return read
}

test("on a phone, choosing a table in the rail keeps the page, and staged edits are asked about", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockEditor(page)
  await page.goto(ITEMS)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  const reads = documentReads(page)
  await page.evaluate(() => Object.assign(window, { __kept: true }))
  const rail = page.getByRole("navigation", { name: "tables of this schema" })

  // The rail lies over the table and is put away by choosing one — by the
  // router, in this document. It used to be taken off the page before its
  // link had acted, and the browser followed the link itself.
  await page.getByRole("button", { name: "Show the tables" }).click()
  await rail.getByRole("link", { name: /^other/ }).click()
  await expect(title(page)).toContainText("other")
  await expect(rail).toHaveCount(0)
  expect(reads.count).toBe(0)
  expect(await page.evaluate(() => "__kept" in window)).toBe(true)

  // With an edit staged the same press is asked about by the page, not by
  // the browser's own "leave site?".
  await edit(page, 0, 1, "Staged")
  await expect(bar(page)).toContainText("1 change")
  await page.getByRole("button", { name: "Show the tables" }).click()
  await rail.getByRole("link", { name: /^items/ }).click()
  const guard = page.getByRole("dialog", { name: "Unapplied changes" })
  await expect(guard).toContainText("Opening items")
  await guard.getByRole("button", { name: "Keep editing" }).click()
  await expect(title(page)).toContainText("other")
  await expect(bar(page)).toContainText("1 change")
  expect(reads.count).toBe(0)
  await bar(page).getByRole("button", { name: "Discard", exact: true }).click()

  // Another schema is a place with no table open in it yet: the rail stays
  // up to choose one from, and the page is still this page.
  await page.getByRole("button", { name: "Show the tables" }).click()
  await rail.getByRole("button", { name: "schema: public" }).click()
  await page.getByRole("menuitem", { name: /^sales/ }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=sales")
  await expect(rail).toBeVisible()
  expect(reads.count).toBe(0)
  expect(await page.evaluate(() => "__kept" in window)).toBe(true)
})

test("a row opened in a frame too narrow for three columns takes the rail's place, or lies over the table", async ({
  page,
}) => {
  await mockEditor(page)
  const rail = page.getByRole("navigation", { name: "tables of this schema" })
  const panel = page.getByRole("complementary", { name: "Row" })
  const frame = page.locator("[data-slot=table-editor]")
  const widthOf = async (target: ReturnType<Page["locator"]>) =>
    (await target.boundingBox())?.width ?? 0

  // 1280: the rail and the panel together would leave the rows a strip. The
  // rail steps aside while the row is open and comes back when it closes.
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto(ITEMS)
  await expect(rail).toBeVisible()
  await cell(page, 1, 1).click()
  await page.keyboard.press("Space")
  await expect(panel).toContainText("Row 2")
  await expect(rail).toHaveCount(0)
  expect(await widthOf(grid(page))).toBeGreaterThanOrEqual(640)
  // Asking for the tables puts the row away: there is room for one of them.
  await page.getByRole("button", { name: "Show the tables" }).click()
  await expect(rail).toBeVisible()
  await expect(panel).toHaveCount(0)
  await cell(page, 1, 1).click()
  await page.keyboard.press("Space")
  await panel.getByRole("button", { name: "Hide the row" }).click()
  await expect(rail).toBeVisible()

  // 1024: not even the panel alone leaves the rows their floor, so it lies
  // over the table as it does on a phone, and the rail stays where it was.
  await page.setViewportSize({ width: 1024, height: 768 })
  await cell(page, 1, 1).click()
  await page.keyboard.press("Space")
  await expect(panel).toContainText("Row 2")
  expect(await widthOf(panel)).toBeGreaterThan((await widthOf(frame)) - 4)
  await panel.getByRole("button", { name: "Hide the row" }).click()
  await expect(rail).toBeVisible()

  // 1720: all three, side by side.
  await page.setViewportSize({ width: 1720, height: 900 })
  await cell(page, 1, 1).click()
  await page.keyboard.press("Space")
  await expect(panel).toBeVisible()
  await expect(rail).toBeVisible()
  expect(await widthOf(grid(page))).toBeGreaterThanOrEqual(640)
})

test("the strip names the table and the toolbar shows every condition, at the widths between", async ({
  page,
}) => {
  await mockEditor(page)
  const filters = JSON.stringify([
    { column: "note", op: "is_null" },
    { column: "qty", op: "gte", value: "2" },
    { column: "name", op: "contains", value: "Item with a long word in it" },
  ])
  const sort = JSON.stringify([
    { column: "id", desc: true },
    { column: "qty", desc: false },
  ])
  const frame = page.locator("[data-slot=table-editor]")
  for (const width of [820, 900, 1024, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto(
      `${ITEMS}&filters=${encodeURIComponent(filters)}&sort=${encodeURIComponent(sort)}`,
    )
    await expect(grid(page)).toBeVisible()
    // The name is there to read: it used to give way to nothing while the
    // schema before it stayed.
    const name = await title(page).getByText("items", { exact: true }).boundingBox()
    expect(name?.width ?? 0, `the table's name at ${width}`).toBeGreaterThanOrEqual(30)
    // Every chip of what filters and orders the rows is inside the frame.
    const edge = await frame.boundingBox()
    for (const chip of await page.getByRole("group", { name: /^(Filter:|Sorted by)/ }).all()) {
      await expect(chip).toBeVisible()
      const box = await chip.boundingBox()
      expect(
        (box?.x ?? 0) + (box?.width ?? 0),
        `a chip runs past the frame at ${width}`,
      ).toBeLessThanOrEqual((edge?.x ?? 0) + (edge?.width ?? 0))
    }
    await expect(page.getByRole("group", { name: /^Sorted by/ })).toHaveCount(2)
    await expect(page.getByRole("group", { name: "Rows match" })).toBeVisible()
  }
})

/* ------------------------------------------------- writes to the address */

test("of two writes made before the first has landed, the second stands", async ({ page }) => {
  await mockEditor(page)
  await page.goto(ITEMS)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  // The router's own reads are held back, so a second press lands while the
  // first write to the address is still on its way.
  await page.route("**/*", async (route) => {
    const request = route.request()
    if (request.headers()["rsc"] === "1" || request.url().includes("_rsc=")) {
      await new Promise((resolve) => setTimeout(resolve, 700))
    }
    await route.fallback()
  })

  // Structure, then Data at once: the second asks for the address the router
  // still shows, and used to be lost to the first landing after it.
  await page.getByRole("tab", { name: "Structure" }).click()
  await page.getByRole("tab", { name: "Data" }).click()
  await page.waitForTimeout(2500)
  await expect(page.getByRole("tab", { name: "Data" })).toHaveAttribute("aria-selected", "true")
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=items")

  // A filter applied and removed at once stays removed.
  await page.getByRole("button", { name: "Filter", exact: true }).click()
  await page.getByRole("combobox", { name: "Column" }).click()
  await page.getByRole("option", { name: "note", exact: true }).click()
  await page.getByRole("combobox", { name: "Condition" }).click()
  await page.getByRole("option", { name: "is NULL", exact: true }).click()
  await page.getByRole("button", { name: "Apply", exact: true }).click()
  await page.getByRole("button", { name: /^Remove the filter/ }).click()
  await page.waitForTimeout(2500)
  await expect(page.getByRole("group", { name: /^Filter:/ })).toHaveCount(0)
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=items")

  // And two writes that both move on still end on the second.
  await page.getByRole("button", { name: "Next page" }).click()
  await page.getByRole("button", { name: "Next page" }).click()
  await expect.poll(() => where(page)).toContain("page=3")
  await expect(cell(page, 0, 0)).toHaveText("201")
})

/* ------------------------------------------------ keyboard and its focus */

const inGrid = (page: Page) =>
  page.evaluate(() => document.activeElement?.closest("[data-slot=data-grid]") != null)

test("closing a dialog hands the keyboard back to where the reader was working", async ({
  page,
}) => {
  await mockEditor(page)
  await page.goto(ITEMS)
  await edit(page, 0, 1, "Staged")

  // The review, opened by the shortcut from a cell: back to the rows.
  await page.keyboard.press("ControlOrMeta+s")
  const review = page.getByRole("dialog", { name: "Apply 1 change to items" })
  await expect(review).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(review).toHaveCount(0)
  await expect.poll(() => inGrid(page)).toBe(true)

  // Opened by its button: back to the button, which stayed a button while
  // the statements were fetched.
  const reviewButton = bar(page).getByRole("button", { name: "Review", exact: true })
  await reviewButton.focus()
  await page.keyboard.press("Enter")
  await expect(review).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(review).toHaveCount(0)
  await expect(reviewButton).toBeFocused()

  // The question before leaving.
  await page.getByRole("link", { name: /^other/ }).click()
  const guard = page.getByRole("dialog", { name: "Unapplied changes" })
  await expect(guard).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(guard).toHaveCount(0)
  await expect.poll(() => inGrid(page)).toBe(true)

  // Discard takes the bar away, and the button with it.
  await bar(page).getByRole("button", { name: "Discard", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  await expect.poll(() => inGrid(page)).toBe(true)

  // A confirmation asked for from a row of the rail: back to that row's menu.
  const actions = page.getByRole("button", { name: "Actions for items" })
  await actions.click()
  await page.getByRole("menuitem", { name: "Empty…" }).click()
  const confirm = page.getByRole("dialog", { name: "Empty table" })
  await expect(confirm).toContainText('TRUNCATE TABLE "public"."items"')
  await page.keyboard.press("Escape")
  await expect(confirm).toHaveCount(0)
  await expect(actions).toBeFocused()
})

test("the views of a table are one stop of the keyboard, moved along with the arrows", async ({
  page,
}) => {
  await mockEditor(page)
  await page.goto(ITEMS)
  const tab = (name: string) => page.getByRole("tab", { name })
  await expect(tab("Data")).toHaveAttribute("tabindex", "0")
  await expect(tab("Structure")).toHaveAttribute("tabindex", "-1")
  await tab("Data").focus()
  await page.keyboard.press("ArrowRight")
  await expect(tab("Structure")).toBeFocused()
  await expect(tab("Structure")).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("tabpanel", { name: "Structure" })).toContainText("Columns")
  await page.keyboard.press("End")
  await expect(tab("Definition")).toHaveAttribute("aria-selected", "true")
  await page.keyboard.press("ArrowRight")
  await expect(tab("Data")).toBeFocused()
  await expect(page.getByRole("tabpanel", { name: "Data" })).toBeVisible()
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=items")
})

/* --------------------------------------------------------- the row panel */

test("the row panel shows the row it names, not what was typed on the one before", async ({
  page,
}) => {
  // Two rows that hold the same quantity: the field used to tell a new row
  // only by a new value, and kept the refused draft of the first over the second.
  await mockEditor(page, {
    held: [
      ["1", "First", null, "4"],
      ["2", "Second", null, "4"],
    ],
  })
  await page.goto(ITEMS)
  await cell(page, 0, 1).click()
  await page.keyboard.press("Space")
  const panel = page.getByRole("complementary", { name: "Row" })
  const qty = field(page, "qty").getByRole("textbox")
  await qty.fill("abc")
  await qty.press("Enter")
  await expect(field(page, "qty").getByRole("alert")).toContainText("Whole numbers only")
  await panel.getByRole("button", { name: "Next row" }).click()
  await expect(panel).toContainText("Row 2")
  await expect(qty).toHaveValue("4")
  await expect(field(page, "qty").getByRole("alert")).toHaveCount(0)
  await expect(bar(page)).toHaveCount(0)
})

test("a field opens on what its column holds: a bit string as text, a zone-less moment without a zone", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(`${DATA}?schema=public&table=typed`)
  await cell(page, 0, 1).click()
  await page.keyboard.press("Space")
  // The column stores no zone: the field does not show one to be kept.
  await expect(field(page, "seen").getByRole("textbox")).toHaveValue("2026-01-02 03:04:05.5")
  // A string of bits is typed as one. As bytes it went out as \x11110000,
  // which the engine refuses.
  const bits = field(page, "bits").getByRole("textbox")
  await expect(bits).toHaveValue("10101010")
  await bits.fill("11110000")
  await bits.press("Enter")
  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)[0].changes).toEqual([
    { op: "update", key: { id: "1", bits: "10101010" }, values: { bits: "11110000" } },
  ])
})

test("a foreign key is chosen from the rows it can point at", async ({ page }) => {
  const { sent } = await mockEditor(page, {
    details: {
      items: {
        foreignKeys: [
          { name: "items_qty_fkey", columns: ["qty"], refTable: "other", refColumns: ["id"] },
        ],
      },
    },
  })
  await page.goto(ITEMS)
  await cell(page, 0, 1).click()
  await page.keyboard.press("Space")
  await field(page, "qty").getByRole("button", { name: "Choose" }).click()
  const rows = page.getByRole("listbox", { name: "Rows of other" })
  // The row the key points at now stands first, marked.
  await expect(rows.getByRole("option").first()).toContainText("Item 2")
  await expect(rows.getByRole("option", { selected: true })).toHaveCount(1)

  // A word is looked for in the referenced table itself, by the server.
  await page.getByRole("textbox", { name: "Find a row of other" }).fill("item 17")
  await expect(rows.getByRole("option")).toHaveCount(11)
  const asked = sent.browse.filter((url) => url.searchParams.get("table") === "other").at(-1)
  expect(asked?.searchParams.get("match")).toBe("any")
  expect(JSON.parse(asked?.searchParams.get("filters") ?? "[]")).toEqual([
    { column: "name", op: "icontains", value: "item 17" },
    { column: "note", op: "icontains", value: "item 17" },
  ])
  // A number is the key itself, asked for whole and listed first.
  await page.getByRole("textbox", { name: "Find a row of other" }).fill("41")
  await expect(rows.getByRole("option").first()).toContainText("Item 41")

  await page.keyboard.press("ArrowDown")
  await page.keyboard.press("Enter")
  await expect(rows).toHaveCount(0)
  await expect(field(page, "qty").getByRole("textbox")).toHaveValue("41")
  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)[0].changes).toEqual([
    { op: "update", key: { id: "1", qty: "2" }, values: { qty: "41" } },
  ])
})

test("a key of a document is renamed and an item moved, and the rest goes back as it came", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(`${DATA}?schema=public&table=docs`)
  await cell(page, 0, 1).click()
  await page.keyboard.press("Space")
  const tree = field(page, "meta").locator("[data-slot=json-doc-tree]")

  // A name the object already has is refused where it is typed.
  await tree.getByRole("button", { name: "Rename the key theme" }).click()
  await page.keyboard.insertText("big")
  await page.keyboard.press("Enter")
  await expect(field(page, "meta").getByRole("alert")).toContainText("already has a key called big")
  await page.keyboard.press("ControlOrMeta+a")
  await page.keyboard.insertText("mode")
  await page.keyboard.press("Enter")
  await expect(tree.getByRole("button", { name: "Rename the key mode" })).toBeVisible()

  // Two items, and the second moved before the first.
  await tree.getByRole("button", { name: "Add an item to tags" }).click()
  await page.getByRole("textbox", { name: "New value" }).fill("b")
  await page.keyboard.press("Enter")
  await tree.getByRole("button", { name: "Move item 1 up" }).click()

  await bar(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  expect(applied(sent)[0].changes).toEqual([
    {
      op: "update",
      key: { id: "1", meta: DOCUMENT },
      values: { meta: '{"mode": "dark", "big": 9007199254740993, "tags": ["b", "a"]}' },
    },
  ])
})

/* ------------------------------------------------- import, the other ways */

test("a refused import is said beside the button that was pressed", async ({ page }) => {
  await mockEditor(page, {
    imported: {
      status: 400,
      body: {
        error: {
          code: "bad_request",
          message: 'row 2 (line 3): null value in column "name" — nothing was imported',
        },
      },
    },
  })
  await page.setViewportSize({ width: 1280, height: 720 })
  await page.goto(ITEMS)
  await page.getByRole("button", { name: "Import", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Import into items" })
  await dialog.locator("input[type=file]").setInputFiles({
    name: "items.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("Name,Quantity\nImported one,7\n,9\n"),
  })
  const statement = dialog.getByText('INSERT INTO "public"."items"')
  await expect(statement).toBeVisible()
  // The reader is at the end of the preview, where the mapping was checked.
  await statement.scrollIntoViewIfNeeded()
  await dialog.getByRole("button", { name: "Import", exact: true }).click()
  const refusal = dialog.getByRole("alert")
  await expect(refusal).toContainText("nothing was imported")
  await expect(refusal).toBeInViewport({ ratio: 1 })
  // And the dialog is still the dialog it was: the file can be sent again.
  await expect(dialog.getByRole("button", { name: "Import", exact: true })).toBeEnabled()
})

test("an upsert says what makes two rows the same, and a file becomes a new table", async ({
  page,
}) => {
  const { sent } = await mockEditor(page, {
    details: {
      items: {
        indexes: [
          { name: "items_pkey", columns: ["id"], unique: true, primary: true },
          { name: "items_name_key", columns: ["name"], unique: true, primary: false },
          { name: "items_note_idx", columns: ["note"], unique: false, primary: false },
        ],
      },
    },
  })
  const file = {
    name: "Spare Parts.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("Name,Quantity\nImported one,7\n"),
  }
  await page.goto(ITEMS)
  await page.getByRole("button", { name: "Import", exact: true }).click()
  const into = page.getByRole("dialog", { name: "Import into items" })
  await into.locator("input[type=file]").setInputFiles(file)
  await into.getByRole("radio", { name: "Add or update" }).click()
  // The primary key unless the reader says otherwise; a unique index is offered, a plain one not.
  await expect.poll(() => sent.imports.at(-1)?.options.mode).toBe("upsert")
  expect(sent.imports.at(-1)?.options.conflict).toBeUndefined()
  await into.getByRole("combobox", { name: "What rows are matched by" }).click()
  await expect(page.getByRole("option")).toHaveCount(2)
  await page.getByRole("option", { name: /items_name_key/ }).click()
  await expect.poll(() => sent.imports.at(-1)?.options.conflict).toEqual({ columns: ["name"] })
  await page.keyboard.press("Escape")
  await expect(into).toHaveCount(0)

  // The same file as a table of its own, from the rail.
  await page.getByRole("button", { name: "Import a file as a new table" }).click()
  const made = page.getByRole("dialog", { name: "Import a file as a new table" })
  await made.locator("input[type=file]").setInputFiles(file)
  const name = made.getByRole("textbox", { name: "Name of the new table" })
  await expect(name).toHaveValue("spare_parts")
  await expect(made).toContainText('CREATE TABLE "public"."spare_parts"')
  // A name the schema already uses is refused where it is typed.
  await name.fill("items")
  await expect(made).toContainText("already has a table called items")
  await expect(made.getByRole("button", { name: "Create and import" })).toBeDisabled()
  await name.fill("spare_parts")
  // A column is renamed, retyped and made the key before anything exists.
  await made.getByRole("textbox", { name: "The column made of Quantity" }).fill("qty")
  await made.getByRole("textbox", { name: "The type of qty" }).fill("integer")
  await made.getByRole("checkbox", { name: "qty is part of the primary key" }).click()
  await expect
    .poll(() => sent.imports.at(-1)?.options)
    .toMatchObject({
      table: "spare_parts",
      dryRun: true,
      mapping: { Name: "name", Quantity: "qty" },
      createTable: {
        columns: [{ name: "qty", type: "integer", notNull: true, primaryKey: true }],
      },
    })
  await expect(made.getByRole("button", { name: "Create and import" })).toBeEnabled()
  await made.getByRole("button", { name: "Create and import" }).click()
  await expect(made).toContainText("created")
  const real = sent.imports.filter((upload) => upload.options.dryRun !== true)
  expect(real).toHaveLength(1)
  expect(real[0].options).toMatchObject({ table: "spare_parts", mode: "insert" })
  expect(real[0].file).toContain("Imported one,7")
  // Done opens the table that was made.
  await made.getByRole("button", { name: "Done" }).click()
  await expect.poll(() => where(page)).toBe("/databases/1/data?schema=public&table=spare_parts")
})

test("an export can be asked for past the usual number of rows before it runs", async ({
  page,
}) => {
  const { sent } = await mockEditor(page)
  await page.goto(ITEMS)
  await expect(cell(page, 0, 1)).toHaveText("Item 1")
  await page.getByRole("button", { name: "Export", exact: true }).click()
  await page.getByRole("menuitemcheckbox", { name: "Up to 1,000,000 rows" }).click()
  await expect(page.getByRole("menu")).toContainText("up to 1,000,000")
  await page.getByRole("menuitem", { name: /^CSV/ }).click()
  await expect.poll(() => sent.exports.length).toBe(1)
  expect(sent.exports[0].searchParams.get("limit")).toBe("1000000")
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
    await mockEditor(page, {
      details: {
        items: {
          foreignKeys: [
            { name: "items_qty_fkey", columns: ["qty"], refTable: "other", refColumns: ["id"] },
          ],
        },
      },
    })
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
    await field(page, "qty").getByRole("button", { name: "Choose" }).click()
    await expect(page.getByRole("listbox", { name: "Rows of other" })).toContainText("Item 2")
    await keepsTheRules(page, "the rows a foreign key can point at")
    await page.keyboard.press("Escape")
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
    await page.keyboard.press("Escape")

    // A file as a table of its own, with the columns it would be made of.
    if (viewport.width < 640) await page.getByRole("button", { name: "Show the tables" }).click()
    await page.getByRole("button", { name: "Import a file as a new table" }).click()
    const made = page.getByRole("dialog", { name: "Import a file as a new table" })
    await made.locator("input[type=file]").setInputFiles({
      name: "parts.csv",
      mimeType: "text/csv",
      buffer: Buffer.from("Name,Quantity\nImported one,7\n"),
    })
    await expect(made).toContainText('CREATE TABLE "public"."parts"')
    await keepsTheRules(page, "a file as a new table")
    await page.keyboard.press("Escape")

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
