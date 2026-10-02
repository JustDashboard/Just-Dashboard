import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { hold, mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * The SQL editor (`/databases/<id>/query`), find-a-value (`…/search`) and code
 * generation (`…/generate`).
 *
 * Much of what is checked is a defect the pages these replace shipped: a
 * statement that destroys run on the strength of whatever was classified
 * last, a result cut at 500 rows and exported as if it were whole, a
 * statement handed over in the address written over the reader's draft, a
 * result grid that drew every row, and a completion that went on offering
 * the last connection's tables. The API is mocked in the browser; what the
 * requests carry is what is asserted.
 *
 * The second half is what a reviewer found by using the pages: a statement
 * sent twice by two presses of the run key, a script's other results thrown
 * away by fetching more of one, a completed name left with two closing
 * quotes, an editor the keyboard could not leave, and the files a generator
 * wrote drawn below the fold.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

type Cell = string | number | boolean | null

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const OUTLINES: Record<
  number,
  { schema: string; tables: Record<string, string[]>; kinds?: Record<string, string> }
> = {
  1: {
    schema: "public",
    tables: {
      customers: ["id", "email", "tier"],
      orders: ["id", "customer_id", "status", "total_cents"],
      "Mixed Case Table": ["Id", "select"],
      order_summary: ["id", "email"],
    },
    kinds: { order_summary: "view" },
  },
  7: { schema: "main", tables: { notes: ["id", "title", "body"], notebooks: ["id", "title"] } },
}

const READ = { destructive: false, level: "read", reasons: [] as string[] }
const WRITE = { destructive: false, level: "medium", reasons: ["inserts rows"] }
const DROP = { destructive: true, level: "critical", reasons: ["drops a database object"] }

/** The mock's reading of a statement, by its first word — the server's job, stood in for. */
function riskOf(sql: string) {
  const verb = /^\s*([a-z]+)/i.exec(sql)?.[1]?.toLowerCase() ?? ""
  if (["drop", "delete", "truncate", "alter", "update"].includes(verb)) return DROP
  if (["insert", "create"].includes(verb)) return WRITE
  return READ
}

const split = (text: string) =>
  text
    .split(";")
    .map((part) => part.trim())
    .filter(Boolean)

const lineOf = (text: string, sql: string) => text.slice(0, text.indexOf(sql)).split("\n").length

function resultOf(sql: string, maxRows: number) {
  const written = /^\s*(insert|update|delete|drop|create|alter)/i.test(sql)
  if (written) {
    return {
      columns: [],
      types: [],
      rows: [],
      rowCount: 0,
      rowsAffected: 3,
      duration: "2ms",
      truncated: false,
      statement: sql,
    }
  }
  // "many" stands for a statement that returns more rows than any limit.
  const total = /many/.test(sql) ? 20_000 : /count/.test(sql) ? 1 : 3
  const shown = Math.min(total, maxRows)
  const rows: Cell[][] = Array.from({ length: shown }, (_, i) => [
    String(BigInt("9007199254740993") + BigInt(i)),
    i % 2 === 0 ? `user${i}@example.com` : null,
    i === 0 ? "" : "pro",
  ])
  return {
    columns: ["id", "email", "tier"],
    types: ["INT8", "TEXT", "TEXT"],
    kinds: ["integer", "text", "text"],
    rows,
    rowCount: shown,
    rowsAffected: 0,
    duration: "1.5ms",
    truncated: total > maxRows,
    statement: sql,
  }
}

const PLAN = [
  {
    Plan: {
      "Node Type": "Limit",
      "Total Cost": 498.64,
      "Plan Rows": 10,
      Plans: [
        {
          "Node Type": "Hash Join",
          "Total Cost": 459.71,
          "Plan Rows": 1800,
          "Hash Cond": "(o.customer_id = c.id)",
          Plans: [
            {
              "Node Type": "Seq Scan",
              "Relation Name": "orders",
              Alias: "o",
              "Total Cost": 376,
              "Plan Rows": 9000,
            },
            {
              "Node Type": "Index Scan",
              "Relation Name": "customers",
              Alias: "c",
              "Index Name": "customers_pkey",
              "Total Cost": 57,
              "Plan Rows": 240,
              Filter: "(tier = 'pro'::customer_tier)",
            },
          ],
        },
      ],
    },
  },
]

const measured = (plan: typeof PLAN) => {
  const walk = (node: Record<string, unknown>): Record<string, unknown> => ({
    ...node,
    "Actual Rows": node["Plan Rows"],
    "Actual Total Time": 2.5,
    "Actual Loops": 1,
    Plans: Array.isArray(node.Plans) ? node.Plans.map(walk) : undefined,
  })
  return [{ Plan: walk(plan[0].Plan), "Planning Time": 0.4, "Execution Time": 2.6 }]
}

const TARGETS = {
  targets: [
    {
      id: "prisma",
      label: "Prisma",
      filename: "schema.prisma",
      description: "A schema.prisma to drop into an existing Prisma project.",
      language: "prisma",
      group: "ORM",
      engines: ["postgres", "mysql", "sqlite", "sqlserver"],
      unsupported: { clickhouse: "Prisma has no connector for ClickHouse." },
      options: [
        {
          id: "relations",
          label: "Relations",
          description: "Turn foreign keys into relations.",
          type: "boolean",
          default: true,
        },
        {
          id: "naming",
          label: "Naming",
          description: "Keep the database's own names, or use camelCase.",
          type: "select",
          default: "preserve",
          choices: [
            { value: "preserve", label: "As in the database" },
            { value: "camel", label: "camelCase" },
          ],
        },
      ],
    },
    {
      id: "typeorm",
      label: "TypeORM",
      filename: "entities.ts",
      description: "Entity classes with decorators.",
      language: "typescript",
      group: "ORM",
      engines: ["postgres", "mysql", "sqlite"],
      unsupported: {},
      options: [
        {
          id: "split",
          label: "One file per model",
          description: "Write each model to its own file.",
          type: "boolean",
          default: false,
        },
      ],
    },
    {
      id: "diesel",
      label: "Diesel",
      filename: "schema.rs",
      description: "Diesel's table! schema.",
      language: "rust",
      group: "ORM",
      engines: ["mysql"],
      unsupported: {
        postgres: "Diesel is not written for this server in this mock, and says why.",
      },
      options: [],
    },
    {
      id: "gorm",
      label: "GORM",
      filename: "models.go",
      description: "Go structs with gorm tags.",
      language: "go",
      group: "ORM",
      engines: ["postgres"],
      unsupported: {},
      options: [
        {
          id: "package",
          label: "Package",
          description: "The Go package the file declares.",
          type: "text",
          default: "models",
        },
      ],
    },
    {
      id: "zod",
      label: "Zod schemas",
      filename: "schemas.ts",
      description: "Runtime validators.",
      language: "typescript",
      group: "Types & validation",
      engines: ["postgres"],
      unsupported: {},
      options: [],
    },
    {
      id: "sql",
      label: "SQL",
      filename: "schema.sql",
      description: "CREATE statements.",
      language: "sql",
      group: "Schema",
      engines: ["postgres"],
      unsupported: {},
      options: [],
    },
  ],
}

type WorkMock = DatabaseMock & {
  /** The session's capabilities, when neither an admin's nor a viewer's. */
  capabilities?: string[]
  /** `POST /classify` fails. */
  classifyFails?: boolean
  /** `POST /classify` answers only once this settles. */
  classifyHeld?: Promise<unknown>
  /** `POST /query` of a statement holding "slow" answers only once this settles. */
  slowHeld?: Promise<unknown>
  /** `GET /search` answers only once this settles. */
  searchHeld?: Promise<unknown>
  /** What `GET /history` holds to begin with. */
  history?: Record<string, unknown>[]
  saved?: { id: number; name: string; sql: string }[]
}

/** The three pages' routes, laid over the section's fixture. Hands back what the pages sent. */
async function mockWork(page: Page, options: WorkMock = {}) {
  const shell = await mockDatabases(page, options)
  const sent = {
    classify: [] as string[],
    query: [] as Record<string, unknown>[],
    script: [] as Record<string, unknown>[],
    cancel: [] as Record<string, unknown>[],
    explain: [] as Record<string, unknown>[],
    exports: [] as Record<string, unknown>[],
    search: [] as URL[],
    browse: [] as URL[],
    outline: [] as URL[],
    orm: [] as Record<string, unknown>[],
    saves: [] as { method: string; path: string; body: Record<string, unknown> }[],
  }
  const history = [...(options.history ?? [])]
  let saved = [...(options.saved ?? [])]
  let cancelled = false

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

  await page.route("**/api/v1/databases/orm/targets", (route) => json(route, TARGETS))

  await page.route(/\/api\/v1\/databases\/(1|7)\/.+/, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const [, id, rest] = /^\/api\/v1\/databases\/(\d+)(\/.*)$/.exec(url.pathname) ?? []
    const outline = OUTLINES[Number(id)]
    const body = () => (request.postDataJSON() ?? {}) as Record<string, unknown>

    if (rest === "/catalog") {
      return json(route, {
        schema: outline.schema,
        defaultSchema: outline.schema,
        schemas:
          id === "1"
            ? [
                { name: "public", default: true, tables: 4 },
                { name: "analytics", tables: 2 },
                { name: "pg_catalog", system: true, tables: 140 },
              ]
            : [{ name: "main", default: true, tables: 2 }],
        objects: {},
        truncated: [],
        limit: 5000,
      })
    }
    if (rest === "/outline") {
      sent.outline.push(url)
      const entries = Object.keys(outline.tables).map((name) => ({
        id: `${outline.schema}.${name}`,
        schema: outline.schema,
        name,
        type: outline.kinds?.[name] ?? "table",
      }))
      return json(route, {
        schema: url.searchParams.get("schema") ?? "",
        tables: Object.fromEntries(entries.map((entry) => [entry.id, outline.tables[entry.name]])),
        entries,
        truncated: false,
        total: entries.length,
        limit: 5000,
      })
    }
    if (rest === "/relations") {
      return json(
        route,
        id === "1"
          ? {
              "public.orders": [
                {
                  name: "orders_customer_id_fkey",
                  columns: ["customer_id"],
                  refSchema: "public",
                  refTable: "customers",
                  refColumns: ["id"],
                },
              ],
            }
          : {},
      )
    }
    if (rest === "/browse") {
      sent.browse.push(url)
      const table = url.searchParams.get("table") ?? ""
      const columns = outline.tables[table] ?? []
      // Customers hold the value in two rows, and more of them than are handed over.
      const found = table === "customers"
      return json(route, {
        columns,
        types: columns.map(() => "TEXT"),
        kinds: columns.map(() => "text"),
        rows: found
          ? [
              ["12", "pro@example.com", "pro"],
              ["19", "ann@example.com", "pro"],
            ]
          : [],
        rowCount: found ? 2 : 0,
        rowsAffected: 0,
        duration: "1ms",
        truncated: found,
        statement: "",
        primaryKey: ["id"],
      })
    }
    if (rest === "/columns") {
      const table = url.searchParams.get("table") ?? ""
      return json(
        route,
        (outline.tables[table] ?? []).map((name, index) => ({
          name,
          type: index === 0 ? "bigint" : "text",
          nullable: index > 0,
          position: index + 1,
        })),
      )
    }
    if (rest === "/table") {
      const table = url.searchParams.get("table") ?? ""
      return json(route, {
        schema: outline.schema,
        name: table,
        type: "table",
        columns: [],
        primaryKey: table === "audit_log" ? [] : ["id"],
        indexes: [],
        foreignKeys: [],
      })
    }
    if (rest === "/history") return json(route, history)
    if (rest === "/queries" && request.method() === "GET") return json(route, saved)
    if (rest === "/queries" && request.method() === "POST") {
      const made = {
        id: 40 + saved.length,
        ...(body() as { name: string; sql: string }),
        createdAt: "2026-10-01T09:00:00Z",
      }
      sent.saves.push({ method: "POST", path: rest, body: body() })
      saved = [...saved, made]
      return json(route, made, 201)
    }
    const one = /^\/queries\/(\d+)$/.exec(rest)
    if (one) {
      sent.saves.push({
        method: request.method(),
        path: rest,
        body: request.method() === "DELETE" ? {} : body(),
      })
      if (request.method() === "DELETE") {
        saved = saved.filter((query) => query.id !== Number(one[1]))
        return route.fulfill({ status: 204, body: "" })
      }
      saved = saved.map((query) => (query.id === Number(one[1]) ? { ...query, ...body() } : query))
      return json(
        route,
        saved.find((query) => query.id === Number(one[1])),
      )
    }
    if (rest === "/classify") {
      const text = String(body().query ?? "")
      sent.classify.push(text)
      await options.classifyHeld
      if (options.classifyFails) {
        return json(
          route,
          { error: { code: "connect_failed", message: "the server did not answer" } },
          502,
        )
      }
      const statements = split(text).map((sql) => ({
        sql,
        line: lineOf(text, sql),
        risk: riskOf(sql),
      }))
      const worst =
        statements.find((entry) => entry.risk.destructive)?.risk ??
        statements.find((entry) => entry.risk.level !== "read")?.risk ??
        READ
      return json(route, { ...worst, statements })
    }
    if (rest === "/query") {
      const asked = body()
      sent.query.push(asked)
      const sql = split(String(asked.query))[0] ?? ""
      if (/slow/.test(sql)) {
        await options.slowHeld
        if (cancelled) {
          return json(
            route,
            { error: { code: "query_cancelled", message: "the statement was cancelled" } },
            409,
          )
        }
      }
      if (/nope/.test(sql)) {
        history.unshift({
          id: history.length + 1,
          sql,
          risk: "read",
          success: false,
          durationMs: 1,
          rowCount: 0,
          error: 'ERROR: relation "nope" does not exist (SQLSTATE 42P01)',
          ranAt: "2026-10-01T09:00:00Z",
        })
        return json(
          route,
          {
            error: {
              code: "bad_request",
              message: 'ERROR: relation "nope" does not exist (SQLSTATE 42P01)',
            },
          },
          400,
        )
      }
      history.unshift({
        id: history.length + 1,
        sql,
        risk: riskOf(sql).level,
        success: true,
        durationMs: 2,
        rowCount: 3,
        ranAt: "2026-10-01T09:00:00Z",
      })
      return json(route, { result: resultOf(sql, Number(asked.maxRows ?? 500)), risk: riskOf(sql) })
    }
    if (rest === "/script") {
      const asked = body()
      sent.script.push(asked)
      const text = String(asked.script)
      let failed = -1
      const statements = split(text).map((sql, index) => {
        const base = { index, sql, line: lineOf(text, sql), risk: riskOf(sql), durationMs: 2 }
        if (failed >= 0) return { ...base, status: "skipped", durationMs: 0 }
        if (/nope/.test(sql)) {
          failed = index
          return {
            ...base,
            status: "error",
            error: 'ERROR: relation "nope" does not exist (SQLSTATE 42P01)',
          }
        }
        return { ...base, status: "ok", result: resultOf(sql, Number(asked.maxRows ?? 500)) }
      })
      return json(route, {
        statements,
        failed,
        transaction: "read_only",
        durationMs: 6,
        risk: READ,
      })
    }
    if (rest === "/query/cancel") {
      sent.cancel.push(body())
      cancelled = true
      return json(route, { cancelled: true })
    }
    if (rest === "/explain") {
      const asked = body()
      sent.explain.push(asked)
      const plan = asked.analyze ? measured(PLAN) : PLAN
      return json(route, {
        result: {
          columns: ["QUERY PLAN"],
          types: ["JSON"],
          kinds: ["json"],
          rows: [[JSON.stringify(plan)]],
          rowCount: 1,
          rowsAffected: 0,
          duration: "1ms",
          truncated: false,
          statement: `EXPLAIN ${asked.query}`,
        },
        plan,
        format: "json",
        analyzed: Boolean(asked.analyze),
        rolledBack: false,
      })
    }
    if (rest === "/export/query") {
      sent.exports.push(body())
      return route.fulfill({
        status: 200,
        contentType: "text/csv",
        headers: { "Content-Disposition": 'attachment; filename="query-20261001.csv"' },
        body: "id,email\n1,a@example.com\n",
      })
    }
    if (rest === "/export/status") {
      return json(route, {
        status: "complete",
        rows: 20000,
        format: "csv",
        startedAt: "2026-10-01T09:00:00Z",
      })
    }
    if (rest === "/search") {
      sent.search.push(url)
      await options.searchHeld
      const q = url.searchParams.get("q") ?? ""
      if (q === "nothing") return json(route, { matches: [], tablesScanned: 4, truncated: false })
      return json(route, {
        matches: [
          {
            schema: "public",
            table: "customers",
            column: "tier",
            value: "pro",
            row: { id: "12", email: "pro@example.com", tier: "pro", avatar: null },
          },
          {
            schema: "public",
            table: "customers",
            column: "tier",
            value: "pro",
            row: { id: "19", email: "ann@example.com", tier: "pro", avatar: null },
          },
          {
            schema: "public",
            table: "audit_log",
            column: "action",
            value: "approved",
            row: { at: "2026-10-01T09:00:00Z", actor: "ann", action: "approved" },
          },
        ],
        tablesScanned: 4,
        tablesSkipped: ["secrets"],
        truncated: true,
      })
    }
    if (rest === "/orm") {
      const asked = body()
      sent.orm.push(asked)
      if (asked.target === "typeorm" && asked.split) {
        return json(route, {
          target: "typeorm",
          language: "typescript",
          files: [
            { filename: "index.ts", content: 'export * from "./Customer"\n' },
            { filename: "Customer.ts", content: "export class Customer {}\n" },
          ],
          warnings: [],
          counts: { tables: 1, views: 0, enums: 0, relations: 0 },
        })
      }
      return json(route, {
        target: asked.target,
        language: TARGETS.targets.find((target) => target.id === asked.target)?.language,
        files: [
          {
            filename: TARGETS.targets.find((target) => target.id === asked.target)?.filename,
            content: `// ${asked.target} for ${asked.schema ?? "every schema"}\nmodel customers {}\n`,
          },
        ],
        warnings:
          asked.target === "prisma"
            ? ["audit_log has no primary key, so Prisma Client cannot address its rows."]
            : [],
        counts: { tables: 4, views: 0, enums: 1, relations: 2 },
      })
    }
    return route.fallback()
  })

  return { ...shell, sent }
}

