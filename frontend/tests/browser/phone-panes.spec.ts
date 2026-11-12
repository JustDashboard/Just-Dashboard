import { expect, test, type Locator, type Page } from "@playwright/test"
import { json, mockHost } from "./host-fixture"
import { admin, mockNetwork } from "./network-fixture"
import { risen } from "./risen"

/**
 * One pane at a time on a phone (`components/phone-panes.tsx`).
 *
 * Stacked, the Logs page gave its sources a 173px box and started the
 * console four fifths of the way down a phone's screen, and the network
 * tools put a 278px catalogue over the tool. Each is one full-height pane
 * now with a bar at the foot, as the Git workspace is (`git-panel.spec.ts`)
 * and the assistant (`database-assistant.spec.ts`), and opens on the thing
 * the page is for. From `md` up nothing has changed: a tablet stacks, a desk
 * has its columns.
 */

const PHONE = { width: 390, height: 844 }

const sources = {
  sources: [
    {
      id: "file:/var/log/auth.log",
      label: "auth.log",
      kind: "system",
      path: "/var/log/auth.log",
      size: 1024,
      rotated: false,
    },
    { id: "docker:api", label: "api", kind: "docker", status: "running", detail: "shop · redis:7" },
    // Enough of them that the list is longer than a phone is tall.
    ...Array.from({ length: 30 }, (_, i) => ({
      id: `file:/var/log/app/worker-${i}.log`,
      label: `worker-${i}.log`,
      kind: "system",
      path: `/var/log/app/worker-${i}.log`,
      size: 2048,
      rotated: false,
    })),
  ],
  units: [],
  roots: ["/var/log"],
  missing: {},
}

const LINES: Record<string, object[]> = {
  "file:/var/log/auth.log": [
    {
      text: "2026-09-23T05:21:42.600655Z atlas sshd-session[3254114]: Failed password for root from 62.60.130.253 port 59026 ssh2",
      timestamp: "2026-09-23T05:21:42.600655Z",
      level: "error",
    },
  ],
  "docker:api": [
    {
      text: "2026-09-23T05:11:01Z listening on :8080",
      timestamp: "2026-09-23T05:11:01Z",
      level: "info",
    },
  ],
}

async function mockLogs(page: Page) {
  await mockHost(page)
  await page.route("**/api/v1/logs/sources", (route) => json(route, sources))
  await page.route("**/api/v1/logs/retention**", (route) => json(route, {}))
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const source = new URL(socket.url()).searchParams.get("source") ?? ""
    socket.send(JSON.stringify({ type: "logs", data: LINES[source] ?? [], ts: Date.now() }))
  })
}

const box = async (locator: Locator) => {
  const found = await locator.boundingBox()
  expect(found).not.toBeNull()
  return found!
}

/** Nothing scrolls but what is inside the pane: not the window, not the page, not sideways. */
async function expectHeldToTheWindow(page: Page) {
  const overflow = await page.evaluate(() => {
    const html = document.documentElement
    const held = document.querySelector("[data-slot=page]")!
    return {
      sideways: html.scrollWidth - html.clientWidth,
      down: html.scrollHeight - html.clientHeight,
      page: held.scrollHeight - held.clientHeight,
    }
  })
  expect(overflow.sideways).toBeLessThanOrEqual(0)
  expect(overflow.down).toBeLessThanOrEqual(0)
  expect(overflow.page).toBeLessThanOrEqual(1)
}

/** The bar is at the foot of the screen, edge to edge, 56px of stops. */
async function expectBarAtTheFoot(bar: Locator) {
  const at = await box(bar)
  expect(Math.round(at.x)).toBe(0)
  expect(Math.round(at.width)).toBe(PHONE.width)
  expect(Math.round(at.y + at.height)).toBe(PHONE.height)
  expect(Math.round(at.height)).toBeGreaterThanOrEqual(56)
  expect(Math.round(at.height)).toBeLessThanOrEqual(58)
}

