import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A PostgreSQL table's row-level security on the schema page, and the function
 * and column grants on an account's panel.
 *
 * What is checked is what the pages promise their reader: every change shows
 * the server's statement before it runs and sends exactly what the form says;
 * turning the filter off asks first; the test of a role sends the role, the
 * settings and the statement and says what came back; a role that may only
 * read, and a protected connection, are shown the policies and nothing that
 * changes them. The API is mocked in the browser, and what the requests carry
 * is what is asserted.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })

const refused = (route: Route, message: string, status = 400) =>
  json(route, { error: { code: "bad_request", message } }, status)

type Policy = {
  name: string
  command: "ALL" | "SELECT" | "INSERT" | "UPDATE" | "DELETE"
  permissive: boolean
  roles: string[]
  using?: string
  withCheck?: string
}

type Security = {
  schema: string
  table: string
  enabled: boolean
  forced: boolean
  owner: string
  exempt: string[]
  policies: Policy[]
}

const TENANT: Policy = {
  name: "notes_tenant_isolation",
  command: "ALL",
  permissive: true,
  roles: [],
  using: "((tenant_id)::text = current_setting('app.tenant_id'::text, true))",
  withCheck: "((tenant_id)::text = current_setting('app.tenant_id'::text, true))",
}

const AUDITORS: Policy = {
  name: "notes_auditor_read_only",
  command: "SELECT",
  permissive: true,
  roles: ["auditor"],
  using: "true",
}

const security = (over: Partial<Security> = {}): Security => ({
  schema: "public",
  table: "notes",
  enabled: true,
  forced: false,
  owner: "app",
  exempt: ["app", "etl", "postgres"],
  policies: [TENANT, AUDITORS],
  ...over,
})

const role = (name: string, over: Record<string, unknown> = {}) => ({
  name,
  login: true,
  superuser: false,
  createDb: false,
  createRole: false,
  connectionLimit: -1,
  connections: 0,
  ...over,
})

const ROLES = {
  supported: true,
  roles: [
    role("app", { superuser: true }),
    role("auditor", { login: false }),
    role("web"),
    role("etl"),
    role("reporting"),
    role("readers", { login: false }),
    role("pg_monitor", { login: false, system: true }),
  ],
}

const NOTES_COLUMNS = [
  { name: "id", type: "bigint", nullable: false, position: 1 },
  { name: "tenant_id", type: "uuid", nullable: false, position: 2 },
  { name: "owner", type: "text", nullable: false, position: 3 },
  { name: "body", type: "text", nullable: true, position: 4 },
]

const FUNCTIONS = [
  {
    kind: "function",
    schema: "public",
    name: "order_count",
    signature: "cid bigint",
    returns: "bigint",
  },
  {
    kind: "function",
    schema: "public",
    name: "order_count",
    signature: "cid text",
    returns: "bigint",
  },
  { kind: "function", schema: "public", name: "armor", signature: "bytea", extension: "pgcrypto" },
]

const CATALOG = {
  schema: "public",
  defaultSchema: "public",
  schemas: [
    { name: "public", default: true, tables: 2 },
    { name: "pg_catalog", system: true, tables: 140 },
  ],
  objects: {
    tables: [
      { kind: "table", schema: "public", name: "notes", estimatedRows: 420 },
      { kind: "table", schema: "public", name: "orders", estimatedRows: 9000 },
    ],
    views: [],
    materializedViews: [],
    functions: FUNCTIONS,
    procedures: [],
    triggers: [],
    sequences: [],
    types: [],
  },
  truncated: [] as string[],
  limit: 5000,
}

function detailOf(table: string) {
  return {
    schema: "public",
    name: table,
    type: "table",
    owner: "app",
    columns: NOTES_COLUMNS,
    primaryKey: ["id"],
    indexes: [],
    foreignKeys: [],
    constraints: [],
    referencedBy: [],
    facts: [{ name: "Row-level security", value: "enabled" }],
    estimatedRows: 420,
    size: 512_000,
    createSql: `CREATE TABLE "public"."${table}" ("id" bigint NOT NULL);`,
    createSqlSource: "generated",
  }
}

/** Every field a request of this page can carry. */
type Body = {
  schema?: string
  table?: string
  name?: string
  command?: string
  restrictive?: boolean
  roles?: string[]
  using?: string
  withCheck?: string
  enabled?: boolean
  force?: boolean
  role?: string
  query?: string
  claims?: { name: string; value: string }[]
  level?: string
  routine?: string
  arguments?: string
  columns?: string[]
  privileges?: string[]
  future?: boolean
}

type Sent = { method: string; path: string; preview: boolean; body: Body }

type Options = DatabaseMock & {
  security?: Security
  /** What `GET /catalog` answers instead of `CATALOG`. */
  catalog?: typeof CATALOG
  /** The session's capabilities, when neither an admin's nor a viewer's. */
  capabilities?: string[]
  /** `GET /rls` fails while this is set. */
  down?: { down: boolean }
  /** What `POST /rls/test` answers instead of its rows. */
  testRefusal?: string
  /** A preview of a change answers with this refusal instead of its statement. */
  previewRefusal?: string
}

