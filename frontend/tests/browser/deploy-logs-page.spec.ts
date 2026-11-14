import { expect, test, type Page } from "@playwright/test"
import { risen } from "./risen"
import { deploymentRequests, json, mockProject, user } from "./deploy-fixture"

/**
 * A project's Logs after its pass to the deploy standard: no tile row opens
 * it, the hour is read in the Overview's lines, the page holds its place while
 * it loads, and on a phone it is one pane with its views on a bar.
 */

// The readings count up on a spring; reduced motion draws the figure itself.
test.use({ timezoneId: "UTC", contextOptions: { reducedMotion: "reduce" } })

const PHONE = { width: 390, height: 844 }

async function top(page: Page, selector: string) {
  await risen(page)
  const box = await page.locator(selector).first().boundingBox()
  return Math.round(box!.y)
}

/** Holds the request record and the rules until released, so the page is seen waiting. */
async function held(page: Page) {
  let release = () => {}
  const gate = new Promise<void>((resolve) => (release = resolve))
  for (const path of ["**/api/v1/deploy/7/requests?*", "**/api/v1/deploy/7/alerts"]) {
    await page.route(path, async (route) => {
      await gate
      await route.fallback()
    })
  }
  return () => release()
}

test("no tile opens the page, and the pane is in one place waiting and read", async ({ page }) => {
  await mockProject(page)
  const release = await held(page)
  await page.goto("/deploy/7/logs")

  const hour = page.getByRole("group", { name: "The last hour" })
  const lines = hour.locator('[data-slot="reading-line"]')
  await expect(lines).toHaveCount(4)
  await expect(lines.first()).toContainText("No reading yet")
  await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
  const waiting = {
    lines: await top(page, '[data-slot="reading-lines"]'),
    pane: await top(page, '[data-slot="pane"]'),
  }

  release()
  await expect(lines.first()).toContainText("21.4 requests a minute")
  await expect(page.getByText("1 firing")).toBeVisible()
  expect({
    lines: await top(page, '[data-slot="reading-lines"]'),
    pane: await top(page, '[data-slot="pane"]'),
  }).toEqual(waiting)
  await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
  // A desk has no bar: the views are the strip at the pane's head.
  await expect(page.locator('[data-slot="pane-bar"]')).toBeHidden()
  await expect(page.locator('[data-slot="hour-line"]')).toHaveCount(0)
})

