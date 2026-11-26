import { expect, test, type Page } from "@playwright/test"
import { mockProject } from "./deploy-fixture"
import { STACK_PASSWORD, mockStackProject, stackState } from "./deploy-stack-fixture"

/**
 * A stack project's page, `/deploy/[id]/stack`: the server's sentence and
 * state, the five facts each on its own, what needs the person, the services
 * and how each is reached, the setup step by step and the accounts it made.
 */

const lead = (page: Page) => page.locator("[data-slot='stack-lead']")
const facts = (page: Page) => page.getByRole("list", { name: "What the stack can do" })
const services = (page: Page) => page.getByRole("list", { name: "Services", exact: true })
/** A row of the project's rail, by where it goes: Runtime is also a settings page's name. */
const railRow = (page: Page, path: string) =>
  page.locator("[data-slot='sidebar']").first().locator(`a[href="/deploy/7${path}"]`)

test("a ready stack reads as ready, with every service, how it is reached and the whole setup", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("ready") })
  await page.goto("/deploy/7/stack")

  // A reading page: the project's own header, then what is true of the stack.
  await expect(page.locator("[data-slot='page']")).toHaveAttribute("data-register", "reading")
  await expect(page.getByRole("heading", { name: "Media server", exact: true })).toBeVisible()
  await expect(lead(page)).toContainText("Complete · v1.0.0")
  await expect(lead(page).locator("[data-slot='stack-summary']")).toHaveText(
    "Everything is set up and connected: a request in Seerr is searched for, downloaded and lands in Jellyfin.",
  )
  await expect(lead(page).getByText("Ready", { exact: true }).first()).toBeVisible()
  await expect(facts(page).getByRole("listitem")).toHaveText([
    "Installed",
    "Every service healthy",
    "Services connected",
    "Can fetch new titles",
    "Can play your library",
  ])
  // Nothing needs a look, so nothing is drawn for it; and no figure is a tile.
  await expect(page.getByRole("region", { name: "What needs a look" })).toHaveCount(0)
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)

  const rows = services(page).locator("[data-slot='choice-row']")
  await expect(rows).toHaveCount(8)
  // The public service is a link to where it answers.
  const jellyfin = rows.filter({ hasText: "Jellyfin" })
  await expect(jellyfin).toContainText("Plays your films and series")
  await expect(jellyfin).toContainText("media.example.test")
  await expect(jellyfin).toContainText("Healthy")
  await expect(
    jellyfin.getByRole("link", { name: "Open Jellyfin at media.example.test" }),
  ).toHaveAttribute("href", "https://media.example.test")
  // Its card opens its container in Docker.
  await expect(jellyfin.getByRole("link", { name: "Open Jellyfin's container" })).toHaveAttribute(
    "href",
    `/docker/containers/${stack.containerId("jellyfin")}`,
  )

  // A private service says its address on this server, and offers no way to
  // open it that would not work: the tunnel is behind the mark.
  const seerr = rows.filter({ hasText: "Seerr" })
  await expect(seerr).toContainText("127.0.0.1:28055")
  await expect(seerr.getByRole("link", { name: /Open Seerr at/ })).toHaveCount(0)
  await seerr.getByRole("button", { name: "How to reach Seerr" }).click()
  const how = page.getByRole("dialog", { name: "How to reach Seerr" })
  await expect(how).toContainText("which only this server can open")
  await expect(how).toContainText("ssh -L 28055:127.0.0.1:28055 user@")
  await expect(how).toContainText("http://127.0.0.1:28055")
  // The mark is a toggle: a second press puts the card away.
  await seerr.getByRole("button", { name: "How to reach Seerr" }).click()
  await page.mouse.move(0, 0)
  await expect(how).toBeHidden()
  // A worker has no address and no mark.
  const recyclarr = rows.filter({ hasText: "Recyclarr" })
  await expect(recyclarr.getByRole("button")).toHaveCount(0)
  await expect(recyclarr).toContainText("Running")

  // Every row is one height, whatever it carries.
  const heights = await rows.evaluateAll((items) =>
    items.map((item) => Math.round(item.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)

  // The setup by stage, in the order a pass runs it.
  const setup = page.getByRole("region", { name: "Setting up each application" })
  await expect(setup.getByRole("listitem")).toHaveCount(11)
  await expect(page.getByRole("region", { name: "Connecting them to each other" })).toContainText(
    "8 of 8",
  )
  await expect(page.getByRole("region", { name: "Checking the connections" })).toContainText(
    "3 of 3",
  )
  await expect(page.getByText("22 of 22 done", { exact: true })).toBeVisible()
  await expect(page.locator("[data-slot='stack-skipped']")).toHaveCount(0)

  // What it does not do is said again where it runs.
  await expect(page.getByRole("list", { name: "What this stack does not do" })).toContainText(
    "An existing library elsewhere on this server is not mounted",
  )
  // The rail has the page's row, and it is the current one.
  await expect(railRow(page, "/stack")).toHaveText("Stack")
  await expect(railRow(page, "/stack")).toHaveAttribute("data-active", "true")
  await page.screenshot({ path: "test-results/deploy-stack-ready-1440.png", fullPage: true })
})

