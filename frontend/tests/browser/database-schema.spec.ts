import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { hold, mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * The schema browser (`/databases/<id>/schema`) and the diagram
 * (`/databases/<id>/diagram`) of a SQL engine.
 *
 * Most of what is checked is a defect the pages these replace shipped: a
 * table known by its bare name, so two schemas' `orders` were one; a
 * create-table form that left half-filled columns out without a word; forms
 * that drew their own SQL and lost what was typed to a stray Escape; a
 * diagram that forgot a layout without asking and could not say that it had
 * left tables out. The API is mocked in the browser; what the requests carry
 * is what is asserted.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

type Sent = { method: string; path: string; preview: boolean; body: Record<string, unknown> }

const ORDERS_COLUMNS = [
  {
    name: "id",
    type: "bigint",
    nullable: false,
    position: 1,
    default: "nextval('orders_id_seq'::regclass)",
  },
  { name: "customer_id", type: "bigint", nullable: false, position: 2 },
  { name: "status", type: "text", nullable: false, position: 3, default: "'pending'" },
  {
    name: "note",
    type: "text",
    nullable: true,
    position: 4,
    comment: "What the customer asked for",
  },
]

function detailOf(schema: string, name: string, over: Record<string, unknown> = {}) {
  return {
    schema,
    name,
    type: "table",
    owner: "app",
    columns: ORDERS_COLUMNS,
    primaryKey: ["id"],
    indexes: [
      {
        name: `${name}_pkey`,
        columns: ["id"],
        unique: true,
        primary: true,
        method: "btree",
        size: 16384,
        constraint: `${name}_pkey`,
      },
      {
        name: `${name}_status_idx`,
        columns: ["status"],
        unique: false,
        primary: false,
        method: "btree",
        size: 8192,
      },
    ],
    foreignKeys: [
      {
        name: `${name}_customer_id_fkey`,
        columns: ["customer_id"],
        refSchema: schema,
        refTable: "customers",
        refColumns: ["id"],
        onDelete: "CASCADE",
        onUpdate: "NO ACTION",
      },
    ],
    constraints: [
      {
        name: `${name}_status_check`,
        type: "check",
        columns: ["status"],
        definition: "CHECK (status <> '')",
      },
    ],
    referencedBy: [
      {
        name: "items_order_id_fkey",
        schema,
        table: "items",
        columns: ["order_id"],
        refColumns: ["id"],
      },
    ],
    facts: [],
    estimatedRows: 9000,
    size: 3_399_680,
    dataSize: 2_383_872,
    indexSize: 1_015_808,
    createSql: `CREATE TABLE "${schema}"."${name}" (\n  "id" bigint NOT NULL\n);`,
    createSqlSource: "generated",
    ...over,
  }
}

const SCHEMAS = [
  { name: "public", default: true, tables: 4, owner: "app" },
  { name: "sales", tables: 1 },
  { name: "pg_catalog", system: true, tables: 140 },
]

/** A file's one schema: SQLite lists `main`, and nothing else to choose from. */
const MAIN_CATALOG = {
  schema: "main",
  defaultSchema: "main",
  schemas: [{ name: "main", default: true, tables: 2 }],
  objects: {
    tables: [{ kind: "table", schema: "main", name: "orders", estimatedRows: 9 }],
    views: [{ kind: "view", schema: "main", name: "order_summary" }],
    triggers: [],
  },
  truncated: [],
  limit: 5000,
}

function catalogOf(schema: string) {
  const objects =
    schema === "public"
      ? {
          tables: [
            { kind: "table", schema, name: "orders", estimatedRows: 9000, size: 3_399_680 },
            { kind: "table", schema, name: "customers", estimatedRows: 1200, size: 504_000 },
            { kind: "table", schema, name: "items", estimatedRows: 18000, size: 1_900_000 },
          ],
          views: [{ kind: "view", schema, name: "order_summary" }],
          materializedViews: [],
          functions: [
            {
              kind: "function",
              schema,
              name: "order_count",
              signature: "cid bigint",
              returns: "bigint",
              language: "sql",
            },
            { kind: "function", schema, name: "armor", signature: "bytea", extension: "pgcrypto" },
            {
              kind: "function",
              schema,
              name: "armor",
              signature: "bytea, text[], text[]",
              extension: "pgcrypto",
            },
          ],
          procedures: [],
          triggers: [
            {
              kind: "trigger",
              schema,
              name: "orders_touch",
              table: "orders",
              detail: "BEFORE UPDATE, each row",
            },
          ],
          sequences: [{ kind: "sequence", schema, name: "orders_id_seq", table: "orders.id" }],
          types: [{ kind: "enum", schema, name: "mood", values: ["sad", "ok", "happy"] }],
        }
      : schema === "sales"
        ? {
            tables: [{ kind: "table", schema, name: "orders", estimatedRows: 12, size: 8192 }],
            views: [],
            materializedViews: [],
            functions: [],
            procedures: [],
            triggers: [],
            sequences: [],
            types: [],
          }
        : { tables: [], views: [], triggers: [] }
  return { schema, defaultSchema: "public", schemas: SCHEMAS, objects, truncated: [], limit: 5000 }
}

/** A statement the mock writes for a change, recognisable in the dialog. */
function statementOf(sent: Sent): string {
  const { body } = sent
  const table = `"${body.schema}"."${body.table}"`
  if (sent.path === "/ddl/table" && sent.method === "POST") {
    const columns = (body.columns as { name: string; type: string }[]).map(
      (c) => `"${c.name}" ${c.type}`,
    )
    return `CREATE TABLE ${table} (${columns.join(", ")})`
  }
  if (sent.path === "/ddl/column" && sent.method === "POST") {
    const column = body.column as { name: string; type: string }
    return `ALTER TABLE ${table} ADD COLUMN "${column.name}" ${column.type}`
  }
  if (sent.path === "/ddl/column" && sent.method === "PATCH") {
    return `ALTER TABLE ${table} ALTER COLUMN "${body.name}" ${body.type ? `TYPE ${body.type}` : "SET …"}`
  }
  if (sent.path === "/ddl/column" && sent.method === "DELETE") {
    return `ALTER TABLE ${table} DROP COLUMN "${body.name}"`
  }
  if (sent.path === "/ddl/table" && sent.method === "DELETE") return `DROP TABLE ${table}`
  return `-- ${sent.method} ${sent.path} ${JSON.stringify(body)}`
}

type SchemaMock = DatabaseMock & {
  /** The session's capabilities, when neither an admin's nor a viewer's. */
  capabilities?: string[]
  /** Fields laid over a table's detail, by `schema.table`. */
  details?: Record<string, Record<string, unknown>>
  /** A change that is run (not previewed) answers only once this settles. */
  runHeld?: Promise<unknown>
  /** What a run answers instead of its statement. */
  runRefused?: { status: number; message: string }
  /** `GET /catalog` fails until this is cleared by the spec. */
  catalogDown?: { down: boolean }
  /** `GET /table` fails while this is set. */
  tableDown?: { down: boolean }
  /** A preview of a destructive change answers only once this settles. */
  previewHeld?: Promise<unknown>
  /** A maintenance command answers only once this settles. */
  maintenanceHeld?: Promise<unknown>
  /** The engine gave no index sizes or counts, and said why in its own error's words. */
  indexesUnread?: boolean
  /** The graph `GET /graph` answers, by schema asked for ("" = every schema). */
  graphs?: Record<string, unknown>
  /** What `GET /diagram` holds as the saved layout. */
  layout?: { layout: Record<string, unknown> | null; updatedAt?: string }
}

/**
 * The schema area's routes on a connection, laid over the section's fixture.
 * Hands back what the page sent to the structure routes and the diagram's.
 */
async function mockSchema(page: Page, options: SchemaMock = {}, connection = 1) {
  const shell = await mockDatabases(page, options)
  const sent = {
    ddl: [] as Sent[],
    tables: [] as URL[],
    objects: [] as URL[],
    graphs: [] as URL[],
    layouts: [] as { method: string; schema: string; body?: Record<string, unknown> }[],
    maintenance: [] as Record<string, unknown>[],
    browse: [] as URL[],
  }
  let saved = options.layout ?? { layout: null }

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

  await page.route(`**/api/v1/databases/${connection}/**`, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const rest = url.pathname.replace(new RegExp(`^/api/v1/databases/${connection}`), "")
    const schema = url.searchParams.get("schema") ?? ""
    const method = request.method()

    if (rest === "/catalog") {
      if (options.catalogDown?.down) {
        return json(
          route,
          { error: { code: "connect_failed", message: "the server did not answer" } },
          502,
        )
      }
      if (connection === 7) return json(route, MAIN_CATALOG)
      return json(route, catalogOf(schema || "public"))
    }
    if (rest === "/table") {
      sent.tables.push(url)
      const table = url.searchParams.get("table") ?? ""
      if (options.tableDown?.down) {
        return json(
          route,
          { error: { code: "connect_failed", message: "the server did not answer" } },
          502,
        )
      }
      if (table === "gone") {
        return json(
          route,
          { error: { code: "not_found", message: "no table or view named gone" } },
          404,
        )
      }
      const view =
        table === "order_summary"
          ? {
              type: "view",
              primaryKey: [],
              indexes: [],
              foreignKeys: [],
              constraints: [],
              referencedBy: [],
              createSql: `CREATE OR REPLACE VIEW "public"."order_summary" AS\nSELECT id, status FROM orders;`,
              createSqlSource: "engine",
            }
          : {}
      const other =
        schema === "sales"
          ? {
              columns: [{ name: "ref", type: "uuid", nullable: false, position: 1 }],
              primaryKey: ["ref"],
              foreignKeys: [],
              estimatedRows: 12,
            }
          : {}
      const held = schema || (connection === 7 ? "main" : "public")
      return json(
        route,
        detailOf(held, table, {
          ...view,
          ...other,
          ...options.details?.[`${schema}.${table}`],
        }),
      )
    }
    if (rest === "/object") {
      sent.objects.push(url)
      const kind = url.searchParams.get("kind")
      const name = url.searchParams.get("name") ?? ""
      if (kind === "enum") {
        return json(route, {
          kind,
          schema,
          name,
          definition: `CREATE TYPE "${schema}"."${name}" AS ENUM ('sad', 'ok', 'happy');`,
          source: "generated",
          owner: "app",
          values: ["sad", "ok", "happy"],
          details: [{ name: "Labels", value: "3" }],
        })
      }
      return json(route, {
        kind,
        schema,
        name,
        signature: url.searchParams.get("signature") ?? undefined,
        definition: `CREATE OR REPLACE FUNCTION ${schema}.${name}(cid bigint)\n RETURNS bigint\n LANGUAGE sql\nAS $function$SELECT 1$function$`,
        source: "engine",
        owner: "app",
        language: "sql",
        returns: "bigint",
        details: [
          { name: "Arguments", value: "cid bigint" },
          { name: "Returns", value: "bigint" },
        ],
      })
    }
    if (rest.startsWith("/ddl/")) {
      const body = (request.postDataJSON() ?? {}) as Record<string, unknown>
      const preview = url.searchParams.get("preview") === "1"
      const entry = { method, path: rest, preview, body }
      sent.ddl.push(entry)
      // Two limits of an engine the flags do not state: asked of the server.
      if (connection === 7 && rest === "/ddl/view" && body.replace) {
        return json(
          route,
          {
            error: {
              code: "bad_request",
              message: "SQLite cannot replace a view: drop the view and create it again",
            },
          },
          400,
        )
      }
      if (rest === "/ddl/column" && method === "PATCH" && body.using === "NULL" && preview) {
        // The page's own question: this engine takes a conversion expression.
        return json(route, { preview: true, statement: "probe", statements: ["probe"] })
      }
      if (
        rest === "/ddl/column" &&
        method === "POST" &&
        (body.column as { type: string }).type === "nonsense"
      ) {
        return json(
          route,
          {
            error: {
              code: "bad_request",
              message:
                'column type "nonsense" is not one this form can build; use the Query tab for it',
            },
          },
          400,
        )
      }
      const statement = statementOf(entry)
      if (preview) {
        if (method === "DELETE") await options.previewHeld
        return json(route, { preview: true, statement, statements: [statement] })
      }
      await options.runHeld
      if (options.runRefused) {
        return json(
          route,
          { error: { code: "bad_request", message: options.runRefused.message } },
          options.runRefused.status,
        )
      }
      return json(route, { statement, statements: [statement] })
    }
    if (rest === "/tablestats") {
      return json(route, {
        supported: true,
        schema,
        truncated: false,
        tables: [
          {
            schema: schema || "public",
            table: "orders",
            kind: "table",
            rows: 9000,
            deadRows: 12,
            totalBytes: 3_399_680,
            tableBytes: 2_342_912,
            indexBytes: 1_015_808,
            toastBytes: 8192,
            bloatBytes: 1_294_336,
            seqScans: 4,
            seqRowsRead: 18000,
            indexScans: 18001,
            indexRowsRead: 18101,
            inserts: 9000,
            updates: 9000,
            deletes: 0,
            modsSinceAnalyze: 0,
            lastAutovacuum: "2026-10-01T08:00:00Z",
            lastAnalyze: "2026-10-01T08:00:00Z",
          },
        ],
      })
    }
    if (rest === "/indexstats" && options.indexesUnread) {
      return json(route, {
        supported: true,
        schema,
        truncated: false,
        notes: [
          "Index sizes need read access to mysql.innodb_index_stats.",
          "Use counts are unavailable: this account may not read performance_schema (Error 1142 (42000): SELECT command denied to user 'app'@'10.0.0.1'), so no index can be called unused.",
        ],
        indexes: [
          {
            schema,
            table: "orders",
            name: "orders_status_idx",
            columns: ["status"],
            unique: false,
            primary: false,
            valid: true,
            bytes: 0,
            scans: -1,
            rowsRead: -1,
            unused: false,
          },
        ],
      })
    }
    if (rest === "/indexstats") {
      return json(route, {
        supported: true,
        schema,
        truncated: false,
        indexes: [
          {
            schema,
            table: "orders",
            name: "orders_status_idx",
            method: "btree",
            columns: ["status"],
            unique: false,
            primary: false,
            valid: true,
            bytes: 8192,
            scans: 0,
            rowsRead: 0,
            unused: true,
          },
          {
            schema,
            table: "orders",
            name: "orders_pkey",
            method: "btree",
            columns: ["id"],
            unique: true,
            primary: true,
            valid: true,
            constraint: true,
            bytes: 16384,
            scans: 18001,
            rowsRead: 18101,
            unused: false,
          },
        ],
      })
    }
    if (rest === "/maintenance") {
      if (method === "GET") {
        return json(route, {
          supported: true,
          actions: [
            {
              id: "analyze",
              label: "Analyze",
              description: "Refreshes the statistics the planner chooses plans from.",
              scope: "either",
              requires: "service.control",
            },
            {
              id: "vacuum_full",
              label: "Vacuum full",
              description: "Rewrites the table into a new file. Takes an exclusive lock.",
              scope: "either",
              blocking: true,
              requires: "destructive",
            },
            {
              id: "reindex",
              label: "Reindex",
              description: "Builds every index of the table again. Blocks writes while it does.",
              scope: "either",
              blocking: true,
              requires: "destructive",
              options: ["concurrently"],
            },
            {
              id: "checkpoint",
              label: "Checkpoint",
              description: "The whole database.",
              scope: "database",
              requires: "service.control",
            },
          ],
        })
      }
      const body = request.postDataJSON() as Record<string, unknown>
      sent.maintenance.push(body)
      await options.maintenanceHeld
      return json(route, {
        action: body.action,
        statements: [`ANALYZE VERBOSE "public"."orders"`],
        output: ['INFO:  analyzing "public.orders"'],
        outputTruncated: false,
        duration: "132ms",
        ok: true,
      })
    }
    if (rest === "/browse") {
      sent.browse.push(url)
      const table = url.searchParams.get("table") ?? ""
      return json(route, {
        columns: ["id"],
        types: ["INT8"],
        rows: [],
        rowCount: 0,
        statement: `SELECT * FROM "${schema}"."${table}" ORDER BY "id" ASC LIMIT $1 OFFSET $2`,
        primaryKey: ["id"],
      })
    }
    if (rest === "/graph") {
      sent.graphs.push(url)
      const graph = options.graphs?.[schema]
      return json(
        route,
        graph ?? { schema, tables: [], edges: [], truncated: false, total: 0, limit: 120 },
      )
    }
    if (rest === "/diagram") {
      if (method === "GET") {
        sent.layouts.push({ method, schema })
        return json(route, saved)
      }
      if (method === "PUT") {
        const body = request.postDataJSON() as { layout: Record<string, unknown> }
        sent.layouts.push({ method, schema, body: body.layout })
        saved = { layout: body.layout, updatedAt: "2026-10-01T10:00:00Z" }
        return json(route, saved)
      }
      sent.layouts.push({ method, schema })
      saved = { layout: null }
      return json(route, saved)
    }
    return route.fallback()
  })

  return { ...shell, sent }
}

