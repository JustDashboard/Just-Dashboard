import { expect, test, type Page } from "@playwright/test"
import { deployment, expectSaved, json, mockProject, project, saveSettings } from "./deploy-fixture"
import type { SleepStatus } from "../../src/lib/types"

/**
 * Scale to zero (`deploy/sleep.go`): its terms in Runtime settings, saved
 * with the plan, and an environment asleep by design on the overview — said
 * as Asleep rather than Stopped, with when and why, its site preview not
 * loaded (loading it would wake it), and Wake now.
 */

const asleepSince = new Date(Date.now() - 42 * 60_000).toISOString()
const lastRequest = new Date(Date.now() - 58 * 60_000).toISOString()

const asleep: SleepStatus = {
  enabled: true,
  idleMinutes: 15,
  wakeTimeoutSeconds: 60,
  holdSeconds: 30,
  state: "asleep",
  since: asleepSince,
  reason: "No request reached it for 15 minutes; the last was at 11:02 UTC.",
  lastRequestAt: lastRequest,
  switch: { driver: "caddy", ready: true, on: true },
  recent: [
    {
      id: 2,
      environmentId: 12,
      releaseId: 20,
      idleMinutes: 15,
      reason: "No request reached it for 15 minutes; the last was at 11:02 UTC.",
      sleptAt: asleepSince,
    },
    {
      id: 1,
      environmentId: 12,
      releaseId: 20,
      idleMinutes: 15,
      reason: "No request reached it for 15 minutes since release #4 went live.",
      sleptAt: new Date(Date.now() - 6 * 3600_000).toISOString(),
      wokeAt: lastRequest,
      wakeTrigger: "request",
      wakeDetail: "api.example.test",
      wakeSeconds: 1.2,
    },
  ],
  asleepSeconds: 5 * 3600,
}

async function mockAsleep(page: Page) {
  const dashboard = await mockProject(page)
  let state: SleepStatus = asleep
  let woken = 0
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: {
        ...deployment,
        activeRun: undefined,
        pendingChanges: false,
        asleepSince: state.state === "asleep" ? asleepSince : undefined,
      },
    }),
  )
  await page.route("**/api/v1/deploy/7/environments/12/sleep", (route) => json(route, state))
  await page.route("**/api/v1/deploy/7/environments/12/sleep/wake", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    woken++
    state = {
      ...asleep,
      state: "awake",
      since: undefined,
      recent: [{ ...asleep.recent[0], wokeAt: new Date().toISOString(), wakeTrigger: "manual" }],
    }
    await json(route, {
      outcome: { status: 200, reason: "Woken in 1.4 s.", seconds: 1.4 },
      sleep: state,
    })
  })
  return { dashboard, woken: () => woken }
}

test("an environment asleep by design says when and why, and wakes from Wake now", async ({
  page,
}) => {
  const mocked = await mockAsleep(page)
  let framed = 0
  page.on("request", (request) => {
    if (request.url().includes("/preview-frame")) framed++
  })
  await page.goto("/deploy/7")
  await expect(page.getByText("Asleep", { exact: true }).first()).toBeVisible()
  await expect(page.getByText("Stopped", { exact: true })).toHaveCount(0)
  const panel = page.locator("section, div").filter({ hasText: "Asleep by design" }).first()
  await expect(panel).toBeVisible()
  await expect(
    page.getByText("No request reached it for 15 minutes; the last was at 11:02 UTC.").first(),
  ).toBeVisible()
  await expect(page.getByText("≤ 30 s", { exact: true })).toBeVisible()
  await expect(
    page.getByText(/Last woken by a request to api\.example\.test in 1\.2 s/),
  ).toBeVisible()
  // The preview is not loaded: loading it would be a request that wakes it.
  await expect(
    page.getByText("The next visit wakes it; this preview does not, so it stays asleep"),
  ).toBeVisible()
  expect(framed).toBe(0)

  await page.getByRole("button", { name: "Wake now", exact: true }).first().click()
  await expect.poll(() => mocked.woken()).toBe(1)
  await expect(page.getByText("Asleep by design")).toHaveCount(0)
})

test("the sleep terms are saved with the runtime plan, and refused out of bounds", async ({
  page,
}) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/runtime")
  const section = page.locator("#sleep")
  await expect(section.getByText("Sleep when idle", { exact: true })).toBeVisible()
  await expect(section).toContainText("Off · the container runs whether or not anyone visits")

  await section.getByRole("switch", { name: "Stop the container while nobody visits" }).click()
  await expect(section).toContainText(
    "On · stopped after 15 min without a request, woken within 60 s",
  )
  await expect(section).toContainText("A request while it sleeps waits up to 30 s.")
  await section.getByLabel("Idle time").fill("2")
  let sent = false
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith("/environments/12/configuration"))
      sent = true
  })
  await saveSettings(page)
  await expect(section.getByText("The idle time is a whole number from 5 to 1440.")).toBeVisible()
  expect(sent).toBe(false)

  await section.getByLabel("Idle time").fill("45")
  await section.getByLabel("Wake limit").fill("20")
  await expect(section).toContainText(
    "On · stopped after 45 min without a request, woken within 20 s",
  )
  await expect(section).toContainText("A request while it sleeps waits up to 20 s.")
  const saved = page.waitForRequest(
    (request) =>
      request.method() === "PUT" && request.url().endsWith("/environments/12/configuration"),
  )
  await saveSettings(page)
  const body = (await saved).postDataJSON() as { runtime: { sleep?: unknown } }
  expect(body.runtime.sleep).toEqual({ enabled: true, idleMinutes: 45, wakeTimeoutSeconds: 20 })
  await expectSaved(page)
  await expect(section.getByText("Unsaved changes")).toHaveCount(0)
})

test("a refusal the server makes for the switch is said under it", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    await route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_plan",
          field: "runtime.sleep.enabled",
          message:
            'The schedule "nightly export" runs a command inside the container, which a sleeping environment\'s stopped container would fail. Pause that schedule, or leave sleeping off.',
        },
      }),
    })
  })
  await page.goto("/deploy/7/settings/runtime")
  const section = page.locator("#sleep")
  await section.getByRole("switch", { name: "Stop the container while nobody visits" }).click()
  await saveSettings(page)
  await expect(
    section.getByText(/The schedule "nightly export" runs a command inside the container/),
  ).toBeVisible()
})
