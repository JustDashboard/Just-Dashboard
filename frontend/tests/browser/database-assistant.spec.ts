import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { hold, mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A database's assistant: the connection's own Assistant page beside its Home,
 * and the same chat as the SQL editor's third column — a question streamed into
 * the conversation, its statement opened in a tab or written at the cursor, what
 * the tab holds attached only by the reader's press, the error and the plan
 * asked about from where they are shown, a command on the host waiting with the
 * command itself, a saved chat read back, Ask from another page, and the states
 * where there is no assistant to ask — a role that only reads, a protected
 * connection, a provider that failed.
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

const PRESETS = [
  {
    id: "anthropic",
    label: "Anthropic",
    detail: "Claude, with your API key",
    product: "claude",
    provider: "anthropic",
    keyRequired: true,
  },
]

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
  tools: true,
  mode: "changes",
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

/** A command it wants to run on the host, as the server streams the call. */
const DU = {
  id: "r1",
  tool: "run_command",
  label: "Run du -sh /var/lib/postgresql",
  kind: "change",
  reason: "To see how much of the disk the data directory takes.",
  input: { command: "du -sh /var/lib/postgresql/16/main", timeout: 30 },
}

type Answer =
  | { lines: object[] }
  | { status: number; code: string; message: string }
  | { held: Promise<unknown>; lines: object[] }

interface ChatSummary {
  id: string
  title: string
  scope: Record<string, unknown>
  createdAt: string
  updatedAt: string
  provider: string
  model: string
  running: boolean
  waiting: boolean
  steps: number
  changes: number
}

type AssistantMock = DatabaseMock & {
  settings?: object
  answers?: Answer[]
  chats?: ChatSummary[]
  lines?: Record<string, unknown[]>
}

const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * The editor's API and the assistant's: one answer a press, in order, on any
 * connection's or the server's route, with every body sent and the path it went to.
 */
async function mockAssistant(page: Page, options: AssistantMock = {}) {
  await mockDatabases(page, options)
  const sent = {
    asked: [] as Record<string, unknown>[],
    paths: [] as string[],
    setups: [] as Record<string, unknown>[],
    listed: [] as string[],
  }
  let settings = options.settings ?? OFF
  const answers = [...(options.answers ?? [])]
  const chats = [...(options.chats ?? [])]

  const view = () => {
    const current = settings as { configured?: boolean } & Record<string, unknown>
    const preset = current.configured ? "anthropic" : undefined
    return {
      ...current,
      preset,
      tools: current.configured === true,
      presets: PRESETS,
      defaultMode: "changes",
      profiles: preset
        ? [
            {
              preset,
              provider: current.provider,
              model: current.model,
              modelLabel: current.modelLabel,
              keySet: current.keySet,
              toolCalls: true,
              active: true,
            },
          ]
        : [],
    }
  }
  await page.route("**/api/v1/assistant", (route) => json(route, view()))
  await page.route("**/api/v1/assistant/models", (route) => json(route, { models: MODELS }))
  await page.route("**/api/v1/assistant/providers/*", async (route) => {
    const request = route.request()
    const preset = new URL(request.url()).pathname.split("/").at(-1)
    if (request.method() === "PUT") {
      const body: Record<string, unknown> = { preset, ...request.postDataJSON() }
      sent.setups.push(body)
      settings = {
        ...ON,
        model: body.model,
        modelLabel: MODELS.find((m) => m.id === body.model)?.label,
      }
    }
    return json(route, view())
  })
  await page.route(/\/api\/v1\/assistant\/chats(\/[^/?]+)?(\?.*)?$/, async (route) => {
    const url = new URL(route.request().url())
    const id = decodeURIComponent(url.pathname.split("/chats/")[1] ?? "")
    if (!id) {
      sent.listed.push(url.search)
      const database = url.searchParams.get("database")
      return json(route, {
        chats: chats.filter((chat) => !database || chat.scope.database === Number(database)),
      })
    }
    const chat = chats.find((one) => one.id === id)
    if (!chat) return json(route, { error: { code: "not_found", message: "No such chat" } }, 404)
    return json(route, { chat, lines: options.lines?.[id] ?? [] })
  })

  const answer = async (route: Route) => {
    const request = route.request()
    sent.asked.push((request.postDataJSON() ?? {}) as Record<string, unknown>)
    sent.paths.push(new URL(request.url()).pathname.replace(/^\/api\/v1/, ""))
    const next = answers.shift() ?? { lines: COUNT_ANSWER }
    if ("held" in next) await next.held
    if ("status" in next) {
      return json(route, { error: { code: next.code, message: next.message } }, next.status)
    }
    // The whole stream at once: the page reads it line by line either way.
    return route
      .fulfill({
        status: 200,
        contentType: "application/x-ndjson",
        body: next.lines.map((line) => JSON.stringify(line)).join("\n") + "\n",
      })
      .catch(() => {
        // The page stopped the answer before it came.
      })
  }
  await page.route(/\/api\/v1\/assistant\/chat(\?.*)?$/, answer)

  await page.route(/\/api\/v1\/databases\/\d+\/.+/, async (route) => {
    const request = route.request()
    const rest = new URL(request.url()).pathname.replace(/^\/api\/v1\/databases\/\d+/, "")
    const body = () => (request.postDataJSON() ?? {}) as Record<string, unknown>
    if (rest === "/assistant") return answer(route)
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
    return route.fallback()
  })
  return sent
}

const editor = (page: Page) => page.locator("[data-slot=sql-editor] .monaco-editor").first()
const pane = (page: Page) => page.locator("[data-slot=query-assistant]")
const toggle = (page: Page) => page.getByRole("button", { name: "Assistant", exact: true })
const question = (scope: ReturnType<Page["locator"]>) =>
  scope.getByRole("textbox", { name: "Ask the assistant" })
const results = (page: Page) => page.locator("[data-slot=query-results]")
const chat = (page: Page) => page.locator("[data-slot=agent-chat]")
const statements = (page: Page) => pane(page).locator("[data-slot=assistant-statement]")
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

async function openEditor(page: Page, id = 1) {
  await page.goto(`/databases/${id}/query`)
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

async function ask(scope: ReturnType<Page["locator"]>, words: string) {
  await question(scope).fill(words)
  await scope.getByRole("button", { name: "Ask", exact: true }).click()
}

/* ------------------------------------------------------------- setting up */

test("the editor's column is off until an administrator sets a provider up", async ({ page }) => {
  const sent = await mockAssistant(page)
  await openEditor(page)
  await openAssistant(page)
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "true")
  await expect(pane(page)).toContainText("The assistant is off")

  await pane(page).getByRole("button", { name: "Set up the assistant" }).click()
  const sheet = page.getByRole("dialog", { name: "Set up the assistant" })
  await expect(sheet).toBeVisible()
  await sheet.getByRole("radio", { name: "Sonnet 5.5" }).click()
  await sheet.getByLabel("API key", { exact: true }).fill("sk-ant-api03-right")
  await sheet.getByRole("button", { name: "Save" }).click()
  await expect(sheet).toBeHidden()
  expect(sent.setups.at(-1)).toMatchObject({ preset: "anthropic", model: "claude-sonnet-5-5" })
  await expect(question(pane(page))).toBeVisible()
  await expect(pane(page)).toContainText("Ask about shop")
})

/* ------------------------------------------------------------- questions */

test("a question streams an answer whose statement opens in a tab of its own", async ({ page }) => {
  const sent = await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await write(page, "select * from customers")
  await openAssistant(page)
  await ask(pane(page), "How many orders has each customer placed?")

  await expect(statements(page)).toContainText("LEFT JOIN orders o ON o.customer_id = c.id")
  await expect(pane(page)).toContainText("keeps customers who have no orders yet")
  // What the model read from its cache is input too: 2,140 new and 1,890 cached.
  await expect(pane(page)).toContainText("Claude Opus 5.5 · 4,030 tokens in · 186 out")
  // It went to the connection's own route, with nothing attached that the reader did not attach.
  expect(sent.paths[0]).toBe("/databases/1/assistant")
  expect(sent.asked[0]).toEqual({
    conversation: "",
    question: "How many orders has each customer placed?",
  })

  const before = await openTabs(page).count()
  await statements(page).getByRole("button", { name: "Open in a tab" }).click()
  await expect(openTabs(page)).toHaveCount(before + 1)
  await expect(
    page.getByRole("tablist", { name: "Open statements" }).getByRole("tab", { selected: true }),
  ).toContainText("How many orders")
  expect(await editorText(page)).toContain("GROUP BY c.email")
  // The statement is the reader's to run: nothing was sent to the query route.
  await expect(results(page)).toContainText("Nothing has been run in this tab")

  // The next question carries the conversation on.
  await ask(pane(page), "Only customers on the pro tier")
  await expect(statements(page)).toHaveCount(2)
  expect(sent.asked[1]).toMatchObject({ conversation: "c0ffee" })
})

test("a statement from an answer is written at the cursor once the answer is in", async ({
  page,
}) => {
  const slow = hold()
  await mockAssistant(page, {
    settings: ON,
    answers: [{ held: slow.until, lines: COUNT_ANSWER }],
  })
  await openEditor(page)
  await write(page, "-- counts\n")
  await openAssistant(page)
  await ask(pane(page), "Count the orders per customer")
  await expect(pane(page).locator("[data-slot=agent-thinking]")).toBeVisible()
  await expect(statements(page)).toHaveCount(0)
  slow.release()
  await expect(statements(page).getByRole("button", { name: "Insert" })).toBeVisible()
  await expect(statements(page).getByRole("button", { name: "Copy the statement" })).toBeVisible()
  await statements(page).getByRole("button", { name: "Insert" }).click()
  await expect.poll(() => editorText(page)).toContain("-- counts")
  expect(await editorText(page)).toContain("SELECT c.email, count(o.id) AS orders")
})

test("what the tab holds goes with a question only when it is pressed, and rows are said in amber", async ({
  page,
}) => {
  const sent = await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await write(page, "select * from customers")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page)).toContainText("3 rows")
  await openAssistant(page)

  const sends = pane(page).locator("[data-slot=agent-sends]")
  await expect(sends).toContainText("Sends Anthropic your question,")
  await pane(page).getByRole("button", { name: "Attach the statement in the editor" }).click()
  const result = pane(page).getByRole("button", { name: "Attach the first 3 rows of the result" })
  await result.click()
  await expect(result).toHaveAttribute("aria-pressed", "true")
  await expect(sends).toContainText("Sends Anthropic your question with 3 rows of the result")
  await expect(sends).toHaveClass(/text-warning/)

  await ask(pane(page), "Why is bo's tier empty?")
  const attached = pane(page).locator("[data-slot=agent-question-attached]")
  await expect(attached).toContainText("statement")
  await expect(attached).toContainText("result · 3 rows")
  // The values themselves are never drawn on the question.
  await expect(attached).not.toContainText("ann@example.com")
  expect(sent.asked[0].attached).toEqual([
    { label: "statement", text: "select * from customers" },
    {
      label: "result · 3 rows",
      text: [
        "id | email           | tier",
        "---+-----------------+-----",
        "1  | ann@example.com | pro",
        "2  | bo@example.com  | NULL",
        "3  | cy@example.com  | free",
        "(3 rows)",
      ].join("\n"),
    },
  ])
  // The rows are for one question; the statement stays attached.
  await expect(result).toHaveAttribute("aria-pressed", "false")
  await expect(sends).not.toHaveClass(/text-warning/)
  await ask(pane(page), "And how do I fix it?")
  await expect.poll(() => sent.asked.length).toBe(2)
  expect(sent.asked[1].attached).toEqual([{ label: "statement", text: "select * from customers" }])
})