/** The statement the mock writes for a change, recognisable in the dialog. */
function statementOf(sent: Sent): string {
  const { body } = sent
  const table = `"${body.schema}"."${body.table}"`
  if (sent.path === "/ddl/policy" && sent.method === "POST") {
    const roles = (body.roles as string[]).length ? (body.roles as string[]).join(", ") : "PUBLIC"
    return [
      `CREATE POLICY "${body.name}" ON ${table}`,
      `  AS ${body.restrictive ? "RESTRICTIVE" : "PERMISSIVE"}`,
      `  FOR ${body.command}`,
      `  TO ${roles}`,
      body.using ? `  USING (${body.using})` : "",
      body.withCheck ? `  WITH CHECK (${body.withCheck})` : "",
    ]
      .filter(Boolean)
      .join("\n")
  }
  if (sent.path === "/ddl/policy" && sent.method === "PATCH") {
    return `ALTER POLICY "${body.name}" ON ${table}\n  USING (${body.using ?? "…"})`
  }
  if (sent.path === "/ddl/policy" && sent.method === "DELETE") {
    return `DROP POLICY "${body.name}" ON ${table}`
  }
  const lines = []
  if (body.enabled !== undefined) {
    lines.push(`ALTER TABLE ${table} ${body.enabled ? "ENABLE" : "DISABLE"} ROW LEVEL SECURITY`)
  }
  if (body.force !== undefined) {
    lines.push(`ALTER TABLE ${table} ${body.force ? "FORCE" : "NO FORCE"} ROW LEVEL SECURITY`)
  }
  return lines.join(";\n")
}

async function mockRls(page: Page, options: Options = {}) {
  const shell = await mockDatabases(page, options)
  const state = options.security ?? security()
  const sent = { ddl: [] as Sent[], tests: [] as Body[], rls: [] as URL[] }

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
    const method = request.method()

    if (rest === "/catalog") return json(route, options.catalog ?? CATALOG)
    if (rest === "/table") return json(route, detailOf(url.searchParams.get("table") ?? "notes"))
    if (rest === "/server/roles") return json(route, ROLES)
    if (rest === "/rls") {
      sent.rls.push(url)
      if (options.down?.down) {
        return json(
          route,
          { error: { code: "connect_failed", message: "the server did not answer" } },
          502,
        )
      }
      return json(route, state)
    }
    if (rest === "/rls/test") {
      const body = request.postDataJSON() as Body
      sent.tests.push(body)
      if (options.testRefusal) return refused(route, options.testRefusal)
      return json(route, {
        role: body.role,
        setup: [
          "SET LOCAL statement_timeout = '15000ms'",
          ...(body.claims as { name: string; value: string }[]).map(
            (claim) => `SELECT set_config('${claim.name}', '${claim.value}', true)`,
          ),
          `SET LOCAL ROLE "${body.role}"`,
        ],
        notes:
          body.role === "etl"
            ? ["etl is a superuser or has BYPASSRLS, so no policy filters what it reads."]
            : [],
        result: {
          columns: ["id", "owner", "body"],
          types: ["int8", "text", "text"],
          kinds: ["integer", "text", "text"],
          rows: [
            [1, "web", "Quarterly numbers"],
            [4, "web", "Launch checklist"],
          ],
          rowCount: 2,
          rowsAffected: 0,
          duration: "2.1ms",
          truncated: false,
          statement: body.query,
        },
      })
    }
    if (rest.startsWith("/ddl/")) {
      const body = (request.postDataJSON() ?? {}) as Body
      const preview = url.searchParams.get("preview") === "1"
      const entry = { method, path: rest, preview, body }
      sent.ddl.push(entry)
      if (preview && options.previewRefusal) return refused(route, options.previewRefusal)
      const statement = statementOf(entry)
      if (preview) return json(route, { preview: true, statement, statements: [statement] })
      if (rest === "/ddl/policy" && method === "POST") {
        state.policies.push({
          name: body.name as string,
          command: body.command as Policy["command"],
          permissive: !body.restrictive,
          roles: body.roles ?? [],
          using: body.using,
          withCheck: body.withCheck,
        })
      }
      if (rest === "/ddl/policy" && method === "DELETE") {
        state.policies = state.policies.filter((policy) => policy.name !== body.name)
      }
      if (rest === "/ddl/row-security") {
        if (body.enabled !== undefined) state.enabled = body.enabled
        if (body.force !== undefined) state.forced = body.force
      }
      return json(route, { statement, statements: [statement] })
    }
    return route.fallback()
  })
  return { ...shell, sent, state }
}

async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const POLICIES = "/databases/1/schema?schema=public&table=notes&view=policies"
const dialog = (page: Page) => page.getByRole("dialog")
const status = (page: Page) => page.getByRole("region", { name: "Row-level security" })
const tester = (page: Page) => page.getByRole("region", { name: "Try it as a role" })
const policyRow = (page: Page, name: string) => page.getByRole("row").filter({ hasText: name })

/** A dialog or popover is shot only once its entrance has finished. */
async function settled(page: Page) {
  const open = dialog(page)
  await open.evaluate((el) =>
    Promise.all(el.getAnimations({ subtree: true }).map((animation) => animation.finished)),
  )
}

// ---------------------------------------------------------------------------
// The reading
// ---------------------------------------------------------------------------

