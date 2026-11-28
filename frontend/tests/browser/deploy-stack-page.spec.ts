import { expect, test, type Page } from "@playwright/test"
import { json, mockProject, run } from "./deploy-fixture"
import {
  FLARESOLVERR_UPGRADE,
  STACK_PASSWORD,
  mediaCompleteDefinition,
  mockStackProject,
  stackCatalogue,
  stackState,
} from "./deploy-stack-fixture"
import { risen } from "./risen"

/**
 * A stack project's page, `/deploy/[id]/stack`, as a launcher: the server's
 * sentence and state, the five facts each on its own, the one next step
 * when something is left, every app with what it is for, its address and
 * Open, the one login beside them, and the setup step by step.
 */

const lead = (page: Page) => page.locator("[data-slot='stack-lead']")
const facts = (page: Page) => page.getByRole("list", { name: "What the stack can do" })
const services = (page: Page) => page.getByRole("list", { name: "Apps", exact: true })
const login = (page: Page) => page.locator("[data-slot='stack-login']")
const left = (page: Page) => page.getByRole("region", { name: "What is left to do" })
/** A row of the project's rail, by where it goes: Runtime is also a settings page's name. */
const railRow = (page: Page, path: string) =>
  page.locator("[data-slot='sidebar']").first().locator(`a[href="/deploy/7${path}"]`)

