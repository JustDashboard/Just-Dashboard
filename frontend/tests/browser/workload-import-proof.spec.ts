import { expect, test } from "@playwright/test"
import { mockWorkloadImport } from "./workload-import-fixture"

test.use({ video: { mode: "on", size: { width: 1280, height: 900 } } })

test("captures discovery, review and the imported outcome", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockWorkloadImport(page)
  await page.goto("/deploy/import")
  await expect(page.getByRole("button", { name: "Review bet-bot" })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("discovery-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await expect(page.getByRole("button", { name: "Import workload" })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("review-1280.png"), fullPage: true })
  await page.setViewportSize({ width: 1720, height: 1000 })
  await page.screenshot({ path: testInfo.outputPath("review-1720.png"), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.getByRole("button", { name: "Import workload" }).click()
  await expect(page.getByRole("heading", { name: "Workload imported" })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("imported-1280.png"), fullPage: true })
})
