import { expect, test, type Page, type Route } from "@playwright/test"
import { json, mockDraftJourney, mockProject, now, user } from "./deploy-fixture"

/**
 * The dashboard's assistant: a deployment's own page, the way a change waits or runs
 * by the mode, the shortcut to what it changed and the floating way back, the page
 * that sets up who answers, and a new deployment handed to it.
 */

const PRESETS = [
  {
    id: "anthropic",
    label: "Anthropic",
    detail: "Claude, with your API key",
    product: "claude",
    provider: "anthropic",
    keyRequired: true,
    keyHint: "sk-ant-…",
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
    keyHint: "sk-…",
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

/** The assistant's routes: one answer a press, and every body the page sent. */
async function mockAssistant(
  page: Page,
  answers: unknown[][],
  options: { settings?: object; models?: object[] } = {},
) {
  // The project's own mock answers everything, so it goes first: the last route
  // added is the first asked.
  await mockProject(page)
  const sent: Record<string, unknown>[] = []
  const activated: { preset: string; model?: string }[] = []
  const settings = { current: options.settings ?? claude }
  await page.route("**/api/v1/assistant", (route) => json(route, settings.current))
  await page.route("**/api/v1/assistant/models", (route) =>
    json(route, { models: options.models ?? MODELS }),
  )
  await page.route("**/api/v1/assistant/providers/*/activate", (route) => {
    const preset = new URL(route.request().url()).pathname.split("/").at(-2)!
    activated.push({ preset, ...(route.request().postDataJSON() as { model?: string }) })
    return json(route, { ...claude, modelLabel: "Claude Haiku 5.5", model: "claude-haiku-5-5" })
  })
  await page.route(/\/api\/v1\/deploy\/(7|drafts\/[^/]+)\/assistant/, (route) => {
    sent.push(route.request().postDataJSON())
    return stream(route, answers[sent.length - 1] ?? [])
  })
  return { sent, activated, settings }
}

const chat = (page: Page) => page.locator("[data-slot='agent-chat']")
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

test("on a phone the conversation and what it did are one pane at a time", async ({ page }) => {
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
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/deploy/7/assistant")
  const box = chat(page).getByRole("textbox", { name: "Ask the assistant" })
  await box.scrollIntoViewIfNeeded()
  await expect(box).toBeInViewport()
  await ask(page, "Fix the port")
  await expect(chat(page)).toContainText("Saved.")
  await expect(page.locator("[data-slot='agent-activity']")).toBeHidden()
  await page.getByRole("button", { name: /What it did/ }).click()
  await expect(page.locator("[data-slot='agent-activity']")).toBeVisible()
  await expect(chat(page)).toBeHidden()
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
  await page.goto("/assistant")
  await page.getByRole("button", { name: /^Full access/ }).click()
  await expect.poll(() => chosen).toEqual([{ defaultMode: "full" }])
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
