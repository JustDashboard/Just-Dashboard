import { expect, test, type Page } from "@playwright/test"
import { host, mockHost, snapshot } from "./host-fixture"
import { risen } from "./risen"

const recordWorkspace = process.env.JD_WORKSPACE_VIDEO === "1"
test.use({ video: recordWorkspace ? "on" : "off" })

/**
 * The metrics page after its overhaul, against a mocked host.
 *
 * What is checked is the shape the redesign settled on and the features it
 * added, not the chart library: every block on the page is plain, the
 * interfaces included, the five moving readings are tiles carrying their window
 * as a trend, each resource is a section that reads what it is now beside
 * what it did, the moments list groups what happened together and pins or
 * zooms it, a zoom is a link, the
 * window exports as a file, and the live feed can be paused.
 * The screenshots at 1280 and 1720 are the eyes the assertions do not have.
 */

test.beforeEach(async ({ page }) => {
  await mockHost(page)
})

/**
 * §2 lets a table keep a frame, for the scroll region it owns. Metrics has none
 * left: the interfaces were its one table and are drawn as the filesystems are
 * since 0.8, so every block in the page's flow is on the page's own ground.
 */
async function framedBlocks(page: Page) {
  return page.evaluate(() =>
    Array.from(
      document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"),
    ).map((el) => el.outerHTML.slice(0, 120)),
  )
}

test("the metrics page is readings on the page, not boxes", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByRole("heading", { name: "Metrics" })).toHaveClass(/sr-only/)
  await expect(page.getByText("CPU 97%", { exact: true })).toBeVisible({ timeout: 20_000 })

  // The five readings that move, as the Overview draws them: hairlines
  // between, nothing around, each over its line across the window on screen.
  const tiles = page.locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(5)
  for (const name of ["CPU", "Memory", "Load", "Network", "Disk I/O"]) {
    await expect(page.getByRole("img", { name: `${name} over the last hour` })).toBeVisible()
  }
  await expect(page.getByText("Last hour", { exact: true })).toBeVisible()
  await expect(page.getByRole("img", { name: "4 cores, the busiest at 31%" })).toBeVisible()

  // Nothing draws a frame: the readings, the charts and the interfaces are all
  // on the page's own ground.
  expect(await framedBlocks(page), "a framed block").toEqual([])

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
  await expect(page.getByText("CPU 97%", { exact: true })).toBeVisible({ timeout: 20_000 })

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

  // A scan that ran out of time answers with what it counted, and says which
  // figures are floors.
  await page.route("**/api/v1/system/disk-usage?*", (route) =>
    route.fulfill({
      json: [
        {
          name: "home",
          path: "/home",
          size: 4 * 1024 ** 3,
          isDir: true,
          entries: 900,
          partial: true,
        },
        { name: "var", path: "/var", size: 2 * 1024 ** 3, isDir: true, entries: 300 },
      ],
    }),
  )
  await disks.getByRole("button", { name: "scan" }).first().click()
  await expect(disks.getByText("at least 4.0 GB", { exact: true })).toBeVisible()
  await expect(disks.getByText("2.0 GB", { exact: true })).toBeVisible()

  // A sensor whose driver names the processor's maker carries its mark.
  await expect(
    panel("Temperatures").locator("li", { hasText: "k10temp_Tctl" }).locator("img[src$='amd.svg']"),
  ).toBeVisible()

  // An interface's row draws its in and out against the busiest one.
  await expect(
    panel("Interfaces").getByRole("img", { name: "2.3 MB/s in, 420.0 KB/s out" }),
  ).toBeVisible()
})

test("an incident is the signals that came together, and pins or zooms the charts", async ({
  page,
}) => {
  // Two cold loads of the page, one of them a zoomed link.
  test.setTimeout(60_000)
  await page.goto("/metrics")
  const panel = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Notable moments" }),
  })
  const incidents = panel.getByRole("list", { name: "Incidents" }).locator(":scope > li")

  // The failed deploy and the spike it caused are one incident, led by the
  // deploy, rather than five rows stamped with the same minute.
  await expect(incidents).toHaveCount(1, { timeout: 20_000 })
  await expect(panel.getByText("1 incident · 5 signals")).toBeVisible()
  const incident = panel.getByRole("button", { name: /api deploy failed/ })
  for (const signal of ["CPU 97%", "load 5.20", "disk 45 ms"]) {
    await expect(incidents.first().getByText(signal, { exact: true })).toBeVisible()
  }

  // Pressing it pins its moment on every chart and leaves the window alone.
  await incident.click()
  await expect(incident).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("link", { name: "Logs around this moment" })).toBeVisible()
  await expect(page).not.toHaveURL(/[?&]from=/)
  await incident.click()
  await expect(incident).toHaveAttribute("aria-pressed", "false")
  await expect(page.getByRole("link", { name: "Logs around this moment" })).toHaveCount(0)

  // Zooming is the row's own verb, and the moment stays pinned through it.
  await panel.getByRole("button", { name: "Zoom the charts to this incident" }).click()
  await expect(page).toHaveURL(/[?&]from=\d+&to=\d+/)
  await expect(page.getByText(/^\d+[smhd] window$/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy link to this window" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Logs around this moment" })).toBeVisible()
  await expect(incident).toHaveAttribute("aria-pressed", "true")

  // Opening the link lands on the same span rather than the named range.
  const url = page.url()
  await page.goto(url)
  await expect(page.getByText(/^\d+[smhd] window$/)).toBeVisible()

  await page.getByRole("button", { name: "Zoom out" }).click()
  await expect(page).not.toHaveURL(/from=/)
})

