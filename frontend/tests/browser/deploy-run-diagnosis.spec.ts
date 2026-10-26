import { expect, test, type Page } from "@playwright/test"
import {
  json,
  mockProject,
  now,
  run as fixtureRun,
  steps as fixtureSteps,
  user,
} from "./deploy-fixture"
import { liveDiagnosis, liveCause, liveTranscript } from "./deploy-run-diagnosis-fixture"
import type {
  DeploymentEngineRun,
  DeploymentRunDiagnosis,
  DeploymentStep,
} from "../../src/lib/types"

/**
 * A failed run asked about (`run-diagnosis.tsx`): nothing is sent until
 * somebody presses, the press says where it goes and what of the run, the
 * answer is the cause, the lines that show it and what to try, and it stays
 * with the run. The failure's own fix is untouched by it.
 */

test.use({ timezoneId: "UTC" })

const run = fixtureRun as DeploymentEngineRun

/** A run whose build failed, every step after it skipped. */
function failedRun(cause: Record<string, unknown> = {}) {
  const steps = (fixtureSteps as unknown as DeploymentStep[]).map((step) =>
    step.key === "build_artifact"
      ? { ...step, state: "failed" as const, errorCode: "build_failed", evidence: { cause } }
      : step.ordinal > 5
        ? { ...step, state: "skipped" as const }
        : step,
  )
  return {
    run: {
      ...run,
      state: "failed" as const,
      terminalCode: "build_module_not_found",
      terminalReason: "The build command (`npm run build`) exited with code 1.",
      endedAt: now,
    },
    steps,
  }
}

type Lines = { seq: number; text: string }[]

async function mockFailedRun(page: Page, lines: Lines, cause: Record<string, unknown> = {}) {
  const snapshot = failedRun(cause)
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot, ts: Date.now() }))
    socket.send(
      JSON.stringify({
        type: "events",
        data: lines.map((line) => ({
          seq: line.seq,
          type: "step.log",
          runId: 84,
          stepId: 105,
          ts: now,
          data: {
            // The engine's own sentence is its status line, as the run wrote it.
            stream: line.text.startsWith("Diagnosis: ") ? "status" : "stderr",
            text: `${line.text}\n`,
            truncated: false,
          },
        })),
        ts: Date.now(),
      }),
    )
  })
}

const claude = {
  configured: true,
  provider: "anthropic",
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  keySet: true,
  models: [{ id: "claude-opus-5-5", label: "Claude Opus 5.5" }],
}

/**
 * The diagnosis routes: what is kept, and an answer for each press, with
 * every body the page sent.
 */
async function mockDiagnosis(
  page: Page,
  options: {
    settings?: Record<string, unknown>
    kept?: DeploymentRunDiagnosis | null
    answers?: DeploymentRunDiagnosis[]
  },
) {
  const asked: unknown[] = []
  let kept = options.kept ?? null
  const answers = [...(options.answers ?? [])]
  await page.route("**/api/v1/databases/assistant", (route) =>
    json(route, options.settings ?? claude),
  )
  await page.route("**/api/v1/deploy/7/runs/84/diagnosis", async (route) => {
    if (route.request().method() === "GET") return json(route, { diagnosis: kept })
    asked.push(route.request().postDataJSON())
    // A slow model: the page says it is reading while it waits.
    await new Promise((resolve) => setTimeout(resolve, 300))
    kept = answers.shift() ?? kept
    return json(route, { diagnosis: kept })
  })
  return asked
}

const lines: Lines = [
  { seq: 40, text: "#10 [build 5/5] RUN npm run build" },
  { seq: 41, text: "#10 0.441 Error: Cannot find module 'left-pad'" },
  { seq: 42, text: "#10 0.441 Require stack:" },
  {
    seq: 43,
    text: '#10 ERROR: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1',
  },
]

function answer(
  cause: string,
  overrides: Partial<DeploymentRunDiagnosis> = {},
): DeploymentRunDiagnosis {
  return {
    runId: 84,
    provider: "anthropic",
    model: "claude-opus-5-5",
    modelLabel: "Claude Opus 5.5",
    answer: {
      cause,
      evidence: [{ seq: 41, text: "#10 0.441 Error: Cannot find module 'left-pad'" }],
      steps: ["Add `left-pad` to `dependencies` in package.json.", "Deploy again."],
      unmatched: 0,
      text: cause,
    },
    sent: "A deployment run failed. What the dashboard recorded about it follows.\n41 | #10 0.441 Error: Cannot find module 'left-pad'",
    sentLines: 4,
    masked: 2,
    createdBy: "operator",
    createdAt: new Date().toISOString(),
    ...overrides,
  }
}

