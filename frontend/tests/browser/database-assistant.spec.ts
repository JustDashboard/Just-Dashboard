import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { hold, mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * The SQL editor's assistant (`/databases/<id>/query`, its pane beside the
 * editor): setting a provider up, a question and its streamed answer, the
 * statement it writes opened in a tab or written at the cursor, a result
 * attached only when the reader attaches it, a request to read tables that
 * waits for Allow, the error and the plan explained from where they are
 * shown, and the states where there is no assistant to ask — a role that
 * only reads, a protected connection, a provider that failed.
 *
 * The API is mocked in the browser, the streamed answer included; what the
 * requests carry is what is asserted. Nothing here reaches a model.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

const MODELS = [
  { id: "claude-opus-5-5", label: "Claude Opus 5.5" },
  { id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
  { id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
]
const OFF = { configured: false, keySet: false, models: MODELS }
const ON = {
  configured: true,
  provider: "anthropic",
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  keySet: true,
  updatedAt: "2026-10-16T09:00:00Z",
  updatedBy: "operator",
  models: MODELS,
}
const LOCAL = {
  configured: true,
  provider: "openai",
  model: "qwen2.5-coder:14b",
  modelLabel: "qwen2.5-coder:14b",
  baseUrl: "http://127.0.0.1:11434/v1",
  keySet: false,
  updatedAt: "2026-10-16T09:00:00Z",
  updatedBy: "operator",
  models: MODELS,
}

const TABLES: Record<string, string[]> = {
  customers: ["id", "email", "tier", "created_at"],
  orders: ["id", "customer_id", "status", "total_cents", "created_at"],
  order_items: ["id", "order_id", "sku", "quantity"],
}

const start = (conversation = "c0ffee") => ({
  type: "start",
  conversation,
  provider: "anthropic",
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  context: { schema: "public", described: 3, named: 0, omitted: 0 },
})
const usage = { input: 2140, output: 186, cacheRead: 1890 }
const done = (extra: Record<string, unknown> = {}) => ({
  type: "done",
  stop: "end",
  usage,
  model: "claude-opus-5-5",
  kept: true,
  ...extra,
})
const text = (value: string) => ({ type: "text", text: value })

const COUNT_ANSWER = [
  start(),
  { type: "thinking" },
  text("Count each customer's orders, newest customers first:\n\n```sql\n"),
  text(
    "SELECT c.email, count(o.id) AS orders\nFROM customers c\nLEFT JOIN orders o ON o.customer_id = c.id\nGROUP BY c.email\nORDER BY orders DESC;\n```\n\n",
  ),
  text("A `LEFT JOIN` keeps customers who have **no orders yet**, with a count of 0."),
  done(),
]

type Answer =
  | { lines: object[] }
  | { status: number; code: string; message: string }
  | { held: Promise<unknown>; lines: object[] }

type AssistantMock = DatabaseMock & {
  settings?: object
  answers?: Answer[]
  /** What `PUT /databases/assistant` answers, in order; then success. */
  refusals?: { field: string; message: string }[]
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/** The editor's API and the assistant's, for connection 1 (PostgreSQL). */
async function mockAssistant(page: Page, options: AssistantMock = {}) {
  await mockDatabases(page, options)
  const sent = {
    asked: [] as Record<string, unknown>[],
    setups: [] as Record<string, unknown>[],
    removed: 0,
  }
  let settings = options.settings ?? OFF
  const answers = [...(options.answers ?? [])]
  const refusals = [...(options.refusals ?? [])]

  await page.route("**/api/v1/databases/assistant", async (route) => {
    const request = route.request()
    if (request.method() === "PUT") {
      const body = request.postDataJSON() as Record<string, unknown>
      sent.setups.push(body)
      const refusal = refusals.shift()
      if (refusal) {
        return json(
          route,
          {
            error: {
              code: "assistant_check_failed",
              message: refusal.message,
              field: refusal.field,
            },
          },
          400,
        )
      }
      settings =
        body.provider === "openai"
          ? { ...LOCAL, model: body.model, modelLabel: body.model, baseUrl: body.baseUrl }
          : { ...ON, model: body.model, modelLabel: MODELS.find((m) => m.id === body.model)?.label }
      return json(route, settings)
    }
    if (request.method() === "DELETE") {
      sent.removed++
      settings = OFF
      return json(route, settings)
    }
    return json(route, settings)
  })

  await page.route(/\/api\/v1\/databases\/1\/.+/, async (route) => {
    const request = route.request()
    const rest = new URL(request.url()).pathname.replace(/^\/api\/v1\/databases\/1/, "")
    const body = () => (request.postDataJSON() ?? {}) as Record<string, unknown>
    if (rest === "/catalog") {
      return json(route, {
        schema: "public",
        defaultSchema: "public",
        schemas: [{ name: "public", default: true, tables: 3 }],
        objects: {},
        truncated: [],
        limit: 5000,
      })
    }
    if (rest === "/outline") {
      const entries = Object.keys(TABLES).map((name) => ({
        id: `public.${name}`,
        schema: "public",
        name,
        type: "table",
      }))
      return json(route, {
        schema: "public",
        tables: Object.fromEntries(entries.map((entry) => [entry.id, TABLES[entry.name]])),
        entries,
        truncated: false,
        total: entries.length,
        limit: 5000,
      })
    }
    if (rest === "/relations") return json(route, {})
    if (rest === "/classify") {
      const sql = String(body().query ?? "")
      const risk = { destructive: false, level: "read", reasons: [] }
      return json(route, { ...risk, statements: [{ sql, line: 1, risk }] })
    }
    if (rest === "/query") {
      const sql = String(body().query ?? "")
      if (/nope/.test(sql)) {
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
      return json(route, {
        result: {
          columns: ["id", "email", "tier"],
          types: ["INT8", "TEXT", "TEXT"],
          kinds: ["integer", "text", "text"],
          rows: [
            ["1", "ann@example.com", "pro"],
            ["2", "bo@example.com", null],
            ["3", "cy@example.com", "free"],
          ],
          rowCount: 3,
          rowsAffected: 0,
          duration: "1.5ms",
          truncated: false,
          statement: sql,
        },
        risk: { destructive: false, level: "read", reasons: [] },
      })
    }
    if (rest === "/explain") {
      const plan = [
        {
          Plan: {
            "Node Type": "Seq Scan",
            "Relation Name": "orders",
            "Total Cost": 431.5,
            "Plan Rows": 18000,
            Filter: "(status = 'shipped'::text)",
          },
        },
      ]
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
          statement: `EXPLAIN ${body().query}`,
        },
        plan,
        format: "json",
        analyzed: false,
        rolledBack: false,
      })
    }
    if (rest === "/assistant") {
      sent.asked.push(body())
      const answer = answers.shift() ?? { lines: COUNT_ANSWER }
      if ("held" in answer) await answer.held
      if ("status" in answer) {
        return json(route, { error: { code: answer.code, message: answer.message } }, answer.status)
      }
      // The whole stream at once: the page reads it line by line either way.
      return route
        .fulfill({
          status: 200,
          contentType: "application/x-ndjson",
          body: answer.lines.map((line) => JSON.stringify(line)).join("\n") + "\n",
        })
        .catch(() => {
          // The page stopped the answer before it came.
        })
    }
    return route.fallback()
  })
  return sent
}

const editor = (page: Page) => page.locator("[data-slot=sql-editor] .monaco-editor").first()
const pane = (page: Page) => page.locator("[data-slot=query-assistant]")
const toggle = (page: Page) => page.getByRole("button", { name: "Assistant", exact: true })
const question = (page: Page) => pane(page).getByRole("textbox", { name: "Ask the assistant" })
const results = (page: Page) => page.locator("[data-slot=query-results]")
const openTabs = (page: Page) =>
  page.getByRole("tablist", { name: "Open statements" }).getByRole("tab")
const editorText = (page: Page) =>
  page.locator("[data-slot=sql-editor] .view-lines").evaluate((el) =>
    [...el.querySelectorAll(".view-line")]
      .sort(
        (a, b) =>
          parseFloat((a as HTMLElement).style.top) - parseFloat((b as HTMLElement).style.top),
      )
      .map((line) => (line.textContent ?? "").replace(/\u00a0/g, " "))
      .join("\n"),
  )

async function openEditor(page: Page) {
  await page.goto("/databases/1/query")
  await expect(editor(page)).toBeVisible({ timeout: 45_000 })
}

/** Replaces the editor's text, as the query spec does, and leaves the keyboard in it. */
async function write(page: Page, sql: string) {
  await editor(page).click()
  await page.evaluate((value) => {
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
    held.setValue(value)
    held.setPosition(held.getModel().getFullModelRange().getEndPosition())
    held.focus()
  }, sql)
}

async function openAssistant(page: Page) {
  await toggle(page).click()
  await expect(pane(page)).toBeVisible()
}

async function ask(page: Page, words: string) {
  await question(page).fill(words)
  await question(page).press("Enter")
}

/* ------------------------------------------------------------- setting up */

test("the assistant is off until an administrator sets a provider up", async ({ page }) => {
  const sent = await mockAssistant(page, {
    refusals: [{ field: "key", message: "Anthropic did not accept the key" }],
  })
  await openEditor(page)
  await openAssistant(page)
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "true")
  await expect(pane(page)).toContainText("The assistant is off")

  await pane(page).getByRole("button", { name: "Set up the assistant" }).click()
  const sheet = page.getByRole("dialog", { name: "Set up the assistant" })
  await expect(sheet).toBeVisible()
  await expect(sheet).toContainText("What the provider is sent")
  // Claude's newest Opus is where the form starts.
  await expect(sheet.getByRole("radio", { name: "Opus 5.5" })).toHaveAttribute(
    "aria-checked",
    "true",
  )
  await sheet.getByRole("radio", { name: "Sonnet 5.5" }).click()
  await sheet.getByLabel("API key", { exact: true }).fill("sk-ant-api03-wrong")
  await sheet.getByRole("button", { name: "Save" }).click()
  // The provider's refusal is said under the field it was typed in.
  await expect(sheet.getByText("Anthropic did not accept the key")).toBeVisible()
  await expect(sheet.getByLabel("API key", { exact: true })).toHaveAttribute("aria-invalid", "true")

  await sheet.getByLabel("API key", { exact: true }).fill("sk-ant-api03-right")
  await sheet.getByRole("button", { name: "Save" }).click()
  await expect(sheet).toBeHidden()
  expect(sent.setups.at(-1)).toEqual({
    provider: "anthropic",
    model: "claude-sonnet-5-5",
    baseUrl: "",
    key: "sk-ant-api03-right",
  })
  await expect(pane(page)).toContainText("Claude Sonnet 5.5")
  await expect(question(page)).toBeVisible()
  // The composer carries no standing note about what is sent; only attached rows get one.
  await expect(pane(page)).not.toContainText("Sends Anthropic")
})

