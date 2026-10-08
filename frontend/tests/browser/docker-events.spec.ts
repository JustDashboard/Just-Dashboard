import { expect, test } from "@playwright/test"
import { CONTAINERS, EVENTS, mockEvents } from "./docker-events-fixture"

/**
 * The host's Docker Events page, read against a record with something in it:
 * a database looping for seven minutes, a worker killed for memory, an API
 * restarted from this dashboard, a container started from a shell nobody can
 * name. Each test is a claim the 2026-10-08 overhaul rests on.
 */

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
})

test("a restart loop is the verdict and one row, not sixteen", async ({ page }) => {
  await mockEvents(page)
  await page.goto("/docker/events")

  await expect(page.getByRole("button", { name: "shop-db-1 is in a restart loop" })).toBeVisible()

  const feed = page.getByRole("region", { name: "Event feed" })
  await expect(feed.getByText(/^shop-db-1 restarted ×8 in \d+ min$/)).toBeVisible()
  // Folded: none of its exits is a row of its own until it is opened.
  await expect(feed.getByText("shop-db-1 exited with status 1")).toHaveCount(0)
  await feed.getByRole("button", { name: "Show the 16 events" }).click()
  await expect(feed.getByText("shop-db-1 exited with status 1")).toHaveCount(8)
})

test("the containers table reads each container's state, failures and who", async ({ page }) => {
  await mockEvents(page)
  await page.goto("/docker/events")

  const table = page.getByRole("table")
  const row = (name: string) => table.getByRole("row").filter({ hasText: name })

  await expect(row("shop-db-1").getByText("Restart loop")).toBeVisible()
  await expect(row("worker").getByText("Stopped")).toBeVisible()
  await expect(row("worker").getByText(/^out of memory · \d+m/)).toBeVisible()
  await expect(row("worker").getByText("oom")).toBeVisible()
  // Gone from Docker's listing, and the record saw it go.
  await expect(row("shop-migrate-1").getByText("Removed")).toBeVisible()
  // Nothing in the audit log or compose explains a `docker run` from a shell.
  await expect(
    row("uptime-kuma").getByRole("button", { name: "Where this event came from" }),
  ).toHaveText("External")
  // The polled copy, laid against the audit log, wins over the socket's.
  await expect(row("shop-api-1").getByText("wayy")).toBeVisible()
  // A container Docker still lists is a link to its own page.
  await expect(row("shop-api-1").getByRole("link", { name: /shop-api-1/ })).toHaveAttribute(
    "href",
    `/docker/containers/${CONTAINERS[1].id}`,
  )

  // Worst first: the loop, then what failed.
  const names = await table
    .locator("tbody tr")
    .evaluateAll((rows) => rows.map((r) => r.querySelector("td")?.textContent ?? ""))
  expect(names[0]).toContain("shop-db-1")
  expect(names[1]).toContain("worker")
})

test("a reading narrows the table and the feed to what it counts", async ({ page }) => {
  await mockEvents(page)
  await page.goto("/docker/events")

  await page.getByRole("button", { name: "Show only failures" }).click()
  const feed = page.getByRole("region", { name: "Event feed" })
  await expect(feed.getByText("worker ran out of memory")).toBeVisible()
  await expect(feed.getByText("redis started")).toHaveCount(0)
  await expect(page.getByRole("table").getByRole("row").filter({ hasText: "redis" })).toHaveCount(0)

  await page.getByRole("button", { name: "Stop showing only failures" }).click()
  await expect(feed.getByText("redis started")).toBeVisible()
})

test("a container's row narrows the feed to its events", async ({ page }) => {
  await mockEvents(page)
  await page.goto("/docker/events")

  await page.getByRole("table").getByRole("row").filter({ hasText: "redis" }).click()
  const feed = page.getByRole("region", { name: "Event feed" })
  await expect(feed.getByText("redis started")).toBeVisible()
  await expect(feed.getByText("worker ran out of memory")).toHaveCount(0)

  await page.getByRole("button", { name: "Show every container's events" }).click()
  await expect(feed.getByText("worker ran out of memory")).toBeVisible()
})

test("the window narrows everything, and an arriving event is drawn", async ({ page }) => {
  const mock = await mockEvents(page)
  await page.goto("/docker/events")

  // The whole buffer is read once, not a page of it.
  await expect.poll(() => mock.asked.some((q) => q.get("limit") === "2000")).toBe(true)

  const feed = page.getByRole("region", { name: "Event feed" })
  await expect(feed.getByText("redis started")).toBeVisible()
  await page.getByRole("radio", { name: "1h" }).click()
  await expect(feed.getByText("redis started")).toHaveCount(0)
  await expect(feed.getByText("worker ran out of memory")).toBeVisible()

  mock.emit([
    {
      time: new Date().toISOString(),
      type: "container",
      action: "start",
      name: "worker",
      id: EVENTS.find((e) => e.name === "worker")!.id,
      image: "python:3.12-slim",
      message: "worker started",
      level: "notice",
      source: "docker",
    },
  ])
  await expect(feed.getByText("worker started")).toBeVisible()
})

test("an empty record says why rather than claiming a quiet host", async ({ page }) => {
  await mockEvents(page, { events: [] })
  await page.goto("/docker/events")
  await expect(page.getByText("Docker has been quiet")).toBeVisible()
  await expect(page.getByRole("heading", { level: 1 })).toHaveClass(/sr-only/)
})

for (const width of [390, 768, 1024, 1280]) {
  test(`the events page fits ${width}px without scrolling sideways`, async ({ page }) => {
    await mockEvents(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/docker/events")
    await expect(page.getByRole("region", { name: "Event feed" })).toBeVisible()
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow).toBeLessThanOrEqual(1)

    const unnamed = await page.evaluate(() =>
      [...document.querySelectorAll<HTMLElement>("button")]
        .filter((el) => el.offsetParent !== null && !(el.textContent ?? "").trim())
        .filter((el) => !el.getAttribute("aria-label") && !el.querySelector(".sr-only"))
        .map((el) => el.outerHTML.slice(0, 120)),
    )
    expect(unnamed).toEqual([])
  })
}
