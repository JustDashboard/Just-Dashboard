import { expect, test, type Page } from "@playwright/test"
import { json, mockHost, user } from "./host-fixture"
import { risen } from "./risen"

/**
 * The assistant's settings view (`/assistant?view=settings`): one centred
 * column of sections on hairlines, as a project's settings are. The chat in
 * front of it, and what the settings write, are `assistant.spec.ts`'s.
 */

const PRESETS = [
  ["anthropic", "Anthropic", "Claude, with your API key", "claude"],
  ["openai", "OpenAI", "GPT models, with your API key", "openai"],
  ["openrouter", "OpenRouter", "Hundreds of models behind one key", "openrouter"],
  ["gemini", "Google Gemini", "Gemini, with an AI Studio key", "gemini"],
  ["ollama", "Ollama", "A model running on this machine", "ollama"],
].map(([id, label, detail, product]) => ({
  id,
  label,
  detail,
  product,
  provider: id === "anthropic" ? "anthropic" : "openai",
  keyRequired: id !== "ollama",
}))

const configured = {
  configured: true,
  provider: "anthropic",
  preset: "anthropic",
  tools: true,
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  keySet: true,
  models: [{ id: "claude-opus-5-5", label: "Claude Opus 5.5" }],
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
      active: true,
    },
    {
      preset: "openrouter",
      provider: "openai",
      model: "deepseek/deepseek-r2",
      keySet: true,
      active: false,
    },
  ],
}

const projects = [7, 8, 9].map((id) => ({ id, name: `project-${id}`, status: "ready" }))

async function mockSettings(page: Page, settings: object = configured) {
  await mockHost(page)
  await page.route("**/api/v1/assistant", (route) => json(route, settings))
  await page.route("**/api/v1/deploy/", (route) => json(route, projects))
}

const sections = (page: Page) => page.locator("[data-slot=setting]")

for (const width of [1280, 390]) {
  test(`the settings are one column of sections, with no frame round any, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockSettings(page)
    await page.goto("/assistant?view=settings")
    await expect(sections(page).locator("h2")).toHaveText([
      "Model",
      "Providers",
      "Default mode",
      "Where it works",
      "Ask about a deployment",
    ])
    // One column, every section on the same two edges, and no framed panel.
    await risen(page)
    const boxes = await sections(page).evaluateAll((all) =>
      all.map((section) => {
        const box = section.getBoundingClientRect()
        return { x: Math.round(box.x), width: Math.round(box.width) }
      }),
    )
    expect(new Set(boxes.map((box) => `${box.x}:${box.width}`)).size).toBe(1)
    expect(boxes[0].width).toBeLessThanOrEqual(768)
    await expect(page.locator("[data-slot=page] [data-slot=panel]")).toHaveCount(0)
    await expect(page.locator("[data-slot=page] [data-slot=stat-grid]")).toHaveCount(0)

    // Model: who answers as a line of facts, and the picker at its end.
    const model = page.locator("#assistant-model")
    await expect(model.locator('img[src="/logos/claude.svg"]').first()).toBeVisible()
    await expect(model).toContainText("Anthropic")
    await expect(model).toContainText("claude-opus-5-5")
    await expect(model).toContainText("set up by operator on Oct 30, 2026")
    await expect(model.locator("[data-slot=model-picker]")).toContainText("Claude Opus 5.5")

    // Providers: a grid of cards of one height, the one in use first.
    const providers = page.getByRole("list", { name: "Providers" }).getByRole("listitem")
    await expect(providers).toHaveCount(5)
    await expect(providers.first()).toContainText("Anthropic")
    await expect(providers.first()).toContainText("In use")
    await expect(providers.nth(1)).toContainText("deepseek/deepseek-r2")
    const heights = await providers.evaluateAll((all) =>
      all.map((card) => card.getBoundingClientRect().height),
    )
    expect(new Set(heights).size).toBe(1)

    // Default mode: three cards, the chosen one lit, and one clause over them.
    const modes = page.getByRole("group", { name: "Default mode" })
    await expect(modes.locator("[data-slot=choice-card]")).toHaveCount(3)
    await expect(modes.locator("[data-selected]")).toContainText("Ask before changes")
    await expect(page.getByText("Conversations start on Ask before changes")).toBeVisible()
    // The paragraph that stood under them is behind the head's mark.
    await expect(page.getByText(/stay out of its reach/)).toHaveCount(0)
    await page.getByRole("button", { name: "What holds in every mode" }).hover()
    await expect(page.getByRole("tooltip")).toContainText("every call it makes is audited")

    // Where it works: rows of one height on the page's own ground.
    const places = page.getByRole("list", { name: "Where it works" }).getByRole("link")
    await expect(places).toHaveCount(8)
    const placeHeights = await places.evaluateAll((all) =>
      all.map((row) => row.getBoundingClientRect().height),
    )
    expect(new Set(placeHeights)).toEqual(new Set([56]))
    await expect(places.filter({ hasText: "Deployments" }).first()).toHaveAttribute(
      "href",
      "/deploy",
    )

    await expect(
      page
        .getByRole("list", { name: "Deployments to ask about" })
        .getByRole("link", { name: "Ask about project-7" }),
    ).toHaveAttribute("href", "/deploy/7/assistant")
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
  })
}

test("a mode is written once however fast it is pressed", async ({ page }) => {
  await mockSettings(page)
  const written: unknown[] = []
  let answer = () => {}
  const held = new Promise<void>((resolve) => (answer = resolve))
  await page.route("**/api/v1/assistant/preferences", async (route) => {
    written.push(route.request().postDataJSON())
    await held
    await json(route, { ...configured, defaultMode: "full" })
  })
  await page.goto("/assistant?view=settings")
  const modes = page.getByRole("group", { name: "Default mode" })
  await modes.getByRole("button", { name: /^Full access/ }).click()
  // While the first write is out the other cards carry no control.
  await expect(modes.getByRole("button", { name: /^Ask every time/ })).toHaveCount(0)
  await modes.locator("[data-slot=choice-card]", { hasText: "Ask every time" }).click()
  answer()
  await expect(modes.getByRole("button", { name: /^Ask every time/ })).toBeVisible()
  expect(written).toEqual([{ defaultMode: "full" }])
})

test("an account that may not change the settings reads them, with nothing to press", async ({
  page,
}) => {
  await mockSettings(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/assistant?view=settings")
  const modes = page.getByRole("group", { name: "Default mode" })
  await expect(modes.locator("[data-selected]")).toContainText("Ask before changes")
  await expect(modes.getByRole("button")).toHaveCount(0)
  const providers = page.getByRole("list", { name: "Providers" })
  await expect(providers.getByRole("listitem").first()).toContainText("In use")
  await expect(providers.getByRole("button")).toHaveCount(0)
  // What is saved says so; what is not offers nothing to an account that
  // could not take it.
  await expect(providers.getByText("Saved", { exact: true })).toHaveCount(1)
  await expect(providers.getByText("Set up", { exact: true })).toHaveCount(0)
})

test("off, the model section says so and its one command sets it up", async ({ page }) => {
  await mockSettings(page, { configured: false, models: [], presets: PRESETS, profiles: [] })
  await page.goto("/assistant?view=settings")
  const model = page.locator("#assistant-model")
  await expect(model.getByText("The assistant is off", { exact: true })).toBeVisible()
  await expect(page.getByText("5 to choose from")).toBeVisible()
  await model.getByRole("button", { name: "Set up the assistant" }).click()
  await expect(page.getByRole("dialog", { name: "Set up the assistant" })).toBeVisible()
})
