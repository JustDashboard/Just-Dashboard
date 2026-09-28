import { expect, test, type Page } from "@playwright/test"
import { deployment, json, mockProject, now, project } from "./deploy-fixture"

/**
 * Settings → General's "Source" form: changing a committed project's
 * repository or image in place (`PUT /deploy/{id}/environments/{env}/source`).
 *
 * The shared fixture's configuration read has no `source`/`identity` yet —
 * the backend is gaining them alongside this card — so every test answers
 * `.../configuration` itself rather than relying on `mockProject`'s own
 * bookkeeping for that endpoint. The credential picker's own list
 * (`GET /deploy/credentials`) is likewise not part of the shared fixture and
 * is stubbed per test.
 *
 * The duplicate-project dialog built alongside this card is exercised in
 * `deploy-project.spec.ts`, from the project shell's verb menu it is wired
 * into — not here, which is scoped to the Source card itself.
 */

function overflow(page: Page) {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
}

function sourceCard(page: Page) {
  return page.getByRole("form", { name: "Source" })
}

const baseConfiguration = {
  build: { method: "recipe", recipe: "node" },
  runtime: {
    image: "",
    command: ["bun", "start"],
    internalPort: 3000,
    hostPort: 0,
    bindAddress: "127.0.0.1",
    strategy: "blue_green",
    mounts: [],
  },
  dependencies: [],
  checks: [],
  domains: [{ hostname: "api.example.test", https: true, ownership: "managed" }],
  revision: 3,
  variables: [],
  pending: { pending: false, desiredRevision: 3, changes: [] },
}

const credentials = [
  {
    id: 5,
    name: "GitHub PAT",
    kind: "git_bearer",
    target: "github.com",
    createdAt: now,
    updatedAt: now,
    usedBy: 1,
  },
  {
    id: 9,
    name: "Registry login",
    kind: "registry",
    target: "ghcr.io",
    username: "deploy",
    createdAt: now,
    updatedAt: now,
    usedBy: 0,
  },
]

async function mockConfiguration(page: Page, extra: Record<string, unknown> = {}) {
  await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, { ...baseConfiguration, ...extra })
  })
}

async function mockCredentials(page: Page) {
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, credentials)
  })
}

