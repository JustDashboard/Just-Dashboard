import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The databases section, after its redesign: the connection is the workbench location, the
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

/** A second connection, so switching between them can be asserted. */
const otherConnection = {
  id: 2,
  name: "cache",
  driver: "postgres",
  host: "127.0.0.1",
  port: "5433",
  user: "app",
  database: "cache",
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

/** A database this dashboard started: published to loopback, in a container it can recreate. */
const localAccess = {
  detected: true,
  container: "shop-db",
  managed: true,
  exposure: "local",
  port: 5432,
  publicAddresses: ["203.0.113.9"],
  firewall: { backend: "ufw", active: true, open: false, editable: true },
}

/** Every connection dialled at once, as the control center reads it. */
const fleet = {
  checkedAt: now,
  connections: [
    {
      ...connection,
      ok: true,
      version: "PostgreSQL 16.4",
      latencyMs: 3,
      bytes: 5242880,
      sizesKnown: true,
      objects: 3,
      objectWord: "tables",
      sessions: 2,
      source: "docker",
      container: "shop-db",
      exposure: "local",
      consumers: 1,
      lastBackup: now,
    },
    {
      ...otherConnection,
      ok: false,
      error: "connection refused",
      latencyMs: 0,
      bytes: 0,
      sizesKnown: false,
      objects: 0,
      objectWord: "tables",
      sessions: 0,
      source: "docker",
      container: "cache-db",
      exposure: "public",
      consumers: 0,
    },
  ],
  unreachable: [],
  needsCredentials: [
    {
      driver: "postgres",
      host: "127.0.0.1",
      port: 5433,
      process: "postgres",
      name: "postgres on this server",
      user: "postgres",
      database: "postgres",
    },
  ],
}

/** What feeds what: one deployment bound to shop, one container seen connected. */
const topology = {
  checkedAt: now,
  nodes: [
    {
      id: "db:1",
      kind: "database",
      name: "shop",
      product: "postgres",
      detail: "shop",
      connId: 1,
      href: "/databases/browse?conn=1",
    },
    {
      id: "deploy:7",
      kind: "deployment",
      name: "api",
      product: "nextjs",
      detail: "jd-e7-r1",
      status: "connected",
      href: "/deploy/7",
    },
    {
      id: "container:worker",
      kind: "container",
      name: "worker",
      product: "python",
      detail: "python:3.12",
      status: "running",
      href: "/docker/containers/abc",
    },
  ],
  edges: [
    { from: "db:1", to: "deploy:7", via: ["binding", "session"], sessions: 2, status: "connected" },
    { from: "db:1", to: "container:worker", via: ["env"], sessions: 0, status: "observed" },
  ],
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * Just enough of the API for the section. The diagram layout store is real
 * enough to remember: a PUT is kept and handed back on the next GET, which is
 * what the reload assertion below relies on.
 */
async function mockDatabases(
  page: Page,
  state: {
    layout: unknown
    puts: unknown[]
    access?: unknown
    deletes?: unknown[]
    /** Every /export request's query string, so the download can be asserted. */
    exports?: string[]
    /** Every /browse request's query string, for the paging assertions. */
    browses?: string[]
  },
) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (path === "/auth/session") return json(route, user)
    if (path === "/databases/1/access") return json(route, state.access ?? localAccess)
    if (path === "/databases/1/url") {
      const target = url.searchParams.get("target")
      const host = target === "public" ? "203.0.113.9" : "127.0.0.1"
      return json(route, {
        id: 1,
        name: "shop",
        driver: "postgres",
        url: `postgres://app:s3cret@${host}:5432/shop?sslmode=disable`,
        reference: "",
      })
    }
    if (path === "/databases/1/database" && method === "DELETE") {
      state.deletes?.push(route.request().postDataJSON())
      return json(route, { detail: "container shop-db removed", connectionRemoved: true })
    }
    if (path === "/updates/self" || path === "/dashboard/update")
      return json(route, { current: "0.6.7", latest: "0.6.7" })
    if (path === "/databases/") return json(route, [connection, otherConnection])
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
    if (path === "/databases/1/ping" || path === "/databases/2/ping")
      return json(route, { ok: true })
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
    if (path === "/databases/1/relations")
      return json(route, {
        orders: [
          {
            name: "orders_customer_id_fkey",
            columns: ["customer_id"],
            refTable: "users",
            refColumns: ["id"],
            onDelete: "CASCADE",
          },
        ],
      })
    if (path === "/databases/1/count") return json(route, { count: 1200 })
    if (path === "/databases/1/export") {
      state.exports?.push(url.search)
      // A download still has to be fulfilled, or the navigation never settles.
      return route.fulfill({ status: 200, contentType: "text/csv", body: "id,email\n" })
    }
    if (path === "/databases/1/browse") {
      state.browses?.push(url.search)
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
    }
    if (path === "/databases/fleet") return json(route, fleet)
    if (path === "/databases/topology" || path === "/databases/1/consumers")
      return json(route, topology)
    if (path === "/databases/1/schemas")
      return json(route, [
        { name: "shop", size: 5242880, owner: "app", encoding: "UTF8" },
        { name: "analytics", size: 1048576, owner: "app", encoding: "UTF8" },
      ])
    if (path === "/databases/1/server/roles")
      return json(route, {
        supported: true,
        roles: [
          {
            name: "app",
            login: true,
            superuser: false,
            createDb: true,
            createRole: false,
            connectionLimit: -1,
            connections: 2,
          },
          {
            name: "postgres",
            login: true,
            superuser: true,
            createDb: true,
            createRole: true,
            connectionLimit: -1,
            connections: 0,
          },
        ],
      })
    if (path === "/databases/1/server/extensions")
      return json(route, {
        supported: true,
        editable: true,
        extensions: [
          {
            name: "pgcrypto",
            version: "1.3",
            availableVersion: "1.3",
            installed: true,
            schema: "public",
            comment: "cryptographic functions",
          },
          {
            name: "vector",
            availableVersion: "0.7.0",
            installed: false,
            comment: "vector data type and ivfflat and hnsw access methods",
          },
        ],
      })
    if (path === "/databases/1/server/settings")
      return json(route, {
        supported: true,
        settings: [
          {
            name: "max_connections",
            value: "100",
            category: "Connections",
            description: "Sets the maximum number of concurrent connections.",
            source: "configuration file",
            restartRequired: true,
          },
        ],
      })
    if (path === "/databases/1/advisor")
      return json(route, {
        tablesChecked: 3,
        engineChecks: true,
        findings: [
          {
            id: "unindexed-foreign-key",
            level: "warning",
            category: "performance",
            title: "1 foreign key with no index",
            detail: "Every delete of the referenced row scans the referencing table.",
            advice: "Create an index on the referencing columns.",
            objects: ["orders(customer_id)"],
            sql: 'CREATE INDEX "orders_customer_id_idx" ON "public"."orders" ("customer_id");',
          },
        ],
      })
    if (path === "/databases/1/statements")
      return json(route, {
        supported: true,
        totalMs: 12000,
        statements: [
          {
            id: "1",
            query: "SELECT * FROM orders WHERE customer_id = $1",
            calls: 900,
            totalMs: 9000,
            meanMs: 10,
            maxMs: 80,
            rows: 900,
            hitRatio: 0.99,
          },
        ],
      })
    if (path === "/databases/1/backups")
      return json(route, {
        dir: "/var/backups/just-dashboard/databases/shop",
        files: [
          {
            file: "shop-2026-09-24T02-00-00.dump",
            size: 2048000,
            takenAt: now,
            format: "pg_dump archive",
          },
        ],
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

test("the connection sits in the compact workbench strip and the tables are on the rail", async ({
  page,
}) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/browse")

  await expect(
    page.getByRole("button", { name: "Connection: shop. Switch connection" }),
  ).toBeVisible()
  await expect(page.getByRole("heading", { name: "Database shop" })).toHaveClass(/sr-only/)
  await page.screenshot({ path: "test-results/database-context-1280.png", fullPage: true })
  // The section's pages are the sidebar's, not a strip above the page, and
  // every one of them carries the connection it was opened with.
  const rail = page.getByRole("navigation", { name: "Sidebar" })
  await expect(rail.getByRole("link", { name: "Diagram" })).toHaveAttribute(
    "href",
    "/databases/diagram?conn=1",
  )
  await expect(page.getByRole("button", { name: /^orders/ })).toBeVisible()

  await page.getByRole("button", { name: /^users/ }).click()
  await expect(page.getByText("ada@example.com")).toBeVisible()
  await expect(page.getByRole("button", { name: "Insert" })).toBeVisible()
})

test("the connection strip wraps without sideways scrolling on a phone", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/databases/browse")
  await expect(
    page.getByRole("button", { name: "Connection: shop. Switch connection" }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "New database" })).toBeVisible()
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)
  await page.screenshot({ path: "test-results/database-context-phone.png", fullPage: true })
})

