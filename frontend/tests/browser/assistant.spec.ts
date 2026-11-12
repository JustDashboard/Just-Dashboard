import { expect, test, type Page, type Route } from "@playwright/test"
import { json, mockDraftJourney, mockProject, now, user } from "./deploy-fixture"

/**
 * The dashboard's assistant: a deployment's own page, the way a change waits or runs
 * by the mode, the shortcut to what it changed and the floating way back, the saved
 * chats and taking one over, the server's own chat, the page that sets up who
 * answers, and a new deployment handed to it.
 */

const PRESETS = [
  {
    id: "anthropic",
    label: "Anthropic",
    detail: "Claude, with your API key",
    product: "claude",
    provider: "anthropic",
    keyRequired: true,
    keyUrl: "https://console.anthropic.com/settings/keys",
  },
  {
    id: "openai",
    label: "OpenAI",
    detail: "GPT models, with your API key",
    product: "openai",
    provider: "openai",
    baseUrl: "https://api.openai.com/v1",
    keyRequired: true,
    keyUrl: "https://platform.openai.com/api-keys",
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    detail: "Hundreds of models behind one key",
    product: "openrouter",
    provider: "openai",
    baseUrl: "https://openrouter.ai/api/v1",
    keyRequired: true,
  },
  {
    id: "gemini",
    label: "Google Gemini",
    detail: "Gemini, with an AI Studio key",
    product: "gemini",
    provider: "openai",
    baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai",
    keyRequired: true,
  },
  {
    id: "ollama",
    label: "Ollama",
    detail: "A model running on this machine",
    product: "ollama",
    provider: "openai",
    baseUrl: "http://127.0.0.1:11434/v1",
    local: true,
    keyRequired: false,
  },
]

const MODELS = [
  { id: "claude-opus-5-5", label: "Claude Opus 5.5" },
  { id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
  { id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
]

const claude = {
  configured: true,
  provider: "anthropic",
  preset: "anthropic",
  tools: true,
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  keySet: true,
  models: MODELS,
  presets: PRESETS,
  defaultMode: "changes",
  updatedAt: "2026-10-30T09:00:00Z",
  updatedBy: "operator",
  profiles: [
    {
      preset: "anthropic",
      provider: "anthropic",
      model: "claude-opus-5-5",
      modelLabel: "Claude Opus 5.5",
      keySet: true,
      toolCalls: true,
      active: true,
    },
    {
      preset: "openrouter",
      provider: "openai",
      model: "deepseek/deepseek-r2",
      modelLabel: "deepseek/deepseek-r2",
      baseUrl: "https://openrouter.ai/api/v1",
      keySet: true,
      toolCalls: true,
      active: false,
    },
  ],
}
const off = { configured: false, keySet: false, models: MODELS, presets: PRESETS, profiles: [] }

const start = {
  type: "start",
  conversation: "c1",
  provider: "anthropic",
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  tools: true,
  mode: "changes",
}
const usage = { input: 1240, output: 320 }
const done = (extra: object = {}) => ({
  type: "done",
  stop: "end",
  usage,
  model: "claude-opus-5-5",
  kept: true,
  ...extra,
})

const readRuns = {
  id: "a",
  tool: "recent_runs",
  label: "Read the last 10 runs",
  kind: "read",
  input: { limit: 10 },
}
const readLogs = {
  id: "b",
  tool: "container_logs",
  label: "Read the last 120 lines the live containers wrote",
  kind: "read",
  input: { lines: 120 },
}
const setPort = {
  id: "c",
  tool: "call_operation",
  operation: "deployment_configuration_save",
  route: "PUT /deploy/{id}/environments/{env}/configuration",
  label: "Save the environment's configuration at its revision",
  kind: "change",
  reason: "The health check waits on 8080, and the app listens on 3000.",
  input: { body: { runtime: { port: 3000 } } },
}
const restart = {
  id: "d",
  tool: "call_operation",
  operation: "deployment_run_create",
  route: "POST /deploy/{id}/environments/{env}/runs",
  label: "Queue a run for an environment",
  kind: "destructive",
  reason: "So the new port takes effect.",
  input: { body: { operation: "restart" } },
}

const stream = (route: Route, events: unknown[]) =>
  route.fulfill({
    status: 200,
    contentType: "application/x-ndjson",
    body: events.map((event) => JSON.stringify(event)).join("\n") + "\n",
  })

interface ChatSummary {
  id: string
  title: string
  scope: {
    kind: "deployment" | "draft" | "server"
    deployment?: number
    environment?: number
    draft?: string
    name: string
  }
  createdAt: string
  updatedAt: string
  provider: string
  model: string
  running: boolean
  waiting: boolean
  steps: number
  changes: number
}

const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

const savedChat = (id: string, title: string, extra: Partial<ChatSummary> = {}): ChatSummary => ({
  id,
  title,
  scope: { kind: "deployment", deployment: 7, environment: 12, name: "api-production" },
  createdAt: ago(60),
  updatedAt: ago(5),
  provider: "anthropic",
  model: "claude-opus-5-5",
  running: false,
  waiting: false,
  steps: 0,
  changes: 0,
  ...extra,
})

/**
 * The assistant's routes: one answer a press, every body the page sent, and the
 * person's saved chats with the lines each was read back from.
 */
async function mockAssistant(
  page: Page,
  answers: (unknown[] | { status: number; body: unknown })[],
  options: {
    settings?: object
    models?: object[]
    chats?: ChatSummary[]
    lines?: Record<string, unknown[]>
  } = {},
) {
  // The project's own mock answers everything, so it goes first: the last route
  // added is the first asked.
  await mockProject(page)
  const sent: Record<string, unknown>[] = []
  const turns: string[] = []
  const activated: { preset: string; model?: string }[] = []
  const settings = { current: options.settings ?? claude }
  const chats = { list: [...(options.chats ?? [])], lines: { ...(options.lines ?? {}) } }
  const renamed: { id: string; title: string }[] = []
  const deleted: string[] = []
  await page.route("**/api/v1/assistant", (route) => json(route, settings.current))
  await page.route("**/api/v1/assistant/models", (route) =>
    json(route, { models: options.models ?? MODELS }),
  )
  await page.route("**/api/v1/assistant/providers/*/activate", (route) => {
    const preset = new URL(route.request().url()).pathname.split("/").at(-2)!
    activated.push({ preset, ...(route.request().postDataJSON() as { model?: string }) })
    return json(route, { ...claude, modelLabel: "Claude Haiku 5.5", model: "claude-haiku-5-5" })
  })
  await page.route(
    /\/api\/v1\/(deploy\/(7|drafts\/[^/]+)\/assistant|assistant\/chat)(\?.*)?$/,
    (route) => {
      sent.push(route.request().postDataJSON())
      turns.push(new URL(route.request().url()).pathname)
      const answer = answers[sent.length - 1] ?? []
      if (!Array.isArray(answer)) {
        return route.fulfill({
          status: answer.status,
          contentType: "application/json",
          body: JSON.stringify(answer.body),
        })
      }
      return stream(route, answer)
    },
  )
  await page.route(/\/api\/v1\/assistant\/chats(\/[^/?]+)?(\?.*)?$/, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const id = decodeURIComponent(url.pathname.split("/chats/")[1] ?? "")
    if (!id) {
      const deployment = url.searchParams.get("deployment")
      const scope = url.searchParams.get("scope")
      return json(route, {
        chats: chats.list.filter(
          (chat) =>
            (!deployment || chat.scope.deployment === Number(deployment)) &&
            (!scope || chat.scope.kind === scope),
        ),
      })
    }
    const chat = chats.list.find((one) => one.id === id)
    if (!chat) {
      return route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "not_found", message: "No such chat" } }),
      })
    }
    if (request.method() === "PATCH") {
      const { title } = request.postDataJSON() as { title: string }
      renamed.push({ id, title })
      chat.title = title
      return json(route, { chat })
    }
    if (request.method() === "DELETE") {
      deleted.push(id)
      chats.list = chats.list.filter((one) => one.id !== id)
      return route.fulfill({ status: 204 })
    }
    return json(route, { chat, lines: chats.lines[id] ?? [] })
  })
  return { sent, turns, activated, settings, chats, renamed, deleted }
}

