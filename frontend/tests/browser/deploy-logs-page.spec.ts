import { expect, test, type Page } from "@playwright/test"
import { risen } from "./risen"
import { deploymentRequests, json, mockProject, user } from "./deploy-fixture"

/**
 * A project's Logs as a workbench (27 Nov 2026): no project header over it,
 * one strip with the way back and the views, the window's figures as one
 * sentence about the window on screen rather than tiles about the last hour,
 * a request read in an inspector beside the rows, and on a phone one pane
 * with its views on a bar.
 */

test.use({ timezoneId: "UTC", contextOptions: { reducedMotion: "reduce" } })

const PHONE = { width: 390, height: 844 }
const DESK = { width: 1440, height: 900 }

async function top(page: Page, selector: string) {
  await risen(page)
  const box = await page.locator(selector).first().boundingBox()
  return Math.round(box!.y)
}

/**
 * What the bars at the window's foot take of it, as the bottom stack
 * (`components/dock.tsx`) publishes it: a dock rises above this.
 */
async function occupied(page: Page) {
  return page.evaluate(() =>
    Math.round(
      Number.parseFloat(
        document.documentElement.style.getPropertyValue("--bottom-occupied") || "0",
      ),
    ),
  )
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

/**
 * Every question the window asks of the request record, as the server
 * receives it — not the page's own read of the last hour, which asks for one
 * row to learn which proxy answers.
 */
async function asked(page: Page) {
  const urls: URL[] = []
  await page.route("**/api/v1/deploy/7/requests?*", async (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("limit") !== "1") urls.push(url)
    await route.fallback()
  })
  return urls
}

const headline = (page: Page) => page.locator("[data-slot=window-headline]")

