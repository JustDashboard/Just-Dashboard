import { expect as baseExpect, test, type Locator, type Page, type Route } from "@playwright/test"
import { hold, mockDatabases } from "./database-fixture"

/**
 * Functions, procedures and triggers in the schema browser: the forms that
 * make them, the ones that change them, and the drop.
 *
 * What is checked is what each form sends and what it shows before it sends:
 * the statement is the server's own (the mock writes one the dialog can be
 * told from), a program the form cannot state is not offered for editing,
 * and a role that may not run code in the database is not drawn the controls.
 * The API is mocked in the browser.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

type Sent = { method: string; path: string; preview: boolean; body: Record<string, unknown> }

const POSTGRES = 1
const MYSQL = 2
const SQLITE = 7

const ORDERS = {
  schema: "public",
  name: "orders",
  type: "table",
  owner: "app",
  columns: [
    { name: "id", type: "bigint", nullable: false, position: 1 },
    { name: "customer_id", type: "bigint", nullable: false, position: 2 },
    { name: "total", type: "numeric", nullable: false, position: 3 },
  ],
  primaryKey: ["id"],
  indexes: [],
  foreignKeys: [],
  constraints: [],
  referencedBy: [],
  facts: [],
  estimatedRows: 9000,
  size: 3_399_680,
  createSql: 'CREATE TABLE "public"."orders" (\n  "id" bigint NOT NULL\n);',
  createSqlSource: "generated",
}

function catalogOf(connection: number, over: { trigger?: boolean } = {}) {
  if (connection === SQLITE) {
    return {
      schema: "main",
      defaultSchema: "main",
      schemas: [{ name: "main", default: true, tables: 2 }],
      objects: {
        tables: [
          { kind: "table", schema: "main", name: "orders", estimatedRows: 9 },
          { kind: "table", schema: "main", name: "notes", estimatedRows: 4 },
        ],
        views: [],
        triggers: [
          {
            kind: "trigger",
            schema: "main",
            name: "orders_touch",
            table: "orders",
            detail: "AFTER INSERT, each row",
          },
        ],
      },
      truncated: [],
      limit: 5000,
    }
  }
  const schema = connection === MYSQL ? "blog_main" : "public"
  return {
    schema,
    defaultSchema: schema,
    schemas: [{ name: schema, default: true, tables: 2 }],
    objects: {
      tables: [
        { kind: "table", schema, name: "orders", estimatedRows: 9000, size: 3_399_680 },
        { kind: "table", schema, name: "customers", estimatedRows: 1200, size: 504_000 },
      ],
      views: [],
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
        {
          kind: "function",
          schema,
          name: "legacy_total",
          signature: "",
          returns: "numeric",
          language: "sql",
        },
        ...(over.trigger === false
          ? []
          : [
              {
                kind: "function",
                schema,
                name: "touch_row",
                signature: "",
                returns: "trigger",
                language: "plpgsql",
              },
            ]),
      ],
      procedures: [{ kind: "procedure", schema, name: "archive_orders", signature: "cutoff date" }],
      triggers: [
        {
          kind: "trigger",
          schema,
          name: "orders_touch",
          table: "orders",
          detail: "BEFORE UPDATE, each row",
        },
      ],
      sequences: [],
      types: [],
      events: [],
    },
    truncated: [],
    limit: 5000,
  }
}

const ORDER_COUNT = {
  kind: "function",
  editable: true,
  routine: {
    kind: "function",
    schema: "public",
    name: "order_count",
    args: [{ name: "cid", mode: "in", type: "bigint" }],
    returns: "bigint",
    language: "sql",
    body: "SELECT count(*) FROM orders WHERE customer_id = cid",
    volatility: "stable",
  },
}

const ARCHIVE = {
  kind: "procedure",
  editable: true,
  routine: {
    kind: "procedure",
    schema: "public",
    name: "archive_orders",
    args: [{ name: "cutoff", mode: "in", type: "date" }],
    language: "plpgsql",
    body: "BEGIN\n  DELETE FROM orders WHERE placed < cutoff;\nEND;",
  },
}

const ORDERS_TOUCH = {
  kind: "trigger",
  editable: true,
  trigger: {
    schema: "public",
    name: "orders_touch",
    table: "orders",
    timing: "before",
    events: ["update"],
    forEach: "row",
    function: "touch_row",
    functionSchema: "public",
  },
}

const LEGACY = {
  kind: "function",
  editable: false,
  reason: "it sets COST 5",
}

type ProgramsMock = {
  /** The session's capabilities, when neither an admin's nor a viewer's. */
  capabilities?: string[]
  /** What a run answers instead of its statement. */
  runRefused?: { status: number; message: string }
  /** `GET /program` fails. */
  programDown?: boolean
  /** The catalogue has no function that returns trigger. */
  noTriggerFunction?: boolean
  /** A preview answers only once this settles. */
  previewHeld?: Promise<unknown>
}

