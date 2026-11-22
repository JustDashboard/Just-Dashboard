import { expect, test, type Locator } from "@playwright/test"
import { HEAD_SHA, mockGitHubRepo } from "./github-repo-fixture"

/**
 * One GitHub repository's page (`/github/[owner]/[repo]`), against the
 * shared GitHub fixture: the detail-with-rail frame, each view, and every
 * control the repository's abilities hold back saying why.
 */

const API = "/github/northwind/api?account=1"

async function heights(rows: Locator) {
  return rows.evaluateAll((all) =>
    all.map((each) => Math.round(each.getBoundingClientRect().height)),
  )
}

async function oneHeight(rows: Locator) {
  const seen = await heights(rows)
  expect(seen.length).toBeGreaterThan(0)
  expect(new Set(seen).size, `row heights ${seen.join(", ")}`).toBe(1)
}

test.describe("the frame", () => {
  test("the repositories stand beside the page, the one on screen current, every row one height", async ({
    page,
  }) => {
    await mockGitHubRepo(page)
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(API)

    const rail = page.locator("[data-slot=github-repo-rail]")
    await expect(rail).toBeVisible()
    const rows = rail.getByRole("list", { name: "Repositories to open" }).getByRole("link")
    await expect(rows).toHaveCount(9)
    const current = rail.getByRole("link", { name: "northwind/api", exact: true })
    await expect(current).toHaveAttribute("aria-current", "page")
    await oneHeight(rows)
    await expect(rail.getByText("9 repositories · recently updated")).toBeVisible()

    const identity = page.locator("[data-slot=github-repo-identity]")
    await expect(identity).toContainText("api")
    await expect(identity).toContainText("private")
    await expect(identity).toContainText("main")
    // A checkout of it is on this server, so the command opens it in Local Git.
    await expect(identity.getByRole("link", { name: "Open in Local Git" })).toHaveAttribute(
      "href",
      "/git?repo=%2Fsrv%2Fnorthwind-api",
    )
  })

  test("Alt+↓ walks the list with the view carried", async ({ page }) => {
    await mockGitHubRepo(page)
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(`${API}&tab=commits`)
    await expect(page.locator("[data-slot=github-commits]")).toBeVisible()

    await page.keyboard.press("Alt+ArrowDown")
    await expect(page).toHaveURL(/\/github\/mira-dev\/dotfiles\?account=1&tab=commits$/, {
      timeout: 15_000,
    })
    const rail = page.locator("[data-slot=github-repo-rail]")
    await expect(
      rail.getByRole("link", { name: "mira-dev/dotfiles", exact: true }),
    ).toHaveAttribute("aria-current", "page")
    await page.getByRole("button", { name: "Previous repository" }).click()
    await expect(page).toHaveURL(/\/github\/northwind\/api\?account=1&tab=commits$/)
  })

  test("the filters the list was left on narrow the rail, and Clear filters brings the rest back", async ({
    page,
  }) => {
    await mockGitHubRepo(page)
    await page.addInitScript(() =>
      sessionStorage.setItem(
        "jd.session.state.entry.github.list.filters",
        JSON.stringify({ query: "", state: "archived", owner: "", language: "", sort: "updated" }),
      ),
    )
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(API)

    const rail = page.locator("[data-slot=github-repo-rail]")
    const rows = rail.getByRole("list", { name: "Repositories to open" }).getByRole("link")
    await expect(rows).toHaveCount(2)
    await expect(rows.first()).toHaveAttribute("aria-label", "northwind/api")
    await expect(rows.nth(1)).toHaveAttribute("aria-label", "acme-labs/legacy-portal")
    await expect(rail.getByText("1 of 9 · archived")).toBeVisible()
    await rail.getByRole("button", { name: "Clear filters" }).click()
    await expect(rows).toHaveCount(9)
  })

  test("on a phone there is no rail and nothing scrolls sideways", async ({ page }) => {
    await mockGitHubRepo(page)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(API)
    await expect(page.locator("[data-slot=github-repo-identity]")).toContainText("api")
    await expect(page.locator("[data-slot=github-repo-rail]")).toBeHidden()
    for (const tab of ["overview", "pulls", "settings"]) {
      await page.goto(`${API}&tab=${tab}`)
      await expect(page.locator("[data-slot=github-repo-identity]")).toBeVisible()
      const sideways = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
      )
      expect(sideways, `${tab} scrolls sideways`).toBe(false)
    }
  })

  test("a repository no connection reaches says so, with the way back", async ({ page }) => {
    await mockGitHubRepo(page)
    await page.goto("/github/nobody/nothing?account=1")
    await expect(page.getByText("nobody/nothing is not reachable")).toBeVisible()
    await expect(page.getByRole("link", { name: "Back to GitHub" })).toHaveAttribute(
      "href",
      "/github?account=1",
    )
  })

  test("without an account in the address the first connection that lists it is used", async ({
    page,
  }) => {
    await mockGitHubRepo(page)
    await page.goto("/github/northwind/api")
    await expect(page.locator("[data-slot=github-repo-identity]")).toContainText("private")
  })
})

