import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The Git page's claims, checked in a browser.
 *
 * Two of them were bugs a unit test could not see, because each was a true
 * answer rendered so that it read as a false one:
 *
 *   a file staged and then edited again was listed once, under "ready to
 *   commit", so the second edit was invisible until the commit went out
 *   without it;
 *
 *   the list page said "3 changes" and nothing about which of the thirty
 *   repositories were behind their remote, so the question the page is
 *   opened with — is anything waiting — was answered by reading a column.
 *
 * The third is the typed phrase: discarding a file has to send the server
 * exactly what it asked for, encoded the way the client promises.
 */

const now = new Date().toISOString()

const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

const app = {
  path: "/srv/app",
  name: "app",
  branch: "main",
  upstream: "origin/main",
  remote: "https://github.com/acme/app.git",
  head: "abc1234",
  subject: "Ship the thing",
  author: "Ada",
  commitAt: now,
  dirty: true,
  changes: 3,
  staged: 2,
  untracked: 1,
  conflicts: 0,
  ahead: 1,
  behind: 2,
  detached: false,
}

const lib = {
  path: "/srv/lib",
  name: "lib",
  branch: "main",
  upstream: "origin/main",
  head: "def5678",
  subject: "Tidy",
  author: "Ada",
  commitAt: now,
  dirty: false,
  changes: 0,
  staged: 0,
  untracked: 0,
  conflicts: 0,
  ahead: 0,
  behind: 0,
  detached: false,
}

/** a.txt is staged *and* edited again; new.txt has never been committed. */
const status = {
  repo: app,
  files: [
    { path: "a.txt", index: "M", worktree: "M", label: "modified", staged: true, unstaged: true },
    { path: "b.txt", index: "A", worktree: "", label: "added", staged: true, unstaged: false },
    {
      path: "new.txt",
      index: "?",
      worktree: "?",
      label: "untracked",
      staged: false,
      unstaged: true,
    },
  ],
  clean: false,
  stashes: 1,
  identity: { name: "Ada", email: "ada@example.com" },
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

type Seen = { method: string; path: string; headers: Record<string, string> }

async function mockGit(page: Page) {
  const seen: Seen[] = []
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace(/^\/api\/v1/, "")
    seen.push({ method: req.method(), path, headers: req.headers() })
    switch (path) {
      case "/auth/session":
        return json(route, user)
      case "/git/":
        return json(route, { available: true, repos: [app, lib] })
      case "/git/github/":
        return json(route, { available: true, account: { loggedIn: false, gitConfigured: false } })
      case "/git/status":
        return json(route, status)
      case "/git/stashes":
        return json(route, [
          { index: 0, sha: "s0", at: now, branch: "main", message: "parked work" },
        ])
      case "/git/diff":
        return json(route, { diff: "diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-one\n+two\n" })
      case "/git/discard":
      case "/git/stage":
      case "/git/unstage":
        return json(route, { command: "git", output: "Done.", ok: true })
      case "/files/list":
        return json(route, { path: "/srv/app", entries: [] })
      case "/updates/self":
        return json(route, { current: "0.6.7", latest: "0.6.7" })
    }
    return json(route, [])
  })
  return seen
}

test("the list answers what is waiting before the rows are read", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockGit(page)
  await page.goto("/git")
  await expect(page.getByRole("heading", { name: "Git" })).toBeVisible()

  // The readings are on the chips, which is where the four stat tiles went:
  // each one names a state and carries its count, and pressing it is how the
  // answer is acted on. A state nothing is in gets no chip at all.
  await expect(page.getByRole("button", { name: "Uncommitted 1" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Behind 1" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Unpushed 1" })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Detached/ })).toHaveCount(0)

  // The urgent one is first without being asked: `app` is dirty, `lib` is not.
  const names = page.getByRole("button", { name: /^(app|lib)$/ })
  await expect(names.first()).toHaveText("app")
  await expect(page.getByText("Needs attention")).toBeVisible()

  await page.getByRole("button", { name: "Behind 1" }).click()
  await expect(page.getByRole("button", { name: "app", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "lib", exact: true })).toHaveCount(0)

  // Opening a repository is a link, not a state: the address carries it.
  await page.getByRole("button", { name: "app", exact: true }).click()
  await expect(page).toHaveURL(/repo=%2Fsrv%2Fapp/)
  await expect(page.getByRole("button", { name: "Back to repositories" })).toBeVisible()
})

test("a file staged and edited again is listed on both sides", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockGit(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")

  await expect(page.getByText("Ready to commit")).toBeVisible()
  await expect(page.getByText("Changes", { exact: true }).first()).toBeVisible()
  // Once under each heading — the second edit is no longer hidden behind
  // the first — while the one-sided files appear once.
  await expect(page.getByRole("button", { name: "a.txt", exact: true })).toHaveCount(2)
  await expect(page.getByRole("button", { name: "b.txt", exact: true })).toHaveCount(1)
  await expect(page.getByRole("button", { name: "new.txt", exact: true })).toHaveCount(1)
  // An untracked file's discard is a deletion, and is called one.
  await expect(page.getByRole("button", { name: "Delete", exact: true })).toHaveCount(1)
  // What was set aside is on the same screen as what is not yet committed.
  await expect(page.getByText("Set aside")).toBeVisible()
  await expect(page.getByRole("button", { name: /parked work/ })).toBeVisible()
  // Who the commit is recorded as, beside the button that records it.
  await expect(page.getByRole("button", { name: /^as Ada/ })).toBeVisible()
})

