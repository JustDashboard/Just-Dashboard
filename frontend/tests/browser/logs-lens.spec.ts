import { expect, test } from "@playwright/test"
import { json, mockProject, now } from "./deploy-fixture"
import { PG, mockLensLogs, type LensMocks } from "./logs-lens-fixture"

/**
 * `/logs` reading a source through its lens: the lines by what they record,
 * a record's continuation folded under it, a repeat counted rather than
 * printed, the lens's questions one press each and sent to the server, a line
 * opened where it is, and Insights ranking what the window adds up to. The
 * lens's own rules are unit-tested (`src/lib/log-lenses.test.js`,
 * `log-insights.test.js`); this checks what reaches the reader and what
 * reaches the server.
 */

const pgLog = `/logs?source=${encodeURIComponent(PG)}`

/** The questions about lines: the lens's readings ask for counts (`limit=1`) each minute. */
const lineSearches = (mocks: LensMocks) => mocks.searches.filter((s) => s.get("limit") !== "1")

test("native log export discloses its cap, scan limit and implicit journal window", async ({
  page,
}) => {
  await mockLensLogs(page)
  await page.goto("/logs?source=journal%3A")
  await page.getByRole("button", { name: "Export", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("first 20,000 selected lines")
  await expect(dialog).toContainText("five-minute scan limit")
  await expect(dialog).toContainText("last 48 hours")
  await expect(dialog).toContainText("downloaded file records caps")
  await expect(dialog).not.toContainText("every line, on disk")
  await expect(dialog.getByRole("combobox", { name: "Window" })).toContainText("Available history")
})

test("custom log export validates native extended years and date order without crashing", async ({
  page,
}) => {
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  await mockLensLogs(page)
  await page.goto(pgLog)
  await page.getByRole("button", { name: "Export", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("combobox", { name: "Window" }).click()
  await page.getByRole("option", { name: "Custom range", exact: true }).click()
  await expect(dialog.getByRole("button", { name: "Download", exact: true })).toBeDisabled()
  const from = dialog.getByLabel("From", { exact: true })
  const input = await from.evaluate((element) => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!
    setter.call(element, "10000-01-01T00:00")
    element.dispatchEvent(new Event("input", { bubbles: true }))
    element.dispatchEvent(new Event("change", { bubbles: true }))
    return (element as HTMLInputElement).value
  })
  expect(input).toBe("10000-01-01T00:00")
  await expect(dialog.getByText("Log windows support years up to 9999.")).toBeVisible()
  await expect(from).toHaveAttribute("aria-invalid", "true")
  await expect(dialog.getByRole("button", { name: "Download", exact: true })).toBeDisabled()
  await expect(page.getByText("This page stopped rendering")).toHaveCount(0)
  await from.fill("2026-10-14T12:00")
  await dialog.getByLabel("To", { exact: true }).fill("2026-10-14T11:00")
  await expect(
    dialog.getByText("The start of the log window must be before its end."),
  ).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Download", exact: true })).toBeDisabled()
  await dialog.getByLabel("To", { exact: true }).fill("2026-10-14T13:00")
  await expect(dialog.getByRole("link", { name: "Download", exact: true })).toBeVisible()
  const href = await dialog
    .getByRole("link", { name: "Download", exact: true })
    .getAttribute("href")
  expect(new URL(href!, page.url()).searchParams.get("since")).toBe("2026-10-14T12:00:00.000Z")
  expect(errors).toEqual([])
})

test("the shared log time window disables Apply for unsupported native years", async ({ page }) => {
  await mockLensLogs(page)
  await page.goto(pgLog)
  await page.getByRole("button", { name: /^Time window:/ }).click()
  await page.locator("#window-from").evaluate((element) => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!
    setter.call(element, "10000-01-01T00:00")
    element.dispatchEvent(new Event("input", { bubbles: true }))
    element.dispatchEvent(new Event("change", { bubbles: true }))
  })
  await expect(page.getByText("Log windows support years up to 9999.")).toBeVisible()
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
})

test("a lensed log reads as its events, folds a record and counts a repeat", async ({ page }) => {
  await mockLensLogs(page)
  await page.goto(pgLog)
  const lines = page.getByLabel("Log lines")

  // The event word takes the level's place, in its verdict's colour.
  await expect(lines.getByText("deadlock", { exact: true })).toHaveClass(/text-destructive/)
  await expect(lines.getByText("slow", { exact: true })).toHaveClass(/text-warning/)
  await expect(lines.getByText("auth failed", { exact: true })).toBeVisible()

  // Three checkpoints in a row are one line that says how many.
  await expect(lines.getByText("×3", { exact: true })).toBeVisible()

  // The deadlock's six continuation lines: three shown, the rest one press away.
  const fold = lines.getByRole("button", { name: "3 more lines" })
  await expect(fold).toBeVisible()
  await expect(lines.getByText(/while updating tuple/)).toHaveCount(0)
  await fold.click()
  await expect(lines.getByText(/while updating tuple/)).toBeVisible()
  await expect(lines.getByRole("button", { name: "Fold the last 3" })).toBeVisible()
})

test("a quick view and a field's value narrow the stream on the server", async ({ page }) => {
  const mocks = await mockLensLogs(page)
  await page.goto(pgLog)
  await expect(page.getByLabel("Log lines").getByText("slow", { exact: true })).toBeVisible()
  expect(mocks.sockets.at(-1)?.getAll("f")).toEqual([])

  const slow = page.getByRole("button", { name: /^Slow\b/ })
  await slow.click()
  await expect(slow).toHaveAttribute("aria-pressed", "true")
  await expect.poll(() => mocks.sockets.at(-1)?.getAll("f")).toEqual(["event:slow"])
  await expect(page).toHaveURL(/[?&]f=event%3Aslow/)

  await page.getByRole("button", { name: /^Fields/ }).click()
  await page.getByRole("option", { name: /^postgres/ }).click()
  await expect
    .poll(() => mocks.sockets.at(-1)?.getAll("f"))
    .toEqual(["event:slow", "user:postgres"])
  await page.keyboard.press("Escape")
  // The question is no longer the quick view's, so it shows as what it is.
  await expect(page.getByRole("button", { name: "Clear the user filter" })).toBeVisible()
  await expect(slow).toHaveAttribute("aria-pressed", "false")
  expect(lineSearches(mocks)).toHaveLength(0)
})

test("paused, a new question's lines are held, and the pane says so rather than that none match", async ({
  page,
}) => {
  await mockLensLogs(page)
  await page.goto(pgLog)
  const lines = page.getByLabel("Log lines")
  await expect(lines.getByText("slow", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Pause" }).click()
  await page.getByRole("button", { name: /^Slow\b/ }).click()
  await expect(lines.getByText(/ lines? arrived while paused$/)).toBeVisible()
  await expect(page.getByText("Nothing in the recent window matches")).toHaveCount(0)
  await lines.getByRole("button", { name: "Resume" }).click()
  await expect(lines.getByText("slow", { exact: true })).toBeVisible()
})

test("a line opens in place, and the lines around it are two searches either side", async ({
  page,
}) => {
  const mocks = await mockLensLogs(page)
  await page.goto(pgLog)
  await page.getByLabel("Log lines").getByText("slow", { exact: true }).click()

  // The values the lens read, each a press from narrowing to it.
  await expect(page.getByRole("button", { name: "Only lines where user is postgres" })).toHaveCount(
    1,
  )
  await expect(page.getByRole("button", { name: "Only this event" })).toBeVisible()
  // Nothing is fetched for the context until it is asked for.
  expect(lineSearches(mocks)).toHaveLength(0)

  await page.getByRole("button", { name: "Lines around this" }).click()
  await expect.poll(() => lineSearches(mocks).length).toBe(2)
  const before = lineSearches(mocks).find((s) => s.has("until"))!
  const after = lineSearches(mocks).find((s) => s.get("order") === "asc")!
  expect(before.get("until")).toBe("2026-09-27T10:01:03.221Z")
  expect(before.get("limit")).toBe("25")
  expect(after.get("since")).toBe("2026-09-27T10:01:03.221Z")
  expect(after.get("limit")).toBe("25")
  for (const search of [before, after]) {
    expect(search.get("source")).toBe(PG)
    expect(search.has("f")).toBe(false)
    expect(search.has("levels")).toBe(false)
    expect(search.has("q")).toBe(false)
  }
  await expect(page.getByText("4 lines", { exact: true })).toBeVisible()

  await page.keyboard.press("Escape")
})

test("Insights ranks the window's values and a press narrows it in place", async ({ page }) => {
  const mocks = await mockLensLogs(page)
  await page.goto(`${pgLog}&mode=insights`)
  const insights = page.getByRole("button", { name: "Insights", exact: true })
  await expect(insights).toHaveAttribute("aria-pressed", "true")

  // A list per key the lens ranks — events, levels, users, databases,
  // clients, applications, codes — but not the query shape, which is read
  // through its group's sample.
  await expect(page.getByRole("heading", { name: "Events", exact: true })).toBeVisible()
  await expect(page.locator("[data-slot=bar-list]")).toHaveCount(7)
  await expect(page.getByRole("heading", { name: "Query shape" })).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Statement time" })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Slow statements" })).toBeVisible()
  await expect(page.getByText(/JOIN customers c ON/)).toBeVisible()
  await expect(page.getByRole("heading", { name: "Patterns" })).toBeVisible()
  // Insights opens on what the window adds up to, in words: its lines, the
  // errors and warnings among them, its busiest minute and the measure's
  // tail. The lens's own readings stay the chips' counts.
  const tiles = page.locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(5)
  await expect(tiles.filter({ hasText: "Errors" })).toContainText("2")
  await expect(tiles.filter({ hasText: "Warnings" })).toContainText("None")
  await expect(tiles.filter({ hasText: "Statement time p95" })).toContainText("1.84s")

  const overview = mocks.searches.find((s) => s.get("facets")?.includes("pattern"))!
  expect(overview.get("limit")).toBe("1")
  expect(overview.get("measure")).toBe("duration_ms")
  expect(overview.get("histogramBy")).toBe("event")
  const slowGroup = mocks.searches.find((s) => s.get("facets") === "fp")!
  expect(slowGroup.get("facetLimit")).toBe("20")
  expect(slowGroup.get("sample")).toBe("query")
  expect(slowGroup.getAll("f")).toEqual(["event:slow"])

  const asked = mocks.searches.length
  await page.getByRole("button", { name: "Only lines where user is postgres" }).click()
  await expect
    .poll(() =>
      mocks.searches
        .slice(asked)
        .find((s) => s.get("facets") === "fp")
        ?.getAll("f"),
    )
    .toEqual(["event:slow", "user:postgres"])
  await expect(insights).toHaveAttribute("aria-pressed", "true")
  await expect(page).toHaveURL(/mode=insights/)
  await expect(page.getByRole("button", { name: "Clear the user filter" })).toBeVisible()
})

test("the source is named over the workbench, and the strip is its views", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const mocks = await mockLensLogs(page)
  await page.goto(pgLog)

  // What is being read, as the page's opening line, with its verbs at its end.
  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity.getByText("postgresql-16-main.log", { exact: true })).toBeVisible()
  await expect(identity.getByText("/var/log/postgresql/postgresql-16-main.log")).toBeVisible()
  await expect(identity.getByRole("button", { name: "Export" })).toBeVisible()
  await expect(identity.getByRole("button", { name: "Logs shortcuts" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Export" })).toHaveCount(1)

  // The strip is the views alone, from its leading edge, as a deployment's is.
  const strip = page.getByRole("navigation", { name: "Log mode" })
  const toggle = await page.getByRole("button", { name: "Hide the sources" }).boundingBox()
  const live = await strip.getByRole("button", { name: "Live", exact: true }).boundingBox()
  expect(live!.x - (toggle!.x + toggle!.width)).toBeLessThan(16)

  // No tiles over the frame: the lens's readings are the counts on its row's
  // chips, at every width, and the lines keep the height.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  const slow = page.getByRole("button", { name: /^Slow\b/ })
  await expect(slow).toContainText("4")

  // A press narrows the lines to what it counts, and a second lets it go.
  await slow.click()
  await expect.poll(() => mocks.sockets.at(-1)?.getAll("f")).toEqual(["event:slow"])
  await expect(slow).toHaveAttribute("aria-pressed", "true")
  await slow.click()
  await expect.poll(() => mocks.sockets.at(-1)?.getAll("f")).toEqual([])
})

test("the rail draws a stack as what it runs and offers the journal's readings a host lacks", async ({
  page,
}) => {
  await mockLensLogs(page)
  await page.goto(pgLog)
  const rail = page.locator('[aria-label="Log sources"]')
  const stack = rail.getByRole("button", { name: /^shop/ })
  await expect(stack.locator('img[src="/logos/postgresql.svg"]')).toBeVisible()
  await expect(stack.locator('img[src="/logos/redis.svg"]')).toBeVisible()
  // No kern.log and no cron file: the journal reads them by program. The
  // host has an auth.log, so an SSH log from the journal would be a second
  // row for the same lines.
  await expect(rail.getByRole("button", { name: /^Kernel ring/ })).toBeVisible()
  await expect(rail.getByRole("button", { name: /^Cron/ })).toBeVisible()
  await expect(rail.getByRole("button", { name: /^SSH log/ })).toHaveCount(0)
})

test("a service page's logs open on the lens's defaults and drop them for another vocabulary", async ({
  page,
}) => {
  // A deployment's Output is the service logs every page embeds, one source
  // per container its release runs.
  const container = (containerId: string, name: string, image: string) => ({
    containerId,
    name,
    releaseId: 20,
    liveRelease: true,
    state: "running",
    health: "healthy",
    imageId: `sha256:${containerId.padEnd(64, "0")}`,
    image,
  })
  await mockProject(page, {
    runtime: {
      status: "available",
      observedAt: now,
      services: [
        container("gate", "ssh-gate", "linuxserver/openssh-server"),
        container("db", "shop-db", "postgres:16"),
      ],
    },
  })
  const sockets: URLSearchParams[] = []
  await page.routeWebSocket(/\/api\/v1\/logs\/stream/, (socket) => {
    const params = new URL(socket.url()).searchParams
    sockets.push(params)
    socket.send(
      JSON.stringify({
        type: "logs",
        data: [{ text: `hello from ${params.get("source")}`, level: "info" }],
        ts: Date.now(),
      }),
    )
  })
  const searches: URL[] = []
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    // The page names no lens; the server describes each container as its image reads.
    if (url.pathname.endsWith("/logs/source")) {
      return json(route, {
        lens: url.searchParams.get("source") === "docker:gate" ? "auth" : "postgres",
      })
    }
    if (url.pathname.endsWith("/logs/search")) searches.push(url)
    return json(route, {})
  })

  await page.goto("/deploy/7/logs?view=output&service=gate")
  await expect(page.getByText("hello from docker:gate")).toBeVisible()
  // The auth lens hides its scans and cron sessions from the first socket —
  // not a second one after the fact — and says so as a chip.
  await expect(
    page.getByRole("button", { name: "Clear the scans and cron sessions hidden filter" }),
  ).toBeVisible()
  expect(sockets).toHaveLength(1)
  expect(sockets[0].getAll("f")).toEqual(["event:!cron_session", "event:!ssh_scan"])

  await page.getByRole("combobox", { name: "Service" }).click()
  await page.getByRole("option", { name: "shop-db" }).click()
  await expect(page.getByText("hello from docker:db")).toBeVisible()
  expect(sockets.at(-1)!.getAll("f")).toEqual([])
  expect(searches).toHaveLength(0)
})