test("a table's row security is a reading of its own, with its switches and its policies", async ({
  page,
}) => {
  await mockRls(page)
  await visit(page, POLICIES)

  const tab = page.getByRole("tab", { name: /Row security/ })
  await expect(tab).toHaveAttribute("aria-selected", "true")
  await expect(tab).toContainText("2")
  await expect(status(page)).toContainText("On")
  await expect(status(page)).toContainText("The owner is not held to them.")
  await expect(status(page)).toContainText("No policy filters")
  await expect(status(page).getByText("postgres", { exact: true })).toBeVisible()

  await expect(policyRow(page, "notes_tenant_isolation")).toContainText("Every command")
  await expect(policyRow(page, "notes_tenant_isolation")).toContainText("everybody")
  await expect(policyRow(page, "notes_tenant_isolation")).toContainText("permissive")
  await expect(policyRow(page, "notes_auditor_read_only")).toContainText("Reading")
  await expect(policyRow(page, "notes_auditor_read_only")).toContainText("auditor")
  // The table's own figures say so as well.
  await expect(page.getByText("Row-level security", { exact: true }).first()).toBeVisible()
})

test("a table with security off and no policy says what that means and offers to begin", async ({
  page,
}) => {
  await mockRls(page, { security: security({ enabled: false, policies: [] }) })
  await visit(page, POLICIES)

  await expect(status(page)).toContainText("Off")
  await expect(status(page)).toContainText("Every row is visible to anyone who may read the table.")
  await expect(page.getByText("This table has no policy.")).toBeVisible()
  await expect(status(page).getByRole("button", { name: "Turn on…" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Add policy" })).toBeVisible()
})

test("security on with no policy says that nobody who is held to it sees a row", async ({
  page,
}) => {
  await mockRls(page, { security: security({ policies: [] }) })
  await visit(page, POLICIES)
  await expect(status(page)).toContainText("no role that is held to them sees a row")
})

test("a reading that cannot be had says so and can be asked again", async ({ page }) => {
  const down = { down: true }
  await mockRls(page, { down })
  await visit(page, POLICIES)

  await expect(page.getByText(/did not answer/).first()).toBeVisible()
  down.down = false
  await page
    .getByRole("button", { name: /Try again|Retry/ })
    .first()
    .click()
  await expect(status(page)).toContainText("On")
})

// ---------------------------------------------------------------------------
// Adding, changing, dropping
// ---------------------------------------------------------------------------

test("a policy is begun from a template and sent as the form says, after its statement is shown", async ({
  page,
}) => {
  const server = await mockRls(page, { security: security({ policies: [] }) })
  await visit(page, POLICIES)
  await page.getByRole("button", { name: "Add policy" }).click()

  const form = dialog(page)
  await expect(form.getByRole("button", { name: "Add policy" })).toBeDisabled()
  await form.getByRole("button", { name: "Tenant isolation" }).click()
  // The template asks for the column, and guesses the one such columns are called.
  await expect(form.getByRole("combobox", { name: "Column the policy compares" })).toContainText(
    "tenant_id",
  )
  await expect(form.getByLabel("Setting")).toHaveValue("app.tenant_id")
  await expect(form.getByLabel("A row is visible when")).toHaveValue(
    `"tenant_id"::text = current_setting('app.tenant_id', true)`,
  )
  await expect(form.getByLabel("Name")).toHaveValue("notes_tenant_isolation")

  // The statement is the server's, for exactly what the form says.
  await expect(form).toContainText(`CREATE POLICY "notes_tenant_isolation" ON "public"."notes"`)
  const preview = server.sent.ddl.findLast((one) => one.preview)
  expect(preview?.body).toEqual({
    schema: "public",
    table: "notes",
    name: "notes_tenant_isolation",
    command: "ALL",
    restrictive: false,
    roles: [],
    using: `"tenant_id"::text = current_setting('app.tenant_id', true)`,
    withCheck: `"tenant_id"::text = current_setting('app.tenant_id', true)`,
  })

  await form.getByRole("radio", { name: "Restrictive" }).click()
  await form.getByRole("button", { name: "Add a role" }).click()
  await page.getByRole("menuitem", { name: "web" }).click()
  await expect(form).toContainText("AS RESTRICTIVE")
  await expect(form).toContainText("TO web")

  await form.getByRole("button", { name: "Add policy" }).click()
  await expect(form).toHaveCount(0)
  const run = server.sent.ddl.findLast((one) => !one.preview)
  expect(run).toMatchObject({ method: "POST", path: "/ddl/policy" })
  expect(run?.body).toMatchObject({
    restrictive: true,
    roles: ["web"],
    name: "notes_tenant_isolation",
  })
  // The list is read again.
  await expect(policyRow(page, "notes_tenant_isolation")).toContainText("restrictive")
  await expect(page.getByRole("tab", { name: /Row security/ })).toContainText("1")
})

test("the other templates fill the form with their condition, and a name the reader wrote stays", async ({
  page,
}) => {
  await mockRls(page, { security: security({ policies: [] }) })
  await visit(page, POLICIES)
  await page.getByRole("button", { name: "Add policy" }).click()
  const form = dialog(page)

  await form.getByRole("button", { name: "Owner only" }).click()
  await form.getByRole("combobox", { name: "Column the policy compares" }).click()
  await page.getByRole("option", { name: "owner", exact: true }).click()
  await expect(form.getByLabel("A row is visible when")).toHaveValue(`"owner" = current_user`)
  await expect(form.getByLabel("A row may be written when")).toHaveValue(`"owner" = current_user`)

  await form.getByLabel("Name").fill("mine")
  await form.getByRole("button", { name: "Read-only for a role" }).click()
  await form.getByRole("combobox", { name: "Role the policy is for" }).click()
  await page.getByRole("option", { name: "auditor" }).click()
  await expect(form.getByLabel("A row is visible when")).toHaveValue("true")
  // A SELECT policy has no WITH CHECK to write.
  await expect(form.getByLabel("A row may be written when")).toHaveCount(0)
  await expect(form).toContainText("FOR SELECT")
  await expect(form).toContainText("TO auditor")
  await expect(form.getByLabel("Name")).toHaveValue("mine")
})

test("a command takes only the conditions it has a use for", async ({ page }) => {
  await mockRls(page, { security: security({ policies: [] }) })
  await visit(page, POLICIES)
  await page.getByRole("button", { name: "Add policy" }).click()
  const form = dialog(page)

  await form.getByRole("combobox", { name: "Command the policy applies to" }).click()
  await page.getByRole("option", { name: "Inserting" }).click()
  await expect(form.getByLabel("A row is visible when")).toHaveCount(0)
  await expect(form.getByLabel("A row may be written when")).toBeVisible()

  await form.getByRole("combobox", { name: "Command the policy applies to" }).click()
  await page.getByRole("option", { name: "Deleting" }).click()
  await expect(form.getByLabel("A row is visible when")).toBeVisible()
  await expect(form.getByLabel("A row may be written when")).toHaveCount(0)
})

test("a statement the server refuses is said in its words, and nothing can be run", async ({
  page,
}) => {
  const server = await mockRls(page, {
    security: security({ policies: [] }),
    previewRefusal: "the USING condition contains a statement separator; use the Query tab for it",
  })
  await visit(page, POLICIES)
  await page.getByRole("button", { name: "Add policy" }).click()
  const form = dialog(page)
  await form.getByLabel("Name").fill("bad")
  await form.getByLabel("A row is visible when").fill("true); DROP TABLE notes; --")

  await expect(form).toContainText("contains a statement separator")
  await expect(form).toContainText("No statement: the change cannot be planned as it stands.")
  await expect(form.getByRole("button", { name: "Add policy" })).toBeDisabled()
  expect(server.sent.ddl.filter((one) => !one.preview)).toHaveLength(0)
})

test("a policy is changed by restating its roles and its conditions", async ({ page }) => {
  const server = await mockRls(page)
  await visit(page, POLICIES)
  await page.getByRole("button", { name: "Edit notes_auditor_read_only" }).click()
  const form = dialog(page)

  await expect(form).toContainText("Edit policy")
  await expect(form).toContainText(/command and kind cannot be changed/)
  await expect(form.getByRole("button", { name: "Save policy" })).toBeDisabled()
  await form.getByLabel("A row is visible when").fill("owner = 'auditor'")
  await expect(form).toContainText(`ALTER POLICY "notes_auditor_read_only"`)
  await form.getByRole("button", { name: "Save policy" }).click()
  await expect(form).toHaveCount(0)

  const run = server.sent.ddl.findLast((one) => !one.preview)
  expect(run).toMatchObject({ method: "PATCH", path: "/ddl/policy" })
  expect(run?.body).toEqual({
    schema: "public",
    table: "notes",
    name: "notes_auditor_read_only",
    roles: ["auditor"],
    using: "owner = 'auditor'",
  })
})

test("a policy is dropped after its statement is shown", async ({ page }) => {
  const server = await mockRls(page)
  await visit(page, POLICIES)
  await page.getByRole("button", { name: "Drop notes_auditor_read_only" }).click()

  const confirm = page.getByRole("dialog", { name: "Drop policy" })
  await expect(confirm).toContainText(`DROP POLICY "notes_auditor_read_only" ON "public"."notes"`)
  await confirm.getByRole("button", { name: "Drop policy" }).click()
  await expect(policyRow(page, "notes_auditor_read_only")).toHaveCount(0)
  expect(server.sent.ddl.findLast((one) => !one.preview)).toMatchObject({
    method: "DELETE",
    path: "/ddl/policy",
    body: { schema: "public", table: "notes", name: "notes_auditor_read_only" },
  })
})

test("turning security off asks first, and turning it on does not", async ({ page }) => {
  const server = await mockRls(page)
  await visit(page, POLICIES)
  await status(page).getByRole("button", { name: "Change…" }).click()
  const form = dialog(page)

  await form.getByRole("switch", { name: /Filter the table's rows/ }).click()
  await expect(form).toContainText("Every row becomes visible")
  await expect(form).toContainText(`ALTER TABLE "public"."notes" DISABLE ROW LEVEL SECURITY`)
  await form.getByRole("button", { name: "Apply…" }).click()
  await expect(form).toContainText("Turn row-level security off?")
  expect(server.sent.ddl.filter((one) => !one.preview)).toHaveLength(0)
  await form.getByRole("button", { name: "Keep editing" }).click()
  await form.getByRole("button", { name: "Apply…" }).click()
  await form.getByRole("button", { name: "Apply", exact: true }).click()

  await expect(form).toHaveCount(0)
  expect(server.sent.ddl.findLast((one) => !one.preview)?.body).toEqual({
    schema: "public",
    table: "notes",
    enabled: false,
  })
  await expect(status(page)).toContainText("Off")

  // And on again: no question.
  await status(page).getByRole("button", { name: "Turn on…" }).click()
  await dialog(page)
    .getByRole("switch", { name: /Filter the table's rows/ })
    .click()
  await expect(dialog(page)).not.toContainText("Every row becomes visible")
  await dialog(page)
    .getByRole("switch", { name: /Hold the table's owner/ })
    .click()
  await dialog(page).getByRole("button", { name: "Apply", exact: true }).click()
  await expect(dialog(page)).toHaveCount(0)
  expect(server.sent.ddl.findLast((one) => !one.preview)?.body).toEqual({
    schema: "public",
    table: "notes",
    enabled: true,
    force: true,
  })
  await expect(status(page)).toContainText("The owner is held to the policies as well.")
})

test("no longer holding the owner to the policies asks first", async ({ page }) => {
  const server = await mockRls(page, { security: security({ forced: true }) })
  await visit(page, POLICIES)
  await status(page).getByRole("button", { name: "Change…" }).click()
  const form = dialog(page)

  await form.getByRole("switch", { name: /Hold the table's owner/ }).click()
  await expect(form).toContainText("The owner sees every row")
  await expect(form).toContainText(`NO FORCE ROW LEVEL SECURITY`)
  await form.getByRole("button", { name: "Apply…" }).click()
  await expect(form).toContainText("Stop holding the owner to the policies?")
  expect(server.sent.ddl.filter((one) => !one.preview)).toHaveLength(0)
  await form.getByRole("button", { name: "Apply", exact: true }).click()
  await expect(form).toHaveCount(0)
  expect(server.sent.ddl.findLast((one) => !one.preview)?.body).toEqual({
    schema: "public",
    table: "notes",
    force: false,
  })
})

// ---------------------------------------------------------------------------
// The test of a role
// ---------------------------------------------------------------------------

test("a role is tried with the settings the policies read, and the rows it sees are shown", async ({
  page,
}) => {
  const server = await mockRls(page)
  await visit(page, POLICIES)

  // The setting the tenant policy reads is offered as a row already.
  await expect(tester(page).getByLabel("Setting 1 name")).toHaveValue("app.tenant_id")
  await expect(tester(page).getByRole("button", { name: "Run" })).toBeDisabled()
  await tester(page).getByRole("combobox", { name: "Role" }).click()
  // The engine's own roles are not offered, and the ones nothing filters are marked.
  await expect(page.getByRole("option", { name: "pg_monitor" })).toHaveCount(0)
  await expect(page.getByRole("option", { name: "etl · not filtered" })).toBeVisible()
  await page.getByRole("option", { name: "web", exact: true }).click()
  await tester(page).getByLabel("Setting 1 value").fill("acme")
  await expect(tester(page).getByLabel("Query")).toHaveValue(`SELECT * FROM "public"."notes"`)
  await tester(page).getByRole("button", { name: "Run as web" }).click()

  await expect(tester(page)).toContainText("web sees 2 rows")
  await expect(tester(page)).toContainText("Quarterly numbers")
  await expect(tester(page)).toContainText(`SET LOCAL ROLE "web"`)
  await expect(tester(page)).toContainText(`SELECT set_config('app.tenant_id', 'acme', true)`)
  expect(server.sent.tests).toEqual([
    {
      role: "web",
      schema: "public",
      table: "notes",
      query: `SELECT * FROM "public"."notes"`,
      claims: [{ name: "app.tenant_id", value: "acme" }],
    },
  ])
})

test("a role no policy filters is said to be one", async ({ page }) => {
  await mockRls(page)
  await visit(page, POLICIES)
  await tester(page).getByRole("combobox", { name: "Role" }).click()
  // A superuser says nothing about the policies and is not offered.
  await expect(page.getByRole("option", { name: /^app/ })).toHaveCount(0)
  await page.getByRole("option", { name: "etl · not filtered" }).click()
  await tester(page).getByRole("button", { name: "Run as etl" }).click()
  await expect(tester(page)).toContainText("no policy filters what it reads")
})

test("a setting that would leave the role is stopped before anything is sent", async ({ page }) => {
  const server = await mockRls(page)
  await visit(page, POLICIES)
  await tester(page).getByRole("combobox", { name: "Role" }).click()
  await page.getByRole("option", { name: "web", exact: true }).click()
  await tester(page).getByLabel("Setting 1 name").fill("role")
  await expect(tester(page)).toContainText("A setting is a name with a dot in it")
  await expect(tester(page).getByRole("button", { name: "Run as web" })).toBeDisabled()
  expect(server.sent.tests).toHaveLength(0)
})

test("a test the server refuses is said in its words", async ({ page }) => {
  await mockRls(page, {
    testRefusal: "only a SELECT can be tried as a role: this one starts with DELETE",
  })
  await visit(page, POLICIES)
  await tester(page).getByRole("combobox", { name: "Role" }).click()
  await page.getByRole("option", { name: "web", exact: true }).click()
  await tester(page).getByLabel("Query").fill("DELETE FROM notes")
  await tester(page).getByRole("button", { name: "Run as web" }).click()
  await expect(tester(page).getByRole("alert")).toContainText(
    "Nothing was run as web. only a SELECT can be tried as a role",
  )
})

// ---------------------------------------------------------------------------
// Who is shown what
// ---------------------------------------------------------------------------

test("a role that may only read sees the policies and nothing that changes them", async ({
  page,
}) => {
  await mockRls(page, { viewer: true })
  await visit(page, POLICIES)

  await expect(policyRow(page, "notes_tenant_isolation")).toBeVisible()
  await expect(page.getByRole("button", { name: "Add policy" })).toHaveCount(0)
  await expect(status(page).getByRole("button")).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Edit / })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Drop notes_/ })).toHaveCount(0)
  await expect(tester(page)).toHaveCount(0)
})

test("an account that runs queries may try a role and may not change a policy", async ({
  page,
}) => {
  await mockRls(page, { capabilities: ["read", "service.control"] })
  await visit(page, POLICIES)

  await expect(tester(page)).toBeVisible()
  await expect(page.getByRole("button", { name: "Add policy" })).toHaveCount(0)
  await expect(status(page).getByRole("button")).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Edit / })).toHaveCount(0)
})

test("an administrator without the destructive capability cannot take the filter off", async ({
  page,
}) => {
  await mockRls(page, { capabilities: ["read", "service.control", "system.admin"] })
  await visit(page, POLICIES)

  await expect(page.getByRole("button", { name: "Edit notes_tenant_isolation" })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Drop notes_/ })).toHaveCount(0)
  await status(page).getByRole("button", { name: "Change…" }).click()
  await expect(dialog(page).getByRole("switch", { name: /Filter the table's rows/ })).toBeDisabled()
  await expect(dialog(page)).toContainText("Turning it off needs the destructive capability")
})

test("a protected connection shows the policies and can still try a role", async ({ page }) => {
  await mockRls(page, { rows: { 1: { readOnly: true } } })
  await visit(page, POLICIES)

  await expect(policyRow(page, "notes_tenant_isolation")).toBeVisible()
  await expect(page.getByRole("button", { name: "Add policy" })).toHaveCount(0)
  await expect(status(page).getByRole("button")).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Edit / })).toHaveCount(0)
  await expect(tester(page)).toBeVisible()
})

test("an engine without row security has no such reading", async ({ page }) => {
  const asked: string[] = []
  await mockRls(page)
  await page.route("**/api/v1/databases/2/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    asked.push(path)
    if (path.endsWith("/table")) return json(route, detailOf("posts"))
    return route.fallback()
  })
  await visit(page, "/databases/2/schema?schema=blog_main&table=posts")
  await expect(page.getByRole("tablist", { name: /Readings of/ })).toBeVisible()
  await expect(page.getByRole("tab", { name: /Row security/ })).toHaveCount(0)
  expect(asked.filter((path) => path.endsWith("/rls"))).toHaveLength(0)
})