const runs = (sent: { ddl: Sent[] }) => sent.ddl.filter((entry) => !entry.preview)
const previews = (sent: { ddl: Sent[] }, path: string, method?: string) =>
  sent.ddl.filter(
    (entry) => entry.preview && entry.path === path && (!method || entry.method === method),
  )

const TABLE = "/databases/1/schema?schema=public&table=orders"

/* ------------------------------------------------------------ the tree */

test("the tree lists a schema by kind, with what an extension installed folded away", async ({
  page,
}) => {
  await mockSchema(page)
  await page.goto("/databases/1/schema?schema=public")
  const rail = page.getByRole("navigation", { name: "Objects of this schema" })

  for (const kind of [
    "Tables",
    "Views",
    "Materialized views",
    "Functions",
    "Triggers",
    "Sequences",
    "Types",
  ]) {
    await expect(rail.getByRole("region", { name: kind, exact: true })).toBeVisible()
  }
  // A branch says how many it holds, and one that holds none says so.
  await expect(rail.getByRole("button", { name: /^Tables/ })).toContainText("3")
  await expect(rail.getByRole("region", { name: "Materialized views" })).toContainText("None")

  // Forty functions nobody wrote are one row until asked for.
  const functions = rail.getByRole("region", { name: "Functions" })
  await expect(functions.getByRole("link")).toHaveCount(1)
  await functions.getByRole("button", { name: /Show 2 from pgcrypto/ }).click()
  await expect(functions.getByRole("link")).toHaveCount(3)

  // A search is a search of every kind, extensions included.
  await rail.getByLabel("Find an object").fill("armor")
  await expect(rail.getByRole("link")).toHaveCount(2)
  await expect(rail.getByRole("button", { name: /^Functions/ })).toContainText("2 of 3")
  await rail.getByLabel("Find an object").fill("zzz")
  await rail.getByRole("button", { name: "Clear the search" }).click()
  await expect(
    rail.getByRole("region", { name: "Tables" }).getByRole("link", { name: /^orders/ }),
  ).toBeVisible()

  // A folded branch stays folded for the next visit.
  await rail.getByRole("button", { name: /^Sequences/ }).click()
  await expect(rail.getByRole("link", { name: "orders_id_seq" })).toHaveCount(0)
  await page.reload()
  await expect(rail.getByRole("button", { name: /^Sequences/ })).toHaveAttribute(
    "aria-expanded",
    "false",
  )
})