/**
 * The export is the view. Narrowing the grid and pressing Export as CSV used to
 * download the whole table, which looks exactly like a correct export until
 * somebody opens the file.
 */
test("exporting carries the conditions and the order the grid is under", async ({ page }) => {
  const exports: string[] = []
  await mockDatabases(page, { layout: null, puts: [], exports })
  await page.goto("/databases/browse?conn=1")

  await page.getByRole("button", { name: /^users/ }).click()
  await expect(page.getByText("ada@example.com")).toBeVisible()

  // Order by a column, then narrow to one value.
  await page.getByRole("button", { name: "email", exact: true }).click()
  await page.getByRole("button", { name: "Filter rows" }).click()
  await page.getByRole("button", { name: "Add condition" }).click()
  await page.getByRole("textbox", { name: "Value" }).fill("ada@example.com")

  await page.getByRole("button", { name: "More table actions" }).click()
  await page.getByRole("menuitem", { name: /Export as CSV/ }).click()

  await expect.poll(() => exports.length).toBeGreaterThan(0)
  const query = exports[exports.length - 1]
  expect(query).toContain("orderBy=email")
  expect(query).toContain("ada%40example.com")
  expect(query).toContain("limit=100000")
})

/**
 * A grid can always say what a row points at. Saying what points *at* it needs
 * the rest of the schema — the route for which existed from the start and was
 * called by nothing.
 */