test("a stack waiting for its release says so, links the run and claims nothing else", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("waiting") })
  await page.goto("/deploy/7/stack")
  await expect(lead(page).getByText("Not installed yet", { exact: true }).first()).toBeVisible()
  // Only the one fact that can be read: its containers do not exist.
  await expect(facts(page).getByRole("listitem")).toHaveText(["Not installed"])
  const waiting = page.getByRole("region", { name: "What needs a look" })
  await expect(waiting).toContainText("The deployment's run is still bringing the release up")
  await expect(waiting.getByRole("link", { name: "Watch the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/84",
  )
  // Nothing to configure yet, so no button offers it; no account exists yet.
  await expect(lead(page).getByRole("button", { name: /Repair|Retry/ })).toHaveCount(0)
  await expect(page.getByRole("list", { name: "Accounts the stack created" })).toHaveCount(0)
  await expect(
    page.getByText(/6 more accounts appear once their applications are set up/),
  ).toBeVisible()
  // A service with no container is a row that opens nothing.
  const rows = services(page).locator("[data-slot='choice-row']")
  await expect(rows).toHaveCount(8)
  await expect(rows.first()).toContainText("No container")
  await expect(rows.first().getByRole("link")).toHaveCount(0)

  // It asks every two seconds while the release is coming up, and the page
  // follows without a reload when it lands.
  const before = stack.reads()
  await expect.poll(() => stack.reads() - before, { timeout: 12_000 }).toBeGreaterThanOrEqual(2)
  stack.set(stackState("configuring"))
  await expect(lead(page).getByText("Setting up", { exact: true }).first()).toBeVisible({
    timeout: 12_000,
  })
})

test("a stack being configured shows the pass moving and is never called ready", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("configuring") })
  await page.goto("/deploy/7/stack")
  await expect(lead(page).getByText("Setting up", { exact: true }).first()).toBeVisible()
  await expect(lead(page).getByText("Ready", { exact: true })).toHaveCount(0)
  await expect(facts(page).getByRole("listitem")).toHaveText([
    "Installed",
    "Every service healthy",
    "Services not connected",
    "Cannot fetch new titles",
    "Cannot play yet",
  ])
  await expect(page.getByText("7 of 22 done", { exact: true })).toBeVisible()
  const running = page.locator("li[data-status='running']")
  await expect(running).toHaveCount(1)
  await expect(running).toContainText("Secure Sonarr with its own sign-in")
  await expect(running).toContainText("Attempt 2")
  await expect(page.locator("li[data-status='pending']").first()).toContainText("Waiting")
  // A pass is running, so there is nothing to press.
  await expect(lead(page).getByRole("button", { name: /Repair|Retry/ })).toHaveCount(0)
  // Only the account that exists is listed.
  await expect(
    page
      .getByRole("list", { name: "Accounts the stack created" })
      .locator("[data-slot='choice-row']"),
  ).toHaveCount(1)

  stack.set(stackState("ready"))
  await expect(lead(page).getByText("Ready", { exact: true }).first()).toBeVisible({
    timeout: 12_000,
  })
  await expect(page.getByText("22 of 22 done", { exact: true })).toBeVisible()
})