test("with nothing chosen the page shows the schema and its largest tables without metric cards", async ({
  page,
}) => {
  await mockSchema(page)
  await page.goto("/databases/1/schema?schema=public")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  // The large ones are a press from open.
  const largest = page.getByRole("region", { name: "Largest in public" })
  await expect(largest.getByRole("button")).toHaveCount(3)
  await largest.getByRole("button", { name: "Open orders" }).click()
  await expect(page).toHaveURL(/schema=public&table=orders$/)
  await page.goBack()
  // An enum's labels are read without opening it.
  await expect(page.getByRole("region", { name: "Enum types" })).toContainText("sadokhappy")
})

test("on a phone the tree lies over the schema, and has a way off it with nothing chosen", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockSchema(page)
  await page.goto("/databases/1/schema?schema=sales")
  const rail = page.getByRole("navigation", { name: "Objects of this schema" })
  await expect(rail).toBeVisible()
  // What the tree covers is not there to the keyboard.
  await expect(page.locator("[data-slot=schema-browser] > [inert]")).toHaveCount(1)
  // The schema's own verbs are in the tree's head: it is all a phone shows.
  await rail.getByRole("button", { name: "More for sales" }).click()
  await expect(page.getByRole("menuitem", { name: "Drop schema…" })).toBeVisible()
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: "Show sales" }).click()
  await expect(rail).toBeHidden()
  await expect(page.getByRole("region", { name: "Largest in sales" })).toBeVisible()
  await page.getByRole("button", { name: "Show the objects" }).click()
  await expect(rail).toBeVisible()

  // With a table open the same foot leads back to it, and the head keeps its schema.
  await rail.getByRole("link", { name: /^orders/ }).click()
  await expect(page.getByRole("heading", { name: "sales.orders" })).toContainText("sales")
})

test("a table is its schema and its name: two schemas' orders are two tables", async ({ page }) => {
  const { sent } = await mockSchema(page)
  await page.goto("/databases/1/schema?schema=public")
  const rail = page.getByRole("navigation", { name: "Objects of this schema" })
  await rail
    .getByRole("region", { name: "Tables" })
    .getByRole("link", { name: /^orders/ })
    .click()
  await expect(page).toHaveURL(/schema=public&table=orders/)
  await expect(page.getByRole("heading", { name: "public.orders" })).toBeVisible()
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()

  // The other schema's table of the same name is read as itself.
  await rail.getByRole("button", { name: "schema: public" }).click()
  await page.getByRole("menuitem", { name: /^sales/ }).click()
  await expect(page).toHaveURL(/schema=sales$/)
  await page
    .getByRole("navigation", { name: "Objects of this schema" })
    .getByRole("link", { name: /^orders/ })
    .click()
  await expect(page.getByRole("heading", { name: "sales.orders" })).toBeVisible()
  await expect(page.getByRole("cell", { name: "ref", exact: true })).toBeVisible()
  expect(sent.tables.at(-1)?.searchParams.get("schema")).toBe("sales")

  // A change to it names its schema, not the one the reader came from.
  await page.getByRole("button", { name: "Add column" }).click()
  const dialog = page.getByRole("dialog", { name: "Add column" })
  await dialog.getByLabel("Name", { exact: true }).fill("region")
  await dialog.getByLabel("Type", { exact: true }).fill("text")
  await expect(
    dialog.getByText('ALTER TABLE "sales"."orders" ADD COLUMN "region" text'),
  ).toBeVisible()

  // Back is a way out of a table, and of a schema.
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await page.goBack()
  await expect(page).toHaveURL(/schema=sales$/)
  await page.goBack()
  await expect(page.getByRole("heading", { name: "public.orders" })).toBeVisible()
})

test("a table opened from the tree opens on its columns, whatever reading it was left on", async ({
  page,
}) => {
  await mockSchema(page)
  await page.goto(TABLE)
  await page.getByRole("tab", { name: /Indexes/ }).click()
  await expect(page).toHaveURL(/view=indexes/)

  const tables = page
    .getByRole("navigation", { name: "Objects of this schema" })
    .getByRole("region", { name: "Tables" })
  await tables.getByRole("link", { name: /^customers/ }).click()
  await expect(page.getByRole("heading", { name: "public.customers" })).toBeVisible()
  await expect(page.getByRole("tab", { name: /Columns/ })).toHaveAttribute("aria-selected", "true")

  // Back to the first table by its link: the address says its columns, and
  // the reading chosen there earlier is not written over it again.
  await tables.getByRole("link", { name: /^orders/ }).click()
  await expect(page.getByRole("heading", { name: "public.orders" })).toBeVisible()
  await expect(page).not.toHaveURL(/view=/)
  await expect(page.getByRole("tab", { name: /Columns/ })).toHaveAttribute("aria-selected", "true")
})

test("a schema, a table or an object that is not there says so, with the way back", async ({
  page,
}) => {
  await mockSchema(page)
  await page.goto("/databases/1/schema?schema=nope")
  await expect(page.getByText("No schema called nope")).toBeVisible()
  await page.getByRole("link", { name: "Open public" }).click()
  await expect(page).toHaveURL(/schema=public/)

  await page.goto("/databases/1/schema?schema=public&table=gone")
  await expect(page.getByText("No table or view called gone")).toBeVisible()
  await expect(page.getByRole("link", { name: "Back to public" })).toBeVisible()
})

test("a catalogue that cannot be read offers Retry, and a later failure keeps what is shown", async ({
  page,
}) => {
  const down = { down: true }
  await mockSchema(page, { catalogDown: down })
  await page.goto("/databases/1/schema?schema=public")
  await expect(page.getByText("the server did not answer")).toBeVisible()
  down.down = false
  await page.getByRole("button", { name: "Try again" }).click()
  const rail = page.getByRole("navigation", { name: "Objects of this schema" })
  await expect(
    rail.getByRole("region", { name: "Tables" }).getByRole("link", { name: /^orders/ }),
  ).toBeVisible()

  // The next read fails: the tree that was read stays — and says it is the
  // last reading, not the one just asked for.
  down.down = true
  await rail.getByRole("button", { name: "Read the schema again" }).click()
  await expect(rail.getByText("Not read again")).toBeVisible()
  await expect(page.getByText("The schema could not be read again")).toBeVisible()
  await expect(
    rail.getByRole("region", { name: "Tables" }).getByRole("link", { name: /^orders/ }),
  ).toBeVisible()
  down.down = false
  await rail.getByRole("button", { name: "Try again" }).click()
  await expect(rail.getByText("Not read again")).toBeHidden()
})

test("a table that cannot be read again says so over what it read before", async ({ page }) => {
  const down = { down: false }
  await mockSchema(page, { tableDown: down })
  await page.goto(TABLE)
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()
  down.down = true
  // The read that follows a change is the same read: a failure there used to
  // leave the old structure on screen without a word.
  await page.getByRole("button", { name: "Read the schema again" }).click()
  await expect(page.getByText("What is shown is the last reading of orders.")).toBeVisible()
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()
  down.down = false
  await page
    .getByRole("status")
    .filter({ hasText: "last reading of orders" })
    .getByRole("button", { name: "Try again" })
    .click()
  await expect(page.getByText("What is shown is the last reading of orders.")).toBeHidden()
})

test("reading the schema again reads the open table again with it", async ({ page }) => {
  const { sent } = await mockSchema(page)
  await page.goto(TABLE)
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()
  const before = sent.tables.length
  await page.getByRole("button", { name: "Read the schema again" }).click()
  await expect.poll(() => sent.tables.length).toBe(before + 1)
  // What was on screen stays there while it is read.
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()
})

/* ------------------------------------------------------------ new table */

test("?new=table opens the panel; a half-filled column stops it and the statement is the server's", async ({
  page,
}) => {
  const { sent } = await mockSchema(page)
  // The table editor's "New table" is this address.
  await page.goto("/databases/1/schema?schema=public&new=table")
  const panel = page.getByRole("dialog", { name: "New table" })
  await expect(panel).toBeVisible()

  // The schema is a field, not whichever one the reader last stood in.
  await expect(panel.getByRole("combobox", { name: "Schema" })).toContainText("public")
  await panel.getByRole("combobox", { name: "Schema" }).click()
  await page.getByRole("option", { name: "sales" }).click()

  await panel.getByLabel("Name", { exact: true }).fill("invoices")
  await panel.getByRole("button", { name: "id, numbered" }).click()
  await expect(panel.getByLabel("Type of id", { exact: true })).toHaveValue("bigserial")
  await expect(panel.getByText(/id is numbered by the engine/)).toBeVisible()
  await panel.getByRole("button", { name: "Add column" }).click()
  await panel.getByLabel("Name of column 2").fill("total")

  // A name with no type is typed work: it is not left out, it stops the form.
  await panel.getByRole("button", { name: "Create table" }).click()
  await expect(panel.getByText("total has no type.").first()).toBeVisible()
  expect(runs(sent)).toEqual([])

  await panel.getByLabel("Type of total", { exact: true }).fill("bigint")
  await expect(
    panel.getByText('CREATE TABLE "sales"."invoices" ("id" bigserial, "total" bigint)'),
  ).toBeVisible()
  await panel.getByRole("button", { name: "Create table" }).click()
  await expect(page).toHaveURL(/schema=sales&table=invoices$/)
  // The entry that asked for the form no longer asks: Back is the schema.
  await page.goBack()
  await expect(page).toHaveURL(/schema\?schema=public$/)
  await expect(panel).toBeHidden()
  await page.goForward()
  await expect(page).toHaveURL(/schema=sales&table=invoices$/)

  // What was shown is what ran: the same body, once with preview and once without.
  const [ran] = runs(sent)
  expect(ran.path).toBe("/ddl/table")
  expect(ran.body).toEqual({
    schema: "sales",
    table: "invoices",
    columns: [
      { name: "id", type: "bigserial", primaryKey: true },
      { name: "total", type: "bigint" },
    ],
  })
  expect(previews(sent, "/ddl/table").at(-1)?.body).toEqual(ran.body)
})