/** What the mock writes as the statement for a request: recognisable in the dialog. */
function statementOf(sent: Sent): string {
  const { body } = sent
  const name = `"${body.schema ?? "public"}"."${body.name}"`
  if (sent.path === "/ddl/routine" && sent.method === "POST") {
    const args = ((body.args as { name: string; type: string }[]) ?? [])
      .map((a) => `${a.name} ${a.type}`)
      .join(", ")
    const verb = body.replaces ? "CREATE OR REPLACE" : "CREATE"
    const returns = body.returns ? `\nRETURNS ${body.returns}` : ""
    return `${verb} ${String(body.kind).toUpperCase()} ${name}(${args})${returns}\nAS $jd$${body.body}$jd$`
  }
  if (sent.path === "/ddl/routine")
    return `DROP ${String(body.kind).toUpperCase()} ${name}(${body.signature ?? ""})`
  if (sent.path === "/ddl/trigger" && sent.method === "POST") {
    const on = `"${body.schema ?? "public"}"."${body.table}"`
    const run = body.function ? `EXECUTE FUNCTION "${body.function}"()` : String(body.body)
    const make = `CREATE TRIGGER "${body.name}"\n${String(body.timing).toUpperCase()} ${(body.events as string[]).join(" OR ").toUpperCase()} ON ${on}\n${run}`
    return body.replace ? `DROP TRIGGER "${body.name}" ON ${on};\n${make}` : make
  }
  return `DROP TRIGGER "${body.name}" ON "${body.schema ?? "public"}"."${body.table}"`
}

async function mockPrograms(page: Page, options: ProgramsMock = {}, connection = POSTGRES) {
  const shell = await mockDatabases(page, options.capabilities ? {} : {})
  const sent = { ddl: [] as Sent[], programs: [] as URL[], objects: [] as URL[] }

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
    const method = request.method()

    if (rest === "/catalog") {
      return json(route, catalogOf(connection, { trigger: !options.noTriggerFunction }))
    }
    if (rest === "/table") return json(route, ORDERS)
    if (rest === "/program") {
      sent.programs.push(url)
      if (options.programDown) {
        return json(
          route,
          { error: { code: "connect_failed", message: "the server did not answer" } },
          502,
        )
      }
      const name = url.searchParams.get("name")
      const kind = url.searchParams.get("kind")
      if (name === "legacy_total") return json(route, LEGACY)
      if (kind === "procedure") return json(route, ARCHIVE)
      if (kind === "trigger") return json(route, ORDERS_TOUCH)
      return json(route, ORDER_COUNT)
    }
    if (rest === "/object") {
      sent.objects.push(url)
      const kind = url.searchParams.get("kind")
      const name = url.searchParams.get("name") ?? ""
      const schema = url.searchParams.get("schema") ?? "public"
      if (kind === "trigger") {
        return json(route, {
          kind,
          schema,
          name,
          table: "orders",
          definition: `CREATE TRIGGER ${name} BEFORE UPDATE ON ${schema}.orders\n  FOR EACH ROW EXECUTE FUNCTION touch_row();`,
          source: "engine",
          details: [{ name: "Fires", value: "BEFORE UPDATE, each row" }],
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
        details: [{ name: "Returns", value: "bigint" }],
      })
    }
    if (rest.startsWith("/ddl/")) {
      const body = (request.postDataJSON() ?? {}) as Record<string, unknown>
      const preview = url.searchParams.get("preview") === "1"
      const entry = { method, path: rest, preview, body }
      sent.ddl.push(entry)
      const statement = statementOf(entry)
      if (preview) {
        await options.previewHeld
        return json(route, {
          preview: true,
          statement,
          statements: [statement],
          // The Postgres reply for a routine whose signature changes.
          ...(body.replaces && body.returns !== "bigint"
            ? {
                note: "Postgres cannot replace a function whose parameters or return type change, so this drops it and creates it again in one transaction: if the new definition is refused, the old one stays.",
              }
            : {}),
        })
      }
      if (options.runRefused) {
        return json(
          route,
          { error: { code: "bad_request", message: options.runRefused.message } },
          options.runRefused.status,
        )
      }
      return json(route, { statement, statements: [statement] })
    }
    return route.fallback()
  })

  return { ...shell, sent }
}

