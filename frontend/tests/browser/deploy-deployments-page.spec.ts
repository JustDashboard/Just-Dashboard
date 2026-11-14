import { expect, test, type Page } from "@playwright/test"
import type { DeploymentEngineRun } from "../../src/lib/types"
import { risen } from "./risen"
import { json, mockProject, run, user } from "./deploy-fixture"

/**
 * A project's Deployments after its pass to the deploy standard: Delivery
 * opens on the Overview's sentence with the figures as one line of facts, the
 * block holds its place while it loads, and a run's time is said in words —
 * amber when it ran long — with no bar under it.
 */

test.use({ timezoneId: "UTC" })

/**
 * Eight finished runs a day apart: one failed with a named cause, one that
 * took four times as long as the rest, and the others ordinary.
 */
function history(): DeploymentEngineRun[] {
  return Array.from({ length: 8 }, (_, index) => {
    const requested = Date.now() - (index + 1) * 86_400_000
    const requestedAt = new Date(requested).toISOString()
    const seconds = index === 3 ? 400 : 100
    return {
      ...run,
      slotClass: "heavy" as const,
      id: 84 - index,
      runNumber: 8 - index,
      state: index === 1 ? "failed" : "succeeded",
      requestedAt,
      claimedAt: requestedAt,
      queuedAt: requestedAt,
      endedAt: new Date(requested + seconds * 1000).toISOString(),
      terminalCode: index === 1 ? "build_failed" : undefined,
    }
  })
}

async function withHistory(page: Page) {
  await mockProject(page)
  const runs = history()
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    if (new URL(route.request().url()).searchParams.get("view") !== "engine") {
      return route.fallback()
    }
    return json(route, { runs, running: false })
  })
}

const section = (page: Page, name: string) =>
  page.locator("section").filter({ has: page.getByRole("heading", { name, exact: true }) })

for (const width of [1280, 390]) {
  test(`delivery leads with a sentence and the list is where it will be, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await withHistory(page)
    let release = () => {}
    const gate = new Promise<void>((resolve) => (release = resolve))
    await page.route("**/api/v1/deploy/7/insights?*", async (route) => {
      await gate
      await route.fallback()
    })
    await page.goto("/deploy/7/deployments")

    const delivery = section(page, "Delivery")
    const list = section(page, "Deployments")
    await expect(list.getByRole("listitem")).toHaveCount(8)
    await expect(delivery.locator('[data-slot="delivery-facts"]')).toHaveCount(0)
    await risen(page)
    const waiting = Math.round(
      (await delivery.locator('[data-slot="delivery-waiting"] > div').last().boundingBox())!.y,
    )

    release()
    await expect(
      delivery.getByText("9 of 12 releases succeeded in the last 30 days", { exact: true }),
    ).toBeVisible()
    // The sentence stands on the source's own mark, as on the Overview.
    await expect(delivery.locator('img[src="/logos/github.svg"]')).toBeVisible()
    const facts = delivery.locator('[data-slot="delivery-facts"]')
    await expect(facts).toContainText("75% succeeded")
    await expect(facts).toContainText("2.1 a week")
    await expect(facts).toContainText("1.5h to recover")
    await expect(facts.getByTitle("9 of 12 decided releases").locator("span")).toHaveClass(
      /text-warning/,
    )
    // No figure over a caption, no tile, and both charts under the lead.
    await expect(delivery.locator("dl")).toHaveCount(0)
    await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
    await expect(page.locator('[data-slot="stat-tile"]')).toHaveCount(0)
    await expect(delivery.getByTestId("insights-daily")).toBeVisible()
    await expect(delivery.getByTestId("insights-durations")).toBeVisible()
    // A reason still narrows the list to the failed runs.
    await expect(delivery.getByText(/Health gate failed/)).toBeVisible()

    // The charts land where their placeholders stood. On a phone the facts
    // wrap to as many lines as the figures need, so only a desk is held to
    // the pixel.
    await risen(page)
    if (width >= 1280) {
      expect(
        Math.round((await delivery.locator('[data-slot="delivery-charts"]').boundingBox())!.y),
      ).toBe(waiting)
    }
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  })

  test(`a run's time is words, amber when it ran long, and every row is one height at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await withHistory(page)
    await page.goto("/deploy/7/deployments")
    const list = section(page, "Deployments")
    const rows = list.getByRole("listitem")
    await expect(rows).toHaveCount(8)

    // No bar under a time: the list is not a chart of itself.
    await expect(list.getByRole("meter")).toHaveCount(0)
    const slow = rows.filter({ hasText: "#5 Deploy" })
    await expect(slow.getByText("6m 40s", { exact: true })).toHaveClass(/text-warning/)
    await expect(slow.getByText("6m 40s", { exact: true })).toHaveAttribute(
      "title",
      "More than twice as long as most of the runs listed",
    )
    const ordinary = rows.filter({ hasText: "#8 Deploy" })
    await expect(ordinary.getByText("1m 40s", { exact: true })).not.toHaveClass(/text-warning/)

    // The failed run names its cause, the slow one is amber, the rest say
    // neither: one height all the same.
    await expect(rows.filter({ hasText: "#7 Deploy" })).toContainText("Failed")
    await risen(page)
    const heights = await rows.evaluateAll((all) =>
      all.map((row) => Math.round(row.getBoundingClientRect().height)),
    )
    expect(new Set(heights).size).toBe(1)
  })
}

