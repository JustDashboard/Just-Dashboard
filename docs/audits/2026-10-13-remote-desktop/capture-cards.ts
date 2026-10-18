import { chromium } from "../../../frontend/node_modules/playwright/index.mjs"
import { mockFleet } from "../../../frontend/tests/browser/database-fleet-fixture"
import { mockShowcase } from "../../../frontend/tests/browser/proxy-fixtures"

// The other two cards that drew a hue down their left edge, with it removed:
//   JD_BROWSER_BASE_URL=http://127.0.0.1:<port> bun docs/audits/2026-10-13-remote-desktop/capture-cards.ts
const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43117"
const directory = import.meta.dir
const browser = await chromium.launch()

for (const [name, path, mock, heading] of [
  ["cards-databases-1440", "/databases", (page) => mockFleet(page), "All databases"],
  ["cards-sites-1440", "/proxy/sites", (page) => mockShowcase(page), "Routing"],
] as const) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  await mock(page)
  await page.goto(base + path)
  await page.waitForLoadState("networkidle")
  await page.waitForTimeout(1600)
  if (heading) {
    await page
      .getByRole("heading", { name: new RegExp(`^${heading}`) })
      .first()
      .evaluate((element) => element.scrollIntoView({ block: "start" }))
    await page.waitForTimeout(800)
  }
  await page.screenshot({ path: `${directory}/${name}.png` })
  await page.close()
}
await browser.close()