const runs = (sent: { ddl: Sent[] }) => sent.ddl.filter((entry) => !entry.preview)
const previews = (sent: { ddl: Sent[] }) => sent.ddl.filter((entry) => entry.preview)

const SCHEMA = "/databases/1/schema"
const FUNCTION = `${SCHEMA}?schema=public&object=function&name=order_count&sig=${encodeURIComponent("cid bigint")}`

/** Replaces the body editor's text, through the editor, with the keyboard in it. */
async function writeBody(page: Page, dialog: Locator, text: string) {
  const body = dialog.locator("[data-slot=program-body] .monaco-editor").first()
  await expect(body).toBeVisible({ timeout: 45_000 })
  await body.click()
  await page.evaluate((value) => {
    type Held = {
      getDomNode(): HTMLElement | null
      setValue(value: string): void
      focus(): void
    }
    const monaco = (window as unknown as { monaco: { editor: { getEditors(): Held[] } } }).monaco
    const held = monaco.editor
      .getEditors()
      .find((candidate) => candidate.getDomNode()?.closest("[data-slot=program-body]"))
    if (!held) throw new Error("no body editor on the page")
    held.setValue(value)
    held.focus()
  }, text)
}

const settle = (dialog: Locator) =>
  dialog.evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)))

/* -------------------------------------------------------------- functions */

test("a function is written with its parameters and body, and the statement shown is the server's", async ({
  page,
}) => {
  const { sent } = await mockPrograms(page)
  await page.goto(`${SCHEMA}?new=function`)
  const dialog = page.getByRole("dialog", { name: "New function" })
  await expect(dialog).toBeVisible()
  // Nothing is asked of the server until the form says enough.
  await expect(dialog.getByText(/Fill in the function/)).toBeVisible()
  expect(previews(sent)).toHaveLength(0)
  await expect(dialog.getByRole("button", { name: "Create function" })).toBeDisabled()

  await dialog.getByLabel("Name", { exact: true }).fill("invoice_total")
  await dialog.getByRole("button", { name: "Add parameter" }).click()
  const parameter = dialog.getByRole("group", { name: "Parameter 1" })
  await parameter.getByRole("textbox").nth(0).fill("cid")
  await parameter.getByRole("textbox").nth(1).fill("bigint")
  await dialog.getByLabel("Returns", { exact: true }).fill("numeric")
  await writeBody(page, dialog, "SELECT sum(total) FROM orders WHERE customer_id = cid;")

  const statement = dialog.getByText(/CREATE FUNCTION "public"."invoice_total"\(cid bigint\)/)
  await expect(statement).toBeVisible()
  await expect(dialog.getByText("RETURNS numeric")).toBeVisible()
  await expect(dialog.getByText(/\$jd\$SELECT sum\(total\)/)).toBeVisible()
  expect(runs(sent)).toHaveLength(0)

  await dialog.getByRole("button", { name: "Create function" }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)).toHaveLength(1)
  expect(runs(sent)[0]).toMatchObject({ method: "POST", path: "/ddl/routine" })
  expect(runs(sent)[0].body).toEqual({
    kind: "function",
    schema: "public",
    name: "invoice_total",
    args: [{ name: "cid", mode: "in", type: "bigint" }],
    returns: "numeric",
    language: "plpgsql",
    body: "SELECT sum(total) FROM orders WHERE customer_id = cid;",
  })
  // It opens on what it made.
  await expect(page).toHaveURL(/object=function&name=invoice_total/)
})

