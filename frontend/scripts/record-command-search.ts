import { chromium, expect } from "@playwright/test"
import { mkdir } from "node:fs/promises"
import { resolve } from "node:path"
import { CONTAINER_ID, mockCommandSearch } from "../tests/browser/command-search-fixture"

const baseURL = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43117"
const output = resolve("../docs/audits/2026-10-04-command-search/evidence")
await mkdir(output, { recursive: true })
const browser = await chromium.launch()
const context = await browser.newContext({
  baseURL,
  viewport: { width: 1280, height: 800 },
  recordVideo: { dir: output, size: { width: 1280, height: 800 } },
  reducedMotion: "reduce",
})
const page = await context.newPage()
const traffic = await mockCommandSearch(page)
const dialog = page.getByRole("dialog", { name: "Command palette" })
const input = dialog.getByRole("combobox", { name: "Search dashboard" })
const selected = dialog.getByRole("listbox").getByRole("option", { selected: true })

async function caption(title: string, keys: string) {
  await page.evaluate(
    ({ title, keys }) => {
      document.querySelector("#recording-caption")?.remove()
      const box = document.createElement("div")
      box.id = "recording-caption"
      Object.assign(box.style, {
        position: "fixed",
        bottom: "16px",
        right: "20px",
        zIndex: "10000",
        padding: "12px 20px",
        background: "#101820",
        color: "#f6f7f8",
        border: "1px solid #667582",
        borderRadius: "8px",
        pointerEvents: "none",
        font: "15px system-ui",
        boxShadow: "0 2px 12px #0008",
      })
      for (const [text, fontSize, color] of [
        ["Keyboard-only demo · API fixture host", "11px", "#aab7c2"],
        [title, "16px", "#f6f7f8"],
        [keys, "13px", "#8bbaf5"],
      ]) {
        const line = document.createElement("div")
        line.textContent = text
        Object.assign(line.style, { fontSize, color, marginTop: "4px" })
        box.append(line)
      }
      document.body.append(box)
    },
    { title, keys },
  )
}

try {
  await page.goto("/account")
  await caption("Find the site serving a domain from anywhere", "Ctrl+K → domain: shop.example.com")
  await page.waitForTimeout(1200)
  await page.keyboard.press("Control+k")
  await expect(input).toBeFocused()
  await input.pressSequentially("domain: shop.example.com", { delay: 65 })
  await expect(selected).toContainText("shop-ingress")
  await page.screenshot({ path: resolve(output, "domain-search-1280.png") })
  await page.waitForTimeout(1600)
  await input.press("Enter")
  await expect(page).toHaveURL(/\/proxy\/sites\/shop-ingress$/)
  await expect(page.getByRole("heading", { name: "shop-ingress", exact: true })).toBeAttached()
  await caption("The result opens the site's routing and logs", "Enter → shop-ingress")
  await page.waitForTimeout(1800)

  await caption("Jump straight to the running container", "Ctrl+K → container: shop-web → Enter")
  await page.keyboard.press("Control+k")
  await expect(input).toBeFocused()
  await input.pressSequentially("container: shop-web", { delay: 65 })
  await expect(selected).toContainText("shop-web")
  await page.waitForTimeout(1400)
  await input.press("Enter")
  await expect(page).toHaveURL(new RegExp(`/docker/containers/${CONTAINER_ID}$`))
  await expect(page.getByRole("tab", { name: "Overview", exact: true })).toBeVisible()
  await caption(
    "Inspect the container without searching a list",
    "shop-web · running · nginx:alpine",
  )
  await page.waitForTimeout(1800)

  await caption("Return to the exact previous destination", "Ctrl+K → Enter")
  await page.keyboard.press("Control+k")
  await expect(selected).toContainText("Back to shop-ingress")
  await page.waitForTimeout(1600)
  await input.press("Enter")
  await expect(page).toHaveURL(/\/proxy\/sites\/shop-ingress$/)
  await caption(
    "Back at the same site, entirely by keyboard",
    "Ctrl+K → Enter switches between the last two places",
  )
  await page.waitForTimeout(1800)

  await page.keyboard.press("Control+k")
  await input.pressSequentially("shop", { delay: 100 })
  await expect(
    dialog.getByRole("listbox").getByRole("option").filter({ hasText: "Shop architecture" }),
  ).toBeVisible()
  await caption(
    "One name finds related resources across the dashboard",
    "Projects · domains · databases · containers · repositories · backups",
  )
  await page.waitForTimeout(1600)
  await input.press("ArrowDown")
  await input.press("ArrowDown")
  await page.waitForTimeout(1000)
  await page.evaluate(() => document.querySelector("#recording-caption")?.remove())
  await page.screenshot({ path: resolve(output, "global-search-1280.png") })
  await page.setViewportSize({ width: 1720, height: 1000 })
  await page.screenshot({ path: resolve(output, "global-search-1720.png") })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: resolve(output, "global-search-mobile.png") })
  expect(traffic.mutations).toEqual([])
  await page.close()
  await page.video()!.saveAs(resolve(output, "keyboard-workflow.webm"))
  await page.video()!.delete()
} finally {
  await context.close()
  await browser.close()
}
console.log(`Recording and screenshots: ${output}`)
