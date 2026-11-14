import { expect, test, type Locator, type Page } from "@playwright/test"
import { mockSections } from "./deploy-section-fixture"
import { risen } from "./risen"

/**
 * What the four section pages beside Notifications promise since they were
 * drawn as it is (Log drains, Secret stores, Templates, Shared variables): a
 * picture first, a count on the section's head, rows and cards of one height
 * in every state with their detail in a layer over the list, the picture in
 * one place loading and loaded, and no control a read-only account cannot
 * use. What each page's verbs send is held by the page's own spec.
 */

const WIDTHS = [
  { width: 1280, height: 900 },
  { width: 390, height: 844 },
]

const picture = (page: Page) => page.locator("[data-slot=wire-flow]")
const head = (page: Page, title: string) =>
  page.locator("[data-slot=panel-header]").filter({ hasText: title })
const row = (page: Page, list: string, name: string) =>
  page.getByRole("list", { name: list }).locator("[data-slot=choice-row]", { hasText: name })

async function open(page: Page, path: string, size: { width: number; height: number }) {
  await page.emulateMedia({ reducedMotion: "reduce" })
  await page.setViewportSize(size)
  await page.goto(path)
}

/** Every logo and favicon drawn: a capture taken before they load shows empty tiles. */
const pictured = (page: Page) =>
  page.waitForFunction(() => [...document.images].every((image) => image.complete))

async function box(page: Page, locator: Locator) {
  await risen(page)
  const found = await locator.boundingBox()
  if (!found) throw new Error("not drawn")
  return found
}

function overflow(page: Page) {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
}

/** The page opens on its picture, with no row of tiles anywhere on it. */
async function opensOnPicture(page: Page, title: string) {
  await expect(picture(page)).toBeVisible()
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  const drawn = await box(page, picture(page))
  const section = await box(page, head(page, title))
  expect(drawn.y).toBeLessThan(section.y)
}