test.describe("the views", () => {
  test("Overview wires the repository to its checkout and says what you can do", async ({
    page,
  }) => {
    await mockGitHubRepo(page)
    await page.goto(API)
    await expect(
      page.getByRole("link", { name: "Open /srv/northwind-api in Local Git" }),
    ).toBeVisible()
    const abilities = page.locator("[data-slot=github-abilities]")
    await expect(abilities.getByRole("listitem")).toHaveCount(8)
    await expect(page.getByText("for selected repositories only")).toBeVisible()
  })

  test("a repository with no checkout offers Clone", async ({ page }) => {
    await mockGitHubRepo(page)
    await page.goto("/github/mira-dev/notes?account=1")
    await expect(page.locator("[data-slot=github-no-checkout]")).toContainText("no checkout")
    await expect(
      page
        .locator("[data-slot=github-repo-identity]")
        .getByRole("button", { name: "Clone to server" }),
    ).toBeVisible()
  })

  test("Branches: the default and protected marked, protection in a popover, one height", async ({
    page,
  }) => {
    await mockGitHubRepo(page)
    await page.goto(`${API}&tab=branches`)
    const list = page.locator("[data-slot=github-branches]")
    await expect(list.getByRole("listitem")).toHaveCount(3)
    // The default branch comes first.
    await expect(list.getByRole("listitem").first()).toHaveAttribute("data-branch", "main")
    await oneHeight(list.locator("[data-slot=row] > *"))
    await list.getByRole("button", { name: "Protection of main" }).click()
    const rules = page.locator("[data-slot=github-protection]")
    await expect(rules).toContainText("1 approving review before merging")
    await expect(rules).toContainText("Checks must pass: build, test")
    await expect(rules).toContainText("Deletion is blocked")
  })

  test("a branch is created from another and deleted, and the default is refused", async ({
    page,
  }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=branches`)
    const list = page.locator("[data-slot=github-branches]")
    await expect(list.getByRole("listitem")).toHaveCount(3)

    await page.getByRole("button", { name: "New branch" }).click()
    const dialog = page.getByRole("dialog", { name: "New branch" })
    await dialog.getByLabel("Name").fill("fix/rounding")
    await dialog.getByRole("button", { name: "Create" }).click()
    await expect(dialog).toBeHidden()
    expect(mock.calls.find((c) => c.method === "POST")).toMatchObject({
      path: "/branches",
      body: { name: "fix/rounding", from: "main" },
    })

    // The default branch's delete is off, with the reason.
    const held = list.locator("[data-branch=main] [data-slot=blocked]")
    await expect(held).toHaveAttribute("aria-label", /default branch cannot be deleted/)
    await expect(list.getByRole("button", { name: "Delete main" })).toBeDisabled()

    await list.getByRole("button", { name: "Delete feature/invoices" }).click()
    await page
      .getByRole("dialog", { name: "Delete feature/invoices" })
      .getByRole("button", { name: "Delete" })
      .click()
    await expect
      .poll(() => mock.calls.find((c) => c.method === "DELETE"))
      .toMatchObject({
        path: "/branches",
        query: { name: "feature/invoices" },
      })
  })

  test("Commits: the subject line, the author, the verified mark and older pages", async ({
    page,
  }) => {
    await mockGitHubRepo(page)
    await page.goto(`${API}&tab=commits`)
    const list = page.locator("[data-slot=github-commits]")
    await expect(list.getByRole("listitem")).toHaveCount(2)
    await expect(list).toContainText("Bill annual plans on the renewal date")
    await expect(list).not.toContainText("The old date drifted")
    await expect(list.getByLabel("Verified signature")).toHaveCount(1)
    await page.getByRole("button", { name: "Show older" }).click()
    await expect(page.getByRole("button", { name: "Show older" })).toBeHidden()
  })

  test("Pull requests: chips, cards of one height, a request opened in place and merged with the sha shown", async ({
    page,
  }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=pulls`)
    const list = page.locator("[data-slot=github-pulls]")
    await expect(list.getByRole("listitem")).toHaveCount(2)
    await oneHeight(list.locator("[data-slot=choice-row]"))
    await page.getByRole("button", { name: "Merged", exact: true }).click()
    await expect(list.getByRole("listitem")).toHaveCount(1)
    await expect(list).toContainText("Move to Go 1.26")
    await page.getByRole("button", { name: "Open", exact: true }).click()

    await list.getByRole("button", { name: "Open pull request #42" }).click()
    await expect(page).toHaveURL(/tab=pulls&pull=42$/)
    const detail = page.locator("[data-slot=github-pull]")
    await expect(detail.getByText("Invoices go out as")).toBeVisible()
    await expect(detail.locator("[data-slot=github-checks]").getByRole("listitem")).toHaveCount(2)
    await expect(detail.locator("[data-slot=github-reviews]")).toContainText("approved")

    await detail.getByRole("button", { name: "Merge" }).click()
    const dialog = page.getByRole("dialog", { name: "Merge #42" })
    await dialog.getByRole("button", { name: "Merge", exact: true }).click()
    await expect(dialog).toBeHidden()
    expect(mock.calls.find((c) => c.path === "/pulls/42/merge")?.body).toEqual({
      method: "squash",
      sha: HEAD_SHA,
    })

    await detail.getByRole("button", { name: "Pull requests" }).click()
    await expect(page).toHaveURL(/tab=pulls$/)
  })

  test("Issues: by state and label, opened in place, closed as not planned", async ({ page }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=issues`)
    const list = page.locator("[data-slot=github-issues]")
    await expect(list.getByRole("listitem")).toHaveCount(2)
    await oneHeight(list.locator("[data-slot=choice-row]"))
    await page.getByRole("combobox", { name: "Label" }).click()
    await page.getByRole("option", { name: "bug" }).click()
    await expect(list.getByRole("listitem")).toHaveCount(1)

    await list.getByRole("button", { name: "Open issue #51" }).click()
    const detail = page.locator("[data-slot=github-issue]")
    await expect(detail.getByText("for a GBP refund")).toBeVisible()
    await detail.getByRole("button", { name: "Close", exact: true }).click()
    await page.getByRole("menuitem", { name: "As not planned" }).click()
    await expect
      .poll(() => mock.calls.find((c) => c.method === "PATCH")?.body)
      .toEqual({
        state: "closed",
        stateReason: "not_planned",
      })
  })

  test("a new issue sends its title, body and labels", async ({ page }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=issues`)
    await page.getByRole("button", { name: "New issue" }).click()
    const dialog = page.getByRole("dialog", { name: "New issue" })
    await dialog.getByLabel("Title").fill("Totals round down")
    await dialog.getByLabel("Description").fill("By a cent.")
    await dialog.getByText("billing", { exact: true }).click()
    await dialog.getByRole("button", { name: "Open", exact: true }).click()
    await expect(dialog).toBeHidden()
    expect(mock.calls.find((c) => c.path === "/issues")?.body).toEqual({
      title: "Totals round down",
      body: "By a cent.",
      labels: ["billing"],
    })
    await expect(page).toHaveURL(/issue=60$/)
  })

  test("Actions: Run only for a dispatchable workflow, with its inputs; runs re-run and cancel", async ({
    page,
  }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=actions`)
    const workflows = page.locator("[data-slot=github-workflows]")
    await expect(workflows.getByRole("listitem")).toHaveCount(2)
    await expect(workflows.getByRole("button", { name: /^Run / })).toHaveCount(1)
    await expect(
      workflows.locator("[data-workflow='.github/workflows/ci.yml'] [data-slot=no-dispatch]"),
    ).toBeVisible()

    const runs = page.locator("[data-slot=github-runs]")
    await expect(runs.getByRole("listitem")).toHaveCount(3)
    await oneHeight(runs.locator("[data-slot=row] > *"))
    await expect(runs.getByText("running", { exact: true })).toBeVisible()
    await expect(runs.getByText("failed", { exact: true })).toBeVisible()
    await expect(runs.getByRole("button", { name: "Cancel run 103" })).toBeVisible()

    await workflows.getByRole("button", { name: "Run Deploy" }).click()
    const dialog = page.getByRole("dialog", { name: "Run Deploy" })
    await expect(dialog.getByRole("switch")).toBeChecked()
    await dialog.getByLabel("note").fill("hotfix")
    await dialog.getByRole("button", { name: "Run", exact: true }).click()
    await expect(dialog).toBeHidden()
    expect(mock.calls.find((c) => c.path === "/actions/workflows/701/dispatch")?.body).toEqual({
      ref: "main",
      inputs: { environment: "staging", dry_run: "true", note: "hotfix" },
    })

    await runs.getByRole("button", { name: "Re-run run 102" }).click()
    await page.getByRole("menuitem", { name: "Re-run failed jobs" }).click()
    await expect
      .poll(() => mock.calls.find((c) => c.path === "/actions/runs/9102/rerun")?.body)
      .toEqual({
        failedOnly: true,
      })
  })

  test("Collaborators: roles, invitations, and the unreadable state", async ({ page }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=collaborators`)
    const people = page.locator("[data-slot=github-collaborators]")
    await expect(people.getByRole("listitem")).toHaveCount(2)
    await expect(page.locator("[data-slot=github-invitations]")).toContainText("lee-ops")
    await people.getByRole("combobox", { name: "Role of sam-k" }).click()
    await page.getByRole("option", { name: "Maintain" }).click()
    await expect
      .poll(() => mock.calls.find((c) => c.method === "PUT"))
      .toMatchObject({
        path: "/collaborators/sam-k",
        body: { role: "maintain" },
      })

    await page.goto("/github/acme-labs/design-system?account=1&tab=collaborators")
    await expect(page.locator("[data-slot=github-collaborators-unreadable]")).toContainText(
      "push access or more",
    )
    await expect(page.getByRole("button", { name: "Invite" })).toHaveCount(0)
  })
})

