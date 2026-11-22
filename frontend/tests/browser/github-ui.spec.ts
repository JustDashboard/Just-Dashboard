import { expect, test, type Page } from "@playwright/test"
import { connections, json, repositoryList } from "./github-fixture-data"
import { manyRepositories, mockGitHubPage, setupFor } from "./github-fixture"

/**
 * The GitHub page (`/github`) and the rail's split of Git into Local Git and
 * GitHub, against the shared fixture: the App's setup, both sign-ins, the
 * callback, the list's identity line, toolbar and rows, and the two dialogs
 * that make something (a repository on GitHub, a clone on this server).
 * Nothing reaches github.com: every navigation there is answered here.
 */

const rail = (page: Page) => page.getByRole("navigation", { name: "Sidebar" })
const identity = (page: Page) => page.getByRole("region", { name: "About GitHub" })
const rows = (page: Page) =>
  page.getByRole("list", { name: /^Repositories of/ }).getByRole("listitem")

/** github.com answered by a blank page, with what was asked of it. */
async function catchGitHub(page: Page) {
  const seen: { url: string; method: string; body?: string }[] = []
  await page.route("https://github.com/**", (route) => {
    seen.push({
      url: route.request().url(),
      method: route.request().method(),
      body: route.request().postData() ?? undefined,
    })
    return route.fulfill({ status: 200, contentType: "text/html", body: "<p>GitHub</p>" })
  })
  return seen
}

test("the rail has Local Git and GitHub, and /git is still Local Git", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockGitHubPage(page)
  await page.goto("/git")
  await expect(page.getByRole("heading", { name: "Local Git", level: 1 })).toBeAttached()
  await expect(rail(page).getByRole("link", { name: "Local Git", exact: true })).toBeVisible()
  await rail(page).getByRole("link", { name: "GitHub", exact: true }).click()
  await expect(page).toHaveURL(/\/github$/)
  await expect(identity(page)).toContainText("GitHub")
  await expect(page).toHaveTitle(/^GitHub · /)
})

test("search finds both pages by git and by github", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockGitHubPage(page)
  await page.goto("/github")
  await expect(identity(page)).toBeVisible()
  const trigger = page
    .locator('[data-slot="sidebar-header"]')
    .getByRole("button", { name: /Search/ })
  await trigger.focus()
  await page.keyboard.press("Control+k")
  const palette = page.getByRole("dialog", { name: "Command palette" })
  const input = palette.getByRole("combobox", { name: "Search dashboard" })
  const options = palette.getByRole("listbox").getByRole("option")
  await input.fill("page: git")
  await expect(options.filter({ hasText: "Local Git" }).first()).toBeVisible()
  await expect(options.filter({ hasText: /^GitHub/ }).first()).toBeVisible()
  await input.fill("page: github")
  await expect(options.first()).toContainText("GitHub")
})

test.describe("before the App exists", () => {
  test("an administrator sets it up and is sent to GitHub with the manifest", async ({
    page,
    baseURL,
  }) => {
    const { calls } = await mockGitHubPage(page, { setup: { configured: false } })
    const github = await catchGitHub(page)
    await page.goto("/github")
    await expect(page.getByText("GitHub is not set up")).toBeVisible()
    await page.getByRole("button", { name: "Set up" }).click()
    const dialog = page.getByRole("dialog", { name: "Set up GitHub" })
    await expect(dialog).toContainText("Read and push code")
    await dialog.getByRole("radio", { name: "An organisation" }).click()
    await dialog.getByRole("textbox", { name: "Organisation" }).fill("-bad")
    await expect(dialog.getByRole("button", { name: "Continue on GitHub" })).toBeDisabled()
    await dialog.getByRole("textbox", { name: "Organisation" }).fill("northwind")
    await dialog.getByRole("button", { name: "Continue on GitHub" }).click()
    await expect.poll(() => github.length).toBe(1)
    expect(github[0].method).toBe("POST")
    const posted = new URLSearchParams(github[0].body ?? "").get("manifest")
    expect(JSON.parse(posted ?? "{}")).toEqual({ name: "Just Dashboard Repos" })
    const manifest = calls.find((call) => call.path === "/github/app/manifest")
    expect(manifest?.body).toMatchObject({
      origin: baseURL,
      organization: "northwind",
      public: true,
    })
  })

  test("anyone else is told an administrator sets it up", async ({ page }) => {
    await mockGitHubPage(page, {
      setup: { configured: false },
      capabilities: ["read"],
      role: "readonly",
    })
    await page.goto("/github")
    await expect(page.getByText(/An administrator sets it up once/)).toBeVisible()
    await expect(page.getByRole("button", { name: "Set up" })).toHaveCount(0)
  })
})

