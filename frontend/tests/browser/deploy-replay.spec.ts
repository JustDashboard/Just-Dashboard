import { expect, test, type Page } from "@playwright/test"
import {
  expectSaved,
  mockProject,
  now,
  run as fixtureRun,
  saveSettings,
  steps as fixtureSteps,
} from "./deploy-fixture"
import type { DeploymentEngineRun, DeploymentStep, ReplayReport } from "../../src/lib/types"

/**
 * Traffic replay before the cutover (`deploy/replay.go`): its terms on
 * Runtime settings, beside how releases replace each other, and its report on
 * the run page — a passing replay read as how many recorded requests the
 * candidate answered like the live release, a failing one as the requests it
 * answered differently and the reason the release stopped before traffic
 * moved.
 */

test.use({ timezoneId: "UTC" })

const run = fixtureRun as DeploymentEngineRun
const baseSteps = fixtureSteps as unknown as DeploymentStep[]

const drifted: ReplayReport = {
  replayed: true,
  domains: ["api.example.test"],
  target: "10.20.0.7:3000",
  read: 812,
  eligible: 64,
  requests: 50,
  answered: 50,
  drifted: 2,
  driftPercent: 4,
  maxStatusDrift: 2,
  latencyFactor: 2,
  latencyRecorded: true,
  latencyJudged: true,
  recordedP95: 41.5,
  candidateP95: 38.2,
  verdict: {
    code: "status_drift",
    tripped: true,
    reason:
      "2 of 50 replayed requests (4.0%) answered in another status class than the live release recorded; the plan allows 2.0%.",
  },
  drifts: [
    {
      method: "GET",
      path: "/pricing",
      query: "plan=team",
      recordedStatus: 200,
      candidateStatus: 404,
      recordedMs: 31,
      candidateMs: 4.2,
    },
    {
      method: "HEAD",
      path: "/feed.xml",
      recordedStatus: 200,
      candidateStatus: 0,
      error: "timeout",
      recordedMs: 12,
    },
  ],
  concurrency: 4,
  timeoutSeconds: 60,
  timedOut: false,
  durationMs: 1840,
  recovery: { candidateStopped: true, runtimeRestored: true, routeRestored: true },
}

const passed: ReplayReport = {
  ...drifted,
  drifted: 0,
  driftPercent: 0,
  verdict: {
    code: "within_drift",
    tripped: false,
    reason:
      "Within the live release's record over 50 requests: 0 drifted (0.0%), p95 38ms (42ms recorded).",
  },
  drifts: [],
  recovery: undefined,
}

const failedMessage = `${drifted.verdict.reason} The release stopped before the cutover and the live release was not touched.`

/** The run's steps with the replay between the smoke checks and the cutover. */
function withReplay(
  state: DeploymentStep["state"],
  report: ReplayReport,
  error?: { code: string; message: string },
) {
  const out: DeploymentStep[] = []
  for (const step of baseSteps) {
    if (step.key === "verify_readiness" || step.key === "verify_smoke") {
      out.push({ ...step, state: "passed", endedAt: "2026-09-03T12:00:06Z" })
      continue
    }
    if (step.key === "activate") {
      out.push({
        ...step,
        id: 130,
        key: "replay_traffic",
        state,
        endedAt: "2026-09-03T12:00:08Z",
        evidence: report as unknown as Record<string, unknown>,
        errorCode: error?.code,
        errorMessage: error?.message,
      })
    }
    out.push(
      step.ordinal >= 12
        ? {
            ...step,
            ordinal: step.ordinal + 1,
            state: error ? "pending" : "passed",
            startedAt: error ? undefined : now,
          }
        : step,
    )
  }
  return out
}

