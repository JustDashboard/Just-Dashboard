import { expect, test, type Page } from "@playwright/test"
import { REFUSED_KEY, mockStackProject, mockStackSources, stackState } from "./deploy-stack-fixture"

/**
 * A stack's media sources, `/deploy/[id]/stack/sources`: the sources the
 * stack has, the catalogue to add one from (searched, filtered Public or
 * Private), one definition's fields as a form on the page's own address,
 * Add with its outcome in words, and Remove.
 */

const catalogue = (page: Page) => page.getByRole("list", { name: "Sources to add" })
const current = (page: Page) => page.locator("[data-slot='stack-sources']")
const form = (page: Page) => page.locator("[data-slot='source-form']")
const rail = (page: Page) => page.locator("[data-slot='source-rail']")

const EXISTING = {
  id: 3,
  name: "Public Domain Archive",
  definition: "publicdomainarchive",
  privacy: "public" as const,
  protocol: "torrent",
  enabled: true,
}

test("the picker lists the stack's sources and the catalogue, searched and filtered", async ({
  page,
}) => {
  await mockStackProject(page, { state: stackState("ready") })
  await mockStackSources(page, { sources: [EXISTING] })
  await page.goto("/deploy/7/stack/sources")

  await expect(page.locator("[data-slot='page']")).toHaveAttribute("data-register", "reading")
  await expect(page.getByRole("link", { name: "Stack", exact: true }).first()).toBeVisible()
  // The rail keeps the Stack row current under it.
  await expect(
    page.locator("[data-slot='sidebar']").first().locator('a[href="/deploy/7/stack"]'),
  ).toHaveAttribute("data-active", "true")

  // What the stack has: one row, its kind and that it is in use.
  const have = current(page).locator("[data-slot='choice-row']")
  await expect(have).toHaveCount(1)
  await expect(have).toContainText("Public Domain Archive")
  await expect(have).toContainText("Public · torrent")
  await expect(have).toContainText("In use")

  // The catalogue, cut to its first rows with a line that says so.
  const rows = catalogue(page).locator("[data-slot='choice-row']")
  await expect(rows).toHaveCount(60)
  await expect(page.getByText("The first 60 of 67. Search to reach the rest.")).toBeVisible()
  await expect(rows.first()).toContainText("Community Library")
  await expect(rows.first()).toContainText("Asks for your account")
  await expect(rows.first()).toContainText("Semi-private")

  const search = page.getByRole("textbox", { name: "Search the sources" })
  await search.fill("torznab")
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText("Generic Torznab")
  await expect(page.getByText(/The first 60 of/)).toHaveCount(0)
  await search.fill("no such source anywhere")
  await expect(page.getByText("No source matches.")).toBeVisible()
  await search.fill("")

  // Public, then Private, which holds the semi-private ones too; kept in the address.
  const filters = page.getByRole("group", { name: "Which sources" })
  await filters.getByRole("button", { name: "Public", exact: true }).click()
  await expect(page).toHaveURL(/\?filter=public$/)
  await expect(rows.first()).toContainText("Public Domain Archive")
  await expect(catalogue(page)).not.toContainText("Generic Torznab")
  await filters.getByRole("button", { name: "Private", exact: true }).click()
  await expect(page).toHaveURL(/\?filter=private$/)
  await expect(rows.first()).toContainText("Community Library")
  await expect(rows.nth(1)).toContainText("Generic Torznab")
  await expect(catalogue(page)).not.toContainText("Public Domain Archive")
  // Every row is one height.
  const heights = await rows.evaluateAll((items) =>
    items.map((item) => Math.round(item.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)
  await page.screenshot({ path: "test-results/deploy-stack-sources-1280.png", fullPage: true })
})

test("a source with a password field opens as a form on its own address, masked, and Add says it is added", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockStackProject(page, { state: stackState("action_required") })
  const sources = await mockStackSources(page)
  await page.goto("/deploy/7/stack/sources")
  await expect(current(page)).toContainText("None yet")
  await catalogue(page).getByRole("link", { name: "Open Generic Torznab" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/stack\/sources\?definition=torznab$/)

  // A page, not a sheet: the form is the page's, with the catalogue as its rail.
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(form(page)).toContainText("Generic Torznab")
  await expect(form(page)).toContainText("Private")
  await expect(form(page)).toContainText("Any Torznab feed you run yourself")
  await expect(rail(page)).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Generic Torznab" })).toHaveAttribute(
    "aria-current",
    "page",
  )

  const url = form(page).getByRole("textbox", { name: "URL" })
  const key = form(page).getByLabel("API key", { exact: true })
  await expect(key).toHaveAttribute("type", "password")
  await form(page).getByRole("button", { name: "Show the password" }).click()
  await expect(key).toHaveAttribute("type", "text")
  await form(page).getByRole("button", { name: "Show the password" }).click()
  await expect(key).toHaveAttribute("type", "password")
  // The advanced fields are folded.
  await expect(form(page).getByRole("spinbutton", { name: "Minimum seeders" })).toHaveCount(0)
  await form(page).getByRole("button", { name: "More settings" }).click()
  await expect(form(page).getByRole("spinbutton", { name: "Minimum seeders" })).toHaveValue("1")

  await url.fill("https://feed.example.test")
  await key.fill("typed-api-key")
  await form(page).getByRole("button", { name: "Add", exact: true }).click()
  const outcome = form(page).locator("[data-slot='source-outcome']")
  await expect(outcome).toContainText("Generic Torznab is added")
  await expect(outcome).toContainText("shared it with Radarr and Sonarr")
  expect(sources.added()).toEqual([
    {
      definition: "torznab",
      fields: { baseUrl: "https://feed.example.test", apiKey: "typed-api-key", minimumSeeders: 1 },
    },
  ])
  // What was typed is the form's alone: not in the address, not in the tab's storage.
  expect(page.url()).not.toContain("typed-api-key")
  expect(await page.evaluate(() => JSON.stringify({ ...sessionStorage }))).not.toContain(
    "typed-api-key",
  )
  await expect(form(page).getByRole("button", { name: "Add", exact: true })).toBeDisabled()

  // Back on the list, the stack has it.
  await outcome.getByRole("link", { name: "See the stack's sources" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/stack\/sources$/)
  await expect(current(page).locator("[data-slot='choice-row']")).toContainText("Generic Torznab")
  await page.screenshot({ path: "test-results/deploy-stack-source-1440.png", fullPage: true })
})

test("a source Prowlarr's test refuses is said with its reason, and nothing is added", async ({
  page,
}) => {
  await mockStackProject(page, { state: stackState("action_required") })
  const sources = await mockStackSources(page)
  await page.goto("/deploy/7/stack/sources?definition=torznab")
  await form(page).getByRole("textbox", { name: "URL" }).fill("https://feed.example.test")
  await form(page).getByLabel("API key", { exact: true }).fill(REFUSED_KEY)
  await form(page).getByRole("button", { name: "Add", exact: true }).click()
  const outcome = form(page).locator("[data-slot='source-outcome']")
  await expect(outcome).toContainText(
    "Prowlarr's test of Generic Torznab failed, so it was not added",
  )
  await expect(outcome).toContainText("the feed answered 401 Unauthorized for the API key")
  await expect(page.getByText("source_test_failed")).toHaveCount(0)
  expect(sources.added()).toHaveLength(1)
  expect(sources.sources()).toEqual([])
  // Changing what was typed puts the old answer away, and Add is there to press again.
  await form(page).getByLabel("API key", { exact: true }).fill("typed-api-key")
  await expect(outcome).toHaveCount(0)
  await form(page).getByRole("button", { name: "Add", exact: true }).click()
  await expect(form(page).locator("[data-slot='source-outcome']")).toContainText(
    "Generic Torznab is added",
  )
})

test("a source with a switch and a list is asked with both, and one with nothing to fill in says so", async ({
  page,
}) => {
  await mockStackProject(page, { state: stackState("action_required") })
  const sources = await mockStackSources(page)
  await page.goto("/deploy/7/stack/sources?definition=communitylibrary")
  await form(page).getByRole("textbox", { name: "User name" }).fill("member")
  await form(page).getByLabel("Password", { exact: true }).fill("member-password")
  await form(page)
    .getByRole("switch", { name: /Only what does not count against your ratio/ })
    .click()
  await form(page).getByRole("button", { name: "More settings" }).click()
  await form(page).getByRole("combobox", { name: "Sort results by" }).click()
  await page.getByRole("option", { name: "Seeders" }).click()
  await form(page).getByRole("button", { name: "Add", exact: true }).click()
  await expect(form(page).locator("[data-slot='source-outcome']")).toContainText(
    "Community Library is added",
  )
  expect(sources.added()[0]).toEqual({
    definition: "communitylibrary",
    fields: { username: "member", password: "member-password", freeleech: true, sort: 1 },
  })

  await page.goto("/deploy/7/stack/sources?definition=publicdomainarchive")
  await expect(form(page)).toContainText("Nothing to fill in")
  await expect(form(page).getByRole("button", { name: "Add", exact: true })).toBeEnabled()
})

test("Remove asks first, then the source is gone", async ({ page }) => {
  await mockStackProject(page, { state: stackState("ready") })
  const sources = await mockStackSources(page, { sources: [EXISTING] })
  await page.goto("/deploy/7/stack/sources")
  const row = current(page).locator("[data-slot='choice-row']")
  await row.getByRole("button", { name: "More for Public Domain Archive" }).click()
  await page.getByRole("menuitem", { name: "Remove" }).click()
  const dialog = page.getByRole("dialog", { name: "Remove source" })
  await expect(dialog).toContainText("Radarr and Sonarr stop searching it")
  expect(sources.removed()).toEqual([])
  await dialog.getByRole("button", { name: "Remove", exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(sources.removed()).toEqual([3])
  await expect(current(page)).toContainText("None yet")
})

for (const [refusal, words] of [
  ["stack_app_unreachable", "Prowlarr did not answer"],
  ["stack_waiting", "The stack is not installed yet"],
  ["stack_no_sources", "This preset has no Prowlarr"],
] as const) {
  test(`a picker refused as ${refusal} says so in words`, async ({ page }) => {
    await mockStackProject(page, { state: stackState("ready") })
    await mockStackSources(page, { refusal })
    await page.goto("/deploy/7/stack/sources")
    await expect(current(page).locator("[data-slot='sources-refusal']")).toContainText(words)
    await expect(page.getByText(refusal)).toHaveCount(0)
    if (refusal === "stack_app_unreachable")
      await expect(
        current(page).getByRole("button", { name: "Try again", exact: true }),
      ).toBeVisible()
  })
}

test("on a phone the picker is one pane, and a source is a page with the way back", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockStackProject(page, { state: stackState("action_required") })
  await mockStackSources(page, { sources: [EXISTING] })
  await page.goto("/deploy/7/stack/sources")
  const sideways = () =>
    page.evaluate(() =>
      [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
        .map((el) => el.parentElement ?? el)
        .some((el) => el.scrollWidth > el.clientWidth + 1),
    )
  expect(await sideways()).toBe(false)
  await catalogue(page).getByRole("link", { name: "Open Generic Torznab" }).click()
  await expect(page).toHaveURL(/definition=torznab/)
  // One pane: no rail beside it, the way back on top, the form's first field in view.
  await expect(rail(page)).toBeHidden()
  await expect(current(page)).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Media sources" })).toBeVisible()
  await expect(form(page).getByRole("textbox", { name: "URL" })).toBeInViewport()
  expect(await sideways()).toBe(false)
  await page.screenshot({ path: "test-results/deploy-stack-source-390.png", fullPage: true })
  await page.getByRole("link", { name: "Media sources" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/stack\/sources$/)
  await expect(current(page)).toContainText("Public Domain Archive")
})
