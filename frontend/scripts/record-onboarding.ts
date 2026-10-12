import { chromium, expect, type Page } from "@playwright/test"
import { mkdir, rename, rm } from "node:fs/promises"
import { resolve } from "node:path"
import { mockOnboarding } from "../tests/browser/onboarding-fixture"

/**
 * The first-run question from sign-in to the sidebar it draws, recorded
 * against the onboarding fixture: a server with a project, databases and a
 * proxy, and a new account that has never been asked. The same walk as
 * `tests/browser/onboarding.spec.ts`, slowed to be watched, with the step
 * named in a caption, ending on the link from a deployment's domain into the
 * proxy the account hid.
 *
 *   bun run build && bun run start --port 43117   # in another terminal
 *   bun scripts/record-onboarding.ts
 */

const baseURL = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43117"
const output = resolve("../docs/audits/2026-10-12-onboarding")
const raw = resolve(output, "raw")
await mkdir(raw, { recursive: true })

const browser = await chromium.launch()
const context = await browser.newContext({
  baseURL,
  viewport: { width: 1280, height: 800 },
  recordVideo: { dir: raw, size: { width: 1280, height: 800 } },
})
const page = await context.newPage()
await mockOnboarding(page, { signedIn: false })

async function caption(page: Page, step: string, title: string) {
  await page.evaluate(
    ({ step, title }) => {
      document.querySelector("#recording-caption")?.remove()
      const box = document.createElement("div")
      box.id = "recording-caption"
      Object.assign(box.style, {
        // Top centre: the one place no step of this walk puts a control.
        position: "fixed",
        top: "12px",
        left: "50%",
        transform: "translateX(-50%)",
        zIndex: "10000",
        maxWidth: "520px",
        padding: "12px 18px",
        background: "#101820",
        color: "#f6f7f8",
        border: "1px solid #667582",
        borderRadius: "8px",
        pointerEvents: "none",
        font: "15px system-ui",
        boxShadow: "0 2px 12px #0008",
      })
      for (const [text, fontSize, color] of [
        [step, "11px", "#8bbaf5"],
        [title, "15px", "#f6f7f8"],
      ]) {
        const line = document.createElement("div")
        line.textContent = text
        Object.assign(line.style, { fontSize, color, marginTop: "3px", lineHeight: "1.35" })
        box.append(line)
      }
      document.body.append(box)
    },
    { step, title },
  )
}

const pause = (ms: number) => page.waitForTimeout(ms)