test("a Postgres function has the fields Postgres has", async ({ page }) => {
  await mockPrograms(page)
  await page.goto(`${SCHEMA}?new=function`)
  const dialog = page.getByRole("dialog", { name: "New function" })
  await expect(dialog.getByLabel("Name", { exact: true })).toBeVisible()
  await expect(dialog.getByText("Volatility")).toBeVisible()
  await expect(dialog.getByText("Language", { exact: true })).toBeVisible()
  await expect(dialog.getByRole("switch", { name: "Strict" })).toBeVisible()
  await expect(dialog.getByText("Data access")).toHaveCount(0)
  await expect(dialog.getByRole("switch", { name: "Deterministic" })).toHaveCount(0)

  // OUT parameters stand in for a return type: the form stops asking for one.
  await dialog.getByRole("button", { name: "Add parameter" }).click()
  const parameter = dialog.getByRole("group", { name: "Parameter 1" })
  await parameter.getByRole("combobox").click()
  await page.getByRole("option", { name: "OUT" }).click()
  await parameter.getByRole("textbox").nth(0).fill("total")
  await parameter.getByRole("textbox").nth(1).fill("numeric")
  await dialog.getByLabel("Name", { exact: true }).fill("sum_all")
  await expect(
    dialog.getByText(/CREATE FUNCTION "public"."sum_all"\(total numeric\)/),
  ).toBeVisible()
})

test("a MySQL procedure has the fields MySQL has", async ({ page }) => {
  await mockPrograms(page, {}, MYSQL)
  await page.goto("/databases/2/schema?new=procedure")
  const form = page.getByRole("dialog", { name: "New procedure" })
  await expect(form).toBeVisible()
  await expect(form.getByText("Data access")).toBeVisible()
  await expect(form.getByText("Volatility")).toHaveCount(0)
  await expect(form.getByLabel("Returns", { exact: true })).toHaveCount(0)
  await expect(form.getByText("Language", { exact: true })).toHaveCount(0)
  await form.getByRole("button", { name: "Add parameter" }).click()
  // MySQL parameters have no defaults; a procedure's can be OUT.
  const parameter = form.getByRole("group", { name: "Parameter 1" })
  await expect(parameter.getByRole("combobox")).toBeVisible()
  await expect(parameter.getByPlaceholder("no default")).toHaveCount(0)
})

