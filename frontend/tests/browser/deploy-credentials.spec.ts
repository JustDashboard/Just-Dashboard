import { expect, test, type Page } from "@playwright/test"
import { json, mockProject, now } from "./deploy-fixture"

/**
 * The fleet-level Credentials page (`/deploy/credentials`): a page of its
 * own rather than a project tab, since one token or key is shared by every
 * project that references it. None of its routes exist in the shared
 * fixture yet, so every test stubs `/deploy/credentials` itself.
 */

function credentialRow(page: Page, name: string) {
  return page.getByRole("list", { name: "Credentials" }).locator("li", { hasText: name })
}

const githubToken = {
  id: 5,
  name: "GitHub PAT",
  kind: "git_bearer",
  target: "github.com",
  createdAt: now,
  updatedAt: now,
  lastUsedAt: undefined,
  usedBy: 0,
}

const registryLogin = {
  id: 9,
  name: "Registry login",
  kind: "registry",
  target: "ghcr.io",
  username: "deploy",
  createdAt: now,
  updatedAt: "2026-09-03T09:00:00Z",
  lastUsedAt: "2026-09-03T09:00:00Z",
  usedBy: 2,
}

const providerToken = {
  id: 11,
  name: "GitHub API",
  kind: "provider_token",
  target: "",
  createdAt: now,
  updatedAt: now,
  lastUsedAt: undefined,
  usedBy: 0,
}

test("lists credentials with their kind, target and usage under three readings", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [githubToken, registryLogin])
  })
  await page.route("**/api/v1/deploy/github-app/", (route) =>
    json(route, { configured: false, installations: [] }),
  )

  await page.goto("/deploy/credentials")
  await expect(page.getByRole("heading", { name: "Credentials", exact: true })).toBeVisible()

  // The readings: how many are held, how many a project's source points at,
  // and whether the App is connected.
  const tiles = page.locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(3)
  await expect(tiles.nth(0)).toContainText("2")
  await expect(tiles.nth(1)).toContainText("1")
  await expect(tiles.nth(2)).toContainText("Not connected")

  const github = credentialRow(page, "GitHub PAT")
  await expect(github.getByText("Git token", { exact: true })).toBeVisible()
  await expect(github.getByText("github.com")).toBeVisible()
  await expect(github.getByText("not in use")).toBeVisible()
  await expect(github.getByText("never used")).toBeVisible()

  const registry = credentialRow(page, "Registry login")
  await expect(registry.getByText("Registry", { exact: true })).toBeVisible()
  await expect(registry.getByText("ghcr.io · deploy")).toBeVisible()
  await expect(registry.getByText("used by 2 projects")).toBeVisible()
  await expect(registry.getByText(/last used/)).toBeVisible()

  await page.setViewportSize({ width: 1280, height: 900 })
  await page.screenshot({ path: test.info().outputPath("credentials-1280.png"), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
  await page.screenshot({ path: test.info().outputPath("credentials-390.png"), fullPage: true })
})

test("the SSH key secret is a PEM textarea; every other kind is a password input", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [])
  })

  await page.goto("/deploy/credentials")
  await page.getByRole("button", { name: "Add credential" }).click()

  // Git token over HTTPS is the default selection: a plain password field.
  await expect(page.locator("input#credential-secret")).toHaveCount(1)
  await expect(page.locator("textarea#credential-secret")).toHaveCount(0)

  // Kind is a set of choice cards: each carries its own sentence in its
  // accessible name, so the match is a prefix.
  await page.getByRole("button", { name: /^SSH key/ }).click()
  await expect(page.locator("textarea#credential-secret")).toHaveCount(1)
  await expect(page.locator("input#credential-secret")).toHaveCount(0)
  await expect(
    page.getByText(
      "A private key in PEM form; the public half goes in the provider's deploy keys.",
    ),
  ).toBeVisible()

  await page.getByRole("button", { name: /^Registry login/ }).click()
  // Exact: the "Registry login" card's own hint text contains the word
  // "username" too, so a substring match would also catch that card.
  await expect(page.getByLabel("Username", { exact: true })).toBeVisible()
  await expect(page.locator("input#credential-secret")).toHaveCount(1)
})

