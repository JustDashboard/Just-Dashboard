import { chromium } from "../../../frontend/node_modules/playwright/index.mjs"
import { mockKeys } from "../../../frontend/tests/browser/api-keys-fixture"

const browser = await chromium.launch()
const context = await browser.newContext({
  viewport: { width: 1280, height: 1000 },
  reducedMotion: "reduce",
  recordVideo: {
    dir: `${import.meta.dir}/.recording`,
    size: { width: 1280, height: 1000 },
  },
})
const page = await context.newPage()
const video = page.video()!
await mockKeys(page)
await page.goto(`${process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43128"}/account/keys`)
await page.waitForLoadState("networkidle")
await page.getByRole("button", { name: "More actions for github-actions" }).click()
await page.getByRole("menuitem", { name: "Replace key", exact: true }).click()
await page.getByRole("dialog", { name: "Replace API key" }).waitFor()
await page.waitForTimeout(1000)
await page.getByRole("button", { name: "Create replacement" }).click()
const secret = page.getByRole("dialog", { name: "Your new key" })
await secret.getByText("vpsd_synthetic_key_101", { exact: true }).waitFor()
await page.waitForTimeout(1500)
await secret.getByRole("button", { name: "Revoke previous key" }).click()
const confirmation = page.getByRole("dialog", {
  name: "Revoke previous key",
  exact: true,
})
await confirmation.waitFor()
await page.waitForTimeout(1000)
await confirmation.getByRole("button", { name: "Revoke previous key" }).click()
await secret.getByText("Previous key is inactive", { exact: true }).waitFor()
await page.waitForTimeout(1000)
await secret.getByRole("button", { name: "Done", exact: true }).click()
await page.getByRole("button", { name: "Show inactive keys" }).click()
await page.getByRole("button", { name: "More actions for old-laptop" }).click()
await page.getByRole("menuitem", { name: "Delete record", exact: true }).click()
await page.getByRole("dialog", { name: "Delete key record" }).waitFor()
await page.waitForTimeout(1000)
await page.getByRole("dialog").getByRole("button", { name: "Delete record", exact: true }).click()
await page.waitForTimeout(1000)
await context.close()
await video.saveAs(`${import.meta.dir}/replacement-and-cleanup.webm`)
await video.delete()
await browser.close()
