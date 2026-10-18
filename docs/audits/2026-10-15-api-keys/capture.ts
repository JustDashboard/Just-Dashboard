import { chromium } from "../../../frontend/node_modules/playwright/index.mjs"
import { mockKeys } from "../../../frontend/tests/browser/api-keys-fixture"

const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43128"
const phase = process.argv[2] ?? "after"
if (!["before", "after"].includes(phase)) throw new Error("Use before or after")
const browser = await chromium.launch()
for (const width of [1280, 1720, 390]) {
  const page = await browser.newPage({
    viewport: { width, height: width === 390 ? 844 : 1000 },
    reducedMotion: "reduce",
  })
  await mockKeys(page)
  await page.goto(`${base}/account/keys`)
  await page.waitForLoadState("networkidle")
  await page.screenshot({
    path: `${import.meta.dir}/${phase}-${width}.png`,
    fullPage: true,
  })
  if (phase === "after" && width === 1280) {
    await page.getByRole("button", { name: "Show inactive keys" }).click()
    await page.getByRole("button", { name: "More actions for old-laptop" }).click()
    await page.screenshot({ path: `${import.meta.dir}/after-history.png` })
    await page.getByRole("menuitem", { name: "Delete record", exact: true }).click()
    await page.getByRole("dialog", { name: "Delete key record" }).waitFor()
    await page.screenshot({ path: `${import.meta.dir}/after-delete-confirmation.png` })
    await page.getByRole("dialog").getByRole("button", { name: "Cancel", exact: true }).click()
    await page.getByRole("button", { name: "More actions for github-actions" }).click()
    await page.getByRole("menuitem", { name: "Replace key" }).click()
    await page.getByRole("dialog", { name: "Replace API key" }).waitFor()
    await page.screenshot({ path: `${import.meta.dir}/after-replace.png` })
    await page.getByRole("button", { name: "Create replacement" }).click()
    await page.getByText("vpsd_synthetic_key_101", { exact: true }).waitFor()
    await page.screenshot({ path: `${import.meta.dir}/after-handoff.png` })
  }
  await page.close()
}
await browser.close()
