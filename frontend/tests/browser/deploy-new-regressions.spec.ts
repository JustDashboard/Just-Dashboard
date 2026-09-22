import { expect, test } from "@playwright/test"
import {
  blueprintCatalogue,
  gotoStep,
  json,
  mockNewProject,
  postgresBlueprint,
} from "./deploy-fixture"

test("a phone opens selected settings above a long catalogue and returns to the choices", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ reducedMotion: "reduce" })
  await page.route("**/api/v1/deploy/blueprints/", (route) =>
    json(route, [
      ...blueprintCatalogue,
      ...Array.from({ length: 40 }, (_, index) => ({
        ...blueprintCatalogue[1],
        id: `extra-${index}`,
        name: `Extra template ${index}`,
      })),
    ]),
  )
  await page.goto("/deploy/new?source=template")
  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()
  const settings = page.getByRole("region", { name: "Selected template settings" })
  await expect(settings).toBeFocused()
  await expect(page.getByRole("textbox", { name: "Public domain" })).toBeInViewport()
  expect((await settings.boundingBox())!.y).toBeLessThan(100)
  await page.getByRole("button", { name: "Choose another template" }).click()
  await expect(page.locator("#template-catalogue")).toBeFocused()
  await expect(page.getByRole("heading", { name: "Application templates" })).toBeInViewport()
  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()
  await page.getByRole("textbox", { name: "Public domain" }).fill("vault.example.test")
  await page.getByRole("button", { name: "Use this template", exact: true }).click()
  const question = page.getByRole("heading", { level: 1, name: "What does it need to run?" })
  await expect(question).toBeFocused()
  await expect(question).toBeInViewport()
  await gotoStep(page, "project")
  await expect(
    page.getByRole("heading", { level: 1, name: "What are you building?" }),
  ).toBeInViewport()
  await gotoStep(page, "review")
  await expect(page.getByText("Runtime plan is valid")).toBeVisible()
  const reviewQuestion = page.getByRole("heading", { level: 1, name: "Ready to deploy?" })
  await expect(reviewQuestion).toBeFocused()
  await expect(reviewQuestion).toBeInViewport()
})

test("switching templates keeps the catalogue width and each template's input", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.setViewportSize({ width: 1720, height: 1000 })
  let release!: () => void
  const delayed = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/deploy/blueprints/postgresql", async (route) => {
    await delayed
    return json(route, postgresBlueprint)
  })
  await page.goto("/deploy/new?source=template")
  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()
  const domain = page.getByRole("textbox", { name: "Public domain" })
  await domain.fill("vault.example.test")
  const catalogue = page.getByRole("group", { name: "Web applications" })
  const width = (await catalogue.boundingBox())!.width

  await page.getByRole("button", { name: "Use PostgreSQL", exact: true }).click()
  await expect(page.getByRole("status", { name: "Loading template settings" })).toBeVisible()
  expect((await catalogue.boundingBox())!.width).toBeCloseTo(width, 0)
  await expect(page.getByRole("button", { name: "Use this template" })).toBeDisabled()
  release()
  await page.getByRole("textbox", { name: "Database name" }).fill("analytics")
  await page.getByRole("button", { name: "Use PostgreSQL", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Database name" })).toHaveValue("analytics")
  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()
  await expect(domain).toHaveValue("vault.example.test")
  await page.getByRole("button", { name: "Use PostgreSQL", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Database name" })).toHaveValue("analytics")
})

test("a failed template detail stays visible and can be retried", async ({ page }) => {
  await mockNewProject(page)
  let requests = 0
  await page.route("**/api/v1/deploy/blueprints/postgresql", async (route) => {
    if (++requests > 1) return json(route, postgresBlueprint)
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "unavailable",
          message: "Template temporarily unavailable",
          retryable: true,
        },
      }),
    })
  })
  await page.goto("/deploy/new?source=template")
  await page.getByRole("button", { name: "Use PostgreSQL", exact: true }).click()
  await expect(page.getByText("Template temporarily unavailable", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByRole("textbox", { name: "Database name" })).toHaveValue("app")
})

test("a source inspection finishing after a tab change cannot reopen the abandoned source", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  let release!: () => void
  let started!: () => void
  const delayed = new Promise<void>((resolve) => {
    release = resolve
  })
  const requested = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route("**/api/v1/deploy/drafts/journey-draft/detect", async (route) => {
    started()
    await delayed
    return route.fallback()
  })
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await requested
  await page.getByRole("button", { name: "Template", exact: true }).click()
  release()
  await expect.poll(() => journey.discarded()).toContain("journey-draft")
  await expect(page.getByRole("heading", { name: "Application templates" })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Ready to deploy?" })).toHaveCount(0)
})