const editor = (page: Page) => page.locator("[data-slot=sql-editor] .monaco-editor").first()
const commands = (page: Page) => page.locator("[data-slot=query-commands]")
const results = (page: Page) => page.locator("[data-slot=query-results]")
const runButton = (page: Page) => commands(page).getByRole("button", { name: "Run", exact: true })
const openTabs = (page: Page) =>
  page.getByRole("tablist", { name: "Open statements" }).getByRole("tab")
const editorText = (page: Page) =>
  page.locator("[data-slot=sql-editor] .view-lines").evaluate((el) =>
    [...el.querySelectorAll(".view-line")]
      .sort(
        (a, b) =>
          parseFloat((a as HTMLElement).style.top) - parseFloat((b as HTMLElement).style.top),
      )
      .map((line) => (line.textContent ?? "").replace(/ /g, " "))
      .join("\n"),
  )

/** Opens the editor of connection 1 and waits for Monaco. */
async function openEditor(page: Page, path = "/databases/1/query") {
  await page.goto(path)
  // Monaco is loaded on demand: on a busy runner its first paint is the slow part.
  await expect(editor(page)).toBeVisible({ timeout: 45_000 })
}

/**
 * Replaces the editor's text and leaves the cursor at its end, with the
 * keyboard in the editor. Set through the editor rather than typed: typed
 * line breaks are indented by the editor as typing is, and the text that
 * reaches a request has to be the text the test wrote.
 */