test("closing the new-table panel keeps the draft; Start over clears it", async ({ page }) => {
  await mockSchema(page)
  await page.goto("/databases/1/schema?schema=public&new=table")
  const panel = page.getByRole("dialog", { name: "New table" })
  await panel.getByLabel("Name", { exact: true }).fill("invoices")
  await panel.getByLabel("Name of column 1").fill("number")
  await expect(panel.getByText("Closing keeps this draft for the tab.")).toBeVisible()

  await page.keyboard.press("Escape")
  await expect(panel).toBeHidden()
  await expect(page).not.toHaveURL(/new=table/)

  await page.getByRole("button", { name: "New table" }).click()
  await expect(panel.getByLabel("Name", { exact: true })).toHaveValue("invoices")
  await expect(panel.getByLabel("Name of column 1")).toHaveValue("number")
  await panel.getByRole("button", { name: "Start over" }).click()
  await expect(panel.getByLabel("Name", { exact: true })).toHaveValue("")
})

test("the table editor's New table opens the panel, and opens it again after it was closed", async ({
  page,
}) => {
  await mockSchema(page)
  await page.goto("/databases/1/data?schema=public")
  const tables = page.getByRole("navigation", { name: "tables of this schema" })
  await tables.getByRole("link", { name: "New table" }).click()
  const panel = page.getByRole("dialog", { name: "New table" })
  await expect(panel).toBeVisible()
  await expect(page).toHaveURL(/\/schema\?schema=public&new=table/)
  await panel.getByRole("button", { name: "Cancel" }).click()
  await expect(panel).toBeHidden()

  // The same link, a second time: a closed panel is not a panel that stays closed.
  await page.goBack()
  await tables.getByRole("link", { name: "New table" }).click()
  await expect(panel).toBeVisible()
  await expect(page).toHaveURL(/new=table/)
})

/* ---------------------------------------------------------------- forms */

test("a form shows the server's statement, and runs exactly what it showed", async ({ page }) => {
  const { sent } = await mockSchema(page)
  await page.goto(TABLE)
  await page.getByRole("button", { name: "Add column" }).click()
  const dialog = page.getByRole("dialog", { name: "Add column" })
  const command = dialog.getByRole("button", { name: "Add column" })

  // Nothing to ask about yet: no statement, no command.
  await expect(dialog.getByText("Name the column and give it a type.")).toBeVisible()
  await expect(command).toBeDisabled()

  await dialog.getByLabel("Name", { exact: true }).fill("priority")
  await dialog.getByLabel("Type", { exact: true }).fill("nonsense")
  // The server's refusal, in its own words, where the statement would be.
  await expect(dialog.getByText(/is not one this form can build/)).toBeVisible()
  await expect(command).toBeDisabled()

  await dialog.getByLabel("Type", { exact: true }).fill("integer")
  await expect(
    dialog.getByText('ALTER TABLE "public"."orders" ADD COLUMN "priority" integer'),
  ).toBeVisible()
  await command.click()
  await expect(dialog).toBeHidden()

  const [ran] = runs(sent)
  expect(ran.body).toEqual({
    schema: "public",
    table: "orders",
    column: { name: "priority", type: "integer" },
  })
  expect(previews(sent, "/ddl/column", "POST").at(-1)?.body).toEqual(ran.body)
  // The table is read again once it has changed.
  expect(sent.tables.length).toBeGreaterThan(1)
})

test("Escape does not lose typed work, and a running statement cannot be dismissed", async ({
  page,
}) => {
  const run = hold()
  const { sent } = await mockSchema(page, { runHeld: run.until })
  await page.goto(TABLE)
  await page.getByRole("button", { name: "Add column" }).click()
  const dialog = page.getByRole("dialog", { name: "Add column" })

  // Nothing typed: Escape simply closes.
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()

  await page.getByRole("button", { name: "Add column" }).click()
  await dialog.getByLabel("Name", { exact: true }).fill("priority")
  await dialog.getByLabel("Type", { exact: true }).fill("integer")
  await page.keyboard.press("Escape")
  await expect(dialog.getByText("Close and lose what you typed?")).toBeVisible()
  // The question has the keyboard, and Escape again means "keep editing".
  await expect(dialog.getByRole("button", { name: "Keep editing" })).toBeFocused()
  await page.keyboard.press("Escape")
  await expect(dialog.getByText("Close and lose what you typed?")).toBeHidden()
  await expect(dialog.getByLabel("Type", { exact: true })).toBeFocused()
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue("priority")

  // While the statement runs, neither Escape nor the corner closes the dialog.
  await dialog.getByRole("button", { name: "Add column" }).click()
  await expect.poll(() => runs(sent).length).toBe(1)
  await page.keyboard.press("Escape")
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText("Close and lose what you typed?")).toBeHidden()
  run.release()
  await expect(dialog).toBeHidden()
  // The keyboard goes back to the control the form was opened from.
  await expect(page.getByRole("button", { name: "Add column" })).toBeFocused()
})