const chat = (page: Page) => page.locator("[data-slot='agent-chat']")
const assistantTabs = (page: Page) => page.locator("[data-slot='assistant-tabs']")
const approval = (page: Page) => page.locator("[data-slot='agent-approval']")
const dock = (page: Page) => page.locator("[data-slot='agent-dock']")

async function ask(page: Page, question: string) {
  await chat(page).getByRole("textbox", { name: "Ask the assistant" }).fill(question)
  await chat(page).getByRole("button", { name: "Ask", exact: true }).click()
}

test("Diagnose opens the deployment's own page and asks at once", async ({ page }) => {
  const { sent } = await mockAssistant(page, [
    [
      start,
      { type: "call", call: readRuns },
      { type: "called", id: "a", tool: "recent_runs", ok: true, said: "12 runs" },
      { type: "text", text: "Run **#44** failed its health check." },
      done(),
    ],
  ])
  await page.goto("/deploy/7")
  await page.getByRole("link", { name: "Diagnose" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/assistant$/)
  await expect(chat(page)).toContainText("Run #44 failed its health check.")
  await expect(chat(page)).toContainText("Read the last 10 runs")
  await expect(chat(page)).toContainText("12 runs")
  await expect(chat(page)).toContainText("1,240 tokens in")
  expect(sent[0].question).toContain("Diagnose this deployment")
  // A read ran without a card: nothing here waited.
  await expect(approval(page)).toHaveCount(0)
})

test("the page offers questions, and it is one press from the project's rail", async ({ page }) => {
  await mockAssistant(page, [[start, { type: "text", text: "Three errors." }, done()]])
  await page.goto("/deploy/7")
  await page.getByRole("link", { name: "Assistant", exact: true }).first().click()
  await expect(page).toHaveURL(/\/deploy\/7\/assistant$/)
  await expect(chat(page)).toContainText("Ask anything about")
  await chat(page).getByRole("button", { name: "Check the logs for errors" }).click()
  await expect(chat(page)).toContainText("Three errors.")
})

test("a change waits for Allow, runs, and says where to see it", async ({ page }) => {
  const { sent } = await mockAssistant(page, [
    [
      start,
      { type: "text", text: "The port is wrong." },
      done({ stop: "tool", pending: [setPort, restart] }),
    ],
    [
      { type: "call", call: setPort },
      {
        type: "called",
        id: "c",
        tool: "call_operation",
        ok: true,
        said: "Done",
        change: {
          label: "Runtime",
          href: "/deploy/7/settings/runtime#listen",
          diff: [{ path: "runtime.port", from: "8080", to: "3000" }],
        },
      },
      { type: "called", id: "d", tool: "call_operation", ok: false, declined: true },
      { type: "text", text: "Saved. It applies with the next release." },
      done(),
    ],
  ])
  await page.goto("/deploy/7/assistant")
  await ask(page, "Fix the port")
  await expect(approval(page)).toContainText("It wants to do 2 things")
  await expect(approval(page)).toContainText("The health check waits on 8080")
  await expect(approval(page)).toContainText("Stops or replaces")

  await approval(page)
    .getByRole("button", { name: `Allow: ${setPort.label}` })
    .click()
  // One answered is not an answer: nothing is sent until both are.
  expect(sent).toHaveLength(1)
  await approval(page)
    .getByRole("button", { name: `Decline: ${restart.label}` })
    .click()
  await expect(chat(page)).toContainText("Saved. It applies with the next release.")
  expect(sent[1]).toEqual({ conversation: "c1", decisions: { c: true, d: false } })

  const change = chat(page).locator("[data-slot='agent-change']")
  await expect(change).toContainText("runtime.port")
  await expect(change).toContainText("3000")
  await expect(change.getByRole("link", { name: "Open Runtime" })).toHaveAttribute(
    "href",
    "/deploy/7/settings/runtime#listen",
  )
  // And it stands in the rail beside the conversation.
  await expect(page.locator("[data-slot='agent-activity']")).toContainText("Changes")
  await expect(
    page.locator("[data-slot='agent-activity']").getByRole("link", { name: "Open Runtime" }),
  ).toBeVisible()
})

test("asking something else declines what waits", async ({ page }) => {
  const { sent } = await mockAssistant(page, [
    [start, done({ stop: "tool", pending: [restart] })],
    [start, { type: "text", text: "Leaving it." }, done()],
  ])
  await page.goto("/deploy/7/assistant")
  await ask(page, "Restart it")
  await expect(approval(page)).toBeVisible()
  await expect(chat(page)).toContainText("Asking something else declines")
  await ask(page, "Actually, do not")
  await expect(chat(page)).toContainText("Leaving it.")
  expect(sent[1].question).toBe("Actually, do not")
  await expect(approval(page)).toHaveCount(0)
})

test("the mode is chosen beside the question and sent with it", async ({ page }) => {
  const { sent } = await mockAssistant(page, [
    [
      { ...start, mode: "full" },
      { type: "call", call: restart },
      {
        type: "called",
        id: "d",
        tool: "call_operation",
        ok: true,
        said: "Done",
        change: { label: "Run #45", href: "/deploy/7/runs/45" },
      },
      done(),
    ],
  ])
  await page.goto("/deploy/7/assistant")
  const menu = page.locator("[data-slot='mode-menu']")
  await expect(menu).toContainText("Ask before changes")
  await menu.click()
  await page.getByRole("menuitem", { name: /Full access/ }).click()
  await expect(menu).toHaveAttribute("data-mode", "full")
  await ask(page, "Restart it")
  await expect(chat(page).getByRole("link", { name: "Open Run #45" })).toBeVisible()
  expect(sent[0].mode).toBe("full")
  await expect(approval(page)).toHaveCount(0)
})

/** Pastes a screenshot into the box, as the system's screenshot tool leaves one. */
async function pasteScreenshot(page: Page, width = 2400, height = 1200) {
  await chat(page)
    .getByRole("textbox", { name: "Ask the assistant" })
    .evaluate(
      async (element, size) => {
        const canvas = document.createElement("canvas")
        canvas.width = size.width
        canvas.height = size.height
        const context = canvas.getContext("2d")!
        context.fillStyle = "#c33"
        context.fillRect(0, 0, size.width, size.height)
        const blob = await new Promise<Blob>((resolve) =>
          canvas.toBlob((made) => resolve(made!), "image/png"),
        )
        const data = new DataTransfer()
        data.items.add(new File([blob], "screenshot.png", { type: "image/png" }))
        element.dispatchEvent(
          new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }),
        )
      },
      { width, height },
    )
}

