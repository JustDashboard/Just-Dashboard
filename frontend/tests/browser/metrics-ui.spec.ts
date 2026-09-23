import { expect, test, type Page } from "@playwright/test"
import { mockHost } from "./host-fixture"

/**
 * The metrics page after its design-system pass, against a mocked host.
 *
 * What is checked is the shape the redesign settled on and the features it
 * added, not the chart library: every block on the page is plain but the one
 * holding a table, the ten headline readings are tiles, the moments list can
 * zoom the charts, a zoom is a link, the window exports as a file, and the
 * live feed can be paused.
 * The screenshots at 1280 and 1720 are the eyes the assertions do not have.
 */

test.beforeEach(async ({ page }) => {
  await mockHost(page)
})

/**
 * §2: the only block on a page that may draw a frame is a table. A grid owns a
 * scroll region, and an edge is what says where it ends — a row whose actions
 * sit past the boundary otherwise reads as a row with no actions. Everything
 * else in the page's flow stays plain, which is what the frame is read against.
 *
 * Asserted structurally rather than as a count, so the rule keeps holding as
 * pages gain and lose tables.
 */
async function framedNonTables(page: Page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"))
      .filter((el) => !el.querySelector("[data-slot=table-container]"))
      .map((el) => el.outerHTML.slice(0, 120)),
  )
}

test("the metrics page is readings on the page, not boxes", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByRole("heading", { name: "Metrics" })).toBeVisible()
  await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })

  // Ten headline tiles, hairlines between, nothing around: the tenth is the
  // temperature because this host reports sensors.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(10)
  await expect(page.locator("[data-slot=stat-tile]", { hasText: "Temperature" })).toContainText(
    "62°C",
  )

  // Nothing draws a frame but the Interfaces table (§2): the readings, the
  // charts and the findings are all on the page's own ground.
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])

  // What the machine is made of, as a row of facts under the title.
  await expect(page.getByText("AMD EPYC 7B13")).toBeVisible()
  await expect(page.getByText("sampled every 15s, kept 7d")).toBeVisible()

  // The verdict's findings are a plain list, and the failed deploy is a moment.
  await expect(page.getByText("/ is filling up")).toBeVisible()
  await expect(page.getByText("api deploy failed")).toBeVisible()

  // Top processes from the process table, ordered by CPU by default.
  await expect(page.getByText("postgres", { exact: true })).toBeVisible()

  // Sensors appear under Hardware, hottest first.
  await expect(page.getByRole("heading", { name: "Temperatures" })).toBeVisible()
  await expect(page.getByText("nvme_Composite")).toBeVisible()
})

test("a moment zooms the charts and the zoom is a link", async ({ page }) => {
  await page.goto("/metrics")
  await page.getByRole("button", { name: /CPU peaked at 97%/ }).click({ timeout: 20_000 })

  await expect(page).toHaveURL(/[?&]from=\d+&to=\d+/)
  await expect(page.getByText(/^\d+[smhd] window$/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy link to this window" })).toBeVisible()

  // Opening the link lands on the same span rather than the named range.
  const url = page.url()
  await page.goto(url)
  await expect(page.getByText(/^\d+[smhd] window$/)).toBeVisible()

  await page.getByRole("button", { name: "Zoom out" }).click()
  await expect(page).not.toHaveURL(/from=/)
})

test("the window exports as a spreadsheet", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })
  // The tiles compare this window with the one before it.
  await expect(page.locator("[data-slot=stat-tile]", { hasText: "CPU" }).first()).toContainText("+")

  const download = page.waitForEvent("download")
  await page.getByRole("button", { name: "Export CSV" }).click()
  const file = await download
  expect(file.suggestedFilename()).toMatch(/^atlas-metrics-1h-.*\.csv$/)
  const text = await (await file.createReadStream()).toArray().then((c) => c.join(""))
  expect(text.split("\n")[0]).toMatch(/^time,cpu,cpuPeak,/)
})

test("the live feed can be paused", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByRole("heading", { name: "Metrics" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Pause live feed" })).toHaveCount(0)

  await page.getByRole("radio", { name: "Live" }).click()
  await page.getByRole("button", { name: "Pause live feed" }).click()
  await expect(page.getByRole("button", { name: "Resume live feed" })).toBeVisible()

  // Picking a recorded range drops the pause with it.
  await page.getByRole("radio", { name: "1h" }).click()
  await expect(page.getByRole("button", { name: /live feed/ })).toHaveCount(0)
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 3400 })
    await page.goto("/metrics")
    await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })
    await expect(page.locator(".recharts-cartesian-grid").first()).toBeAttached({
      timeout: 20_000,
    })
    // The page never scrolls sideways.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    )
    expect(overflow).toBe(false)
    await page.screenshot({ path: `test-results/metrics-${width}.png`, fullPage: true })
  })
}