test("a ready stack reads as a launcher: every app, what it is for, its address and Open", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("ready") })
  await page.goto("/deploy/7/stack")

  // A reading page: the project's own header, then what is true of the stack.
  await expect(page.locator("[data-slot='page']")).toHaveAttribute("data-register", "reading")
  await expect(page.getByRole("heading", { name: "Media server", exact: true })).toBeVisible()
  await expect(lead(page)).toContainText("Complete · v1.1.0")
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
  // Nothing is left to do, so nothing is drawn for it; and no figure is a tile.
  await expect(left(page)).toHaveCount(0)
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)

  const rows = services(page).locator("[data-slot='choice-row']")
  await expect(rows).toHaveCount(8)
  // Each app says what it is for, where it answers, its state and Open.
  const jellyfin = rows.filter({ hasText: "Jellyfin" })
  await expect(jellyfin).toContainText("Plays your films and series")
  await expect(jellyfin).toContainText("media.example.test")
  await expect(jellyfin).toContainText("Healthy")
  const open = jellyfin.getByRole("link", { name: "Open Jellyfin at media.example.test" })
  await expect(open).toHaveAttribute("href", "https://media.example.test")
  await expect(open).toHaveAttribute("target", "_blank")
  await expect(open).toHaveText("Open")
  const seerr = rows.filter({ hasText: "Seerr" })
  await expect(seerr).toContainText("media-requests.example.test")
  await expect(
    seerr.getByRole("link", { name: "Open Seerr at media-requests.example.test" }),
  ).toHaveAttribute("href", "https://media-requests.example.test")
  await expect(
    rows
      .filter({ hasText: "qBittorrent" })
      .getByRole("link", { name: "Open qBittorrent at media-downloads.example.test" }),
  ).toBeVisible()
  // A worker has no interface: it says so and offers nothing to open.
  const recyclarr = rows.filter({ hasText: "Recyclarr" })
  await expect(recyclarr).toContainText("In the background")
  await expect(recyclarr.getByRole("link")).toHaveCount(0)
  await expect(recyclarr).toContainText("Running")
  // Nothing on the page sends anyone to a tunnel or a loopback address.
  await expect(page.locator("[data-slot='page']")).not.toContainText(/\bssh\b|tunnel|127\.0\.0\.1/i)

  // The container is behind the row's menu, in words.
  await jellyfin.getByRole("button", { name: "More for Jellyfin" }).click()
  await expect(page.getByRole("menu").locator("svg")).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Open its container" })).toHaveAttribute(
    "href",
    `/docker/containers/${stack.containerId("jellyfin")}`,
  )
  await page.keyboard.press("Escape")
  await expect(page.getByRole("menu")).toHaveCount(0)

  // Every row is one height, whatever it carries.
  const heights = await rows.evaluateAll((items) =>
    items.map((item) => Math.round(item.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)

  // The one login, beside the apps: its user name, and a password nobody has read.
  await expect(login(page)).toContainText("One login for every app")
  await expect(login(page)).toContainText("admin")
  await expect(login(page).getByLabel("Password hidden")).toBeVisible()
  await expect(login(page)).toContainText(
    "Jellyfin, Seerr, Radarr, Sonarr, Prowlarr, qBittorrent and Bazarr all sign in with it.",
  )
  // At a laptop's width it is one panel over the apps, so their rows keep room for
  // what each is for; from 2xl it stands beside them.
  let apps = await services(page).boundingBox()
  let beside = await login(page).boundingBox()
  expect(beside!.y + beside!.height).toBeLessThanOrEqual(apps!.y)
  await page.setViewportSize({ width: 1600, height: 900 })
  await expect
    .poll(async () => (await login(page).boundingBox())!.x)
    .toBeGreaterThan((await services(page).boundingBox())!.x + 100)
  apps = await services(page).boundingBox()
  beside = await login(page).boundingBox()
  expect(beside!.x).toBeGreaterThan(apps!.x + apps!.width - 1)
  await page.setViewportSize({ width: 1280, height: 720 })

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
  // A checklist: a done step is its tick and its name, in two columns, and
  // what it did is behind the line rather than a second sentence on it.
  await expect(setup.getByText("Done", { exact: true })).toHaveCount(0)
  await expect(setup.getByText("Checked and unchanged")).toHaveCount(0)
  const lines = setup.locator("li[data-status='done']")
  const columns = await lines.evaluateAll((items) =>
    items.map((item) => Math.round(item.getBoundingClientRect().x)),
  )
  expect(new Set(columns).size).toBe(2)
  await lines
    .first()
    .getByRole("button", { name: /Make the download and library folders/ })
    .click()
  const detail = page.getByRole("dialog")
  await expect(detail).toContainText("Make the download and library folders")
  await expect(detail).toContainText("Done")
  await expect(detail).toContainText("Checked and unchanged")
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)

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
  const waiting = left(page)
  await expect(waiting).toContainText("The deployment's run is still bringing the release up")
  await expect(waiting.getByRole("link", { name: "Watch the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/84",
  )
  // Nothing to configure yet, so no button offers it; the login is not made yet.
  await expect(lead(page).getByRole("button", { name: /Repair|Retry/ })).toHaveCount(0)
  await expect(login(page)).toContainText("Made by the stack's first setup")
  await expect(login(page).getByRole("button", { name: "Show the password" })).toHaveCount(0)
  await expect(login(page).getByRole("button", { name: "Change password" })).toHaveCount(0)
  // An app with no container and no address is a row that opens nothing.
  const rows = services(page).locator("[data-slot='choice-row']")
  await expect(rows).toHaveCount(8)
  await expect(rows.first()).toContainText("No container")
  await expect(rows.first()).toContainText("No address yet")
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
  // A step still to run is its ring and a fainter name, not fifteen "Waiting"s.
  const pending = page.locator("li[data-status='pending']")
  await expect(pending).toHaveCount(14)
  await expect(pending.getByText("Waiting", { exact: true })).toHaveCount(0)
  // Not reached yet, a step has no record to open: its line is no press onto
  // its title and "Waiting" again, and a reader still hears its state.
  await expect(pending.getByRole("button")).toHaveCount(0)
  await expect(pending.first()).toContainText(", Waiting")
  // A line with a record and one without stand one height, and opening a
  // record leaves the line under it where it was.
  const heights = await page
    .locator("li[data-status]")
    .evaluateAll((items) => items.map((item) => Math.round(item.getBoundingClientRect().height)))
  expect(new Set(heights).size).toBe(1)
  const done = page.locator("li[data-status='done']")
  // Where the line stands on the page, not in the view the press scrolls.
  const place = () =>
    done.nth(1).evaluate((line) => {
      const box = line.getBoundingClientRect()
      let y = box.y + window.scrollY
      for (let parent = line.parentElement; parent; parent = parent.parentElement)
        y += parent.scrollTop
      return { x: box.x, y, height: box.height }
    })
  await risen(page)
  const under = await place()
  await done.first().getByRole("button").click()
  await expect(page.getByRole("dialog")).toBeVisible()
  expect(await place()).toEqual(under)
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  // A pass is running, so there is nothing to press.
  await expect(lead(page).getByRole("button", { name: /Repair|Retry/ })).toHaveCount(0)
  // The login exists from the first pass on.
  await expect(login(page).getByRole("button", { name: "Show the password" })).toBeVisible()

  stack.set(stackState("ready"))
  await expect(lead(page).getByText("Ready", { exact: true }).first()).toBeVisible({
    timeout: 12_000,
  })
  await expect(page.getByText("22 of 22 done", { exact: true })).toBeVisible()
})

test("the one next step is said in words and leads to the source picker, the blocking one first", async ({
  page,
}) => {
  await mockStackProject(page, { state: stackState("action_required") })
  await page.goto("/deploy/7/stack")
  await expect(lead(page).getByText("Needs you", { exact: true }).first()).toBeVisible()
  await expect(lead(page).locator("[data-slot='stack-summary']")).toContainText(
    "nothing can be downloaded until you add a media source",
  )
  await expect(facts(page).getByRole("listitem").nth(3)).toHaveText("Cannot fetch new titles")
  const actions = page.locator("[data-slot='stack-action']")
  await expect(actions).toHaveCount(2)
  await expect(actions.first()).toHaveAttribute("data-blocking", "true")
  await expect(actions.first()).toContainText("Add a media source")
  await expect(actions.first()).toContainText("Choose one you are allowed to use")
  await expect(actions.first()).toContainText(
    "Prowlarr · the stack is not ready until this is done",
  )
  // The page's one next step: a link to the picker, in the brand's face.
  const next = actions.first().getByRole("link", { name: "Add a media source" })
  await expect(next).toHaveAttribute("href", "/deploy/7/stack/sources")
  await expect(next).toHaveAttribute("data-variant", "default")
  await expect(actions.nth(1)).toHaveAttribute("data-blocking", "false")
  await expect(actions.nth(1)).toContainText("Let people in to Seerr")
  await expect(actions.nth(1)).toContainText("optional")
  await expect(actions.nth(1).getByRole("link")).toHaveCount(0)
  // Nobody is told to open Prowlarr itself.
  await expect(left(page)).not.toContainText(/open Prowlarr|indexer/i)
  await expect(
    services(page).locator("[data-slot='choice-row']").filter({ hasText: "Prowlarr" }),
  ).toContainText("Needs you")
  await expect(lead(page).getByRole("button", { name: "Repair", exact: true })).toBeVisible()

  await next.click()
  await expect(page).toHaveURL(/\/deploy\/7\/stack\/sources$/)
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
  await expect(lead(page)).toContainText("Essential · v1.1.0")
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

test("the login's password is shown where its mask was, copied without being drawn, and audited", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  const stack = await mockStackProject(page, { state: stackState("ready") })
  await page.goto("/deploy/7/stack")
  const panel = login(page)
  await expect(panel.getByLabel("Password hidden")).toBeVisible()
  await expect(page.getByText(STACK_PASSWORD)).toHaveCount(0)
  expect(stack.reveals()).toEqual([])
  const line = panel.locator("dl > div").nth(1)
  const height = (await line.boundingBox())!.height

  await panel.getByRole("button", { name: "Show the password" }).click()
  await expect(panel.getByText(STACK_PASSWORD)).toBeVisible()
  expect(stack.reveals()).toEqual(["login"])
  // Showing moves nothing: the line is the height it was.
  expect((await line.boundingBox())!.height).toBe(height)
  await panel.getByRole("button", { name: "Hide the password" }).click()
  await expect(page.getByText(STACK_PASSWORD)).toHaveCount(0)

  // Copy reads it again and puts it on the clipboard, never on the screen.
  await panel.getByRole("button", { name: "Copy the password" }).click()
  await expect.poll(() => stack.reveals()).toEqual(["login", "login"])
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(STACK_PASSWORD)
  await expect(page.getByText(STACK_PASSWORD)).toHaveCount(0)
  await expect(panel).toContainText("Showing or copying it is recorded in the audit log.")
})

test("Change password asks for one by the dashboard's rule and moves every app to it", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("ready") })
  await page.goto("/deploy/7/stack")
  await login(page).getByRole("button", { name: "Show the password" }).click()
  await expect(login(page).getByText(STACK_PASSWORD)).toBeVisible()
  await login(page).getByRole("button", { name: "Change password" }).click()
  const dialog = page.getByRole("dialog", { name: "Change the password for every app" })
  const field = dialog.getByLabel("New password", { exact: true })
  await expect(field).toHaveAttribute("type", "password")
  const change = dialog.getByRole("button", { name: "Change password", exact: true })
  await expect(change).toBeDisabled()
  await field.fill("short")
  await expect(change).toBeDisabled()
  await field.fill("Every-app-new-password-1")
  await expect(change).toBeEnabled()
  await change.click()
  await expect(dialog).toBeHidden()
  expect(stack.logins()).toEqual([{ password: "Every-app-new-password-1" }])
  // 202: the pass it started is followed, and the old password is no longer drawn.
  await expect(lead(page).getByText("Setting up", { exact: true }).first()).toBeVisible()
  await expect(page.getByText(STACK_PASSWORD)).toHaveCount(0)
  await expect(page.getByText("Every-app-new-password-1")).toHaveCount(0)
})