test("a server on this machine is set up by its address and model", async ({ page }) => {
  const sent = await mockAssistant(page)
  await openEditor(page)
  await openAssistant(page)
  await pane(page).getByRole("button", { name: "Set up the assistant" }).click()
  const sheet = page.getByRole("dialog", { name: "Set up the assistant" })
  await sheet.getByRole("button", { name: /^OpenAI-compatible/ }).click()
  await sheet.getByLabel("Address", { exact: true }).fill("http://127.0.0.1:11434/v1")
  await sheet.getByLabel("Model", { exact: true }).fill("qwen2.5-coder:14b")
  await sheet.getByRole("button", { name: "Save" }).click()
  await expect(sheet).toBeHidden()
  expect(sent.setups.at(-1)).toEqual({
    provider: "openai",
    model: "qwen2.5-coder:14b",
    baseUrl: "http://127.0.0.1:11434/v1",
    key: "",
  })
  await expect(pane(page)).not.toContainText("Sends 127.0.0.1:11434")
})

test("turning the assistant off asks first and forgets the provider", async ({ page }) => {
  const sent = await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await openAssistant(page)
  await pane(page).getByRole("button", { name: "Assistant settings" }).click()
  const sheet = page.getByRole("dialog", { name: "Assistant settings" })
  await sheet.getByRole("button", { name: "Turn off the assistant" }).click()
  const confirm = page.getByRole("dialog", { name: "Turn off the assistant" })
  await expect(confirm).toContainText("Claude Opus 5.5")
  await confirm.getByRole("button", { name: "Turn off" }).click()
  await expect(pane(page)).toContainText("The assistant is off")
  expect(sent.removed).toBe(1)
})