test("a pasted URL or SSH remote is saved as a bare host", async ({ page }) => {
  await mockProject(page)
  const posts: Record<string, unknown>[] = []
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() === "POST") {
      posts.push(route.request().postDataJSON() as Record<string, unknown>)
      return json(route, { ...githubToken, id: 6, name: "Pasted" })
    }
    await json(route, [])
  })

  await page.goto("/deploy/credentials")
  await page.getByRole("button", { name: "Add credential" }).click()
  const dialog = page.getByRole("dialog")
  const submit = dialog.getByRole("button", { name: "Add credential" })
  // Nothing to seal yet: the button waits rather than bouncing off a 400.
  await expect(submit).toBeDisabled()
  await page.locator("#credential-name").fill("Pasted")
  await page.locator("#credential-target").fill("https://github.com/acme/app.git")
  await page.locator("#credential-secret").fill("ghp_token")
  await submit.click()
  await expect(page.getByText("Credential added")).toBeVisible()
  expect(posts.at(-1)?.target).toBe("github.com")

  await page.getByRole("button", { name: "Add credential" }).click()
  await page.getByRole("button", { name: /^SSH key/ }).click()
  await page.locator("#credential-name").fill("Pasted")
  await page.locator("#credential-target").fill("git@gitlab.com:acme/app.git")
  await page.locator("#credential-secret").fill("-----BEGIN OPENSSH PRIVATE KEY-----")
  await submit.click()
  await expect.poll(() => posts.length).toBe(2)
  expect(posts.at(-1)?.target).toBe("gitlab.com")
})

test("a shape refusal from the server is shown under the form, not as a toast", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() === "POST") {
      return route.fulfill({
        status: 400,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_credential",
            message:
              "invalid deployment credential: bearer token is empty, too long or contains unsupported characters",
          },
        }),
      })
    }
    await json(route, [])
  })

  await page.goto("/deploy/credentials")
  await page.getByRole("button", { name: "Add credential" }).click()
  const dialog = page.getByRole("dialog")
  await page.locator("#credential-name").fill("Bad")
  await page.locator("#credential-secret").fill("has spaces")
  await dialog.getByRole("button", { name: "Add credential" }).click()
  await expect(dialog.getByRole("alert")).toContainText("unsupported characters")
  // The panel stays open with what was typed, so the token can be corrected.
  await expect(page.locator("#credential-name")).toHaveValue("Bad")
})

test("editing without retyping the secret omits it from the save", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [githubToken])
  })
  const puts: Record<string, unknown>[] = []
  await page.route("**/api/v1/deploy/credentials/5", async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    puts.push(route.request().postDataJSON() as Record<string, unknown>)
    await json(route, { ...githubToken, name: "GitHub PAT (renamed)" })
  })

  await page.goto("/deploy/credentials")
  await credentialRow(page, "GitHub PAT")
    .getByRole("button", { name: "Actions for GitHub PAT" })
    .click()
  await page.getByRole("menuitem", { name: "Edit credential" }).click()

  // The kind is fixed once created: a fact under the title, no picker.
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("button", { name: /^SSH key/ })).toHaveCount(0)
  await expect(page.getByText("Leave empty to keep the stored secret.")).toBeVisible()
  await page.getByLabel("Name", { exact: true }).fill("GitHub PAT (renamed)")
  await page.getByRole("button", { name: "Save credential" }).click()
  await expect(page.getByText("Credential saved")).toBeVisible()

  expect(puts.at(-1)?.secret).toBeUndefined()
  expect(puts.at(-1)?.name).toBe("GitHub PAT (renamed)")
})

