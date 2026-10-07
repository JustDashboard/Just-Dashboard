import { chromium } from "../../../frontend/node_modules/playwright/index.mjs"
import { mockHost } from "../../../frontend/tests/browser/packages-fixture"
import { mockHostLogs } from "../../../frontend/tests/browser/host-logs-fixture"

// Run against a production build of the tree whose appearance is being recorded.
const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43371"
const phase = process.argv[2] ?? "after"
if (!["before", "after"].includes(phase)) throw new Error("Use before or after")
const directory = import.meta.dir
const browser = await chromium.launch()
for (const width of [1440, 1280, 1720, 390]) {
  const page = await browser.newPage({
    viewport: { width, height: width === 390 ? 844 : 1000 },
    reducedMotion: "reduce",
  })
  await mockHost(page)
  await mockHostLogs(page)
  await page.goto(`${base}/packages`)
  await page.waitForLoadState("networkidle")
  await page.screenshot({ path: `${directory}/${phase}-page-${width}.png` })
  await page
    .getByRole("row", { name: /nginx/ })
    .getByRole("button", { name: "nginx", exact: true })
    .click()
  const sheet = page.getByRole("dialog")
  await sheet.getByText("GPL-2.0", { exact: true }).waitFor()
  await page.screenshot({ path: `${directory}/${phase}-sheet-${width}.png` })
  if (width === 1440) {
    await sheet.getByRole("button", { name: "Remove and purge", exact: true }).click()
    const confirm = page.getByRole("dialog", {
      name: "Remove nginx and its configuration",
      exact: true,
    })
    await confirm.waitFor()
    await page.screenshot({ path: `${directory}/${phase}-confirmation.png` })
    await confirm.getByRole("button", { name: "Cancel", exact: true }).click()
  }
  await page.keyboard.press("Escape")
  const strip = page.getByRole("navigation", { name: "Package views" })
  if (width === 1440) {
    await strip.getByRole("button", { name: /^Updates/ }).click()
    await page.screenshot({ path: `${directory}/${phase}-updates.png` })
    await strip.getByRole("button", { name: "Add software" }).click()
    await page.screenshot({ path: `${directory}/${phase}-catalogue.png` })
    await page.getByPlaceholder(/What do you need/).fill("htop")
    await page.getByText("2 matches").waitFor()
    await page.screenshot({ path: `${directory}/${phase}-search.png` })
  }
  if (phase === "after" && width === 390) {
    await page.locator("[data-slot='table-container']").scrollIntoViewIfNeeded()
    await page.screenshot({ path: `${directory}/after-table-phone.png` })
  }
  await page.close()
}
if (phase === "after") {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    recordVideo: {
      dir: "/tmp/jd-packages-recording",
      size: { width: 1440, height: 1000 },
    },
  })
  const page = await context.newPage()
  await mockHost(page)
  await page.goto(`${base}/packages`)
  await page.waitForLoadState("networkidle")
  await page.getByRole("button", { name: "Only Docker packages" }).click()
  await page.waitForTimeout(700)
  await page.screenshot({ path: `${directory}/after-software-filter.png` })
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Inspect update for nginx" }).click()
  await page.getByRole("button", { name: "Copy nginx", exact: true }).waitFor()
  await page.waitForTimeout(700)
  await page.getByRole("button", { name: "Inspect dependency libc6", exact: true }).click()
  await page.waitForTimeout(700)
  await page.goBack()
  await page.waitForTimeout(700)
  await page.keyboard.press("Escape")
  await page
    .getByRole("navigation", { name: "Package views" })
    .getByRole("button", { name: "Add software" })
    .click()
  await page.getByPlaceholder(/What do you need/).fill("htop")
  await page.getByText("2 matches").waitFor()
  await page.waitForTimeout(700)
  const video = page.video()!
  await context.close()
  await video.saveAs(`${directory}/workspace.webm`)
}
await browser.close()