test.describe("at a desk", () => {
  test.use({ viewport: DESK })

  test("the page is the logs: no project header, one strip, and the sheet's edges", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    const strip = page.locator("[data-workbench-header]")
    await expect(strip).toBeVisible()
    await expect(page.locator('[data-slot="project-header"]')).toHaveCount(0)
    // The way back is to the project, which says everything the header did.
    await expect(strip.getByRole("link", { name: /^Back to / })).toHaveAttribute(
      "href",
      "/deploy/7",
    )
    // Four views, as tabs; the builds are the run pages', on Deployments.
    const views = strip.getByRole("tablist", { name: "Log view" })
    await expect(views.getByRole("tab")).toHaveText([/Requests/, /Insights/, /Output/, /Events/])
    await expect(views.getByRole("tab", { name: "Requests" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    // No tile, no reading line, no box inside the sheet.
    await expect(page.locator('[data-slot="stat-tile"]')).toHaveCount(0)
    await expect(page.locator('[data-slot="reading-line"]')).toHaveCount(0)
    await expect(page.locator('[data-slot="pane-bar"]')).toBeHidden()
    expect(await occupied(page)).toBe(0)

    // Held to the window: the page does not scroll, the rows do.
    await expect(headline(page)).toContainText("1,284")
    await risen(page)
    expect(
      await page.evaluate(() => {
        const frame = document.querySelector('[data-slot="page"]')!
        return frame.scrollHeight <= frame.clientHeight + 1
      }),
    ).toBe(true)
  })

  test("the window is one sentence, about the window chosen", async ({ page }) => {
    await mockProject(page)
    const urls = await asked(page)
    await page.goto("/deploy/7/logs")

    const words = headline(page)
    await expect(words).toContainText("1,284 requests")
    await expect(words).toContainText("402 page views")
    await expect(words).toContainText("13 failed (1.0%)")
    await expect(words).toContainText("19 in 20 inside 412ms")
    await expect(words).toContainText("39.4 MB sent")
    await expect(words).toContainText("Over the last hour")
    // The alerts are its last words, with the way to the rules.
    await expect(words.getByText("1 firing")).toBeVisible()
    await expect(words.getByRole("link", { name: "Alerts" })).toHaveAttribute(
      "href",
      "/deploy/7/settings/automation#alerts",
    )

    // Seven days asks the server for seven days, and the sentence says so.
    await page.getByRole("button", { name: /^Time window: / }).click()
    await page
      .getByRole("group", { name: "Presets" })
      .getByRole("button", { name: "7 days" })
      .click()
    await expect(words).toContainText("Over the last 7 days")
    await expect(page).toHaveURL(/range=7d/)
    await expect
      .poll(() => {
        const week = urls.at(-1)
        if (!week) return 0
        return Math.round((Date.now() - Date.parse(week.searchParams.get("since")!)) / 3_600_000)
      })
      .toBe(7 * 24)
  })

  test("thirty days is offered, and a record younger than the window says where it starts", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/requests?*", (route) => {
      const window = deploymentRequests(new URL(route.request().url()))
      return json(route, {
        ...window,
        coverage: {
          ...window.coverage,
          history: {
            from: new Date(Date.now() - 2 * 86_400_000).toISOString(),
            days: 30,
            recording: true,
          },
        },
      })
    })
    await page.goto("/deploy/7/logs?range=30d")
    await expect(headline(page)).toContainText("Over the last 30 days")
    await expect(headline(page)).toContainText(/Recorded since \w+ \d+, \d\d:\d\d/)

    // The tab remembers the window, so the hour is asked for by name.
    await page.goto("/deploy/7/logs?range=1h")
    await expect(headline(page)).toContainText("Kept for 30 days")
    await expect(headline(page)).not.toContainText("Recorded since")
  })

  test("everyone, people or bots is one press, asked of the server", async ({ page }) => {
    await mockProject(page)
    const urls = await asked(page)
    await page.goto("/deploy/7/logs")
    const who = page.getByRole("radiogroup", { name: "Who sent them" })
    await expect(who.getByRole("radio", { name: "Everyone" })).toHaveAttribute(
      "aria-checked",
      "true",
    )

    await who.getByRole("radio", { name: "People" }).click()
    await expect.poll(() => urls.at(-1)?.searchParams.get("who")).toBe("people")
    await expect(page).toHaveURL(/who=people/)
    await who.getByRole("radio", { name: "Bots" }).click()
    await expect.poll(() => urls.at(-1)?.searchParams.get("who")).toBe("bots")
    await who.getByRole("radio", { name: "Everyone" }).click()
    await expect.poll(() => urls.at(-1)?.searchParams.has("who")).toBe(false)
    await expect(page).not.toHaveURL(/who=/)
  })

  test("a request opens beside the rows, and no row moves", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    const row = (path: string) =>
      page.getByRole("button", { pressed: false }).filter({ hasText: path }).first()
    await expect(row("/api/items")).toBeVisible()
    await risen(page)
    const before = (await row("/api/items").boundingBox())!

    await page.getByRole("button").filter({ hasText: "/api/checkout" }).first().click()
    const inspector = page.locator("[data-slot=request-inspector]")
    await expect(inspector).toBeVisible()
    await expect(inspector).toContainText("500")
    await expect(inspector).toContainText("Server error")
    await expect(inspector).toContainText("2.84s")
    await expect(inspector.getByText("Person", { exact: true })).toBeVisible()
    await expect(
      inspector.getByRole("button", { name: "Every request to this path" }),
    ).toBeVisible()
    // The next row is where it was, at the height it was.
    const after = (await row("/api/items").boundingBox())!
    expect(Math.round(after.y)).toBe(Math.round(before.y))
    expect(Math.round(after.height)).toBe(Math.round(before.height))

    // ↓ walks to the older request, Escape closes.
    await page.keyboard.press("ArrowDown")
    await expect(inspector).toContainText("/api/items")
    await expect(inspector.getByText("Program", { exact: true })).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(inspector).toHaveCount(0)
  })

  test("walking the requests with ↓ keeps the one inspected in the list's view", async ({
    page,
  }) => {
    await mockProject(page)
    // More requests than the pane shows, so walking them leaves its edge.
    await page.route("**/api/v1/deploy/7/requests?*", (route) => {
      const window = deploymentRequests(new URL(route.request().url()))
      const like = window.entries[1]
      const entries = Array.from({ length: 60 }, (_, i) => ({
        ...like,
        seq: 9000 - i,
        time: new Date(Date.parse("2026-09-03T11:59:00Z") - i * 10_000).toISOString(),
        path: `/api/walk/${i}`,
        query: undefined,
      }))
      return json(route, { ...window, entries })
    })
    await page.goto("/deploy/7/logs")
    await page.getByRole("button").filter({ hasText: "/api/walk/0" }).first().click()
    const inspector = page.locator("[data-slot=request-inspector]")
    await expect(inspector).toContainText("/api/walk/0")
    for (let i = 1; i <= 30; i++) {
      await page.keyboard.press("ArrowDown")
      await expect(inspector).toContainText(`/api/walk/${i}`)
    }
    // The row the inspector reads is the row lit in the list, and in view.
    const lit = page.getByRole("button", { pressed: true }).filter({ hasText: "/api/walk/30" })
    await expect(lit).toHaveCount(1)
    await expect(lit).toBeInViewport()
  })

  test("the page holds still while it reads", async ({ page }) => {
    await mockProject(page)
    const release = await held(page)
    await page.goto("/deploy/7/logs")
    await expect(headline(page)).toBeVisible()
    const waiting = await top(page, "[data-slot=window-headline]")
    release()
    await expect(headline(page)).toContainText("1,284 requests")
    expect(await top(page, "[data-slot=window-headline]")).toBe(waiting)
  })
})