test("Test asks every kind for something to try and shows the result inline", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [githubToken, registryLogin, providerToken])
  })
  const probes: Record<string, unknown>[] = []
  await page.route("**/api/v1/deploy/credentials/*/test", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    probes.push(route.request().postDataJSON() as Record<string, unknown>)
    await json(route, { ok: true, message: "Connected as deploy-bot" })
  })

  await page.goto("/deploy/credentials")
  await credentialRow(page, "GitHub PAT").getByRole("button", { name: "Test" }).click()
  // Scoped to the dialog: its own footer button shares the row icon's name.
  const dialog = page.getByRole("dialog", { name: "Test GitHub PAT" })
  await expect(dialog).toBeVisible()
  const run = dialog.getByRole("button", { name: "Test", exact: true })
  // The server refuses a probe with nothing to read, so the button waits.
  await expect(run).toBeDisabled()
  await expect(dialog.getByText("owner/name on github.com, or a full Git URL.")).toBeVisible()
  await dialog.locator("#credential-test-subject").fill("acme/app")
  await run.click()
  await expect(dialog.getByText("Connected as deploy-bot")).toBeVisible()
  expect(probes.at(-1)?.repository).toBe("acme/app")
  await dialog.getByRole("button", { name: "Close" }).first().click()

  // A registry login is tried against an image on its own host — the
  // dialog used to send nothing at all for this kind, which the server
  // always refused.
  await credentialRow(page, "Registry login").getByRole("button", { name: "Test" }).click()
  const registry = page.getByRole("dialog", { name: "Test Registry login" })
  await expect(registry.getByLabel("Image")).toBeVisible()
  await expect(registry.getByText("An image on ghcr.io to resolve with this login.")).toBeVisible()
  await registry.locator("#credential-test-subject").fill("ghcr.io/acme/app:latest")
  await registry.getByRole("button", { name: "Test", exact: true }).click()
  await expect(registry.getByText("Connected as deploy-bot")).toBeVisible()
  expect(probes.at(-1)?.repository).toBe("ghcr.io/acme/app:latest")
  await registry.getByRole("button", { name: "Close" }).first().click()

  // A provider token without a saved host needs the whole URL.
  await credentialRow(page, "GitHub API").getByRole("button", { name: "Test" }).click()
  const provider = page.getByRole("dialog", { name: "Test GitHub API" })
  await expect(
    provider.getByText("A full Git URL — this credential has no saved host."),
  ).toBeVisible()
})

test("Test shows a probe the server refused as a field error", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [githubToken])
  })
  await page.route("**/api/v1/deploy/credentials/5/test", async (route) => {
    await route.fulfill({
      status: 400,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_credential",
          message:
            "invalid deployment credential: repository must be a Git URL or an owner/name path",
        },
      }),
    })
  })

  await page.goto("/deploy/credentials")
  await credentialRow(page, "GitHub PAT").getByRole("button", { name: "Test" }).click()
  const dialog = page.getByRole("dialog", { name: "Test GitHub PAT" })
  await dialog.locator("#credential-test-subject").fill("bad path")
  await dialog.getByRole("button", { name: "Test", exact: true }).click()
  await expect(dialog.getByRole("alert")).toContainText("owner/name path")
})

test("removing a credential still in use shows the server's reason once", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [registryLogin])
  })
  await page.route("**/api/v1/deploy/credentials/9", async (route) => {
    if (route.request().method() !== "DELETE") return route.fallback()
    await route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "credential_in_use", message: "Used by 2 projects' current source." },
      }),
    })
  })

  await page.goto("/deploy/credentials")
  await credentialRow(page, "Registry login")
    .getByRole("button", { name: "Actions for Registry login" })
    .click()
  await page.getByRole("menuitem", { name: "Remove credential" }).click()
  await page.getByRole("button", { name: "Remove credential" }).click()

  // No special-cased toast for this refusal: the confirm dialog's own
  // failure toast already names the server's message, which for
  // `credential_in_use` names the projects, so it is shown exactly once.
  await expect(page.getByText("Remove Registry login failed")).toBeVisible()
  await expect(page.getByText("Used by 2 projects' current source.")).toHaveCount(1)
})

test("a role without system.admin sees no add action and no row controls", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [githubToken])
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

  await page.goto("/deploy/credentials")
  await expect(page.getByText("GitHub PAT")).toBeVisible()
  await expect(page.getByRole("button", { name: "Add credential" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Test" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
})