test("a function is edited from its page: filled in, with the way it is replaced said", async ({
  page,
}) => {
  const { sent } = await mockPrograms(page)
  await page.goto(FUNCTION)
  await expect(page.getByRole("heading", { name: /order_count/ })).toBeVisible()
  await page.getByRole("button", { name: "Edit", exact: true }).click()

  const dialog = page.getByRole("dialog", { name: "Edit function" })
  await expect(dialog).toBeVisible()
  // It is the routine the server read, and it keeps its name.
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveCount(0)
  const parameter = dialog.getByRole("group", { name: "Parameter 1" })
  await expect(parameter.getByRole("textbox").nth(0)).toHaveValue("cid")
  await expect(parameter.getByRole("textbox").nth(1)).toHaveValue("bigint")
  await expect(dialog.getByLabel("Returns", { exact: true })).toHaveValue("bigint")
  await expect(dialog.getByText(/CREATE OR REPLACE FUNCTION "public"."order_count"/)).toBeVisible()
  await expect(dialog.getByText(/drops it and creates it again/)).toHaveCount(0)

  // A changed return type is the one thing OR REPLACE refuses, and the dialog says what happens instead.
  await dialog.getByLabel("Returns", { exact: true }).fill("integer")
  await expect(dialog.getByText(/drops it and creates it again in one transaction/)).toBeVisible()
  await dialog.getByRole("button", { name: "Save changes" }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)).toHaveLength(1)
  expect(runs(sent)[0].body).toMatchObject({
    kind: "function",
    schema: "public",
    name: "order_count",
    returns: "integer",
    replaces: { signature: "cid bigint" },
  })
})

test("an edit that is cancelled after a change asks before it discards", async ({ page }) => {
  const { sent } = await mockPrograms(page)
  await page.goto(FUNCTION)
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Edit function" })
  await expect(dialog.getByRole("button", { name: "Save changes" })).toBeEnabled()
  // Untouched, it closes at once.
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()

  await page.getByRole("button", { name: "Edit", exact: true }).click()
  await dialog.getByLabel("Returns", { exact: true }).fill("integer")
  await page.keyboard.press("Escape")
  await expect(dialog.getByText("Close and lose what you typed?")).toBeVisible()
  await dialog.getByRole("button", { name: "Keep editing" }).click()
  await expect(dialog.getByLabel("Returns", { exact: true })).toHaveValue("integer")
  expect(runs(sent)).toHaveLength(0)
})