test("a failed statement and a plan are asked about from where they are shown", async ({
  page,
}) => {
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
  await expect(pane(page)).toContainText("Why did this statement fail")
  await expect(pane(page).locator("[data-slot=agent-question-attached]")).toContainText("error")
  await expect(pane(page)).toContainText("There is no table called nope")
  const error = (sent.asked[0].attached as { label: string; text: string }[])[0]
  expect(error.label).toBe("error")
  expect(error.text).toContain("select * from nope")
  expect(error.text).toContain('ERROR: relation "nope" does not exist (SQLSTATE 42P01)')

  await write(page, "select * from orders where status = 'shipped'")
  await page
    .locator("[data-slot=query-commands]")
    .getByRole("button", { name: "Explain", exact: true })
    .click()
  await expect(results(page).locator("[data-slot=plan-tree]")).toBeVisible()
  await results(page).getByRole("button", { name: "Ask the assistant" }).click()
  await expect(pane(page)).toContainText("reads every row of orders")
  const plan = (sent.asked[1].attached as { label: string; text: string }[])[0]
  expect(plan.label).toBe("plan")
  expect(plan.text).toContain("select * from orders where status = 'shipped'")
  expect(plan.text).toContain('"Node Type": "Seq Scan"')
  // The plan is asked about in the same conversation.
  expect(sent.asked[1].conversation).toBe("c0ffee")
})