async function openRun(
  page: Page,
  overrides: Partial<DeploymentEngineRun>,
  steps: DeploymentStep[],
) {
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({
        type: "snapshot",
        data: { run: { ...run, ...overrides }, steps },
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")
}

test("a drifting candidate's replay names the requests it answered differently", async ({
  page,
}) => {
  await mockProject(page)
  await openRun(
    page,
    {
      state: "failed",
      endedAt: now,
      terminalCode: "replay_status_drift",
      terminalReason: failedMessage,
    },
    withReplay("failed", drifted, { code: "replay_status_drift", message: failedMessage }),
  )

  await expect(
    page.getByText("Candidate answered recorded requests differently").first(),
  ).toBeVisible()
  const list = page.getByRole("list", { name: "Deployment steps" })
  // The replay is part of the Verify stage, after the smoke checks.
  const verify = list.getByRole("list", { name: "Verify", exact: true })
  const step = verify.getByRole("button", { name: /Traffic replay/ })
  await expect(step).toBeVisible()
  await step.click()

  const record = page.getByRole("region", { name: "Traffic replay" })
  await expect(record.getByText("replay_status_drift", { exact: true })).toBeVisible()
  const report = record.getByRole("region", { name: "Replay report" })
  await expect(report.getByText("of 64 recorded requests", { exact: true })).toBeVisible()
  await expect(report.getByText("4% · 2% allowed", { exact: true })).toBeVisible()
  await expect(report.getByText("38 ms", { exact: true })).toBeVisible()
  await expect(report.getByText("42 ms recorded", { exact: true })).toBeVisible()
  const requests = report.getByRole("list", { name: "Requests answered differently" })
  await expect(requests.getByRole("listitem")).toHaveCount(2)
  await expect(requests.getByRole("listitem").first()).toContainText("GET/pricing?plan=team")
  await expect(requests.getByRole("listitem").first()).toContainText("recorded 200")
  await expect(requests.getByText("404", { exact: true })).toBeVisible()
  await expect(requests.getByText("no answer (timeout)", { exact: true })).toBeVisible()
  await expect(report.getByText(/no cookies, credentials or bodies/)).toBeVisible()
  await expect(
    report.getByText(/The candidate was stopped and the live release was left serving/),
  ).toBeVisible()
  // The report's own fields are not listed a second time as raw facts.
  await expect(record.getByText("Drift percent", { exact: true })).toHaveCount(0)
  // Nothing was cut over.
  await expect(list.getByRole("button", { name: /^Switch traffic/ })).toContainText("Waiting")
})

test("a passing replay reads as the record matched", async ({ page }) => {
  await mockProject(page)
  await openRun(page, { state: "succeeded", endedAt: now }, withReplay("passed", passed))
  const list = page.getByRole("list", { name: "Deployment steps" })
  const step = list.getByRole("button", { name: /Traffic replay/ })
  // Its row says what it concluded while another step is open.
  await expect(step).toContainText("50 requests · none drifted · p95 38 ms (42 ms recorded)")
  await step.click()
  const report = page.getByRole("region", { name: "Replay report" })
  await expect(report.getByText(/Within the live release's record over 50 requests/)).toBeVisible()
  await expect(report.getByRole("list", { name: "Requests answered differently" })).toHaveCount(0)
})

test("a replay that did not run says why", async ({ page }) => {
  await mockProject(page)
  const skipped = {
    replayed: false,
    reason: "no request has been recorded for this deployment yet",
    drifts: [],
  } as unknown as ReplayReport
  await openRun(page, { state: "succeeded", endedAt: now }, withReplay("skipped", skipped))
  const list = page.getByRole("list", { name: "Deployment steps" })
  const step = list.getByRole("button", { name: /Traffic replay/ })
  await expect(step).toContainText("no request has been recorded for this deployment yet")
  await step.click()
  const record = page.getByRole("region", { name: "Traffic replay" })
  await expect(
    record.getByText(
      "Not replayed: no request has been recorded for this deployment yet. The release went on to the cutover on its health checks alone.",
    ),
  ).toBeVisible()
  await expect(record.getByRole("region", { name: "Replay report" })).toHaveCount(0)
})

test("the replay terms are saved with the runtime plan", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/runtime")
  const section = page.locator("#replay")
  await expect(section.getByText("Traffic replay", { exact: true })).toBeVisible()
  await expect(section).toContainText("Off · releases go live on their health checks alone")

  await section.getByRole("switch", { name: "Replay recorded requests before the cutover" }).click()
  await expect(section).toContainText("On · the last 50 requests, 2% drift, p95 within 2×")
  // A count past the server's bound is named under its field, and nothing is sent.
  await section.getByLabel("Requests replayed").fill("900")
  let sent = false
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith("/environments/12/configuration"))
      sent = true
  })
  await saveSettings(page)
  await expect(section.getByText("The count is a whole number from 1 to 500.")).toBeVisible()
  expect(sent).toBe(false)

  await section.getByLabel("Requests replayed").fill("120")
  await section.getByLabel("Only paths under").fill(" /docs\n/blog\n/docs")
  await expect(section).toContainText(
    "On · the last 120 requests under 2 prefixes, 2% drift, p95 within 2×",
  )
  const saved = page.waitForRequest(
    (request) =>
      request.method() === "PUT" && request.url().endsWith("/environments/12/configuration"),
  )
  await saveSettings(page)
  const body = (await saved).postDataJSON() as { runtime: { replay?: unknown } }
  expect(body.runtime.replay).toEqual({ enabled: true, requests: 120, paths: ["/docs", "/blog"] })
  await expectSaved(page)
  // The saved terms read back as saved, not as an edit.
  await expect(section.getByLabel("Only paths under")).toHaveValue("/docs\n/blog")
  await expect(section.getByText("Unsaved changes")).toHaveCount(0)
})