test("a screenshot pasted into the box goes with the question, and alone is one", async ({
  page,
}) => {
  const { sent } = await mockAssistant(page, [
    [start, { type: "text", text: "That is a 502 from the proxy." }, done()],
    [start, { type: "text", text: "The same page, smaller." }, done()],
  ])
  await page.goto("/deploy/7/assistant")
  const tiles = chat(page).locator("[data-slot='prompt-image']")
  await pasteScreenshot(page)
  await expect(tiles).toHaveCount(1)
  await expect(chat(page)).toContainText("Images are sent as they are")
  await ask(page, "What is this error?")
  await expect(chat(page)).toContainText("That is a 502 from the proxy.")
  const [first] = sent[0].images as { mediaType: string; data: string; thumb: { data: string } }[]
  // Made small before it left: its longest side is what a model reads at full detail.
  const png = Buffer.from(first.data, "base64")
  expect(first.mediaType).toBe("image/png")
  expect([png.readUInt32BE(16), png.readUInt32BE(20)]).toEqual([1568, 784])
  expect(Buffer.from(first.thumb.data, "base64").length).toBeLessThan(96 * 1024)
  await expect(chat(page).locator("[data-slot='agent-question-images'] img")).toHaveCount(1)
  await expect(tiles).toHaveCount(0)

  // One taken out is not sent; an image alone is a question.
  await pasteScreenshot(page, 600, 400)
  await pasteScreenshot(page, 600, 400)
  await expect(tiles).toHaveCount(2)
  await chat(page).getByRole("button", { name: "Remove image 2" }).click()
  await expect(tiles).toHaveCount(1)
  await chat(page).getByRole("button", { name: "Ask", exact: true }).click()
  await expect(chat(page)).toContainText("The same page, smaller.")
  expect(sent[1].question).toBe("")
  expect(sent[1].images).toHaveLength(1)
})

/** A one-pixel PNG: a thumbnail, or an image the server keeps whole. */
const PIXEL =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

test("a pasted image opens large before it is sent, after, and from a saved chat", async ({
  page,
}) => {
  const shot = { mediaType: "image/png", data: PIXEL, width: 1568, height: 784 }
  await mockAssistant(
    page,
    [[start, { type: "text", text: "That is a 502 from the proxy." }, done()]],
    {
      chats: [savedChat("c-shot", "What is this error?")],
      lines: {
        "c-shot": [
          {
            type: "question",
            text: "What is this error?",
            images: [
              { ...shot, id: "img-1" },
              { ...shot, id: "img-gone" },
            ],
            at: ago(30),
          },
          { ...start, conversation: "c-shot", at: ago(30) },
          { type: "text", text: "A 502.", at: ago(30) },
          done({ at: ago(30) }),
        ],
      },
    },
  )
  const kept: string[] = []
  await page.route("**/api/v1/assistant/chats/c-shot/images/*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1)!
    kept.push(id)
    if (id !== "img-1") {
      return route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "not_found", message: "only a small copy" } }),
      })
    }
    return route.fulfill({ contentType: "image/png", body: Buffer.from(PIXEL, "base64") })
  })
  await page.goto("/deploy/7/assistant")
  const lightbox = page.locator("[data-slot='lightbox']")
  const picture = lightbox.locator("[data-slot='lightbox-picture']")

  // Before it is sent: the tile opens the image as it will be sent, at its size.
  await pasteScreenshot(page)
  const open = chat(page).getByRole("button", { name: "Open image 1" })
  await open.click()
  await expect(lightbox).toBeVisible()
  await expect(lightbox.getByRole("heading")).toHaveText("The image to send")
  await expect(picture).toHaveAttribute("data-state", "ready")
  await expect(picture.locator("img").last()).toHaveAttribute("src", /^data:image\/png;base64,/)
  const box = (await picture.boundingBox())!
  expect(Math.round(box.width / box.height)).toBe(2)
  // A press in it stays in it: the box behind does not take the caret.
  await picture.click()
  await expect(lightbox).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(lightbox).toHaveCount(0)
  await expect(open).toBeFocused()
  await expect(chat(page).locator("[data-slot='prompt-image']")).toHaveCount(1)

  // Sent, the question's image opens whole, not as its thumbnail.
  await ask(page, "What is this error?")
  await expect(chat(page)).toContainText("That is a 502 from the proxy.")
  await chat(page).getByRole("button", { name: "Open image 1 of the question" }).click()
  await expect(picture.locator("img").last()).toHaveAttribute("src", /^data:image\/png;base64,/)
  await lightbox.getByRole("button", { name: "Close" }).click()
  await expect(lightbox).toHaveCount(0)

  // A saved chat's opens from where the server keeps it, and one it no longer
  // keeps opens as its thumbnail and says so.
  await page.goto("/deploy/7/assistant?chat=c-shot")
  await chat(page).getByRole("button", { name: "Open image 1 of the question" }).click()
  await expect(lightbox).toContainText("1 of 2")
  await expect(picture).toHaveAttribute("data-state", "ready")
  await expect(picture.locator("img").last()).toHaveAttribute(
    "src",
    /\/api\/v1\/assistant\/chats\/c-shot\/images\/img-1$/,
  )
  await page.keyboard.press("ArrowRight")
  await expect(lightbox).toContainText("2 of 2")
  await expect(lightbox.locator("[data-slot='lightbox-note']")).toHaveText(
    "Only a small copy of this image is kept.",
  )
  expect(kept).toEqual(["img-1", "img-gone"])
})