test("an answer that fails, and one the reader stops, say so", async ({ page }) => {
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
  await ask(pane(page), "How many orders?")
  await expect(pane(page)).toContainText("No answer")
  await expect(pane(page)).toContainText("Anthropic is not answering right now (529)")

  await ask(pane(page), "How many orders, again?")
  await expect(pane(page).locator("[data-slot=agent-thinking]")).toBeVisible()
  await pane(page).getByRole("button", { name: "Stop" }).click()
  await expect(pane(page)).toContainText("Stopped.")
  await expect(pane(page).getByRole("button", { name: "Ask", exact: true })).toBeVisible()
  slow.release()
})

/* ------------------------------------------------------ the database's page */

test("the connection's Assistant is beside its Home, and carries on the editor's chat", async ({
  page,
}) => {
  const sent = await mockAssistant(page, { settings: ON })
  await openEditor(page)
  await openAssistant(page)
  await ask(pane(page), "How many orders has each customer placed?")
  await expect(statements(page)).toBeVisible()
  // The column is the chat here, so the dock is not.
  await expect(page.locator("[data-slot=agent-dock]")).toHaveCount(0)

  const rail = page.getByRole("navigation", { name: "Sidebar" })
  const links = await rail.getByRole("link").allTextContents()
  expect(links.indexOf("Assistant")).toBe(links.indexOf("Home") + 1)
  await rail.getByRole("link", { name: "Assistant", exact: true }).click()
  await expect(page).toHaveURL(/\/databases\/1\/assistant$/)
  const page_ = page.locator("[data-slot=database-assistant]")
  await expect(page_).toBeVisible()
  // The same chat is in front: the question and its answer, ready to carry on.
  await expect(chat(page)).toContainText("How many orders has each customer placed?")
  await expect(chat(page)).toContainText("keeps customers who have no orders yet")
  await expect.poll(() => sent.listed.some((search) => search.includes("database=1"))).toBe(true)
  await ask(chat(page), "And the ones with none?")
  await expect.poll(() => sent.asked.length).toBe(2)
  expect(sent.paths[1]).toBe("/databases/1/assistant")
  expect(sent.asked[1]).toMatchObject({
    conversation: "c0ffee",
    question: "And the ones with none?",
  })

  // On another of its pages the dock goes back to it. A chat is the tab's, so this
  // is a step through the rail rather than a load of the page.
  await rail.getByRole("link", { name: "Logs", exact: true }).click()
  await expect(page).toHaveURL(/\/databases\/1\/logs/)
  const dock = page.locator("[data-slot=agent-dock]")
  await expect(dock).toContainText("shop")
  await dock.getByRole("link", { name: "Back to chat" }).click()
  await expect(page).toHaveURL(/\/databases\/1\/assistant$/)
})