test("a row says which tables reference it, and following one lands filtered", async ({ page }) => {
  const browses: string[] = []
  await mockDatabases(page, { layout: null, puts: [], browses })
  await page.goto("/databases/browse?conn=1")

  await page.getByRole("button", { name: /^users/ }).click()
  await page.getByRole("button", { name: "More row actions" }).first().click()
  // The row names the table and, beside it, the column that points here.
  const reference = page.getByRole("menuitem", { name: /orders/ })
  await expect(reference).toContainText("customer_id")
  await reference.click()

  // It is the same navigation the rail does, plus a filter: the orders table,
  // narrowed to the row that was open.
  await expect.poll(() => browses.some((q) => q.includes("table=orders"))).toBe(true)
  const landed = browses.filter((q) => q.includes("table=orders")).pop() ?? ""
  expect(decodeURIComponent(landed)).toContain('"column":"customer_id"')
  await expect(page.getByRole("button", { name: "Remove this condition" })).toBeVisible()
})

/** Paging past the first few hundred rows, without pressing Next four hundred times. */
test("the grid pages by typing a page number and by First and Last", async ({ page }) => {
  const browses: string[] = []
  await mockDatabases(page, { layout: null, puts: [], browses })
  await page.goto("/databases/browse?conn=1")

  await page.getByRole("button", { name: /^users/ }).click()
  await expect(page.getByText("ada@example.com")).toBeVisible()
  // The sync notice about a server that needs credentials never expires by
  // design, and it sits over the footer's controls at this height.
  await page.getByLabel("Close toast").click()

  await page.getByRole("textbox", { name: "Page" }).fill("7")
  await page.getByRole("textbox", { name: "Page" }).press("Enter")
  await expect.poll(() => browses.some((q) => q.includes("offset=600"))).toBe(true)

  // Last needs a count, so it appears once the table has been counted.
  await expect(page.getByRole("button", { name: "Last" })).toHaveCount(0)
  await page.getByRole("button", { name: "Count all rows" }).click()
  await expect(page.getByRole("button", { name: "Last" })).toBeVisible()
  await page.getByRole("button", { name: "Last" }).click()
  await expect.poll(() => browses.some((q) => q.includes("offset=1100"))).toBe(true)

  await page.getByRole("button", { name: "First" }).click()
  await expect(page.getByRole("textbox", { name: "Page" })).toHaveValue("1")
})