test("what only the person can do is said in words, the blocking one first", async ({ page }) => {
  await mockStackProject(page, { state: stackState("action_required") })
  await page.goto("/deploy/7/stack")
  await expect(lead(page).getByText("Needs you", { exact: true }).first()).toBeVisible()
  await expect(lead(page).locator("[data-slot='stack-summary']")).toContainText(
    "nothing can be downloaded until you add a media source in Prowlarr",
  )
  await expect(facts(page).getByRole("listitem").nth(3)).toHaveText("Cannot fetch new titles")
  const actions = page.locator("[data-slot='stack-action']")
  await expect(actions).toHaveCount(2)
  await expect(actions.first()).toHaveAttribute("data-blocking", "true")
  await expect(actions.first()).toContainText("Add a media source in Prowlarr")
  await expect(actions.first()).toContainText(
    "Open Prowlarr, add an indexer you are allowed to use",
  )
  await expect(actions.first()).toContainText(
    "Prowlarr · the stack is not ready until this is done",
  )
  await expect(actions.nth(1)).toHaveAttribute("data-blocking", "false")
  await expect(actions.nth(1)).toContainText("Let people in to Seerr")
  await expect(actions.nth(1)).toContainText("optional")
  await expect(
    services(page).locator("[data-slot='choice-row']").filter({ hasText: "Prowlarr" }),
  ).toContainText("Needs you")
  await expect(lead(page).getByRole("button", { name: "Repair", exact: true })).toBeVisible()
})

test("a failed stack shows the error, collapses what it blocked, and Retry starts a pass", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("failed") })
  await page.goto("/deploy/7/stack")
  await expect(lead(page).getByText("Setup failed", { exact: true }).first()).toBeVisible()
  const failure = page.locator("[data-slot='stack-failure']")
  await expect(failure).toHaveCount(1)
  await expect(failure).toContainText("Give qBittorrent's web interface its own password")
  await expect(failure).toContainText(
    "qBittorrent answered 403 to POST /api/v2/auth/login: the temporary password in its log was not accepted ([redacted])",
  )
  await expect(failure).toContainText("qBittorrent · failed after 8 attempts")
  // The four steps behind it are one line with their reason, not four rows.
  await expect(page.locator("[data-slot='stack-skipped']")).toHaveText(
    "4 steps skipped: Waiting on qBittorrent's password.",
  )
  await expect(page.locator("li[data-status]")).toHaveCount(18)
  await expect(page.locator("li[data-status='failed']")).toContainText("Failed")
  await expect(page.getByText("17 of 18 done", { exact: true })).toBeVisible()
  await expect(
    services(page).locator("[data-slot='choice-row']").filter({ hasText: "qBittorrent" }),
  ).toContainText("Setup failed")

  await lead(page).getByRole("button", { name: "Retry", exact: true }).click()
  await expect.poll(() => stack.configures().length).toBe(1)
  expect(stack.configures()[0]).toEqual({})
  // 202: the page follows the pass the press started.
  await expect(lead(page).getByText("Setting up", { exact: true }).first()).toBeVisible()
  await expect(lead(page).getByRole("button", { name: /Repair|Retry/ })).toHaveCount(0)
})

for (const [code, words] of [
  ["stack_busy", "A setup pass is already running. This page follows it."],
  ["stack_waiting", "There is no live release yet, so there is nothing to set up."],
] as const) {
  test(`a pass refused as ${code} is answered in words, not with its code`, async ({ page }) => {
    const stack = await mockStackProject(page, { state: stackState("failed"), configure: code })
    await page.goto("/deploy/7/stack")
    await lead(page).getByRole("button", { name: "Retry", exact: true }).click()
    await expect.poll(() => stack.configures().length).toBe(1)
    await expect(lead(page).getByRole("status")).toContainText(words)
    await expect(page.getByText(code)).toHaveCount(0)
    await expect(page.locator("[data-sonner-toast]")).toHaveCount(0)
  })
}

test("a preset's skipped steps are one quiet line and no stage of nothing is drawn", async ({
  page,
}) => {
  await mockStackProject(page, { state: stackState("ready", { essential: true }) })
  await page.goto("/deploy/7/stack")
  await expect(lead(page)).toContainText("Essential · v1.0.0")
  await expect(services(page).locator("[data-slot='choice-row']")).toHaveCount(2)
  await expect(page.locator("[data-slot='stack-skipped']")).toHaveText(
    "16 steps skipped: Not part of the Essential preset.",
  )
  await expect(page.locator("li[data-status]")).toHaveCount(6)
  await expect(page.getByText("6 of 6 done", { exact: true })).toBeVisible()
  // Ready, and still honest about what this preset does not do.
  await expect(facts(page).getByRole("listitem").nth(3)).toHaveText("Cannot fetch new titles")
  await expect(page.getByRole("list", { name: "What this stack does not do" })).toContainText(
    "A request cannot be fulfilled automatically",
  )
})

