import { expect, test } from "@playwright/test"
import type { RollbackWatch } from "../../src/lib/types"
import { expectSaved, mockProject, saveSettings } from "./deploy-fixture"

/**
 * Metric-gated rollback on the project's pages: the window after a cutover
 * is read on the overview as four figures with its two verbs, a rollback
 * that happened stays there with the reading that decided it, the release
 * list marks which release was taken back, and the terms are edited on
 * Runtime settings beside the release strategy.
 */
function watch(overrides: Partial<RollbackWatch> = {}): RollbackWatch {
  const now = Date.now()
  return {
    id: 4,
    projectId: 7,
    environmentId: 12,
    runId: 84,
    releaseId: 20,
    warmReleaseId: 19,
    warmRuntimeId: "4f1c2d3e4a5b6c7d8e9f",
    state: "watching",
    policy: {
      keepWarm: true,
      windowSeconds: 600,
      automatic: true,
      errorRateMargin: 5,
      latencyFactor: 2,
      minRequests: 20,
    },
    cutoverAt: new Date(now - 180_000).toISOString(),
    expiresAt: new Date(now + 420_000).toISOString(),
    baseline: { requests: 412, failures: 2, errorRate: 0.0049, p95: 118, from: "", until: "" },
    observed: { requests: 63, failures: 1, errorRate: 0.0159, p95: 131, from: "", until: "" },
    verdict: {
      code: "within_baseline",
      judged: true,
      tripped: false,
      latencyJudged: true,
      reason:
        "Within the previous release's baseline over 63 requests: 5xx 1.6% (0.5% before), p95 131 ms (118 ms before).",
    },
    attempts: 0,
    createdAt: new Date(now - 180_000).toISOString(),
    releaseNumber: 2,
    warmReleaseNumber: 1,
    ...overrides,
  }
}