async function write(page: Page, sql: string) {
  await editor(page).click()
  await page.evaluate((text) => {
    type Held = {
      getDomNode(): HTMLElement | null
      setValue(value: string): void
      getModel(): { getFullModelRange(): { getEndPosition(): unknown } }
      setPosition(position: unknown): void
      focus(): void
    }
    const monaco = (window as unknown as { monaco: { editor: { getEditors(): Held[] } } }).monaco
    const held = monaco.editor
      .getEditors()
      .find((candidate) => candidate.getDomNode()?.closest("[data-slot=sql-editor]"))
    if (!held) throw new Error("no editor on the page")
    held.setValue(text)
    held.setPosition(held.getModel().getFullModelRange().getEndPosition())
    held.focus()
  }, sql)
}

/* ------------------------------------------------------------------ run */

test("Ctrl+Enter runs the statement under the cursor as one statement", async ({ page }) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(page, "select 1;\nselect * from customers;")
  // The cursor rests after the last semicolon: on the statement it closed.
  await page.keyboard.press("ControlOrMeta+Enter")

  await expect(results(page).getByRole("grid", { name: "Query result" })).toBeVisible()
  expect(sent.query).toHaveLength(1)
  expect(sent.query[0].query).toBe("select * from customers")
  expect(sent.query[0].maxRows).toBe(500)
  expect(String(sent.query[0].queryId)).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
  expect(sent.script).toHaveLength(0)
  // The statement was classified first, and it was this statement.
  expect(sent.classify.at(-1)).toBe("select * from customers")
  // A 64-bit integer is its digits, NULL is NULL and an empty text is not NULL.
  await expect(results(page).getByText("9007199254740993", { exact: true })).toBeVisible()
  await expect(results(page).getByRole("gridcell", { name: "NULL" }).first()).toBeVisible()
})

test("a selection is what runs, and several statements go as one script with a result each", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(
    page,
    "select count(*) from orders;\nselect * from customers;\nselect * from nope;\nselect 2;",
  )
  await page.keyboard.press("ControlOrMeta+Shift+Enter")

  const chips = results(page).getByRole("group", { name: "Statements of this run" })
  await expect(chips.getByRole("button")).toHaveCount(4)
  expect(sent.script).toHaveLength(1)
  expect(sent.query).toHaveLength(0)
  expect(sent.script[0].script).toBe(
    "select count(*) from orders;\nselect * from customers;\nselect * from nope;\nselect 2",
  )
  // The statement that failed is the one shown, with the engine's own words, in place.
  const failure = results(page).locator("[data-slot=query-refusal]")
  await expect(failure).toContainText("Statement 3 failed")
  await expect(failure).toContainText('ERROR: relation "nope" does not exist (SQLSTATE 42P01)')
  await expect(failure).toHaveAttribute("role", "alert")
  await expect(chips.getByRole("button").nth(3)).toContainText("not run")
  // …and marked where it stands in the editor.
  await expect(page.locator("[data-slot=sql-editor] .squiggly-error").first()).toBeVisible()

  // Each statement keeps its own result.
  await chips.getByRole("button").nth(1).click()
  await expect(results(page).getByRole("grid", { name: "Result of statement 2" })).toBeVisible()

  // What each did, told in order.
  await results(page)
    .getByRole("tab", { name: /Messages/ })
    .click()
  const messages = results(page).locator("[data-slot=query-messages]")
  await expect(messages.getByRole("listitem")).toHaveCount(6)
  await expect(messages).toContainText("Stopped at statement 3 of 4: 2 statements ran, 1 did not.")
  await expect(messages).toContainText("Run in one read-only scope")

  // A selection is what runs.
  await editor(page).click()
  await page.keyboard.press("ControlOrMeta+Home")
  await page.keyboard.press("Shift+End")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect.poll(() => sent.query.length).toBe(1)
  expect(sent.query[0].query).toBe("select count(*) from orders;")
})

/* ---------------------------------------------------------- C3: destroys */

test("a statement that destroys is classified as it is run, and asked about with what it does", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(page, "select 1")
  await expect(commands(page).getByText("reads", { exact: true })).toBeVisible()
  // The text changes and Run is pressed at once: before any advice about the new text.
  await write(page, "drop table customers")
  await page.keyboard.press("ControlOrMeta+Enter")

  const dialog = page.getByRole("dialog")
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText("Run statement")
  await expect(dialog).toContainText("critical")
  await expect(dialog).toContainText("drops a database object")
  await expect(dialog).toContainText("drop table customers")
  await expect(dialog).toContainText("shop")
  expect(sent.query).toHaveLength(0)
  expect(sent.classify).toContain("drop table customers")

  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toBeHidden()
  expect(sent.query).toHaveLength(0)

  await runButton(page).click()
  await page.getByRole("dialog").getByRole("button", { name: "Run statement" }).click()
  await expect.poll(() => sent.query.length).toBe(1)
  expect(sent.query[0].query).toBe("drop table customers")
  await expect(results(page)).toContainText("rows changed")
})

test("a classification that does not come back is a run that does not happen", async ({ page }) => {
  const { sent } = await mockWork(page, { classifyFails: true })
  await openEditor(page)
  await write(page, "delete from customers")
  await page.keyboard.press("ControlOrMeta+Enter")

  const refusal = results(page).locator("[data-slot=query-refusal]")
  await expect(refusal).toContainText("Not run")
  await expect(refusal).toContainText("could not be checked")
  await expect(refusal).toContainText("the server did not answer")
  expect(sent.query).toHaveLength(0)
  expect(sent.script).toHaveLength(0)
})

test("a role without the destructive capability is told why Run is refused, and nothing is sent", async ({
  page,
}) => {
  const { sent } = await mockWork(page, { capabilities: ["read", "service.control"] })
  await openEditor(page)
  await write(page, "drop table customers")

  const why = commands(page).locator("[data-slot=run-refused]")
  await expect(why).toContainText("Your role may not run a statement that destroys")
  await expect(why).toContainText("drops a database object")
  await expect(runButton(page)).toBeDisabled()
  // The key is refused the same way, in place.
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page).locator("[data-slot=query-refusal]")).toContainText("Not run")
  expect(sent.query).toHaveLength(0)

  // What it may run, it runs.
  await write(page, "select * from customers")
  await expect(runButton(page)).toBeEnabled()
  await runButton(page).click()
  await expect.poll(() => sent.query.length).toBe(1)
})

test("a protected connection runs what reads and says why it refuses the rest", async ({
  page,
}) => {
  const { sent } = await mockWork(page, { rows: { 1: { readOnly: true } } })
  await openEditor(page)
  await write(page, "insert into customers (email) values ('a')")

  await expect(commands(page).locator("[data-slot=run-refused]")).toContainText(
    "This connection is protected, so only statements that read are run. This one inserts rows.",
  )
  await expect(runButton(page)).toBeDisabled()
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page).locator("[data-slot=query-refusal]")).toContainText("protected")
  expect(sent.query).toHaveLength(0)

  await write(page, "select * from customers")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page).getByRole("grid", { name: "Query result" })).toBeVisible()
})

test("a role that may not run statements has no Run, and can still read a plan", async ({
  page,
}) => {
  const { sent } = await mockWork(page, {
    viewer: true,
    saved: [{ id: 3, name: "Pro customers", sql: "select * from customers where tier = 'pro'" }],
  })
  await openEditor(page)
  await write(page, "select * from customers")

  await expect(runButton(page)).toHaveCount(0)
  await expect(commands(page)).toContainText(
    "Your role reads this database and may not run statements.",
  )
  await expect(commands(page).getByRole("button", { name: /^Save/ })).toHaveCount(0)
  await expect(commands(page).getByRole("button", { name: "More ways to explain" })).toHaveCount(0)
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page).locator("[data-slot=query-refusal]")).toContainText(
    "may not run statements",
  )
  expect(sent.query).toHaveLength(0)

  await commands(page).getByRole("button", { name: "Explain", exact: true }).click()
  await expect(results(page).locator("[data-slot=plan-tree]")).toBeVisible()
  expect(sent.explain[0]).toEqual({ query: "select * from customers", format: "json" })

  // A saved query can be opened, and nothing about it changed.
  await page
    .locator("[data-slot=query-rail]")
    .getByRole("tab", { name: /^Saved/ })
    .click()
  await page.getByRole("button", { name: "Open Pro customers" }).click()
  await expect(openTabs(page).last()).toContainText("Pro customers")
  await page.getByRole("button", { name: "Actions for Pro customers" }).click()
  await expect(page.getByRole("menuitem", { name: "Copy link" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Rename" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Delete" })).toHaveCount(0)
})

/* ------------------------------------------------------- H7: truncation */

