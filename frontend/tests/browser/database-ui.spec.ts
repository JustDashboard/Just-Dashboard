import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The databases section, after its redesign: the connection is the title, the
 * schema-editing forms show their statement, a server found on the host is
 * offered by its engine's name (it used to be offered as the literal text
 * "{server.driver}"), and the diagram remembers what was done to it — on the
 * server, so a hidden table stays hidden across a reload.
 */

const now = new Date().toISOString()

const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

const connection = {
  id: 1,
  name: "shop",
  driver: "postgres",
  host: "127.0.0.1",
  port: "5432",
  user: "app",
  database: "shop",
  createdAt: now,
}

const drivers = [
  {
    id: "postgres",
    label: "PostgreSQL",
    kind: "sql",
    placeholder: "postgres://user:password@127.0.0.1:5432/dbname?sslmode=disable",
    sql: true,
    ddl: true,
    columnTypes: ["bigserial", "integer", "text", "timestamptz"],
    filterOps: ["eq", "ne", "contains", "is_null", "not_null"],
  },
]

const tables = [
  { schema: "public", name: "users", type: "table", estimatedRows: 1200, size: 1048576 },
  { schema: "public", name: "orders", type: "table", estimatedRows: 8400, size: 4194304 },
  { schema: "public", name: "audit_log", type: "table", estimatedRows: 0, size: 8192 },
]

const columns: Record<
  string,
  { name: string; type: string; nullable: boolean; position: number }[]
> = {
  users: [
    { name: "id", type: "int4", nullable: false, position: 1 },
    { name: "email", type: "text", nullable: false, position: 2 },
  ],
  orders: [
    { name: "id", type: "int4", nullable: false, position: 1 },
    { name: "customer_id", type: "int4", nullable: false, position: 2 },
    { name: "total", type: "numeric", nullable: true, position: 3 },
  ],
  audit_log: [
    { name: "id", type: "int8", nullable: false, position: 1 },
    { name: "payload", type: "jsonb", nullable: true, position: 2 },
  ],
}

const graph = {
  schema: "public",
  tables: tables.map((t) => ({
    schema: t.schema,
    name: t.name,
    type: t.type,
    rows: t.estimatedRows,
    columns: columns[t.name].map((c) => ({
      name: c.name,
      type: c.type,
      nullable: c.nullable,
      primaryKey: c.name === "id",
      foreignKey: c.name === "customer_id" ? "users" : undefined,
    })),
  })),
  edges: [
    {
      name: "orders_customer_id_fkey",
      fromTable: "orders",
      fromColumn: "customer_id",
      toTable: "users",
      toColumn: "id",
      onDelete: "CASCADE",
      cardinality: "many-to-one",
    },
  ],
  truncated: false,
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * Just enough of the API for the section. The diagram layout store is real
 * enough to remember: a PUT is kept and handed back on the next GET, which is
 * what the reload assertion below relies on.
 */
async function mockDatabases(page: Page, state: { layout: unknown; puts: unknown[] }) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self" || path === "/dashboard/update")
      return json(route, { current: "0.6.7", latest: "0.6.7" })
    if (path === "/databases/") return json(route, [connection])
    if (path === "/databases/drivers") return json(route, drivers)
    if (path === "/databases/sync")
      return json(route, {
        added: [],
        already: [],
        needsCredentials: [
          {
            driver: "postgres",
            host: "127.0.0.1",
            port: 5432,
            process: "postgres",
            name: "postgres on this server",
            user: "postgres",
            database: "postgres",
          },
        ],
      })
    if (path === "/databases/1/ping") return json(route, { ok: true })
    if (path === "/databases/1/tables") return json(route, tables)
    if (path === "/databases/1/table") {
      const table = url.searchParams.get("table") ?? "users"
      return json(route, {
        schema: "public",
        name: table,
        columns: columns[table] ?? [],
        primaryKey: ["id"],
        indexes: [{ name: `${table}_pkey`, columns: ["id"], unique: true, primary: true }],
        foreignKeys:
          table === "orders"
            ? [
                {
                  name: "orders_customer_id_fkey",
                  columns: ["customer_id"],
                  refTable: "users",
                  refColumns: ["id"],
                  onDelete: "CASCADE",
                },
              ]
            : [],
        createSql: `CREATE TABLE ${table} ()`,
      })
    }
    if (path === "/databases/1/browse")
      return json(route, {
        columns: ["id", "email"],
        types: ["int4", "text"],
        rows: [[1, "ada@example.com"]],
        rowCount: 1,
        rowsAffected: 0,
        duration: "1ms",
        truncated: false,
        statement: "",
      })
    if (path === "/databases/1/graph") return json(route, graph)
    if (path === "/databases/1/diagram") {
      if (method === "GET")
        return json(route, { layout: state.layout, updatedAt: state.layout ? now : undefined })
      if (method === "PUT") {
        const body = route.request().postDataJSON() as { layout: unknown }
        state.layout = body.layout
        state.puts.push(body.layout)
        return json(route, { layout: body.layout, updatedAt: now })
      }
      if (method === "DELETE") {
        state.layout = null
        return route.fulfill({ status: 204 })
      }
    }
    if (path === "/databases/1/overview")
      return json(route, {
        schema: "",
        tables: [],
        totalBytes: 5242880,
        totalRows: 9600,
        tableCount: 3,
        sizesKnown: true,
        pool: {
          open: 1,
          inUse: 0,
          idle: 1,
          waitCount: 0,
          waitDuration: "0s",
          maxOpen: 4,
          maxIdleClosed: 0,
          maxLifetimeClosed: 0,
        },
      })
    return json(route, [])
  })
}

