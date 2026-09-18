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

test("lists credentials with their kind, target and usage", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [githubToken, registryLogin])
  })

  await page.goto("/deploy/credentials")
  await expect(page.getByRole("heading", { name: "Credentials", exact: true })).toBeVisible()

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

  // Kind is a set of OptionRow switches standing in for radios: each carries
  // its own sentence in its accessible name, so the match is a prefix.
  await page.getByRole("switch", { name: /^SSH key/ }).click()
  await expect(page.locator("textarea#credential-secret")).toHaveCount(1)
  await expect(page.locator("input#credential-secret")).toHaveCount(0)
  await expect(
    page.getByText(
      "A private key in PEM form; the public half goes in the provider's deploy keys.",
    ),
  ).toBeVisible()

  await page.getByRole("switch", { name: /^Registry login/ }).click()
  // Exact: the "Registry login" OptionRow's own hint text contains the word
  // "username" too, so a substring match would also catch that switch.
  await expect(page.getByLabel("Username", { exact: true })).toBeVisible()
  await expect(page.locator("input#credential-secret")).toHaveCount(1)
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

  await expect(page.getByText("Leave empty to keep the stored secret.")).toBeVisible()
  await page.getByLabel("Name", { exact: true }).fill("GitHub PAT (renamed)")
  await page.getByRole("button", { name: "Save credential" }).click()
  await expect(page.getByText("Credential saved")).toBeVisible()

  expect(puts.at(-1)?.secret).toBeUndefined()
  expect(puts.at(-1)?.name).toBe("GitHub PAT (renamed)")
})

test("Test shows the adapter's result inline", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [githubToken])
  })
  await page.route("**/api/v1/deploy/credentials/5/test", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    await json(route, { ok: true, message: "Connected as deploy-bot" })
  })

  await page.goto("/deploy/credentials")
  await credentialRow(page, "GitHub PAT").getByRole("button", { name: "Test" }).click()
  // Scoped to the dialog: its own footer button shares the row icon's name.
  const dialog = page.getByRole("dialog", { name: "Test GitHub PAT" })
  await expect(dialog).toBeVisible()
  await dialog.getByRole("button", { name: "Test", exact: true }).click()
  await expect(dialog.getByText("Connected as deploy-bot")).toBeVisible()
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