test("a read-only account reads the history and is offered no verb it cannot use", async ({
  page,
}) => {
  await withHistory(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/deploy/7/deployments")
  const list = section(page, "Deployments")
  await expect(list.getByRole("listitem")).toHaveCount(8)
  await expect(section(page, "Delivery").locator('[data-slot="delivery-facts"]')).toBeVisible()

  // The row's menu still opens the run; nothing in it starts or undoes one.
  await list
    .getByRole("listitem")
    .first()
    .getByRole("button", { name: /^Actions for/ })
    .click()
  const menu = page.getByRole("menu")
  await expect(menu.getByRole("menuitem", { name: "Open deployment" })).toBeVisible()
  for (const verb of ["Redeploy", "Retry", "Cancel", "Roll back to this release", "Pin release"]) {
    await expect(menu.getByRole("menuitem", { name: verb })).toHaveCount(0)
  }
})

test("in a window with no failure the deployments do not move when the figures land", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await withHistory(page)
  let release = () => {}
  const gate = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/deploy/7/insights?*", async (route) => {
    await gate
    await json(route, {
      projectId: 7,
      windowDays: 30,
      generatedAt: new Date().toISOString(),
      runs: 14,
      succeeded: 14,
      failed: 0,
      rolledBack: 0,
      cancelled: 0,
      successRate: 1,
      failureStreak: 0,
      medianDurationSeconds: 131,
      p95DurationSeconds: 181,
      deploysPerWeek: 3.27,
      meanRecoverySeconds: 0,
      recoveredFailures: 0,
      daily: Array.from({ length: 31 }, (_, index) => ({
        date: `2026-08-${String(1 + index).padStart(2, "0")}`,
        succeeded: index % 2 === 0 ? 1 : 0,
        failed: 0,
        cancelled: 0,
        medianDurationSeconds: index % 2 === 0 ? 131 : 0,
      })),
      topFailures: [],
    })
  })
  await page.goto("/deploy/7/deployments")
  const list = section(page, "Deployments")
  await expect(list.getByRole("listitem")).toHaveCount(8)
  await risen(page)
  const waiting = Math.round((await list.boundingBox())!.y)

  release()
  const delivery = section(page, "Delivery")
  await expect(
    delivery.getByText("14 releases in the last 30 days, all successful", { exact: true }),
  ).toBeVisible()
  // Nothing failed, so nothing is said about recovering.
  const facts = delivery.locator('[data-slot="delivery-facts"]')
  await expect(facts).toContainText("100% succeeded")
  await expect(facts).toContainText("2m 11s usually, 19 in 20 inside 3m 1s")
  await expect(facts).not.toContainText("recover")
  await risen(page)
  expect(Math.round((await list.boundingBox())!.y)).toBe(waiting)
})
