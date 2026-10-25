import { chromium, type Page } from "@playwright/test"
import { mkdir } from "node:fs/promises"
import { resolve } from "node:path"
import { freshMap, mockServerMap } from "../tests/browser/server-map-fixture"

/**
 * Screenshots and a recording of the server map, for a pull request or an
 * audit: `bun scripts/record-server-map.ts <out-dir>` against a built frontend
 * (`JD_BROWSER_BASE_URL`, default the browser gate's port). Every frame says
 * it is the API fixture's host, not a real server.
 */

const baseURL = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43117"
const out = resolve(process.argv[2] ?? "server-map-recording")
await mkdir(out, { recursive: true })

const node = (page: Page, id: string) => page.locator(`.react-flow__node[data-id="${id}"]`)

async function caption(page: Page, title: string, how: string) {
  await page.evaluate(
    ({ title, how }) => {
      document.querySelector("#recording-caption")?.remove()
      const box = document.createElement("div")
      box.id = "recording-caption"
      Object.assign(box.style, {
        position: "fixed",
        bottom: "18px",
        left: "50%",
        transform: "translateX(-50%)",
        zIndex: "10000",
        padding: "10px 18px",
        background: "#101820",
        color: "#f6f7f8",
        border: "1px solid #667582",
        borderRadius: "8px",
        pointerEvents: "none",
        font: "15px system-ui",
        boxShadow: "0 2px 12px #0008",
        textAlign: "center",
      })
      for (const [text, fontSize, color] of [
        ["Server map · API fixture host", "11px", "#aab7c2"],
        [title, "16px", "#f6f7f8"],
        [how, "13px", "#8bbaf5"],
      ]) {
        const line = document.createElement("div")
        line.textContent = text
        Object.assign(line.style, { fontSize, color, marginTop: "3px" })
        box.append(line)
      }
      document.body.append(box)
    },
    { title, how },
  )
}

async function uncaption(page: Page) {
  await page.evaluate(() => document.querySelector("#recording-caption")?.remove())
}

async function shot(page: Page, name: string) {
  await uncaption(page)
  await page.screenshot({ path: `${out}/${name}.png` })
}

async function centre(page: Page, id: string) {
  const box = await node(page, id).boundingBox()
  if (!box) throw new Error(`${id} is not drawn`)
  return { x: box.x + box.width / 2, y: box.y + box.height / 2, box }
}

async function glide(page: Page, from: { x: number; y: number }, to: { x: number; y: number }) {
  await page.mouse.move(from.x, from.y)
  await page.mouse.down()
  for (let step = 1; step <= 24; step++) {
    await page.mouse.move(
      from.x + ((to.x - from.x) * step) / 24,
      from.y + ((to.y - from.y) * step) / 24,
    )
    await page.waitForTimeout(16)
  }
  await page.mouse.up()
}

async function wheelAt(page: Page, at: { x: number; y: number }, notches: number) {
  await page.mouse.move(at.x, at.y)
  for (let i = 0; i < Math.abs(notches); i++) {
    await page.mouse.wheel(0, notches > 0 ? 160 : -160)
    await page.waitForTimeout(90)
  }
}