test("a shortcut goes to the page, and the dock goes back to the chat", async ({ page }) => {
  await mockAssistant(page, [
    [
      start,
      { type: "call", call: setPort },
      {
        type: "called",
        id: "c",
        tool: "call_operation",
        ok: true,
        said: "Done",
        change: { label: "Runtime", href: "/deploy/7/settings/runtime" },
      },
      { type: "text", text: "Saved." },
      done(),
    ],
  ])
  await page.goto("/deploy/7/assistant")
  await expect(dock(page)).toHaveCount(0)
  await ask(page, "Fix the port")
  await chat(page)
    .locator("[data-slot='agent-change']")
    .getByRole("link", { name: "Open Runtime" })
    .click()
  await expect(page).toHaveURL(/\/deploy\/7\/settings\/runtime/)
  await expect(dock(page)).toContainText("Changed Runtime")
  await dock(page).getByRole("link", { name: "Back to chat" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/assistant$/)
  // The conversation is still there, and the dock is not over its own chat.
  await expect(chat(page)).toContainText("Saved.")
  await expect(dock(page)).toHaveCount(0)
})

test("the conversation goes on while the person is on another page", async ({ page }) => {
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await mockProject(page)
  await page.route("**/api/v1/assistant", (route) => json(route, claude))
  await page.route("**/api/v1/deploy/7/assistant", async (route) => {
    await held
    return stream(route, [start, { type: "text", text: "Finished while you were away." }, done()])
  })
  await page.goto("/deploy/7/assistant")
  await ask(page, "Take your time")
  await page.getByRole("link", { name: "Logs", exact: true }).first().click()
  await expect(page).toHaveURL(/\/deploy\/7\/logs/)
  await expect(dock(page)).toContainText("Thinking")
  await expect(dock(page).locator("[data-working='true']")).toBeVisible()
  release()
  await expect(dock(page)).toContainText("Finished")
  await dock(page).getByRole("link", { name: "Back to chat" }).click()
  await expect(chat(page)).toContainText("Finished while you were away.")
})

test("the dock turns to what it is waiting on", async ({ page }) => {
  await mockAssistant(page, [[start, done({ stop: "tool", pending: [restart] })]])
  await page.goto("/deploy/7/assistant")
  await ask(page, "Restart it")
  await expect(approval(page)).toBeVisible()
  await page.getByRole("link", { name: "Logs", exact: true }).first().click()
  await expect(dock(page).locator("[data-slot='agent-dock-row']")).toHaveAttribute(
    "data-tone",
    "asking",
  )
  await expect(dock(page)).toContainText("Waiting for your answer")
})

test("the model is changed from beside the question", async ({ page }) => {
  const { activated } = await mockAssistant(page, [])
  await page.goto("/deploy/7/assistant")
  await page.locator("[data-slot='model-picker']").click()
  await page.getByRole("option", { name: "Claude Haiku 5.5" }).click()
  await expect.poll(() => activated).toEqual([{ preset: "anthropic", model: "claude-haiku-5-5" }])
})

test("a provider saved elsewhere is one press from taking over", async ({ page }) => {
  const { activated } = await mockAssistant(page, [])
  await page.goto("/deploy/7/assistant")
  await page.locator("[data-slot='model-picker']").click()
  await page.getByRole("option", { name: /OpenRouter/ }).click()
  await expect.poll(() => activated.at(-1)?.preset).toBe("openrouter")
})

test("with the assistant off the page says how to have it", async ({ page }) => {
  await mockAssistant(page, [], { settings: off })
  await page.goto("/deploy/7/assistant")
  await expect(page.getByText("The assistant is off")).toBeVisible()
  await page.getByRole("button", { name: "Set up the assistant" }).click()
  await expect(page.getByRole("dialog", { name: "Set up the assistant" })).toBeVisible()
})

test("a role without service control is told so and asks nothing", async ({ page }) => {
  await mockAssistant(page, [])
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, role: "readonly", capabilities: ["read"] }),
  )
  await page.goto("/deploy/7/assistant")
  await expect(page.getByText("Your role does not ask the assistant")).toBeVisible()
  await expect(chat(page)).toHaveCount(0)
})

test("on a phone the conversation, the chats and what it did are one pane at a time", async ({
  page,
}) => {
  await mockAssistant(
    page,
    [
      [
        start,
        { type: "call", call: setPort },
        {
          type: "called",
          id: "c",
          tool: "call_operation",
          ok: true,
          said: "Done",
          change: { label: "Runtime", href: "/deploy/7/settings/runtime" },
        },
        { type: "text", text: "Saved." },
        done(),
      ],
    ],
    { chats: [savedChat("c-old", "Which database does it use?")] },
  )
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/deploy/7/assistant")
  const box = chat(page).getByRole("textbox", { name: "Ask the assistant" })
  // The box is on the screen without scrolling to it, and the page does not scroll.
  await expect(box).toBeInViewport()
  expect(
    await page.evaluate(() => {
      const scroller = document.querySelector("[data-workspace-shell-scroll]")!
      return scroller.scrollHeight - scroller.clientHeight
    }),
  ).toBeLessThanOrEqual(0)
  await ask(page, "Fix the port")
  await expect(chat(page)).toContainText("Saved.")
  await expect(page.locator("[data-slot='agent-activity']")).toBeHidden()
  // The bar's stops are found by their word; how many changes are waiting
  // is said beside the name, not in it.
  const bar = page.getByRole("navigation", { name: "Assistant panes" })
  const did = bar.getByRole("tab", { name: "What it did", exact: true })
  await expect(did).toHaveAttribute("aria-description", "1")
  await did.click()
  await expect(did).toHaveAttribute("aria-selected", "true")
  await expect(page.locator("[data-slot='agent-activity']")).toBeVisible()
  await expect(chat(page)).toBeHidden()
  await bar.getByRole("tab", { name: "Chats", exact: true }).click()
  await expect(page.locator("[data-slot='agent-chats-list']")).toContainText("Which database")
  await expect(page.locator("[data-slot='agent-activity']")).toBeHidden()
  await bar.getByRole("tab", { name: "Chat", exact: true }).click()
  await expect(box).toBeInViewport()
})

test("the assistant's page sets up who answers, once, for everywhere", async ({ page }) => {
  await mockAssistant(page, [])
  const saved: Record<string, unknown>[] = []
  await page.route("**/api/v1/assistant/providers/*", (route) => {
    const request = route.request()
    if (request.method() !== "PUT") return route.fallback()
    saved.push({ url: new URL(request.url()).pathname, ...request.postDataJSON() })
    return json(route, claude)
  })
  await page.goto("/assistant")
  await assistantTabs(page).getByRole("link", { name: "Settings" }).click()
  await expect(page).toHaveURL(/\/assistant\?view=settings$/)
  await expect(page.locator("[data-slot='assistant-now']")).toContainText("Claude Opus 5.5")
  await expect(page.locator("[data-preset='anthropic']")).toContainText("In use")
  await expect(page.locator("[data-preset='openrouter']")).toContainText("deepseek/deepseek-r2")
  await page.getByRole("button", { name: "OpenAI: set up" }).click()
  const sheet = page.getByRole("dialog", { name: "Assistant settings" })
  await expect(sheet.getByRole("link", { name: /Get a key from OpenAI/ })).toBeVisible()
  await sheet.getByLabel("API key", { exact: true }).fill("sk-example")
  // Its models come from the provider, once it can be asked.
  await sheet.getByRole("combobox", { name: "Model" }).click()
  await page.getByRole("option", { name: "Claude Sonnet 5.5" }).click()
  await sheet.getByRole("button", { name: "Save and use" }).click()
  await expect(sheet).toBeHidden()
  expect(saved[0].url).toBe("/api/v1/assistant/providers/openai")
  expect(saved[0]).toMatchObject({ key: "sk-example", model: "claude-sonnet-5-5", activate: true })
})

test("what a conversation starts on is chosen for the install", async ({ page }) => {
  await mockAssistant(page, [])
  const chosen: unknown[] = []
  await page.route("**/api/v1/assistant/preferences", (route) => {
    chosen.push(route.request().postDataJSON())
    return json(route, { ...claude, defaultMode: "full" })
  })
  await page.goto("/assistant?view=settings")
  await page.getByRole("button", { name: /^Full access/ }).click()
  await expect.poll(() => chosen).toEqual([{ defaultMode: "full" }])
})

/* ------------------------------------------------------------ the composer */