test.describe("abilities", () => {
  test("a read-only role sees every control off with its reason", async ({ page }) => {
    await mockGitHubRepo(page)
    await page.goto("/github/acme-labs/design-system?account=1&tab=branches")
    const held = page.locator("[data-slot=blocked]").filter({ hasText: "New branch" })
    await expect(held).toHaveAttribute(
      "aria-label",
      /Your role on acme-labs\/design-system is read/,
    )
    await expect(page.getByRole("button", { name: "New branch" })).toBeDisabled()

    await page.goto("/github/acme-labs/design-system?account=1&tab=settings")
    const form = page.getByRole("form", { name: "Repository settings" })
    await expect(form.getByLabel("Description")).toBeDisabled()
    await expect(form).toContainText("this needs admin")
    // GitHub states the merge settings only to people who may change them.
    await expect(form.getByText("Squash merging")).toHaveCount(0)
  })

  test("an archived repository without the administration grant holds its danger verbs with the reason", async ({
    page,
  }) => {
    await mockGitHubRepo(page)
    await page.goto("/github/acme-labs/legacy-portal?account=1")
    const identity = page.locator("[data-slot=github-repo-identity]")
    await expect(identity).toContainText("archived")
    await page.getByRole("button", { name: "More actions for acme-labs/legacy-portal" }).click()
    const remove = page.getByRole("menuitem", { name: "Delete" })
    await expect(remove).toHaveAttribute("aria-disabled", "true")
    await expect(remove).toHaveAttribute("aria-description", /not granted administration/)
    await remove.hover()
    await expect(page.getByRole("tooltip")).toContainText("not granted administration")
    await remove.click({ force: true })
    await expect(page.getByRole("dialog")).toHaveCount(0)
  })
})