test("a password the server calls weak is answered in the dialog, in its words", async ({
  page,
}) => {
  const stack = await mockStackProject(page, {
    state: stackState("ready"),
    login: "password_weak",
  })
  await page.goto("/deploy/7/stack")
  await login(page).getByRole("button", { name: "Change password" }).click()
  const dialog = page.getByRole("dialog", { name: "Change the password for every app" })
  await dialog.getByLabel("New password", { exact: true }).fill("Every-app-new-password-1")
  await dialog.getByRole("button", { name: "Change password", exact: true }).click()
  await expect.poll(() => stack.logins().length).toBe(1)
  await expect(dialog).toContainText("The password needs at least 12 characters")
  await expect(dialog).toBeVisible()
  await expect(page.getByText("password_weak")).toHaveCount(0)
  // Cancel puts it away with nothing kept.
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toBeHidden()
  await login(page).getByRole("button", { name: "Change password" }).click()
  await expect(dialog.getByLabel("New password", { exact: true })).toHaveValue("")
})

test("a stack with apps that have no address offers to give them theirs, then links the run", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("ready", { addressless: true }) })
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-addresses']")
  await expect(step).toContainText("Give every app its address")
  await expect(step).toContainText(
    "Seerr, Radarr, Sonarr, Prowlarr, qBittorrent and Bazarr have no address yet. Each gets one beside media.example.test, and a deployment starts to serve them.",
  )
  // Until then those apps say so and offer nothing to open.
  const rows = services(page).locator("[data-slot='choice-row']")
  const seerr = rows.filter({ hasText: "Seerr" })
  await expect(seerr).toContainText("No address yet")
  await expect(seerr.getByRole("link", { name: /^Open Seerr/ })).toHaveCount(0)
  await expect(
    rows.filter({ hasText: "Jellyfin" }).getByRole("link", { name: /^Open/ }),
  ).toBeVisible()

  // The page's one next step, so its command wears the brand.
  const give = step.getByRole("button", { name: "Give every app its address" })
  await expect(give).toHaveAttribute("data-variant", "default")
  await give.click()
  const dialog = page.getByRole("dialog", { name: "Give every app its address" })
  const names = dialog.getByRole("list", { name: "The addresses the apps get" })
  await expect(names.getByRole("listitem")).toHaveCount(6)
  await expect(names.getByRole("listitem").first()).toHaveText(
    /Seerr\s*media-requests\.example\.test/,
  )
  await expect(names).toContainText("media-subtitles.example.test")
  await expect(dialog.getByRole("textbox")).toHaveCount(0)
  await dialog.getByRole("button", { name: "Add the addresses and deploy" }).click()
  await expect(dialog).toBeHidden()
  expect(stack.addressRequests()).toEqual([{}])
  await expect(step).toContainText("A deployment is giving every app its address")
  await expect(step).toContainText("media-requests.example.test")
  await expect(step.getByRole("link", { name: "Watch the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/91",
  )
  // The next read has every app at its address.
  await expect(
    seerr.getByRole("link", { name: "Open Seerr at media-requests.example.test" }),
  ).toBeVisible({ timeout: 20_000 })
})