test("a result the server cut says so, fetches more on request and exports every row", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(page, "select many from customers")
  await page.keyboard.press("ControlOrMeta+Enter")

  const notice = results(page).locator("[data-slot=result-truncated]")
  await expect(notice).toContainText("These are the first 500 rows. The statement returns more.")
  await notice.getByRole("button", { name: "Fetch 5,000" }).click()
  await expect.poll(() => sent.query.length).toBe(2)
  expect(sent.query[1]).toMatchObject({ query: "select many from customers", maxRows: 5000 })
  // At the most a run shows there is nothing more to fetch, and the way to all of it is said.
  await expect(notice).toContainText("These are the first 5,000 rows.")
  await expect(notice).toContainText("Export writes every row to a file")
  await expect(notice.getByRole("button")).toHaveCount(0)

  // M9: five thousand rows are a windowful of elements.
  expect(await results(page).getByRole("row").count()).toBeLessThan(120)

  const download = page.waitForEvent("download")
  await results(page).getByRole("button", { name: "Export" }).click()
  await page.getByRole("menuitem", { name: /CSV/ }).click()
  expect((await download).suggestedFilename()).toBe("query-20261001.csv")
  expect(sent.exports[0]).toMatchObject({
    sql: "select many from customers",
    format: "csv",
    limit: 100_000,
  })
  expect(String(sent.exports[0].exportId)).toMatch(/^[0-9a-f-]{36}$/)
})

test("a statement that changes data is not run again to fetch more of what it returned", async ({
  page,
}) => {
  await mockWork(page)
  await page.route("**/api/v1/databases/1/query", (route) =>
    json(route, {
      result: {
        ...resultOf("select many", 500),
        statement: "insert into t select many returning *",
      },
      risk: WRITE,
    }),
  )
  await openEditor(page)
  await write(page, "insert into t select many returning *")
  await page.keyboard.press("ControlOrMeta+Enter")
  const notice = results(page).locator("[data-slot=result-truncated]")
  await expect(notice).toContainText("It changes data, so it is not run again for them.")
  await expect(notice.getByRole("button")).toHaveCount(0)
  await expect(results(page).getByRole("button", { name: "Export" })).toHaveCount(0)
})

/* ---------------------------------------------------- H9: the hand-over */

test("a statement handed over in the address opens in a tab of its own and is read once", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  await write(page, "update customers set tier = 'plus' where id = 7 -- my draft")
  await expect(openTabs(page)).toHaveCount(1)

  const handed = "select * from orders where status = 'paid'"
  await page.goto(`/databases/1/query?sql=${encodeURIComponent(handed)}`)
  await expect(openTabs(page)).toHaveCount(2)
  await expect.poll(() => editorText(page)).toBe(handed)
  // Taken out of the address, so a reload or Back does not bring it in again.
  await expect.poll(() => new URL(page.url()).search).toBe("")

  await page.reload()
  await expect(editor(page)).toBeVisible()
  await expect(openTabs(page)).toHaveCount(2)
  // The draft is as it was left.
  await openTabs(page).first().click()
  await expect
    .poll(() => editorText(page))
    .toBe("update customers set tier = 'plus' where id = 7 -- my draft")

  // The same statement handed over again comes to the front; it is not a third tab.
  await page.goto(`/databases/1/query?sql=${encodeURIComponent(handed)}`)
  await expect.poll(() => editorText(page)).toBe(handed)
  await expect(openTabs(page)).toHaveCount(2)
})

test("tabs are kept per connection, closed with an undo, and named in place", async ({ page }) => {
  await mockWork(page)
  await openEditor(page)
  await write(page, "select 1")
  await page.getByRole("button", { name: "New tab" }).click()
  await write(page, "select 2")
  await expect(openTabs(page)).toHaveCount(2)

  await openTabs(page).nth(1).dblclick()
  await page.getByLabel("Name of this tab").fill("Twos")
  await page.keyboard.press("Enter")
  await expect(openTabs(page).nth(1)).toHaveText("Twos")

  // Another connection has its own tabs…
  await openEditor(page, "/databases/7/query")
  await expect(openTabs(page)).toHaveCount(1)
  await expect.poll(() => editorText(page)).toBe("")
  // …and this one's are there on return.
  await openEditor(page)
  await expect(openTabs(page)).toHaveText(["Query 1", "Twos"])
  await expect.poll(() => editorText(page)).toBe("select 2")

  await page.getByRole("button", { name: "Close Twos" }).click()
  await expect(openTabs(page)).toHaveCount(1)
  await page.getByRole("button", { name: "Undo" }).click()
  await expect(openTabs(page)).toHaveText(["Query 1", "Twos"])
  await expect.poll(() => editorText(page)).toBe("select 2")
})

/* ------------------------------------------------------ M11: completion */

test("completion offers this connection's schema, and another connection's after a switch", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  const suggestions = page.locator(".suggest-widget .monaco-list-row")

  await write(page, "select * from ")
  await page.keyboard.press("ControlOrMeta+Space")
  await expect(suggestions.first()).toBeVisible()
  const first = await suggestions.allInnerTexts()
  expect(first.some((row) => row.startsWith("customers"))).toBe(true)
  expect(first.some((row) => row.startsWith("orders"))).toBe(true)
  expect(first.some((row) => row.startsWith("notes"))).toBe(false)
  // A name the engine would not read back bare is written quoted.
  await page.keyboard.insertText("Mix")
  await expect(suggestions.first()).toContainText("Mixed Case Table")
  await page.keyboard.press("Enter")
  await expect.poll(() => editorText(page)).toBe('select * from "Mixed Case Table"')

  // After an alias and a dot: that table's columns.
  await write(page, "select o. from orders o")
  await page.keyboard.press("ControlOrMeta+Home")
  for (let i = 0; i < "select o.".length; i++) await page.keyboard.press("ArrowRight")
  await page.keyboard.press("ControlOrMeta+Space")
  await expect(suggestions.first()).toBeVisible()
  const columns = (await suggestions.allInnerTexts()).map((row) => row.split("\n")[0])
  expect(columns.slice(0, 4).sort()).toEqual(["customer_id", "id", "status", "total_cents"])
  await page.keyboard.press("Escape")

  // Another connection: its tables, and none of the last one's.
  await openEditor(page, "/databases/7/query")
  await write(page, "select * from ")
  await page.keyboard.press("ControlOrMeta+Space")
  await expect(suggestions.first()).toBeVisible()
  const second = await suggestions.allInnerTexts()
  expect(second.some((row) => row.startsWith("notes"))).toBe(true)
  expect(second.some((row) => row.startsWith("customers"))).toBe(false)
})

/* --------------------------------------------------------------- cancel */

test("Cancel takes Run's place while a statement is out, and stops it by its name", async ({
  page,
}) => {
  const slow = hold()
  const { sent } = await mockWork(page, { slowHeld: slow.until })
  await openEditor(page)
  await write(page, "select slow from customers")
  await page.keyboard.press("ControlOrMeta+Enter")

  const cancel = commands(page).getByRole("button", { name: "Cancel" })
  await expect(cancel).toBeVisible()
  await expect(runButton(page)).toHaveCount(0)
  await expect(commands(page).getByRole("status")).toContainText(/Running…\s*0:0\d/)
  // The tab says its statement is still out.
  await expect(openTabs(page).first().locator("svg")).toBeVisible()

  await cancel.click()
  await expect.poll(() => sent.cancel.length).toBe(1)
  expect(sent.cancel[0]).toEqual({ queryId: sent.query[0].queryId })
  slow.release()

  await expect(results(page).locator("[data-slot=query-refusal]")).toContainText(
    "The statement was cancelled on the server.",
  )
  await expect(runButton(page)).toBeVisible()
})

/* -------------------------------------------------------------- explain */

test("Explain draws the plan as a tree; measuring it says it runs the statement and asks the gate", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(page, "select * from orders o join customers c on c.id = o.customer_id")
  await commands(page).getByRole("button", { name: "Explain", exact: true }).click()

  const tree = results(page).locator("[data-slot=plan-tree]")
  await expect(tree.locator("[data-plan-step]")).toHaveCount(4)
  await expect(tree).toContainText("Hash Join")
  await expect(tree).toContainText("orders o")
  await expect(tree).toContainText("customers c using customers_pkey")
  await expect(results(page)).toContainText("estimated")
  expect(sent.explain[0]).toEqual({
    query: "select * from orders o join customers c on c.id = o.customer_id",
    format: "json",
  })
  // A step's conditions open under it.
  await tree.getByRole("button", { name: /Hash Join/ }).click()
  await expect(tree).toContainText("Hash Cond: (o.customer_id = c.id)")
  // The engine's own text is one press away.
  await results(page).getByRole("radio", { name: "Text" }).click()
  await expect(results(page)).toContainText('"Node Type": "Limit"')

  await commands(page).getByRole("button", { name: "More ways to explain" }).click()
  await expect(page.getByRole("menuitem", { name: /Explain and measure/ })).toContainText(
    "runs the statement",
  )
  await page.getByRole("menuitem", { name: /Explain and measure/ }).click()
  await expect(results(page)).toContainText("measured")
  await expect(results(page)).toContainText("ran in 2.6 ms")
  expect(sent.explain[1]).toMatchObject({ analyze: true, format: "json" })

  // Measuring a statement that destroys is asked about, as running it would be.
  await write(page, "delete from customers")
  await commands(page).getByRole("button", { name: "More ways to explain" }).click()
  await page.getByRole("menuitem", { name: /Explain and measure/ }).click()
  await expect(page.getByRole("dialog")).toContainText("Measuring a plan runs the statement")
  expect(sent.explain).toHaveLength(2)
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()
  expect(sent.explain).toHaveLength(2)
})

/* ----------------------------------------------- saved, history, format */

