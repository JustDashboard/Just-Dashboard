import { expect, test, type Page } from "@playwright/test"
import {
  blueprintCatalogue,
  deployment,
  json,
  mockNewProject,
  mockProject,
  now,
  project,
  postgresBlueprint,
} from "./deploy-fixture"

/**
 * Templates added on this dashboard: the picker's Your templates beside the
 * reviewed catalogue, Save as template on a deployment's General settings,
 * and the Templates page that lists, imports, exports and removes them. The
 * server is mocked; what is held here is what each surface says and what it
 * sends.
 */

/** A template added here, as the catalogue lists it. */
const shopSummary = {
  id: "user-shop-front",
  version: "1.0.0",
  name: "Shop front",
  category: "http",
  profile: "web",
  description: "Shop front, the image nginx:1.27-alpine, as a template added on this dashboard.",
  iconId: "globe",
  docsUrl: "https://hub.docker.com/_/nginx",
  license: "Not stated",
  maintainer: "operator",
  reviewedAt: "2026-10-29",
  access: { kind: "open", note: "This template was added on this dashboard." },
  image: "nginx:1.27-alpine",
  memoryMb: 256,
  sourceKind: "blueprint",
  origin: "user",
  deploymentSupported: true,
}

const shopDefinition = {
  ...shopSummary,
  image: { reference: "nginx:1.27-alpine", tagPolicy: "pinned", pullPolicy: "missing" },
  provenance: {
    maintainer: "operator",
    license: "Not stated",
    upstreamUrl: "https://hub.docker.com/_/nginx",
    reviewedAt: "2026-10-29",
    minimumDashboard: "0.8.2",
  },
  inputs: [
    { name: "domain", kind: "domain", label: "Public domain", required: true },
    { name: "tz", kind: "text", label: "TZ", variable: "TZ", default: "Europe/Paris" },
    { name: "db-password", kind: "secret", label: "DB_PASSWORD", variable: "DB_PASSWORD" },
  ],
  ports: [{ name: "http", internal: 80, protocol: "tcp", purpose: "Answers", exposure: "proxy" }],
  resources: { memoryMb: 256, minMemoryMb: 128 },
  update: { detector: "registry" },
}

const shopTemplate = {
  id: "user-shop-front",
  name: "Shop front",
  version: "1.0.0",
  source: "deployment",
  fromDeploymentId: 7,
  summary: shopSummary,
  deployments: 2,
  createdBy: "operator",
  createdAt: now,
  updatedBy: "operator",
  updatedAt: now,
}

const stackTemplate = {
  ...shopTemplate,
  id: "user-cached-site",
  name: "Cached site",
  source: "compose",
  fromDeploymentId: undefined,
  deployments: 0,
  summary: {
    ...shopSummary,
    id: "user-cached-site",
    name: "Cached site",
    sourceKind: "compose",
    services: [
      { name: "cache", role: "cache", purpose: "Cache", image: "redis:7.4-alpine", memoryMb: 256 },
      {
        name: "web",
        role: "app",
        purpose: "Web",
        image: "nginx:1.27-alpine",
        memoryMb: 512,
        public: true,
      },
    ],
  },
}

test("the picker has a Your templates section that says how to make one", async ({ page }) => {
  await mockNewProject(page)
  await page.goto("/deploy/new?source=template")
  const section = page.getByRole("region", { name: "Your templates" })
  await expect(section).toBeVisible()
  await expect(
    section.getByText(/Save a running deployment as one from its Settings/),
  ).toBeVisible()
  await expect(section.getByText(/add one from a Compose file on the Templates page/)).toBeVisible()
  await expect(section.getByRole("link", { name: "Open Templates" })).toHaveAttribute(
    "href",
    "/deploy/templates",
  )
  // The reviewed shelves are where they were, under it.
  await expect(page.getByRole("group", { name: "Databases" })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("picker-empty-yours.png") })
})