/**
 * An exact count belongs to the conditions it was taken under. When it did not,
 * counting a filtered view and then clearing the filter left the page count
 * clamped to the filtered total — Next was enabled and did nothing, and the
 * button that would have corrected the figure was hidden because a figure existed.
 */
test("counting a filtered view does not clamp paging once the filter is gone", async ({ page }) => {
  const browses: string[] = []
  await mockDatabases(page, { layout: null, puts: [], browses })
  await page.goto("/databases/browse?conn=1")

  await page.getByRole("button", { name: /^users/ }).click()
  await expect(page.getByText("ada@example.com")).toBeVisible()
  await page.getByLabel("Close toast").click()

  // Count under a filter…
  await page.getByRole("button", { name: "Filter rows" }).click()
  await page.getByRole("button", { name: "Add condition" }).click()
  await page.getByRole("textbox", { name: "Value" }).fill("ada@example.com")
  await page.getByRole("button", { name: "Count all rows" }).click()
  await expect(page.getByRole("button", { name: "Last" })).toBeVisible()

  // …then drop it. The count no longer describes this view, so the page total
  // and the Last it implied go with it, and paging is free again.
  await page.getByRole("button", { name: "Remove this condition" }).click()
  await expect(page.getByRole("button", { name: "Last" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Count all rows" })).toBeVisible()

  // The clamp is what broke: under the stale count the page field could not
  // leave page 1, because ceil(1 / 100) said there was only one page.
  browses.length = 0
  await page.getByRole("textbox", { name: "Page" }).fill("3")
  await page.getByRole("textbox", { name: "Page" }).press("Enter")
  await expect.poll(() => browses.some((q) => q.includes("offset=200"))).toBe(true)
  await expect(page.getByRole("textbox", { name: "Page" })).toHaveValue("3")
})

/**
 * The value viewer is the one way to read a JSON blob the column has truncated,
 * and it hung off a click handler on the `<td>` — which a keyboard cannot reach.
 */
test("a cell opens its value from the keyboard", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/browse?conn=1")

  await page.getByRole("button", { name: /^users/ }).click()
  const cell = page.getByRole("button", { name: "ada@example.com", exact: true })
  await cell.focus()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("dialog")).toContainText("ada@example.com")
})

/**
 * The rail's filter and schema belong to the connection being read. Under one
 * shared key, a schema chosen on one engine emptied the rail on the next.
 */
test("the table filter belongs to the connection, not to the rail", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/browse?conn=1")

  await page.getByRole("textbox", { name: "Filter tables" }).fill("orders")
  await expect(page.getByRole("button", { name: /^users/ })).toHaveCount(0)

  await page.goto("/databases/browse?conn=2")
  await expect(page.getByRole("textbox", { name: "Filter tables" })).toHaveValue("")
})

