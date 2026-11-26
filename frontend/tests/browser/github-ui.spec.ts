import { expect, test, type Page } from "@playwright/test"
import { connections, json, repositoryList } from "./github-fixture-data"
import { manyRepositories, mockGitHubPage, setupFor } from "./github-fixture"

/**
 * The GitHub page (`/github`) and the rail's split of Git into Local Git and
 * GitHub, against the shared fixture: the App's setup, both sign-ins, the
 * callback, the identity line, the account's Overview (profile, calendar,
 * recent repositories, pull requests, issues, activity), the Repositories
 * view's toolbar and cards, the Pull requests and Issues views, and the two
 * dialogs that make something (a repository on GitHub, a clone on this
 * server).
 * Nothing reaches github.com: every navigation there is answered here.
 */

const rail = (page: Page) => page.getByRole("navigation", { name: "Sidebar" })
const identity = (page: Page) => page.getByRole("region", { name: "About GitHub" })
const rows = (page: Page) =>
  page.getByRole("list", { name: /^Repositories of/ }).getByRole("listitem")
const REPOSITORIES = "/github?tab=repositories"

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
      "https://dashboard.example.net/github/callback",
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
    await page.goto(REPOSITORIES)
    await expect(identity(page)).toContainText("9 repositories")
    await expect(identity(page)).toContainText("4 private")
    await expect(identity(page)).toContainText("1 archived")
    await expect(identity(page).getByRole("button", { name: "New repository" })).toBeVisible()
    await expect(rows(page)).toHaveCount(9)
    // Recently updated first.
    // A card names its repository by its link, "Open owner/name".
    await expect(rows(page).first().getByRole("link", { name: "Open northwind/api" })).toBeVisible()

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
    await expect(
      rows(page).first().getByRole("link", { name: "Open acme-labs/design-system" }),
    ).toBeVisible()

    await page.getByRole("textbox", { name: "Search repositories" }).fill("nothing-like-this")
    await expect(page.getByText("No repository matches.")).toBeVisible()
    await page.getByRole("button", { name: "Clear filters" }).click()
    await expect(rows(page)).toHaveCount(9)
  })

  test("Show more adds thirty and the cards already drawn hold still", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page, { repositories: { 1: manyRepositories(40), 2: [] } })
    await page.goto(REPOSITORIES)
    await expect(rows(page)).toHaveCount(30)
    // Where a drawn card stands on the shelf, whatever the press scrolled.
    const place = () =>
      rows(page).evaluateAll((items) =>
        items.slice(0, 30).map((item) => {
          const box = item.getBoundingClientRect()
          const first = items[0].getBoundingClientRect()
          return `${Math.round(box.left - first.left)},${Math.round(box.top - first.top)}`
        }),
      )
    const before = await place()
    await page.getByRole("button", { name: "Show more" }).click()
    await expect(rows(page)).toHaveCount(49)
    expect(await place()).toEqual(before)
    await expect(page.getByRole("button", { name: "Show more" })).toHaveCount(0)
  })

  for (const width of [1440, 390]) {
    test(`every card is one height at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockGitHubPage(page)
      await page.goto(REPOSITORIES)
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
    await page.goto(REPOSITORIES)
    await expect(rows(page)).toHaveCount(9)
    const marked = rows(page).filter({ has: page.locator("[data-slot=on-server]") })
    await expect(marked).toHaveCount(2)
    await expect(marked.nth(0).getByRole("link", { name: "Open northwind/api" })).toBeVisible()
    await expect(marked.nth(1).getByRole("link", { name: "Open mira-dev/dotfiles" })).toBeVisible()
    await marked.nth(0).getByRole("button", { name: "Actions for northwind/api" }).click()
    await expect(page.getByRole("menuitem", { name: "Open in Local Git" })).toHaveAttribute(
      "href",
      "/git?repo=%2Fsrv%2Fnorthwind-api",
    )
    // Menus here are words (standard.md): no glyph beside an item.
    await expect(page.getByRole("menu").locator("svg")).toHaveCount(0)
  })

  test("a card opens the repository's page on this account", async ({ page }) => {
    await mockGitHubPage(page)
    await page.goto(REPOSITORIES)
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
    await expect(page.locator("[data-slot=github-profile]")).toContainText("Mira")
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
    await page.goto(REPOSITORIES)
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
    await expect(access.locator("[data-slot=github-installation]")).toHaveCount(3)
    await expect(access.locator("[data-slot=github-installation]").nth(1)).toContainText(
      /northwind\s*Selected/,
    )
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

test.describe("the Overview", () => {
  test("opens on the account's profile, its year, and the repositories it pushed to last", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page)
    await page.goto("/github")
    await expect(page.getByRole("tab", { name: "Overview", selected: true })).toBeVisible()
    const profile = page.locator("[data-slot=github-profile]")
    await expect(profile).toContainText("Mira")
    await expect(profile).toContainText("Backend and platform work")
    await expect(profile).toContainText("184 followers · 31 following")
    await expect(profile.getByRole("link", { name: "mira.example.net" })).toHaveAttribute(
      "href",
      "https://mira.example.net/",
    )

    const calendar = page.locator("[data-slot=github-contributions]")
    await expect(calendar.getByRole("heading")).toContainText(/contributions this year/)
    await expect(calendar.getByText(/^Longest streak \d+ days$/)).toBeVisible()
    // The whole year on a desk: fifty-three weeks, seven days each but today's.
    expect(await calendar.locator("svg g").count()).toBe(53)
    const day = calendar.locator("[data-slot=github-contributions-day]")
    await expect(day).toHaveText(/^Busiest day \d+ \w{3}, \d+ contributions$/)
    await calendar.locator("rect[data-level]").last().hover()
    await expect(day).toHaveText(/contributions? on \d+ \w{3}$/)

    const recent = page.getByRole("list", { name: "Recently pushed" }).getByRole("listitem")
    await expect(recent).toHaveCount(4)
    await expect(recent.first()).toContainText("api")
    // Archived repositories are not "recent", however lately they moved.
    await expect(recent.filter({ hasText: "legacy-portal" })).toHaveCount(0)
    await page.getByRole("link", { name: "All repositories" }).click()
    await expect(page).toHaveURL(/tab=repositories/)
    await expect(rows(page)).toHaveCount(9)
  })

  test("the calendar shows the latest weeks a phone holds, at the same height", async ({
    page,
  }) => {
    const heights: number[] = []
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 })
      await mockGitHubPage(page)
      await page.goto("/github")
      const calendar = page.locator("[data-slot=github-contributions] svg")
      await expect(calendar).toBeVisible()
      const weeks = await calendar.locator("g").count()
      if (width === 390) expect(weeks).toBeLessThan(53)
      if (width === 390) expect(weeks).toBeGreaterThan(15)
      heights.push(Math.round((await calendar.boundingBox())!.height))
      const box = (await calendar.boundingBox())!
      expect(box.x + box.width).toBeLessThanOrEqual(width)
    }
    expect(heights[0]).toBe(heights[1])
  })

  test("pull requests, review requests, assigned issues and the activity", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page)
    await page.goto("/github")
    const mine = page.locator("[data-slot=github-inbox-pull-created]")
    await expect(mine.getByRole("listitem")).toHaveCount(3)
    await expect(mine.getByRole("listitem").first()).toContainText(
      "Retry webhook deliveries with backoff",
    )
    // Open, as GitHub colours it; a draft keeps its own grey glyph.
    await expect(mine.getByRole("img", { name: "open" }).first()).toBeVisible()
    await expect(mine.getByRole("img", { name: "draft" })).toHaveCount(1)
    await expect(page.locator("[data-slot=github-inbox-pull-review]")).toContainText(
      "Lazy-load the product gallery",
    )
    await expect(page.locator("[data-slot=github-inbox-issue-assigned]")).toContainText(
      "Invoices for January show the wrong tax line",
    )
    const activity = page.locator("[data-slot=github-activity]")
    await expect(activity.getByRole("listitem")).toHaveCount(10)
    await expect(activity.getByRole("listitem").first()).toContainText(
      "Pushed 3 commits to retry-webhooks in northwind/api",
    )
    await expect(activity.getByRole("link", { name: "northwind/api" }).first()).toHaveAttribute(
      "href",
      "/github/northwind/api?account=1",
    )
    // A repository the page does not list is GitHub's.
    await expect(activity.getByRole("link", { name: "astro-starters/blog" })).toHaveAttribute(
      "href",
      "https://github.com/astro-starters/blog",
    )
  })

  test("nothing under the views jumps when the reads land", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    let release: () => void = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await mockGitHubPage(page, {
      handle: async (route, path) => {
        if (!/\/profile$/.test(path)) return false
        await held
        return false
      },
    })
    await page.goto("/github")
    const calendar = page.locator("[data-slot=github-contributions]")
    await expect(calendar).toBeVisible()
    const before = (await page.locator("[data-slot=github-overview-main]").boundingBox())!
    release()
    await expect(calendar.locator("svg")).toBeVisible()
    const after = (await page.locator("[data-slot=github-overview-main]").boundingBox())!
    expect(Math.abs(after.y - before.y)).toBeLessThan(1)
  })
})

test.describe("pull requests and issues", () => {
  test("every pull request the account opened, by state, each opening where it can be acted on", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    // A row GitHub's opens in a new tab, which the context's route answers.
    await page
      .context()
      .route("https://github.com/**", (route) =>
        route.fulfill({ status: 200, contentType: "text/html", body: "<p>GitHub</p>" }),
      )
    await mockGitHubPage(page)
    await page.goto("/github?tab=pulls")
    const view = page.locator("[data-slot=github-inbox-pull]")
    await expect(view.getByRole("button", { name: "Created 3" })).toBeVisible()
    await expect(view.getByRole("listitem")).toHaveCount(3)
    // One in a repository the page lists opens on that repository's page.
    await expect(view.getByRole("link", { name: "Open northwind/api#412" })).toHaveAttribute(
      "href",
      "/github/northwind/api?account=1&tab=pulls&pull=412",
    )
    // One anywhere else is GitHub's.
    const [tab] = await Promise.all([
      page.waitForEvent("popup"),
      view.getByRole("button", { name: "Open torvalds/subsurface#3021" }).click(),
    ])
    await expect(tab).toHaveURL("https://github.com/torvalds/subsurface/pull/3021")
    await tab.close()

    await view.getByRole("radio", { name: "Closed" }).click()
    await expect(view.getByRole("listitem")).toHaveCount(2)
    await expect(view.getByRole("img", { name: "merged" })).toHaveCount(1)
    await expect(view.getByRole("img", { name: "closed" })).toHaveCount(1)

    await view.getByRole("button", { name: "Review requests" }).click()
    await expect(view.getByText("No closed pull requests waiting on your review.")).toBeVisible()
    await view.getByRole("radio", { name: "Open" }).click()
    await expect(view.getByRole("listitem")).toHaveCount(2)
    await expect(view.getByRole("listitem").first()).toContainText("sam-k")
  })

  test("issues have no review filter, and closed ones say how they were closed", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page)
    await page.goto("/github?tab=issues")
    const view = page.locator("[data-slot=github-inbox-issue]")
    await expect(view.getByRole("button", { name: "Review requests" })).toHaveCount(0)
    await expect(view.getByRole("listitem")).toHaveCount(1)
    await view.getByRole("button", { name: "Assigned" }).click()
    await expect(view.getByRole("listitem")).toHaveCount(2)
    await expect(view.getByRole("link", { name: "Open northwind/api#398" })).toHaveAttribute(
      "href",
      "/github/northwind/api?account=1&tab=issues&issue=398",
    )
    await view.getByRole("radio", { name: "Closed" }).click()
    await expect(view.getByRole("img", { name: "Closed as completed" })).toHaveCount(1)
    await expect(view.getByRole("img", { name: "Closed as not planned" })).toHaveCount(1)
  })

  test("the Overview's sections open the list on their own filter, kept in the address", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page)
    await page.goto("/github")
    await expect(page.locator("[data-slot=github-inbox-pull-review]")).toContainText(
      "Lazy-load the product gallery",
    )
    // "All pull requests" under Review requests is the review requests.
    const all = page.getByRole("link", { name: "All pull requests" })
    await expect(all).toHaveCount(2)
    await expect(all.nth(1)).toHaveAttribute("href", "/github?account=1&tab=pulls&filter=review")
    await all.nth(1).click()
    await expect(page).toHaveURL(/tab=pulls&filter=review$/)
    const view = page.locator("[data-slot=github-inbox-pull]")
    await expect(view.getByRole("button", { name: /^Review requests \d+$/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect(view.getByRole("listitem").first()).toContainText("Lazy-load the product gallery")

    // A state chosen here is in the address too, so a reload (or Back from
    // a request) finds the list as it was left.
    await view.getByRole("radio", { name: "Closed" }).click()
    await expect(page).toHaveURL(/filter=review&state=closed$/)
    await page.reload()
    await expect(view.getByRole("radio", { name: "Closed" })).toBeChecked()
    await expect(view.getByText("No closed pull requests waiting on your review.")).toBeVisible()

    // Another view starts on its own defaults.
    await page.getByRole("tab", { name: "Issues" }).click()
    await expect(page).toHaveURL(/tab=issues$/)
    await expect(
      page.locator("[data-slot=github-inbox-issue]").getByRole("button", { name: /^Created \d+$/ }),
    ).toHaveAttribute("aria-pressed", "true")
  })

  test("reading a list again asks GitHub, past the server's minute of memory", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const reads: string[] = []
    await mockGitHubPage(page, {
      handle: async (_route, path, url) => {
        if (/\/(pulls|issues)$/.test(path)) reads.push(url.search)
        return false
      },
    })
    await page.goto("/github?tab=pulls")
    const view = page.locator("[data-slot=github-inbox-pull]")
    await expect(view.getByRole("listitem")).toHaveCount(3)
    expect(reads.every((search) => !search.includes("refresh"))).toBe(true)
    const before = reads.length
    await view.getByRole("button", { name: "Read the pull requests again" }).click()
    await expect
      .poll(() => reads.slice(before))
      .toContainEqual(expect.stringContaining("refresh=1"))
    // A chip chosen after it is read as usual.
    const after = reads.length
    await view.getByRole("button", { name: "Mentioned" }).click()
    await expect.poll(() => reads.length).toBeGreaterThan(after)
    expect(reads.slice(after).some((search) => search.includes("refresh"))).toBe(false)
  })

  test("a closed row names who opened it, not who closed it", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page)
    await page.goto("/github?tab=pulls&state=closed")
    const merged = page
      .locator("[data-slot=github-inbox-pull]")
      .getByRole("listitem")
      .filter({ has: page.getByRole("img", { name: "merged" }) })
    await expect(merged).toHaveCount(1)
    await expect(merged).toContainText(/by \S+ was merged/)
    await expect(merged).not.toContainText(/merged .* by/)
  })

  test("the page's Refresh reads the Overview's pull requests and issues past the server's memory", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const reads: string[] = []
    await mockGitHubPage(page, {
      handle: async (_route, path, url) => {
        if (/\/(pulls|issues)$/.test(path)) reads.push(`${path}${url.search}`)
        return false
      },
    })
    await page.goto("/github")
    await expect(page.locator("[data-slot=github-inbox-pull-review]")).toContainText(
      "Lazy-load the product gallery",
    )
    // The three previews: your pull requests, review requests, assigned issues.
    await expect.poll(() => reads.length).toBeGreaterThanOrEqual(3)
    expect(reads.some((read) => read.includes("refresh"))).toBe(false)
    const before = reads.length
    await identity(page).getByRole("button", { name: "More actions for GitHub" }).click()
    await page.getByRole("menuitem", { name: "Refresh", exact: true }).click()
    await expect
      .poll(() =>
        [
          ...new Set(
            reads
              .slice(before)
              .filter((read) => read.includes("refresh=1"))
              .map((read) => read.replace(/\?.*filter=([a-z]+).*/, " $1")),
          ),
        ].sort(),
      )
      .toEqual([
        "/github/connections/1/issues assigned",
        "/github/connections/1/pulls created",
        "/github/connections/1/pulls review",
      ])
  })

  test("a list whose first page failed keeps nothing of the filter read before it", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGitHubPage(page, {
      handle: async (route, path, url) => {
        if (!/\/pulls$/.test(path) || url.searchParams.get("filter") !== "mentioned") return false
        await route.fulfill({
          status: 502,
          contentType: "application/json",
          body: JSON.stringify({ error: "github_unavailable", message: "GitHub did not answer" }),
        })
        return true
      },
    })
    await page.goto("/github?tab=pulls")
    const view = page.locator("[data-slot=github-inbox-pull]")
    await expect(view.getByRole("button", { name: "Created 3" })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await view.getByRole("button", { name: "Mentioned" }).click()
    // Once its read has failed, the chosen chip does not carry Created's total.
    await expect(view.getByRole("button", { name: "Try again" })).toBeVisible()
    const mentioned = view.getByRole("button", { name: /^Mentioned/ })
    await expect(mentioned).toHaveAttribute("aria-pressed", "true")
    await expect(view.getByRole("listitem")).toHaveCount(0)
    await expect(mentioned).toHaveAccessibleName("Mentioned")
  })

  for (const width of [1440, 390]) {
    test(`every row is one height at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockGitHubPage(page)
      await page.goto("/github?tab=pulls")
      const items = page.locator("[data-slot=github-inbox-pull]").getByRole("listitem")
      await expect(items).toHaveCount(3)
      const heights = await items.evaluateAll((all) =>
        all.map((item) => Math.round(item.getBoundingClientRect().height)),
      )
      expect(new Set(heights).size, `heights ${heights.join(", ")}`).toBe(1)
    })
  }
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
  for (const view of ["/github", REPOSITORIES, "/github?tab=pulls", "/github?tab=issues"]) {
    await page.goto(view)
    await expect(page.getByRole("tab", { selected: true })).toBeVisible()
    await page.waitForLoadState("networkidle")
    const sideways = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
    )
    expect(sideways, view).toBe(false)
  }
  const command = identity(page).getByRole("button", { name: "New repository" })
  await expect(command).toBeVisible()
  const box = await command.boundingBox()
  expect(Math.round(box!.width)).toBeLessThanOrEqual(32)
})