test("edited project intent reaches the server and invalidates the previous review", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "project")
  await page.getByRole("textbox", { name: "Project name" }).fill("background-jobs")
  await page.getByRole("combobox", { name: "Project type" }).click()
  await page.getByRole("option", { name: "Worker or bot" }).click()
  await gotoStep(page, "review")
  await expect
    .poll(() => journey.intent())
    .toMatchObject({ name: "background-jobs", profile: "worker" })
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(journey.configuration()).toMatchObject({
    domains: [],
    runtime: { internalPort: 0, strategy: "stop_first" },
  })
})

test("typing a Git ref inspects it once and preserves build overrides", async ({ page }) => {
  await mockNewProject(page)
  const refs: string[] = []
  await page.route("**/api/v1/deploy/drafts/journey-draft", async (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON()
      if (body.step === "source") refs.push(body.source.ref)
    }
    return route.fallback()
  })
  await page.goto("/deploy/new?repo=https://git.example.test/team/site.git")
  await page.getByRole("button", { name: "Import", exact: true }).click()
  await gotoStep(page, "project")
  await page.getByRole("button", { name: "Change the build settings" }).click()
  await page.getByRole("textbox", { name: "Build command" }).fill("bun run build:production")
  const branch = page.getByRole("textbox", { name: "Branch" })
  await branch.fill("")
  await branch.pressSequentially("release/2026")
  expect(refs).toEqual(["main"])
  await branch.press("Enter")
  await expect.poll(() => refs).toEqual(["main", "release/2026"])
  await expect(page.getByRole("textbox", { name: "Build command" })).toHaveValue(
    "bun run build:production",
  )
  await expect(page.getByRole("button", { name: "Continue", exact: true })).toBeEnabled()
})

test("encrypted draft values survive a reload and can be retained or removed without exposing them", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "variables")
  await page.getByText("Paste .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables", exact: true })
    .fill("API_TOKEN=never-store-this\nEMPTY_OVERRIDE=")
  await gotoStep(page, "review")
  await expect.poll(() => journey.environmentKeys()).toEqual(["API_TOKEN", "EMPTY_OVERRIDE"])
  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    "never-store-this",
  )
  await page.reload()
  await gotoStep(page, "variables")
  await expect(page.getByRole("button", { name: "Remove saved variable API_TOKEN" })).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Remove saved variable EMPTY_OVERRIDE" }),
  ).toBeVisible()
  await gotoStep(page, "review")
  await expect.poll(() => journey.environmentKeys()).toEqual(["API_TOKEN", "EMPTY_OVERRIDE"])
  expect(journey.staged()?.retainEnvironmentKeys).toContain("EMPTY_OVERRIDE")
  await gotoStep(page, "variables")
  await page.getByRole("button", { name: "Remove saved variable API_TOKEN" }).click()
  await page.getByRole("button", { name: "Remove saved variable EMPTY_OVERRIDE" }).click()
  await gotoStep(page, "review")
  await expect.poll(() => journey.environmentKeys()).toEqual([])
})

test("pasted Compose credentials stay out of browser storage", async ({ page }) => {
  await mockNewProject(page)
  await page.goto("/deploy/new?source=compose")
  await page
    .getByLabel("compose.yml content")
    .fill(
      "services:\n  app:\n    image: example/app\n    environment:\n      PASSWORD: compose-private-value\n",
    )
  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    "compose-private-value",
  )
  await page.getByRole("button", { name: "Template", exact: true }).click()
  await page.getByRole("button", { name: "Compose", exact: true }).click()
  await expect(page.getByLabel("compose.yml content")).toHaveValue(/compose-private-value/)
})

test("an environment validation failure does not create a partially configured project", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.route("**/api/v1/deploy/drafts/journey-draft", async (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON()
      if (body.dotenv?.includes("invalid-value"))
        return route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "invalid_request", message: "Environment input is invalid" },
          }),
        })
    }
    return route.fallback()
  })
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "variables")
  await page.getByText("Paste .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables", exact: true })
    .fill("API_TOKEN=invalid-value")
  await gotoStep(page, "review")
  await expect(page.getByText("Environment input is invalid", { exact: true })).toBeVisible()
  expect(journey.commits()).toBe(0)
  await gotoStep(page, "variables")
  await page.getByText("Paste .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables", exact: true })
    .fill("API_TOKEN=correct-value")
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  expect(journey.commits()).toBe(1)
})

