import { expect, test } from "@playwright/test"
import {
  expectSaved,
  json,
  mockProject,
  now,
  run as fixtureRun,
  saveSettings,
  steps as fixtureSteps,
} from "./deploy-fixture"
import type { DeploymentCanary, DeploymentEngineRun, DeploymentStep } from "../../src/lib/types"

/**
 * A canary release: the strategy and its terms on the Runtime settings, and
 * the run page's trial — the new release beside the live one, read side by
 * side as it comes in, with its two verbs.
 */

test.use({ timezoneId: "UTC" })

const run = fixtureRun as DeploymentEngineRun

// The run at its canary: checked, and holding a share of the route.
const canarySteps = [
  ...(fixtureSteps as unknown as DeploymentStep[])
    .filter(
      (step) => !["activate", "retire_previous", "record_release", "notify"].includes(step.key),
    )
    .map((step) =>
      step.key === "verify_readiness" || step.key === "verify_smoke"
        ? { ...step, state: "passed" as const }
        : step,
    ),
  {
    ...(fixtureSteps[10] as unknown as DeploymentStep),
    id: 120,
    key: "canary",
    ordinal: 12,
    state: "running" as const,
  },
  ...(fixtureSteps as unknown as DeploymentStep[])
    .filter((step) =>
      ["activate", "retire_previous", "record_release", "notify"].includes(step.key),
    )
    .map((step) => ({ ...step, ordinal: step.ordinal + 1 })),
]

function trial(overrides: Partial<DeploymentCanary> = {}): DeploymentCanary {
  const started = Date.now() - 4 * 60_000
  return {
    id: 5,
    projectId: 7,
    environmentId: 12,
    runId: 84,
    releaseId: 21,
    liveReleaseId: 20,
    percent: 10,
    durationSeconds: 600,
    autoPromote: true,
    policy: {
      keepWarm: true,
      windowSeconds: 600,
      automatic: true,
      errorRateMargin: 5,
      latencyFactor: 2,
      minRequests: 20,
    },
    state: "running",
    startedAt: new Date(started).toISOString(),
    endsAt: new Date(started + 600_000).toISOString(),
    live: { requests: 1840, failures: 3, errorRate: 3 / 1840, p95: 84, from: now, until: now },
    candidate: { requests: 203, failures: 1, errorRate: 1 / 203, p95: 91, from: now, until: now },
    verdict: {
      code: "within_baseline",
      judged: true,
      tripped: false,
      latencyJudged: true,
      reason:
        "Within the live release's figures over 203 requests: 5xx 0.5% (0.2% on the live release), p95 91 ms (84 ms on the live release).",
    },
    releaseNumber: 2,
    liveReleaseNumber: 1,
    ...overrides,
  }
}

