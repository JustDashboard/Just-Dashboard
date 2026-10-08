import { chromium } from "../../../frontend/node_modules/playwright/index.mjs"
import { N8N, RUNNER, mockContainerPage } from "../../../frontend/tests/browser/container-page-fixture"

// Run against a production build of the tree whose appearance is being recorded.
const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43219"
const phase = process.argv[2] ?? "after"
if (!["before", "after"].includes(phase)) throw new Error("Use before or after")
const directory = import.meta.dir
const browser = await chromium.launch()

async function shot(width: number, address: string, name: string, full = false) {
  const page = await browser.newPage({
    viewport: { width, height: width === 390 ? 844 : 1000 },
    reducedMotion: "reduce",
  })
  await mockContainerPage(page)
  await page.goto(`${base}${address}`)
  await page.waitForLoadState("networkidle").catch(() => {})
  // A few frames of the live socket, so the readings have moved.
  await page.waitForTimeout(6000)
  await page.screenshot({ path: `${directory}/${phase}-${name}-${width}.png`, fullPage: full })
  await page.close()
}

for (const width of [1440, 1280, 390]) {
  await shot(width, `/docker/containers/${N8N}?tab=overview`, "overview", width === 390)
}
await shot(1440, `/docker/containers/${N8N}?tab=overview`, "overview-full", true)
await shot(1720, `/docker/containers/${N8N}?tab=overview`, "overview")
await shot(1440, `/docker/containers/${RUNNER}?tab=overview`, "looping", true)
await shot(1440, `/docker/containers/${N8N}?tab=usage`, "usage", true)
await shot(1440, `/docker/containers/${N8N}?tab=env`, "environment")
await shot(1440, `/docker/containers/${N8N}?tab=mounts`, "storage")
await browser.close()