try {
  await page.goto("/")
  await expect(page).toHaveURL(/\/login$/)
  await caption(page, "1 · Sign in", "A new account signs in for the first time")
  await pause(900)
  await page.getByLabel("Username").pressSequentially("ion", { delay: 90 })
  await page
    .getByLabel("Password", { exact: true })
    .pressSequentially("Correct-Horse-Battery-9", { delay: 35 })
  await pause(400)
  await page.getByRole("button", { name: "Continue" }).click()

  await expect(page).toHaveURL(/\/welcome$/)
  await expect(page.getByText("already on this server")).toBeVisible()
  await caption(
    page,
    "2 · What is this server for?",
    "Asked once, straight after sign-in. What the server already runs is chosen for you.",
  )
  await page.screenshot({ path: resolve(output, "purpose-1280.png") })
  await pause(2600)
  const purposes = page.getByRole("group", { name: "Purposes" })
  await purposes.getByRole("button", { name: "Run databases" }).hover()
  await pause(500)
  await purposes.getByRole("button", { name: "Run databases" }).click()
  await caption(page, "2 · What is this server for?", "Pick any number — here, just shipping apps")
  await pause(1400)
  await purposes.getByRole("button", { name: "Manage containers" }).hover()
  await pause(700)
  await page.getByRole("button", { name: "Continue" }).hover()
  await pause(400)
  await page.getByRole("button", { name: "Continue" }).click()

  await expect(page.getByRole("switch", { name: "Deployments" })).toBeChecked()
  await caption(
    page,
    "3 · Which sections should the sidebar show?",
    "The answer fills in the switches; the preview is the sidebar you will get",
  )
  await pause(2600)
  const proxy = page.getByRole("switch", { name: "Proxy & TLS" })
  await proxy.scrollIntoViewIfNeeded()
  await proxy.hover()
  await pause(600)
  await proxy.click()
  await caption(
    page,
    "3 · Which sections should the sidebar show?",
    "Proxy & TLS off — it leaves the preview at once",
  )
  await pause(1800)
  const terminal = page.getByRole("switch", { name: "Terminal" })
  await terminal.scrollIntoViewIfNeeded()
  await terminal.hover()
  await pause(500)
  await terminal.click()
  await caption(page, "3 · Which sections should the sidebar show?", "Terminal on — it arrives")
  await page.screenshot({ path: resolve(output, "sidebar-1280.png") })
  await pause(1800)
  await page.getByRole("button", { name: "Finish" }).click()

  await expect(page.getByRole("heading", { level: 1, name: "Your sidebar is ready" })).toBeVisible()
  await caption(
    page,
    "4 · Ready",
    "Saved to the account. Hidden means out of the sidebar, never out of reach.",
  )
  await pause(1200)
  await page.screenshot({ path: resolve(output, "ready-1280.png") })
  await pause(2400)
  await page.getByRole("button", { name: "Open the dashboard" }).click()

  await expect(page).toHaveURL(/\/$/)
  const rail = page.getByRole("navigation", { name: "Sidebar", exact: true })
  await expect(rail.getByRole("link", { name: "Deployments" })).toBeVisible()
  await caption(
    page,
    "5 · The dashboard",
    "The sidebar and the Overview hold only what this account uses",
  )
  await pause(1500)
  await page.screenshot({ path: resolve(output, "overview-1280.png") })
  await pause(1800)

  await caption(
    page,
    "6 · A link into a hidden section",
    "A project's domain links to its proxy site — and Proxy & TLS is hidden",
  )
  await page.getByRole("link", { name: "api-production" }).first().click()
  await expect(page).toHaveURL(/\/deploy\/7$/)
  await pause(1600)
  await rail.getByRole("link", { name: "Domains" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/settings\/domains$/)
  const site = page.getByRole("link", { name: "just-dashboard-env-12.conf" })
  await expect(site).toBeVisible()
  await site.hover()
  await pause(1800)
  await site.click()

  await expect(page).toHaveURL(/\/proxy\/sites/)
  const note = page.locator("[data-slot=hidden-section-note]")
  await expect(note).toBeVisible()
  await caption(
    page,
    "7 · It still opens",
    "The rail goes into Proxy & TLS to say where you are, and that it is hidden",
  )
  await pause(1200)
  await page.screenshot({ path: resolve(output, "hidden-section-1280.png") })
  await note.getByRole("button", { name: "Show it" }).hover()
  await pause(2400)

  await caption(page, "8 · Search reaches every page", "Ctrl+K → certificates")
  await page.keyboard.press("Control+k")
  const dialog = page.getByRole("dialog", { name: "Command palette" })
  const input = dialog.getByRole("combobox", { name: "Search dashboard" })
  await expect(input).toBeFocused()
  await pause(900)
  await input.pressSequentially("certificates", { delay: 70 })
  await pause(2200)
  await page.keyboard.press("Escape")

  await page.goto("/account/workspace")
  await expect(page.getByRole("switch", { name: "Network" })).toBeVisible()
  await caption(
    page,
    "9 · Change it any time",
    "Account → Workspace: the same switches, or the question again",
  )
  await page.screenshot({ path: resolve(output, "workspace-1280.png") })
  await pause(1600)
  const network = page.getByRole("switch", { name: "Network" })
  await network.scrollIntoViewIfNeeded()
  await network.click()
  await pause(900)
  await page.getByRole("button", { name: "Save changes" }).click()
  await pause(2400)
} finally {
  const video = page.video()
  await context.close()
  if (video) await rename(await video.path(), resolve(output, "onboarding.webm"))
  await rm(raw, { recursive: true, force: true })
  await browser.close()
}