test("the composer shows focus by its caret alone", async ({ page }) => {
  await mockAssistant(page, [])
  await page.goto("/deploy/7/assistant")
  const box = chat(page).getByRole("textbox", { name: "Ask the assistant" })
  const frame = chat(page).locator("[data-slot='prompt-frame']")
  const look = () =>
    frame.evaluate((element) => {
      const read = (node: Element) => {
        const style = getComputedStyle(node)
        // A shadow made of nothing but transparent layers draws nothing.
        const layers = style.boxShadow === "none" ? [] : style.boxShadow.split(/,(?![^(]*\))/)
        return {
          outline: style.outlineStyle === "none" || parseFloat(style.outlineWidth) === 0,
          shadow: layers.every((layer) => /rgba\(0, 0, 0, 0\)|transparent/.test(layer))
            ? "none"
            : style.boxShadow,
          border: style.borderColor,
        }
      }
      return { frame: read(element), box: read(element.querySelector("textarea")!) }
    })
  const resting = await look()
  // A press on the frame's own ground puts the caret in the box, and so does Tab.
  await frame.click({ position: { x: 6, y: 6 } })
  await expect(box).toBeFocused()
  const pressed = await look()
  await box.blur()
  await page.keyboard.press("Shift+Tab")
  await page.keyboard.press("Tab")
  await box.focus()
  await page.keyboard.type("x")
  const typed = await look()
  for (const focused of [pressed, typed]) {
    expect(focused.box.outline).toBe(true)
    expect(focused.box.shadow).toBe("none")
    expect(focused.frame.outline).toBe(true)
    expect(focused.frame.shadow).toBe("none")
    // Nothing about the frame changes when it holds the caret.
    expect(focused.frame.border).toBe(resting.frame.border)
  }
})

test("the Ask press turns into Stop while it works, and back", async ({ page }) => {
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await mockAssistant(page, [])
  await page.route("**/api/v1/deploy/7/assistant", async (route) => {
    await held
    return stream(route, [start, { type: "text", text: "Done thinking." }, done()])
  })
  await page.goto("/deploy/7/assistant")
  const send = chat(page).locator("[data-slot='prompt-send']")
  await expect(send).toHaveAttribute("data-state", "ask")
  await ask(page, "Take your time")
  await expect(send).toHaveAttribute("data-state", "stop")
  await expect(chat(page).getByRole("button", { name: "Stop", exact: true })).toBeVisible()
  await expect(chat(page).locator("[data-slot='agent-thinking']")).toContainText("Thinking")
  release()
  await expect(chat(page)).toContainText("Done thinking.")
  await expect(send).toHaveAttribute("data-state", "ask")
})

/* --------------------------------------------------------------- the page */

