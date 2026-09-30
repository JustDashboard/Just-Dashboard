import { expect, test } from "@playwright/test"
import type { DeploymentEngineRun } from "../../src/lib/types"
import { json, mockProject, run } from "./deploy-fixture"

for (const width of [390, 1280, 1720]) {
  test(`deployment filters keep the scroll position at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    await page.emulateMedia({ reducedMotion: width === 390 ? "reduce" : "no-preference" })
    await mockProject(page)
    const runs: DeploymentEngineRun[] = Array.from({ length: 24 }, (_, index) => {
      const requested = Date.now() - index * 3_600_000 - 60_000
      const requestedAt = new Date(requested).toISOString()
      return {
        ...run,
        slotClass: "heavy" as const,
        id: 84 - index,
        runNumber: 24 - index,
        state: index === 2 ? "failed" : "succeeded",
        requestedAt,
        claimedAt: requestedAt,
        queuedAt: requestedAt,
        endedAt: new Date(requested + 16_000).toISOString(),
        terminalCode: index === 2 ? "build_failed" : undefined,
      }
    })
    runs.push({ ...runs[0], id: 40, runNumber: 0, environmentId: 13 })
    await page.route("**/api/v1/deploy/7/runs**", (route) => {
      if (new URL(route.request().url()).searchParams.get("view") !== "engine") {
        return route.fallback()
      }
      return json(route, { runs, running: false })
    })
    await page.goto("/deploy/7/deployments")
    const history = page.locator("section").filter({
      has: page.getByRole("heading", { name: "Deployments", exact: true }),
    })
    const scroller = page.locator('[data-slot="sidebar-inset"] > div').last()
    const all = history.getByRole("button", { name: /^All/ })
    await expect(all).toHaveText(/All\s*24/)
    await expect(history.getByRole("listitem")).toHaveCount(24)
    const delivery = page.locator("section").filter({
      has: page.getByRole("heading", { name: "Delivery", exact: true }),
    })
    await expect(delivery.locator('[data-slot="stat-tile"]')).toHaveCount(0)
    await expect(delivery.getByText("75%", { exact: true })).toBeVisible()
    await expect(delivery.getByText("9 of 12 decided releases", { exact: true })).toBeVisible()
    await expect(delivery.getByText("2.1", { exact: true })).toBeVisible()
    await expect(delivery.getByText("1m 35s", { exact: true })).toBeVisible()
    await expect(delivery.getByText("1.5h", { exact: true })).toBeVisible()
    await expect(
      delivery.getByText("mean over 2 recovered failures", { exact: true }),
    ).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`history-${width}.png`) })

    // Put the controls near the top, where narrowing a long list used to
    // clamp the shell's scroll and move the controls away from the pointer.
    await scroller.evaluate((element) => {
      const filters = element.querySelector('[data-slot="panel-toolbar"]')!
      element.scrollTop += filters.getBoundingClientRect().top - 180
    })
    const before = await all.boundingBox()
    const scrollTop = await scroller.evaluate((element) => element.scrollTop)
    expect(scrollTop).toBeGreaterThan(0)
    await page.screenshot({ path: testInfo.outputPath(`filters-${width}.png`) })

    await history.getByRole("button", { name: /^Failed/ }).click()
    await expect(history.getByRole("listitem")).toHaveCount(1)
    await expect(history.getByText("#22 Deploy", { exact: true })).toBeVisible()
    await expect
      .poll(() => scroller.evaluate((element) => element.scrollTop))
      .toBeCloseTo(scrollTop, 0)
    const after = await all.boundingBox()
    expect(after!.x).toBeCloseTo(before!.x, 0)
    expect(after!.y).toBeCloseTo(before!.y, 0)
    expect(after!.width).toBeCloseTo(before!.width, 0)

    await history.getByRole("button", { name: /^Ready/ }).click()
    await expect(history.getByRole("listitem")).toHaveCount(23)
    await expect(history.getByText("#22 Deploy", { exact: true })).toHaveCount(0)
    await expect
      .poll(() => scroller.evaluate((element) => element.scrollTop))
      .toBeCloseTo(scrollTop, 0)

    await all.click()
    await expect(history.getByRole("listitem")).toHaveCount(24)
    await expect
      .poll(() => scroller.evaluate((element) => element.scrollTop))
      .toBeCloseTo(scrollTop, 0)

    const cancelled = history.getByRole("button", { name: /^Cancelled/ })
    await cancelled.focus()
    await cancelled.press("Enter")
    await expect(cancelled).toHaveAttribute("aria-pressed", "true")
    await expect(cancelled).toBeFocused()
    await expect(history.getByText("No deployments match", { exact: true })).toBeVisible()
    await expect(history.getByRole("listitem")).toHaveCount(0)
    await expect
      .poll(() => scroller.evaluate((element) => element.scrollTop))
      .toBeCloseTo(scrollTop, 0)
    await history.getByRole("button", { name: "Clear filters", exact: true }).click()
    await expect(all).toHaveAttribute("aria-pressed", "true")
    await expect(history.getByRole("listitem")).toHaveCount(24)

    await history.getByRole("combobox", { name: "Environment", exact: true }).click()
    await page.getByRole("option", { name: "Environment 13", exact: true }).click()
    await expect(all).toHaveText(/All\s*1/)
    await expect(history.getByRole("listitem")).toHaveCount(1)
    await expect
      .poll(() => scroller.evaluate((element) => element.scrollTop))
      .toBeCloseTo(scrollTop, 0)
    await history.getByRole("combobox", { name: "Environment", exact: true }).click()
    await page.getByRole("option", { name: "Production", exact: true }).click()
    await expect(history.getByRole("listitem")).toHaveCount(24)
  })
}