test("the reading fits a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockRls(page)
  await visit(page, POLICIES)

  await expect(
    policyRow(page, "notes_tenant_isolation").or(page.getByText("notes_tenant_isolation")).first(),
  ).toBeVisible()
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(0)
  await expect(tester(page)).toBeVisible()
  await tester(page).scrollIntoViewIfNeeded()
  const after = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(after).toBeLessThanOrEqual(0)
})

// ---------------------------------------------------------------------------
// Function and column grants
// ---------------------------------------------------------------------------

const PRIVILEGES = {
  supported: true,
  editable: ["password", "login", "superuser"],
  levels: [
    {
      level: "database",
      privileges: ["CONNECT", "CREATE", "TEMPORARY", "ALL"],
      needs: ["database"],
      grantOption: true,
    },
    {
      level: "table",
      privileges: ["SELECT", "INSERT", "UPDATE", "DELETE", "ALL"],
      needs: ["schema", "table"],
      allObjects: true,
      future: true,
      grantOption: true,
    },
    {
      level: "function",
      privileges: ["EXECUTE", "ALL"],
      needs: ["schema", "routine"],
      allObjects: true,
      future: true,
      grantOption: true,
    },
    {
      level: "column",
      privileges: ["SELECT", "INSERT", "UPDATE", "REFERENCES", "ALL"],
      needs: ["schema", "table", "columns"],
      grantOption: true,
    },
    { level: "role", privileges: [], needs: ["memberOf"] },
  ],
}