test("a saved visitor password survives reload as its server hash without repeated preflight", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  let checks = 0
  await page.route("**/api/v1/deploy/drafts/journey-draft/preflight", (route) => {
    checks += 1
    return route.fallback()
  })
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")
  await page.getByRole("switch", { name: "Ask visitors for a password" }).click()
  await page.getByRole("textbox", { name: "User name" }).fill("client")
  await page.locator("#public-protection-password").fill("visitor-private-value")
  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    "visitor-private-value",
  )
  await gotoStep(page, "review")
  await expect(page.getByRole("button", { name: "Deploy", exact: true })).toBeEnabled()
  const checked = checks
  await page.waitForTimeout(300)
  expect(checks).toBe(checked)
  await page.reload()
  await gotoStep(page, "runtime")
  await expect(page.getByRole("textbox", { name: "User name" })).toHaveValue("client")
  await expect(page.locator("#public-protection-password")).toHaveValue("")
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(journey.configuration()).toMatchObject({
    domains: [{ protection: { username: "client", hash: "fixture-sealed-password" } }],
  })
})

test("an explicitly supplied secret overrides its generator and removing it restores the default", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new?source=template")
  await page.getByRole("button", { name: "Use PostgreSQL", exact: true }).click()
  await page.getByRole("button", { name: "Use this template", exact: true }).click()
  await page.getByText("Paste .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables", exact: true })
    .fill("POSTGRES_PASSWORD=explicit-private-value")
  await gotoStep(page, "review")
  await expect.poll(() => journey.environmentKeys()).toEqual(["POSTGRES_PASSWORD"])
  const variables = journey.configuration()?.variables as Array<Record<string, unknown>>
  expect(variables.find((variable) => variable.name === "POSTGRES_PASSWORD")).toMatchObject({
    sensitivity: "secret",
    scopes: ["runtime"],
  })
  expect(variables.find((variable) => variable.name === "POSTGRES_PASSWORD")?.generate).toBe(40)
  await expect(page.getByRole("heading", { name: "Secrets made on this server" })).toHaveCount(0)
  await page.reload()
  await gotoStep(page, "variables")
  await expect(
    page.getByRole("button", { name: "Remove saved variable POSTGRES_PASSWORD" }),
  ).toBeVisible()
  await expect(page.getByRole("textbox", { name: "Variable POSTGRES_PASSWORD value" })).toHaveValue(
    "Supplied value overrides the 40-character default",
  )
  await gotoStep(page, "review")
  await expect.poll(() => journey.environmentKeys()).toEqual(["POSTGRES_PASSWORD"])
  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    "explicit-private-value",
  )
  await gotoStep(page, "variables")
  await page.getByRole("button", { name: "Remove saved variable POSTGRES_PASSWORD" }).click()
  await gotoStep(page, "review")
  await expect.poll(() => journey.environmentKeys()).toEqual([])
  await expect(page.getByRole("heading", { name: "Secrets made on this server" })).toBeVisible()
})

test("an explicit variable reference replaces a retained value and a pasted conflict is explained", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "variables")
  await page.getByText("Paste .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables", exact: true })
    .fill("API_TOKEN=old-private-value")
  await gotoStep(page, "review")
  await expect.poll(() => journey.environmentKeys()).toEqual(["API_TOKEN"])
  await page.reload()
  await gotoStep(page, "variables")
  await page.getByRole("button", { name: /Variable references & scopes/ }).click()
  const reference = page.getByRole("textbox", { name: "Variable API_TOKEN reference" })
  await reference.fill("${{credential.api}}")
  await expect(page.getByRole("button", { name: "Remove saved variable API_TOKEN" })).toHaveCount(0)
  await gotoStep(page, "review")
  await expect.poll(() => journey.environmentKeys()).toEqual([])
  expect(journey.configuration()).toMatchObject({
    variables: [{ name: "API_TOKEN", reference: "${{credential.api}}" }],
  })
  await gotoStep(page, "variables")
  await page.getByText("Paste .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables", exact: true })
    .fill("API_TOKEN=conflicting-private-value")
  await page.getByRole("button", { name: /Variable references & scopes/ }).click()
  await reference.fill("${{credential.replacement}}")
  await expect(
    page.getByText(
      "Remove API_TOKEN from the pasted .env before changing its reference or generator.",
    ),
  ).toBeVisible()
  await expect(reference).toHaveValue("${{credential.api}}")
})