/* ------------------------------------------------------------- questions */

test("a question streams an answer whose statement opens in a tab of its own", async ({ page }) => {
  const sent = await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await write(page, "select * from customers")
  await openAssistant(page)
  await ask(page, "How many orders has each customer placed?")

  const statement = pane(page).locator("[data-slot=assistant-statement]")
  await expect(statement).toContainText("LEFT JOIN orders o ON o.customer_id = c.id")
  await expect(pane(page)).toContainText("keeps customers who have no orders yet")
  await expect(pane(page)).toContainText("Claude Opus 5.5 · 2,140 tokens in · 186 out")
  // Nothing was attached that the reader did not attach.
  expect(sent.asked[0]).toEqual({
    conversation: "",
    schema: "public",
    task: { kind: "sql", prompt: "How many orders has each customer placed?" },
  })

  const before = await openTabs(page).count()
  await statement.getByRole("button", { name: "Open in a tab" }).click()
  await expect(openTabs(page)).toHaveCount(before + 1)
  await expect(
    page.getByRole("tablist", { name: "Open statements" }).getByRole("tab", { selected: true }),
  ).toContainText("How many orders")
  expect(await editorText(page)).toContain("GROUP BY c.email")
  // The statement is the reader's to run: nothing was sent to the query route.
  await expect(results(page)).toContainText("Nothing has been run in this tab")

  // The next question carries the conversation on.
  await ask(page, "Only customers on the pro tier")
  await expect(pane(page).locator("[data-slot=assistant-statement]")).toHaveCount(2)
  expect(sent.asked[1]).toMatchObject({ conversation: "c0ffee", task: { kind: "sql" } })
})