test("the create table form shows the statement it will run", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/browse?conn=1")

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
  await page.goto("/databases/browse?conn=1")

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

/**
 * The connection page hands out the string an application needs, masked until
 * asked for, and only offers to forget a connection the sync would not simply
 * re-add on the next load.
 */
test("the connection string is masked, shown on request, and the public one names the server", async ({
  page,
}) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.route("**/api/v1/databases/sync", (route) =>
    json(route, { added: [], already: [], needsCredentials: [] }),
  )
  await page.setViewportSize({ width: 1696, height: 992 })
  await page.goto("/databases/connection?conn=1")

  const local = page.getByText("On this server", { exact: true }).locator("..")
  await expect(local).toContainText("postgres://app:••••••@127.0.0.1:5432/shop")
  await expect(local).not.toContainText("s3cret")
  await page.getByRole("button", { name: "Show the connection string" }).click()
  await expect(local).toContainText("postgres://app:s3cret@127.0.0.1:5432/shop?sslmode=disable")
  await page.getByRole("button", { name: "Hide the connection string" }).click()
  await expect(local).not.toContainText("s3cret")

  // Loopback only, so the second row explains rather than offers a string,
  // and Maintenance carries the switch that opens it up.
  await expect(
    page.getByText("Not reachable from outside this server", { exact: false }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Open up…" })).toBeVisible()
  await page.screenshot({
    path: "test-results/database-docs.png",
    fullPage: true,
    animations: "disabled",
  })
  // A database running here is re-added by the sync, so there is nothing to
  // forget: the Remove row is not drawn.
  await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0)
  await expect(page.getByText("Remove from the dashboard")).toHaveCount(0)
})

test("a server published to every interface hands out the public string and can be closed", async ({
  page,
}) => {
  await mockDatabases(page, {
    layout: null,
    puts: [],
    access: {
      ...localAccess,
      exposure: "public",
      firewall: { backend: "ufw", active: true, open: true, editable: true },
    },
  })
  await page.goto("/databases/connection?conn=1")

  const remote = page.getByText("From anywhere", { exact: true }).locator("..")
  await expect(remote).toContainText("postgres://app:••••••@203.0.113.9:5432/shop")
  await expect(page.getByText("The firewall lets it through", { exact: false })).toBeVisible()
  await expect(page.getByRole("button", { name: "Close", exact: true })).toBeVisible()
})

test("a connection to a server somewhere else keeps its Remove row and has no switch", async ({
  page,
}) => {
  await mockDatabases(page, {
    layout: null,
    puts: [],
    access: {
      detected: false,
      managed: false,
      exposure: "remote",
      port: 5432,
      publicAddresses: [],
      firewall: { active: false, open: false, editable: false },
    },
  })
  await page.goto("/databases/connection?conn=1")

  await expect(page.getByText("Remove from the dashboard")).toBeVisible()
  await expect(page.getByRole("button", { name: "Open up…" })).toHaveCount(0)
  await expect(page.getByText("From anywhere")).toHaveCount(0)
})

test("deleting a container database can take the container and its data with it", async ({
  page,
}) => {
  const deletes: unknown[] = []
  await mockDatabases(page, { layout: null, puts: [], deletes })
  await page.goto("/databases/connection?conn=1")

  // The sync notice about a server that needs credentials never expires by
  // design, and it sits over the maintenance rows at this height.
  await page.getByLabel("Close toast").click()
  await page.getByRole("button", { name: "Delete…" }).click()
  const dialog = page.getByRole("dialog", { name: "Delete database" })
  const also = dialog.getByRole("checkbox", {
    name: "Also remove the container shop-db and its data",
  })
  await expect(also).not.toBeChecked()
  await also.click()
  await dialog.getByPlaceholder("Type the phrase above").fill("shop")
  await dialog.getByRole("button", { name: "Delete for good" }).click()
  await expect.poll(() => deletes.length).toBe(1)
  expect(deletes[0]).toEqual({ removeContainer: true })
})

/**
 * The section opens on the control center rather than on one connection's
 * table rail: every database as a card drawn as its engine, with what needs
 * attention above them and the servers found on this machine offered.
 */
test("the section opens on every database at once, worst first", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases")

  await expect(page.getByRole("heading", { name: "Databases", level: 1 })).toHaveClass(/sr-only/)
  // No connection strip on a page about every connection.
  await expect(page.getByRole("button", { name: /Switch connection/ })).toHaveCount(0)
  await expect(page.getByText("1 of 2")).toBeVisible()
  // The unreachable one stands first.
  // The cards only: the map below also links each database by name.
  const cards = page
    .locator("[data-slot=choice-card]")
    .getByRole("link", { name: /^Open (shop|cache)$/ })
  await expect(cards).toHaveCount(2)
  await expect(cards.first()).toHaveAccessibleName("Open cache")
  await expect(cards.first()).toHaveAttribute("href", "/databases/browse?conn=2")
  await expect(page.getByText("cache connection refused")).toBeVisible()
  // A server found on the machine is offered, and its dialog can make the
  // account from the host's own shell.
  await page.getByRole("button", { name: "Connect postgres on this server" }).click()
  const dialog = page.getByRole("dialog", { name: "Connect PostgreSQL on this server" })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Make the account and connect" })).toBeVisible()
  await expect(dialog.getByLabel("Account to make")).toHaveValue("just_dashboard")
  await page.keyboard.press("Escape")
  // The map draws both columns.
  await expect(page.getByRole("list", { name: "What they feed" })).toContainText("api")
  await page.screenshot({ path: "test-results/database-overview-1280.png", fullPage: true })
})