test("the run page reads the canary beside the live release and promotes it", async ({
  page,
}, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  await mockProject(page)
  let canary = trial()
  const decisions: string[] = []
  await page.route("**/api/v1/deploy/7/environments/12/canary", (route) => json(route, { canary }))
  await page.route("**/api/v1/deploy/7/environments/12/canary/*", async (route) => {
    const verb = route.request().url().split("/").at(-1) ?? ""
    decisions.push(verb)
    canary = { ...canary, decision: verb as "promote", decidedBy: "operator" }
    await route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify(canary),
    })
  })
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({ type: "snapshot", data: { run, steps: canarySteps }, ts: Date.now() }),
    )
  })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/deploy/7/runs/84")

  const panel = page.getByRole("region", { name: "Canary trial" })
  await expect(panel.getByText("Release #2 on 10% of the requests")).toBeVisible()
  const table = panel.getByRole("table", { name: "The two releases side by side" })
  await expect(table.getByRole("row", { name: /#2 canary 203 0\.5% 91 ?ms/ })).toBeVisible()
  await expect(table.getByRole("row", { name: /#1 live 1,840 0\.2% 84 ?ms/ })).toBeVisible()
  await expect(panel.getByText(/Within the live release's figures over 203 requests/)).toBeVisible()
  await expect(panel.getByRole("progressbar", { name: "Canary window" })).toBeVisible()
  await panel.screenshot({ path: testInfo.outputPath("canary-run-1280.png") })

  await panel.getByRole("button", { name: "Promote now" }).click()
  await page.getByRole("button", { name: "Promote", exact: true }).click()
  await expect(panel.getByText("Promoting release #2")).toBeVisible()
  expect(decisions).toEqual(["promote"])
  await expect(panel.getByRole("button", { name: "Promote now" })).toHaveCount(0)

  await page.setViewportSize({ width: 390, height: 844 })
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth),
  ).toBeLessThanOrEqual(1)
})

test("a canary that did worse reads as aborted with its reading", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  await mockProject(page)
  const aborted = trial({
    state: "aborted",
    endReason: "failed",
    candidate: { requests: 60, failures: 30, errorRate: 0.5, p95: 120, from: now, until: now },
    endMessage:
      "Release #2 did worse than release #1 during its canary, so it was removed and release #1 kept every request: 5xx answers rose to 50.0% (30 of 60 requests) from 0.2% on the live release.",
    endedAt: now,
  })
  await page.route("**/api/v1/deploy/7/environments/12/canary", (route) =>
    json(route, { canary: aborted }),
  )
  const ended = canarySteps.map((step) =>
    step.key === "canary" ? { ...step, state: "failed" as const } : step,
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({
        type: "snapshot",
        data: { run: { ...run, state: "failed", terminalCode: "canary_failed" }, steps: ended },
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")
  const panel = page.getByRole("region", { name: "Canary trial" })
  await expect(panel.getByText("Release #2's canary aborted")).toBeVisible()
  await expect(panel.getByText("Aborted on its figures")).toBeVisible()
  await expect(panel.getByText(/5xx answers rose to 50\.0%/)).toBeVisible()
  await expect(panel.getByRole("button", { name: "Abort" })).toHaveCount(0)
})

test("the strategy offers a canary with its terms, and saves them", async ({ page }, testInfo) => {
  const writes: Array<{ runtime: Record<string, unknown> }> = []
  await mockProject(page, { runtimePlan: { mounts: [] } })
  await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
    if (route.request().method() === "PUT")
      writes.push(route.request().postDataJSON() as { runtime: Record<string, unknown> })
    await route.fallback()
  })
  await page.goto("/deploy/7/settings/runtime")
  const runtimeCard = page.getByRole("form", { name: "Runtime" })
  await runtimeCard.getByRole("button", { name: "Release as a canary" }).click()
  const share = runtimeCard.getByLabel("Share of requests")
  await expect(share).toHaveValue("10")
  await expect(runtimeCard.getByLabel("Window")).toHaveValue("10")
  await share.fill("25")
  await expect(runtimeCard.getByText("Canary on 25% for 10 minutes")).toBeVisible()
  await page.setViewportSize({ width: 1280, height: 900 })
  await runtimeCard
    .locator("#releases")
    .screenshot({ path: testInfo.outputPath("canary-settings-1280.png") })
  await saveSettings(page)
  await expectSaved(page)
  expect(writes.at(-1)?.runtime.strategy).toBe("canary")
  expect(writes.at(-1)?.runtime.canary).toEqual({
    percent: 25,
    durationSeconds: 600,
    autoPromote: true,
  })

  // Another strategy leaves the terms out, which the server would refuse.
  await runtimeCard.getByRole("button", { name: "Release blue / green" }).click()
  await saveSettings(page)
  await expectSaved(page)
  expect(writes.at(-1)?.runtime.strategy).toBe("blue_green")
  expect("canary" in (writes.at(-1)?.runtime ?? {})).toBe(false)
})

test("a canary needs what blue / green needs, and a domain", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/runtime")
  const runtimeCard = page.getByRole("form", { name: "Runtime" })
  // The fixture's writable /data mount rules out both.
  await expect(
    runtimeCard.getByText(
      "Blue / green and canary are unavailable: /data is writable — two releases cannot share it.",
    ),
  ).toBeVisible()
  await expect(runtimeCard.getByRole("button", { name: "Release as a canary" })).toHaveCount(0)
})