test("a function the form cannot state is shown, and not offered for editing", async ({ page }) => {
  await mockPrograms(page)
  await page.goto(`${SCHEMA}?schema=public&object=function&name=legacy_total`)
  await expect(page.getByRole("heading", { name: /legacy_total/ })).toBeVisible()
  await expect(
    page.getByText("Not editable here: it sets COST 5. Change it in Query."),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toBeDisabled()
  // The text stays readable and the drop stays possible.
  await expect(page.getByRole("button", { name: "Open in Query" })).toBeEnabled()
  await expect(page.getByRole("button", { name: "Drop" })).toBeEnabled()
})

test("an engine's refusal stays in the form with the engine's words", async ({ page }) => {
  await mockPrograms(page, {
    runRefused: {
      status: 400,
      message: 'ERROR: function "invoice_total" already exists with same argument types',
    },
  })
  await page.goto(`${SCHEMA}?new=function`)
  const dialog = page.getByRole("dialog", { name: "New function" })
  await dialog.getByLabel("Name", { exact: true }).fill("invoice_total")
  await dialog.getByLabel("Returns", { exact: true }).fill("numeric")
  await writeBody(page, dialog, "SELECT 1;")
  await dialog.getByRole("button", { name: "Create function" }).click()
  await expect(
    dialog.getByText(/Nothing was changed\..*already exists with same argument types/),
  ).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(1)
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue("invoice_total")
})

/* ------------------------------------------------------------------ drops */

test("a drop is read once, confirmed by name and sent to the destructive route", async ({
  page,
}) => {
  const read = hold()
  const { sent } = await mockPrograms(page, { previewHeld: read.until })
  await page.goto(FUNCTION)
  await page.getByRole("button", { name: "Drop" }).click()
  await expect(page.getByText("Drop function: reading the statement…")).toBeVisible()
  read.release()
  const confirm = page.getByRole("dialog", { name: "Drop function" })
  await expect(confirm.getByText("public.order_count(cid bigint)")).toBeVisible()
  await expect(confirm.getByText('DROP FUNCTION "public"."order_count"(cid bigint)')).toBeVisible()
  expect(runs(sent)).toHaveLength(0)
  await confirm.getByRole("button", { name: "Drop function" }).click()
  await expect(confirm).toBeHidden()
  expect(runs(sent)).toHaveLength(1)
  expect(runs(sent)[0]).toMatchObject({
    method: "DELETE",
    path: "/ddl/routine",
    body: { kind: "function", schema: "public", name: "order_count", signature: "cid bigint" },
  })
  // The page it was on is gone: the schema is what is left.
  await expect(page).not.toHaveURL(/object=function/)
})

test("a trigger is dropped with its table named", async ({ page }) => {
  const { sent } = await mockPrograms(page)
  await page.goto(`${SCHEMA}?schema=public&object=trigger&name=orders_touch&on=orders`)
  await page.getByRole("button", { name: "Drop" }).click()
  const confirm = page.getByRole("dialog", { name: "Drop trigger" })
  await expect(confirm.getByText('DROP TRIGGER "orders_touch" ON "public"."orders"')).toBeVisible()
  await confirm.getByRole("button", { name: "Drop trigger" }).click()
  await expect(confirm).toBeHidden()
  expect(runs(sent)[0]).toMatchObject({
    method: "DELETE",
    path: "/ddl/trigger",
    body: { schema: "public", name: "orders_touch", table: "orders" },
  })
})

/* ---------------------------------------------------------------- triggers */

test("a trigger is made on a table from the table's own page, executing a function that returns trigger", async ({
  page,
}) => {
  const { sent } = await mockPrograms(page)
  await page.goto(`${SCHEMA}?schema=public&table=orders&view=triggers`)
  await page.getByRole("button", { name: "New trigger" }).click()
  const dialog = page.getByRole("dialog", { name: "New trigger" })
  await expect(dialog).toBeVisible()
  // It is on the table the page is, and says so rather than asking.
  await expect(dialog.getByText("orders", { exact: true })).toBeVisible()
  await dialog.getByLabel("Name", { exact: true }).fill("orders_audit")

  await dialog.getByRole("radio", { name: "Before" }).click()
  const events = dialog.getByRole("group", { name: "The events it fires on" })
  await expect(events.getByRole("button", { name: "Insert" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await events.getByRole("button", { name: "Update" }).click()
  await events.getByRole("button", { name: "Insert" }).click()
  await dialog.getByLabel("Only when", { exact: true }).fill("new.total > 0")
  await dialog.getByLabel("Executes").click()
  await page.getByRole("option", { name: "public.touch_row" }).click()

  await expect(dialog.getByText(/CREATE TRIGGER "orders_audit"/)).toBeVisible()
  await expect(dialog.getByText(/BEFORE UPDATE ON "public"."orders"/)).toBeVisible()
  await dialog.getByRole("button", { name: "Create trigger" }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)[0].body).toEqual({
    schema: "public",
    name: "orders_audit",
    table: "orders",
    timing: "before",
    events: ["update"],
    forEach: "row",
    condition: "new.total > 0",
    function: "touch_row",
    functionSchema: "public",
  })
})

test("a trigger is edited in place: it stays on its table and names the function it runs", async ({
  page,
}) => {
  const { sent } = await mockPrograms(page)
  await page.goto(`${SCHEMA}?schema=public&object=trigger&name=orders_touch&on=orders`)
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Edit trigger" })
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveCount(0)
  await expect(dialog.getByRole("radio", { name: "Before" })).toBeChecked()
  const events = dialog.getByRole("group", { name: "The events it fires on" })
  await expect(events.getByRole("button", { name: "Update" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await events.getByRole("button", { name: "Insert" }).click()
  await dialog.getByRole("button", { name: "Save changes" }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)[0].body).toMatchObject({
    name: "orders_touch",
    table: "orders",
    events: ["insert", "update"],
    function: "touch_row",
    replace: true,
  })
})

test("a Postgres schema with no function returning trigger says so", async ({ page }) => {
  await mockPrograms(page, { noTriggerFunction: true })
  await page.goto(`${SCHEMA}?new=trigger`)
  const dialog = page.getByRole("dialog", { name: "New trigger" })
  await expect(dialog.getByText("There is no function returning trigger yet.")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Create trigger" })).toBeDisabled()
})

test("a SQLite file takes triggers with a body of their own, and no functions", async ({
  page,
}) => {
  const { sent } = await mockPrograms(page, {}, SQLITE)
  await page.goto("/databases/7/schema")
  await page.getByRole("button", { name: "New", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: "New function" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "New procedure" })).toHaveCount(0)
  await page.getByRole("menuitem", { name: "New trigger" }).click()

  const dialog = page.getByRole("dialog", { name: "New trigger" })
  await expect(dialog.getByLabel("Executes")).toHaveCount(0)
  await expect(dialog.getByRole("group", { name: "Body" })).toBeVisible()
  await dialog.getByLabel("Name", { exact: true }).fill("orders_stamp")
  await dialog.getByRole("combobox").first().click()
  await page.getByRole("option", { name: "orders", exact: true }).click()
  await dialog.getByRole("radio", { name: "Update" }).click()
  await dialog.getByLabel("Only when these columns are written").fill("total, note")
  await writeBody(page, dialog, "UPDATE orders SET note = 'x' WHERE id = new.id;")
  await dialog.getByRole("button", { name: "Create trigger" }).click()
  await expect(dialog).toBeHidden()
  expect(runs(sent)[0].body).toEqual({
    schema: "main",
    name: "orders_stamp",
    table: "orders",
    timing: "after",
    events: ["update"],
    forEach: "row",
    updateOf: ["total", "note"],
    body: "UPDATE orders SET note = 'x' WHERE id = new.id;",
  })
})

test("a table with no trigger offers one, and says it has none", async ({ page }) => {
  await mockPrograms(page)
  await page.goto(`${SCHEMA}?schema=public&table=customers&view=triggers`)
  await expect(page.getByText("No trigger fires on this table.")).toBeVisible()
  await expect(page.getByRole("button", { name: "New trigger" })).toBeVisible()
})

/* ------------------------------------------------------------------- roles */

test("a role that may not run code is drawn none of the controls", async ({ page }) => {
  await mockPrograms(page, { capabilities: ["service.control"] })
  await page.goto(SCHEMA)
  await page.getByRole("button", { name: "New", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: "New view" })).toBeVisible()
  await expect(
    page.getByRole("menuitem", { name: /New (function|procedure|trigger)/ }),
  ).toHaveCount(0)
  await page.keyboard.press("Escape")

  await page.goto(FUNCTION)
  await expect(page.getByRole("heading", { name: /order_count/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "Open in Query" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Drop" })).toHaveCount(0)

  await page.goto(`${SCHEMA}?new=function`)
  await expect(page.getByRole("dialog", { name: "New function" })).toHaveCount(0)
})

test("a read-only account reads a program and changes none", async ({ page }) => {
  const { sent } = await mockPrograms(page, { capabilities: [] })
  await page.goto(`${SCHEMA}?schema=public&object=trigger&name=orders_touch&on=orders`)
  await expect(page.getByRole("heading", { name: /orders_touch/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Drop" })).toHaveCount(0)
  expect(sent.programs).toHaveLength(0)
})

test("a program that could not be read for editing says so and leaves the page readable", async ({
  page,
}) => {
  await mockPrograms(page, { programDown: true })
  await page.goto(FUNCTION)
  await expect(page.getByRole("heading", { name: /order_count/ })).toBeVisible()
  await expect(
    page.getByText(/could not be read for editing: the server did not answer/),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Open in Query" })).toBeEnabled()
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0)
})

/* ------------------------------------------------------------------ phones */

test("the forms and the object page fit a phone with no sideways scroll", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockPrograms(page)
  const fits = () =>
    page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    )

  await page.goto(FUNCTION)
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toBeVisible()
  expect(await fits()).toBe(true)

  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Edit function" })
  await expect(dialog).toBeVisible()
  await settle(dialog)
  expect(await fits()).toBe(true)
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  await page.keyboard.press("Escape")

  await page.goto(`${SCHEMA}?schema=public&table=orders&view=triggers`)
  await page.getByRole("button", { name: "New trigger" }).click()
  const trigger = page.getByRole("dialog", { name: "New trigger" })
  await expect(trigger).toBeVisible()
  await settle(trigger)
  expect(await fits()).toBe(true)
  expect(await trigger.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
})

/* ------------------------------------------------------------------ shots */

test("screenshots of the stored-program forms @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to take them")
  const out = process.env.JD_SHOTS as string
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockPrograms(page)
  const shot = async (name: string) => {
    await page.screenshot({ path: `${out}/${name}.png` })
  }

  // The object page with its controls, then one the form cannot state.
  await page.goto(FUNCTION)
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toBeEnabled()
  await expect(page.getByText("SELECT 1")).toBeVisible()
  await shot("function-page-1440")
  await page.goto(`${SCHEMA}?schema=public&object=function&name=legacy_total`)
  await expect(
    page.getByText("Not editable here: it sets COST 5. Change it in Query."),
  ).toBeVisible()
  await shot("function-not-editable-1440")

  // A new function, filled in, with the server's statement under it.
  await page.goto(`${SCHEMA}?new=function`)
  let dialog = page.getByRole("dialog", { name: "New function" })
  await dialog.getByLabel("Name", { exact: true }).fill("invoice_total")
  await dialog.getByRole("button", { name: "Add parameter" }).click()
  const parameter = dialog.getByRole("group", { name: "Parameter 1" })
  await parameter.getByRole("textbox").nth(0).fill("cid")
  await parameter.getByRole("textbox").nth(1).fill("bigint")
  await dialog.getByLabel("Returns", { exact: true }).fill("numeric")
  await writeBody(
    page,
    dialog,
    "BEGIN\n  RETURN (SELECT sum(total) FROM orders WHERE customer_id = cid);\nEND;",
  )
  await expect(dialog.getByText(/CREATE FUNCTION "public"."invoice_total"/)).toBeVisible()
  await settle(dialog)
  await shot("function-new-1440")
  await page.setViewportSize({ width: 390, height: 844 })
  await settle(dialog)
  await shot("function-new-390")
  await page.setViewportSize({ width: 1440, height: 900 })

  // An edit that changes the return type says how the engine replaces it.
  await page.goto(FUNCTION)
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  dialog = page.getByRole("dialog", { name: "Edit function" })
  await dialog.getByLabel("Returns", { exact: true }).fill("integer")
  await expect(dialog.getByText(/drops it and creates it again in one transaction/)).toBeVisible()
  await settle(dialog)
  await shot("function-edit-1440")

  // A trigger, made from its table.
  await page.goto(`${SCHEMA}?schema=public&table=orders&view=triggers`)
  await page.getByRole("button", { name: "New trigger" }).click()
  dialog = page.getByRole("dialog", { name: "New trigger" })
  await dialog.getByLabel("Name", { exact: true }).fill("orders_audit")
  await dialog.getByRole("radio", { name: "Before" }).click()
  await dialog
    .getByRole("group", { name: "The events it fires on" })
    .getByRole("button", { name: "Update" })
    .click()
  await dialog.getByLabel("Executes").click()
  await page.getByRole("option", { name: "public.touch_row" }).click()
  await expect(dialog.getByText(/CREATE TRIGGER "orders_audit"/)).toBeVisible()
  await settle(dialog)
  await shot("trigger-new-1440")

  // A drop, named and with its statement.
  await page.goto(FUNCTION)
  await page.getByRole("button", { name: "Drop" }).click()
  const confirm = page.getByRole("dialog", { name: "Drop function" })
  await expect(confirm.getByText('DROP FUNCTION "public"."order_count"(cid bigint)')).toBeVisible()
  await settle(confirm)
  await shot("function-drop-1440")
})