test("the window is one control, and a preset picked on the stream opens History over it", async ({
  page,
}) => {
  const mocks = await mockLensLogs(page)
  await page.goto(pgLog)
  await expect(page.getByLabel("Log lines").getByText("slow", { exact: true })).toBeVisible()

  // On the stream the window says it is following, not a range it is not reading.
  const window = page.getByRole("button", { name: /^Time window: / })
  await expect(window).toHaveText(/Live/)
  await window.click()
  await expect(page.getByRole("button", { name: /^Live tail/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await page.getByRole("button", { name: "6 hours", exact: true }).click()

  await expect(page.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(window).toHaveText(/Last 6 hours/)
  await expect.poll(() => lineSearches(mocks).at(-1)?.get("since")).toBeTruthy()
  const since = Date.parse(lineSearches(mocks).at(-1)!.get("since")!)
  expect(Math.abs(Date.now() - since - 6 * 3_600_000)).toBeLessThan(120_000)

  // A span typed into the same popover is the window too, and its bounds go to the server.
  await window.click()
  await page.getByLabel("From", { exact: true }).fill("2026-09-27T10:00")
  await page.getByLabel("To", { exact: true }).fill("2026-09-27T10:30")
  await page.getByRole("button", { name: "Apply", exact: true }).click()
  await expect
    .poll(() => lineSearches(mocks).at(-1)?.get("until"))
    .toBe(new Date("2026-09-27T10:30").toISOString())
  await expect(window).not.toHaveText(/Last/)

  // And back to the stream from the same place.
  await window.click()
  await page.getByRole("button", { name: /^Live tail/ }).click()
  await expect(page.getByRole("button", { name: "Live", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("what Options sets is drawn in the filter row, and every narrowing has one way back", async ({
  page,
}) => {
  const mocks = await mockLensLogs(page)
  await page.goto(pgLog)
  await expect(page.getByLabel("Log lines").getByText("slow", { exact: true })).toBeVisible()

  const options = page.getByRole("button", { name: "Options", exact: true })
  await options.click()
  await page.getByLabel("Hide lines containing").fill("checkpoint")
  await page.keyboard.press("Escape")
  await expect(options).toContainText("1")
  const hiding = page.getByRole("button", { name: "Stop hiding lines", exact: true })
  await expect(hiding).toContainText("checkpoint")
  await expect.poll(() => mocks.sockets.at(-1)?.get("exclude")).toBe("checkpoint")

  // The levels lead the same row, each with its count, and Clear filters
  // lets every narrowing go at once.
  const levels = page.getByRole("group", { name: "Levels" })
  await levels.getByRole("button", { name: /^error/ }).click()
  await expect.poll(() => mocks.sockets.at(-1)?.get("levels")).toBe("error")
  await page.getByRole("button", { name: "Clear filters", exact: true }).click()
  await expect(hiding).toHaveCount(0)
  await expect(levels.getByRole("button", { name: /^error/ })).toHaveAttribute(
    "aria-pressed",
    "false",
  )
  await expect.poll(() => mocks.sockets.at(-1)?.has("exclude")).toBe(false)
  await expect(page.getByRole("button", { name: "Clear filters", exact: true })).toHaveCount(0)
})

test("the stream says how busy it is, and the view switches are one group", async ({ page }) => {
  await mockLensLogs(page)
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const at = (ago: number) => new Date(Date.now() - ago).toISOString()
    socket.send(
      JSON.stringify({ type: "meta", data: { kind: "app", lens: "postgres" }, ts: Date.now() }),
    )
    socket.send(
      JSON.stringify({
        type: "logs",
        data: [
          { text: "a", level: "info", timestamp: at(30_000) },
          { text: "b", level: "error", timestamp: at(4_000) },
          { text: "c", level: "info", timestamp: at(1_000) },
        ],
        ts: Date.now(),
      }),
    )
  })
  await page.goto(pgLog)
  await expect(page.getByTitle("3 lines written in the last minute")).toBeVisible()
  await expect(page.getByText("3 a minute", { exact: true })).toBeVisible()

  const view = page.getByRole("group", { name: "How the lines are drawn" })
  await expect(view.getByRole("button", { name: "Wrap" })).toBeVisible()
  await expect(view.getByRole("button", { name: "Colour" })).toHaveAttribute("aria-pressed", "true")
})