test("a statement from an answer is written at the cursor", async ({ page }) => {
  await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await write(page, "-- counts\n")
  await openAssistant(page)
  await ask(page, "Count the orders per customer")
  await pane(page)
    .locator("[data-slot=assistant-statement]")
    .getByRole("button", { name: "Insert" })
    .click()
  await expect.poll(() => editorText(page)).toContain("-- counts")
  expect(await editorText(page)).toContain("SELECT c.email, count(o.id) AS orders")
})

test("a result reaches the assistant only when it is attached, and the composer says so", async ({
  page,
}) => {
  const sent = await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await write(page, "select * from customers")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page)).toContainText("3 rows")
  await openAssistant(page)

  await pane(page).getByRole("button", { name: "Attach the statement in the editor" }).click()
  const result = pane(page).getByRole("button", { name: "Attach the first 3 rows of the result" })
  await result.click()
  await expect(result).toHaveAttribute("aria-pressed", "true")
  await expect(pane(page)).toContainText("Sends Anthropic the question and the attached rows")
  await ask(page, "Why is bo's tier empty?")
  await expect(pane(page).getByLabel("Attached")).toContainText("result · 3 rows")
  const task = sent.asked[0].task as Record<string, unknown>
  expect(task.statement).toBe("select * from customers")
  expect(task.result).toEqual({
    columns: ["id", "email", "tier"],
    rows: [
      ["1", "ann@example.com", "pro"],
      ["2", "bo@example.com", null],
      ["3", "cy@example.com", "free"],
    ],
    total: 3,
  })
  // The attachment is for one question: the next goes without it.
  await expect(result).toHaveAttribute("aria-pressed", "false")
  await ask(page, "And how do I fix it?")
  await expect.poll(() => sent.asked.length).toBe(2)
  expect((sent.asked[1].task as Record<string, unknown>).result).toBeUndefined()
})

test("a request to read more of the structure waits for the reader's Allow", async ({ page }) => {
  const sent = await mockAssistant(page, {
    settings: ON,
    answers: [
      {
        lines: [
          start(),
          text("I need the columns of `order_items` to count what was sold."),
          done({ stop: "tool", pending: [{ id: "toolu_01", tables: ["order_items"] }] }),
        ],
      },
      {
        lines: [
          start(),
          { type: "described", calls: [{ tables: ["order_items"], missing: [] }] },
          text("```sql\nSELECT sku, sum(quantity) FROM order_items GROUP BY sku;\n```"),
          done(),
        ],
      },
    ],
  })
  await openEditor(page)
  await openAssistant(page)
  await ask(page, "Which products sell best?")
  const card = pane(page).locator("[data-slot=assistant-ask]")
  await expect(card).toContainText("It asks to read the structure of order_items")
  await expect(card).toContainText("no rows")
  // Nothing was read before the reader answered.
  expect(sent.asked).toHaveLength(1)

  await card.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(card).toContainText("Allowed")
  await expect(pane(page)).toContainText("Read the structure of order_items.")
  await expect(pane(page).locator("[data-slot=assistant-statement]")).toContainText("sum(quantity)")
  expect(sent.asked[1]).toEqual({ conversation: "c0ffee", allow: true })
})