test("a template added here is chosen from Your templates as a built-in is", async ({ page }) => {
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/blueprints/", (route) =>
    json(route, [...blueprintCatalogue, shopSummary]),
  )
  await page.route("**/api/v1/deploy/blueprints/user-shop-front", (route) =>
    json(route, shopDefinition),
  )
  const sources: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/deploy/drafts/journey-draft", (route) => {
    const body = route.request().postDataJSON() as { source?: Record<string, unknown> } | null
    if (route.request().method() === "PUT" && body?.source) sources.push(body.source)
    return route.fallback()
  })
  await page.goto("/deploy/new?source=template")

  const yours = page.getByRole("group", { name: "Your templates" })
  await expect(yours.getByRole("button", { name: "Use Shop front", exact: true })).toBeVisible()
  // It is in its own section and on no reviewed shelf.
  await expect(
    page
      .getByRole("group", { name: "Web & files" })
      .getByRole("button", { name: "Use Shop front" }),
  ).toHaveCount(0)
  await page.getByRole("group", { name: "Template topics" }).getByText("Yours").click()
  await expect(page.getByRole("group", { name: "Databases" })).toHaveCount(0)
  await yours.getByRole("button", { name: "Use Shop front", exact: true }).click()

  const settings = page.getByRole("region", { name: "Selected template settings" })
  await expect(settings.getByText("Added 2026-10-29 by operator on this dashboard.")).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("picker-yours.png") })
  await page.getByRole("button", { name: "Use this template", exact: true }).click()
  await expect.poll(() => sources.length).toBeGreaterThan(0)
  expect(sources[0]).toMatchObject({
    kind: "blueprint",
    mode: "blueprint",
    blueprintId: "user-shop-front",
    blueprintVersion: "1.0.0",
  })
  // The browser names the template; the server freezes its definition in.
  expect(sources[0].blueprintDefinition).toBeUndefined()
})

async function imageDeployment(page: Page) {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: {
        ...deployment,
        sourceKind: "image",
        buildMethod: "image",
        activeRun: undefined,
        recentRuns: [],
      },
    }),
  )
}

test("Save as template says what the template will ask before it is saved", async ({ page }) => {
  await imageDeployment(page)
  const bodies: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/deploy/templates/from-deployment", (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>
    bodies.push(body)
    if (body.dryRun) {
      return json(route, {
        definition: { ...shopDefinition, id: "user-api-production" },
        summary: shopSummary,
        exists: false,
      })
    }
    return route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({ ...shopTemplate, definition: shopDefinition }),
    })
  })
  await page.goto("/deploy/7/settings/general")
  await page.getByRole("button", { name: "Save as template…" }).click()

  const dialog = page.getByRole("dialog", { name: "Save as template" })
  await expect(dialog.getByLabel("Name")).toHaveValue("api-production")
  await expect(dialog.getByText("Saved as user-api-production")).toBeVisible()
  const asks = dialog.getByLabel("What the template asks")
  await expect(asks.getByText("nginx:1.27-alpine")).toBeVisible()
  await expect(asks.getByText("Asked on the Variables screen")).toBeVisible()
  await expect(asks.getByText("DB_PASSWORD")).toBeVisible()
  await expect(asks.getByText("— defaults to Europe/Paris")).toBeVisible()
  await expect(asks.getByText(/No value of this server's is kept/)).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("save-as-template.png") })

  await dialog.getByLabel("Name").fill("Shop front")
  await expect(dialog.getByText("Saved as user-shop-front")).toBeVisible()
  await dialog.getByRole("button", { name: "Save template" }).click()
  await expect(page.getByText("Shop front saved as a template")).toBeVisible()
  await expect(dialog).toHaveCount(0)
  expect(bodies[0]).toMatchObject({ deploymentId: 7, environmentId: 12, dryRun: true })
  expect(bodies.at(-1)).toEqual({ deploymentId: 7, environmentId: 12, name: "Shop front" })
})

test("Save as template says why a deployment cannot be one", async ({ page }) => {
  await imageDeployment(page)
  await page.route("**/api/v1/deploy/templates/from-deployment", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "no_live_release",
          message:
            "this environment has not gone live yet; a template is saved from the plan a running release was deployed with",
        },
      }),
    }),
  )
  await page.goto("/deploy/7/settings/general")
  await page.getByRole("button", { name: "Save as template…" }).click()
  const dialog = page.getByRole("dialog", { name: "Save as template" })
  await expect(dialog.getByText("This cannot be saved as a template")).toBeVisible()
  await expect(dialog.getByText(/has not gone live yet/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Save template" })).toBeDisabled()
})

