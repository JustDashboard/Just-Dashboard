import { expect, test, type Page } from "@playwright/test"
import { mockHost } from "./host-fixture"

const recordWorkspace = process.env.JD_WORKSPACE_VIDEO === "1"
test.use({ video: recordWorkspace ? "on" : "off" })

/**
 * The metrics page after its overhaul, against a mocked host.
 *
 * What is checked is the shape the redesign settled on and the features it
 * added, not the chart library: every block on the page is plain but the one
 * holding a table, the five moving readings are tiles carrying their window
 * as a trend, each resource is a section that reads what it is now beside
 * what it did, the moments list can zoom the charts, a zoom is a link, the
 * window exports as a file, and the live feed can be paused.
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
  await expect(page.getByRole("heading", { name: "Metrics" })).toHaveClass(/sr-only/)
  await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })

  // The five readings that move, as the Overview draws them: hairlines
  // between, nothing around, each over its line across the window on screen.
  const tiles = page.locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(5)
  for (const name of ["CPU", "Memory", "Load", "Network", "Disk I/O"]) {
    await expect(page.getByRole("img", { name: `${name} over the last hour` })).toBeVisible()
  }
  await expect(page.getByText("Last hour", { exact: true })).toBeVisible()
  await expect(page.getByRole("img", { name: "4 cores, the busiest at 31%" })).toBeVisible()

  // Nothing draws a frame but the Interfaces table (§2): the readings and the
  // charts are all on the page's own ground.
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])

  // What the machine is made of is the first visible row, and no sentence
  // stands above it: how to pin a moment is in the shortcuts.
  await expect(page.getByText("AMD EPYC 7B13")).toBeVisible()
  await expect(page.getByText("sampled every 15s, kept 7d")).toBeVisible()
  await expect(page.getByText("Click a chart to pin a moment")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Metrics shortcuts" })).toBeVisible()

  // The failed deploy is a moment.
  await expect(page.getByText("api deploy failed")).toBeVisible()

  // Top processes from the process table, ordered by CPU by default.
  await expect(page.getByText("postgres", { exact: true })).toBeVisible()

  // One section per resource, in the order a reader asks about them.
  const sections = await page
    .locator("[data-slot=page] > section > div:first-child h2")
    .evaluateAll((heads) => heads.map((h) => h.textContent))
  expect(sections).toEqual(["Resources", "Processor", "Memory", "Network", "Storage", "Saturation"])

  // Sensors appear beside the cores, hottest first.
  await expect(page.getByRole("heading", { name: "Temperatures" })).toBeVisible()
  await expect(page.getByText("nvme_Composite")).toBeVisible()
})

test("each resource reads what it is now beside what it did", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })

  // Every core is a column filled to its share.
  const cores = page.getByRole("list", { name: "Cores" }).getByRole("meter")
  await expect(cores).toHaveCount(4)
  await expect(cores.first()).toHaveAttribute("aria-valuenow", "31")
  await expect(page.getByText("mean 23% · busiest cpu0 at 31%")).toBeVisible()

  // Memory is split into what programs hold, the cache and what is free.
  const memory = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Allocation" }),
  })
  await expect(memory.getByRole("img", { name: /^Programs 9\.0 GB, Cache 4\.5 GB/ })).toBeVisible()
  await expect(memory.getByText("6.5 GB", { exact: true })).toBeVisible()
  await expect(memory.getByRole("meter", { name: "Swap used" })).toHaveAttribute(
    "aria-valuenow",
    "5",
  )

  // The five tiles that stood under the readings went to the charts they
  // explain: the stalls to Pressure, the processes to Load, sockets and open
  // files to Sockets, the disks to Filesystems.
  const panel = (title: string) =>
    page.locator("[data-slot=panel]", { has: page.getByRole("heading", { name: title }) })
  const head = (title: string) => panel(title).locator("[data-slot=panel-header]")
  await expect(head("Pressure").getByText("2.4%", { exact: true })).toBeVisible()
  await expect(head("Load average").getByText("184", { exact: true })).toBeVisible()
  await expect(head("Sockets").getByText("4,640", { exact: true })).toBeVisible()
  const disks = panel("Filesystems").getByRole("list", { name: "Filesystems" })
  await expect(disks.locator(":scope > li")).toHaveCount(2)
  await expect(disks.getByRole("button", { name: "scan" })).toHaveCount(2)

  // A sensor whose driver names the processor's maker carries its mark.
  await expect(
    panel("Temperatures").locator("li", { hasText: "k10temp_Tctl" }).locator("img[src$='amd.svg']"),
  ).toBeVisible()

  // An interface's row draws its in and out against the busiest one.
  await expect(
    panel("Interfaces").getByRole("img", { name: "2.3 MB/s in, 420.0 KB/s out" }),
  ).toBeVisible()
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

test("the interfaces are the real devices until everything is asked for", async ({ page }) => {
  await page.goto("/metrics")
  const panel = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Interfaces" }),
  })
  await expect(panel.getByText("eth0", { exact: true })).toBeVisible({ timeout: 20_000 })
  await expect(panel.getByText("docker0", { exact: true })).toHaveCount(0)
  await expect(panel.getByText("veth4f14332", { exact: true })).toHaveCount(0)

  await panel.getByRole("radio", { name: "Everything 3" }).click()
  await expect(panel.getByText("docker0", { exact: true })).toBeVisible()
  await expect(panel.getByText("veth4f14332", { exact: true })).toBeVisible()
})

test("the live feed can be paused", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByRole("heading", { name: "Metrics" })).toHaveClass(/sr-only/)
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

test("the controls stay reachable beside the machine facts on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/metrics")
  await expect(page.getByText("AMD EPYC 7B13")).toBeVisible()
  await expect(page.getByRole("button", { name: "Export CSV" })).toBeVisible()
  await expect(page.getByRole("radio", { name: "1h" })).toBeVisible()
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)
  await page.screenshot({ path: "test-results/metrics-phone.png", fullPage: true })
})

test("workspace: a pinned moment has readings, adjacent samples and a precise log handoff", async ({
  page,
}, testInfo) => {
  await page.goto("/metrics")
  const chart = page.getByRole("group", { name: /chart\. Click or press Enter/ }).first()
  await expect(chart.locator("svg.recharts-surface")).toBeVisible({ timeout: 20_000 })
  await chart.focus()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("status", { name: "" }).filter({ hasText: "Pinned" })).toBeVisible()
  await expect(chart.locator("[data-pinned-reading]")).toBeVisible()
  if (recordWorkspace) {
    await page.waitForTimeout(700)
    await page.screenshot({ path: testInfo.outputPath("metrics-pinned.png") })
  }
  const initial = await page
    .getByRole("link", { name: "Logs around this moment" })
    .getAttribute("href")
  await page.keyboard.press("ArrowLeft")
  const previous = await page
    .getByRole("link", { name: "Logs around this moment" })
    .getAttribute("href")
  expect(previous).not.toEqual(initial)
  if (recordWorkspace) await page.waitForTimeout(700)
  const question = new URL(previous!, "http://localhost").searchParams
  expect(question.get("source")).toBe("journal:")
  expect(Date.parse(question.get("until")!) - Date.parse(question.get("since")!)).toBe(120000)
  await page.keyboard.press("Escape")
  await expect(page.getByRole("link", { name: "Logs around this moment" })).toHaveCount(0)
  const bounds = (await chart.boundingBox())!
  await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)
  await page.mouse.down()
  await page.mouse.up()
  await expect(page.getByRole("link", { name: "Logs around this moment" })).toBeVisible()
  await page.mouse.move(0, 0)
  await expect(page.getByRole("link", { name: "Logs around this moment" })).toBeVisible()
  if (recordWorkspace) await page.waitForTimeout(700)
  await page.getByRole("button", { name: "Release moment" }).click()
  await expect(page.getByRole("link", { name: "Logs around this moment" })).toHaveCount(0)
})

test("connections are charted by how they fared", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })
  const panel = (title: string) =>
    page.locator("[data-slot=panel]", { has: page.getByRole("heading", { name: title }) })
  for (const title of ["Resent segments", "Connection RTT", "Failed connections"]) {
    await expect(panel(title).locator(".recharts-surface").first()).toBeVisible()
  }
  const reading = panel("Resent segments").locator("[data-slot=panel-header]")
  await expect(reading).toContainText("0.25%")
  await expect(reading).toContainText("38 ms")
})
