import { chromium } from "../../../frontend/node_modules/playwright/index.mjs"
import {
  N8N,
  POSTGRES,
  RUNNER,
  STANDALONE,
  mockContainerPage,
} from "../../../frontend/tests/browser/container-page-fixture"

// Run against a production build of the tree whose appearance is being recorded.
const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43219"
const phase = process.argv[2] ?? "after"
// Names of the shots to take, when not all of them: `bun capture.ts after usage storage`.
const only = new Set(process.argv.slice(3))
if (!["before", "after"].includes(phase)) throw new Error("Use before or after")
const directory = import.meta.dir
const browser = await chromium.launch()

async function shot(width: number, address: string, name: string, full = false) {
  if (only.size > 0 && !only.has(name)) return
  // The views scroll inside the page rather than the page itself, so a whole
  // tab is shot through a window tall enough to hold it.
  const page = await browser.newPage({
    viewport: { width, height: full ? 2600 : width === 390 ? 844 : 1000 },
    reducedMotion: "reduce",
  })
  await mockContainerPage(page)
  await page.goto(`${base}${address}`, {
    waitUntil: "domcontentloaded",
    timeout: 90_000,
  })
  // A few frames of the live socket, so the readings have moved.
  await page.waitForTimeout(6000)
  await page.screenshot({ path: `${directory}/${phase}-${name}-${width}.png` })
  await page.close()
}

for (const width of [1440, 1280, 390]) {
  await shot(width, `/docker/containers/${N8N}?tab=overview`, "overview", width === 390)
}
await shot(1440, `/docker/containers/${N8N}?tab=overview`, "overview-full", true)
await shot(1720, `/docker/containers/${N8N}?tab=overview`, "overview")
await shot(1440, `/docker/containers/${RUNNER}?tab=overview`, "looping", true)
await shot(1440, `/docker/containers/${STANDALONE}?tab=configure`, "configuration-standalone")
await shot(1440, `/docker/containers/${N8N}?tab=usage`, "usage", true)
await shot(1440, `/docker/containers/${N8N}?tab=env`, "environment")
await shot(1440, `/docker/containers/${N8N}?tab=mounts`, "storage")
await shot(1440, `/docker/containers/${N8N}?tab=logs`, "logs")
await shot(1440, `/docker/containers/${POSTGRES}?tab=logs`, "logs-postgres")
await shot(1440, `/docker/containers/${N8N}?tab=inspect`, "inspect")
await shot(1440, `/docker/containers/${N8N}?tab=configure`, "configuration")
await shot(1440, `/docker/containers/${N8N}?tab=shell`, "shell")
await browser.close()