test("a statement is saved under a name, saved again, renamed and deleted", async ({ page }) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(page, "select * from customers where tier = 'pro'")
  await commands(page).getByRole("button", { name: "Save as" }).click()
  await page.locator("#saved-query-name").fill("Pro customers")
  await page.getByRole("dialog").getByRole("button", { name: "Save", exact: true }).click()

  await expect(openTabs(page).first()).toHaveText("Pro customers")
  expect(sent.saves[0]).toEqual({
    method: "POST",
    path: "/queries",
    body: { name: "Pro customers", sql: "select * from customers where tier = 'pro'" },
  })
  const rail = page.locator("[data-slot=query-rail]")
  await expect(rail.getByRole("button", { name: "Open Pro customers" })).toBeVisible()

  // An edit is a change not yet kept; Ctrl+S keeps it.
  await editor(page).click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.insertText(" limit 5")
  await expect(openTabs(page).first()).toContainText("changed since it was saved")
  await page.keyboard.press("ControlOrMeta+S")
  await expect.poll(() => sent.saves.length).toBe(2)
  expect(sent.saves[1]).toMatchObject({
    method: "PUT",
    body: { sql: "select * from customers where tier = 'pro' limit 5" },
  })
  await expect(openTabs(page).first()).not.toContainText("changed since it was saved")

  await rail.getByRole("button", { name: "Actions for Pro customers" }).click()
  await page.getByRole("menuitem", { name: "Rename" }).click()
  await page.getByLabel("Name of the saved query").fill("Paying customers")
  await page.keyboard.press("Enter")
  await expect(openTabs(page).first()).toHaveText("Paying customers")
  expect(sent.saves[2]).toMatchObject({ method: "PUT", body: { name: "Paying customers" } })

  await rail.getByRole("button", { name: "Actions for Paying customers" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Paying customers")
  await expect(dialog).toContainText("select * from customers where tier = 'pro' limit 5")
  await dialog.getByRole("button", { name: "Delete" }).click()
  await expect(rail).toContainText("Nothing saved yet")
  // The tab keeps its text as a plain draft.
  await expect
    .poll(() => editorText(page))
    .toBe("select * from customers where tier = 'pro' limit 5")
})

test("the history, the schema and the snippets lead into the editor without replacing a draft", async ({
  page,
}) => {
  await mockWork(page, {
    history: [
      {
        id: 2,
        sql: "select * from nope",
        risk: "read",
        success: false,
        durationMs: 1,
        rowCount: 0,
        error: "no such relation",
        ranAt: "2026-10-01T08:59:00Z",
      },
      {
        id: 1,
        sql: "select count(*) from orders",
        risk: "read",
        success: true,
        durationMs: 4,
        rowCount: 1,
        ranAt: "2026-10-01T08:58:00Z",
      },
    ],
  })
  await openEditor(page)
  await write(page, "select ")
  const rail = page.locator("[data-slot=query-rail]")

  // The schema writes a name at the cursor.
  await rail.getByRole("button", { name: "Write Mixed Case Table at the cursor" }).click()
  await expect.poll(() => editorText(page)).toBe('select "Mixed Case Table"')
  await rail.getByRole("button", { name: "Show the columns of customers" }).click()
  await expect(
    rail.getByRole("list", { name: "Columns of customers" }).getByRole("listitem"),
  ).toHaveCount(3)
  await expect(rail.getByRole("list", { name: "Columns of customers" })).toContainText("bigint")

  // A history entry opens in a tab of its own: the draft is not written over.
  await rail.getByRole("tab", { name: /^History/ }).click()
  const entries = rail.getByRole("list", { name: "Statements that were run" }).getByRole("listitem")
  await expect(entries).toHaveCount(2)
  await expect(entries.first()).toContainText("failed")
  await rail.getByPlaceholder("Find in the history").fill("count")
  await expect(entries).toHaveCount(1)
  await entries.first().getByRole("button").click()
  await expect(openTabs(page)).toHaveCount(2)
  await expect.poll(() => editorText(page)).toBe("select count(*) from orders")

  // A snippet opens under its own name.
  await rail.getByRole("tab", { name: /^Snippets/ }).click()
  await rail.getByRole("button", { name: "Open in a tab: Largest tables" }).click()
  // …named for the statement it holds.
  await expect(openTabs(page)).toHaveText([
    "Query 1",
    "select count(*) from orders",
    "Largest tables",
  ])
  await expect.poll(() => editorText(page)).toContain("pg_total_relation_size")

  // The same entry pressed again is the same tab brought to the front, not another.
  await rail.getByRole("tab", { name: /^History/ }).click()
  await rail.getByRole("button", { name: "Open in a tab: select count(*) from orders" }).click()
  await expect(openTabs(page)).toHaveCount(3)
  await expect.poll(() => editorText(page)).toBe("select count(*) from orders")

  await openTabs(page).first().click()
  await expect.poll(() => editorText(page)).toBe('select "Mixed Case Table"')
})

test("Format lays the statement out and changes nothing but space and capitals", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  await write(page, "select id, email from customers where tier = 'pro' order by id limit 5")
  await commands(page).getByRole("button", { name: "Format" }).click()
  await expect
    .poll(() => editorText(page))
    .toBe("SELECT id, email\nFROM customers\nWHERE tier = 'pro'\nORDER BY id\nLIMIT 5\n")
})

/* --------------------------------------------------------------- search */

test("a search is asked once, drawn by table with the value marked, and each row opens Data filtered to it", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await page.goto("/databases/1/search")
  await page.getByLabel("The value to find").fill("pro")
  await page.keyboard.press("Enter")

  const groups = page.locator("[data-slot=search-table]")
  await expect(groups).toHaveCount(2)
  expect(sent.search).toHaveLength(1)
  expect(sent.search[0].searchParams.get("q")).toBe("pro")
  expect(sent.search[0].searchParams.get("schema")).toBe("public")
  // The question is in the address.
  expect(new URL(page.url()).searchParams.get("q")).toBe("pro")

  await expect(page.getByRole("status").filter({ hasText: "is in" })).toContainText(
    "pro is in 2 tables: 3 rows are shown, at most 5 from each. 4 tables of public read",
  )
  // Every cell that holds the value is marked, not only the one the server named.
  const first = groups.first().locator("[data-slot=choice-row]").first()
  await expect(first.locator("mark")).toHaveText(["pro", "pro"])
  // A row with a key is found again by its key.
  const link = first.getByRole("link", { name: "Open this row of customers in Data" })
  const filtersOf = async (anchor: typeof link) =>
    JSON.parse(
      new URL((await anchor.getAttribute("href")) ?? "", page.url()).searchParams.get("filters") ??
        "[]",
    )
  await expect.poll(() => filtersOf(link)).toEqual([{ column: "id", op: "eq", value: "12" }])
  const target = new URL((await link.getAttribute("href")) ?? "", page.url())
  expect(target.pathname).toBe("/databases/1/data")
  expect(target.searchParams.get("table")).toBe("customers")
  // A row with none, by its own values, the matched cell first — and the link
  // says what it opens: the rows like it. The moment is left out: an engine
  // that reads one its own way answered the table editor with a refusal.
  const keyless = groups
    .nth(1)
    .getByRole("link", { name: "Open the rows of audit_log like this one in Data" })
  await expect
    .poll(() => filtersOf(keyless))
    .toEqual([
      { column: "action", op: "eq", value: "approved" },
      { column: "actor", op: "eq", value: "ann" },
    ])
  // What a row is recognised by: its key, then what a person reads, an instant to the second.
  await expect(first).toContainText("id 12")
  await expect(groups.nth(1).locator("[data-slot=choice-row]").first()).toContainText(
    "actor ann · at 2026-10-01 09:00:00 UTC",
  )
  // Every match of a table: the value in any column it was found in.
  const every = new URL(
    (await groups
      .first()
      .getByRole("link", { name: "Every row of customers that matches" })
      .getAttribute("href")) ?? "",
    page.url(),
  )
  expect(JSON.parse(every.searchParams.get("filters") ?? "[]")).toEqual([
    { column: "email", op: "icontains", value: "pro" },
    { column: "tier", op: "icontains", value: "pro" },
  ])
  expect(every.searchParams.get("match")).toBe("any")
  // What the search could not say for sure is said.
  await expect(page.getByText("There may be more than this")).toBeVisible()
  await expect(page.getByText("secrets")).toBeVisible()

  // Narrowing to one table reads nothing again.
  await page
    .getByRole("group", { name: "Narrow to one table" })
    .getByRole("button", { name: /audit_log/ })
    .click()
  await expect(groups).toHaveCount(1)
  expect(sent.search).toHaveLength(1)

  await page
    .getByRole("group", { name: "Narrow to one table" })
    .getByRole("button", { name: /^All/ })
    .click()
  await expect(groups).toHaveCount(2)

  // Opening a row and coming back costs no second scan.
  await link.click()
  await expect(page).toHaveURL(/\/databases\/1\/data/)
  await page.goBack()
  await expect(groups.first()).toBeVisible()
  expect(sent.search).toHaveLength(1)

  // Another scope is another question.
  await page
    .getByRole("group", { name: "Which schema is read" })
    .getByRole("button", { name: "Every schema" })
    .click()
  await expect.poll(() => sent.search.length).toBe(2)
  expect(sent.search[1].searchParams.get("schema")).toBeNull()
})

test("one scan at a time: Enter while it is out starts no second one, and Stop drops it", async ({
  page,
}) => {
  const held = hold()
  const { sent } = await mockWork(page, { searchHeld: held.until })
  await page.goto("/databases/1/search?q=pro")

  await expect(page.getByRole("button", { name: "Stop" })).toBeVisible()
  await page.getByLabel("The value to find").press("Enter")
  await page.getByLabel("The value to find").press("Enter")
  expect(sent.search).toHaveLength(1)
  await page.getByRole("button", { name: "Stop" }).click()
  await expect(page.getByRole("search").getByRole("button", { name: "Search" })).toBeVisible()
  held.release()
  await expect(page.locator("[data-slot=search-table]")).toHaveCount(0)
  // A scan that was stopped says so, and is one press from being asked again.
  await expect(page.getByText("Stopped before it finished")).toBeVisible()
  await page.getByRole("button", { name: "Search again" }).click()
  await expect(page.locator("[data-slot=search-table]")).toHaveCount(2)
  expect(sent.search).toHaveLength(2)

  // Nothing found is said as that, with what was read.
  await page.getByLabel("The value to find").fill("nothing")
  await page.keyboard.press("Enter")
  await expect(page.getByText("Nothing found")).toBeVisible()
  await expect(page.getByRole("status").filter({ hasText: "No row contains" })).toContainText(
    "4 tables of public read",
  )
})