test("a failed run is asked about only when somebody presses, and the answer stays with it", async ({
  page,
}) => {
  await mockProject(page)
  await mockFailedRun(page, lines)
  const asked = await mockDiagnosis(page, {
    answers: [
      answer("build.js requires `left-pad`, which package.json never declares."),
      answer("The build needs left-pad.", { createdBy: "operator" }),
    ],
  })
  await page.goto("/deploy/7/runs/84")

  const ask = page.getByRole("region", { name: "Ask why it failed" })
  await expect(ask).toContainText("Ask Claude Opus 5.5 why this run failed")
  await expect(ask).toContainText("to Anthropic, with secret values masked. Nothing is changed.")
  expect(asked).toHaveLength(0)

  await ask.getByRole("button", { name: "Ask why it failed" }).click()
  await expect(ask.getByRole("button", { name: "Asking…" })).toBeDisabled()
  const read = page.getByRole("region", { name: "What the assistant read in this run" })
  await expect(read).toContainText("build.js requires left-pad, which package.json never declares.")
  expect(asked).toEqual([{ again: false }])
  await expect(read).toContainText("asked just now by operator")
  await expect(read.getByRole("list", { name: "Lines the answer quoted" })).toContainText(
    "Cannot find module 'left-pad'",
  )
  await expect(read.locator("ol li").filter({ hasText: "Deploy again." })).toBeVisible()
  await expect(read).toContainText("4 lines sent · 2 values masked")

  // What left the server is on the page, folded.
  await read.getByText("What was sent", { exact: true }).click()
  await expect(read.locator("pre")).toContainText("A deployment run failed.")

  // A quoted line opens the build logs at it.
  await read.getByRole("button", { name: "Show the line" }).click()
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toHaveText("Build")

  await read.getByRole("button", { name: "Ask again" }).click()
  await expect(read).toContainText("The build needs left-pad.")
  expect(asked).toEqual([{ again: false }, { again: true }])

  // The failure's own block is unchanged by it.
  await expect(page.getByRole("region", { name: "Why this deployment failed" })).toBeVisible()
})

test("a kept answer is read again, and one that kept to no shape is shown whole", async ({
  page,
}) => {
  await mockProject(page)
  await mockFailedRun(page, lines)
  const shapeless = answer("", {
    answer: { evidence: [], steps: [], text: "I could not tell from these lines." },
  })
  const asked = await mockDiagnosis(page, { kept: shapeless })
  await page.goto("/deploy/7/runs/84")

  const read = page.getByRole("region", { name: "What the assistant read in this run" })
  await expect(read).toContainText("I could not tell from these lines.")
  await expect(read.getByRole("list", { name: "Lines the answer quoted" })).toHaveCount(0)
  expect(asked).toHaveLength(0)
})

test("an administrator sets the assistant up from the failed run", async ({ page }) => {
  await mockProject(page)
  await mockFailedRun(page, lines)
  await mockDiagnosis(page, {
    settings: { configured: false, keySet: false, models: claude.models },
  })
  await page.goto("/deploy/7/runs/84")

  const ask = page.getByRole("region", { name: "Ask why it failed" })
  await expect(ask).toContainText("The assistant is off.")
  await ask.getByRole("button", { name: "Set up the assistant" }).click()
  await expect(page.getByRole("dialog", { name: "Set up the assistant" })).toBeVisible()
  await expect(page.getByRole("dialog")).toContainText("a failed deployment’s Ask why it failed")
})

test("an account that may not ask sees an answer somebody asked for, and nothing else", async ({
  page,
}) => {
  await mockProject(page)
  await mockFailedRun(page, lines)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "readonly" } }),
  )
  await mockDiagnosis(page, { kept: null })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByRole("region", { name: "Why this deployment failed" })).toBeVisible()
  await expect(page.getByRole("region", { name: "Ask why it failed" })).toHaveCount(0)

  await mockDiagnosis(page, { kept: answer("build.js requires left-pad.") })
  await page.reload()
  const read = page.getByRole("region", { name: "What the assistant read in this run" })
  await expect(read).toContainText("build.js requires left-pad.")
  await expect(read.getByRole("button", { name: "Ask again" })).toHaveCount(0)
})

// The pictures for the pull request, drawn from what a real model on this
// machine answered about a real failed build (`TestLiveFailedBuildIsDiagnosedByARealModel`):
// the transcript, the dashboard's own cause and the answer are replayed as
// that test recorded them.
test("run diagnosis, for the pull request @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go in")
  const shots = process.env.JD_SHOTS
  const settings = {
    configured: true,
    provider: "openai",
    model: liveDiagnosis.model,
    modelLabel: liveDiagnosis.model,
    baseUrl: "http://127.0.0.1:11434/v1",
    keySet: false,
    models: claude.models,
  }
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: width > 600 ? 1000 : 900 })
    await mockProject(page)
    await mockFailedRun(page, liveTranscript, liveCause)
    await mockDiagnosis(page, {
      settings,
      answers: [{ ...liveDiagnosis, createdAt: new Date().toISOString() }],
    })
    await page.goto("/deploy/7/runs/84")
    const ask = page.getByRole("region", { name: "Ask why it failed" })
    await expect(ask).toContainText("127.0.0.1:11434")
    await ask.evaluate((element) => element.scrollIntoView({ block: "center" }))
    await page.screenshot({ path: `${shots}/run-diagnosis-ask-${width}.png` })
    await ask.getByRole("button", { name: "Ask why it failed" }).click()
    const read = page.getByRole("region", { name: "What the assistant read in this run" })
    await expect(read).toBeVisible()
    await read.evaluate((element) => element.scrollIntoView({ block: "start" }))
    await page.mouse.wheel(0, -120)
    await page.screenshot({ path: `${shots}/run-diagnosis-answer-${width}.png` })
    await read.getByText("What was sent", { exact: true }).click()
    await read.locator("pre").evaluate((element) => element.scrollIntoView({ block: "center" }))
    await page.screenshot({ path: `${shots}/run-diagnosis-sent-${width}.png` })
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
  }
})