const REPORTING_GRANTS = [
  {
    grantee: "reporting",
    level: "function",
    database: "shop_main",
    schema: "public",
    routine: "order_count",
    arguments: "cid bigint",
    privileges: ["EXECUTE"],
    grantor: "app",
  },
  {
    grantee: "reporting",
    level: "column",
    database: "shop_main",
    schema: "public",
    table: "notes",
    column: "body",
    privileges: ["SELECT"],
    grantor: "app",
  },
]

async function mockGrants(page: Page, options: Options = {}) {
  await mockRls(page, options)
  const sent: { path: string; preview: boolean; body: Body }[] = []
  await page.route("**/api/v1/databases/1/server/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const rest = url.pathname.replace(/^\/api\/v1\/databases\/1/, "")
    if (rest === "/server/privileges") return json(route, PRIVILEGES)
    const account = /^\/server\/roles\/([^/]+)$/.exec(rest)?.[1]
    if (account) {
      return json(route, {
        ...ROLES.roles.find((one) => one.name === account),
        attributes: { inherit: true, replication: false, bypassRls: false },
        members: [],
        config: [],
        grants: account === "reporting" ? REPORTING_GRANTS : [],
        grantsTruncated: false,
        editable: PRIVILEGES.editable,
      })
    }
    const target = /^\/server\/roles\/([^/]+)\/privileges/.exec(rest)?.[1]
    if (target) {
      const body = request.postDataJSON() as Body
      const preview = url.searchParams.get("preview") === "1"
      sent.push({ path: rest, preview, body })
      const statements =
        body.level === "function"
          ? [
              body.routine
                ? `${rest.endsWith("/revoke") ? "REVOKE" : "GRANT"} EXECUTE ON ROUTINE "public"."${body.routine}"(${body.arguments ?? ""}) TO "${target}"`
                : `GRANT EXECUTE ON ALL ROUTINES IN SCHEMA "public" TO "${target}"`,
            ]
          : [
              `${rest.endsWith("/revoke") ? "REVOKE" : "GRANT"} ${(body.privileges as string[]).map((one) => `${one} (${(body.columns as string[]).map((c) => `"${c}"`).join(", ")})`).join(", ")} ON TABLE "public"."${body.table}" ${rest.endsWith("/revoke") ? "FROM" : "TO"} "${target}"`,
            ]
      return json(route, preview ? { preview: true, statements } : { ok: true, statements })
    }
    return route.fallback()
  })
  return { sent }
}

