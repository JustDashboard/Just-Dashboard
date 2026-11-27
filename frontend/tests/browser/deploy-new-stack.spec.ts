import { expect, test, type Page } from "@playwright/test"
import { currentStep, gotoStep, json } from "./deploy-fixture"
import { mockNewStack } from "./deploy-stack-fixture"

/**
 * The Stacks tab on `/deploy/new`: the media server's presets, what each
 * runs and cannot do, the preset template's inputs, and the hand-over to the
 * product's own Configure, Review and Deploy as a Compose template draft.
 */

const settings = (page: Page) => page.getByRole("region", { name: "Stack settings" })

test("the Stacks tab says what each preset runs and what it cannot do before anything is pressed", async ({
  page,
}) => {
  await mockNewStack(page)
  await page.goto("/deploy/new?source=stack")

  // A fifth source, reached by its address, in the same group of toggles.
  const strip = page.getByRole("group", { name: "Project source" })
  await expect(strip.getByRole("button")).toHaveCount(5)
  await expect(strip.getByRole("button", { name: "Stacks", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.locator("[data-slot='page']")).toHaveAttribute("data-register", "flow")
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("What are you deploying?")
  await expect(page.getByRole("heading", { name: "Media server", exact: true })).toBeVisible()
  await expect(page.getByText(/No films, series or sources are included\./)).toBeVisible()
  await expect(page.getByText(/kept in the stack's own volume/)).toBeVisible()

  // The presets are one choice: Complete is recommended and chosen.
  const presets = page.getByRole("radiogroup", { name: "Preset" })
  await expect(presets.getByRole("radio")).toHaveCount(2)
  const complete = presets.getByRole("radio", { name: "Complete preset", exact: true })
  const essential = presets.getByRole("radio", { name: "Essential preset", exact: true })
  await expect(complete).toBeChecked()
  await expect(essential).not.toBeChecked()
  await expect(presets.getByText("Recommended", { exact: true })).toHaveCount(1)
  // Nothing that is not built is offered.
  await expect(page.getByText(/Ultimate/i)).toHaveCount(0)
  await expect(page.getByText(/existing deployment/i)).toHaveCount(0)

  const services = page.getByRole("list", { name: "Services Complete runs" })
  await expect(services.getByRole("listitem")).toHaveCount(8)
  await expect(services.getByRole("listitem").first()).toContainText("Jellyfin")
  await expect(services.getByRole("listitem").first()).toContainText("Plays your films and series")
  await expect(services.getByRole("listitem").nth(1)).toContainText("Seerr")
  // Every app has an address now, so nothing is called private; a worker says it has no interface.
  await expect(services).not.toContainText(/Public|Private/)
  await expect(services.getByRole("listitem").last()).toContainText("Recyclarr")
  await expect(services.getByRole("listitem").last()).toContainText("Background")
  await expect(
    page.getByText(
      /Every app answers at its own HTTPS address beside the domain, and they all sign in with one password\. Recyclarr runs in the background/,
    ),
  ).toBeVisible()
  await expect(page.getByText(/Memory limits add up to 7\.3 GB/)).toBeVisible()
  await expect(page.getByText(/about 12 GB of disk/)).toBeVisible()

  const limits = page.getByRole("list", { name: "What Complete does not do" })
  await expect(limits.getByRole("listitem")).toHaveCount(2)
  await expect(limits).toContainText("Nothing can be downloaded until you add a source")
  await expect(limits).toContainText("An existing library elsewhere on this server is not mounted")

  // The preset's own inputs, asked as the Template tab asks a template's.
  await expect(
    settings(page).getByRole("heading", { name: "Media server · Complete" }),
  ).toBeVisible()
  // The domain is the server's suggestion, required, and under it the address each app gets.
  const domain = settings(page).getByRole("textbox", { name: "Domain" })
  await expect(domain).toHaveValue("wesmokefish-a1b2c3.203-0-113-7.sslip.io")
  const addresses = settings(page).getByRole("list", { name: "The address each app gets" })
  await expect(addresses.getByRole("listitem")).toHaveText([
    /Jellyfin\s*wesmokefish-a1b2c3\.203-0-113-7\.sslip\.io/,
    /Seerr\s*wesmokefish-a1b2c3-requests\.203-0-113-7\.sslip\.io/,
    /Radarr\s*wesmokefish-a1b2c3-radarr\.203-0-113-7\.sslip\.io/,
    /Sonarr\s*wesmokefish-a1b2c3-sonarr\.203-0-113-7\.sslip\.io/,
    /Prowlarr\s*wesmokefish-a1b2c3-prowlarr\.203-0-113-7\.sslip\.io/,
    /qBittorrent\s*wesmokefish-a1b2c3-downloads\.203-0-113-7\.sslip\.io/,
    /Bazarr\s*wesmokefish-a1b2c3-subtitles\.203-0-113-7\.sslip\.io/,
  ])
  // The password for every app is optional and typed hidden.
  const password = settings(page).getByLabel("Password for every app", { exact: true })
  await expect(password).toHaveValue("")
  await expect(password).toHaveAttribute("type", "password")
  await expect(settings(page)).toContainText("Leave it empty and the dashboard makes one")
  await expect(settings(page).getByRole("combobox", { name: "Quality" })).toHaveText("1080p")
  await expect(settings(page).getByRole("textbox", { name: "Subtitle languages" })).toHaveValue(
    "en",
  )
  await expect(settings(page).getByRole("combobox", { name: "Requests" })).toHaveText(
    "You approve each request",
  )
  await expect(settings(page).getByText("You create the first account")).toBeVisible()

  // The pins are there for whoever checks them, folded.
  const pins = page.getByRole("button", { name: "Pinned images" })
  await expect(pins).toHaveAttribute("aria-expanded", "false")
  await pins.click()
  await expect(page.getByRole("list", { name: "Pinned images" })).toContainText(
    "jellyfin/jellyfin:12.4",
  )

  // Essential: two services, its own limitation, and only the inputs it has.
  await essential.click()
  await expect(essential).toBeChecked()
  const fewer = page.getByRole("list", { name: "Services Essential runs" })
  await expect(fewer.getByRole("listitem")).toHaveCount(2)
  await expect(page.getByRole("list", { name: "What Essential does not do" })).toContainText(
    "A request cannot be fulfilled automatically",
  )
  await expect(page.getByText(/Memory limits add up to 3\.5 GB/)).toBeVisible()
  await expect(
    settings(page).getByRole("heading", { name: "Media server · Essential" }),
  ).toBeVisible()
  await expect(settings(page).getByRole("textbox", { name: "Domain" })).toBeVisible()
  await expect(
    settings(page).getByRole("list", { name: "The address each app gets" }).getByRole("listitem"),
  ).toHaveCount(2)
  await expect(settings(page).getByRole("combobox", { name: "Requests" })).toBeVisible()
  await expect(settings(page).getByRole("combobox", { name: "Quality" })).toHaveCount(0)
  await expect(settings(page).getByRole("textbox", { name: "Subtitle languages" })).toHaveCount(0)
})

test("Continue hands a preset to Configure as a Compose template draft, keeping edits per preset", async ({
  page,
}) => {
  const journey = await mockNewStack(page)
  await page.goto("/deploy/new?source=stack")
  const presets = page.getByRole("radiogroup", { name: "Preset" })
  const languages = settings(page).getByRole("textbox", { name: "Subtitle languages" })
  await languages.fill("english")

  // The format the definition would refuse is refused before a draft exists.
  await settings(page).getByRole("button", { name: "Continue", exact: true }).click()
  await expect(
    settings(page).getByText("Subtitle languages: This is not in the expected format."),
  ).toBeVisible()
  expect(journey.started()).toBe(0)

  // What was typed for one preset is still there after a look at the other.
  await languages.fill("en,fr")
  await presets.getByRole("radio", { name: "Essential preset", exact: true }).click()
  await expect(languages).toHaveCount(0)
  await presets.getByRole("radio", { name: "Complete preset", exact: true }).click()
  await expect(languages).toHaveValue("en,fr")

  await settings(page).getByRole("combobox", { name: "Quality" }).click()
  await page.getByRole("option", { name: "4K" }).click()
  await settings(page).getByRole("button", { name: "Continue", exact: true }).click()

  await expect.poll(() => journey.sources().length).toBeGreaterThan(0)
  expect(journey.sources()[0]).toMatchObject({
    kind: "compose",
    mode: "blueprint",
    blueprintId: "media-complete",
    blueprintVersion: "1.1.0",
    blueprintInputs: {
      domain: "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
      quality: "2160p",
      "subtitle-languages": "en,fr",
    },
  })
  // The browser never sends a document: the server renders the reviewed one.
  expect(journey.sources()[0].composeFiles).toBeUndefined()
  expect(journey.intent()).toMatchObject({ name: "Media-server", profile: "compose" })

  // The flow is the product's own from here: Configure's screens, its spine.
  await currentStep(page)
  await expect(page.getByRole("group", { name: "Project source" })).toHaveCount(0)
  await gotoStep(page, "project")
  await expect(page.getByRole("textbox", { name: "Project name" })).toHaveValue("Media-server")
})

test("the address preview follows the domain, and the password for every app goes to Variables, never to the draft", async ({
  page,
}) => {
  const journey = await mockNewStack(page)
  await page.goto("/deploy/new?source=stack")
  const domain = settings(page).getByRole("textbox", { name: "Domain" })
  const addresses = settings(page).getByRole("list", { name: "The address each app gets" })
  await domain.fill("media.example.com")
  await expect(addresses.getByRole("listitem").first()).toHaveText(/Jellyfin\s*media\.example\.com/)
  await expect(addresses.getByRole("listitem").nth(1)).toHaveText(
    /Seerr\s*media-requests\.example\.com/,
  )
  // An apex has no first label to join, so each app's label goes in front of it.
  await domain.fill("example.com")
  await expect(addresses.getByRole("listitem").nth(1)).toHaveText(/Seerr\s*requests\.example\.com/)
  // Cleared, nothing is listed, and the domain is required.
  await domain.fill("")
  await expect(addresses).toHaveCount(0)
  await settings(page).getByRole("button", { name: "Continue", exact: true }).click()
  await expect(settings(page).getByText(/Domain: Required\./)).toBeVisible()
  expect(journey.started()).toBe(0)
  await domain.fill("media.example.com")

  // A typed password is held to the dashboard's rule before anything is made.
  const password = settings(page).getByLabel("Password for every app", { exact: true })
  await password.fill("short")
  await settings(page).getByRole("button", { name: "Continue", exact: true }).click()
  await expect(
    settings(page).getByText("Password for every app: Use at least 12 characters."),
  ).toBeVisible()
  expect(journey.started()).toBe(0)
  await password.fill("Every-app-pass-0001")
  await settings(page).getByRole("button", { name: "Continue", exact: true }).click()

  await expect.poll(() => journey.sources().length).toBeGreaterThan(0)
  const source = journey.sources()[0]
  // Only what was typed goes, and never the password: it is a secret, not an input.
  expect(source.blueprintInputs).toEqual({ domain: "media.example.com" })
  expect(JSON.stringify(journey.sources())).not.toContain("Every-app-pass-0001")
  // Never written to the tab's storage either.
  expect(await page.evaluate(() => JSON.stringify({ ...sessionStorage }))).not.toContain(
    "Every-app-pass-0001",
  )

  // The plan's names keep their services: each app's row says which it routes to.
  await currentStep(page)
  await gotoStep(page, "runtime")
  const extra = page.getByRole("list", { name: "Additional hostnames" }).getByRole("listitem")
  await expect(extra).toHaveCount(6)
  await expect(extra.first()).toContainText("Seerr")
  await expect(extra.first().locator("[data-slot='service-route']")).toHaveText("→ Serves Seerr")
  await expect(extra.first().getByRole("textbox")).toHaveValue("media-requests.example.com")
  await expect(extra.nth(4).locator("[data-slot='service-route']")).toContainText("qBittorrent")
  // An edit of a row keeps the service it routes to.
  await extra.first().getByRole("button", { name: "Serve this hostname over HTTPS" }).click()

  // The password waits in Variables as the template's own variable.
  await gotoStep(page, "variables")
  const keys = await page
    .locator("input[id^='env-key-']")
    .evaluateAll((inputs) => inputs.map((input) => (input as HTMLInputElement).value))
  expect(keys).toContain("MEDIA_LOGIN_PASSWORD")

  // Saved, every app's name still routes to its service, the edited one too.
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  const saved = (
    journey.configuration() as {
      domains: { hostname: string; https: boolean; service?: string }[]
    }
  ).domains
  expect(saved[0]).toEqual(expect.objectContaining({ hostname: "media.example.com", https: true }))
  expect(saved[0]).not.toHaveProperty("service")
  expect(saved[1]).toEqual(
    expect.objectContaining({
      hostname: "media-requests.example.com",
      https: false,
      service: "seerr",
    }),
  )
  expect(saved.filter((domain) => domain.service)).toHaveLength(6)
})

test("the Template tab lists no preset of a stack", async ({ page }) => {
  await mockNewStack(page)
  await page.goto("/deploy/new?source=template")
  await expect(page.getByRole("heading", { name: "Application templates" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Use Uptime Kuma", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: /Use Media server/ })).toHaveCount(0)
  // The count is of what is listed: four templates, not six.
  await expect(page.getByText("4 templates", { exact: true })).toBeVisible()
  await page.getByRole("textbox", { name: "Search templates" }).fill("media")
  await expect(page.getByText("No template matches that search.")).toBeVisible()
})

test("a link to a stack's template names the Stacks tab instead of using it", async ({ page }) => {
  await mockNewStack(page)
  await page.goto("/deploy/new?template=media-complete")
  const chosen = page.getByRole("region", { name: "Selected template settings" })
  await expect(chosen.getByText("This is a preset of a stack")).toBeVisible()
  await expect(chosen.getByRole("button", { name: "Use this template" })).toBeDisabled()
  await expect(chosen.getByText(/Choose it on the Stacks tab/)).toBeVisible()
  await page.getByRole("button", { name: "Stacks", exact: true }).click()
  await expect(page.getByRole("radiogroup", { name: "Preset" })).toBeVisible()
})

test("the Stacks tab fits the window at 1280 and is one column on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await mockNewStack(page)
  await page.goto("/deploy/new?source=stack")
  await expect(settings(page).getByRole("combobox", { name: "Quality" })).toBeVisible()
  const overflow = () =>
    page.locator("[data-slot='page']").evaluate((element) => {
      const shell = element.closest<HTMLElement>("[data-workspace-shell-scroll]")!
      return {
        down: shell.scrollHeight - shell.clientHeight,
        across: shell.scrollWidth - shell.clientWidth,
      }
    })
  // The flow is decided in one view: the columns scroll, not the page.
  expect(await overflow()).toEqual({ down: 0, across: 0 })
  await expect(page.locator("[data-slot='flow-panel']")).toHaveCount(1)
  await page.screenshot({ path: "test-results/deploy-new-stack-1280.png" })

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole("radiogroup", { name: "Preset" })).toBeVisible()
  expect((await overflow()).across).toBe(0)
  // Stacked, the choice and its limitations come before the command.
  const limits = await page.getByRole("list", { name: "What Complete does not do" }).boundingBox()
  const command = await settings(page)
    .getByRole("button", { name: "Continue", exact: true })
    .boundingBox()
  expect(limits!.y).toBeLessThan(command!.y)
  await page.screenshot({ path: "test-results/deploy-new-stack-390.png", fullPage: true })
})

test("a stack that cannot be read says so with a way to ask again, and an empty catalogue says there is none", async ({
  page,
}) => {
  await mockNewStack(page)
  let fail = true
  await page.route("**/api/v1/deploy/stacks", (route) =>
    fail
      ? route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "internal", message: "catalogue unreadable" } }),
        })
      : json(route, []),
  )
  await page.goto("/deploy/new?source=stack")
  await expect(page.getByText("catalogue unreadable")).toBeVisible()
  fail = false
  await page.getByRole("button", { name: "Try again", exact: true }).click()
  await expect(page.getByText("No stacks", { exact: true })).toBeVisible()
})

test("a stack's outcome points to its stack page", async ({ page }) => {
  await mockNewStack(page)
  // The first run is refused, so the outcome stays on screen to be read.
  await page.route("**/api/v1/deploy/77/environments/78/runs", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "run_refused", message: "A run could not start" } }),
    }),
  )
  await page.goto("/deploy/new?source=stack")
  await settings(page).getByRole("button", { name: "Continue", exact: true }).click()
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Deployment created")
  await expect(page.getByRole("link", { name: "Open the stack" })).toHaveAttribute(
    "href",
    "/deploy/77/stack",
  )
  await expect(page.getByText(/the stack's page says how far that is/)).toBeVisible()
  await expect(page.getByRole("link", { name: "Open deployment" })).toHaveAttribute(
    "href",
    "/deploy/77/deployments",
  )
})