test("a stack with no domain is given one in the dialog, and the names follow what is typed", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: stackState("ready", { noDomain: true }) })
  await page.route("**/api/v1/deploy/stacks", (route) => json(route, stackCatalogue))
  await page.route("**/api/v1/deploy/hostname**", (route) =>
    json(route, {
      hostname: "media-server.203-0-113-7.sslip.io",
      base: "203-0-113-7.sslip.io",
      covered: false,
      certificateMethod: "caddy",
      method: "sslip",
      address: "203.0.113.7",
      detail: "Resolves to 203.0.113.7 with no DNS record to create.",
    }),
  )
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-addresses']")
  await expect(step).toContainText("and neither has the stack: give it a domain")
  await step.getByRole("button", { name: "Give every app its address" }).click()
  const dialog = page.getByRole("dialog", { name: "Give every app its address" })
  const domain = dialog.getByRole("textbox", { name: "The stack's domain" })
  await expect(domain).toHaveValue("media-server.203-0-113-7.sslip.io")
  const names = dialog.getByRole("list", { name: "The addresses the apps get" })
  await expect(names).toContainText("media-server-requests.203-0-113-7.sslip.io")
  await domain.fill("tv.example.org")
  await expect(names).toContainText("tv-requests.example.org")
  await expect(names).toContainText("tv-downloads.example.org")
  await domain.fill("not a name")
  await expect(dialog.getByRole("button", { name: "Add the addresses and deploy" })).toBeDisabled()
  await domain.fill("https://tv.example.org/")
  await dialog.getByRole("button", { name: "Add the addresses and deploy" }).click()
  await expect(dialog).toBeHidden()
  expect(stack.addressRequests()).toEqual([{ domain: "tv.example.org" }])
  await expect(step.getByRole("link", { name: "Watch the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/91",
  )
})