test.describe("set up, nothing connected", () => {
  test("Connect posts this address and goes to GitHub", async ({ page, baseURL }) => {
    const { calls } = await mockGitHubPage(page, { setup: setupFor(baseURL!), connections: [] })
    const github = await catchGitHub(page)
    await page.goto("/github")
    await page.getByRole("button", { name: "Connect GitHub" }).click()
    await expect.poll(() => github.length).toBeGreaterThan(0)
    expect(github[0].url).toContain("/login/oauth/authorize")
    expect(calls.find((call) => call.path === "/github/connections/authorize")?.body).toEqual({
      origin: baseURL,
    })
  })

  test("an unregistered address offers the code sign-in, which connects", async ({ page }) => {
    // The fixture's App names another address than this server's.
    await mockGitHubPage(page, { connections: [] })
    await page.goto("/github")
    const help = page.locator("[data-slot=github-connect-help]")
    await expect(help).toContainText("only sends a sign-in back")
    await expect(help.getByRole("list", { name: "Registered addresses" })).toContainText(
      "http://127.0.0.1:43117/github/callback",
    )
    await expect(help.getByRole("button", { name: "Add this address" })).toBeVisible()
    await help.getByRole("button", { name: "Sign in with a code" }).click()
    const dialog = page.getByRole("dialog", { name: "Sign in with a code" })
    await expect(dialog.locator("[data-slot=device-code]")).toHaveText("WDJB-MJHT")
    await expect(dialog.getByRole("link", { name: "Open GitHub" })).toHaveAttribute(
      "href",
      "https://github.com/login/device",
    )
    // Pending once, then complete: the page lands on the new account.
    await expect(page).toHaveURL(/\/github\?account=1$/, { timeout: 10_000 })
    await expect(dialog).toBeHidden()
  })

  test("an App without device flow says where to switch it on", async ({ page }) => {
    await mockGitHubPage(page, { connections: [], device: "disabled" })
    await page.goto("/github")
    await page.getByRole("button", { name: "Sign in with a code" }).click()
    const dialog = page.getByRole("dialog", { name: "Sign in with a code" })
    await expect(dialog).toContainText("Enable Device Flow")
    await expect(dialog.getByRole("link", { name: "App settings" })).toHaveAttribute(
      "href",
      /settings\/apps\/jd-repos-3f2a/,
    )
  })
})

test.describe("the callback", () => {
  test("posts the code once, cleans the address and lands on the account", async ({ page }) => {
    const { calls } = await mockGitHubPage(page)
    await page.goto("/github/callback?code=c0de&state=st4te")
    await expect(page).toHaveURL(/\/github\?account=1$/)
    await expect(identity(page)).toContainText("@mira-dev")
    const posts = calls.filter((call) => call.path === "/github/callback")
    expect(posts).toEqual([
      { method: "POST", path: "/github/callback", body: { code: "c0de", state: "st4te" } },
    ])
  })

  test("a cancel on GitHub explains and posts nothing", async ({ page }) => {
    const { calls } = await mockGitHubPage(page)
    await page.goto("/github/callback?error=access_denied&error_description=denied")
    await expect(page.getByText("The sign-in was cancelled on GitHub")).toBeVisible()
    await expect(page).toHaveURL(/\/github\/callback$/)
    expect(calls.filter((call) => call.path === "/github/callback")).toEqual([])
    await page.getByRole("link", { name: "Back to GitHub" }).click()
    await expect(page).toHaveURL(/\/github$/)
  })
})