test.describe("Source setting card", () => {
  test("prefills a saved Git source, including its credential, and saves an edit", async ({
    page,
  }) => {
    await mockProject(page)
    await mockCredentials(page)
    await mockConfiguration(page, {
      source: {
        kind: "git",
        mode: "git_url",
        url: "https://github.com/acme/api.git",
        ref: "main",
        subdirectory: "apps/api",
        credentialId: 5,
        includeSubmodules: false,
        includeLfs: true,
      },
    })
    const puts: Record<string, unknown>[] = []
    await page.route("**/api/v1/deploy/7/environments/12/source", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      puts.push(route.request().postDataJSON() as Record<string, unknown>)
      await json(route, { ...baseConfiguration, source: route.request().postDataJSON() })
    })

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await expect(card.getByLabel("Repository URL")).toHaveValue("https://github.com/acme/api.git")
    await expect(card.getByLabel("Branch or tag")).toHaveValue("main")
    await expect(card.getByLabel("Root directory")).toHaveValue("apps/api")
    // Radix keeps a hidden native <option> per item for form semantics, so a
    // bare text search also matches that; the combobox's own displayed value
    // is the one that is actually on screen.
    await expect(card.getByRole("combobox", { name: "Credential" })).toHaveText("GitHub PAT")
    await expect(card.getByLabel("Include Git LFS objects")).toBeChecked()
    await expect(card.getByLabel("Include Git submodules")).not.toBeChecked()
    // The head says how the source is reached, beside the repository's link.
    await expect(card.getByText("Git URL", { exact: true })).toBeVisible()

    await page.setViewportSize({ width: 1280, height: 900 })
    await page.screenshot({ path: test.info().outputPath("source-1280.png"), fullPage: true })

    await card.getByLabel("Branch or tag").fill("release/1.0")
    await card.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Source updated")).toBeVisible()

    expect(puts.at(-1)).toEqual({
      revision: 3,
      kind: "git",
      mode: "git_url",
      url: "https://github.com/acme/api.git",
      ref: "release/1.0",
      subdirectory: "apps/api",
      credentialId: 5,
      includeSubmodules: false,
      includeLfs: true,
    })

    await page.setViewportSize({ width: 390, height: 844 })
    expect(await overflow(page)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: test.info().outputPath("source-390.png"), fullPage: true })
  })

  test("derives the URL for a connected GitHub repository instead of asking again", async ({
    page,
  }) => {
    await mockProject(page)
    await mockCredentials(page)
    // A repository connected through GitHub carries owner/name, not a bare
    // URL — but the plain https URL is exactly reproducible, so this is the
    // one connected shape that does not need to ask again.
    await mockConfiguration(page, {
      source: {
        kind: "git",
        mode: "connected_repository",
        provider: "github",
        repository: "acme/api",
        ref: "main",
      },
    })

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await expect(card.getByLabel("Repository URL")).toHaveValue("https://github.com/acme/api")
    await expect(card.getByText("Enter the repository again.")).toHaveCount(0)
    await expect(card.getByLabel("Branch or tag")).toHaveValue("main")
    // The App's installation is what reads a connected repository, so there is
    // no token or key to pick until the URL is changed.
    await expect(card.getByText(/Read through the GitHub App/)).toBeVisible()
    await expect(card.getByRole("combobox", { name: "Credential" })).toHaveCount(0)
    await card.getByLabel("Repository URL").fill("https://gitlab.com/acme/mirror.git")
    await expect(card.getByRole("combobox", { name: "Credential" })).toBeVisible()
  })

  test("asks for the repository again for a connected provider it cannot derive a URL for", async ({
    page,
  }) => {
    await mockProject(page)
    await mockCredentials(page)
    await mockConfiguration(page, {
      source: {
        kind: "git",
        mode: "connected_repository",
        provider: "gitlab",
        repository: "acme/api",
        ref: "main",
      },
    })

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await expect(card.getByLabel("Repository URL")).toHaveValue("")
    await expect(card.getByText("Enter the repository again.")).toBeVisible()
  })

  test("keeps the connected repository shape when the derived URL is left unedited", async ({
    page,
  }) => {
    await mockProject(page)
    await mockCredentials(page)
    await mockConfiguration(page, {
      source: {
        kind: "git",
        mode: "connected_repository",
        provider: "github",
        repository: "acme/api",
        ref: "main",
      },
    })
    const puts: Record<string, unknown>[] = []
    await page.route("**/api/v1/deploy/7/environments/12/source", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      puts.push(route.request().postDataJSON() as Record<string, unknown>)
      await json(route, { ...baseConfiguration, source: route.request().postDataJSON() })
    })

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    // Editing something else on the card must not itself convert a connected
    // repository into a bare URL — only editing the URL field does that.
    await card.getByLabel("Branch or tag").fill("release/2.0")
    await card.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Source updated")).toBeVisible()

    expect(puts.at(-1)).toEqual({
      revision: 3,
      kind: "git",
      mode: "connected_repository",
      provider: "github",
      repository: "acme/api",
      ref: "release/2.0",
      includeSubmodules: false,
      includeLfs: false,
    })
  })

  test("switches to a plain URL once the operator types a different one", async ({ page }) => {
    await mockProject(page)
    await mockCredentials(page)
    await mockConfiguration(page, {
      source: {
        kind: "git",
        mode: "connected_repository",
        provider: "github",
        repository: "acme/api",
        ref: "main",
      },
    })
    const puts: Record<string, unknown>[] = []
    await page.route("**/api/v1/deploy/7/environments/12/source", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      puts.push(route.request().postDataJSON() as Record<string, unknown>)
      await json(route, { ...baseConfiguration, source: route.request().postDataJSON() })
    })

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await card.getByLabel("Repository URL").fill("https://gitlab.com/acme/mirror.git")
    await card.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Source updated")).toBeVisible()

    const body = puts.at(-1)
    expect(body).toMatchObject({ mode: "git_url", url: "https://gitlab.com/acme/mirror.git" })
    expect(body).not.toHaveProperty("provider")
    expect(body).not.toHaveProperty("repository")
  })

  test("falls back to the fleet summary when the backend has not shipped a source yet", async ({
    page,
  }) => {
    await mockProject(page)
    await mockCredentials(page)
    await mockConfiguration(page)

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await expect(card.getByLabel("Repository URL")).toHaveValue("")
    await expect(card.getByText("Enter the repository again.")).toBeVisible()
    // deployment.sourceRef from the fixture stands in for the branch.
    await expect(card.getByLabel("Branch or tag")).toHaveValue(deployment.sourceRef)
  })

  test("shows an unresolved remote as the card's own line, not a toast", async ({ page }) => {
    await mockProject(page)
    await mockCredentials(page)
    await mockConfiguration(page, {
      source: { kind: "git", mode: "git_url", url: "https://github.com/acme/api.git", ref: "main" },
    })
    await page.route("**/api/v1/deploy/7/environments/12/source", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      await route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_source",
            message: "The branch could not be found on the remote.",
          },
        }),
      })
    })

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await card.getByRole("button", { name: "Save" }).click()
    await expect(card.getByText("The branch could not be found on the remote.")).toBeVisible()
    await expect(page.getByText("Could not update source")).toHaveCount(0)
  })

  test("routes a field refusal to the credential control instead of a toast", async ({ page }) => {
    await mockProject(page)
    await mockCredentials(page)
    await mockConfiguration(page, {
      source: { kind: "git", mode: "git_url", url: "https://github.com/acme/api.git", ref: "main" },
    })
    await page.route("**/api/v1/deploy/7/environments/12/source", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      await route.fulfill({
        status: 400,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "invalid_source", message: "Unknown credential.", field: "credentialId" },
        }),
      })
    })

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await card.getByRole("button", { name: "Save" }).click()
    await expect(card.getByText("Unknown credential.")).toBeVisible()
    await expect(page.getByText("Could not update source")).toHaveCount(0)
  })

  test("shows the image fields, with a registry credential, for an image-sourced project", async ({
    page,
  }) => {
    await mockProject(page)
    await mockCredentials(page)
    await page.route("**/api/v1/deploy/7", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      await json(route, {
        project,
        running: false,
        deployment: {
          ...deployment,
          sourceKind: "image",
          sourceRef: "ghcr.io/acme/api:1.4.0",
          buildMethod: "image",
          activeRun: undefined,
        },
      })
    })
    await mockConfiguration(page, {
      source: {
        kind: "image",
        mode: "image_reference",
        image: "ghcr.io/acme/api:1.4.0",
        credentialId: 9,
      },
    })

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await expect(card.getByLabel("Image reference")).toHaveValue("ghcr.io/acme/api:1.4.0")
    await expect(card.getByLabel("Platform")).toBeVisible()
    await expect(card.getByRole("combobox", { name: "Credential" })).toHaveText("Registry login")
    await expect(card.getByLabel("Repository URL")).toHaveCount(0)
    await expect(
      card.getByText(
        "A project keeps its source kind. Start a new project to move from an image to a repository.",
      ),
    ).toBeVisible()
  })

  test("a read-only role sees the source but no Save button", async ({ page }) => {
    await mockProject(page)
    await mockCredentials(page)
    await mockConfiguration(page, {
      source: { kind: "git", mode: "git_url", url: "https://github.com/acme/api.git", ref: "main" },
    })
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read"],
      }),
    )

    await page.goto("/deploy/7/settings/general")
    const card = sourceCard(page)
    await expect(card.getByLabel("Repository URL")).toHaveValue("https://github.com/acme/api.git")
    await expect(card.getByRole("button", { name: "Save" })).toHaveCount(0)
  })
})