test("a line with a reading and a line without one are the same height", async ({ page }) => {
  await mockProject(page)
  // A record with no timing: Response time has nothing to say, the rest do.
  await page.route("**/api/v1/deploy/7/requests?*", (route) => {
    const window = deploymentRequests(new URL(route.request().url()))
    return json(route, {
      ...window,
      latency: false,
      summary: { ...window.summary, latency: undefined },
    })
  })
  await page.goto("/deploy/7/logs")
  const lines = page.locator('[data-slot="reading-line"]')
  await expect(lines.filter({ hasText: "Response time" })).toContainText("No reading yet")
  await expect(lines.filter({ hasText: "Response time" })).toContainText(
    "This ingress records no timing",
  )
  await risen(page)
  const heights = await lines.evaluateAll((all) =>
    all.map((line) => Math.round(line.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)
})

test.describe("on a phone", () => {
  test.use({ viewport: PHONE })

  test("the page is one pane the height of the window, with its views on a bar", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    const bar = page.getByRole("navigation", { name: "Log view" }).filter({
      has: page.getByRole("tablist"),
    })
    await expect(bar).toBeVisible()
    await expect(bar.getByRole("tab")).toHaveText(
      ["Requests", "Insights", "Output", "Builds", "Events"].map((name) => new RegExp(name)),
    )
    await expect(bar.getByRole("tab", { name: "Requests" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    // The strip a desk picks the view on is not drawn twice.
    await expect(page.getByRole("button", { name: "Insights", exact: true })).toBeHidden()
    await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
    // The readings are behind the pane's first line, not stacked over it.
    await expect(page.getByRole("group", { name: "The last hour" })).toHaveCount(0)

    // One pane: it ends where the window does, out to the screen's edges, and
    // nothing scrolls but what is inside it.
    await risen(page)
    const pane = (await page.locator('[data-slot="pane"]').boundingBox())!
    expect(Math.round(pane.x)).toBe(0)
    expect(Math.round(pane.width)).toBe(PHONE.width)
    expect(Math.round(pane.y + pane.height)).toBe(PHONE.height)
    const barBox = (await bar.boundingBox())!
    expect(Math.round(barBox.y + barBox.height)).toBe(PHONE.height)
    expect(pane.height).toBeGreaterThan(420)
    expect(
      await page.evaluate(() => {
        const frame = document.querySelector('[data-slot="page"]')!
        return (
          frame.scrollHeight <= frame.clientHeight + 1 &&
          document.documentElement.scrollWidth <= window.innerWidth
        )
      }),
    ).toBe(true)

    // Every view is a stop, and the address says which.
    await bar.getByRole("tab", { name: "Insights" }).click()
    await expect(page.getByText("Pages", { exact: true })).toBeVisible()
    await expect(page).toHaveURL(/view=insights/)
    await bar.getByRole("tab", { name: "Output" }).click()
    await expect(page).toHaveURL(/view=output/)
    await bar.getByRole("tab", { name: "Builds" }).click()
    await expect(page).toHaveURL(/view=builds/)
    await bar.getByRole("tab", { name: "Events" }).click()
    await expect(page.getByLabel("Filter container events")).toBeVisible()
    await expect(page).toHaveURL(/view=events/)
    // The exit the hour saw is counted on the stop, beside its word.
    await expect(bar.getByRole("tab", { name: "Events" })).toHaveAttribute("aria-description", "1")
    expect(Math.round((await bar.boundingBox())!.y + barBox.height)).toBe(PHONE.height)
  })

  test("the hour is the pane's first line, and opens as a page with a way back", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    const line = page.getByRole("button", { name: "The last hour's readings and alerts" })
    await expect(line).toContainText("1 firing")
    await expect(line).toContainText("21.4 a minute")
    await expect(line).toContainText("1.0% of requests failed")
    // What the reader typed stays behind the page it opens.
    await page.getByLabel("Filter requests by path").fill("/api")

    await line.click()
    const hour = page.getByRole("region", { name: "The last hour" })
    await expect(hour.locator('[data-slot="reading-line"]')).toHaveCount(4)
    await expect(hour).toContainText("21.4 requests a minute")
    await expect(hour).toContainText("19 in 20 inside 412ms")
    await expect(hour).toContainText("39.4 MB sent")
    // The alerts are on it too: who is told, and the way to the rules.
    await expect(hour.getByText("1 firing")).toBeVisible()
    await expect(hour.getByRole("link", { name: "Alerts" })).toHaveAttribute(
      "href",
      "/deploy/7/settings/automation#alerts",
    )
    // A page a row opened has the bar out of its way.
    await expect(page.locator('[data-slot="pane-bar"]')).toHaveCount(0)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)

    await hour.getByRole("button", { name: "Back to the logs" }).click()
    await expect(hour).toHaveCount(0)
    await expect(page.locator('[data-slot="pane-bar"]')).toBeVisible()
    await expect(page.getByLabel("Filter requests by path")).toHaveValue("/api")
  })

  test("the pane is in one place waiting and read", async ({ page }) => {
    await mockProject(page)
    const release = await held(page)
    await page.goto("/deploy/7/logs")
    const line = page.locator('[data-slot="hour-line"]')
    await expect(line).toContainText("Reading the request record…")
    const waiting = [
      await top(page, '[data-slot="pane"]'),
      await top(page, '[data-slot="pane-bar"]'),
    ]
    release()
    await expect(line).toContainText("21.4 a minute")
    expect([
      await top(page, '[data-slot="pane"]'),
      await top(page, '[data-slot="pane-bar"]'),
    ]).toEqual(waiting)
  })

  test("the first rule is added from the hour's page", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await expect(page.locator('[data-slot="hour-line"]')).toContainText("1 firing")
    await page.evaluate(() => fetch("/api/v1/deploy/7/alerts/91", { method: "DELETE" }))
    await page.reload()

    const line = page.locator('[data-slot="hour-line"]')
    await expect(line).toContainText("21.4 a minute")
    await expect(line).not.toContainText("firing")
    await line.click()
    const hour = page.getByRole("region", { name: "The last hour" })
    await expect(hour.getByText("No alerts", { exact: true })).toBeVisible()
    await hour.getByRole("button", { name: "Add alert" }).click()
    const sheet = page.getByRole("dialog", { name: "Add alert" })
    await sheet.getByLabel("Alert limit").fill("2")
    await sheet.getByRole("button", { name: "Add alert" }).click()
    await expect(sheet).toHaveCount(0)
    await expect(hour.getByText("All quiet", { exact: true })).toBeVisible()
  })
})

for (const viewport of [{ width: 1280, height: 900 }, PHONE]) {
  test(`a read-only account is offered no rule to add, at ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockProject(page)
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
    )
    await page.route("**/api/v1/deploy/7/alerts", (route) => json(route, { alerts: [] }))
    await page.goto("/deploy/7/logs")
    if (viewport.width < 768) await page.locator('[data-slot="hour-line"]').click()

    await expect(page.getByText("No alerts", { exact: true })).toBeVisible()
    await expect(
      page.getByText("Nobody is told when this deployment fails or slows."),
    ).toBeVisible()
    await expect(page.getByRole("button", { name: "Add alert" })).toHaveCount(0)
    // The rules are still a link away, to be read.
    await expect(page.getByRole("link", { name: "Alerts" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Block" })).toHaveCount(0)
  })
}

test("Insights says how long things took in one sentence, and each figure still narrows", async ({
  page,
}) => {
  await mockProject(page)
  const asked: URL[] = []
  await page.route("**/api/v1/deploy/7/requests*", async (route) => {
    asked.push(new URL(route.request().url()))
    await route.fallback()
  })
  await page.goto("/deploy/7/logs?view=insights")

  const times = page.locator('[data-slot="latency-readings"]')
  await expect(times).toHaveText(
    "Half inside 24ms, 19 in 20 inside 412ms, 99 in 100 inside 2.84s; the slowest took 3.12s.",
  )
  // A sentence, not four tiles in a frame.
  await expect(times.locator('[data-slot="stat-tile"]')).toHaveCount(0)
  await expect(page.locator('[data-slot="stat-grid"]').filter({ hasText: "Half" })).toHaveCount(0)
  for (const name of ["Half: 24ms", "19 in 20: 412ms", "99 in 100: 2.84s", "Slowest: 3.12s"]) {
    await expect(times.getByRole("button", { name: new RegExp(`^${name}`) })).toBeVisible()
  }
  // Past a second is amber, as the tile was.
  await expect(times.getByRole("button", { name: /^99 in 100/ })).toHaveClass(/text-warning/)
  await expect(times.getByRole("button", { name: /^Half/ })).not.toHaveClass(/text-warning/)

  await times.getByRole("button", { name: /^99 in 100/ }).click()
  await expect.poll(() => asked.some((url) => url.searchParams.get("minMs") === "2840")).toBe(true)
  await expect(page.getByRole("button", { name: "Clear the slow filter" })).toBeVisible()
})

test("what was served is a line under Pages, and the heaviest pages keep their list", async ({
  page,
}) => {
  await mockProject(page)
  await page.goto("/deploy/7/logs?view=insights")

  const pages = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Pages", exact: true }),
  })
  await expect(pages).toContainText("39.4 MB sent")
  await expect(pages).toContainText("each on average")
  // The block of two figures under two captions is gone.
  await expect(page.getByRole("heading", { name: "Served", exact: true })).toHaveCount(0)
  await expect(page.getByText("Each, on average")).toHaveCount(0)
})