test.describe("connected", () => {
  test("the identity line counts in words and the toolbar narrows the list", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page)
    await page.goto("/github")
    await expect(identity(page)).toContainText("9 repositories")
    await expect(identity(page)).toContainText("4 private")
    await expect(identity(page)).toContainText("1 archived")
    await expect(identity(page).getByRole("button", { name: "New repository" })).toBeVisible()
    await expect(rows(page)).toHaveCount(9)
    // Recently updated first.
    await expect(rows(page).first()).toContainText("northwind/api")

    await page.getByRole("textbox", { name: "Search repositories" }).fill("billing")
    await expect(rows(page)).toHaveCount(1)
    await page.getByRole("textbox", { name: "Search repositories" }).fill("")
    await page.getByRole("button", { name: "Private 4" }).click()
    await expect(rows(page)).toHaveCount(4)
    await page.getByRole("button", { name: "All 9" }).click()

    await page.getByRole("combobox", { name: "Owner" }).click()
    await page.getByRole("option", { name: "acme-labs" }).click()
    await expect(rows(page)).toHaveCount(3)
    await page.getByRole("combobox", { name: "Language" }).click()
    await page.getByRole("option", { name: "TypeScript" }).click()
    await expect(rows(page)).toHaveCount(1)
    await expect(rows(page).first()).toContainText("Template")
    await expect(rows(page).first()).toContainText("Read only")

    await page.getByRole("combobox", { name: "Language" }).click()
    await page.getByRole("option", { name: "Every language" }).click()
    await page.getByRole("combobox", { name: "Sort repositories" }).click()
    await page.getByRole("option", { name: "Most stars" }).click()
    await expect(rows(page).first()).toContainText("acme-labs/design-system")

    await page.getByRole("textbox", { name: "Search repositories" }).fill("nothing-like-this")
    await expect(page.getByText("No repository matches.")).toBeVisible()
    await page.getByRole("button", { name: "Clear filters" }).click()
    await expect(rows(page)).toHaveCount(9)
  })

  test("Show more adds thirty and the rows already drawn hold still", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page, { repositories: { 1: manyRepositories(40), 2: [] } })
    await page.goto("/github")
    await expect(rows(page)).toHaveCount(30)
    // Where a drawn row stands in the list, whatever the press scrolled.
    const place = () =>
      rows(page).evaluateAll((items) =>
        items
          .slice(0, 30)
          .map((item) =>
            Math.round(item.getBoundingClientRect().top - items[0].getBoundingClientRect().top),
          ),
      )
    const before = await place()
    await page.getByRole("button", { name: "Show more" }).click()
    await expect(rows(page)).toHaveCount(49)
    expect(await place()).toEqual(before)
    await expect(page.getByRole("button", { name: "Show more" })).toHaveCount(0)
  })

  for (const width of [1440, 390]) {
    test(`every row is one height at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockGitHubPage(page)
      await page.goto("/github")
      await expect(rows(page)).toHaveCount(9)
      const heights = await rows(page).evaluateAll((items) =>
        items.map((item) => Math.round(item.getBoundingClientRect().height)),
      )
      expect(new Set(heights).size, `heights ${heights.join(", ")}`).toBe(1)
    })
  }

  test("only the two repositories checked out here carry the server mark", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page)
    await page.goto("/github")
    await expect(rows(page)).toHaveCount(9)
    const marked = rows(page).filter({ has: page.locator("[data-slot=on-server]") })
    await expect(marked).toHaveCount(2)
    await expect(marked.nth(0)).toContainText("northwind/api")
    await expect(marked.nth(1)).toContainText("mira-dev/dotfiles")
    await marked.nth(0).getByRole("button", { name: "Actions for northwind/api" }).click()
    await expect(page.getByRole("menuitem", { name: "Open in Local Git" })).toHaveAttribute(
      "href",
      "/git?repo=%2Fsrv%2Fnorthwind-api",
    )
  })

  test("a row opens the repository's page on this account", async ({ page }) => {
    await mockGitHubPage(page)
    await page.goto("/github")
    await expect(page.getByRole("link", { name: "Open northwind/api" })).toHaveAttribute(
      "href",
      "/github/northwind/api?account=1",
    )
  })

  test("an expired account offers Reconnect, and the picker switches back", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page)
    await page.goto("/github?account=2")
    const notice = page.locator("[data-slot=github-reconnect]")
    await expect(notice).toContainText("GitHub no longer accepts the sign-in for mira-work")
    await expect(notice.getByRole("button", { name: "Reconnect" })).toBeVisible()
    await expect(identity(page)).toContainText("Sign-in ended")
    await identity(page)
      .getByRole("button", { name: /Account @mira-work/ })
      .click()
    await page.getByRole("menuitemcheckbox", { name: /@mira-dev/ }).click()
    await expect(page).toHaveURL(/\/github\?account=1$/)
    await expect(rows(page)).toHaveCount(9)
  })

  test("a spent rate limit says when it lifts", async ({ page }) => {
    await mockGitHubPage(page, {
      list: async (route) => {
        await route.fulfill({
          status: 429,
          contentType: "application/json",
          headers: { "Retry-After": "600" },
          body: JSON.stringify({
            error: {
              code: "github_rate_limited",
              message: "GitHub's rate limit for this account is spent until 14:05 UTC.",
            },
          }),
        })
        return true
      },
    })
    await page.goto("/github")
    const state = page.locator("[data-slot=github-rate-limited]")
    await expect(state).toContainText("spent until 14:05 UTC")
    await expect(state).toContainText(/Try again after \d{2}:\d{2}/)
  })

  test("an installation that failed to list is a warning over the rest", async ({ page }) => {
    await mockGitHubPage(page, {
      list: async (route) => {
        await json(route, {
          ...repositoryList(connections[0]),
          partial: [{ installationId: 12, account: "northwind", message: "GitHub answered 502." }],
        })
        return true
      },
    })
    await page.goto("/github")
    await expect(page.locator("[data-slot=github-partial]")).toContainText(
      "Could not list northwind's repositories",
    )
    await expect(rows(page)).toHaveCount(9)
  })

  test("the access strip names each installation and refreshes it", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const { calls } = await mockGitHubPage(page)
    await page.goto("/github")
    const access = page.getByRole("group", { name: "What the App can reach" })
    await expect(access).toContainText("northwind")
    await expect(access).toContainText("Selected repositories")
    await expect(
      access.getByRole("link", { name: "Change what northwind grants" }),
    ).toHaveAttribute("href", /installations\/12/)
    await access.getByRole("button", { name: "Refresh access" }).click()
    await expect
      .poll(() => calls.filter((call) => call.path === "/github/connections/1/refresh").length)
      .toBe(1)
  })

  test("GitHub's setup redirect reads every account's access once", async ({ page }) => {
    const { calls } = await mockGitHubPage(page)
    await page.goto("/github?installed=1")
    await expect(page).toHaveURL(/\/github$/)
    // The expired account is not asked; it has to sign in again first.
    expect(calls.filter((call) => /\/refresh$/.test(call.path)).map((call) => call.path)).toEqual([
      "/github/connections/1/refresh",
    ])
  })

  test("Disconnect asks first and removes the account", async ({ page }) => {
    const { calls } = await mockGitHubPage(page)
    await page.goto("/github")
    await identity(page).getByRole("button", { name: "More actions for GitHub" }).click()
    await page.getByRole("menuitem", { name: "Disconnect @mira-dev" }).click()
    const dialog = page.getByRole("dialog", { name: "Disconnect @mira-dev" })
    await dialog.getByRole("button", { name: "Disconnect" }).click()
    await expect
      .poll(() => calls.filter((call) => call.method === "DELETE").map((call) => call.path))
      .toEqual(["/github/connections/1"])
  })
})

test.describe("new repository", () => {
  test("holds GitHub's name rule, says the name it will use, and opens it", async ({ page }) => {
    const { calls } = await mockGitHubPage(page)
    await page.goto("/github")
    await identity(page).getByRole("button", { name: "New repository" }).click()
    const dialog = page.getByRole("dialog", { name: "New repository" })
    await expect(dialog.getByRole("combobox", { name: "Owner" })).toContainText("mira-dev")
    const create = dialog.getByRole("button", { name: "Create" })
    await expect(create).toBeDisabled()
    await dialog.getByLabel("Name").fill("my new tool")
    await expect(dialog).toContainText("Created as my-new-tool")
    await dialog.getByRole("combobox", { name: "Owner" }).click()
    await expect(page.getByRole("option", { name: /acme-labs/ })).toHaveAttribute(
      "data-disabled",
      "",
    )
    await page.keyboard.press("Escape")
    await dialog.getByRole("radio", { name: "Public" }).click()
    await create.click()
    await expect(page).toHaveURL(/\/github\/mira-dev\/my-new-tool\?account=1$/)
    expect(
      calls.find((call) => call.method === "POST" && call.path.endsWith("/repositories"))?.body,
    ).toEqual({ owner: "mira-dev", name: "my-new-tool", visibility: "public", autoInit: true })
  })

  test("GitHub's refusal of the name lands on the field", async ({ page }) => {
    await mockGitHubPage(page, { create: "name-taken" })
    await page.goto("/github")
    await identity(page).getByRole("button", { name: "New repository" }).click()
    const dialog = page.getByRole("dialog", { name: "New repository" })
    await dialog.getByLabel("Name").fill("dotfiles")
    await dialog.getByRole("button", { name: "Create" }).click()
    await expect(dialog).toContainText("name already exists on this account")
    await expect(dialog).toBeVisible()
  })

  test("a repository the App cannot reach yet stays in the dialog and says why", async ({
    page,
  }) => {
    await mockGitHubPage(page, { create: "unreachable" })
    await page.goto("/github")
    await identity(page).getByRole("button", { name: "New repository" }).click()
    const dialog = page.getByRole("dialog", { name: "New repository" })
    await dialog.getByLabel("Name").fill("ledger")
    await dialog.getByRole("button", { name: "Create" }).click()
    await expect(dialog).toContainText("add it there to see it here")
    await expect(page).toHaveURL(/\/github$/)
  })
})

test.describe("clone to server", () => {
  test("streams git's output and offers the checkout in Local Git", async ({ page }) => {
    const { calls } = await mockGitHubPage(page)
    await page.goto("/github")
    await page.getByRole("button", { name: "Actions for northwind/api" }).click()
    await page.getByRole("menuitem", { name: "Clone to server…" }).click()
    const dialog = page.getByRole("dialog", { name: "Clone to server" })
    await expect(dialog.getByLabel("Folder")).toHaveValue("api")
    await expect(dialog.getByLabel("Branch")).toHaveValue("main")
    await expect(dialog).toContainText("one-time read-only token")
    await dialog.getByLabel("Folder").fill(".hidden")
    await expect(dialog.getByRole("button", { name: "Clone" })).toBeDisabled()
    await dialog.getByLabel("Folder").fill("api")
    await dialog.getByRole("button", { name: "Clone" }).click()
    await expect(dialog).toContainText("Receiving objects: 100%")
    await expect(dialog.getByRole("link", { name: "Open in Local Git" })).toHaveAttribute(
      "href",
      "/git?repo=%2Fsrv%2Fapi",
    )
    expect(calls.find((call) => call.path.endsWith("/clone"))).toEqual({
      method: "POST",
      path: "/github/connections/1/repos/northwind/api/clone",
      body: { parent: "/srv", name: "api", branch: "main" },
    })
  })

  test("a failed clone keeps the form with git's reason", async ({ page }) => {
    await mockGitHubPage(page, { clone: "failed" })
    await page.goto("/github")
    await page.getByRole("button", { name: "Actions for northwind/api" }).click()
    await page.getByRole("menuitem", { name: "Clone to server…" }).click()
    const dialog = page.getByRole("dialog", { name: "Clone to server" })
    await dialog.getByRole("button", { name: "Clone" }).click()
    await expect(dialog).toContainText("already exists")
    await expect(dialog.getByRole("button", { name: "Clone" })).toBeEnabled()
    await expect(dialog.getByRole("link", { name: "Open in Local Git" })).toHaveCount(0)
  })
})

test("on a phone the page does not scroll sideways and the command is its icon", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockGitHubPage(page)
  await page.goto("/github")
  await expect(rows(page)).toHaveCount(9)
  const sideways = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
  )
  expect(sideways).toBe(false)
  const command = identity(page).getByRole("button", { name: "New repository" })
  await expect(command).toBeVisible()
  const box = await command.boundingBox()
  expect(Math.round(box!.width)).toBeLessThanOrEqual(32)
})