for (const size of WIDTHS) {
  test(`drains in every state are one height, and their detail opens over the list at ${size.width}px`, async ({
    page,
  }) => {
    await mockSections(page)
    await open(page, "/deploy/drains", size)
    const names = ["Grafana Cloud", "Local Loki", "Vector", "Tempo"]
    await expect(row(page, "Log drains", "Tempo")).toBeVisible()
    await opensOnPicture(page, "Drains")
    await expect(head(page, "Drains")).toContainText("4 drains · 1 failing · 1 paused")

    const before = await Promise.all(names.map((name) => box(page, row(page, "Log drains", name))))
    for (const drawn of before) expect(drawn.height).toBe(before[0].height)

    // A state column, not a second line: a dot and a word on each.
    const failing = row(page, "Log drains", "Local Loki")
    await expect(failing.getByText(/^Failing 3m$/)).toBeVisible()
    await expect(row(page, "Log drains", "Grafana Cloud").getByText("Shipping")).toBeVisible()
    await expect(row(page, "Log drains", "Vector").getByText("Paused")).toBeVisible()
    await expect(row(page, "Log drains", "Tempo").getByText("Nothing sent")).toBeVisible()
    await expect(failing.getByText("86 waiting")).toBeVisible()

    // The destination's own words are a press on the state away, and move nothing.
    await failing.getByRole("button", { name: /^Failing 3m/ }).click()
    await expect(
      page.getByText(/Failing since .*: the destination did not answer: connection refused/),
    ).toBeVisible()
    await expect(page.getByRole("dialog", { name: /drain/i })).toHaveCount(0)
    const after = await Promise.all(names.map((name) => box(page, row(page, "Log drains", name))))
    expect(after).toEqual(before)
    expect(await overflow(page)).toBeLessThanOrEqual(1)
  })

  test(`stores in every state are one height, and their readers open over the list at ${size.width}px`, async ({
    page,
  }) => {
    await mockSections(page)
    await open(page, "/deploy/secret-stores", size)
    const names = ["vault-prod", "bao-staging", "infisical", "aws"]
    await expect(row(page, "Secret stores", "aws")).toBeVisible()
    await opensOnPicture(page, "Stores")
    await expect(head(page, "Stores")).toContainText("4 stores · 1 refused")

    const before = await Promise.all(
      names.map((name) => box(page, row(page, "Secret stores", name))),
    )
    for (const drawn of before) expect(drawn.height).toBe(before[0].height)

    const refused = row(page, "Secret stores", "bao-staging")
    await expect(refused.getByText(/^Refused 2m ago$/)).toBeVisible()
    await expect(row(page, "Secret stores", "vault-prod").getByText("Answering")).toBeVisible()
    await expect(row(page, "Secret stores", "aws").getByText("Not tested")).toBeVisible()
    await expect(row(page, "Secret stores", "aws").getByText("Not read")).toBeVisible()

    await refused.getByRole("button", { name: /^Refused 2m ago/ }).click()
    await expect(
      page.getByText(/A run's read failed .*: AppRole sign-in refused: invalid role or secret ID/),
    ).toBeVisible()
    await page.keyboard.press("Escape")

    await row(page, "Secret stores", "vault-prod")
      .getByRole("button", { name: "Read by 3" })
      .click()
    const readers = page.getByRole("list", { name: "Variables reading vault-prod" })
    await expect(readers.getByRole("link", { name: "billing-worker" })).toHaveAttribute(
      "href",
      "/deploy/14/settings/variables",
    )
    await expect(readers.getByText("vault-prod/apps/api-production#DATABASE_URL")).toBeVisible()
    // A press in the layer stays there: the row's editor does not open.
    await readers.getByText("production as", { exact: false }).first().click()
    await expect(page.getByRole("dialog", { name: /Edit/ })).toHaveCount(0)
    const after = await Promise.all(
      names.map((name) => box(page, row(page, "Secret stores", name))),
    )
    expect(after).toEqual(before)
    expect(await overflow(page)).toBeLessThanOrEqual(1)
  })

  test(`template cards are one height, and choosing one draws it without moving them at ${size.width}px`, async ({
    page,
  }) => {
    await mockSections(page)
    await open(page, "/deploy/templates", size)
    const grid = page.getByRole("group", { name: "Your templates" })
    const card = (name: string) => grid.locator("[data-slot=choice-card]", { hasText: name })
    const names = ["Shop front", "Plausible", "Valkey"]
    await expect(card("Valkey")).toBeVisible()
    await opensOnPicture(page, "Your templates")
    await expect(head(page, "Your templates")).toContainText("3 templates · 1 in use")

    // The first is drawn until another is chosen: where it came from and what it runs.
    await expect(picture(page).getByText("user-shop-front · v1.0.0")).toBeVisible()
    await expect(picture(page).getByText("2 deployments use it")).toBeVisible()
    await expect(picture(page).getByRole("link", { name: "Open api-production" })).toHaveAttribute(
      "href",
      "/deploy/7",
    )

    const before = await Promise.all(names.map((name) => box(page, card(name))))
    for (const drawn of before) expect(drawn.height).toBe(before[0].height)
    await expect(card("Shop front").getByText("Used by 2")).toBeVisible()
    await expect(card("Valkey").getByText("Preview only")).toBeVisible()

    await card("Plausible").getByRole("button", { name: "Show Plausible" }).click()
    await expect(card("Plausible")).toHaveAttribute("data-selected", "true")
    await expect(picture(page).getByText("user-plausible · v1.0.0")).toBeVisible()
    await expect(picture(page).getByText("No deployment uses it")).toBeVisible()
    if (size.width >= 1280) {
      await expect(picture(page).getByText("A Compose file", { exact: true })).toBeVisible()
      for (const service of ["plausible", "db", "events"]) {
        await expect(picture(page).getByText(service, { exact: true })).toBeVisible()
      }
      await expect(picture(page).getByText("postgres:16-alpine")).toBeVisible()
    } else {
      // A phone draws the marks alone: the hub says how it was made, and the
      // card names the services.
      await expect(picture(page).getByText(/^From a Compose file by operator · /)).toBeVisible()
      await expect(card("Plausible").getByText("3 services: plausible, db, events")).toBeVisible()
    }
    // The picture is as tall for three services as for one image, so nothing
    // under it moves.
    const after = await Promise.all(names.map((name) => box(page, card(name))))
    expect(after).toEqual(before)

    // One this dashboard cannot deploy says why, and offers no way to try.
    await card("Valkey").getByRole("button", { name: "Show Valkey" }).click()
    await expect(
      picture(page).getByText("This template needs dashboard 0.9.0 or newer."),
    ).toBeVisible()
    await expect(card("Valkey").getByRole("button", { name: "Deploy Valkey" })).toHaveCount(0)
    await expect(card("Plausible").getByRole("button", { name: "Deploy Plausible" })).toBeVisible()
    expect(await overflow(page)).toBeLessThanOrEqual(1)
  })

  test(`shared variables open on what they reach, counted on the head at ${size.width}px`, async ({
    page,
  }) => {
    await mockSections(page)
    await open(page, "/deploy/variables", size)
    await expect(row(page, "Server variables", "PUBLIC_API_URL")).toBeVisible()
    await opensOnPicture(page, "Server variables")
    await expect(head(page, "Server variables")).toContainText("5 variables · 2 not live yet")
    // The projects that read them, each once.
    for (const project of ["api-production", "automations", "billing-worker"]) {
      await expect(picture(page).getByRole("link", { name: `Open ${project}` })).toHaveCount(1)
    }
    // Four projects read them: the column draws all four, one more than it counts past.
    await expect(picture(page).getByRole("link", { name: "Open status-page" })).toHaveCount(1)
    expect(await overflow(page)).toBeLessThanOrEqual(1)
  })

  for (const [path, title] of [
    ["/deploy/drains", "Drains"],
    ["/deploy/secret-stores", "Stores"],
    ["/deploy/templates", "Your templates"],
    ["/deploy/variables", "Server variables"],
  ]) {
    test(`${path} keeps its picture and its head in one place loading and loaded at ${size.width}px`, async ({
      page,
    }) => {
      const { release } = await mockSections(page, { hold: true })
      await open(page, path, size)
      await expect(picture(page)).toHaveAttribute("data-loading", "true")
      const waiting = await box(page, picture(page))
      const waitingHead = await box(page, head(page, title))
      release()
      await expect(picture(page)).not.toHaveAttribute("data-loading", "true")
      await expect(
        page.locator("[data-slot=choice-row], [data-slot=choice-card]").first(),
      ).toBeVisible()
      const loaded = await box(page, picture(page))
      expect(loaded.y).toBe(waiting.y)
      // Wide, the picture is the height its skeleton was, so the list's head
      // stays where it stood. A phone's row of marks wraps by how many there are.
      if (size.width >= 1280) expect((await box(page, head(page, title))).y).toBe(waitingHead.y)
    })
  }
}

test("a read-only account sees the four pages and no control it cannot use", async ({ page }) => {
  await mockSections(page, { readOnly: true })
  await open(page, "/deploy/drains", WIDTHS[0])
  await expect(row(page, "Log drains", "Tempo")).toBeVisible()
  // It may still open what a drain last answered.
  await expect(page.getByRole("button", { name: /^Failing 3m/ })).toBeVisible()
  for (const name of [/^Add /, /^Edit /, "Send test", /^Actions for/]) {
    await expect(page.getByRole("button", { name })).toHaveCount(0)
  }

  await page.goto("/deploy/secret-stores")
  await expect(row(page, "Secret stores", "aws")).toBeVisible()
  await expect(page.getByRole("button", { name: "Read by 3" })).toBeVisible()
  for (const name of [/^Add /, /^Edit /, "Test", /^Actions for/]) {
    await expect(page.getByRole("button", { name, exact: name === "Test" })).toHaveCount(0)
  }

  await page.goto("/deploy/templates")
  const grid = page.getByRole("group", { name: "Your templates" })
  await expect(grid.locator("[data-slot=choice-card]")).toHaveCount(3)
  // Exporting a definition is a read; deploying and removing are not offered.
  await expect(grid.getByRole("button", { name: "Export as JSON" })).toHaveCount(3)
  for (const name of [/^Deploy /, /^Actions for/, "Import…", "Add from Compose file"]) {
    await expect(page.getByRole("button", { name })).toHaveCount(0)
  }

  await page.goto("/deploy/variables")
  await expect(row(page, "Server variables", "PUBLIC_API_URL")).toBeVisible()
  for (const name of ["Add variable", /^Edit /, "Reveal value"]) {
    await expect(page.getByRole("button", { name })).toHaveCount(0)
  }
})

test("an empty page asks its first question, with the picture only where something is wired", async ({
  page,
}) => {
  await mockSections(page, { drains: [], stores: [], templates: [], variables: [] })
  await open(page, "/deploy/drains", WIDTHS[0])
  // The projects are real, so the picture stands: nothing wired to a drain yet.
  await expect(picture(page).getByText("No drain yet")).toBeVisible()
  await expect(picture(page).getByText("copied nowhere yet")).toBeVisible()
  for (const format of ["OpenTelemetry", "Grafana Loki", "JSON lines over HTTP"]) {
    await expect(page.getByRole("button", { name: `Add ${format}` })).toHaveCount(1)
  }
  // Each format is drawn as a product, not as a grey glyph.
  await expect(page.locator("[data-slot=choice-card] img")).toHaveCount(3)

  await page.goto("/deploy/secret-stores")
  await expect(page.getByRole("button", { name: "Add HashiCorp Vault" })).toBeVisible()
  await expect(page.locator("[data-slot=choice-card]")).toHaveCount(6)
  await expect(picture(page)).toHaveCount(0)
  // The card says how it signs in; the sentence about references is in the sheet.
  await expect(page.getByText("KV version 2, token or AppRole").first()).toBeVisible()
  await expect(page.getByText(/A reference names a secret/)).toHaveCount(0)

  await page.goto("/deploy/templates")
  await expect(
    page.getByRole("button", { name: "Add a template from a Compose file" }),
  ).toBeVisible()
  await expect(picture(page)).toHaveCount(0)
  // A Compose file as Compose, a deployment as a project of this server.
  await expect(page.locator("[data-slot=choice-card] img").first()).toBeVisible()

  await page.goto("/deploy/variables")
  await expect(page.getByText("No server variables yet")).toBeVisible()
  await expect(picture(page)).toHaveCount(0)
})

test("the pictures offer what is not added yet, and step back what a node does not reach", async ({
  page,
}) => {
  const [first] = (await import("./deploy-section-fixture")).sectionDrains
  await mockSections(page, { drains: [first] })
  await open(page, "/deploy/drains", WIDTHS[0])
  await expect(row(page, "Log drains", "Grafana Cloud")).toBeVisible()
  // The two formats not added are rings in the picture, pressed to add one.
  await page.getByRole("button", { name: "Add Grafana Loki" }).click()
  const sheet = page.getByRole("dialog", { name: "Add drain" })
  await expect(sheet.getByLabel("Endpoint", { exact: true })).toHaveAttribute(
    "placeholder",
    "http://loki:3100",
  )
  await sheet.getByRole("button", { name: "Cancel" }).click()

  await page.goto("/deploy/secret-stores")
  await expect(row(page, "Secret stores", "aws")).toBeVisible()
  // Pointing at a store keeps the projects that read it and steps back the rest.
  await picture(page).locator('[data-node="store:2"]').hover()
  await expect(picture(page).locator('[data-node="project:8"]')).not.toHaveAttribute("data-dim", "")
  await expect(picture(page).locator('[data-node="project:7"]')).toHaveAttribute("data-dim", "")
})

test("the four section pages at 1280 and 390 @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go in")
  test.setTimeout(240_000)
  const prefix = process.env.JD_SHOTS_PREFIX ?? "after"
  const shot = async (name: string, width: number) => {
    await pictured(page)
    await page.screenshot({
      path: `${process.env.JD_SHOTS}/${prefix}-${name}-${width}.png`,
      fullPage: true,
    })
  }
  const pages = [
    ["drains", "/deploy/drains", "Log drains"],
    ["secret-stores", "/deploy/secret-stores", "Secret stores"],
    ["templates", "/deploy/templates", ""],
    ["variables", "/deploy/variables", "Server variables"],
  ] as const
  const settled = async (list: string) => {
    const item = list
      ? page.getByRole("list", { name: list }).locator("[data-slot=choice-row]").first()
      : page.locator("[data-slot=choice-card]").first()
    await expect(item).toBeVisible()
    await risen(page)
  }
  for (const size of WIDTHS) {
    await page.emulateMedia({ reducedMotion: "reduce" })
    await page.setViewportSize(size)

    await mockSections(page)
    for (const [name, path] of pages) {
      await page.goto(path)
      // Either shape: the base build these are compared with drew rows only.
      await expect(
        page.locator("[data-slot=choice-row], [data-slot=choice-card]").first(),
      ).toBeVisible()
      await risen(page)
      await shot(`${name}-full`, size.width)
    }
    if (prefix !== "after") continue

    // The layers that hold what the rows no longer do.
    await page.goto("/deploy/drains")
    await settled("Log drains")
    await page.getByRole("button", { name: /^Failing/ }).click()
    await shot("drains-state", size.width)
    await page.goto("/deploy/secret-stores")
    await settled("Secret stores")
    await page.getByRole("button", { name: "Read by 3" }).click()
    await shot("secret-stores-readers", size.width)
    await page.goto("/deploy/templates")
    await settled("")
    await page.getByRole("button", { name: "Show Plausible" }).click()
    await expect(picture(page).getByText("user-plausible · v1.0.0")).toBeVisible()
    await risen(page)
    await shot("templates-stack", size.width)

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockSections(page, { readOnly: true })
    for (const [name, path, list] of pages) {
      await page.goto(path)
      await settled(list)
      await shot(`${name}-readonly`, size.width)
    }

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockSections(page, { drains: [], stores: [], templates: [], variables: [] })
    for (const [name, path] of pages) {
      await page.goto(path)
      await expect(
        page.locator("[data-slot=choice-card], [data-slot=empty-state]").first(),
      ).toBeVisible()
      await risen(page)
      await shot(`${name}-empty`, size.width)
    }

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockSections(page, { hold: true })
    for (const [name, path] of pages) {
      await page.goto(path)
      await expect(picture(page)).toHaveAttribute("data-loading", "true")
      await shot(`${name}-loading`, size.width)
    }

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockSections(page)
    for (const pattern of [
      "**/api/v1/deploy/log-drains**",
      "**/api/v1/deploy/secret-stores**",
      "**/api/v1/deploy/templates**",
      "**/api/v1/deploy/shared-variables**",
    ]) {
      await page.route(pattern, (route) =>
        route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "internal", message: "the store did not answer" },
          }),
        }),
      )
    }
    for (const [name, path] of pages) {
      await page.goto(path)
      await expect(page.getByText(/did not answer/i).first()).toBeVisible()
      await risen(page)
      await shot(`${name}-failed`, size.width)
    }
    await page.unrouteAll({ behavior: "ignoreErrors" })
  }
})