test("a form opened from a row's menu hands the keyboard back to that menu's button", async ({
  page,
}) => {
  await mockSchema(page)
  await page.goto(TABLE)
  const menu = page.getByRole("button", { name: "Actions for note", exact: true })
  await menu.click()
  await page.getByRole("menuitem", { name: "Rename" }).click()
  await expect(page.getByRole("dialog", { name: "Rename column" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(menu).toBeFocused()

  // A confirmation that is cancelled does the same.
  await menu.click()
  await page.getByRole("menuitem", { name: "Drop…" }).click()
  await page
    .getByRole("dialog", { name: "Drop column" })
    .getByRole("button", { name: "Cancel" })
    .click()
  await expect(menu).toBeFocused()
})

test("a statement the engine refuses leaves the form as it was, with the engine's words", async ({
  page,
}) => {
  await mockSchema(page, {
    runRefused: { status: 400, message: 'column "priority" of relation "orders" already exists' },
  })
  await page.goto(TABLE)
  await page.getByRole("button", { name: "Add column" }).click()
  const dialog = page.getByRole("dialog", { name: "Add column" })
  await dialog.getByLabel("Name", { exact: true }).fill("priority")
  await dialog.getByLabel("Type", { exact: true }).fill("integer")
  await dialog.getByRole("button", { name: "Add column" }).click()
  await expect(dialog.getByText(/Nothing was changed\..*already exists/)).toBeVisible()
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue("priority")
})

test("a type change is asked about in the form's own footer; a default alone is not", async ({
  page,
}) => {
  const { sent } = await mockSchema(page)
  await page.goto(TABLE)
  await page.getByRole("button", { name: "Edit status" }).click()
  const dialog = page.getByRole("dialog", { name: "Edit column" })
  await expect(dialog.getByText("public.orders.status")).toBeVisible()
  await expect(
    dialog.getByText("Change the type, whether it takes NULL, or the default."),
  ).toBeVisible()

  // Only what changed is sent: the default is left as the engine has it.
  await dialog.getByLabel("Type", { exact: true }).fill("varchar(40)")
  await expect(dialog.getByText('ALTER COLUMN "status" TYPE varchar(40)')).toBeVisible()
  // This engine answered the page's question about a conversion: the field is offered.
  await expect(dialog.getByLabel("Convert the stored values with")).toBeVisible()
  // What the change does to the rows is said as soon as the form states it.
  await expect(dialog.getByText("This rewrites status in every row")).toBeVisible()
  await dialog.getByRole("button", { name: "Change column…" }).click()

  // One dialog: the question is asked over the subject and the statement it is about.
  await expect(page.getByRole("dialog")).toHaveCount(1)
  await expect(dialog.getByText("Rewrite status in every row of orders?")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Keep editing" })).toBeFocused()
  expect(runs(sent)).toEqual([])
  // Escape answers "no" and leaves the form as it was.
  await page.keyboard.press("Escape")
  await expect(dialog.getByText("Rewrite status in every row of orders?")).toBeHidden()
  await expect(dialog.getByLabel("Type", { exact: true })).toHaveValue("varchar(40)")

  await dialog.getByRole("button", { name: "Change column…" }).click()
  await dialog.getByRole("button", { name: "Change column", exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)[0].body).toEqual({
    schema: "public",
    table: "orders",
    name: "status",
    type: "varchar(40)",
  })

  // A default is a change the engine cannot refuse half-way: no second question.
  await page.getByRole("button", { name: "Edit status" }).click()
  await dialog.getByLabel("Default", { exact: true }).fill("")
  await dialog.getByRole("button", { name: "Change column", exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)[1].body).toEqual({
    schema: "public",
    table: "orders",
    name: "status",
    dropDefault: true,
  })
})

test("a confirmed change the engine refuses says so in the form, not only in a toast", async ({
  page,
}) => {
  await mockSchema(page, {
    runRefused: {
      status: 400,
      message: 'ERROR: column "status" cannot be cast automatically to type integer',
    },
  })
  await page.goto(TABLE)
  await page.getByRole("button", { name: "Edit status" }).click()
  const dialog = page.getByRole("dialog", { name: "Edit column" })
  await dialog.getByLabel("Type", { exact: true }).fill("integer")
  await dialog.getByRole("button", { name: "Change column…" }).click()
  await dialog.getByRole("button", { name: "Change column", exact: true }).click()
  await expect(
    dialog.getByText(/Nothing was changed\..*cannot be cast automatically/),
  ).toBeVisible()
  // The form is as it was, the question is withdrawn, and nothing is stacked over it.
  await expect(page.getByRole("dialog")).toHaveCount(1)
  await expect(dialog.getByLabel("Type", { exact: true })).toHaveValue("integer")
  await expect(dialog.getByRole("button", { name: "Change column…" })).toBeVisible()
})

test("a computed column is not offered for editing, and a key into another schema says which", async ({
  page,
}) => {
  await mockSchema(page, {
    details: {
      "public.orders": {
        columns: [
          ...ORDERS_COLUMNS,
          { name: "total", type: "numeric", nullable: true, position: 5, generated: "qty * price" },
        ],
        foreignKeys: [
          {
            name: "orders_customer_id_fkey",
            columns: ["customer_id"],
            refSchema: "sales",
            refTable: "customers",
            refColumns: ["id"],
          },
        ],
      },
    },
  })
  await page.goto(TABLE)
  await expect(page.getByRole("cell", { name: "total", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit status" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit total" })).toHaveCount(0)
  // It can still be renamed and dropped.
  await page.getByRole("button", { name: "Actions for total", exact: true }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Rename", "Add comment", "Drop…"])
  await page.keyboard.press("Escape")

  const key = page.getByRole("link", { name: "sales.customers.id" })
  await expect(key).toHaveAttribute("href", /schema=sales&table=customers/)
})

test("a drop is previewed once, confirmed by name, and sent to the destructive route", async ({
  page,
}) => {
  const read = hold()
  const { sent } = await mockSchema(page, { previewHeld: read.until })
  await page.goto(TABLE)
  // Each row's menu is named for its column.
  await page.getByRole("button", { name: "Actions for note", exact: true }).click()
  await page.getByRole("menuitem", { name: "Drop…" }).click()
  // The statement is slow to come: the wait is said, and a second press is not a second ask.
  await expect(page.getByText("Drop column: reading the statement…")).toBeVisible()
  await page.getByRole("button", { name: "Actions for note", exact: true }).click()
  await page.getByRole("menuitem", { name: "Drop…" }).click()
  read.release()
  const confirm = page.getByRole("dialog", { name: "Drop column" })
  await expect(confirm.getByText("public.orders.note")).toBeVisible()
  await expect(confirm.getByText('ALTER TABLE "public"."orders" DROP COLUMN "note"')).toBeVisible()
  // A preview of a drop spends the same budget as a drop: asked once.
  expect(previews(sent, "/ddl/column", "DELETE")).toHaveLength(1)
  expect(runs(sent)).toEqual([])
  await confirm.getByRole("button", { name: "Drop column" }).click()
  await expect(confirm).toBeHidden()
  expect(runs(sent)[0]).toMatchObject({
    method: "DELETE",
    path: "/ddl/column",
    body: { schema: "public", table: "orders", name: "note" },
  })
  expect(previews(sent, "/ddl/column", "DELETE")).toHaveLength(1)
})

test("keys and constraints: what points where, what is dropped with what", async ({ page }) => {
  const { sent } = await mockSchema(page)
  await page.goto(`${TABLE}&view=keys`)
  const panel = page.getByRole("tabpanel")
  await expect(panel.getByRole("row", { name: /orders_pkey.*primary key/i })).toBeVisible()
  await expect(
    panel.getByRole("row", { name: /orders_customer_id_fkey.*on delete cascade/i }),
  ).toBeVisible()
  await expect(panel.getByRole("row", { name: /orders_status_check/ })).toBeVisible()
  // What points at this table is a way to that table's own keys.
  await expect(panel.getByRole("link", { name: /^items/ })).toHaveAttribute(
    "href",
    /table=items&view=keys/,
  )

  await panel.getByRole("button", { name: "Drop orders_customer_id_fkey" }).click()
  const confirm = page.getByRole("dialog", { name: "Drop foreign key" })
  await confirm.getByRole("button", { name: "Drop foreign key" }).click()
  await expect(confirm).toBeHidden()
  expect(runs(sent)[0]).toMatchObject({
    method: "DELETE",
    path: "/ddl/foreign-key",
    body: { schema: "public", table: "orders", name: "orders_customer_id_fkey" },
  })

  // An index that enforces the key is not dropped as an index.
  await page.getByRole("tab", { name: /Indexes/ }).click()
  await expect(page.getByRole("button", { name: "Drop orders_status_idx" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Drop orders_pkey" })).toHaveCount(0)
  await expect(page.getByText(/enforces a key or a constraint is dropped with it/)).toBeVisible()

  // The reading that is open is in the address, and survives a reload.
  await expect(page).toHaveURL(/view=indexes/)
  await page.reload()
  await expect(page.getByRole("tab", { name: /Indexes/ })).toHaveAttribute("aria-selected", "true")
})

test("on a phone a table's indexes and keys are read down, with every verb in reach", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockSchema(page)
  await page.goto(`${TABLE}&view=indexes`)
  // Across, the table ran off the pane and took its drop control with it.
  await expect(page.getByRole("list", { name: "Indexes" }).getByRole("listitem")).toHaveCount(2)
  await expect(page.getByRole("button", { name: "Drop orders_status_idx" })).toBeInViewport({
    ratio: 1,
  })
  await page.getByRole("tab", { name: /Keys/ }).click()
  await expect(
    page.getByRole("list", { name: "Keys and constraints" }).getByRole("listitem"),
  ).toHaveCount(3)
  await expect(page.getByRole("button", { name: "Drop orders_customer_id_fkey" })).toBeInViewport({
    ratio: 1,
  })
  // The head names the table whole at this width too.
  await expect(page.getByRole("heading", { name: "public.orders" })).toContainText("public")
})

test("a foreign key is built against the other table's own columns", async ({ page }) => {
  const { sent } = await mockSchema(page)
  await page.goto(`${TABLE}&view=keys`)
  await page.getByRole("button", { name: "Add foreign key" }).click()
  const dialog = page.getByRole("dialog", { name: "Add foreign key" })
  await dialog.getByRole("combobox", { name: "Referenced table", exact: true }).click()
  await page.getByRole("option", { name: "customers" }).click()
  // The usual key points at the other table's primary key: it is proposed.
  await expect(dialog.getByRole("combobox", { name: "Column 1 of customers" })).toContainText("id")
  await dialog.getByRole("combobox", { name: "Column 1 of orders" }).click()
  await page.getByRole("option", { name: "customer_id" }).click()
  await dialog.getByRole("button", { name: "Add foreign key" }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)[0].body).toEqual({
    schema: "public",
    table: "orders",
    columns: ["customer_id"],
    refTable: "customers",
    refColumns: ["id"],
  })
})

test("a foreign key's actions and an index's methods are the engine's own choices", async ({
  page,
}) => {
  // PostgreSQL: every action, its methods, and room for an extension's.
  await mockSchema(page)
  await page.goto(`${TABLE}&view=keys`)
  await page.getByRole("button", { name: "Add foreign key" }).click()
  const key = page.getByRole("dialog", { name: "Add foreign key" })
  await key.getByRole("combobox", { name: "When the row it points at is deleted" }).click()
  await expect(page.getByRole("option")).toHaveText([
    "no action",
    "restrict",
    "cascade",
    "set null",
    "set default",
  ])
  await page.keyboard.press("Escape")
  await key.getByRole("button", { name: "Cancel" }).click()

  await page.getByRole("tab", { name: /Indexes/ }).click()
  await page.getByRole("button", { name: "Add index" }).click()
  const index = page.getByRole("dialog", { name: "Add index" })
  await index.getByRole("combobox", { name: "Method" }).click()
  await expect(page.getByRole("option")).toHaveText([
    "The engine’s default",
    "btree",
    "hash",
    "gin",
    "gist",
    "spgist",
    "brin",
    "Another, by name…",
  ])
  await page.getByRole("option", { name: "gin", exact: true }).click()
  await index.getByRole("button", { name: "status", exact: true }).click()
  await expect(index.getByText(/"method":"gin"/)).toBeVisible()
})

test("an engine's limits on those choices are left out and said, not found by refusal", async ({
  page,
}) => {
  // MariaDB behind the mysql driver: no SET DEFAULT, a closed list of methods.
  await mockSchema(page, {}, 2)
  await page.goto("/databases/2/schema?schema=public&table=orders&view=keys")
  await page.getByRole("button", { name: "Add foreign key" }).click()
  const key = page.getByRole("dialog", { name: "Add foreign key" })
  await key.getByRole("combobox", { name: "When its key is changed" }).click()
  await expect(page.getByRole("option")).toHaveText([
    "no action",
    "restrict",
    "cascade",
    "set null",
  ])
  await page.keyboard.press("Escape")
  await expect(key.getByText(/does not set a default through a foreign key/)).toBeVisible()
  await key.getByRole("button", { name: "Cancel" }).click()

  await page.getByRole("tab", { name: /Indexes/ }).click()
  await page.getByRole("button", { name: "Add index" }).click()
  const index = page.getByRole("dialog", { name: "Add index" })
  await index.getByRole("combobox", { name: "Method" }).click()
  await expect(page.getByRole("option")).toHaveText([
    "The engine’s default",
    "btree",
    "hash",
    "fulltext",
    "spatial",
  ])
})

/* ----------------------------------------------------- roles and limits */

test("a viewer reads everything and is offered nothing that writes", async ({ page }) => {
  await mockSchema(page, { viewer: true })
  await page.goto(TABLE)
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Add column" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Edit status" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "New", exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "Actions for orders", exact: true }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Copy name"])
  await page.keyboard.press("Escape")
  // An address that asks for the form does not conjure it.
  await page.goto("/databases/1/schema?schema=public&new=table")
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

test("a role that may change but not destroy gets no drop, and no type change", async ({
  page,
}) => {
  await mockSchema(page, { capabilities: ["read", "service.control"] })
  await page.goto(TABLE)
  await page.getByRole("button", { name: "Actions for note", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: "Drop…" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Edit status" }).click()
  const dialog = page.getByRole("dialog", { name: "Edit column" })
  await expect(dialog.getByLabel("Type", { exact: true })).toHaveCount(0)
  await expect(dialog.getByText(/The type stays text/)).toBeVisible()
  await expect(dialog.getByLabel("Default", { exact: true })).toBeVisible()
})

test("a protected connection draws no control that changes the schema", async ({ page }) => {
  await mockSchema(page, { rows: { 1: { readOnly: true } } })
  await page.goto(TABLE)
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Add column" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "New", exact: true })).toHaveCount(0)
  await page.getByRole("tab", { name: /Statistics/ }).click()
  await expect(page.getByText("3.2 MB on disk")).toBeVisible()
  await expect(page.getByRole("region", { name: "Maintenance" })).toHaveCount(0)
})

test("SQLite's limits are stated where the controls would be, not found by an error", async ({
  page,
}) => {
  const { sent } = await mockSchema(page, {}, 7)
  await page.goto("/databases/7/schema?schema=main&table=orders")
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()
  // It can add, rename and drop a column — and cannot alter one, or comment on one.
  await expect(page.getByRole("button", { name: "Add column" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit status" })).toHaveCount(0)
  await expect(page.getByText(/SQLite cannot change a column in place/)).toBeVisible()
  await expect(page.getByText("SQLite keeps no comments on columns.")).toBeVisible()

  await page.getByRole("tab", { name: /Keys/ }).click()
  await expect(page.getByRole("button", { name: "Add foreign key" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Add constraint" })).toHaveCount(0)
  await expect(
    page.getByText(/Foreign keys cannot be added to or dropped from a SQLite table here/),
  ).toBeVisible()
  await expect(page.getByText(/add a unique index/)).toBeVisible()

  // A view cannot be replaced there: the server said so when asked, before anybody pressed.
  await page.goto("/databases/7/schema?schema=main&table=order_summary&view=definition")
  await expect(
    page.getByText("SQLite cannot replace a view: drop the view and create it again"),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Replace the query" })).toBeDisabled()
  expect(runs(sent)).toEqual([])
})

test("Query hands over a SELECT in the engine's dialect, without reading the table to learn it", async ({
  page,
}) => {
  const { sent } = await mockSchema(page)
  await page.goto(TABLE)
  await expect(page.getByRole("cell", { name: "customer_id", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Query", exact: true }).click()
  await expect.poll(() => new URL(page.url()).pathname).toBe("/databases/1/query")
  await expect(page.locator("[data-slot=sql-editor] .view-lines")).toHaveText(
    'SELECT * FROM "public"."orders" LIMIT 100',
  )
  await expect(page.getByText("Nothing has been run in this tab", { exact: true })).toBeVisible()
  expect(sent.browse).toEqual([])
})

/* ------------------------------------------------- views, objects, types */

test("a view's query is replaced from its own definition", async ({ page }) => {
  const { sent } = await mockSchema(page)
  await page.goto("/databases/1/schema?schema=public&table=order_summary&view=definition")
  // A view has columns and a definition; it has no keys to read.
  await expect(page.getByRole("tab", { name: /Keys/ })).toHaveCount(0)
  await page.getByRole("button", { name: "Replace the query" }).click()
  const dialog = page.getByRole("dialog", { name: "Replace view" })
  await expect(dialog.getByLabel("Query", { exact: true })).toHaveValue(
    "SELECT id, status FROM orders",
  )
  await dialog.getByLabel("Query", { exact: true }).fill("SELECT id FROM orders;")
  await dialog.getByRole("button", { name: "Replace view" }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)[0]).toMatchObject({
    path: "/ddl/view",
    body: {
      schema: "public",
      name: "order_summary",
      query: "SELECT id FROM orders",
      replace: true,
    },
  })
})

test("a function is read as its definition and handed to Query; an overload is told apart", async ({
  page,
}) => {
  const { sent } = await mockSchema(page)
  await page.goto("/databases/1/schema?schema=public")
  const rail = page.getByRole("navigation", { name: "Objects of this schema" })
  await rail
    .getByRole("region", { name: "Functions" })
    .getByRole("button", { name: /Show 2 from pgcrypto/ })
    .click()
  await rail.getByRole("link", { name: /armor.*text\[\]/ }).click()
  await expect(page.getByRole("heading", { name: /public\.armor/ })).toBeVisible()
  expect(sent.objects.at(-1)?.searchParams.get("signature")).toBe("bytea, text[], text[]")
  await expect(page.locator("[data-slot=code-view]")).toBeVisible()
  await expect(page.getByText("Arguments", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Open in Query" }).click()
  await expect.poll(() => new URL(page.url()).pathname).toBe("/databases/1/query")
  await expect(page.locator("[data-slot=sql-editor] .view-lines")).toContainText(
    "CREATE OR REPLACE FUNCTION public.armor",
  )
  await expect(page.getByText("Nothing has been run in this tab", { exact: true })).toBeVisible()
})

test("an enum's labels are read in order, and one is added beside another", async ({ page }) => {
  const { sent } = await mockSchema(page)
  await page.goto("/databases/1/schema?schema=public&object=enum&name=mood")
  const labels = page.getByRole("list").filter({ hasText: "happy" }).getByRole("listitem")
  await expect(labels).toHaveText(["1sad", "2ok", "3happy"])
  await page.getByRole("button", { name: "Add label" }).click()
  const dialog = page.getByRole("dialog", { name: "Add label" })
  await dialog.getByLabel("Label", { exact: true }).fill("ok")
  await expect(dialog.getByText("The type already has that label.").first()).toBeVisible()
  await dialog.getByLabel("Label", { exact: true }).fill("fine")
  await dialog.getByRole("radio", { name: "Before" }).click()
  await dialog.getByRole("button", { name: "Add label" }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)[0]).toMatchObject({
    path: "/ddl/enum/value",
    body: { schema: "public", name: "mood", value: "fine", before: "happy" },
  })
})

/* ------------------------------------------------------------ statistics */

test("statistics: what the engine measured, and the maintenance a role may run", async ({
  page,
}) => {
  const { sent } = await mockSchema(page)
  await page.goto(`${TABLE}&view=statistics`)
  await expect(page.getByText("3.2 MB on disk")).toBeVisible()
  await expect(page.getByText("Read 18,005 times")).toBeVisible()
  await expect(page.getByRole("row", { name: /orders_status_idx.*never used/i })).toBeVisible()

  const maintenance = page.getByRole("region", { name: "Maintenance" })
  // An action for the whole database is not a table's.
  await expect(maintenance.getByRole("button", { name: "Checkpoint" })).toHaveCount(0)
  await maintenance.getByRole("button", { name: "Analyze" }).click()
  await expect(maintenance.getByText(/Analyze finished/)).toBeVisible()
  await expect(maintenance.getByText('INFO: analyzing "public.orders"')).toBeVisible()
  expect(sent.maintenance[0]).toEqual({ action: "analyze", schema: "public", table: "orders" })

  // One that locks the table is asked about by name first.
  await maintenance.getByRole("button", { name: "Vacuum full…" }).click()
  const confirm = page.getByRole("dialog", { name: "Vacuum full" })
  await expect(confirm.getByText("public.orders")).toBeVisible()
  await expect(confirm.getByText(/Takes an exclusive lock/)).toBeVisible()
  expect(sent.maintenance).toHaveLength(1)
  await confirm.getByRole("button", { name: "Cancel" }).click()

  // An action that can be asked for two ways offers both, and sends the one chosen.
  await maintenance.getByRole("button", { name: "Reindex" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Reindex…", "Reindex concurrently…"])
  await page.getByRole("menuitem", { name: "Reindex concurrently…" }).click()
  const ask = page.getByRole("dialog", { name: "Reindex concurrently" })
  await expect(ask.getByText(/beside the old one/)).toBeVisible()
  await ask.getByRole("button", { name: "Reindex concurrently" }).click()
  await expect(maintenance.getByText(/Reindex concurrently finished/)).toBeVisible()
  expect(sent.maintenance[1]).toEqual({
    action: "reindex",
    schema: "public",
    table: "orders",
    options: { concurrently: true },
  })
})

test("a maintenance command goes on while the reader looks at another reading", async ({
  page,
}) => {
  const command = hold()
  const { sent } = await mockSchema(page, { maintenanceHeld: command.until })
  const dropped: string[] = []
  page.on("requestfailed", (request) => {
    if (request.url().includes("/maintenance")) dropped.push(request.url())
  })
  await page.goto(`${TABLE}&view=statistics`)
  await page
    .getByRole("region", { name: "Maintenance" })
    .getByRole("button", { name: "Analyze" })
    .click()
  const running = page.getByRole("status").filter({ hasText: "Analyze is running on orders" })
  await expect(running).toBeVisible()

  // Another reading of the table, and another table: the request is not dropped.
  await page.getByRole("tab", { name: /Columns/ }).click()
  await expect(running).toBeVisible()
  const tables = page
    .getByRole("navigation", { name: "Objects of this schema" })
    .getByRole("region", { name: "Tables" })
  await tables.getByRole("link", { name: /^customers/ }).click()
  await expect(page.getByRole("heading", { name: "public.customers" })).toBeVisible()
  await expect(running).toBeHidden()
  await page.goBack()
  await expect(running).toBeVisible()
  expect(dropped).toEqual([])
  expect(sent.maintenance).toHaveLength(1)

  // It lands while nobody is looking at Statistics: a toast says how it went,
  // and the outcome is there on the way back.
  command.release()
  await expect(page.getByText("Analyze on orders finished")).toBeVisible()
  await expect(running).toBeHidden()
  await page.getByRole("tab", { name: "Statistics" }).click()
  await expect(
    page.getByRole("region", { name: "Maintenance" }).getByText(/Analyze finished/),
  ).toBeVisible()
})

test("only Stop ends a maintenance command", async ({ page }) => {
  const command = hold()
  await mockSchema(page, { maintenanceHeld: command.until })
  const dropped: string[] = []
  page.on("requestfailed", (request) => {
    if (request.url().includes("/maintenance")) dropped.push(request.url())
  })
  await page.goto(`${TABLE}&view=statistics`)
  await page
    .getByRole("region", { name: "Maintenance" })
    .getByRole("button", { name: "Analyze" })
    .click()
  const running = page.getByRole("status").filter({ hasText: "Analyze is running on orders" })
  await running.getByRole("button", { name: "Stop" }).click()
  await expect(page.getByText("Analyze on orders was stopped")).toBeVisible()
  await expect(running).toBeHidden()
  expect(dropped).toHaveLength(1)
  command.release()
})

test("a figure the engine could not read is not drawn as zero, nor its error printed", async ({
  page,
}) => {
  await mockSchema(page, { indexesUnread: true })
  await page.goto(`${TABLE}&view=statistics`)
  const use = page.getByRole("region", { name: "Index use" })
  await expect(use.getByRole("row", { name: /orders_status_idx/ })).toBeVisible()
  await expect(use).not.toContainText("0 B")
  await expect(use.getByRole("columnheader", { name: "Size" })).toHaveCount(0)
  await expect(use.getByRole("columnheader", { name: "Scans" })).toHaveCount(0)
  const panel = page.getByRole("tabpanel")
  await expect(
    panel.getByText(/this account may not read performance_schema, so no index/),
  ).toBeVisible()
  await expect(panel).not.toContainText("Error 1142")
})

/* -------------------------------------------------------------- diagram */

const column = (name: string, over: Record<string, unknown> = {}) => ({
  name,
  type: "bigint",
  nullable: false,
  primaryKey: false,
  ...over,
})

const node = (schema: string, name: string, columns = [column("id", { primaryKey: true })]) => ({
  id: `${schema}.${name}`,
  schema,
  name,
  type: "table",
  rows: 10,
  columns,
})

const EVERY = {
  schema: "",
  tables: [
    node("public", "orders", [
      column("id", { primaryKey: true }),
      column("customer_id", { foreignKey: "customers", foreignKeyId: "public.customers" }),
    ]),
    node("public", "customers"),
    node("sales", "orders"),
  ],
  edges: [
    {
      name: "orders_customer_id_fkey",
      from: "public.orders",
      to: "public.customers",
      fromSchema: "public",
      fromTable: "orders",
      fromColumn: "customer_id",
      toSchema: "public",
      toTable: "customers",
      toColumn: "id",
      onDelete: "CASCADE",
      cardinality: "many-to-one",
    },
  ],
  truncated: false,
  total: 3,
  limit: 120,
}
const PUBLIC = { ...EVERY, schema: "public", tables: EVERY.tables.slice(0, 2), total: 2 }
const GRAPHS = { "": EVERY, public: PUBLIC }

const tableNode = (page: Page, id: string) => page.locator(`.react-flow__node[data-id="${id}"]`)

test("two schemas' tables of one name are two tables on the diagram", async ({ page }) => {
  const { sent } = await mockSchema(page, { graphs: GRAPHS })
  await page.goto("/databases/1/diagram")
  await expect(tableNode(page, "public.orders")).toBeVisible()
  await expect(tableNode(page, "sales.orders")).toBeVisible()
  await expect(page.locator(".react-flow__node")).toHaveCount(3)
  // With more than one schema in the picture, a name is said with its schema.
  await expect(tableNode(page, "sales.orders")).toContainText("sales.orders")

  // Each schema's tables carry its mark, and the legend says whose it is.
  await expect(page.getByText("primary key").locator("..").locator("..")).toContainText("sales")

  // Hiding one of them hides that one, and the layout that is saved says which.
  const inspector = page.getByRole("complementary", { name: "Diagram inspector" })
  await page.getByRole("button", { name: "Show the inspector (I)" }).click()
  await inspector.getByRole("checkbox", { name: "Show sales.orders on the diagram" }).click()
  await expect(tableNode(page, "sales.orders")).toHaveCount(0)
  await expect(tableNode(page, "public.orders")).toBeVisible()
  await expect
    .poll(() => sent.layouts.filter((l) => l.method === "PUT").at(-1)?.body?.hidden)
    .toEqual(["sales.orders"])
  expect(sent.layouts.filter((l) => l.method === "PUT").at(-1)?.body?.version).toBe(2)
  await expect(page.getByRole("button", { name: "1 hidden" })).toBeVisible()
})

test("the diagram says which schema it draws, and another is a press away", async ({ page }) => {
  const { sent } = await mockSchema(page, { graphs: GRAPHS })
  await page.goto("/databases/1/diagram?schema=public")
  await expect(page.locator(".react-flow__node")).toHaveCount(2)
  // One schema: the bare name is enough.
  await expect(tableNode(page, "public.orders")).not.toContainText("public.")
  expect(sent.graphs.at(-1)?.searchParams.get("schema")).toBe("public")

  await page.getByRole("button", { name: "schema: public" }).click()
  await page.getByRole("menuitem", { name: "Every schema" }).click()
  await expect(page.locator(".react-flow__node")).toHaveCount(3)
  // "Every schema" is said in the address, so it can be pasted and gone back from.
  await expect(page).toHaveURL(/every=1/)
  await expect(page.getByRole("button", { name: "schema: Every schema" })).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL(/diagram\?schema=public$/)
  await expect(page.locator(".react-flow__node")).toHaveCount(2)
  await page.goForward()
  await expect(page.locator(".react-flow__node")).toHaveCount(3)
})

test("one schema can be chosen again after every schema was", async ({ page }) => {
  await mockSchema(page, { graphs: GRAPHS })
  await page.goto("/databases/1/diagram?schema=public")
  await expect(page.locator(".react-flow__node")).toHaveCount(2)
  await page.getByRole("button", { name: "schema: public" }).click()
  await page.getByRole("menuitem", { name: "Every schema" }).click()
  await expect(page.locator(".react-flow__node")).toHaveCount(3)

  await page.getByRole("button", { name: "schema: Every schema" }).click()
  await page.getByRole("menuitem", { name: /^public/ }).click()
  await expect(page).toHaveURL(/diagram\?schema=public$/)
  await expect(page.locator(".react-flow__node")).toHaveCount(2)
  await expect(page.getByRole("button", { name: "schema: public" })).toBeVisible()
})

test("a picture that left tables out says how many, and draws more when asked", async ({
  page,
}) => {
  const { sent } = await mockSchema(page, {
    graphs: { public: { ...PUBLIC, truncated: true, total: 340, limit: 120 } },
  })
  await page.goto("/databases/1/diagram?schema=public")
  await expect(
    page.getByRole("status").filter({ hasText: "2 tables of 340 are drawn" }),
  ).toBeVisible()
  await page.getByRole("button", { name: "Draw 340" }).click()
  await expect(page).toHaveURL(/limit=340/)
  await expect.poll(() => sent.graphs.at(-1)?.searchParams.get("limit")).toBe("340")
})

test("a layout saved under bare names is kept, re-keyed by schema and name", async ({ page }) => {
  const { sent } = await mockSchema(page, {
    graphs: GRAPHS,
    layout: {
      layout: {
        version: 1,
        detail: "keys",
        hidden: ["customers"],
        notes: { orders: "the money" },
        positions: { orders: { x: 40, y: 40 } },
      },
      updatedAt: "2026-09-30T10:00:00Z",
    },
  })
  await page.goto("/databases/1/diagram?schema=public")
  // The note and the hidden table are the ones the old layout named.
  await expect(tableNode(page, "public.orders")).toContainText("the money")
  await expect(tableNode(page, "public.customers")).toHaveCount(0)
  await expect
    .poll(() => sent.layouts.filter((l) => l.method === "PUT").at(-1)?.body)
    .toMatchObject({
      version: 2,
      detail: "keys",
      hidden: ["public.customers"],
      notes: { "public.orders": "the money" },
      positions: { "public.orders": { x: 40, y: 40 } },
    })
})

test("forgetting a layout asks first", async ({ page }) => {
  const { sent } = await mockSchema(page, {
    graphs: GRAPHS,
    layout: {
      layout: { version: 2, hidden: ["public.customers"] },
      updatedAt: "2026-09-30T10:00:00Z",
    },
  })
  await page.goto("/databases/1/diagram?schema=public")
  await expect(tableNode(page, "public.customers")).toHaveCount(0)
  await page.getByRole("button", { name: "Diagram options" }).click()
  await page.getByRole("menuitem", { name: "Forget this layout…" }).click()
  const confirm = page.getByRole("dialog", { name: "Forget this layout" })
  await expect(confirm.getByText("Hidden", { exact: true })).toBeVisible()
  expect(sent.layouts.some((l) => l.method === "DELETE")).toBe(false)
  await confirm.getByRole("button", { name: "Cancel" }).click()
  await expect(tableNode(page, "public.customers")).toHaveCount(0)

  await page.getByRole("button", { name: "Diagram options" }).click()
  await page.getByRole("menuitem", { name: "Forget this layout…" }).click()
  await confirm.getByRole("button", { name: "Forget layout" }).click()
  await expect(tableNode(page, "public.customers")).toBeVisible()
  expect(sent.layouts.some((l) => l.method === "DELETE")).toBe(true)
})

test("a table's menu leads to its data, its schema and a SELECT in the engine's dialect", async ({
  page,
}) => {
  const { sent } = await mockSchema(page, { graphs: GRAPHS })
  await page.goto("/databases/1/diagram")
  const button = page.getByRole("button", { name: "Actions for sales.orders" })
  await button.click()
  await expect(page.getByRole("menuitem", { name: "Open data" })).toBeVisible()
  // Closing the menu hands the keyboard back to the button that opened it.
  await page.keyboard.press("Escape")
  await expect(button).toBeFocused()

  await button.click()
  await page.getByRole("menuitem", { name: "Open a SELECT in Query" }).click()
  await expect.poll(() => new URL(page.url()).pathname).toBe("/databases/1/query")
  // Both parts of the name quoted the engine's way — and written at once:
  // the table is not read to learn how it is read.
  await expect(page.locator("[data-slot=sql-editor] .view-lines")).toHaveText(
    'SELECT * FROM "sales"."orders" LIMIT 100',
  )
  await expect(page.getByText("Nothing has been run in this tab", { exact: true })).toBeVisible()
  expect(sent.browse).toEqual([])

  await page.goBack()
  await page.getByRole("button", { name: "Actions for sales.orders" }).click()
  await page.getByRole("menuitem", { name: "Open in Schema" }).click()
  await expect(page).toHaveURL(/\/databases\/1\/schema\?schema=sales&table=orders/)
})

test("the inspector opens on what is chosen, and gives the canvas back when nothing is", async ({
  page,
}) => {
  await mockSchema(page, {
    graphs: {
      ...GRAPHS,
      // A key that leaves the picture: public.orders points into sales.
      public: {
        ...PUBLIC,
        edges: [
          ...PUBLIC.edges,
          {
            name: "orders_region_fkey",
            from: "public.orders",
            to: "sales.regions",
            fromSchema: "public",
            fromTable: "orders",
            fromColumn: "id",
            toSchema: "sales",
            toTable: "regions",
            toColumn: "id",
            cardinality: "many-to-one",
          },
        ],
      },
    },
  })
  await page.goto("/databases/1/diagram?schema=public")
  const inspector = page.getByRole("complementary", { name: "Diagram inspector" })
  await expect(tableNode(page, "public.orders")).toBeVisible()
  // Nobody asked for it yet: the canvas has the whole pane.
  await expect(inspector).toBeHidden()

  await tableNode(page, "public.orders")
    .getByRole("button", { name: "orders", exact: true })
    .click()
  await expect(inspector).toBeVisible()
  await expect(inspector.getByText("Referenced by")).toBeVisible()
  // A key into a table that is not drawn says which schema it goes to, and leads there.
  const outside = inspector.getByRole("link", { name: /sales\.regions\.id/ })
  await expect(outside).toContainText("not drawn")
  await expect(outside).toHaveAttribute("href", /schema\?schema=sales&table=regions/)

  // Closed over the table chosen, it stays closed for that table.
  await inspector.getByRole("button", { name: "Close the inspector" }).click()
  await expect(inspector).toBeHidden()
  await tableNode(page, "public.customers")
    .getByRole("button", { name: "customers", exact: true })
    .click()
  await expect(inspector).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(inspector).toBeHidden()
})

test("the diagram's single keys are heard in the diagram only", async ({ page }) => {
  await mockSchema(page, { graphs: GRAPHS })
  await page.goto("/databases/1/diagram?schema=public")
  const inspector = page.getByRole("complementary", { name: "Diagram inspector" })
  await expect(tableNode(page, "public.orders")).toBeVisible()
  await expect(inspector).toBeHidden()

  // Focus elsewhere on the page: "i" is a letter, not a command.
  await page.getByRole("button", { name: /^Database: shop/ }).focus()
  await page.keyboard.press("i")
  await expect(inspector).toBeHidden()

  // Focus on a table of the diagram: it opens the inspector, and closes it again.
  await page.getByRole("button", { name: "Actions for public.orders" }).focus()
  await page.keyboard.press("i")
  await expect(inspector).toBeVisible()
  await page.keyboard.press("i")
  await expect(inspector).toBeHidden()
})

test("on a phone the inspector takes none of the canvas until it is asked for", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockSchema(page, { graphs: GRAPHS })
  await page.goto("/databases/1/diagram?schema=public")
  const inspector = page.getByRole("complementary", { name: "Diagram inspector" })
  await expect(tableNode(page, "public.orders")).toBeVisible()
  await expect(inspector).toBeHidden()
  await page.getByRole("button", { name: "Show the inspector (I)" }).click()
  await expect(inspector).toBeVisible()
})

test("a note is kept with the table it is on, and typed work is not lost to Escape", async ({
  page,
}) => {
  const { sent } = await mockSchema(page, { graphs: GRAPHS })
  await page.goto("/databases/1/diagram")
  await page.getByRole("button", { name: "Actions for sales.orders" }).click()
  await page.getByRole("menuitem", { name: "Add a note…" }).click()
  const dialog = page.getByRole("dialog", { name: "Add a note" })
  // Two tables are called orders here: the note says which it is on.
  await expect(dialog.getByText("Note on sales.orders")).toBeVisible()
  await dialog.getByRole("textbox").fill("Wholesale only")
  await page.keyboard.press("Escape")
  await expect(dialog.getByText("Close and lose what you typed?")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Keep editing" })).toBeFocused()
  await page.keyboard.press("Escape")
  await expect(dialog.getByRole("textbox")).toBeFocused()
  await dialog.getByRole("button", { name: "Save note" }).click()
  // The keyboard goes back to the table the note is on.
  await expect(page.getByRole("button", { name: "Actions for sales.orders" })).toBeFocused()
  await expect(tableNode(page, "sales.orders")).toContainText("Wholesale only")
  await expect(tableNode(page, "public.orders")).not.toContainText("Wholesale only")
  await expect
    .poll(() => sent.layouts.filter((l) => l.method === "PUT").at(-1)?.body?.notes)
    .toEqual({
      "sales.orders": "Wholesale only",
    })
})

test("an empty schema says so on the diagram, and the picker is still there", async ({ page }) => {
  await mockSchema(page, { graphs: GRAPHS })
  await page.goto("/databases/1/diagram?schema=sales")
  await expect(page.getByText("No tables in sales")).toBeVisible()
  await expect(page.getByRole("link", { name: "New table" })).toHaveAttribute(
    "href",
    /schema\?schema=sales&new=table/,
  )
  await page.getByRole("link", { name: "Show every schema" }).click()
  await expect(page.locator(".react-flow__node")).toHaveCount(3)
})

/* ------------------------------------------------- the design system */

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

/** Fully rounded, filled labels. */
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
      if (filled && (el.textContent ?? "").trim().length > 0) bad.push(el.outerHTML.slice(0, 140))
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
      if (Math.abs(offset) >= 1.5)
        bad.push(`${offset.toFixed(1)}px ${item.outerHTML.slice(0, 140)}`)
    }
    return bad
  })
}

async function keepsTheRules(page: Page, what: string) {
  await page.waitForLoadState("networkidle")
  expect(await unnamedControls(page), `unlabelled icon-only controls: ${what}`).toEqual([])
  expect(await offCentreText(page), `text off its row's centre line: ${what}`).toEqual([])
  expect(await filledPills(page), `a filled pill on the page: ${what}`).toEqual([])
  const registers = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("[data-slot='page']")].map(
      (el) => el.dataset.register,
    ),
  )
  expect(registers, `the register of the page: ${what}`).toEqual(["reading"])
  const sideways = await page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways, `the page scrolls sideways: ${what}`).toBe(false)
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the schema browser keeps the design system's rules${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockSchema(page, { rows: { 1: { environment: "production" } } })

    await page.goto("/databases/1/schema?schema=public")
    await expect(page.getByRole("navigation", { name: "Objects of this schema" })).toBeVisible()
    await keepsTheRules(page, "the schema, nothing chosen")

    for (const view of ["columns", "indexes", "keys", "triggers", "definition", "statistics"]) {
      await page.goto(`${TABLE}&view=${view}`)
      await expect(page.getByRole("tab", { selected: true })).toBeVisible()
      await keepsTheRules(page, `a table's ${view}`)
    }

    await page.getByRole("tab", { name: /Columns/ }).click()
    await page.getByRole("button", { name: "Add column" }).click()
    await page.getByRole("dialog").getByLabel("Name", { exact: true }).fill("priority")
    await keepsTheRules(page, "the add-column form")
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()

    await page.goto("/databases/1/schema?schema=public&object=enum&name=mood")
    await expect(page.locator("[data-slot=code-view]")).toBeVisible()
    await keepsTheRules(page, "an enum type")

    await page.goto("/databases/1/schema?schema=public&new=table")
    await expect(page.getByRole("dialog", { name: "New table" })).toBeVisible()
    await keepsTheRules(page, "the new-table panel")
  })

  test(`the diagram keeps the design system's rules${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockSchema(page, {
      graphs: { ...GRAPHS, public: { ...PUBLIC, truncated: true, total: 340 } },
      rows: { 1: { environment: "production" } },
    })
    await page.goto("/databases/1/diagram")
    await expect(tableNode(page, "sales.orders")).toBeVisible()
    await keepsTheRules(page, "the diagram of every schema")

    await page.getByRole("button", { name: "Actions for public.orders" }).click()
    await expect(page.getByRole("menuitem", { name: "Open data" })).toBeVisible()
    await keepsTheRules(page, "a table's menu")
    await page.keyboard.press("Escape")

    await page.goto("/databases/1/diagram?schema=public")
    await expect(page.getByRole("status").filter({ hasText: "are drawn" })).toBeVisible()
    await keepsTheRules(page, "a picture that left tables out")

    await page.goto("/databases/1/diagram?schema=sales")
    await expect(page.getByText("No tables in sales")).toBeVisible()
    await keepsTheRules(page, "an empty schema's diagram")
  })
}