test("the Templates page lists, imports and removes this dashboard's templates", async ({
  page,
}) => {
  await mockProject(page)
  let listed = [shopTemplate, stackTemplate]
  const deleted: string[] = []
  const imported: unknown[] = []
  await page.route("**/api/v1/deploy/templates", (route) => json(route, listed))
  await page.route("**/api/v1/deploy/templates/import", (route) => {
    imported.push(route.request().postDataJSON())
    return route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        ...shopTemplate,
        id: "user-postgresql",
        name: "PostgreSQL",
        source: "import",
        definition: postgresBlueprint,
      }),
    })
  })
  await page.route("**/api/v1/deploy/templates/user-shop-front", (route) => {
    if (route.request().method() !== "DELETE") return route.fallback()
    deleted.push("user-shop-front")
    listed = [stackTemplate]
    return route.fulfill({ status: 204 })
  })
  await page.goto("/deploy/templates")

  const rows = page.getByRole("list", { name: "Your templates" })
  await expect(rows.getByRole("listitem")).toHaveCount(2)
  const shop = rows.getByRole("listitem").filter({ hasText: "Shop front" })
  await expect(
    shop.getByText(/user-shop-front · v1\.0\.0 · Saved from a deployment by operator/),
  ).toBeVisible()
  await expect(shop.getByText("2 deployments use it")).toBeVisible()
  await expect(shop.getByRole("link", { name: "Deploy Shop front" })).toHaveAttribute(
    "href",
    "/deploy/new?source=template&template=user-shop-front",
  )
  const stack = rows.getByRole("listitem").filter({ hasText: "Cached site" })
  await expect(stack.getByText("2 services: cache, web")).toBeVisible()
  await expect(stack.getByText("Compose stack")).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("templates-page.png") })

  await page.getByTestId("template-import-file").setInputFiles({
    name: "postgresql.json",
    mimeType: "application/json",
    buffer: Buffer.from(JSON.stringify(postgresBlueprint)),
  })
  await expect(page.getByText("PostgreSQL imported")).toBeVisible()
  expect(imported[0]).toMatchObject({ id: postgresBlueprint.id })

  await page.getByRole("button", { name: "Actions for Shop front" }).click()
  await page.getByRole("menuitem", { name: "Remove template" }).click()
  const confirmation = page.getByRole("dialog", { name: "Remove Shop front" })
  await expect(confirmation.getByText(/2 deployments use it, and each keeps running/)).toBeVisible()
  await confirmation.getByRole("button", { name: "Remove template" }).click()
  await expect.poll(() => deleted).toEqual(["user-shop-front"])
  await expect(rows.getByRole("listitem")).toHaveCount(1)
})

test("an empty Templates page offers the three ways to add one", async ({ page }) => {
  await mockProject(page)
  const previews: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/deploy/templates", (route) => {
    if (route.request().method() !== "POST") return json(route, [])
    previews.push(route.request().postDataJSON() as Record<string, unknown>)
    return route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_template",
          message:
            'template is invalid: service "web" builds its image; a template runs published images',
        },
      }),
    })
  })
  await page.goto("/deploy/templates")
  await expect(
    page.getByRole("button", { name: "Add a template from a Compose file" }),
  ).toBeVisible()
  await expect(page.getByRole("link", { name: "Save a deployment as a template" })).toHaveAttribute(
    "href",
    "/deploy",
  )
  await expect(page.getByRole("button", { name: "Import a template file" })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("templates-empty.png") })

  await page.getByRole("button", { name: "Add a template from a Compose file" }).click()
  const sheet = page.getByRole("dialog", { name: "Add from Compose file" })
  await sheet.getByLabel("Name").fill("Built here")
  await expect(sheet.getByText("Saved as user-built-here")).toBeVisible()
  await sheet.getByLabel("Compose file").fill("services:\n  web:\n    build: .\n")
  await sheet.getByRole("button", { name: "Check" }).click()
  await expect(sheet.getByText("The catalogue refuses this file")).toBeVisible()
  await expect(sheet.getByText(/builds its image; a template runs published images/)).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("compose-sheet.png") })
  expect(previews[0]).toMatchObject({ name: "Built here", dryRun: true })
})