test.describe("settings and the danger zone", () => {
  test("a save sends only the changed field", async ({ page }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=settings`)
    const form = page.getByRole("form", { name: "Repository settings" })
    await form.getByLabel("Description").fill("Orders, billing and refunds")
    await form.getByRole("switch", { name: "Wiki" }).click()
    await expect(page.getByText("2 unsaved changes")).toBeVisible()
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect
      .poll(() => mock.calls.find((c) => c.method === "PATCH")?.body)
      .toEqual({
        description: "Orders, billing and refunds",
        hasWiki: true,
      })
  })

  test("a rename lands on the new address", async ({ page }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=settings`)
    const form = page.getByRole("form", { name: "Repository settings" })
    await form.getByLabel("Name").fill("api-v2")
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page).toHaveURL(/\/github\/northwind\/api-v2\?account=1&tab=settings$/)
    expect(mock.calls.find((c) => c.method === "PATCH")?.body).toEqual({ name: "api-v2" })
    await expect(page.locator("[data-slot=github-repo-identity]")).toContainText("api-v2")
  })

  test("making it public says what that shows, then sends the change", async ({ page }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=settings`)
    await page.getByRole("button", { name: "Make public" }).click()
    const dialog = page.getByRole("dialog", { name: "Make api public" })
    await expect(dialog).toContainText("whole history")
    await dialog.getByRole("button", { name: "Make public" }).click()
    await expect
      .poll(() => mock.calls.find((c) => c.path === "/visibility")?.body)
      .toEqual({
        visibility: "public",
      })
  })

  test("deleting asks for the full name, sends it as X-Confirm and returns to the list", async ({
    page,
  }) => {
    const mock = await mockGitHubRepo(page)
    await page.goto(`${API}&tab=settings`)
    await page.getByRole("button", { name: "Delete", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Delete northwind/api" })
    await expect(dialog).toContainText("cannot be undone from here")
    const confirm = dialog.getByRole("button", { name: "Delete" })
    await expect(confirm).toBeDisabled()
    await dialog.getByPlaceholder("Type the phrase above").fill("northwind/api")
    await confirm.click()
    await expect(page).toHaveURL(/\/github\?account=1$/)
    expect(mock.calls.find((c) => c.method === "DELETE" && c.path === "")?.confirm).toBe(
      "northwind/api",
    )
  })
})