/* ------------------------------------------------------------- generate */

test("every generator the server lists is reachable, and the request carries what was chosen", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await page.goto("/databases/1/generate")

  // By group, in the page's order, each one a card.
  await expect(page.getByRole("region", { name: "ORM" }).getByRole("button")).toHaveCount(4)
  await expect(
    page.getByRole("region", { name: "Types & validation" }).getByRole("button"),
  ).toHaveCount(1)
  await expect(page.getByRole("region", { name: "Schema" }).getByRole("button")).toHaveCount(1)
  // Nothing is asked of the server until the reader asks.
  await expect(page.getByText("Nothing written yet")).toBeVisible()
  expect(sent.orm).toHaveLength(0)

  await page.getByRole("button", { name: "Generate" }).click()
  const code = page.locator("[data-slot=code-view]")
  await expect(code).toContainText("schema.prisma")
  expect(sent.orm[0]).toEqual({ target: "prisma", schema: "public" })
  // What could not be written is one line above the code, with how many there are.
  await expect(page.getByText("1 note", { exact: true })).toBeVisible()
  await expect(page.getByText("audit_log has no primary key").first()).toBeVisible()
  await expect(page.getByText("4 tables · 1 enum · 2 relations")).toBeVisible()
  // The files are what the page is for: they begin in the first screen.
  const top = (await code.boundingBox())?.y ?? Infinity
  expect(top).toBeLessThan(400)

  // The switches are one row that says what they are set to, and opens.
  const options = page.getByRole("button", { name: /^Options/ })
  await expect(options).toContainText("As the generator sets them")
  await options.click()
  // A switch asks again by itself, with only what differs from the default.
  await page.getByRole("switch", { name: "Relations" }).click()
  await page.getByRole("radio", { name: "camelCase" }).click()
  await expect
    .poll(() => sent.orm.at(-1))
    .toEqual({
      target: "prisma",
      schema: "public",
      relations: false,
      naming: "camel",
    })

  // Choosing a generator is asking for its files; several files are a tab each.
  await page.locator("[data-target=typeorm]").click()
  await expect(code).toContainText("entities.ts")
  await page.getByRole("switch", { name: "One file per model" }).click()
  const files = page.getByRole("tablist", { name: "Generated files" }).getByRole("tab")
  await expect(files).toHaveText(["index.ts", "Customer.ts"])
  await files.nth(1).click()
  await expect(code).toContainText("Customer.ts")
  const download = page.waitForEvent("download")
  await code.getByRole("button", { name: "Download", exact: true }).click()
  expect((await download).suggestedFilename()).toBe("Customer.ts")
  // The file tabs are a tablist the keyboard walks, and the code is its panel.
  await files.nth(1).press("ArrowLeft")
  await expect(files.first()).toBeFocused()
  await expect(files.first()).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("tabpanel", { name: "index.ts" })).toContainText("index.ts")
  // Several files are one archive, not as many downloads.
  const archive = page.waitForEvent("download")
  await code.getByRole("button", { name: "All 2 files" }).click()
  expect((await archive).suggestedFilename()).toBe("typeorm.zip")

  // The tables written can be chosen.
  await page.getByRole("button", { name: /Every table/ }).click()
  await page
    .getByRole("list", { name: "The tables written" })
    .getByText("orders", { exact: true })
    .click()
  await page.keyboard.press("Escape")
  await expect
    .poll(() => (sent.orm.at(-1)?.tables as string[] | undefined)?.sort())
    .toEqual(["Mixed Case Table", "customers"])

  // A typed switch the server would refuse is said, and not sent.
  await page.locator("[data-target=gorm]").click()
  await expect(code).toContainText("models.go")
  const before = sent.orm.length
  await page.getByLabel("Package").fill("My-Models")
  await expect(page.getByText("A lower-case Go package name").first()).toBeVisible()
  await page.waitForTimeout(900)
  expect(sent.orm).toHaveLength(before)
  // The files still on screen are said to be the ones from before the change.
  await expect(page.getByText("Not written again")).toBeVisible()

  // A generator this engine has no connector for is reachable, and says why in the server's words.
  await page.getByRole("button", { name: "Diesel, not for PostgreSQL" }).click()
  await expect(page.getByText("Diesel is not written for PostgreSQL")).toBeVisible()
  await expect(
    page.getByText("Diesel is not written for this server in this mock, and says why."),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Generate" })).toHaveCount(0)

  // The last generator and its switches are remembered.
  await page.locator("[data-target=prisma]").click()
  await expect(code).toContainText("schema.prisma")
  await page.reload()
  await expect(page.locator("[data-target=prisma]")).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("switch", { name: "Relations" })).not.toBeChecked()
  await expect(page.getByRole("radio", { name: "camelCase" })).toBeChecked()
})

/* ------------------------------------------------ found by using the pages */

const focusIsInEditor = (page: Page) =>
  page.evaluate(() => document.activeElement?.closest("[data-slot=sql-editor]") !== null)

test("the run key pressed twice before the statement has been classified sends it once", async ({
  page,
}) => {
  const classify = hold()
  const { sent } = await mockWork(page, { classifyHeld: classify.until })
  await openEditor(page)
  await write(page, "insert into audit_log (actor) values ('twice')")
  // A remote server's round trip is long enough for a second press, and a third.
  await page.keyboard.press("ControlOrMeta+Enter")
  await page.keyboard.press("ControlOrMeta+Enter")
  await page.keyboard.press("ControlOrMeta+Enter")
  // The button says the statement is being checked, and takes no press meanwhile.
  await expect(runButton(page)).toBeDisabled()
  classify.release()

  await expect(results(page)).toContainText("rows changed")
  await page.waitForTimeout(600)
  expect(sent.query).toHaveLength(1)
  expect(sent.script).toHaveLength(0)
  // Once it has come back the tab runs again.
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect.poll(() => sent.query.length).toBe(2)
})

test("a confirmation the reader declines leaves the tab free to run, with the keyboard in the editor", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(page, "drop table customers")
  await page.keyboard.press("ControlOrMeta+Enter")
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()
  await expect(page.getByRole("dialog")).toBeHidden()
  await expect.poll(() => focusIsInEditor(page)).toBe(true)
  // The key works at once: nothing has to be clicked first.
  await page.keyboard.press("ControlOrMeta+Enter")
  await page.getByRole("dialog").getByRole("button", { name: "Run statement" }).click()
  await expect.poll(() => sent.query.length).toBe(1)
  await expect.poll(() => focusIsInEditor(page)).toBe(true)
})

test("fetching more of one statement of a script keeps the other statements' results", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(page, "select count(*) from orders;\nselect many from customers;\nselect 3;")
  await page.keyboard.press("ControlOrMeta+Shift+Enter")

  const chips = results(page)
    .getByRole("group", { name: "Statements of this run" })
    .getByRole("button")
  await expect(chips).toHaveCount(3)
  await chips.nth(1).click()
  const notice = results(page).locator("[data-slot=result-truncated]")
  await expect(notice).toContainText("These are the first 500 rows.")
  await notice.getByRole("button", { name: "Fetch 1,000" }).click()

  // Only that statement is sent again, by itself, for more.
  await expect.poll(() => sent.query.length).toBe(1)
  expect(sent.query[0]).toMatchObject({ query: "select many from customers", maxRows: 1000 })
  expect(sent.script).toHaveLength(1)
  // The run is still the run: three statements, the reader still on the second.
  await expect(notice).toContainText("These are the first 1,000 rows.")
  await expect(chips).toHaveCount(3)
  await expect(chips.nth(1)).toHaveAttribute("aria-pressed", "true")
  await expect(chips.nth(1)).toContainText("1,000 rows, and more")
  await expect(notice.getByRole("button")).toHaveText(["Fetch 5,000"])
  await chips.first().click()
  await expect(results(page).getByRole("grid", { name: "Result of statement 1" })).toBeVisible()
  await results(page)
    .getByRole("tab", { name: /Messages/ })
    .click()
  await expect(
    results(page).locator("[data-slot=query-messages]").getByRole("listitem"),
  ).toHaveCount(5)
})

test("a quoted name is completed inside its quotes, the closing one the editor typed included", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  await editor(page).click()
  // Typed, not set: the editor closes the quote as it is typed.
  await page.keyboard.type('select * from "Mi', { delay: 30 })
  await expect.poll(() => editorText(page)).toBe('select * from "Mi"')
  const suggestions = page.locator(".suggest-widget .monaco-list-row")
  await expect(suggestions.first()).toContainText("Mixed Case Table")
  await page.keyboard.press("Enter")
  await expect.poll(() => editorText(page)).toBe('select * from "Mixed Case Table"')
})

test("a name of the reader's own is not completed, and a join is written from the foreign key", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  const list = page.locator(".suggest-widget.visible")
  await editor(page).click()
  await page.keyboard.type("select count(*) as d", { delay: 30 })
  await page.waitForTimeout(500)
  await expect(list).toHaveCount(0)
  // Enter is a new line, not a suggestion written over the alias.
  await page.keyboard.press("Enter")
  await page.keyboard.type("from orders o", { delay: 30 })
  await page.waitForTimeout(500)
  await expect(list).toHaveCount(0)
  await page.keyboard.press("Enter")
  await page.keyboard.type("join ", { delay: 30 })
  // What a foreign key ties to the tables already named comes first.
  await expect(list.locator(".monaco-list-row").first()).toContainText("customers")
  await page.keyboard.type("customers c on ", { delay: 30 })
  await expect(list.locator(".monaco-list-row").first()).toContainText("c.id = o.customer_id")
  await page.keyboard.press("Enter")
  await expect
    .poll(() => editorText(page))
    .toBe("select count(*) as d\nfrom orders o\njoin customers c on c.id = o.customer_id")
})