test.describe("on a phone", () => {
  test.use({ viewport: PHONE, hasTouch: true })

  test("Logs opens on the log, the whole height, with the sources on the bar", async ({
    page,
  }, testInfo) => {
    await mockLogs(page)
    await page.goto("/logs")
    const line = page.getByText("Failed password").first()
    await expect(line).toBeVisible()
    await risen(page)

    const bar = page.getByRole("navigation", { name: "Logs", exact: true })
    const rail = page.getByLabel("Log sources")
    await expectBarAtTheFoot(bar)
    await expect(bar.getByRole("tab", { name: "Log", exact: true })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    // The console is what is on screen, without scrolling anything: the
    // whole of it between the page's head and the bar, a dozen lines tall
    // where the stacked page showed five, and nothing stacked over it.
    await expect(line).toBeInViewport()
    const lines = await box(page.getByLabel("Log lines"))
    expect(lines.y).toBeGreaterThan(0)
    expect(lines.y + lines.height).toBeLessThanOrEqual((await box(bar)).y)
    expect(lines.height).toBeGreaterThanOrEqual(260)
    expect(Math.round(lines.x)).toBe(0)
    expect(Math.round(lines.width)).toBe(PHONE.width)
    await expect(rail).toBeHidden()
    await expect(page.getByRole("button", { name: "Hide the sources" })).toBeHidden()
    await expectHeldToTheWindow(page)
    await page.screenshot({ path: testInfo.outputPath("logs-phone-log.png") })

    // The sources are the other pane: the same height, down to the bar.
    await bar.getByRole("tab", { name: "Sources" }).click()
    await expect(rail).toBeVisible()
    await expect(line).toBeHidden()
    const railBox = await box(rail)
    const barBox = await box(bar)
    expect(Math.round(railBox.x)).toBe(0)
    expect(Math.round(railBox.width)).toBe(PHONE.width)
    expect(Math.round(railBox.y + railBox.height)).toBe(Math.round(barBox.y))
    expect(railBox.height).toBeGreaterThan(PHONE.height / 2)
    await expectHeldToTheWindow(page)
    await page.screenshot({ path: testInfo.outputPath("logs-phone-sources.png") })

    // Every source is reachable inside that pane, and picking one reads it.
    const last = rail.getByText("worker-29.log")
    await last.scrollIntoViewIfNeeded()
    await expect(last).toBeInViewport()
    await expectBarAtTheFoot(bar)
    const api = rail.getByText("api", { exact: true })
    await api.scrollIntoViewIfNeeded()
    await api.click()
    await expect(page.getByText("listening on :8080").first()).toBeInViewport()
    await expect(rail).toBeHidden()
    await expect(bar.getByRole("tab", { name: "Log", exact: true })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await expectHeldToTheWindow(page)
  })

  test("the sources hidden at a desk are still on a phone's bar", async ({ page }) => {
    await mockLogs(page)
    await page.addInitScript(() => localStorage.setItem("jd.view.state.entry.logs.rail", "false"))
    await page.goto("/logs")
    await expect(page.getByText("Failed password").first()).toBeVisible()
    const bar = page.getByRole("navigation", { name: "Logs", exact: true })
    await bar.getByRole("tab", { name: "Sources" }).click()
    await expect(page.getByLabel("Log sources")).toBeVisible()
    await expect(page.getByLabel("Log sources").getByText("auth.log")).toBeVisible()
  })

  test("the network tools open on the tool, with the catalogue on the bar", async ({
    page,
  }, testInfo) => {
    await mockNetwork(page, [], { session: admin })
    await page.goto("/network/tools")
    const panel = page.locator("[data-slot=security-tools]")
    const run = panel.getByRole("button", { name: "Run", exact: true })
    await expect(run).toBeVisible()
    await risen(page)

    const bar = page.getByRole("navigation", { name: "Tools", exact: true })
    const catalogue = page.getByRole("navigation", { name: "Diagnostics" })
    await expectBarAtTheFoot(bar)
    await expect(bar.getByRole("tab", { name: "Tool", exact: true })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    // The chosen tool is on screen without scrolling: its Run in the upper
    // half, nothing stacked over it.
    await expect(run).toBeInViewport()
    expect((await box(run)).y).toBeLessThan(PHONE.height / 2)
    await expect(catalogue).toBeHidden()
    // The bar's stop is the one way to the catalogue: the head's button is a desk's.
    await expect(panel.getByRole("button", { name: "Every tool" })).toBeHidden()
    const frame = await box(panel)
    expect(Math.round(frame.x)).toBe(0)
    expect(Math.round(frame.width)).toBe(PHONE.width)
    expect(Math.round(frame.y + frame.height)).toBe(PHONE.height)
    await expectHeldToTheWindow(page)
    await page.screenshot({ path: testInfo.outputPath("tools-phone-tool.png") })

    // The catalogue is the other pane, the same height, and every tool in
    // it is reachable.
    await bar.getByRole("tab", { name: "Every tool" }).click()
    await expect(catalogue).toBeVisible()
    await expect(run).toBeHidden()
    const list = await box(catalogue)
    expect(Math.round(list.y + list.height)).toBe(Math.round((await box(bar)).y))
    expect(list.height).toBeGreaterThan(PHONE.height / 2)
    await expectHeldToTheWindow(page)
    await page.screenshot({ path: testInfo.outputPath("tools-phone-catalogue.png") })

    const subnet = catalogue.getByRole("button", { name: "Subnet calculator" })
    await subnet.scrollIntoViewIfNeeded()
    await expect(subnet).toBeInViewport()
    await expectBarAtTheFoot(bar)
    // Choosing a tool is asking to use it.
    await subnet.click()
    await expect(catalogue).toBeHidden()
    await expect(panel.getByText("calculated in this browser")).toHaveCount(1)
    await expect(panel.getByRole("textbox").first()).toBeInViewport()
    await expect(bar.getByRole("tab", { name: "Tool", exact: true })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await expectHeldToTheWindow(page)
  })

  test("a tool a link asks for is the one on screen", async ({ page }) => {
    await mockNetwork(page, [], { session: admin })
    await page.goto("/network/tools?tool=ping&target=192.0.2.9")
    const panel = page.locator("[data-slot=security-tools]")
    await expect(panel.getByRole("button", { name: "Run", exact: true })).toBeInViewport()
    await expect(panel.getByRole("textbox").first()).toHaveValue("192.0.2.9")
  })
})

test.describe("from md up nothing changed", () => {
  test("a tablet still stacks the sources over the log, with no bar", async ({ page }) => {
    await page.setViewportSize({ width: 800, height: 1000 })
    await mockLogs(page)
    await page.goto("/logs")
    const line = page.getByText("Failed password").first()
    await expect(line).toBeVisible()
    await risen(page)
    const rail = page.getByLabel("Log sources")
    await expect(rail).toBeVisible()
    await expect(page.getByRole("navigation", { name: "Logs", exact: true })).toBeHidden()
    const railBox = await box(rail)
    expect(railBox.height).toBeLessThanOrEqual(257)
    expect((await box(line)).y).toBeGreaterThanOrEqual(railBox.y + railBox.height)
    await expect(page.getByRole("button", { name: "Hide the sources" })).toBeVisible()
  })

  test("a desk has the sources beside the log, and the catalogue beside the tool", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await mockLogs(page)
    await page.goto("/logs")
    const line = page.getByText("Failed password").first()
    await expect(line).toBeVisible()
    await risen(page)
    const rail = await box(page.getByLabel("Log sources"))
    const lineBox = await box(line)
    expect(lineBox.x).toBeGreaterThanOrEqual(rail.x + rail.width)
    expect(lineBox.y).toBeLessThan(rail.y + rail.height)
    await expect(page.getByRole("navigation", { name: "Logs", exact: true })).toBeHidden()

    await mockNetwork(page, [], { session: admin })
    await page.goto("/network/tools")
    const panel = page.locator("[data-slot=security-tools]")
    const run = panel.getByRole("button", { name: "Run", exact: true })
    await expect(run).toBeVisible()
    await risen(page)
    const catalogue = await box(page.getByRole("navigation", { name: "Diagnostics" }))
    const runBox = await box(run)
    expect(runBox.x).toBeGreaterThanOrEqual(catalogue.x + catalogue.width)
    expect(runBox.y).toBeLessThan(catalogue.y + catalogue.height)
    await expect(page.getByRole("navigation", { name: "Tools", exact: true })).toBeHidden()
    await expect(panel.getByRole("button", { name: "Every tool" })).toBeVisible()
  })
})