test("the window exports as a spreadsheet", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByText("CPU 97%", { exact: true })).toBeVisible({ timeout: 20_000 })
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

  // The scope is the block's head, as Top processes keeps its order there.
  await panel
    .locator("[data-slot=panel-header]")
    .getByRole("radio", { name: "Everything 3" })
    .click()
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
    await expect(page.getByText("CPU 97%", { exact: true })).toBeVisible({ timeout: 20_000 })
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
  await expect(page.getByText("CPU 97%", { exact: true })).toBeVisible({ timeout: 20_000 })
  const panel = (title: string) =>
    page.locator("[data-slot=panel]", { has: page.getByRole("heading", { name: title }) })
  // Below the first screen, where a plot is drawn once the reader is near it.
  await panel("Resent segments").scrollIntoViewIfNeeded()
  for (const title of ["Resent segments", "Connection RTT", "Failed connections"]) {
    await expect(panel(title).locator(".recharts-surface").first()).toBeVisible()
  }
  const reading = panel("Resent segments").locator("[data-slot=panel-header]")
  await expect(reading).toContainText("0.25%")
  await expect(reading).toContainText("38 ms")
})

/** The pane the dashboard scrolls: the page's own, not the window. */
const pane = (page: Page) => page.locator("[data-slot=page]").locator("xpath=..")

for (const size of [
  { width: 1280, height: 720 },
  { width: 390, height: 844 },
]) {
  test(`a chart is drawn when the reader comes near it, in a frame that was already there, at ${size.width}`, async ({
    page,
  }) => {
    await page.setViewportSize(size)
    await page.goto("/metrics")
    await expect(page.getByText("CPU 97%", { exact: true })).toBeVisible({ timeout: 20_000 })
    await risen(page)

    // Every chart's frame is in the page from the start; the plots are not.
    const frames = page.locator("[data-slot=metric-chart]")
    const plots = page.locator("[data-slot=metric-chart] .recharts-wrapper")
    const total = await frames.count()
    expect(total).toBeGreaterThanOrEqual(10)
    // The first is on the first screen at a laptop's width, a scroll away on a phone.
    await frames.first().scrollIntoViewIfNeeded()
    await expect(frames.first().locator("svg.recharts-surface")).toBeVisible()
    const last = frames.last()
    await expect(last.locator(".recharts-wrapper")).toHaveCount(0)
    expect(await plots.count(), "plots drawn on arrival").toBeLessThan(total)

    // Coming near draws it where its frame stood: the frame keeps its place
    // and its height, and the page under it grows by nothing.
    const tall = () => pane(page).evaluate((el) => el.scrollHeight)
    const place = () =>
      last.evaluate((el) => {
        const sheet = el.closest("[data-slot=page]")!.getBoundingClientRect()
        const box = el.getBoundingClientRect()
        return {
          top: Math.round(box.top - sheet.top),
          left: Math.round(box.left - sheet.left),
          height: Math.round(box.height),
        }
      })
    const before = { place: await place(), tall: await tall() }
    await last.scrollIntoViewIfNeeded()
    await expect(last.locator("svg.recharts-surface")).toBeVisible()
    expect(await place()).toEqual(before.place)
    expect(await tall()).toBe(before.tall)

    // One that is left stays drawn: going back up takes nothing away.
    const drawn = await plots.count()
    await frames.first().scrollIntoViewIfNeeded()
    await expect(last.locator(".recharts-wrapper")).toHaveCount(1)
    expect(await plots.count()).toBeGreaterThanOrEqual(drawn)
  })
}

test("a live figure in a chart's head does not redraw the chart under it", async ({ page }) => {
  let push: (data: unknown) => void = () => {}
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => {
    socket.send(JSON.stringify({ type: "host", data: host }))
    socket.send(JSON.stringify({ type: "metrics", data: snapshot }))
    push = (data) => socket.send(JSON.stringify({ type: "metrics", data }))
  })
  await page.setViewportSize({ width: 1280, height: 2400 })
  await page.goto("/metrics")
  const sockets = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Sockets" }),
  })
  await expect(sockets.locator("svg.recharts-surface")).toBeVisible({ timeout: 20_000 })
  const head = sockets.locator("[data-slot=panel-header]")
  await expect(head.getByText("312", { exact: true })).toBeVisible()

  // recharts measures a line's length each time it renders one, so a count of
  // those measurements is a count of plots redrawn. The window on screen is
  // the recorded hour: a frame of the socket changes the heads and no row.
  await page.evaluate(() => {
    const w = window as unknown as { measured: number }
    w.measured = 0
    const measure = SVGGeometryElement.prototype.getTotalLength
    SVGGeometryElement.prototype.getTotalLength = function () {
      w.measured++
      return measure.call(this)
    }
  })
  push({ ...snapshot, sockets: { ...snapshot.sockets, tcpInUse: 313 } })
  await expect(head.getByText("313", { exact: true })).toBeVisible()
  expect(await page.evaluate(() => (window as unknown as { measured: number }).measured)).toBe(0)
})