test("discarding a file sends the typed phrase the server demands", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const seen = await mockGit(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await expect(page.getByRole("button", { name: "a.txt", exact: true })).toHaveCount(2)

  await page.getByRole("button", { name: "Discard", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("discard changes")
  // The confirm button stays inert until the phrase is typed exactly.
  await expect(dialog.getByRole("button", { name: "Discard" })).toBeDisabled()
  await dialog.getByPlaceholder("Type the phrase above").fill("discard changes")
  await dialog.getByRole("button", { name: "Discard" }).click()

  await expect.poll(() => seen.some((r) => r.path === "/git/discard")).toBe(true)
  const discard = seen.find((r) => r.path === "/git/discard")!
  expect(discard.method).toBe("POST")
  expect(discard.headers["x-confirm"]).toBe("discard%20changes")
  expect(discard.headers["x-confirm-encoding"]).toBe("uri")
})

/**
 * Dragging a column wider has a floor, and the floor is the other column.
 *
 * The widths were stored raw: `usePanelSize` deliberately does not clamp
 * ("only the page knows what else is on the row") and this page did no
 * clamping at all, so the tree and the changes list could each be dragged to
 * any width at all and the preview — `flex-1 min-w-0`, so it yields to
 * everything — was squeezed to nothing and the diff disappeared.
 */
test("a column cannot be dragged over the preview", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockGit(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")

  const preview = page.locator("[data-slot=git-preview]")
  await expect(preview).toBeVisible()
  const before = (await preview.boundingBox())!.width

  const handle = page.getByRole("separator", { name: "Changes panel width" })
  const box = (await handle.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + 2000, box.y + box.height / 2, { steps: 8 })
  await page.mouse.up()

  const after = (await preview.boundingBox())!.width
  // The drag did something — and stopped where the diff still has room.
  expect(after).toBeLessThan(before)
  expect(after).toBeGreaterThan(320)
  // The separator reports the width it actually has, not the one asked for.
  expect(Number(await handle.getAttribute("aria-valuenow"))).toBeLessThanOrEqual(640)
})

/**
 * And a width stored on a wider monitor gives way on arrival, not a frame
 * later. The fit is applied from a ref callback rather than an effect for
 * exactly this: an effect runs after the browser has painted, so the collapsed
 * layout would be drawn once before the correction.
 */
test("a width left over from a wider screen is fitted on arrival", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockGit(page)
  await page.addInitScript(() => {
    window.localStorage.setItem(
      "jd.panel.sizes",
      JSON.stringify({ "git.tree": 2400, "git.work": 2400 }),
    )
  })
  await page.goto("/git?repo=%2Fsrv%2Fapp")

  const preview = page.locator("[data-slot=git-preview]")
  await expect(preview).toBeVisible()
  expect((await preview.boundingBox())!.width).toBeGreaterThan(320)
})

/**
 * The design system's structural rules, on both faces of the page: every
 * icon-only control carries an accessible name, and nothing is a filled pill.
 */
for (const path of ["/git", "/git?repo=%2Fsrv%2Fapp"] as const) {
  test(`${path} labels its icon-only controls and draws no pill`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockGit(page)
    await page.goto(path)
    await expect(
      page.locator("[data-slot=page-header], [data-slot=pane-header]").first(),
    ).toBeVisible()

    const unnamed = await page.evaluate(() => {
      const bad: string[] = []
      for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
        if (el.offsetParent === null && el.getAttribute("aria-hidden") !== "true") continue
        const text = (el.textContent ?? "").trim()
        if (text.length > 0) continue
        const named =
          el.getAttribute("aria-label") ||
          el.getAttribute("aria-labelledby") ||
          el.querySelector(".sr-only")
        if (!named) bad.push(el.outerHTML.slice(0, 160))
      }
      return bad
    })
    expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])

    const pills = await page.evaluate(() => {
      const bad: string[] = []
      for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
        const s = getComputedStyle(el)
        const r = parseFloat(s.borderTopLeftRadius)
        const h = el.getBoundingClientRect().height
        if (!h || h > 32 || r < h / 2) continue
        const filled =
          s.backgroundColor !== "rgba(0, 0, 0, 0)" && s.backgroundColor !== "transparent"
        const text = (el.textContent ?? "").trim()
        if (filled && text.length > 0) bad.push(el.outerHTML.slice(0, 140))
      }
      return bad
    })
    expect(pills, `fully rounded filled chips on ${path}`).toEqual([])
  })
}