test("the map draws every link and the topology page its readings", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/topology")
  await expect(page.getByRole("list", { name: "Databases" })).toContainText("shop")
  await expect(page.getByRole("list", { name: "What they feed" })).toContainText("worker")
  await expect(page.getByText("linked by its deployment · 2 open sessions")).toBeVisible()
  await expect(
    page.getByText("2 carrying sessions").or(page.getByText("1 carrying sessions")),
  ).toBeVisible()
})

/**
 * The type picker is a popover inside a dialog. The dialog's scroll lock
 * used to swallow the wheel over it, so the list could not be scrolled and
 * every type past the fold was reachable only by typing.
 */
test("the create table type list scrolls with the wheel", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/browse?conn=1")
  await page.getByRole("button", { name: "Create table" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Browse types" }).click()
  const list = page.locator("[data-slot=command-list]")
  await expect(list).toBeVisible()
  await list.evaluate((el) => {
    el.style.maxHeight = "40px"
  })
  await list.hover()
  await page.mouse.wheel(0, 200)
  await expect.poll(() => list.evaluate((el) => el.scrollTop)).toBeGreaterThan(0)
})

test("the server page lists accounts, databases and extensions", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/server?conn=1")
  await expect(page.getByText("2 databases on this server")).toBeVisible()
  await expect(page.getByRole("button", { name: "Open as a connection" })).toBeVisible()
  await page.getByRole("button", { name: /^Accounts/ }).click()
  await expect(page.getByRole("cell", { name: "postgres", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "New account" }).click()
  const dialog = page.getByRole("dialog", { name: "New account" })
  await dialog.getByLabel("Name").fill("reports")
  await expect(dialog.getByText(/CREATE ROLE "reports" WITH LOGIN PASSWORD/)).toBeVisible()
  await expect(dialog.getByText(/GRANT CONNECT ON DATABASE "shop" TO "reports"/)).toBeVisible()
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: /^Extensions/ }).click()
  await expect(page.getByRole("button", { name: "Enable" })).toBeVisible()
})

test("the advisor draws findings with their fix", async ({ page }) => {
  await mockDatabases(page, { layout: null, puts: [] })
  await page.goto("/databases/advisor?conn=1")
  await expect(page.getByText("1 foreign key with no index")).toBeVisible()
  await page.getByRole("button", { name: /1 foreign key with no index/ }).click()
  await expect(page.getByText(/CREATE INDEX "orders_customer_id_idx"/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Open in the console" })).toBeVisible()
})

for (const path of [
  "/databases",
  "/databases/browse?conn=1",
  "/databases/diagram?conn=1",
  "/databases/connection?conn=1",
  "/databases/topology",
  "/databases/server?conn=1",
  "/databases/advisor?conn=1",
  "/databases/backups?conn=1",
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