test("declining the request lets the answer go on without it", async ({ page }) => {
  const sent = await mockAssistant(page, {
    settings: ON,
    answers: [
      {
        lines: [
          start(),
          text("I need the columns of `order_items`."),
          done({ stop: "tool", pending: [{ id: "toolu_01", tables: ["order_items"] }] }),
        ],
      },
      { lines: [start(), text("Without its columns I can only guess the name."), done()] },
    ],
  })
  await openEditor(page)
  await openAssistant(page)
  await ask(page, "Which products sell best?")
  const card = pane(page).locator("[data-slot=assistant-ask]")
  await card.getByRole("button", { name: /Don.t allow/ }).click()
  await expect(card).toContainText("Not allowed")
  await expect(pane(page)).toContainText("Without its columns")
  expect(sent.asked[1]).toEqual({ conversation: "c0ffee", allow: false })
})

test("a failed statement and a plan are explained from where they are shown", async ({ page }) => {
  const sent = await mockAssistant(page, {
    settings: ON,
    answers: [
      { lines: [start(), text("There is no table called `nope` in public."), done()] },
      {
        lines: [start(), text("It reads every row of `orders` to find the shipped ones."), done()],
      },
    ],
  })
  await openEditor(page)
  await write(page, "select * from nope")
  await page.keyboard.press("ControlOrMeta+Enter")
  const failure = results(page).locator("[data-slot=query-refusal]")
  await expect(failure).toContainText("does not exist")
  await failure.getByRole("button", { name: "Ask the assistant" }).click()
  await expect(pane(page)).toContainText("Explain why this statement failed")
  await expect(pane(page)).toContainText("There is no table called nope")
  expect(sent.asked[0].task).toEqual({
    kind: "error",
    prompt: "",
    statement: "select * from nope",
    error: 'ERROR: relation "nope" does not exist (SQLSTATE 42P01)',
  })

  await write(page, "select * from orders where status = 'shipped'")
  await page
    .locator("[data-slot=query-commands]")
    .getByRole("button", { name: "Explain", exact: true })
    .click()
  await expect(results(page).locator("[data-slot=plan-tree]")).toBeVisible()
  await results(page).getByRole("button", { name: "Ask the assistant" }).click()
  await expect(pane(page)).toContainText("reads every row of orders")
  const plan = sent.asked[1].task as Record<string, string>
  expect(plan.kind).toBe("plan")
  expect(plan.statement).toBe("select * from orders where status = 'shipped'")
  expect(plan.plan).toContain('"Node Type": "Seq Scan"')
  // The plan is asked about in the same conversation.
  expect(sent.asked[1].conversation).toBe("c0ffee")
})

test("a row-level security policy is drafted for a table of the schema", async ({ page }) => {
  const sent = await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await openAssistant(page)
  await pane(page).getByRole("button", { name: "Draft a row-level security policy" }).click()
  await pane(page).getByRole("combobox", { name: "Policy on" }).click()
  await page.getByRole("option", { name: "orders" }).click()
  const who = pane(page).getByRole("textbox", { name: "Who may see or change which rows" })
  await who.fill("Each customer sees only their own orders")
  await pane(page).getByRole("button", { name: "Draft", exact: true }).click()
  await expect.poll(() => sent.asked.length).toBe(1)
  expect(sent.asked[0].task).toEqual({
    kind: "policy",
    prompt: "Each customer sees only their own orders",
    table: "orders",
  })
  await expect(pane(page)).toContainText("Draft a row-level security policy on orders")
})

test("an answer that fails, and one the reader stops, say so and keep nothing", async ({
  page,
}) => {
  const slow = hold()
  await mockAssistant(page, {
    settings: ON,
    answers: [
      {
        status: 502,
        code: "assistant_failed",
        message: "Anthropic is not answering right now (529)",
      },
      { held: slow.until, lines: COUNT_ANSWER },
    ],
  })
  await openEditor(page)
  await openAssistant(page)
  await ask(page, "How many orders?")
  await expect(pane(page)).toContainText("No answer")
  await expect(pane(page)).toContainText("Anthropic is not answering right now (529)")

  await ask(page, "How many orders, again?")
  await expect(pane(page).getByRole("status")).toContainText("Asking…")
  await pane(page).getByRole("button", { name: "Stop" }).click()
  await expect(pane(page)).toContainText("Stopped. Nothing of this answer is kept.")
  await expect(pane(page).getByRole("button", { name: "Ask", exact: true })).toBeVisible()
  slow.release()
})

