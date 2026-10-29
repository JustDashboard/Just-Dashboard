import { expect, test, type Page, type Route } from "@playwright/test"
import { json, mockProject, user } from "./deploy-fixture"

/**
 * The deployment operator (`deploy/operator/`): the overview's Diagnose
 * opens the assistant on the environment, a read it asks for waits for the
 * person's Allow, and an action it proposes runs only when its card is
 * pressed — through the route the project's own verb calls.
 */

const claude = {
  configured: true,
  provider: "anthropic",
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  keySet: true,
  models: [{ id: "claude-opus-5-5", label: "Claude Opus 5.5" }],
}

const start = {
  type: "start",
  conversation: "c1",
  provider: "anthropic",
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  tools: true,
}
const usage = { input: 120, output: 40 }
const failure = "health gate failed: GET /healthz answered 503 after 3 attempts"

const stream = (route: Route, events: unknown[]) =>
  route.fulfill({
    status: 200,
    contentType: "application/x-ndjson",
    body: events.map((event) => JSON.stringify(event)).join("\n") + "\n",
  })

/** The operator's route: one answer a press, and every body the page sent. */
async function mockOperator(page: Page, answers: unknown[][]) {
  const sent: Record<string, unknown>[] = []
  await page.route("**/api/v1/databases/assistant", (route) => json(route, claude))
  await page.route("**/api/v1/deploy/7/assistant**", (route) => {
    sent.push(route.request().postDataJSON())
    return stream(route, answers[sent.length - 1] ?? [])
  })
  return sent
}

const asksForRuns = [
  start,
  { type: "text", text: "I'll read the recent runs first." },
  {
    type: "done",
    stop: "tool",
    usage,
    model: "claude-opus-5-5",
    kept: true,
    pending: [
      { id: "toolu_1", tool: "recent_runs", label: "Read the last 10 runs", input: { limit: 10 } },
    ],
  },
]

test("Diagnose asks about the environment, a read waits for its Allow, and a proposal runs only when pressed", async ({
  page,
}) => {
  const dashboard = await mockProject(page)
  const sent = await mockOperator(page, [
    asksForRuns,
    [
      start,
      {
        type: "ran",
        calls: [{ id: "toolu_1", tool: "recent_runs", allowed: true, read: "2 runs" }],
      },
      { type: "text", text: `Run #1 failed: ${failure}. Restarting may clear it.` },
      {
        type: "proposal",
        proposal: {
          id: "toolu_2",
          action: "restart",
          label: "Restart Production",
          reason: "The container stopped answering its health check after the last start.",
          capability: "destructive",
          destructive: true,
        },
      },
      { type: "done", stop: "end", usage, model: "claude-opus-5-5", kept: true },
    ],
  ])
  await page.goto("/deploy/7")
  await page.getByRole("button", { name: "Diagnose" }).click()
  const sheet = page.getByRole("dialog", { name: /Diagnose api-production/ })
  await expect(sheet.getByText("I'll read the recent runs first.")).toBeVisible()
  expect(sent[0]).toEqual({ conversation: "", question: expect.stringContaining("Diagnose") })

  // The read is a card: nothing of it is read or sent until it is allowed.
  const card = sheet.getByRole("region", { name: "A read the assistant asks for" })
  await expect(card.getByText("Read the last 10 runs")).toBeVisible()
  await expect(card.getByText(/secret values masked/)).toBeVisible()
  expect(sent).toHaveLength(1)
  await card.getByRole("button", { name: "Allow: Read the last 10 runs" }).click()
  await expect.poll(() => sent.length).toBe(2)
  expect(sent[1]).toEqual({ conversation: "c1", decisions: { toolu_1: true } })
  await expect(card.getByText("Read 2 runs")).toBeVisible()
  await expect(sheet.getByText(failure, { exact: false })).toBeVisible()

  // The proposal ran nothing; its press is the project's own Restart.
  const proposal = sheet.getByRole("region", { name: "Proposed: Restart Production" })
  await expect(proposal.getByText(/stopped answering its health check/)).toBeVisible()
  expect(dashboard.actions()).not.toContain("restart")
  await proposal.getByRole("button", { name: "Restart" }).click()
  const confirm = page.getByRole("dialog", { name: "Restart" })
  await expect(confirm.getByText(/containers on Production restart/)).toBeVisible()
  await confirm.getByRole("button", { name: "Restart" }).click()
  await expect(proposal.getByText("Run #1 started.")).toBeVisible()
  await expect(proposal.getByRole("link", { name: "Open the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/87",
  )
  expect(dashboard.actions()).toContain("restart")
})

test("a declined read is sent declined, and asking something else declines what waits", async ({
  page,
}) => {
  await mockProject(page)
  const sent = await mockOperator(page, [
    [
      start,
      {
        type: "done",
        stop: "tool",
        usage,
        model: "claude-opus-5-5",
        kept: true,
        pending: [
          { id: "toolu_1", tool: "recent_runs", label: "Read the last 10 runs", input: {} },
          {
            id: "toolu_2",
            tool: "container_logs",
            label: "Read the last 120 lines the live containers wrote",
            input: {},
          },
        ],
      },
    ],
    [
      start,
      { type: "text", text: "Without the logs I can only say the last run failed." },
      { type: "done", stop: "end", usage, model: "claude-opus-5-5", kept: true },
    ],
    asksForRuns,
  ])
  await page.goto("/deploy/7")
  await page.getByRole("button", { name: "Diagnose" }).click()
  const sheet = page.getByRole("dialog", { name: /Diagnose api-production/ })
  const card = sheet.getByRole("region", { name: "Reads the assistant asks for" })
  await expect(card.getByText("It asks for 2 reads")).toBeVisible()
  // One answered is not all answered: nothing goes until both are.
  await card.getByRole("button", { name: "Allow: Read the last 10 runs" }).click()
  await expect(card.getByText("Allowed")).toBeVisible()
  expect(sent).toHaveLength(1)
  await card
    .getByRole("button", { name: "Decline: Read the last 120 lines the live containers wrote" })
    .click()
  await expect.poll(() => sent.length).toBe(2)
  expect(sent[1]).toEqual({ conversation: "c1", decisions: { toolu_1: true, toolu_2: false } })
  await expect(sheet.getByText("Without the logs")).toBeVisible()

  await sheet.getByRole("textbox", { name: "Ask the assistant" }).fill("Is the disk full?")
  await sheet.getByRole("button", { name: "Ask", exact: true }).click()
  await expect.poll(() => sent.length).toBe(3)
  expect(sent[2]).toEqual({ conversation: "c1", question: "Is the disk full?" })
})

test("a role that cannot control the deployment is offered no Diagnose", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "readonly" } }),
  )
  await page.goto("/deploy/7")
  await expect(page.getByRole("heading", { name: "Production", level: 2 })).toBeVisible()
  await expect(page.getByRole("button", { name: "Diagnose" })).toHaveCount(0)
})

test("with the assistant off, Diagnose says so and offers an administrator its setup", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/databases/assistant", (route) =>
    json(route, { configured: false, keySet: false, models: claude.models }),
  )
  let asked = false
  await page.route("**/api/v1/deploy/7/assistant**", (route) => {
    asked = true
    return route.abort()
  })
  await page.goto("/deploy/7")
  await page.getByRole("button", { name: "Diagnose" }).click()
  const sheet = page.getByRole("dialog", { name: /Diagnose api-production/ })
  await expect(sheet.getByText("The assistant is off")).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Set up the assistant" })).toBeVisible()
  expect(asked).toBe(false)
})