test("the connection is the section's title and the tables are on the rail", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases")

  await expect(
    page.getByRole("button", { name: "Connection: shop. Switch connection" }),
  ).toBeVisible()
  await expect(
    page.getByLabel("Database section").getByRole("link", { name: "Diagram" }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: /^orders/ })).toBeVisible()

  await page.getByRole("button", { name: /^users/ }).click()
  await expect(page.getByText("ada@example.com")).toBeVisible()
  await expect(page.getByRole("button", { name: "Insert" })).toBeVisible()
})

test("the create table form shows the statement it will run", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases?conn=1")

  await page.getByRole("button", { name: "Create table" }).click()
  const dialog = page.getByRole("dialog")
  await expect(
    dialog.getByText("Name the table and give at least one column a name and a type."),
  ).toBeVisible()

  await dialog.getByLabel("Table name").fill("invoices")
  await dialog.getByLabel("Column 1 name").fill("id")
  await dialog.getByLabel("Column type").fill("integer")
  await dialog.getByRole("button", { name: "Primary key" }).click()
  await expect(dialog.getByText(/CREATE TABLE invoices/)).toBeVisible()
  await expect(dialog.getByText(/id integer NOT NULL PRIMARY KEY/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Create table" })).toBeEnabled()
})

test("a server found on the host is offered by its engine's name", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases?conn=1")

  // The sync toast carries the one way in; the dialog used to be titled with
  // the literal text "{server.driver}".
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(
    page.getByRole("dialog", { name: "Connect PostgreSQL on this server" }),
  ).toBeVisible()
  await expect(page.getByText("{server.driver}")).toHaveCount(0)
})

test("the diagram draws every table and remembers what was hidden", async ({ page }) => {
  const state = { layout: null as unknown, puts: [] as unknown[] }
  await mockDatabases(page, state)
  await page.goto("/databases/diagram?conn=1")

  const nodes = page.locator(".react-flow__node")
  await expect(nodes).toHaveCount(3)
  await expect(page.getByRole("button", { name: "Full screen" })).toBeVisible()

  await page.getByRole("button", { name: "Export the diagram" }).click()
  await expect(page.getByRole("menuitem", { name: /PNG image/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Mermaid/ })).toBeVisible()
  await page.keyboard.press("Escape")

  // Hiding from the inspector saves the arrangement on the server…
  const saved = page.waitForResponse(
    (r) => r.request().method() === "PUT" && r.url().includes("/databases/1/diagram"),
  )
  await page.getByRole("checkbox", { name: "Show audit_log on the diagram" }).click()
  await expect(nodes).toHaveCount(2)
  await saved
  expect((state.layout as { hidden: string[] }).hidden).toEqual(["audit_log"])
  await expect(page.getByRole("button", { name: "1 table hidden" })).toBeVisible()

  // …and it is what the next visit opens on.
  await page.reload()
  await expect(nodes).toHaveCount(2)
  await expect(page.getByRole("button", { name: "1 table hidden" })).toBeVisible()
})

test("structure picks a table from its own rail and edits from there", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/structure?conn=1")

  await page.getByRole("button", { name: /^orders/ }).click()
  await expect(page.getByRole("cell", { name: "customer_id" }).first()).toBeVisible()
  await page.getByRole("button", { name: "Add column" }).click()
  const dialog = page.getByRole("dialog", { name: "Add column" })
  await expect(dialog.getByText("public.orders")).toBeVisible()
  await dialog.getByLabel("Name").fill("shipped_at")
  await dialog.getByRole("textbox", { name: "Type" }).fill("timestamptz")
  await expect(dialog.getByText(/ADD COLUMN shipped_at timestamptz/)).toBeVisible()
})

for (const path of [
  "/databases?conn=1",
  "/databases/diagram?conn=1",
  "/databases/connection?conn=1",
]) {
  test(`every icon-only control on ${path} has an accessible name`, async ({ page }) => {
    await mockDatabases(page, { layout: null, puts: [] })
    await page.goto(path)
    await page.waitForLoadState("networkidle")

    const unnamed = await page.evaluate(() => {
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
    expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])
  })
}