/* ------------------------------------------------------- who has it, where */

test("a role that only reads is offered no assistant", async ({ page }) => {
  await mockAssistant(page, { settings: ON, viewer: true })
  await openEditor(page)
  await expect(page.getByRole("tablist", { name: "Open statements" })).toBeVisible()
  await expect(toggle(page)).toHaveCount(0)
  await expect(pane(page)).toHaveCount(0)
})

test("a protected connection is never sent to a model", async ({ page }) => {
  const sent = await mockAssistant(page, { settings: ON, rows: { 1: { readOnly: true } } })
  await openEditor(page)
  await openAssistant(page)
  await expect(pane(page)).toContainText("Off for a protected connection")
  await expect(question(page)).toHaveCount(0)
  expect(sent.asked).toHaveLength(0)
})

test("at 390 px the assistant lies over the editor and never scrolls sideways", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await openAssistant(page)
  await ask(page, "How many orders has each customer placed?")
  await expect(pane(page).locator("[data-slot=assistant-statement]")).toBeVisible()
  const sideways = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(sideways).toBeLessThanOrEqual(0)
  // Opening the statement goes back to the editor it opened in.
  await pane(page).getByRole("button", { name: "Open in a tab" }).click()
  await expect(pane(page)).toHaveCount(0)
  expect(await editorText(page)).toContain("GROUP BY c.email")
})

/* -------------------------------------------------------------- pictures */

test("the assistant's states, for the pull request @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go in")
  const shots = process.env.JD_SHOTS
  await page.setViewportSize({ width: 1440, height: 900 })
  const shot = (name: string) => page.screenshot({ path: `${shots}/${name}` })

  await mockAssistant(page, {
    answers: [
      { lines: COUNT_ANSWER },
      {
        lines: [
          start(),
          text("To split the counts by what was sold I need the columns of `order_items`."),
          done({ stop: "tool", pending: [{ id: "toolu_01", tables: ["order_items"] }] }),
        ],
      },
    ],
  })
  await openEditor(page)
  await write(page, "select * from customers")
  await openAssistant(page)
  await expect(pane(page)).toContainText("The assistant is off")
  await shot("assistant-off-1440.png")

  await pane(page).getByRole("button", { name: "Set up the assistant" }).click()
  const sheet = page.getByRole("dialog", { name: "Set up the assistant" })
  await sheet.getByLabel("API key", { exact: true }).fill("sk-ant-api03-example")
  await shot("assistant-settings-1440.png")
  await sheet.getByRole("button", { name: "Save" }).click()
  await expect(sheet).toBeHidden()

  await ask(page, "How many orders has each customer placed?")
  await expect(pane(page)).toContainText("tokens in")
  await shot("assistant-answer-1440.png")

  // On a phone the pane lies over the editor, opened from the same control.
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(pane(page)).toHaveCount(0)
  await openAssistant(page)
  await shot("assistant-answer-390.png")
  await pane(page).getByRole("button", { name: "Hide the assistant" }).click()

  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(pane(page)).toBeVisible()
  await ask(page, "Split it by product")
  await expect(pane(page).locator("[data-slot=assistant-ask]")).toBeVisible()
  await shot("assistant-ask-1440.png")
})

test("an error explained, for the pull request @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go in")
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockAssistant(page, {
    settings: ON,
    answers: [
      {
        lines: [
          start(),
          text(
            "PostgreSQL could not find a table called `nope` in the `public` schema. The tables here are `customers`, `orders` and `order_items`; if you meant orders:\n\n```sql\nSELECT * FROM orders;\n```",
          ),
          done(),
        ],
      },
    ],
  })
  await openEditor(page)
  await write(page, "select * from nope")
  await page.keyboard.press("ControlOrMeta+Enter")
  await results(page).getByRole("button", { name: "Ask the assistant" }).click()
  await expect(pane(page)).toContainText("could not find a table")
  await page.screenshot({ path: `${process.env.JD_SHOTS}/assistant-error-1440.png` })
})