const ACCOUNT = "/databases/1/access?account=reporting"
const matrix = (page: Page) => page.locator("[data-slot=grants-matrix]")

test("EXECUTE is granted on one function of several that share a name", async ({ page }) => {
  const server = await mockGrants(page)
  await visit(page, ACCOUNT)

  await matrix(page)
    .getByRole("button", { name: /^Functions/ })
    .click()
  const list = matrix(page).getByRole("list", { name: "Functions" })
  // The functions an extension installed are left out, and the held one is pressed.
  await expect(list.locator("[data-object]")).toHaveCount(2)
  await expect(list).not.toContainText("armor")
  await expect(
    list.getByRole("button", { name: "EXECUTE on order_count(cid bigint)" }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect(matrix(page)).toContainText("until EXECUTE is taken from PUBLIC")

  await list.getByRole("button", { name: "EXECUTE on order_count(cid text)" }).click()
  const pending = page.locator("[data-slot=grants-pending]")
  await expect(pending).toContainText("1 change not applied")
  await pending.getByRole("button", { name: "Review…" }).click()

  const review = page.getByRole("dialog", { name: /Change what reporting holds/ })
  await expect(review).toContainText(
    `GRANT EXECUTE ON ROUTINE "public"."order_count"(cid text) TO "reporting"`,
  )
  expect(server.sent.find((one) => one.preview)?.body).toEqual({
    level: "function",
    schema: "public",
    routine: "order_count",
    arguments: "cid text",
    privileges: ["EXECUTE"],
  })
  await review.getByRole("button", { name: "Apply" }).click()
  await expect.poll(() => server.sent.filter((one) => !one.preview).length).toBe(1)
})

// A schema whose every function the matrix lists.
const APPLICATION_ONLY = {
  ...CATALOG,
  objects: { ...CATALOG.objects, functions: FUNCTIONS.filter((one) => !one.extension) },
}

test("EXECUTE on every function of a schema is one request that can cover later ones", async ({
  page,
}) => {
  const server = await mockGrants(page, { catalog: APPLICATION_ONLY })
  await visit(page, "/databases/1/access?account=readers")
  await matrix(page)
    .getByRole("button", { name: /^Functions/ })
    .click()
  await matrix(page).getByRole("button", { name: "EXECUTE on every function in public" }).click()
  await page.locator("[data-slot=grants-pending]").getByRole("button", { name: "Review…" }).click()
  const review = page.getByRole("dialog", { name: /Change what readers holds/ })
  await expect(review).toContainText(
    `GRANT EXECUTE ON ALL ROUTINES IN SCHEMA "public" TO "readers"`,
  )
  await review.getByRole("switch", { name: /created later in public/ }).click()
  await expect
    .poll(() => server.sent.filter((one) => one.preview).at(-1)?.body)
    .toEqual({ level: "function", schema: "public", privileges: ["EXECUTE"], future: true })
})

for (const [held, catalog] of [
  ["an extension's functions", CATALOG],
  ["more functions than the catalogue lists", { ...APPLICATION_ONLY, truncated: ["functions"] }],
] as [string, typeof CATALOG][]) {
  test(`EXECUTE on every listed function is asked for one by one when the schema holds ${held}`, async ({
    page,
  }) => {
    const server = await mockGrants(page, { catalog })
    await visit(page, "/databases/1/access?account=readers")
    await matrix(page)
      .getByRole("button", { name: /^Functions/ })
      .click()
    await matrix(page).getByRole("button", { name: "EXECUTE on every function in public" }).click()
    await page
      .locator("[data-slot=grants-pending]")
      .getByRole("button", { name: "Review…" })
      .click()
    const review = page.getByRole("dialog", { name: /Change what readers holds/ })
    // ALL ROUTINES IN SCHEMA would reach the functions the matrix does not show.
    await expect(review).toContainText(
      `GRANT EXECUTE ON ROUTINE "public"."order_count"(cid text) TO "readers"`,
    )
    await expect(review).not.toContainText("ALL ROUTINES")
    await expect(review.getByRole("switch", { name: /created later in public/ })).toHaveCount(0)
    expect(server.sent.filter((one) => one.preview).every((one) => one.body.routine)).toBe(true)
  })
}

test("privileges are granted on single columns, and columns pressed alike are one request", async ({
  page,
}) => {
  const server = await mockGrants(page)
  await visit(page, ACCOUNT)

  await matrix(page)
    .getByRole("button", { name: /^Columns/ })
    .click()
  await expect(matrix(page)).toContainText("A privilege on the whole table covers every column")
  await matrix(page).getByRole("combobox").click()
  await page.getByRole("option", { name: "notes", exact: true }).click()

  const list = matrix(page).getByRole("list", { name: "Columns" })
  await expect(list.locator("[data-object]")).toHaveCount(4)
  await expect(list.getByRole("button", { name: "SELECT on notes.body" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await list.getByRole("button", { name: "SELECT on notes.id" }).click()
  await list.getByRole("button", { name: "SELECT on notes.owner" }).click()
  await list.getByRole("button", { name: "UPDATE on notes.owner" }).click()
  await page.locator("[data-slot=grants-pending]").getByRole("button", { name: "Review…" }).click()

  const review = page.getByRole("dialog", { name: /Change what reporting holds/ })
  await expect(review).toContainText(`GRANT SELECT ("id")`)
  await expect(review).toContainText(`GRANT SELECT ("owner")`)
  const previews = server.sent.filter((one) => one.preview).map((one) => one.body)
  expect(previews).toEqual(
    expect.arrayContaining([
      {
        level: "column",
        schema: "public",
        table: "notes",
        columns: ["id"],
        privileges: ["SELECT"],
      },
      {
        level: "column",
        schema: "public",
        table: "notes",
        columns: ["owner"],
        privileges: ["SELECT", "UPDATE"],
      },
    ]),
  )
})

test("a role that may not grant sees what an account holds on functions and columns", async ({
  page,
}) => {
  await mockGrants(page, { viewer: true })
  await visit(page, ACCOUNT)
  await matrix(page)
    .getByRole("button", { name: /^Functions/ })
    .click()
  await expect(matrix(page).getByRole("list", { name: "Functions" })).toContainText(
    "order_count(cid bigint)",
  )
  await expect(matrix(page).getByRole("button", { name: /EXECUTE on/ })).toHaveCount(0)
  await matrix(page)
    .getByRole("button", { name: /^Columns/ })
    .click()
  await expect(matrix(page)).toContainText("body")
  await expect(matrix(page).getByRole("button", { name: /SELECT on/ })).toHaveCount(0)
})

test("the grants fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockGrants(page)
  await visit(page, ACCOUNT)
  await matrix(page)
    .getByRole("button", { name: /^Functions/ })
    .click()
  await expect(matrix(page).getByRole("list", { name: "Functions" })).toBeVisible()
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(0)
})

// ---------------------------------------------------------------------------
// Screenshots
// ---------------------------------------------------------------------------

test("row security and the new grants, shown @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to save the screenshots")
  const out = process.env.JD_SHOTS as string
  const shot = (name: string, size: "1440" | "390" = "1440") =>
    page.screenshot({ path: `${out}/${name}-${size}.png` })
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockGrants(page)

  await visit(page, POLICIES)
  await expect(policyRow(page, "notes_tenant_isolation")).toBeVisible()
  await shot("policies")

  await tester(page).scrollIntoViewIfNeeded()
  await tester(page).getByRole("combobox", { name: "Role" }).click()
  await page.getByRole("option", { name: "web", exact: true }).click()
  await tester(page).getByLabel("Setting 1 value").fill("acme")
  await tester(page).getByRole("button", { name: "Run as web" }).click()
  await expect(tester(page)).toContainText("web sees 2 rows")
  await tester(page).scrollIntoViewIfNeeded()
  await shot("role-test")

  await page.getByRole("button", { name: "Add policy" }).scrollIntoViewIfNeeded()
  await page.getByRole("button", { name: "Add policy" }).click()
  await dialog(page).getByRole("button", { name: "Tenant isolation" }).click()
  await expect(dialog(page)).toContainText(`CREATE POLICY "notes_tenant_isolation"`)
  await settled(page)
  await shot("policy-template")
  await dialog(page).getByRole("button", { name: "Cancel" }).click()

  await status(page).getByRole("button", { name: "Change…" }).click()
  await dialog(page)
    .getByRole("switch", { name: /Filter the table's rows/ })
    .click()
  await expect(dialog(page)).toContainText("Every row becomes visible")
  await settled(page)
  await shot("security-off")
  await dialog(page).getByRole("button", { name: "Cancel" }).click()

  await visit(page, ACCOUNT)
  await matrix(page)
    .getByRole("button", { name: /^Functions/ })
    .click()
  await matrix(page)
    .getByRole("list", { name: "Functions" })
    .getByRole("button", { name: "EXECUTE on order_count(cid text)" })
    .click()
  await shot("grants-functions")

  await matrix(page)
    .getByRole("button", { name: /^Columns/ })
    .click()
  await matrix(page).getByRole("combobox").click()
  await page.getByRole("option", { name: "notes", exact: true }).click()
  await expect(matrix(page).getByRole("list", { name: "Columns" })).toContainText("owner")
  await matrix(page).getByRole("button", { name: "SELECT on notes.id" }).click()
  await shot("grants-columns")

  await page.setViewportSize({ width: 390, height: 844 })
  await visit(page, POLICIES)
  await expect(page.getByText("notes_tenant_isolation").first()).toBeVisible()
  await shot("policies", "390")
  await visit(page, ACCOUNT)
  await matrix(page)
    .getByRole("button", { name: /^Functions/ })
    .click()
  await expect(matrix(page).getByRole("list", { name: "Functions" })).toBeVisible()
  await shot("grants-functions", "390")
})
