import { expect, test, type Page } from "@playwright/test"
import { json, mockProject, now } from "./deploy-fixture"

/**
 * The fleet-level Credentials page (`/deploy/credentials`): a page of its
 * own rather than a project tab, since one token or key is shared by every
 * project that references it. None of its routes exist in the shared
 * fixture yet, so every test stubs `/deploy/credentials` itself.
 */

/** One credential's card. The list groups them under In use / Not in use, so a card is found by its slot. */
function credentialRow(page: Page, name: string) {
  return page
    .getByRole("list", { name: "Credentials" })
    .locator("[data-slot=choice-row]", { hasText: name })
}

const githubToken = {
  id: 5,
  name: "github-pat",
  kind: "git_bearer",
  target: "github.com",
  createdAt: now,
  updatedAt: now,
  lastUsedAt: undefined,
  usedBy: 0,
}

const registryLogin = {
  id: 9,
  name: "registry-login",
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
  name: "github-api",
  kind: "provider_token",
  target: "",
  createdAt: now,
  updatedAt: now,
  lastUsedAt: undefined,
  usedBy: 0,
}

test("lists credentials as cards drawn as their hosts, under four readings", async ({ page }) => {
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

  // The readings: how many are held, how many a project's source reads
  // through, how many were never used (amber: nobody would notice one being
  // used), and when one was last reached for.
  const tiles = page.locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(4)
  await expect(tiles.nth(0)).toContainText("2")
  await expect(tiles.nth(0)).toContainText("across 2 hosts")
  await expect(tiles.nth(1)).toContainText("1")
  await expect(tiles.nth(2)).toContainText("Never used")
  await expect(tiles.nth(2)).toContainText("github-pat added")
  await expect(tiles.nth(2).locator(".text-warning")).toHaveText("1")
  await expect(tiles.nth(3)).toContainText(/ago/)
  await expect(tiles.nth(3)).toContainText("registry-login")
  // The App's state is said by its own section, not by a third tile.
  await expect(
    page
      .locator("[data-slot=panel-header]")
      .filter({ hasText: "GitHub App" })
      .getByText("Not connected", { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("list", { name: "GitHub App setup" })).toBeVisible()

  // Used first, under a rule saying so.
  await expect(page.getByText("In use", { exact: true }).last()).toBeVisible()
  await expect(page.getByText("Not in use", { exact: true })).toBeVisible()

  const github = credentialRow(page, "github-pat")
  await expect(github.getByText("Git token", { exact: true })).toBeVisible()
  await expect(github.getByText("github.com")).toBeVisible()
  await expect(github.getByText("not in use")).toBeVisible()
  await expect(github.getByText("never used")).toBeVisible()
  // Drawn as the host it signs in to.
  await expect(github.locator('img[src="/logos/github.svg"]')).toHaveCount(1)
  await expect(github.getByRole("button", { name: "Edit github-pat" })).toBeVisible()

  const registry = credentialRow(page, "registry-login")
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
  // On a phone the kind moves under the name rather than off the card.
  await expect(registry.getByText("Registry", { exact: true })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath("credentials-390.png"), fullPage: true })
})

test("an empty list offers the four kinds, each opening its own form", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [])
  })

  await page.goto("/deploy/credentials")
  await page.getByRole("button", { name: "Add SSH key" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("button", { name: /^SSH key/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.locator("textarea#credential-secret")).toHaveCount(1)
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

  // Git token over HTTPS is the default selection: a password field that
  // reads the token it is given, and can show it.
  await expect(page.locator("input#credential-secret")).toHaveCount(1)
  await expect(page.locator("textarea#credential-secret")).toHaveCount(0)
  await expect(page.locator("input#credential-secret")).toHaveAttribute("type", "password")
  await page.getByRole("button", { name: "Show token" }).click()
  await expect(page.locator("input#credential-secret")).toHaveAttribute("type", "text")
  await page.locator("#credential-target").fill("github.com")
  await page.locator("#credential-secret").fill("glpat-abcdefghij")
  await expect(page.getByRole("dialog").getByText("GitLab token", { exact: true })).toBeVisible()
  await expect(
    page.getByText("This looks like a GitLab token, but the host is github.com."),
  ).toBeVisible()

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
  // The key names its format on its first line, and the public half is caught.
  await page.locator("#credential-secret").fill("-----BEGIN OPENSSH PRIVATE KEY-----\nabc")
  await expect(page.getByText("OpenSSH private key", { exact: true })).toBeVisible()
  await page.locator("#credential-secret").fill("ssh-ed25519 AAAAC3Nza ops@laptop")
  await expect(page.getByText(/That is the public half/)).toBeVisible()

  await page.getByRole("button", { name: /^Registry login/ }).click()
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
  // What it will be saved as is said before the press.
  await expect(dialog.getByText("saves as github.com")).toBeVisible()
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
    await json(route, { ...githubToken, name: "github-pat-renamed" })
  })

  await page.goto("/deploy/credentials")
  await credentialRow(page, "github-pat")
    .getByRole("button", { name: "Actions for github-pat" })
    .click()
  await page.getByRole("menuitem", { name: "Edit credential" }).click()

  // The kind is fixed once created: the sheet opens on the credential
  // itself, its kind a fact rather than a picker.
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("button", { name: /^SSH key/ })).toHaveCount(0)
  await expect(dialog.getByText("Git token", { exact: true })).toBeVisible()
  await expect(page.getByText("Leave empty to keep the stored secret.")).toBeVisible()
  // The server's rule for a name is lit and waited on: a space is refused.
  await page.getByLabel("Name", { exact: true }).fill("github pat")
  await expect(page.getByRole("button", { name: "Save credential" })).toBeDisabled()
  await page.getByLabel("Name", { exact: true }).fill("github-pat-renamed")
  await page.getByRole("button", { name: "Save credential" }).click()
  await expect(page.getByText("Credential saved")).toBeVisible()

  expect(puts.at(-1)?.secret).toBeUndefined()
  expect(puts.at(-1)?.name).toBe("github-pat-renamed")
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
  await credentialRow(page, "github-pat").getByRole("button", { name: "Test" }).click()
  // Scoped to the dialog: its own footer button shares the row icon's name.
  const dialog = page.getByRole("dialog", { name: "Test github-pat" })
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
  await credentialRow(page, "registry-login").getByRole("button", { name: "Test" }).click()
  const registry = page.getByRole("dialog", { name: "Test registry-login" })
  await expect(registry.getByLabel("Image")).toBeVisible()
  await expect(registry.getByText("An image on ghcr.io to resolve with this login.")).toBeVisible()
  await registry.locator("#credential-test-subject").fill("ghcr.io/acme/app:latest")
  await registry.getByRole("button", { name: "Test", exact: true }).click()
  await expect(registry.getByText("Connected as deploy-bot")).toBeVisible()
  expect(probes.at(-1)?.repository).toBe("ghcr.io/acme/app:latest")
  await registry.getByRole("button", { name: "Close" }).first().click()

  // A provider token without a saved host needs the whole URL.
  await credentialRow(page, "github-api").getByRole("button", { name: "Test" }).click()
  const provider = page.getByRole("dialog", { name: "Test github-api" })
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
  await credentialRow(page, "github-pat").getByRole("button", { name: "Test" }).click()
  const dialog = page.getByRole("dialog", { name: "Test github-pat" })
  await dialog.locator("#credential-test-subject").fill("bad path")
  await dialog.getByRole("button", { name: "Test", exact: true }).click()
  await expect(dialog.getByRole("alert")).toContainText("owner/name path")
})