test.describe("metric-gated rollback", () => {
  test("an open window reads as four figures and rolls back instantly", async ({ page }) => {
    await mockProject(page, { rollbackWatch: watch() })
    await page.goto("/deploy/7")
    const panel = page.locator("section", { hasText: "Release #2 on watch" }).first()
    await expect(page.getByText("Release #2 on watch")).toBeVisible()
    await expect(panel.getByText("5xx since cutover")).toBeVisible()
    await expect(panel.getByText("1.6%", { exact: true })).toBeVisible()
    await expect(panel.getByText("0.5% before", { exact: true })).toBeVisible()
    await expect(panel.getByText("131ms", { exact: true })).toBeVisible()
    await expect(panel.getByText("release #1 paused")).toBeVisible()
    await expect(panel.getByText(/Within the previous release's baseline/)).toBeVisible()

    await page.getByRole("button", { name: "Roll back instantly" }).click()
    const dialog = page.getByRole("dialog", { name: "Roll back instantly" })
    await expect(dialog.getByText(/Nothing is built or started anew/)).toBeVisible()
    await dialog.getByRole("button", { name: "Roll back" }).click()
    await expect(page).toHaveURL(/\/deploy\/7\/runs\/85$/)
  })

  test("keeping the new release ends the window", async ({ page }) => {
    await mockProject(page, { rollbackWatch: watch() })
    await page.goto("/deploy/7")
    await page.getByRole("button", { name: "Keep release #2" }).click()
    await expect(page.getByText("Kept release #2; release #1 was removed")).toBeVisible()
    await expect(page.getByText("Release #2 on watch")).toHaveCount(0)
  })

  test("a rollback stays on the overview with the reading that decided it", async ({ page }) => {
    const ended = new Date(Date.now() - 120_000).toISOString()
    await mockProject(page, {
      rollbackWatch: watch({
        state: "rolled_back",
        rollbackRunId: 85,
        endReason: "rolled_back",
        endedAt: ended,
        endMessage:
          "Traffic went back to release #1. 5xx answers rose to 38.3% (46 of 120 requests) from 0.5% before.",
        verdict: {
          code: "error_rate",
          judged: true,
          tripped: true,
          latencyJudged: true,
          reason: "5xx answers rose to 38.3% (46 of 120 requests) from 0.5% before.",
        },
      }),
    })
    await page.goto("/deploy/7")
    await expect(page.getByText("Release #2 rolled back")).toBeVisible()
    await expect(page.getByText("Rolled back automatically")).toBeVisible()
    await expect(page.getByText(/5xx answers rose to 38\.3%/)).toBeVisible()
    await expect(page.getByRole("link", { name: /Open the rollback/ })).toHaveAttribute(
      "href",
      "/deploy/7/runs/85",
    )
    await expect(page.getByRole("button", { name: "Roll back instantly" })).toHaveCount(0)
  })

  test("a clean window is the expected answer and is not drawn", async ({ page }) => {
    await mockProject(page, {
      rollbackWatch: watch({
        state: "closed",
        endReason: "window_passed",
        endedAt: new Date().toISOString(),
      }),
    })
    await page.goto("/deploy/7")
    await expect(page.getByText("Production").first()).toBeVisible()
    await expect(page.getByText(/on watch|rolled back/)).toHaveCount(0)
  })

  test("the terms are saved from Runtime settings", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/settings/runtime")
    const section = page.locator("#rollback-watch")
    await expect(section.getByText("After a release goes live")).toBeVisible()
    await expect(section.getByText(/kept warm for 10 minutes/)).toBeVisible()
    await section.getByLabel("Window").fill("15")
    await section.getByLabel("Requests before deciding").fill("40")
    await expect(section.getByText(/kept warm for 15 minutes/)).toBeVisible()
    const saved = page.waitForRequest(
      (request) => request.method() === "PUT" && request.url().endsWith("/rollback-policy"),
    )
    await saveSettings(page)
    const body = (await saved).postDataJSON()
    expect(body).toMatchObject({ keepWarm: true, windowSeconds: 900, minRequests: 40 })
    await expectSaved(page)
  })

  test("a warm release kept for its visitors says how many it answered", async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await mockProject(page, {
      rollbackWatch: watch({
        warmRunning: true,
        warmServed: { requests: 37, failures: 0, errorRate: 0, from: "", until: "" },
      }),
    })
    await page.goto("/deploy/7")
    const panel = page.locator("section", { hasText: "Release #2 on watch" }).first()
    await expect(panel.getByText("release #1 answered 37 of its visitors' requests")).toBeVisible()
    await expect(panel.getByText("release #1 paused")).toHaveCount(0)
    await panel.screenshot({ path: testInfo.outputPath("skew-watch-1280.png") })

    await page.getByRole("button", { name: "Roll back instantly" }).click()
    const dialog = page.getByRole("dialog", { name: "Roll back instantly" })
    await expect(dialog.getByText(/kept running since the cutover for the visitors/)).toBeVisible()
    await expect(dialog.getByText(/its visitors are answered by release #1/)).toBeVisible()
  })

  test("skew protection is saved with the plan, beside the warm window", async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await mockProject(page)
    await page.goto("/deploy/7/settings/runtime")
    const section = page.locator("#rollback-watch")
    const toggle = section.getByRole("switch", {
      name: "Keep each visitor on the release that answered them",
    })
    await expect(toggle).not.toBeChecked()
    await toggle.click()
    await expect(
      section.getByText(/A visitor stays on the release that answered them for up to 10 minutes/),
    ).toBeVisible()
    const policies: string[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/rollback-policy"))
        policies.push(request.url())
    })
    const saved = page.waitForRequest(
      (request) => request.method() === "PUT" && request.url().endsWith("/configuration"),
    )
    await saveSettings(page)
    const body = (await saved).postDataJSON()
    expect(body.runtime).toMatchObject({ skewProtection: true, strategy: "blue_green" })
    await expectSaved(page)
    // Only the plan changed, so the terms were not written again.
    expect(policies).toHaveLength(0)
    await expect(toggle).toBeChecked()
    await section.screenshot({ path: testInfo.outputPath("skew-settings-1280.png") })

    // Turning keep warm off says what is left of it.
    await section.getByRole("switch", { name: "Keep the previous release warm" }).click()
    await expect(section.getByText(/stay on it only while it drains/)).toBeVisible()
  })

  test("a stop-first plan cannot keep visitors on their release", async ({ page }) => {
    await mockProject(page, { runtimePlan: { strategy: "stop_first" } })
    await page.goto("/deploy/7/settings/runtime")
    const section = page.locator("#rollback-watch")
    await expect(
      section.getByRole("switch", { name: "Keep each visitor on the release that answered them" }),
    ).toBeDisabled()
  })
})