test("the chat fills the window: only the conversation scrolls", async ({ page }) => {
  const long = Array.from({ length: 60 }, (_, at) => `Line ${at + 1} of a long answer.`).join(
    "\n\n",
  )
  await mockAssistant(page, [[start, { type: "text", text: long }, done()]])
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/deploy/7/assistant")
  const composer = chat(page).locator("[data-slot='prompt-input']")
  await expect(composer).toBeVisible()
  const fits = async () => {
    const bottom = await composer.evaluate((element) => element.getBoundingClientRect().bottom)
    expect(900 - bottom).toBeGreaterThanOrEqual(0)
    expect(900 - bottom).toBeLessThanOrEqual(32)
    const shell = await page.evaluate(() => {
      const scroller = document.querySelector("[data-workspace-shell-scroll]")!
      return {
        shell: scroller.scrollHeight - scroller.clientHeight,
        document: document.documentElement.scrollHeight - document.documentElement.clientHeight,
      }
    })
    expect(shell.shell).toBeLessThanOrEqual(0)
    expect(shell.document).toBeLessThanOrEqual(0)
  }
  // Empty, the question and its suggestions sit in the middle, the box at the foot.
  const empty = chat(page).locator("[data-slot='agent-empty']")
  await expect(empty).toBeVisible()
  await fits()
  // No frame round the chat: it is the page's own ground, the rail after a hairline.
  const workspace = page.locator("[data-slot='project-assistant']")
  expect(await workspace.evaluate((element) => getComputedStyle(element).borderTopWidth)).toBe(
    "0px",
  )
  const rail = page.locator("[data-slot='agent-rail']")
  expect(await rail.evaluate((element) => getComputedStyle(element).borderLeftWidth)).toBe("1px")

  await ask(page, "Tell me everything")
  await expect(chat(page)).toContainText("Line 60 of a long answer.")
  await fits()
  // The answer is longer than the window, and the conversation is what scrolls.
  const log = chat(page).getByRole("log")
  const scroller = log.locator(":scope > div").first()
  expect(await scroller.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(
    true,
  )
  // It stayed on the newest line while the answer arrived.
  await expect(chat(page).getByText("Line 60 of a long answer.")).toBeInViewport()
  // Scrolled up, it lets go and offers the way back.
  await scroller.evaluate((element) => element.scrollTo({ top: 0 }))
  await log.hover()
  await page.mouse.wheel(0, -400)
  const latest = chat(page).getByRole("button", { name: "Jump to latest" })
  await expect(latest).toBeVisible()
  await latest.click()
  await expect(chat(page).getByText("Line 60 of a long answer.")).toBeInViewport()
  await expect(latest).toHaveCount(0)
})

/* ---------------------------------------------------------- saved chats */

const readConnections = {
  id: "r1",
  tool: "api_request",
  route: "GET /databases/connections",
  label: "List the database connections",
  kind: "read",
  reason: "To see which database it uses.",
}
const findRoutes = {
  id: "f1",
  tool: "find_routes",
  label: "Look up routes: databases",
  kind: "free",
}
const pastLines = [
  { type: "question", text: "Which database does it use?", at: ago(30) },
  { ...start, conversation: "c-old", title: "Which database does it use?", at: ago(30) },
  { type: "call", call: findRoutes, at: ago(30) },
  { type: "called", id: "f1", tool: "find_routes", ok: true, at: ago(30) },
  { type: "call", call: readConnections, at: ago(30) },
  {
    type: "called",
    id: "r1",
    tool: "api_request",
    ok: true,
    said: "2 connections",
    output: '[{"name":"shop-db"}]',
    at: ago(30),
  },
  { type: "text", text: "It uses **shop-db**.", at: ago(30) },
  done({ at: ago(30) }),
]

test("past chats are listed, and one opens as it was and carries on", async ({ page }) => {
  const { sent } = await mockAssistant(
    page,
    [[start, { type: "text", text: "Still shop-db." }, done()]],
    {
      chats: [
        savedChat("c-wait", "Restart it after the migration", { waiting: true, updatedAt: ago(2) }),
        savedChat("c-old", "Which database does it use?", { updatedAt: ago(30) }),
      ],
      lines: { "c-old": pastLines },
    },
  )
  await page.goto("/deploy/7/assistant")
  const list = page.locator("[data-slot='agent-chats-list']")
  const rows = list.locator("[data-slot='agent-chat-row']")
  await expect(rows).toHaveCount(2)
  await expect(rows.first()).toContainText("Restart it after the migration")
  await expect(rows.first()).toContainText("Waits")
  // A row says what it needs on its own line: one waiting is no taller than one not.
  // Rounded: a row still rising measures a hundred-thousandth of a pixel off.
  const heights = await rows.evaluateAll((all) =>
    all.map((row) => Math.round(row.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size, `row heights ${heights.join(", ")}`).toBe(1)

  await rows.filter({ hasText: "Which database" }).getByRole("button").first().click()
  await expect(page).toHaveURL(/\/deploy\/7\/assistant\?chat=c-old$/)
  await expect(chat(page)).toContainText("Which database does it use?")
  await expect(chat(page)).toContainText("It uses shop-db.")
  await expect(rows.filter({ hasText: "Which database" })).toHaveAttribute("aria-current", "true")
  // A route it called is drawn as the request, and looking routes up as a word.
  const step = chat(page).locator("[data-slot='agent-step'][data-tool='api_request']")
  await expect(step).toContainText("List the database connections")
  await expect(step.locator("[data-slot='agent-route']")).toContainText("GET")
  await expect(step.locator("[data-slot='agent-route']")).toContainText("/databases/connections")
  await expect(
    chat(page).locator("[data-slot='agent-step'][data-tool='find_routes']"),
  ).toContainText("Look up routes: databases")
  await step.getByRole("button").first().click()
  await expect(step).toContainText("To see which database it uses.")

  await ask(page, "Still?")
  await expect(chat(page)).toContainText("Still shop-db.")
  expect(sent[0]).toEqual({ conversation: "c-old", question: "Still?" })

  // New chat leaves it in the history and starts empty.
  await list.getByRole("button", { name: "New chat" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/assistant$/)
  await expect(chat(page).locator("[data-slot='agent-empty']")).toBeVisible()
})

test("a call to a route waits with its request and its reason", async ({ page }) => {
  const createConnection = {
    id: "w1",
    tool: "api_request",
    route: "POST /databases/connections",
    label: "Add a database connection",
    kind: "change",
    reason: "So the deployment can reach its new database.",
    input: { body: { name: "shop-db", engine: "postgres" } },
  }
  await mockAssistant(page, [[start, done({ stop: "tool", pending: [createConnection] })]])
  await page.goto("/deploy/7/assistant")
  await ask(page, "Connect a database")
  const card = approval(page)
  await expect(card).toContainText("Add a database connection")
  await expect(card.locator("[data-slot='agent-route']")).toContainText("POST")
  await expect(card.locator("[data-slot='agent-route']")).toContainText("/databases/connections")
  await expect(card).toContainText("So the deployment can reach its new database.")
  await expect(card).toContainText("shop-db")
  await expect(card.getByRole("button", { name: `Allow: ${createConnection.label}` })).toBeVisible()
})

test("a chat answering in another tab is followed, and can be taken over", async ({ page }) => {
  const running = savedChat("c-run", "Watch the deploy", { running: true })
  const { sent, chats } = await mockAssistant(
    page,
    [[start, { type: "text", text: "Carrying on here." }, done()]],
    {
      chats: [running],
      lines: {
        "c-run": [
          { type: "question", text: "Watch the deploy" },
          { ...start, conversation: "c-run" },
          { type: "thinking" },
        ],
      },
    },
  )
  await page.goto("/deploy/7/assistant?chat=c-run")
  await expect(chat(page)).toContainText("Watch the deploy")
  const bar = chat(page).locator("[data-slot='agent-elsewhere']")
  await expect(bar).toContainText("This chat is running in another tab.")
  await expect(chat(page).locator("[data-slot='agent-thinking']")).toContainText(
    "Answering in another tab",
  )
  // The live dot is on its row in the history too.
  await expect(page.locator("[data-slot='agent-chat-row'][data-chat='c-run']")).toHaveAttribute(
    "data-running",
    "true",
  )
  await bar.getByRole("button", { name: "Take over" }).click()
  await expect(chat(page)).toContainText("Carrying on here.")
  // Nothing typed: the chat is carried on from where the other tab put it down,
  // in the turn it was answering, with no question made up for it.
  expect(sent[0]).toMatchObject({ conversation: "c-run", takeOver: true })
  expect(sent[0]).not.toHaveProperty("question")
  await expect(bar).toHaveCount(0)
  await expect(chat(page).locator("[data-slot='agent-question']")).toHaveCount(1)
  await expect(chat(page).locator("[data-turn='stopped']")).toHaveCount(0)
  chats.list[0].running = false
})

test("a question refused because another tab has the chat waits for Take over", async ({
  page,
}) => {
  const { sent } = await mockAssistant(
    page,
    [
      {
        status: 409,
        body: { error: { code: "chat_busy", message: "This chat is answering elsewhere." } },
      },
      [start, { type: "text", text: "Taken over." }, done()],
    ],
    {
      chats: [savedChat("c-two", "Two tabs")],
      lines: {
        "c-two": [
          { type: "question", text: "Two tabs" },
          { ...start, conversation: "c-two" },
          { type: "text", text: "First answer." },
          done(),
        ],
      },
    },
  )
  await page.goto("/deploy/7/assistant?chat=c-two")
  await expect(chat(page)).toContainText("First answer.")
  await ask(page, "And now?")
  const bar = chat(page).locator("[data-slot='agent-elsewhere']")
  await expect(bar).toContainText("This chat is running in another tab.")
  await expect(chat(page)).toContainText("Not sent")
  await bar.getByRole("button", { name: "Take over" }).click()
  await expect(chat(page)).toContainText("Taken over.")
  expect(sent[0]).toEqual({ conversation: "c-two", question: "And now?" })
  expect(sent[1]).toEqual({ conversation: "c-two", question: "And now?", takeOver: true })
  await expect(chat(page).getByText("And now?")).toHaveCount(1)
})

test("a tab whose chat was taken over stops cleanly", async ({ page }) => {
  await mockAssistant(page, [
    [
      start,
      { type: "text", text: "Reading the logs" },
      {
        type: "error",
        error: { code: "taken_over", message: "This chat was continued somewhere else." },
      },
    ],
  ])
  await page.goto("/deploy/7/assistant")
  await ask(page, "Read the logs")
  await expect(chat(page).locator("[data-slot='agent-moved']")).toContainText(
    "Continued somewhere else.",
  )
  await expect(chat(page).locator("[data-turn='moved']")).toHaveCount(1)
  // No failure is drawn over it, and the box is ready again.
  await expect(chat(page).getByText("No answer")).toHaveCount(0)
  await expect(chat(page).locator("[data-slot='prompt-send']")).toHaveAttribute("data-state", "ask")
  await expect(chat(page).locator("[data-slot='agent-moved']").getByRole("button")).toHaveText(
    "Open it here",
  )
})

test("a chat is renamed in its own row and deleted after asking", async ({ page }) => {
  const { renamed, deleted } = await mockAssistant(page, [], {
    chats: [savedChat("c-a", "First chat"), savedChat("c-b", "Second chat", { updatedAt: ago(9) })],
  })
  await page.goto("/deploy/7/assistant")
  const row = page.locator("[data-slot='agent-chat-row'][data-chat='c-a']")
  const next = page.locator("[data-slot='agent-chat-row'][data-chat='c-b']")
  await expect(row).toBeVisible()
  // Measured once they have finished arriving, so a rise is not read as a move.
  const settled = () =>
    page.evaluate(() =>
      Promise.all(
        document
          .getAnimations()
          // A live dot breathes for ever; only what arrives ends.
          .filter((one) => one.effect?.getTiming().iterations !== Infinity)
          .map((one) => one.finished),
      ),
    )
  await settled()
  const before = await row.boundingBox()
  const nextBefore = await next.boundingBox()
  await row.hover()
  await row.getByRole("button", { name: "More for First chat" }).click()
  await page.getByRole("menuitem", { name: "Rename" }).click()
  const field = row.getByRole("textbox", { name: "Chat title" })
  await expect(field).toBeFocused()
  // Renaming is the row's own line: neither it nor the row under it moves.
  expect((await row.boundingBox())!.height).toBeCloseTo(before!.height, 1)
  expect((await next.boundingBox())!.y).toBeCloseTo(nextBefore!.y, 1)
  await field.fill("Database questions")
  await field.press("Enter")
  await expect(row).toContainText("Database questions")
  expect(renamed).toEqual([{ id: "c-a", title: "Database questions" }])

  await row.hover()
  await row.getByRole("button", { name: "More for Database questions" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const dialog = page.getByRole("alertdialog").or(page.getByRole("dialog"))
  await expect(dialog).toContainText("Delete this chat")
  await dialog.getByRole("button", { name: "Delete chat" }).click()
  await expect(row).toHaveCount(0)
  expect(deleted).toEqual(["c-a"])
  await expect(next).toBeVisible()
})

test("the server's page is a chat over the whole server, with every chat", async ({ page }) => {
  const { sent, turns } = await mockAssistant(
    page,
    [[{ ...start, conversation: "s1" }, { type: "text", text: "All quiet." }, done()]],
    {
      chats: [
        savedChat("c-srv", "Disk filling up", {
          scope: { kind: "server", name: "This server" },
        }),
        savedChat("c-old", "Which database does it use?", { updatedAt: ago(30) }),
      ],
      lines: { "c-old": pastLines },
    },
  )
  await page.goto("/assistant")
  await expect(chat(page)).toContainText("Ask anything about this server")
  const list = page.locator("[data-slot='agent-chats-list']")
  await expect(list).toContainText("This server")
  await expect(list).toContainText("api-production")
  await ask(page, "How is it?")
  await expect(chat(page)).toContainText("All quiet.")
  expect(turns[0]).toBe("/api/v1/assistant/chat")
  expect(sent[0]).toEqual({ conversation: "", question: "How is it?" })
  // A deployment's chat opens on that deployment's page, as it was.
  await list.locator("[data-chat='c-old']").getByRole("button").first().click()
  await expect(page).toHaveURL(/\/deploy\/7\/assistant\?chat=c-old$/)
  await expect(chat(page)).toContainText("It uses shop-db.")
  // Nothing of the settings was lost: they are the page's second tab.
  await page.goto("/assistant")
  await assistantTabs(page).getByRole("link", { name: "Settings" }).click()
  await expect(page.locator("[data-slot='assistant-now']")).toContainText("Claude Opus 5.5")
  await expect(page.getByText("What it may do on its own")).toBeVisible()
  await expect(page.getByText("Where it works")).toBeVisible()
})

test("the mode menu says plainly what each mode lets it do", async ({ page }) => {
  await mockAssistant(page, [])
  await page.goto("/deploy/7/assistant")
  await page.locator("[data-slot='mode-menu']").click()
  await expect(page.getByRole("menuitem", { name: /Full access/ })).toContainText(
    "Runs everything without asking",
  )
  // Full access is also what makes access that outlasts the chat, and says so.
  await expect(page.getByRole("menuitem", { name: /Full access/ })).toContainText(
    "including what outlasts the chat: a new account, a key, a sudo rule",
  )
  await expect(page.getByRole("menuitem", { name: /Ask before changes/ })).toContainText(
    "Every change waits for your Allow",
  )
  // A read that leaves the server is held like a change.
  await expect(page.getByRole("menuitem", { name: /Ask before changes/ })).toContainText(
    "so does a read it would send to another machine",
  )
})

/* -------------------------------------------------------- a new deployment */

const planned = {
  build: {
    method: "recipe",
    recipe: "node",
    buildCommand: "bun run build",
    startCommand: "bun start",
    secrets: [],
    releaseTasks: [],
  },
  runtime: {
    image: "",
    command: [],
    internalPort: 3000,
    hostPort: 0,
    bindAddress: "127.0.0.1",
    strategy: "blue_green",
    privileged: false,
    hostNetwork: false,
    capabilities: [],
    devices: [],
    mounts: [],
  },
  variables: [],
  dependencies: [],
  checks: [],
  domains: [],
}

const setupDraft = (revision: number, name: string) => ({
  id: "setup-draft",
  ownerUsername: "operator",
  currentStep: "configuration",
  revision,
  data: {
    intent: { name, profile: "web" },
    source: { kind: "git", mode: "git_url", url: "https://github.com/acme/shop.git", ref: "main" },
    configuration: planned,
    detection: {
      source: { kind: "git", remote: "https://github.com/acme/shop.git", ref: "main" },
      candidates: [
        {
          id: "web",
          name: "Next.js web application",
          root: "",
          profile: "web",
          buildMethod: "recipe",
          recipe: "node",
          confidence: "high",
          framework: "Next.js",
        },
      ],
      selectedId: "web",
      scannedFiles: 6,
      scannedBytes: 512,
      truncated: false,
      gitRequirements: { submodules: false, lfs: false },
    },
  },
  findings: [],
  planPreview: "",
  updatedAt: now,
  expiresAt: "2026-12-04T12:00:00Z",
})

const saveDraft = {
  id: "s",
  tool: "call_operation",
  operation: "deployment_draft_save",
  route: "PUT /deploy/drafts/{draft}",
  label: "Save one step of a draft at its revision",
  kind: "change",
  reason: "Next.js on port 3000, with a health check on /.",
  input: { body: { revision: 2, step: "configuration" } },
}

/** A draft waiting to be configured, and what the assistant answers about it. */
async function mockSetup(page: Page, answer: unknown[], settings: object = claude) {
  await mockDraftJourney(page)
  const sent: Record<string, unknown>[] = []
  const reads = { count: 0 }
  await page.route("**/api/v1/assistant", (route) => json(route, settings))
  await page.route("**/api/v1/deploy/drafts/setup-draft", (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON()
      return json(route, { ...setupDraft(2, body.intent?.name ?? "shop") })
    }
    if (route.request().method() !== "GET") return route.fallback()
    reads.count += 1
    return json(route, setupDraft(reads.count + 2, "shop"))
  })
  await page.route("**/api/v1/deploy/drafts/setup-draft/assistant", (route) => {
    sent.push(route.request().postDataJSON())
    return stream(route, answer)
  })
  return { sent, reads }
}

const configured = [
  start,
  { type: "call", call: saveDraft },
  {
    type: "called",
    id: "s",
    tool: "call_operation",
    ok: true,
    said: "Done",
    change: { label: "Review", href: "/deploy/new?step=review" },
  },
  { type: "text", text: "Chose Next.js on 3000. Press **Deploy** when you are ready." },
  done(),
]

test("a new deployment is handed to the assistant, and Deploy stays the person's", async ({
  page,
}) => {
  const { sent, reads } = await mockSetup(page, configured)
  await page.goto("/deploy/new?draft=setup-draft")
  await page.locator("[data-slot='setup-assistant-offer']").click()
  const panel = page.locator("[data-slot='setup-assistant']")
  await expect(panel).toContainText("Press Deploy when you are ready.")
  // It asked on being handed the job, and nothing waited: a draft runs nothing.
  expect(sent[0].question).toContain("Configure this deployment")
  await expect(panel.locator("[data-slot='agent-approval']")).toHaveCount(0)
  // What it saved was read back, so the form and the plan say the same.
  await expect.poll(() => reads.count).toBeGreaterThan(1)
  await panel.getByRole("button", { name: "Review and deploy" }).click()
  await expect(page.getByRole("heading", { name: "Ready to deploy?" })).toBeVisible()
  await expect(page.getByRole("button", { name: /Deploy$/ }).first()).toBeVisible()
  await expect(panel.getByRole("button", { name: /^Deploy/ })).toHaveCount(0)
})

test("the chooser offers it before there is anything to configure", async ({ page }) => {
  await mockSetup(page, configured)
  await page.goto("/deploy/new")
  const choice = page.locator("[data-slot='assist-switch']")
  await expect(choice).toContainText("Configure it with the assistant")
  const toggle = choice.getByRole("switch", { name: "Configure it with the assistant" })
  await expect(toggle).not.toBeChecked()
  await toggle.click()
  await expect(toggle).toBeChecked()
})

test("with the assistant off the chooser says how to have it", async ({ page }) => {
  await mockSetup(page, [], off)
  await page.goto("/deploy/new")
  await expect(page.locator("[data-slot='assist-switch']")).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Set up the assistant" })).toHaveAttribute(
    "href",
    "/assistant",
  )
})

test("on a phone handing it over is the whole screen, with the way back", async ({ page }) => {
  await mockSetup(page, configured)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/deploy/new?draft=setup-draft")
  await page.locator("[data-slot='setup-assistant-offer']").click()
  const panel = page.locator("[data-slot='setup-assistant']")
  await expect(panel).toContainText("Press Deploy when you are ready.")
  await expect(panel.getByRole("textbox", { name: "Ask the assistant" })).toBeInViewport()
  await panel.getByRole("button", { name: "Back to the form" }).click()
  await expect(panel).toHaveCount(0)
  await expect(page.locator("[data-slot='setup-assistant-offer']")).toBeVisible()
})

/* ------------------------------------------------------------ the terminal */

test("a password the terminal asks for is typed by the person, straight into the window", async ({
  page,
}) => {
  const asking = {
    id: "t1",
    tool: "terminal_run",
    label: "In the terminal: sudo apt-get update",
    kind: "change",
    reason: "Refreshing the package lists.",
    input: { command: "sudo apt-get update", wait: 20 },
  }
  const mocked = await mockAssistant(page, [
    [
      start,
      { type: "call", call: asking },
      {
        type: "called",
        id: "t1",
        tool: "terminal_run",
        ok: true,
        said: "waiting for your password",
        output: "[sudo] password for ubuntu:",
        terminal: { session: "ws-42", state: "password", prompt: "[sudo] password for ubuntu:" },
      },
      { type: "text", text: "It is waiting for your password." },
      done(),
    ],
    [start, { type: "text", text: "The package lists are up to date." }, done()],
  ])
  const typed: unknown[] = []
  await page.route(/\/api\/v1\/assistant\/chats\/[^/]+\/terminal$/, (route) => {
    typed.push(route.request().postDataJSON())
    return route.fulfill({ status: 204 })
  })
  await page.goto("/deploy/7/assistant")
  await ask(page, "Update the package lists")
  const card = chat(page).locator("[data-slot='agent-terminal']")
  await expect(card).toHaveAttribute("data-state", "password")
  await expect(card).toContainText("Waiting for your password")
  await expect(card).toContainText("[sudo] password for ubuntu:")
  await expect(card.getByRole("link", { name: "Open Terminal" })).toHaveAttribute(
    "href",
    "/terminal?session=ws-42",
  )
  await expect(
    chat(page).locator("[data-slot='agent-step'][data-tool='terminal_run']"),
  ).toContainText("sudo apt-get update")
  const field = card.getByLabel("Password for the terminal")
  await expect(field).toHaveAttribute("type", "password")
  await field.fill("correct-horse-battery")
  await card.getByRole("button", { name: "Send", exact: true }).click()
  await expect(card).toContainText("Sent to the terminal")
  expect(typed).toEqual([{ text: "correct-horse-battery" }])
  // The assistant carries on, told only that it was typed.
  await expect(chat(page)).toContainText("The package lists are up to date.")
  expect(mocked.sent).toHaveLength(2)
  expect(mocked.sent[1]).toMatchObject({ conversation: "c1", question: "I've typed the password." })
  expect(JSON.stringify(mocked.sent)).not.toContain("correct-horse-battery")
})

test("a command still running in the terminal says so, with the way to its window", async ({
  page,
}) => {
  await mockAssistant(page, [
    [
      start,
      {
        type: "call",
        call: {
          id: "t1",
          tool: "terminal_run",
          label: "In the terminal: apt-get upgrade -y",
          kind: "change",
          input: { command: "apt-get upgrade -y", wait: 20 },
        },
      },
      {
        type: "called",
        id: "t1",
        tool: "terminal_run",
        ok: true,
        said: "still running",
        output: "Unpacking libssl3 ...",
        terminal: { session: "ws-7", state: "running" },
      },
      { type: "text", text: "It is still upgrading; I will check back." },
      done(),
    ],
  ])
  await page.goto("/deploy/7/assistant")
  await ask(page, "Upgrade everything")
  const card = chat(page).locator("[data-slot='agent-terminal']")
  await expect(card).toHaveAttribute("data-state", "running")
  await expect(card).toContainText("Still running in its terminal")
  await expect(card.getByRole("link", { name: "Open Terminal" })).toHaveAttribute(
    "href",
    "/terminal?session=ws-7",
  )
  await expect(card.getByLabel("Password for the terminal")).toHaveCount(0)
})

// On a phone the head of an assistant that is off keeps its words on their own
// lines' width: the command sits under them at the head's full width, where it
// had squeezed "The / assistant is / off" into a column a word wide.
test("the head of an assistant that is off reads across a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockAssistant(page, [], { settings: off })
  await page.goto("/assistant?view=settings")
  const head = page.locator("[data-slot='assistant-now']")
  const title = head.getByText("The assistant is off", { exact: true })
  const command = head.getByRole("button", { name: "Set up the assistant" })
  await expect(command).toBeVisible()
  const frame = (await head.boundingBox())!
  const words = (await title.boundingBox())!
  const button = (await command.boundingBox())!
  // One line of the title's 28px, not three.
  expect(words.height).toBeLessThanOrEqual(30)
  expect(button.y).toBeGreaterThanOrEqual(words.y + words.height)
  expect(button.width).toBeGreaterThan(frame.width - 40)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
})
