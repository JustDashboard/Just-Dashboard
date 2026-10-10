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
  // The lens's readings stand over the workbench rather than in Insights,
  // which on this window carries them as its chips' counts instead.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

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
