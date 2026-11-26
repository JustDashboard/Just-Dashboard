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
  await expect(services.getByRole("listitem").first()).toContainText("Public")
  await expect(services.getByRole("listitem").first()).toContainText("Plays your films and series")
  await expect(services.getByRole("listitem").nth(1)).toContainText("Seerr")
  await expect(services.getByRole("listitem").nth(1)).toContainText("Private")
  await expect(services.getByRole("listitem").last()).toContainText("Recyclarr")
  await expect(services.getByRole("listitem").last()).toContainText("Background")
  await expect(
    page.getByText(/1 public, 6 private, 1 background: public is on the project's address/),
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
  await expect(settings(page).getByRole("textbox", { name: "Jellyfin's domain" })).toHaveValue(
    "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
  )
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
  await expect(settings(page).getByRole("textbox", { name: "Jellyfin's domain" })).toBeVisible()
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
    blueprintVersion: "1.0.0",
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