test("an address request refused for want of a domain is answered in the dialog", async ({
  page,
}) => {
  await mockStackProject(page, {
    state: stackState("ready", { addressless: true }),
    addresses: "stack_no_domain",
  })
  await page.goto("/deploy/7/stack")
  await page
    .locator("[data-slot='stack-addresses']")
    .getByRole("button", { name: "Give every app its address" })
    .click()
  const dialog = page.getByRole("dialog", { name: "Give every app its address" })
  await dialog.getByRole("button", { name: "Add the addresses and deploy" }).click()
  await expect(dialog).toContainText("The stack has no domain yet")
  await expect(page.getByText("stack_no_domain")).toHaveCount(0)
})

/** A stack whose apps' addresses are saved but not served: the release that was to publish them failed. */
function unpublishedState() {
  const addressless = stackState("ready", { addressless: true })
  return {
    ...addressless,
    addresses: {
      primary: "media.example.test",
      missing: [],
      unpublished: addressless.addresses!.missing,
    },
  }
}

test("addresses saved but not live are offered Deploy again, which links its run", async ({
  page,
}) => {
  const stack = await mockStackProject(page, { state: unpublishedState() })
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-addresses']")
  await expect(step).toContainText("Publish the apps' addresses")
  await expect(step).toContainText(
    "have their addresses saved, but the release that was to publish them did not finish. Deploying again publishes them.",
  )
  const again = step.getByRole("button", { name: "Deploy again" })
  await expect(again).toHaveAttribute("data-variant", "default")
  await again.click()
  const dialog = page.getByRole("dialog", { name: "Publish the apps' addresses" })
  const names = dialog.getByRole("list", { name: "The addresses the apps get" })
  await expect(names.getByRole("listitem")).toHaveCount(6)
  await expect(names).toContainText("media-requests.example.test")
  await expect(dialog.getByRole("textbox")).toHaveCount(0)
  await dialog.getByRole("button", { name: "Deploy again" }).click()
  await expect(dialog).toBeHidden()
  expect(stack.addressRequests()).toEqual([{}])
  await expect(step).toContainText("A deployment is publishing the saved addresses.")
  await expect(step.getByRole("link", { name: "Watch the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/91",
  )
  await expect(step.getByRole("button")).toHaveCount(0)
})

test("while a deployment is under way, saved addresses wait for it rather than offering another", async ({
  page,
}) => {
  // Back on the page from "Watch the run": the run that publishes them is still going.
  const stack = await mockStackProject(page, { state: unpublishedState(), running: true })
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-addresses']")
  await expect(step).toContainText("A deployment is publishing the saved addresses.")
  await expect(step).not.toContainText("did not finish")
  await expect(step.getByRole("button", { name: "Deploy again" })).toHaveCount(0)
  await expect(step.getByRole("link", { name: "Watch the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/84",
  )
  expect(stack.addressRequests()).toEqual([])
})

test("an address request the server finds nothing missing for closes and reads the stack again", async ({
  page,
}) => {
  const stack = await mockStackProject(page, {
    state: stackState("ready", { addressless: true }),
    addresses: "stack_addresses_present",
  })
  await page.goto("/deploy/7/stack")
  await page
    .locator("[data-slot='stack-addresses']")
    .getByRole("button", { name: "Give every app its address" })
    .click()
  const dialog = page.getByRole("dialog", { name: "Give every app its address" })
  const before = stack.reads()
  await dialog.getByRole("button", { name: "Add the addresses and deploy" }).click()
  await expect(dialog).toBeHidden()
  await expect(page.locator("[data-sonner-toast]")).toContainText(
    "Every app already has its address.",
  )
  await expect.poll(() => stack.reads()).toBeGreaterThan(before)
  await expect(page.getByText("stack_addresses_present")).toHaveCount(0)
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
  // Nor a way to it from the Overview: there is no login on a page that is
  // not there.
  const signIn = page
    .locator("[data-slot='panel']")
    .filter({ has: page.getByRole("heading", { name: "First sign-in", exact: true }) })
  await expect(signIn.getByRole("link", { name: /Open it/ })).toBeVisible()
  await expect(signIn.getByRole("link", { name: "See the login" })).toHaveCount(0)
  await page.goto("/deploy/7/stack")
  await expect(page.getByText("This project is not a stack", { exact: true })).toBeVisible()
})

// The stack set every app up before its address answered anyone, so its
// Overview never tells the person to hurry and claim the account: that warning
// stood under a stack someone had long since signed in to, and never went away.
test("a stack's Overview links the one login and never asks anyone to claim an account", async ({
  page,
}) => {
  await mockStackProject(page)
  await page.goto("/deploy/7")
  const signIn = page
    .locator("[data-slot='panel']")
    .filter({ has: page.getByRole("heading", { name: "First sign-in", exact: true }) })
  await expect(signIn).toContainText("The dashboard finishes every application's first-run setup")
  await expect(signIn.getByRole("link", { name: "See the login" })).toHaveAttribute(
    "href",
    "/deploy/7/stack",
  )
  await expect(signIn.getByRole("link", { name: /Open it/ })).toHaveAttribute(
    "href",
    /^https:\/\/media\.example\.test\/?$/,
  )
  await expect(signIn).not.toContainText("Do this now")
  await expect(signIn).not.toContainText("Nobody owns this yet")

  // The warning is the kind's, not the page's: a template whose first visitor
  // does make the account still says so.
  await page.route("**/api/v1/deploy/blueprints/media-*", (route) =>
    json(route, {
      ...mediaCompleteDefinition,
      access: { kind: "setup", note: "The first visitor creates the account." },
    }),
  )
  await page.reload()
  await expect(signIn).toContainText("Nobody owns this yet")
  await expect(signIn.getByRole("link", { name: "See the login" })).toHaveCount(0)
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
  // The state and Open stay on the row; the address gives way to them.
  await expect(rows.nth(1)).toContainText("Healthy")
  await expect(rows.nth(1).getByText("media-requests.example.test")).toBeHidden()
  const open = rows.nth(1).getByRole("link", { name: "Open Seerr at media-requests.example.test" })
  await expect(open).toBeVisible()
  await expect(open.getByText("Open", { exact: true })).toBeHidden()
  // The login stands over the apps, the whole width, its two lines one under the other.
  const apps = await services(page).boundingBox()
  const over = await login(page).boundingBox()
  expect(over!.y + over!.height).toBeLessThanOrEqual(apps!.y)
  const lines = await login(page)
    .locator("dl > div")
    .evaluateAll((items) => items.map((item) => Math.round(item.getBoundingClientRect().x)))
  expect(new Set(lines).size).toBe(1)
  const steps = page.locator("li[data-status]")
  const stepHeights = await steps.evaluateAll((items) =>
    items.map((item) => Math.round(item.getBoundingClientRect().height)),
  )
  expect(new Set(stepHeights).size).toBe(1)
  await page.screenshot({ path: "test-results/deploy-stack-action-390.png", fullPage: true })
})

test("a stack's Overview leads with its address and folds its apps' names behind a press", async ({
  page,
}) => {
  await mockStackProject(page, { state: stackState("ready") })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/.*\/stats\/stream/, () => {})
  const names = [
    "media.example.test",
    "media-downloads.example.test",
    "media-prowlarr.example.test",
    "media-radarr.example.test",
    "media-requests.example.test",
    "media-sonarr.example.test",
    "media-subtitles.example.test",
  ]
  // Listed in the order the server sorts them, the deployment's own address
  // among the others rather than first.
  const sorted = [...names.slice(1, 4), names[0], ...names.slice(4)]
  await page.route("**/api/v1/deploy/7/operations", (route) =>
    json(route, {
      observedAt: new Date().toISOString(),
      releaseId: 20,
      evidence: "release",
      runtime: { status: "available", observedAt: new Date().toISOString(), services: [] },
      domains: {
        status: "available",
        siteName: "just-dashboard-env-12.conf",
        domains: sorted.map((hostname) => ({
          hostname,
          https: true,
          ownership: "managed",
          route: "served",
          certificate: "valid",
          certificateName: hostname,
          certificateDaysLeft: 85,
          certificateIssuer: "R12",
        })),
      },
      storage: { status: "available", mounts: [] },
      backups: { status: "available", jobs: [] },
      dependencies: { status: "available", items: [] },
    }),
  )
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/deploy/7")
  const wiring = page.getByRole("list", { name: "How the project is wired" })
  const domains = wiring.getByRole("listitem").filter({ hasText: "Domains" })
  // The address the deployment is reached at, then every certificate once.
  await expect(domains.getByRole("link")).toHaveCount(1)
  await expect(domains.getByRole("link")).toHaveAttribute("href", "https://media.example.test/")
  await expect(domains.locator("[data-slot='certificates-reading']")).toHaveText(
    /^7 certificates valid\s*85 days left$/,
  )
  // Eight containers are their apps' names, three of them, and the rest one
  // way to the Runtime page.
  const runtime = wiring.getByRole("listitem").filter({ hasText: "Runtime" })
  // A name and one line under it, as tall as Runtime's node rather than seven
  // rows: within the pixel a mono name's line stands over a sans one.
  const tall = (await domains.boundingBox())!.height
  expect(tall).toBeLessThanOrEqual((await runtime.boundingBox())!.height + 3)

  await expect(runtime).toContainText("8 of 8 containers running")
  await expect(runtime.getByText("jellyfin", { exact: true })).toBeVisible()
  await expect(runtime.getByText("media-server-jellyfin-1")).toHaveCount(0)
  await expect(runtime.getByRole("link", { name: "5 more" })).toHaveAttribute(
    "href",
    "/deploy/7/runtime",
  )

  // Every name is behind the press, each with its own certificate.
  const more = domains.getByRole("button", { name: "6 more domains" })
  await more.click()
  const list = page.getByRole("dialog").getByRole("list", { name: "Domains" })
  await expect(list.getByRole("listitem")).toHaveCount(7)
  await expect(list.getByRole("link").first()).toHaveText("media.example.test")
  await expect(list.getByText("Certificate valid", { exact: true })).toHaveCount(7)
  await page.screenshot({ path: "test-results/deploy-stack-overview-1440.png" })
  await page.keyboard.press("Escape")

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(domains.locator("[data-slot='certificates-reading']")).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await more.click()
  await expect(page.getByRole("dialog").getByRole("listitem")).toHaveCount(7)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await page.screenshot({ path: "test-results/deploy-stack-overview-390.png" })
})

test("a step on another try says in its record what the last one met", async ({ page }) => {
  const configuring = stackState("configuring")
  await mockStackProject(page, {
    state: {
      ...configuring,
      // The server keeps the last attempt's error on a step it tries again.
      steps: configuring.steps.map((step) =>
        step.status === "running"
          ? { ...step, error: "Sonarr answered 503 to GET /api/v3/system/status" }
          : step,
      ),
    },
  })
  await page.goto("/deploy/7/stack")
  const running = page.locator("li[data-status='running']")
  await expect(running).toContainText("Attempt 2")
  await running.getByRole("button").click()
  await expect(page.getByRole("dialog")).toContainText(
    "Sonarr answered 503 to GET /api/v3/system/status",
  )
})

test("a stack made before an app came with it offers to add the app, then links the run", async ({
  page,
}) => {
  const stack = await mockStackProject(page, {
    state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE },
  })
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-upgrade']")
  await expect(step).toContainText("New in the stack: FlareSolverr")
  await expect(step).toContainText(
    "The stack now comes with FlareSolverr. Adding it deploys the stack again with it beside the other apps; your library, settings, domains and login stay",
  )
  // The page's one next step, so its command wears the brand.
  const add = step.getByRole("button", { name: "Add FlareSolverr" })
  await expect(add).toHaveAttribute("data-variant", "default")
  await add.click()
  const dialog = page.getByRole("dialog", { name: "Add FlareSolverr" })
  await expect(dialog.getByRole("list", { name: "The apps added" })).toContainText(
    "Opens the sources behind Cloudflare",
  )
  await expect(dialog).toContainText("version 1.2.0")
  await dialog.getByRole("button", { name: "Add and deploy" }).click()
  await expect(dialog).toBeHidden()
  expect(stack.upgradeRequests()).toEqual([{}])
  await expect(step).toContainText("A deployment is adding FlareSolverr")
  await expect(step.getByRole("link", { name: "Watch the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/92",
  )
  await expect(step.getByRole("button")).toHaveCount(0)
})

test("an older stack's version is the one it runs, not the one it is offered", async ({ page }) => {
  await mockStackProject(page, {
    state: { ...stackState("ready"), version: "1.2.0", upgrade: FLARESOLVERR_UPGRADE },
  })
  await page.goto("/deploy/7/stack")
  await expect(page.locator("[data-slot='stack-upgrade']")).toContainText(
    "New in the stack: FlareSolverr",
  )
  await expect(lead(page)).toContainText("Complete · v1.1.0")
})

test("new apps saved but not live wait for the deployment under way, else offer Deploy again", async ({
  page,
}) => {
  const saved = { ...stackState("ready"), upgrade: { ...FLARESOLVERR_UPGRADE, saved: true } }
  const stack = await mockStackProject(page, { state: saved, running: true })
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-upgrade']")
  await expect(step).toContainText("A deployment is adding FlareSolverr")
  await expect(step.getByRole("link", { name: "Watch the run" })).toHaveAttribute(
    "href",
    "/deploy/7/runs/84",
  )
  await expect(step.getByRole("button")).toHaveCount(0)
  expect(stack.upgradeRequests()).toEqual([])

  const idle = await mockStackProject(page, { state: saved })
  await page.goto("/deploy/7/stack")
  await expect(step).toContainText("Start FlareSolverr")
  await expect(step).toContainText("did not finish. Deploying again starts it.")
  await step.getByRole("button", { name: "Deploy again" }).click()
  await page
    .getByRole("dialog", { name: "Start FlareSolverr" })
    .getByRole("button", { name: "Deploy again" })
    .click()
  expect(idle.upgradeRequests()).toEqual([{}])
  await expect(step).toContainText("A deployment is adding FlareSolverr")
})

test("once the run adding the apps has failed, the step offers Deploy again", async ({ page }) => {
  const stack = await mockStackProject(page, {
    state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE },
  })
  // Run 92 is the one the press starts; the spec ends it.
  const started = { state: "queued" }
  await page.route(
    (url) => url.pathname === "/api/v1/deploy/7/runs" && url.searchParams.get("view") === "engine",
    (route) =>
      json(route, {
        runs: [{ ...run, id: 92, runNumber: 2, state: started.state }],
        running: started.state === "queued",
      }),
  )
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-upgrade']")
  await step.getByRole("button", { name: "Add FlareSolverr" }).click()
  await page
    .getByRole("dialog", { name: "Add FlareSolverr" })
    .getByRole("button", { name: "Add and deploy" })
    .click()
  await expect(step).toContainText("A deployment is adding FlareSolverr")
  // While it runs the source names 1.2.0 and the live release is older.
  stack.set({ ...stackState("ready"), upgrade: { ...FLARESOLVERR_UPGRADE, saved: true } })
  const reads = stack.reads()
  await expect.poll(() => stack.reads(), { timeout: 20000 }).toBeGreaterThan(reads)
  await expect(step).toContainText("A deployment is adding FlareSolverr")

  // The release failed: the stack is read again and offers Deploy again.
  started.state = "failed"
  await expect(step).toContainText("Start FlareSolverr", { timeout: 20000 })
  await expect(step.getByRole("button", { name: "Deploy again" })).toBeVisible()
  await expect(step).not.toContainText("A deployment is adding")
})

test("a run that brought the apps leaves no step behind", async ({ page }) => {
  const stack = await mockStackProject(page, {
    state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE },
  })
  const started = { state: "queued" }
  await page.route(
    (url) => url.pathname === "/api/v1/deploy/7/runs" && url.searchParams.get("view") === "engine",
    (route) =>
      json(route, {
        runs: [{ ...run, id: 92, runNumber: 2, state: started.state }],
        running: started.state === "queued",
      }),
  )
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-upgrade']")
  await step.getByRole("button", { name: "Add FlareSolverr" }).click()
  await page
    .getByRole("dialog", { name: "Add FlareSolverr" })
    .getByRole("button", { name: "Add and deploy" })
    .click()
  await expect(step).toContainText("A deployment is adding FlareSolverr")
  stack.set(stackState("ready"))
  started.state = "succeeded"
  await expect(step).toHaveCount(0, { timeout: 20000 })
  await expect(left(page)).toHaveCount(0)
})