const browser = await chromium.launch()
try {
  // The tour, recorded.
  const context = await browser.newContext({
    baseURL,
    viewport: { width: 1600, height: 1000 },
    recordVideo: { dir: out, size: { width: 1600, height: 1000 } },
  })
  const page = await context.newPage()
  await mockServerMap(page)
  await page.goto("/map")
  await node(page, "server").waitFor()
  await page.waitForTimeout(1200)
  await shot(page, "map-overview-1600")
  await caption(page, "The server in the middle, everything it runs around it", "Server → Map")
  await page.waitForTimeout(2600)

  const search = page.getByRole("textbox", { name: "Find anything on the server" })
  await caption(page, "Find anything and see what it is wired to", "/ → mongo-events → Enter")
  await search.click()
  await search.pressSequentially("mongo-events", { delay: 70 })
  await page.waitForTimeout(500)
  await search.press("Enter")
  await page.waitForTimeout(1500)
  await shot(page, "map-links-1600")
  await caption(page, "The inspector says each link in words", "point at one to find it on the map")
  const inspector = page.getByRole("complementary", { name: "Inspector" })
  await inspector.getByRole("button", { name: /mongo-data/ }).hover()
  await page.waitForTimeout(1300)
  await shot(page, "map-hover-1600")
  await inspector.getByRole("button", { name: /events.*serves/ }).hover()
  await page.waitForTimeout(900)
  await caption(page, "Follow a link to its other end", "press the row")
  await inspector.getByRole("button", { name: /events.*serves/ }).click()
  await page.waitForTimeout(1800)
  await shot(page, "map-database-1600")
  // Emptying the search lets the chosen box go too.
  await search.fill("")
  await page.getByRole("button", { name: "Fit" }).click()
  await page.waitForTimeout(900)

  await caption(page, "Zoom in on a branch", "scroll over it")
  const hub = (await centre(page, "branch:jobs")).box
  await wheelAt(page, { x: hub.x - 90, y: hub.y + hub.height / 2 }, -3)
  await page.waitForTimeout(700)
  await shot(page, "map-jobs-1600")
  await caption(page, "Drag a branch and its shelves come with it", "drag the branch's head")
  const jobs = (await centre(page, "branch:jobs")).box
  const grip = { x: jobs.x + jobs.width / 2, y: jobs.y + jobs.height / 2 }
  await glide(page, grip, { x: grip.x + 60, y: grip.y + 120 })
  await page.waitForTimeout(500)
  await glide(page, { x: grip.x + 60, y: grip.y + 120 }, grip)
  await page.waitForTimeout(500)
  await caption(page, "Pull a box off its shelf and it stays where you put it", "drag a box out")
  // A box wholly on the canvas: a drag that starts past its edge would be
  // the page's, not the map's.
  const canvas = await page.locator("[data-slot=server-map]").boundingBox()
  let pulled = null
  for (const id of ["backup:3", "backup:1", "backup:2"]) {
    const box = await node(page, id).boundingBox()
    if (box && canvas && box.x > canvas.x + 8 && box.y > canvas.y + 8) {
      pulled = box
      break
    }
  }
  if (pulled) {
    const from = { x: pulled.x + pulled.width / 2, y: pulled.y + pulled.height / 2 }
    await glide(page, from, { x: from.x + 170, y: from.y + 110 })
  }
  await page.waitForTimeout(1200)
  await shot(page, "map-arranged-1600")
  await caption(page, "Fold what you do not need", "the chevron on a branch")
  await page
    .getByRole("button", { name: "Fold People" })
    .click()
    .catch(() => undefined)
  await page.waitForTimeout(1200)

  await page.getByRole("button", { name: "Fit" }).click()
  await page.waitForTimeout(800)
  await caption(page, "Add anything from one menu", "Add")
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await page.waitForTimeout(1600)
  await shot(page, "map-add-1600")
  await page.keyboard.press("Escape")
  await caption(page, "Your arrangement is saved to your account", "Saved")
  await page.waitForTimeout(2000)
  await uncaption(page)
  await context.close()

  // The stills that are not part of the tour.
  const still = await browser.newContext({ baseURL, viewport: { width: 1600, height: 1000 } })
  const fresh = await still.newPage()
  await mockServerMap(fresh, { map: freshMap })
  await fresh.goto("/map")
  await node(fresh, "server").waitFor()
  await fresh.waitForTimeout(1500)
  await shot(fresh, "map-fresh-1600")
  await still.close()

  const phone = await browser.newContext({ baseURL, viewport: { width: 390, height: 844 } })
  const small = await phone.newPage()
  await mockServerMap(small)
  await small.goto("/map")
  await node(small, "server").waitFor()
  await small.waitForTimeout(1500)
  await shot(small, "map-390")
  await small.getByRole("textbox", { name: "Find anything on the server" }).fill("events")
  await small.getByRole("textbox", { name: "Find anything on the server" }).press("Enter")
  await small.waitForTimeout(1500)
  await shot(small, "map-390-chosen")
  await phone.close()
} finally {
  await browser.close()
}
console.log(`written to ${out}`)