test("GitHub's redirect is finished from the page with the code and state it carried", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [])
  })
  const exchanges: Record<string, unknown>[] = []
  await page.route("**/api/v1/deploy/github-app/callback", async (route) => {
    exchanges.push(route.request().postDataJSON() as Record<string, unknown>)
    await json(route, { slug: "just-dashboard-ab12", owner: "acme" })
  })

  // GitHub lands the browser here without the session cookie (a cross-site
  // navigation), so the page itself has to post the exchange.
  await page.goto("/deploy/credentials?code=one-time-code&state=issued-state")
  await expect(page.getByText("just-dashboard-ab12 is connected.")).toBeVisible()
  expect(exchanges).toEqual([{ code: "one-time-code", state: "issued-state" }])
  // Both came off the address, so a reload cannot replay the exchange.
  expect(new URL(page.url()).search).toBe("")
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
  await credentialRow(page, "registry-login")
    .getByRole("button", { name: "Actions for registry-login" })
    .click()
  await page.getByRole("menuitem", { name: "Remove credential" }).click()
  // The confirmation says what the server will answer before it is asked.
  await expect(page.getByText(/refuses to remove it while their sources point at it/)).toBeVisible()
  await page.getByRole("button", { name: "Remove credential" }).click()

  // No special-cased toast for this refusal: the confirm dialog's own
  // failure toast already names the server's message, which for
  // `credential_in_use` names the projects, so it is shown exactly once.
  await expect(page.getByText("Remove registry-login failed")).toBeVisible()
  await expect(page.getByText("Used by 2 projects' current source.")).toHaveCount(1)
})

test("a role without system.admin is not shown the list it may not read", async ({ page }) => {
  await mockProject(page)
  // The list is an administrator's read: the server answers anyone else 403.
  const reads: string[] = []
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    reads.push(route.request().url())
    await route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "forbidden", message: "forbidden" } }),
    })
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
  await expect(page.getByText("An administrator manages the saved credentials.")).toBeVisible()
  // Asked for nothing it would be refused, and drawn no readings of it.
  expect(reads).toEqual([])
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Add credential" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Test" })).toHaveCount(0)
})
