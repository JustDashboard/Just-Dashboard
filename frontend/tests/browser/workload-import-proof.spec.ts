import { expect, test } from "@playwright/test"
import { mockWorkloadImport } from "./workload-import-fixture"

test.use({ video: { mode: "on", size: { width: 1280, height: 900 } } })

test("captures discovery, recovery and the migration review", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockWorkloadImport(page)
  await page.goto("/deploy/import")
  await expect(page.getByRole("button", { name: "Review bet-bot" })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("discovery-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await expect(page.getByRole("button", { name: "Review migration" })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("review-1280.png"), fullPage: true })
  await page.setViewportSize({ width: 1720, height: 1000 })
  await page.screenshot({ path: testInfo.outputPath("review-1720.png"), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.getByRole("button", { name: "Review migration" }).click()
  await expect(page.getByRole("heading", { name: "Ready to adopt this deployment?" })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("migration-1280.png"), fullPage: true })
})