test.describe("on a phone", () => {
  test.use({ viewport: PHONE })

  test("the page is one pane the height of the window, with its views on a bar", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    const bar = page
      .getByRole("navigation", { name: "Log view" })
      .and(page.locator("[data-slot=pane-bar]"))
    await expect(bar).toBeVisible()
    await expect(bar.getByRole("button")).toHaveText(
      ["Requests", "Insights", "Output", "Events"].map((name) => new RegExp(name)),
    )
    await expect(bar.getByRole("button", { name: "Requests" })).toHaveAttribute(
      "aria-current",
      "page",
    )
    // The strip's tabs are not drawn twice: the bar is the views on a phone.
    await expect(page.getByRole("tablist", { name: "Log view" })).toHaveCount(0)
    await expect(page.locator('[data-slot="stat-tile"]')).toHaveCount(0)

    await risen(page)
    const frame = (await page.locator('[data-slot="logs-workbench"]').boundingBox())!
    expect(Math.round(frame.x)).toBe(0)
    expect(Math.round(frame.width)).toBe(PHONE.width)
    expect(Math.round(frame.y + frame.height)).toBe(PHONE.height)
    const barBox = (await bar.boundingBox())!
    expect(Math.round(barBox.y + barBox.height)).toBe(PHONE.height)
    await expect.poll(() => occupied(page)).toBe(Math.round(barBox.height))
    expect(
      await page.evaluate(() => {
        const frame = document.querySelector('[data-slot="page"]')!
        return (
          frame.scrollHeight <= frame.clientHeight + 1 &&
          document.documentElement.scrollWidth <= window.innerWidth
        )
      }),
    ).toBe(true)

    await bar.getByRole("button", { name: "Insights" }).click()
    await expect(page.getByText("Pages", { exact: true })).toBeVisible()
    await expect(page).toHaveURL(/view=insights/)
    await bar.getByRole("button", { name: "Output" }).click()
    await expect(page).toHaveURL(/view=output/)
    await bar.getByRole("button", { name: "Events" }).click()
    await expect(page.getByLabel("Filter container events")).toBeVisible()
    await expect(page).toHaveURL(/view=events/)
    await expect(bar.getByRole("button", { name: "Events" })).toHaveAttribute(
      "aria-description",
      "1",
    )
  })

  test("the window's sentence is one height waiting and read, so the rows hold still", async ({
    page,
  }) => {
    await mockProject(page)
    const release = await held(page)
    await page.goto("/deploy/7/logs")
    await expect(headline(page)).toBeVisible()
    await risen(page)
    const waiting = (await headline(page).boundingBox())!
    release()
    await expect(headline(page)).toContainText("1,284 requests")
    await risen(page)
    const read = (await headline(page).boundingBox())!
    expect(Math.round(read.y)).toBe(Math.round(waiting.y))
    expect(Math.round(read.height)).toBe(Math.round(waiting.height))
  })

  test("a request is the pane, with the way back to the rows", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await page.getByRole("button").filter({ hasText: "/api/checkout" }).first().click()
    const inspector = page.locator("[data-slot=request-inspector]")
    await expect(inspector).toBeVisible()
    await expect(page.getByRole("button").filter({ hasText: "/api/items" })).toHaveCount(0)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await inspector.getByRole("button", { name: "Back to the requests" }).click()
    await expect(inspector).toHaveCount(0)
    await expect(page.getByRole("button").filter({ hasText: "/api/items" }).first()).toBeVisible()
  })
})

