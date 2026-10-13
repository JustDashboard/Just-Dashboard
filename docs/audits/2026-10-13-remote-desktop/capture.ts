import { chromium } from "../../../frontend/node_modules/playwright/index.mjs"
import {
  ENGINE,
  instruction,
  mockRemoteDesktop,
  OFFICE,
  overview,
} from "../../../frontend/tests/browser/remote-desktop-fixture"

// Run against a production build of this tree, with the fixtures' computers:
//   JD_BROWSER_BASE_URL=http://127.0.0.1:<port> bun docs/audits/2026-10-13-remote-desktop/capture.ts
// The live session screenshots beside these were taken against a running backend, guacd and a
// throwaway XFCE desktop; see the pull request.
const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43117"
const directory = import.meta.dir
const browser = await chromium.launch()

async function shoot(
  name: string,
  path: string,
  options: {
    width?: number
    height?: number
    prepare?: Parameters<typeof mockRemoteDesktop>[1]
    before?: (page: import("playwright").Page) => Promise<void>
    scrollTo?: string
  } = {},
) {
  const page = await browser.newPage({ viewport: { width: options.width ?? 1440, height: options.height ?? 900 } })
  await mockRemoteDesktop(page, options.prepare)
  await options.before?.(page)
  await page.goto(base + path)
  await page.waitForLoadState("networkidle")
  await page.waitForTimeout(1600)
  if (options.scrollTo) {
    await page
      .getByRole("heading", { name: options.scrollTo, exact: true })
      .evaluate((heading) => heading.scrollIntoView({ block: "start" }))
    await page.waitForTimeout(800)
  }
  await page.screenshot({ path: `${directory}/${name}.png` })
  await page.close()
}

await shoot("overview-1440-fold", "/remote-desktop")
await shoot("overview-1440-cards", "/remote-desktop", { scrollTo: "Computers" })
await shoot("overview-768", "/remote-desktop", { width: 768, height: 1024 })
await shoot("overview-375", "/remote-desktop", { width: 375, height: 812 })
await shoot("install-1440", "/remote-desktop", {
  prepare: {
    overview: overview({ engine: { ...ENGINE, state: "missing", pulled: false, address: undefined }, computers: [], sessions: [] }),
  },
})
await shoot("add-computer-1440", "/remote-desktop/new")
await shoot("turn-on-windows-1440", "/remote-desktop/new?peer=n06", { prepare: { probeOpen: () => false } })
await shoot("turn-on-mac-1440", "/remote-desktop/new?peer=mac", { prepare: { probeOpen: () => true } })
await shoot("refused-1440", `/remote-desktop/${OFFICE}`, {
  before: (page) =>
    page.routeWebSocket(/\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/, (socket) => {
      socket.send(instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9") + instruction("error", "Aborted. See logs.", "519"))
      socket.close()
    }),
})

await browser.close()
