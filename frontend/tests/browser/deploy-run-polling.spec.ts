import { expect, test } from "@playwright/test"
import { mockProject } from "./deploy-fixture"

/**
 * Pages that show a project's runs follow a run in flight every two seconds
 * rather than every five, so its stage on a card or the Overview keeps up
 * with the run. The fixture's run is in flight (verifying).
 */

async function countReads(page: import("@playwright/test").Page, path: RegExp) {
  const reads: number[] = []
  page.on("request", (request) => {
    const url = new URL(request.url())
    if (request.method() === "GET" && path.test(url.pathname)) reads.push(Date.now())
  })
  return reads
}

test("the fleet follows a run in flight every two seconds", async ({ page }) => {
  await mockProject(page)
  await page.clock.install({ time: new Date("2026-09-03T12:00:00Z") })
  const reads = await countReads(page, /^\/api\/v1\/deploy\/$/)
  await page.goto("/deploy")
  await expect(page.getByRole("link", { name: /api-production/ }).first()).toBeVisible()
  const before = reads.length
  await page.clock.runFor(10_000)
  // Five reads in ten seconds at two seconds each; two at the resting five.
  await expect.poll(() => reads.length - before).toBeGreaterThanOrEqual(4)
})

test("a project's pages follow its run in flight every two seconds", async ({ page }) => {
  await mockProject(page)
  await page.clock.install({ time: new Date("2026-09-03T12:00:00Z") })
  const reads = await countReads(page, /^\/api\/v1\/deploy\/7\/runs$/)
  await page.goto("/deploy/7")
  await expect(page.getByRole("heading", { level: 1 }).first()).toBeAttached()
  await expect.poll(() => reads.length).toBeGreaterThanOrEqual(1)
  const before = reads.length
  await page.clock.runFor(10_000)
  await expect.poll(() => reads.length - before).toBeGreaterThanOrEqual(4)
})