for (const viewport of [DESK, PHONE]) {
  test(`a read-only account is offered no rule to add, at ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockProject(page)
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
    )
    await page.route("**/api/v1/deploy/7/alerts", (route) => json(route, { alerts: [] }))
    await page.goto(viewport.width < 768 ? "/deploy/7/logs?view=insights" : "/deploy/7/logs")

    await expect(headline(page).getByText("No alerts", { exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Add alert" })).toHaveCount(0)
    // The rules are still a link away, to be read.
    await expect(headline(page).getByRole("link", { name: "Alerts" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Block" })).toHaveCount(0)
  })
}

test("the first rule is added from the window's line", async ({ page }) => {
  await page.setViewportSize(DESK)
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/alerts", async (route) =>
    route.request().method() === "GET" ? json(route, { alerts: [] }) : route.fallback(),
  )
  await page.goto("/deploy/7/logs")
  await headline(page).getByRole("button", { name: "Add alert" }).click()
  const sheet = page.getByRole("dialog", { name: "Add alert" })
  await expect(sheet).toBeVisible()
  await expect(sheet.getByLabel("Alert limit")).toBeVisible()
})

test("Insights says how long things took in one sentence, and each figure still narrows", async ({
  page,
}) => {
  await mockProject(page)
  const urls = await asked(page)
  await page.goto("/deploy/7/logs?view=insights")

  const times = page.locator('[data-slot="latency-readings"]')
  await expect(times).toHaveText(
    "Half inside 24ms, 19 in 20 inside 412ms, 99 in 100 inside 2.84s; the slowest took 3.12s.",
  )
  // No tile anywhere on Insights: the window's figures are the sentence over the chart.
  await expect(page.locator('[data-slot="stat-tile"]')).toHaveCount(0)
  await expect(headline(page)).toContainText("1,284 requests")
  for (const name of ["Half: 24ms", "19 in 20: 412ms", "99 in 100: 2.84s", "Slowest: 3.12s"]) {
    await expect(times.getByRole("button", { name: new RegExp(`^${name}`) })).toBeVisible()
  }
  await expect(times.getByRole("button", { name: /^99 in 100/ })).toHaveClass(/text-warning/)
  await expect(times.getByRole("button", { name: /^Half/ })).not.toHaveClass(/text-warning/)

  await times.getByRole("button", { name: /^99 in 100/ }).click()
  await expect.poll(() => urls.some((url) => url.searchParams.get("minMs") === "2840")).toBe(true)
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
  await expect(page.getByRole("heading", { name: "Served", exact: true })).toHaveCount(0)
})

for (const viewport of [DESK, PHONE]) {
  test(`a record that cannot be read is said as that, not as still reading, at ${viewport.width}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport)
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/requests?*", (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "unavailable", message: "the log is not readable" },
        }),
      }),
    )
    await page.goto("/deploy/7/logs")
    await expect(page.getByText("Could not read the request record")).toBeVisible()
    await expect(page.locator("[data-slot=skeleton]").filter({ visible: true })).toHaveCount(0)
  })
}

test("the page has one name, as every page does", async ({ page }) => {
  await page.setViewportSize(DESK)
  await mockProject(page)
  await page.goto("/deploy/7/logs")
  await expect(page.locator("[data-workbench-header]")).toBeVisible()
  await expect(page.getByRole("main").getByRole("heading", { level: 1 })).toHaveCount(1)
})

test("an old link to Builds opens the page's own view", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7/logs?view=builds")
  await expect(page.getByRole("tab", { name: "Requests" })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("tab", { name: "Builds" })).toHaveCount(0)
})