test("Escape, then Tab, leaves the editor: forward to the commands, back to the tabs", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  await write(page, "select 1")
  // The editor says its own way out, as the grid under it does.
  await expect(
    page.locator("[data-slot=sql-editor]").getByLabel(/Escape, then Tab, leaves the editor/),
  ).toHaveCount(1)

  // Tab alone is a character in an editor.
  await page.keyboard.press("Tab")
  await expect.poll(() => focusIsInEditor(page)).toBe(true)
  await page.keyboard.press("Escape")
  await page.keyboard.press("Tab")
  await expect(runButton(page)).toBeFocused()
  // From there Tab walks the strip and reaches what the statement returned.
  await editor(page).click()
  await page.keyboard.press("Escape")
  await page.keyboard.press("Shift+Tab")
  await expect(openTabs(page).first()).toBeFocused()
  await expect(openTabs(page).first()).toHaveAttribute("aria-controls", /.+/)
  await expect(page.getByRole("tabpanel", { name: "Query 1" })).toBeVisible()
  // Nothing in the list of tabs is anything but a tab.
  expect(
    await page
      .getByRole("tablist", { name: "Open statements" })
      .evaluate((list) =>
        [...list.children].every((child) => child.getAttribute("role") === "tab"),
      ),
  ).toBe(true)
})

test("a tab keeps its undo while another is looked at, and closing one hands the keyboard on", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  await write(page, "select 1")
  await page.getByRole("button", { name: "New tab" }).click()
  // A new tab is opened to be written in: the keyboard is already there.
  await expect.poll(() => focusIsInEditor(page)).toBe(true)
  await page.keyboard.type("select 2", { delay: 20 })
  await page.keyboard.type(" -- typed", { delay: 20 })
  await expect.poll(() => editorText(page)).toBe("select 2 -- typed")

  await openTabs(page).first().click()
  await expect.poll(() => editorText(page)).toBe("select 1")
  await openTabs(page).nth(1).click()
  await expect.poll(() => editorText(page)).toBe("select 2 -- typed")
  await editor(page).click()
  await page.keyboard.press("ControlOrMeta+Z")
  await expect.poll(() => editorText(page)).not.toBe("select 2 -- typed")
  expect(await editorText(page)).toContain("select 2")

  // Closed from the keyboard, the tab that comes to the front has the keyboard.
  await page.getByRole("button", { name: "Close Query 2" }).focus()
  await page.keyboard.press("Enter")
  await expect(openTabs(page)).toHaveCount(1)
  await expect(openTabs(page).first()).toBeFocused()
})

test("with more tabs than fit the one in front stays in view, and every tab is in one menu", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  for (let n = 0; n < 12; n++) await page.getByRole("button", { name: "New tab" }).click()
  await expect(openTabs(page)).toHaveCount(13)
  const strip = page.locator("[data-slot=query-tabs]")
  await expect
    .poll(async () => {
      const [outer, front] = await Promise.all([
        strip.boundingBox(),
        openTabs(page).last().boundingBox(),
      ])
      if (!outer || !front) return false
      return front.x >= outer.x - 1 && front.x + front.width <= outer.x + outer.width + 1
    })
    .toBe(true)
  await expect(openTabs(page).last()).toHaveAttribute("aria-selected", "true")

  await page.getByRole("button", { name: "All 13 tabs" }).click()
  await page.getByRole("menuitem", { name: "Query 2" }).click()
  await expect(openTabs(page).nth(1)).toHaveAttribute("aria-selected", "true")
  await expect(openTabs(page).nth(1)).toBeInViewport()
})

test("at the limit of tabs an opening says so, and a hand-over waits in the address for a tab", async ({
  page,
}) => {
  await mockWork(page, {
    history: [
      {
        id: 1,
        sql: "select count(*) from orders",
        risk: "read",
        success: true,
        durationMs: 4,
        rowCount: 1,
        ranAt: "2026-10-01T08:58:00Z",
      },
    ],
  })
  await page.addInitScript(() => {
    const key = "jd.session.state.entry.databases.1.query.tabs"
    if (window.sessionStorage.getItem(key)) return
    const tabs = Array.from({ length: 24 }, (_, n) => ({
      id: `q${n + 1}`,
      title: `Query ${n + 1}`,
      sql: `select ${n + 1}`,
    }))
    window.sessionStorage.setItem(key, JSON.stringify({ tabs, active: "q24", next: 25 }))
  })
  const handed = "select 'handed over at the limit'"
  await openEditor(page, `/databases/1/query?sql=${encodeURIComponent(handed)}`)
  await expect(openTabs(page)).toHaveCount(24)
  await expect(page.getByText("24 tabs are open").first()).toBeVisible()
  // Not lost: it is still in the address.
  expect(new URL(page.url()).searchParams.get("sql")).toBe(handed)
  await expect(page.getByRole("button", { name: "No more tabs can be opened" })).toBeDisabled()

  // A history entry at the limit says the same rather than doing nothing.
  const rail = page.locator("[data-slot=query-rail]")
  await rail.getByRole("tab", { name: /^History/ }).click()
  await rail
    .getByRole("list", { name: "Statements that were run" })
    .getByRole("button")
    .first()
    .click()
  await expect(page.getByText("Close one to open this statement.")).toBeVisible()
  await expect(openTabs(page)).toHaveCount(24)

  // A tab closed, and the statement handed over opens in its place.
  await page.getByRole("button", { name: "Close Query 24" }).click()
  await expect.poll(() => editorText(page)).toBe(handed)
  await expect(openTabs(page)).toHaveCount(24)
  await expect.poll(() => new URL(page.url()).search).toBe("")
})

test("a table made from the editor is known to the tree and the completion without asking", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await openEditor(page)
  await expect.poll(() => sent.outline.length).toBeGreaterThan(0)
  await write(page, "select * from customers")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page).getByRole("grid", { name: "Query result" })).toBeVisible()
  // A statement that reads changes nothing the schema holds.
  const before = sent.outline.length
  await write(page, "create table a4_new (id int)")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page)).toContainText("rows changed")
  await expect.poll(() => sent.outline.length).toBeGreaterThan(before)
})

test("a failed statement is marked at the word the engine names, where it names one", async ({
  page,
}) => {
  await mockWork(page)
  await openEditor(page)
  await write(page, "select id, email from nope where id = 1")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page).locator("[data-slot=query-refusal]")).toContainText(
    'relation "nope" does not exist',
  )
  const mark = page.locator("[data-slot=sql-editor] .squiggly-error")
  await expect(mark.first()).toBeVisible()
  // The mark is as wide as the name, not as the statement.
  const [squiggle, line] = await Promise.all([
    mark.first().boundingBox(),
    page.locator("[data-slot=sql-editor] .view-line").first().boundingBox(),
  ])
  expect(squiggle!.width).toBeLessThan(line!.width / 3)
})