test("an account's password is revealed where its mask was, and only one that exists is listed", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("ready") })
  await page.goto("/deploy/7/stack")
  const accounts = page.getByRole("list", { name: "Accounts the stack created" })
  const rows = accounts.locator("[data-slot='choice-row']")
  await expect(rows).toHaveCount(6)
  const jellyfin = rows.first()
  await expect(jellyfin).toContainText("Jellyfin administrator (also signs in to Seerr)")
  await expect(jellyfin).toContainText("admin")
  await expect(page.getByText(STACK_PASSWORD)).toHaveCount(0)
  expect(stack.reveals()).toEqual([])
  const height = (await jellyfin.boundingBox())!.height

  await jellyfin
    .getByRole("button", {
      name: "Reveal the password for Jellyfin administrator (also signs in to Seerr)",
    })
    .click()
  await expect(jellyfin.getByText(STACK_PASSWORD)).toBeVisible()
  expect(stack.reveals()).toEqual(["jellyfin-admin"])
  // Revealing moves nothing: the row is the height it was.
  expect((await jellyfin.boundingBox())!.height).toBe(height)
  await expect(rows.nth(1).getByText(STACK_PASSWORD)).toHaveCount(0)

  await jellyfin
    .getByRole("button", {
      name: "Hide the password for Jellyfin administrator (also signs in to Seerr)",
    })
    .click()
  await expect(page.getByText(STACK_PASSWORD)).toHaveCount(0)
  await expect(page.getByText("Reading a password is recorded in the audit log.")).toBeVisible()
})

test("a project that is not a stack has no Stack row and no stack page", async ({ page }) => {
  await mockProject(page)
  let asked = 0
  await page.route("**/api/v1/deploy/7/stack", (route) => {
    asked += 1
    return route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "stack_not_found", message: "not a stack" } }),
    })
  })
  await page.goto("/deploy/7")
  await expect(railRow(page, "/runtime")).toBeVisible()
  await expect(railRow(page, "/stack")).toHaveCount(0)

  await page.goto("/deploy/7/stack")
  await expect(page.getByText("This project is not a stack", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Open the Overview" })).toHaveAttribute(
    "href",
    "/deploy/7",
  )
  await expect(lead(page)).toHaveCount(0)
  // An ordinary project is not even asked about.
  expect(asked).toBe(0)
})

test("a stack's template whose project the server does not call a stack gets no row either", async ({
  page,
}) => {
  await mockStackProject(page, { state: null })
  await page.goto("/deploy/7")
  await expect(railRow(page, "/runtime")).toBeVisible()
  await expect(railRow(page, "/stack")).toHaveCount(0)
  await page.goto("/deploy/7/stack")
  await expect(page.getByText("This project is not a stack", { exact: true })).toBeVisible()
})

test("a later read that fails keeps the last reading on screen", async ({ page }) => {
  await mockStackProject(page, { state: stackState("configuring") })
  await page.goto("/deploy/7/stack")
  await expect(lead(page).getByText("Setting up", { exact: true }).first()).toBeVisible()
  await page.route("**/api/v1/deploy/7/stack", (route) =>
    route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "store is locked" } }),
    }),
  )
  await expect(lead(page).getByText(/Not updating/i)).toBeVisible({ timeout: 15_000 })
  await expect(lead(page).locator("[data-slot='stack-summary']")).toContainText(
    "Setting up the applications",
  )
  await expect(services(page).locator("[data-slot='choice-row']")).toHaveCount(8)
})

test("the stack page is one column on a phone, with every row one height", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockStackProject(page, { state: stackState("action_required") })
  await page.goto("/deploy/7/stack")
  await expect(lead(page).getByText("Needs you", { exact: true }).first()).toBeVisible()
  const sideways = await page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways).toBe(false)
  const rows = services(page).locator("[data-slot='choice-row']")
  await expect(rows).toHaveCount(8)
  const heights = await rows.evaluateAll((items) =>
    items.map((item) => Math.round(item.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)
  // The state stays on the row; the address gives way to it.
  await expect(rows.nth(1)).toContainText("Healthy")
  await expect(rows.nth(1).getByText("127.0.0.1:28055")).toBeHidden()
  // The tunnel is still a press away.
  await expect(rows.nth(1).getByRole("button", { name: "How to reach Seerr" })).toBeVisible()
  const steps = page.locator("li[data-status]")
  const stepHeights = await steps.evaluateAll((items) =>
    items.map((item) => Math.round(item.getBoundingClientRect().height)),
  )
  expect(new Set(stepHeights).size).toBe(1)
  await page.screenshot({ path: "test-results/deploy-stack-action-390.png", fullPage: true })
})