test("the page offers questions in the engine's own words, and one press asks", async ({
  page,
}) => {
  const sent = await mockAssistant(page, {
    settings: ON,
    answers: [
      { lines: [start("r1"), text("It answers in **0.2 ms**; nothing is wrong."), done()] },
    ],
  })
  await page.goto("/databases/4/assistant")
  await expect(chat(page)).toContainText("Ask anything about cache")
  await expect(chat(page).getByRole("button", { name: "Which keys are biggest?" })).toBeVisible()
  await expect(chat(page).getByRole("button", { name: "Find slow commands" })).toBeVisible()
  await chat(page).getByRole("button", { name: "Is it healthy?" }).click()
  await expect(chat(page)).toContainText("It answers in 0.2 ms; nothing is wrong.")
  expect(sent.paths[0]).toBe("/databases/4/assistant")
  expect(String(sent.asked[0].question)).toContain("Is this database healthy right now")
})

test("a command on the host waits with the command itself, and shows what it printed", async ({
  page,
}) => {
  const sent = await mockAssistant(page, {
    settings: ON,
    answers: [
      {
        lines: [
          start("c1"),
          text("I'll check how big the data directory is."),
          done({ stop: "tool", pending: [DU] }),
        ],
      },
      {
        lines: [
          start("c1"),
          { type: "call", call: DU },
          {
            type: "called",
            id: "r1",
            tool: "run_command",
            ok: true,
            said: "exit 0",
            output: "4.2G\t/var/lib/postgresql/16/main",
          },
          text("The data directory takes **4.2 GB**."),
          done(),
        ],
      },
    ],
  })
  await page.goto("/databases/1/assistant")
  await ask(chat(page), "How much disk does it use?")
  const card = chat(page).locator("[data-slot=agent-approval]")
  await expect(card).toContainText("Run du -sh /var/lib/postgresql")
  // The command a person decides on is drawn whole, in mono.
  const command = card.locator("[data-slot=agent-command]")
  await expect(command).toContainText("du -sh /var/lib/postgresql/16/main")
  expect(await command.evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(/mono/i)
  await expect(card).toContainText("To see how much of the disk")
  expect(sent.asked).toHaveLength(1)

  await card.getByRole("button", { name: "Allow: Run du -sh /var/lib/postgresql" }).click()
  await expect(chat(page)).toContainText("The data directory takes 4.2 GB.")
  expect(sent.asked[1]).toEqual({ conversation: "c1", decisions: { r1: true } })
  const step = chat(page).locator("[data-slot=agent-step][data-tool=run_command]")
  await expect(step).toContainText("du -sh /var/lib/postgresql/16/main")
  await expect(step).toContainText("exit 0")
  await step.getByRole("button").first().click()
  await expect(step.getByLabel("What it printed")).toContainText("4.2G")
})

test("a saved chat of the connection is read back with what it carried, and carries on", async ({
  page,
}) => {
  const saved: ChatSummary = {
    id: "d1",
    title: "Why is bo's tier empty?",
    scope: { kind: "database", database: 1, name: "shop" },
    createdAt: ago(60),
    updatedAt: ago(5),
    provider: "anthropic",
    model: "claude-opus-5-5",
    running: false,
    waiting: false,
    steps: 0,
    changes: 0,
  }
  const sent = await mockAssistant(page, {
    settings: ON,
    chats: [saved],
    lines: {
      d1: [
        {
          type: "question",
          text: "Why is bo's tier empty?",
          attached: ["statement", "result · 3 rows"],
        },
        { ...start("d1"), title: "Why is bo's tier empty?" },
        text("Their row was added before tiers existed."),
        done(),
      ],
    },
  })
  await page.goto("/databases/1/assistant")
  const row = page
    .locator("[data-slot=agent-chat-row]")
    .filter({ hasText: "Why is bo's tier empty?" })
  await row.getByRole("button").first().click()
  await expect(page).toHaveURL(/\/databases\/1\/assistant\?chat=d1$/)
  await expect(chat(page)).toContainText("Their row was added before tiers existed.")
  await expect(chat(page).locator("[data-slot=agent-question-attached]")).toContainText(
    "result · 3 rows",
  )
  await ask(chat(page), "Fix it")
  await expect.poll(() => sent.asked.length).toBe(1)
  expect(sent.asked[0]).toMatchObject({ conversation: "d1", question: "Fix it" })
})

/* ------------------------------------------------------- Ask from any page */

test("Ask beside Search begins a chat told which page it came from", async ({ page }) => {
  const sent = await mockAssistant(page, {
    settings: ON,
    answers: [
      { lines: [start("s1"), text("These are the server's log lines for shop."), done()] },
      { lines: [start("s1"), text("Nothing is wrong."), done()] },
    ],
  })
  await page.goto("/databases/1/logs")
  await expect(page.locator("[data-slot=page] h1")).toHaveText("shop · Logs")
  await page.getByRole("button", { name: "Ask the assistant about this page" }).click()
  await expect(page).toHaveURL(/\/assistant$/)
  const about = chat(page).locator("[data-slot=agent-about]")
  await expect(about).toContainText("About shop · Logs")
  await expect(about).toHaveAttribute("href", "/databases/1/logs")
  await chat(page).getByRole("button", { name: "What am I looking at?" }).click()
  await expect(chat(page)).toContainText("These are the server's log lines for shop.")
  expect(sent.paths[0]).toBe("/assistant/chat")
  expect(sent.asked[0].about).toEqual({ path: "/databases/1/logs", title: "shop · Logs" })
  // Only the press that begins the chat says where it was begun.
  await ask(chat(page), "Is anything wrong?")
  await expect.poll(() => sent.asked.length).toBe(2)
  expect(sent.asked[1]).not.toHaveProperty("about")
  expect(sent.asked[1].conversation).toBe("s1")

  // Not offered on the assistant's own pages.
  await expect(page.getByRole("button", { name: "Ask the assistant about this page" })).toHaveCount(
    0,
  )
})

/* ------------------------------------------------------- who has it, where */

test("a role that only reads is offered no assistant", async ({ page }) => {
  await mockAssistant(page, { settings: ON, viewer: true })
  await openEditor(page)
  await expect(page.getByRole("tablist", { name: "Open statements" })).toBeVisible()
  await expect(toggle(page)).toHaveCount(0)
  await expect(pane(page)).toHaveCount(0)
  const rail = page.getByRole("navigation", { name: "Sidebar" })
  await expect(rail.getByRole("link", { name: "Home", exact: true })).toBeVisible()
  await expect(rail.getByRole("link", { name: "Assistant", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Ask the assistant about this page" })).toHaveCount(
    0,
  )
  await page.goto("/databases/1/assistant")
  await expect(page.getByText("Your role does not ask the assistant")).toBeVisible()
})

test("a protected connection is never sent to a model", async ({ page }) => {
  const sent = await mockAssistant(page, { settings: ON, rows: { 1: { readOnly: true } } })
  await page.goto("/databases/1/assistant")
  await expect(page.locator("[data-slot=page]")).toContainText("Off for a protected connection")
  await expect(page.locator("[data-slot=page]")).toContainText(
    "Nothing about shop is sent to the assistant while it is protected. Protection is turned off in its settings.",
  )
  await expect(chat(page)).toHaveCount(0)

  await openEditor(page)
  await openAssistant(page)
  await expect(pane(page)).toContainText("Off for a protected connection")
  await expect(question(pane(page))).toHaveCount(0)
  expect(sent.asked).toHaveLength(0)
})

test("on a phone the column lies over the editor, and the page is one pane at a time", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockAssistant(page, { settings: ON })
  await openEditor(page)
  // On a phone the assistant is a stop of the Query page's bar, not a button in its head.
  await page
    .locator("[data-slot=pane-bar]")
    .getByRole("tab", { name: "Assistant", exact: true })
    .click()
  await expect(pane(page)).toBeVisible()
  await ask(pane(page), "How many orders has each customer placed?")
  await expect(statements(page)).toBeVisible()
  const sideways = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(sideways).toBeLessThanOrEqual(0)
  // Opening the statement goes back to the editor it opened in.
  await statements(page).getByRole("button", { name: "Open in a tab" }).click()
  await expect(pane(page)).toHaveCount(0)
  expect(await editorText(page)).toContain("GROUP BY c.email")

  await page.goto("/databases/1/assistant")
  const box = question(chat(page))
  await expect(box).toBeInViewport()
  const bar = page.getByRole("navigation", { name: "Assistant panes" })
  await expect(bar).toBeVisible()
  // The shared bar (`phone-panes.tsx`) is the one this page drew by hand:
  // three stops of one width across it, at the foot of the screen.
  const stops = await bar
    .getByRole("tab")
    .evaluateAll((tabs) => tabs.map((tab) => Math.round(tab.getBoundingClientRect().width)))
  const foot = await bar.boundingBox()
  expect(stops).toHaveLength(3)
  expect(new Set(stops).size).toBe(1)
  expect(Math.abs(stops[0] * 3 - foot!.width)).toBeLessThanOrEqual(2)
  expect(Math.round(foot!.y + foot!.height)).toBe(844)
  expect(Math.round(foot!.height)).toBe(57)
  await expect(page.locator("[data-slot=agent-rail]")).toBeHidden()
  await bar.getByRole("tab", { name: "Chats" }).click()
  await expect(page.locator("[data-slot=agent-chats-list]")).toBeInViewport()
  await expect(box).toBeHidden()
})

/* -------------------------------------------------------------- pictures */

test("the database's assistant, for the pull request @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go in")
  const shots = process.env.JD_SHOTS
  const shot = (name: string) => page.screenshot({ path: `${shots}/${name}` })
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockAssistant(page, {
    settings: ON,
    answers: [
      { lines: COUNT_ANSWER },
      {
        lines: [
          start("c1"),
          text("I'll check how big the data directory is."),
          done({ stop: "tool", pending: [DU] }),
        ],
      },
    ],
  })
  await page.goto("/databases/1/assistant")
  await expect(chat(page).locator("[data-slot=suggestion]").first()).toBeVisible()
  await page.waitForTimeout(600)
  await shot("database-assistant-empty.png")

  await openEditor(page)
  await write(page, "select * from customers")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page)).toContainText("3 rows")
  await openAssistant(page)
  await pane(page).getByRole("button", { name: "Attach the statement in the editor" }).click()
  await pane(page).getByRole("button", { name: "Attach the first 3 rows of the result" }).click()
  await ask(pane(page), "How many orders has each customer placed?")
  await expect(statements(page).getByRole("button", { name: "Insert" })).toBeVisible()
  await page.waitForTimeout(400)
  await shot("database-assistant-editor.png")

  const rail = page.getByRole("navigation", { name: "Sidebar" })
  await rail.getByRole("link", { name: "Assistant", exact: true }).click()
  await ask(chat(page), "How much disk does it use?")
  await expect(chat(page).locator("[data-slot=agent-approval]")).toBeVisible()
  await page.waitForTimeout(600)
  await shot("database-assistant-command.png")

  await page.goto("/databases/1/logs")
  await page.getByRole("button", { name: "Ask the assistant about this page" }).click()
  await expect(chat(page).locator("[data-slot=agent-about]")).toBeVisible()
  await page.waitForTimeout(600)
  await shot("assistant-ask-about.png")

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/databases/4/assistant")
  await expect(chat(page).locator("[data-slot=suggestion]").first()).toBeVisible()
  await page.waitForTimeout(600)
  await shot("database-assistant-phone.png")
})