test("series of different magnitudes are not drawn on one axis; each measure is a chip", async ({
  page,
}) => {
  await mockWork(page)
  await page.route("**/api/v1/databases/1/query", (route) =>
    json(route, {
      result: {
        columns: ["day", "orders", "revenue"],
        types: ["DATE", "INT8", "NUMERIC"],
        kinds: ["date", "integer", "decimal"],
        rows: [
          ["2026-09-29", "120", "98000.50"],
          ["2026-09-30", "131", "105162.00"],
          ["2026-10-01", "97", "81020.25"],
        ],
        rowCount: 3,
        rowsAffected: 0,
        duration: "2ms",
        truncated: false,
        statement: "",
      },
      risk: READ,
    }),
  )
  await openEditor(page)
  await write(page, "select day, count(*) as orders, sum(total) as revenue from orders group by 1")
  await page.keyboard.press("ControlOrMeta+Enter")
  await results(page).getByRole("tab", { name: "Chart" }).click()

  const measures = results(page).getByRole("group", { name: "Measures drawn" })
  // Revenue would have flattened orders to a line along the bottom: orders is drawn alone.
  await expect(measures.getByRole("button", { name: "orders" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(measures.getByRole("button", { name: "revenue" })).toHaveAttribute(
    "aria-pressed",
    "false",
  )
  await expect(results(page)).toContainText("orders by day")
  await measures.getByRole("button", { name: "revenue" }).click()
  await expect(results(page)).toContainText("orders, revenue by day")
  // One is always drawn.
  await measures.getByRole("button", { name: "orders" }).click()
  await measures.getByRole("button", { name: "revenue" }).click()
  await expect(measures.getByRole("button", { name: "revenue" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("the keys a hint names are the reader's own keyboard's", async ({ browser }) => {
  // The bindings are Command on a Mac; "Ctrl+Enter" there names another key.
  const context = await browser.newContext({
    userAgent:
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36",
  })
  const page = await context.newPage()
  await page.addInitScript(() =>
    Object.defineProperty(navigator, "platform", { get: () => "MacIntel" }),
  )
  await mockWork(page)
  await openEditor(page)
  await write(page, "select 1")
  await expect(runButton(page)).toHaveAttribute("title", "⌘Enter")
  await expect(results(page)).toContainText("⌘Enter runs it from the editor")
  await context.close()
})

test("on a narrow pane a cut result keeps its rows: one control for find and columns, one for more", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const { sent } = await mockWork(page)
  await openEditor(page)
  await write(page, "select count(*) from orders;\nselect many from customers;")
  await page.keyboard.press("ControlOrMeta+Shift+Enter")
  const notice = results(page).locator("[data-slot=result-truncated]")
  await expect(notice).toBeVisible()

  // The banner is one line, and what leads to more rows is one control on it.
  expect((await notice.boundingBox())!.height).toBeLessThan(44)
  await expect(results(page).getByPlaceholder("Find in these rows")).toHaveCount(0)
  // The rows have the room: more than half of what the pane has under its tabs.
  const pane = (await results(page).boundingBox())!
  const rows = (await results(page).locator("[data-slot=data-grid-viewport]").boundingBox())!
  expect(rows.height).toBeGreaterThan((pane.height - 36) / 2)

  await results(page).getByRole("button", { name: "Find in these rows, or choose columns" }).click()
  await results(page).getByPlaceholder("Find in these rows").fill("user2@")
  await expect(results(page).getByRole("gridcell", { name: "user2@example.com" })).toBeVisible()
  await expect(results(page).getByRole("button", { name: "Columns", exact: true })).toBeVisible()

  await notice.getByRole("button", { name: "Fetch more" }).click()
  await page.getByRole("menuitem", { name: "Fetch 5,000" }).click()
  await expect.poll(() => sent.query.at(-1)?.maxRows).toBe(5000)
})

test("a search of ticked tables reads those tables and no others, and says which hold more", async ({
  page,
}) => {
  const { sent } = await mockWork(page)
  await page.goto("/databases/1/search")
  // The scope is chosen before the scan: which of this schema's tables are read.
  await page.getByRole("button", { name: "Every table (3)" }).click()
  const list = page.getByRole("list", { name: "The tables read" })
  await expect(list.getByRole("listitem")).toHaveCount(3)
  // The view is left out, as a search of the whole schema leaves it out.
  await expect(list).not.toContainText("order_summary")
  await list.getByText("Mixed Case Table", { exact: true }).click()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("button", { name: "2 of 3 tables" })).toBeVisible()

  await page.getByLabel("The value to find").fill("pro")
  await page.keyboard.press("Enter")
  const groups = page.locator("[data-slot=search-table]")
  await expect(groups).toHaveCount(1)
  // The whole-schema search was not asked; each ticked table was, for the value in any column.
  expect(sent.search).toHaveLength(0)
  expect(sent.browse.map((url) => url.searchParams.get("table")).sort()).toEqual([
    "customers",
    "orders",
  ])
  const asked = sent.browse.find((url) => url.searchParams.get("table") === "customers")!
  expect(asked.searchParams.get("match")).toBe("any")
  expect(asked.searchParams.get("limit")).toBe("5")
  expect(JSON.parse(asked.searchParams.get("filters") ?? "[]")).toEqual([
    { column: "id", op: "icontains", value: "pro" },
    { column: "email", op: "icontains", value: "pro" },
    { column: "tier", op: "icontains", value: "pro" },
  ])
  // The question, tables included, is in the address.
  expect(JSON.parse(new URL(page.url()).searchParams.get("tables") ?? "[]")).toEqual([
    "customers",
    "orders",
  ])
  await expect(page.getByRole("status").filter({ hasText: "is in" })).toContainText(
    "pro is in 1 table: 2 rows are shown, at most 5 from each. 2 tables of public read",
  )
  // The read said the table holds more than these two, and its key: no second read for it.
  await expect(groups.first()).toContainText("2+")
  const link = groups.first().getByRole("link", { name: "Open this row of customers in Data" })
  await expect(link.first()).toHaveAttribute("href", /filters=/)
  expect(
    JSON.parse(
      new URL((await link.first().getAttribute("href")) ?? "", page.url()).searchParams.get(
        "filters",
      ) ?? "[]",
    ),
  ).toEqual([{ column: "id", op: "eq", value: "12" }])
  // A table showing its first rows is not a warning.
  await expect(page.getByText("There may be more than this")).toHaveCount(0)

  // Every schema has no one list of tables to tick.
  await page
    .getByRole("group", { name: "Which schema is read" })
    .getByRole("button", { name: "Every schema" })
    .click()
  await expect(page.getByRole("button", { name: /Every table|No tables|Reading/ })).toBeDisabled()
  await expect.poll(() => sent.search.length).toBe(1)
  await expect.poll(() => new URL(page.url()).searchParams.get("tables")).toBeNull()
  expect(new URL(page.url()).searchParams.get("scope")).toBe("all")
})

test("a generator remembered from another engine does not open the page on a refusal", async ({
  page,
}) => {
  await mockWork(page)
  await page.goto("/databases/1/generate")
  // Zod is written for PostgreSQL only in this catalogue.
  await page.locator("[data-target=zod]").click()
  await expect(page.locator("[data-slot=code-view]")).toContainText("schemas.ts")

  await page.goto("/databases/7/generate")
  await expect(page.locator("[data-target=prisma]")).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByText(/is not written for/)).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Generate" })).toBeVisible()
  // Picked on this visit, the one it lacks says why, in the server's words.
  await page.getByRole("button", { name: "Zod schemas, not for SQLite" }).click()
  await expect(page.getByText("Zod schemas is not written for SQLite")).toBeVisible()
})

/* -------------------------------------------------- the design system */

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

async function keepsTheRules(page: Page, where: string) {
  expect(await unnamedControls(page), `unlabelled icon-only controls: ${where}`).toEqual([])
  expect(await offCentreText(page), `text off its row's centre line: ${where}`).toEqual([])
  expect(await filledPills(page), `a filled pill: ${where}`).toEqual([])
  const seen = await page.evaluate(() => ({
    registers: [...document.querySelectorAll<HTMLElement>("[data-slot='page']")].map(
      (el) => el.dataset.register ?? "(unset)",
    ),
    panels: document.querySelectorAll("[data-slot='flow-panel']").length,
    sideways: [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  }))
  expect(seen.registers, `the register: ${where}`).toEqual(["reading"])
  expect(seen.panels, `a flow panel on a reading page: ${where}`).toBe(0)
  expect(seen.sideways, `scrolls sideways: ${where}`).toBe(false)
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the editor, the search and the generator keep the design system's rules with their data on screen${label}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport)
    await mockWork(page, {
      saved: [{ id: 3, name: "Pro customers", sql: "select * from customers where tier = 'pro'" }],
      history: [
        {
          id: 1,
          sql: "select * from nope",
          risk: "read",
          success: false,
          durationMs: 1,
          rowCount: 0,
          error: "no such relation",
          ranAt: "2026-10-01T08:59:00Z",
        },
      ],
    })
    const phone = viewport.width < 700
    const rail = page.locator("[data-slot=query-rail]")

    await openEditor(page)
    await keepsTheRules(page, "the editor, empty")
    await write(
      page,
      "select count(*) from orders;\nselect many from customers;\nselect * from nope;",
    )
    await page.keyboard.press("ControlOrMeta+Shift+Enter")
    await expect(results(page).locator("[data-slot=query-refusal]")).toBeVisible()
    await keepsTheRules(page, "a failed statement")
    await results(page)
      .getByRole("group", { name: "Statements of this run" })
      .getByRole("button")
      .nth(1)
      .click()
    await expect(results(page).locator("[data-slot=result-truncated]")).toBeVisible()
    await keepsTheRules(page, "a cut result")
    await results(page)
      .getByRole("tab", { name: /Messages/ })
      .click()
    await keepsTheRules(page, "the messages")
    await commands(page).getByRole("button", { name: "Explain", exact: true }).click()
    await expect(results(page).locator("[data-slot=plan-tree]")).toBeVisible()
    await keepsTheRules(page, "a plan")

    // On a phone the rail lies over the editor, and is put away by choosing from it.
    if (phone) {
      await expect(rail).toHaveCount(0)
      await page.getByRole("button", { name: "Show the rail" }).click()
    }
    for (const name of ["Saved", "History", "Schema", "Snippets"]) {
      await rail.getByRole("tab", { name: new RegExp(`^${name}`) }).click()
      await keepsTheRules(page, `the rail's ${name}`)
    }
    if (phone) {
      await rail.getByRole("button", { name: "Open in a tab: Largest tables" }).click()
      await expect(rail).toHaveCount(0)
    }

    await page.goto("/databases/1/search?q=pro")
    await expect(page.locator("[data-slot=search-table]").first()).toBeVisible()
    await keepsTheRules(page, "search results")
    await page.getByRole("button", { name: /^Every table/ }).click()
    await expect(page.getByRole("list", { name: "The tables read" })).toBeVisible()
    await keepsTheRules(page, "the table picker")
    await page.keyboard.press("Escape")

    await page.goto("/databases/1/generate")
    // Where the generators cannot stand beside the files they are folded to the chosen one.
    const generators = page.getByRole("button", { name: /^All \d+ generators$/ })
    if (phone) {
      await expect(page.locator("[data-target]")).toHaveCount(0)
      await generators.click()
      await keepsTheRules(page, "the generators, unfolded")
    }
    await page.locator("[data-target=typeorm]").click()
    await expect(page.locator("[data-slot=code-view]")).toContainText("entities.ts")
    if (phone) {
      // Choosing one folds the list away, and its files are in the first screen.
      await expect(page.locator("[data-target]")).toHaveCount(0)
      const top = (await page.locator("[data-slot=code-view]").boundingBox())?.y ?? Infinity
      expect(top).toBeLessThan(viewport.height)
    }
    await page.getByRole("button", { name: /^Options/ }).click()
    await keepsTheRules(page, "generated files")
    if (phone) await generators.click()
    await page.locator("[data-target=diesel]").click()
    await expect(page.getByText("Diesel is not written for PostgreSQL")).toBeVisible()
    await keepsTheRules(page, "a generator the engine lacks")
  })
}
