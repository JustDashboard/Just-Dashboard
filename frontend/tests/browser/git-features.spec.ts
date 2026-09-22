import { expect, test, type Page } from "@playwright/test"

const now = "2026-09-22T12:00:00Z"
const sha = "a".repeat(40)
const previous = "b".repeat(40)
const repo = {
  path: "/srv/app",
  name: "app",
  branch: "main",
  head: sha,
  upstream: "origin/main",
  remote: "https://github.com/acme/app.git",
  dirty: false,
  changes: 0,
  staged: 0,
  untracked: 0,
  conflicts: 0,
  ahead: 0,
  behind: 0,
  detached: false,
  author: "Ada",
  subject: "Initial version",
  commitAt: now,
}
const commit = {
  sha,
  short: sha.slice(0, 7),
  subject: "Initial version",
  author: "Ada",
  email: "ada@example.com",
  at: now,
  insertions: 1,
  deletions: 0,
  files: 1,
  isMerge: false,
  parents: [],
}
const change = { path: "a.txt", status: "modified", insertions: 1, deletions: 1 }
const branch = {
  name: "main",
  current: true,
  remote: false,
  ahead: 0,
  behind: 0,
  upstream: "origin/main",
  subject: "Initial version",
}
type Request = { path: string; body: Record<string, unknown>; query: URLSearchParams }

async function fixture(
  page: Page,
  readonly = false,
  mode: "normal" | "conflict" | "partial" | "staged" | "github" | "gitlab" | "gitea" = "normal",
) {
  const requests: Request[] = []
  let resolved = false
  let operation = mode === "conflict" ? "merge" : ""
  let patchVersion = 0
  let forge = {
    configured: mode === "gitlab" || mode === "gitea",
    kind: mode === "gitea" ? "gitea" : "gitlab",
    url: "https://git.example.com",
    project: "acme/app",
    login: "ada",
    defaultBranch: "main",
  }
  const pull = {
    number: 7,
    title: "Review this change",
    body: "Description",
    state: "open",
    head: "feature",
    base: "main",
    sha,
    headSha: sha,
    baseSha: previous,
    url: "https://github.com/acme/app/pull/7",
    draft: false,
    files: 1,
    additions: 1,
    deletions: 1,
    comments: 1,
  }
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const body = req.postDataJSON() ?? {}
    requests.push({ path, body, query: url.searchParams })
    if (path === "/git/conflict/resolve" && req.method() === "POST") resolved = true
    if (
      (path === "/git/operation/continue" || path === "/git/operation/abort") &&
      req.method() === "POST"
    )
      operation = ""
    if (path === "/git/patch/stage" && req.method() === "POST") patchVersion++
    let response: unknown = []
    if (req.method() === "POST") response = { command: "git", output: "Done.", ok: true }
    else
      switch (path) {
        case "/auth/session":
          response = {
            authenticated: true,
            needsTotp: false,
            needsEnrollment: false,
            require2fa: false,
            capabilities: readonly
              ? ["read"]
              : [
                  "read",
                  "file.write",
                  "service.control",
                  "destructive",
                  "terminal",
                  "system.admin",
                ],
            user: {
              id: 1,
              username: "operator",
              role: readonly ? "readonly" : "admin",
              totpEnabled: true,
              disabled: false,
              mustChangePassword: false,
              createdAt: now,
            },
          }
          break
        case "/updates/self":
          response = { current: "0.6.7", latest: "0.6.7" }
          break
        case "/git/":
          response = { available: true, repos: [repo] }
          break
        case "/git/status":
          response = {
            repo,
            operation,
            files:
              mode === "conflict" && !resolved
                ? [
                    {
                      path: "a.txt",
                      index: "U",
                      worktree: "U",
                      label: "conflicted",
                      staged: false,
                      unstaged: true,
                    },
                  ]
                : mode === "partial" || mode === "staged"
                  ? [
                      {
                        path: "a.txt",
                        index: mode === "staged" ? "M" : "",
                        worktree: mode === "partial" ? "M" : "",
                        label: "modified",
                        staged: mode === "staged",
                        unstaged: mode === "partial",
                      },
                    ]
                  : [],
            clean: true,
            stashes: 0,
            identity: { name: "Ada", email: "ada@example.com" },
          }
          break
        case "/git/conflict":
          response = {
            file: "a.txt",
            version: "conflict0",
            operation: "merge",
            editable: true,
            base: { present: true, content: "one\n", binary: false },
            ours: { present: true, content: "current\n", binary: false },
            theirs: { present: true, content: "incoming\n", binary: false },
            result: "<<<<<<< HEAD\ncurrent\n=======\nincoming\n>>>>>>> feature\n",
          }
          break
        case "/git/patch":
          response = {
            file: "a.txt",
            staged: mode === "staged",
            version: `patch-${patchVersion}`,
            lines: [5, 6],
            body: "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,3 @@\n one\n+second\n+third\n",
          }
          break
        case "/git/github/":
          response = {
            available: true,
            account: { loggedIn: mode === "github", login: "ada", gitConfigured: true },
          }
          break
        case "/git/github/repo":
          response = {
            nameWithOwner: "acme/app",
            defaultBranch: "main",
            url: "https://github.com/acme/app",
            private: true,
          }
          break
        case "/git/github/pulls":
          response = [pull]
          break
        case "/git/github/pulls/7":
          response = pull
          break
        case "/git/github/pulls/7/files":
          response = {
            headSha: sha,
            hasMore: false,
            limited: false,
            files: [
              {
                filename: "a.txt",
                status: "modified",
                additions: 1,
                deletions: 1,
                patch: "@@ -1 +1 @@\n-old\n+reviewed",
              },
            ],
          }
          break
        case "/git/github/pulls/7/conversation":
          response = {
            entries: [
              { id: 1, kind: "comments", author: "Ada", body: "Existing conversation", at: now },
            ],
            hasMore: false,
          }
          break
        case "/git/github/runs":
          response = [
            {
              id: 11,
              name: "Build",
              workflow: "Build workflow",
              status: "completed",
              conclusion: "failure",
              url: "https://github.com/acme/app/actions/runs/11",
            },
          ]
          break
        case "/git/github/runs/11":
          response = {
            name: "Build workflow",
            status: "completed",
            conclusion: "failure",
            url: "https://github.com/acme/app/actions/runs/11",
            jobs: [
              {
                databaseId: 12,
                name: "Unit tests",
                conclusion: "failure",
                status: "completed",
                steps: [
                  { number: 1, name: "Run tests", conclusion: "failure", status: "completed" },
                ],
              },
            ],
          }
          break
        case "/git/github/runs/11/log":
          response = { body: "Test failed: expected 42, received 1" }
          break
        case "/git/rebase/plan":
          response = {
            head: sha,
            base: previous,
            items: [
              { sha, action: "pick", message: "First local commit" },
              { sha: "c".repeat(40), action: "pick", message: "Second local commit" },
            ],
          }
          break
        case "/git/submodules":
          response = [
            {
              name: "lib",
              path: "vendor/lib",
              url: "https://example.com/lib.git",
              initialized: true,
              head: sha,
              expected: sha,
              dirty: false,
            },
          ]
          break
        case "/git/lfs":
          response = {
            available: true,
            version: "git-lfs/3.6.1",
            status: "Objects to be committed",
            files: "asset.bin",
            patterns: "*.bin",
          }
          break
        case "/git/patch/export":
          response = {
            body: "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n",
          }
          break
        case "/git/forge/":
          response = forge
          break
        case "/git/forge/requests":
          response = { requests: [pull], hasMore: false }
          break
        case "/git/forge/requests/7":
          response = pull
          break
        case "/git/forge/requests/7/files":
          response = {
            files: [{ path: "a.txt", diff: "@@ -1 +1 @@\n-old\n+provider", omitted: false }],
            hasMore: false,
          }
          break
        case "/git/forge/requests/7/conversation":
          response = {
            comments: [{ id: 1, author: "Ada", body: "Provider discussion", at: now }],
            hasMore: false,
          }
          break
        case "/git/roots":
          response = { roots: ["/srv"] }
          break
        case "/git/log":
          response = [commit]
          break
        case "/git/branches":
          response = [branch, { ...branch, name: "feature", current: false, upstream: "" }]
          break
        case "/git/commit":
          response = { ...commit, changes: [change] }
          break
        case "/git/signature":
          response = { status: "N" }
          break
        case "/git/reflog":
          response = [
            {
              sha: previous,
              selector: "HEAD@{0}",
              message: "reset: moving to HEAD~1",
              author: "Ada",
              at: now,
            },
          ]
          break
        case "/git/blame":
          response = {
            ref: sha,
            file: "a.txt",
            hasMore: false,
            lines: [
              {
                sha,
                line: 1,
                originalLine: 1,
                author: "Ada",
                at: now,
                subject: "Initial version",
                content: "one",
              },
            ],
          }
          break
        case "/git/worktrees":
          response = [
            {
              path: "/srv/app",
              branch: "main",
              head: sha,
              current: true,
              main: true,
              accessible: true,
            },
            {
              path: "/srv/hotfix",
              branch: "hotfix",
              head: sha,
              current: false,
              main: false,
              accessible: true,
            },
          ]
          break
        case "/git/remotes":
          response = [{ name: "origin", fetchUrl: "https://github.com/acme/app.git" }]
          break
        case "/git/compare":
          response = {
            base: "main",
            head: "feature",
            baseSha: previous,
            headSha: sha,
            ahead: 1,
            behind: 0,
            commits: [commit],
            files: 1,
            insertions: 1,
            deletions: 1,
            changes: [change],
          }
          break
        case "/git/compare/diff":
          response = {
            diff: "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-one\n+two\n",
          }
          break
        case "/git/graph":
          response = {
            commits: [{ ...commit, col: 0 }],
            lanes: 1,
            skip: Number(url.searchParams.get("skip")),
            hasMore: !url.searchParams.get("skip") || url.searchParams.get("skip") === "0",
          }
          break
        case "/files/list":
          response = {
            path: "/srv/app",
            entries: [
              {
                name: "a.txt",
                path: "/srv/app/a.txt",
                size: 4,
                isDir: false,
                mode: "0644",
                modified: now,
              },
            ],
          }
          break
        case "/files/read":
          response = {
            path: "/srv/app/a.txt",
            content: "one\n",
            size: 4,
            binary: false,
            language: "plaintext",
          }
          break
      }
    if (path === "/git/clone" && req.method() === "POST")
      response = { path: "/srv/new", result: { ok: true } }
    if (path === "/git/patch/check" && req.method() === "POST")
      response = { version: "checked-patch", summary: "1 file changed", files: ["a.txt"] }
    if (path === "/git/forge/account" && req.method() === "POST") {
      forge = { ...forge, ...body, configured: true }
      response = forge
    }
    if (path === "/git/forge/requests" && req.method() === "POST") response = pull
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(response),
    })
  })
  await page.setViewportSize({ width: 1440, height: 1000 })
  return requests
}

async function openAction(page: Page, name: string) {
  await page.getByRole("button", { name: "More git actions" }).click()
  await page.getByRole("menuitem", { name: new RegExp(`^${name}`) }).click()
}

test("pull request files and conversation stay in the preview", async ({ page }) => {
  const seen = await fixture(page, false, "github")
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("tab", { name: "GitHub" }).click()
  await page.getByRole("button", { name: /Review this change/ }).click()
  await page.getByRole("button", { name: /a.txt.*\+1/ }).click()
  await expect(page.locator("pre").filter({ hasText: "+reviewed" })).toBeVisible()
  expect(seen.find((r) => r.path === "/git/github/pulls/7/files")?.query.get("head")).toBe(sha)
  await page.getByRole("button", { name: "Conversation", exact: true }).click()
  await expect(page.getByText("Existing conversation")).toBeVisible()
})

for (const [label, event] of [
  ["Comment", "COMMENT"],
  ["Approve", "APPROVE"],
  ["Request changes", "REQUEST_CHANGES"],
]) {
  test(`GitHub ${label.toLowerCase()} publishes the reviewed commit`, async ({ page }) => {
    const seen = await fixture(page, false, "github")
    await page.goto("/git?repo=%2Fsrv%2Fapp")
    await page.getByRole("tab", { name: "GitHub" }).click()
    await page.getByRole("button", { name: /Review this change/ }).click()
    await page.getByRole("combobox", { name: "Review action" }).click()
    await page.getByRole("option", { name: label, exact: true }).click()
    await page.getByRole("textbox", { name: "Review comment" }).fill("Reviewed these changes")
    await page.getByRole("button", { name: "Publish review", exact: true }).click()
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Publish review", exact: true })
      .click()
    await expect
      .poll(() => seen.find((r) => r.path.endsWith("/review"))?.body)
      .toEqual({ event, body: "Reviewed these changes", headSha: sha })
  })
}

test("Actions jobs expose step logs and failure filtering", async ({ page }) => {
  const seen = await fixture(page, false, "github")
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("tab", { name: "GitHub" }).click()
  await page.getByRole("button", { name: /Build workflow/ }).click()
  await page.getByRole("button", { name: /Unit tests/ }).click()
  await expect(page.getByText("Test failed: expected 42, received 1")).toBeVisible()
  await page.getByRole("combobox", { name: "Workflow step" }).click()
  await page.getByRole("option", { name: /Run tests/ }).click()
  await page.getByRole("button", { name: "Failed only" }).click()
  await expect
    .poll(() =>
      seen
        .filter((r) => r.path.endsWith("/11/log"))
        .at(-1)
        ?.query.get("step"),
    )
    .toBe("Run tests")
  await expect
    .poll(() =>
      seen
        .filter((r) => r.path.endsWith("/11/log"))
        .at(-1)
        ?.query.get("failed"),
    )
    .toBe("true")
})

test("local rebase preserves the chosen order and edited message", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Edit local history")
  await page.getByRole("textbox", { name: "Rebase base" }).fill("origin/main")
  await page.getByRole("button", { name: "Load commits" }).click()
  await page.getByRole("button", { name: "Move commit 2 earlier" }).click()
  await page.getByRole("combobox", { name: "Action for commit 1" }).click()
  await page.getByRole("option", { name: "Edit message", exact: true }).click()
  await page.getByRole("textbox", { name: "Message for commit 1" }).fill("Reworded commit")
  await page.getByRole("button", { name: "Apply rebase", exact: true }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Apply rebase" }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/rebase")?.body)
    .toEqual({
      head: sha,
      base: previous,
      items: [
        { sha: "c".repeat(40), action: "reword", message: "Reworded commit" },
        { sha, action: "pick", message: "First local commit" },
      ],
    })
})

test("submodules update and confirm deinitialization", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Submodules")
  await page.getByRole("button", { name: "Update to recorded commit" }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/submodule")?.body)
    .toEqual({ action: "update", path: "vendor/lib" })
  await page.getByRole("button", { name: "Deinitialize", exact: true }).click()
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Deinitialize vendor/lib?", exact: true })
    .click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/submodule/remove")?.body)
    .toEqual({ action: "deinit", path: "vendor/lib" })
})

test("LFS tracking uses the typed pattern", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Git LFS")
  await expect(page.getByText("asset.bin", { exact: true })).toBeVisible()
  await page.getByRole("textbox", { name: "LFS file pattern" }).fill("*.psd")
  await page.getByRole("button", { name: "Track pattern", exact: true }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/lfs" && r.body.action)?.body)
    .toEqual({ action: "track", pattern: "*.psd" })
})

test("patch import sends the checked snapshot and exports a download", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Patch exchange")
  await page.getByRole("button", { name: "Prepare patch" }).click()
  const download = page.waitForEvent("download")
  await page.getByRole("button", { name: "Download patch" }).click()
  expect((await download).suggestedFilename()).toBe("changes.patch")
  await page.getByRole("button", { name: "Import", exact: true }).click()
  await page.getByRole("textbox", { name: "Patch content" }).fill("sample patch")
  await page.getByRole("button", { name: "Check patch" }).click()
  await expect(page.getByText("1 file changed", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Apply patch", exact: true }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Apply patch" }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/patch/import")?.body)
    .toEqual({ body: "sample patch", staged: true, version: "checked-patch" })
})

for (const kind of ["gitlab", "gitea"] as const) {
  test(`${kind} requests expose diffs and publish a pinned review`, async ({ page }) => {
    const seen = await fixture(page, false, kind)
    await page.goto("/git?repo=%2Fsrv%2Fapp")
    await openAction(page, "GitLab and Gitea")
    await page.getByRole("button", { name: /Review this change/ }).click()
    await page.getByRole("button", { name: "a.txt", exact: true }).last().click()
    await expect(page.locator("pre").filter({ hasText: "+provider" })).toBeVisible()
    await page.getByRole("textbox", { name: "Provider comment" }).fill("Provider review")
    await page.getByRole("button", { name: "Publish review", exact: true }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Publish review" }).click()
    await expect
      .poll(() => seen.find((r) => r.path === "/git/forge/requests/7/action")?.body)
      .toEqual({ action: "comment", method: "merge", body: "Provider review", sha })
  })
}

test("provider setup submits the token once and reads the connected account", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "GitLab and Gitea")
  await page.getByRole("button", { name: "Connect account", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog
    .getByRole("textbox", { name: "Provider server URL" })
    .fill("https://gitlab.example.com")
  await dialog.getByRole("textbox", { name: "Provider project" }).fill("team/app")
  await dialog.getByLabel("Provider access token").fill("test-token")
  await dialog.getByRole("button", { name: "Connect account", exact: true }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/forge/account")?.body)
    .toEqual({
      kind: "gitlab",
      url: "https://gitlab.example.com",
      project: "team/app",
      token: "test-token",
    })
  await expect(dialog).not.toBeVisible()
  await expect(page.getByText("Signed in as ada", { exact: true })).toBeVisible()
})

test("read-only users cannot publish reviews or configure providers", async ({ page }) => {
  await fixture(page, true, "github")
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("tab", { name: "GitHub" }).click()
  await page.getByRole("button", { name: /Review this change/ }).click()
  await expect(page.getByRole("button", { name: "Publish review" })).toHaveCount(0)
  await openAction(page, "GitLab and Gitea")
  await expect(page.getByRole("button", { name: "Connect account" })).toHaveCount(0)
  await openAction(page, "Patch exchange")
  await expect(page.getByRole("button", { name: "Import", exact: true })).toHaveCount(0)
})

test("amend can change only the message with a clean index", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("checkbox", { name: "Amend the previous commit" }).check()
  await page
    .getByPlaceholder("New message — leave empty to keep the old one")
    .fill("Corrected message")
  await page.getByRole("button", { name: "Commit", exact: true }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/commit" && r.body.amend)?.body)
    .toEqual({ message: "Corrected message", amend: true })
})

test("author filter reaches the history API and signatures appear on commits", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("tab", { name: "History", exact: true }).click()
  await page.getByRole("textbox", { name: "Filter commits by author" }).fill("ada@example.com")
  await expect
    .poll(() =>
      seen.some((r) => r.path === "/git/log" && r.query.get("author") === "ada@example.com"),
    )
    .toBe(true)
  await page.getByRole("button", { name: /Initial version/ }).click()
  await expect(page.getByText("Unsigned commit", { exact: true })).toBeVisible()
})

test("recovery creates a branch from the selected lost commit", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Recovery timeline")
  await page.getByRole("button", { name: "Rescue", exact: true }).click()
  await page.getByLabel("New branch name", { exact: true }).fill("rescued")
  await page.getByRole("button", { name: "Create recovery branch" }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/recover")?.body)
    .toEqual({ name: "rescued", ref: previous })
})

test("worktrees expose navigation and confirm removal of the chosen checkout", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Worktrees")
  const dialog = page.getByRole("dialog", { name: "Worktrees", exact: true })
  await expect(dialog.getByRole("link", { name: "Open", exact: true })).toHaveAttribute(
    "href",
    "/git?repo=%2Fsrv%2Fhotfix",
  )
  await dialog.getByRole("button", { name: "Remove", exact: true }).click()
  await page.getByRole("button", { name: "Remove worktree", exact: true }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/worktree/remove")?.body)
    .toEqual({ path: "/srv/hotfix" })
})

test("worktree creation keeps every selected field", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Worktrees")
  await page.getByLabel("Folder name", { exact: true }).fill("review")
  await page.getByLabel("Branch", { exact: true }).fill("review/one")
  await page.getByLabel("Start from", { exact: true }).fill("feature")
  await page.getByRole("button", { name: "Create worktree" }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/worktree")?.body)
    .toEqual({
      parent: "/srv",
      name: "review",
      branch: "review/one",
      from: "feature",
      create: true,
    })
})

test("remote URLs can be edited in the existing form", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Remotes")
  await page.getByRole("button", { name: "Edit origin", exact: true }).click()
  await page.getByLabel("URL", { exact: true }).fill("https://github.com/acme/new.git")
  await page.getByRole("button", { name: "Save remote" }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/remote/update")?.body)
    .toEqual({ name: "origin", url: "https://github.com/acme/new.git" })
})

test("clone options are sent through the Add repository dialog", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git")
  await page.getByRole("button", { name: "Add repository", exact: true }).click()
  await page.getByLabel("Repository URL", { exact: true }).fill("https://github.com/acme/new.git")
  await page.getByText("Clone options", { exact: true }).click()
  await page.getByLabel("Branch or tag", { exact: true }).fill("feature")
  await page.getByLabel("History depth", { exact: true }).fill("10")
  await page.getByLabel("Sparse folders", { exact: true }).fill("src\npackages/shared")
  await page.getByRole("button", { name: "Clone", exact: true }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/clone")?.body)
    .toMatchObject({ branch: "feature", depth: 10, sparse: ["src", "packages/shared"] })
})

test("graph searches and pages without moving the workspace", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("button", { name: "Branch graph", exact: true }).click()
  await page.getByRole("textbox", { name: "Search graph commits" }).fill("version")
  await expect
    .poll(() => seen.some((r) => r.path === "/git/graph" && r.query.get("search") === "version"))
    .toBe(true)
  await page.getByRole("button", { name: "Next", exact: true }).click()
  await expect
    .poll(() => seen.some((r) => r.path === "/git/graph" && r.query.get("skip") === "250"))
    .toBe(true)
  await expect(page.getByRole("button", { name: "Back to repositories" })).toBeVisible()
})

test("file blame opens the commit responsible for a line", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("button", { name: "a.txt", exact: true }).click()
  await page.getByRole("button", { name: "Blame", exact: true }).click()
  await page.getByRole("button", { name: /aaaaaaa · Ada/ }).click()
  await expect
    .poll(() => seen.some((r) => r.path === "/git/commit" && r.query.get("ref") === sha))
    .toBe(true)
})

test("read-only users can inspect recovery and worktrees without mutation controls", async ({
  page,
}) => {
  await fixture(page, true)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await openAction(page, "Recovery timeline")
  await expect(page.getByText("reset: moving to HEAD~1", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Rescue", exact: true })).toHaveCount(0)
  await openAction(page, "Worktrees")
  await expect(page.getByRole("dialog", { name: "Worktrees", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Create worktree" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Remove", exact: true })).toHaveCount(0)
})

test("branch comparison opens a file diff at the reviewed revisions", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("tab", { name: "Branches", exact: true }).click()
  const feature = page.locator("li").filter({ has: page.getByText("feature", { exact: true }) })
  await feature.getByRole("button", { name: "More actions", exact: true }).click()
  await page.getByRole("menuitem", { name: /^Compare with main/ }).click()
  await page.getByRole("button", { name: "All files", exact: true }).click()
  await expect
    .poll(() =>
      seen.some(
        (r) =>
          r.path === "/git/compare/diff" &&
          r.query.get("base") === previous &&
          r.query.get("head") === sha,
      ),
    )
    .toBe(true)
  await expect(page.locator("pre").filter({ hasText: "+two" })).toBeVisible()
})

test("branches can change and unset their upstream", async ({ page }) => {
  const seen = await fixture(page)
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("tab", { name: "Branches", exact: true }).click()
  const main = page.locator("li").filter({ has: page.getByText("main", { exact: true }) })
  await main.getByRole("button", { name: "More actions", exact: true }).click()
  await page.getByRole("menuitem", { name: /^Set upstream/ }).click()
  await page.getByLabel("Upstream branch", { exact: true }).fill("origin/release")
  await page.getByRole("button", { name: "Set upstream", exact: true }).click()
  await expect
    .poll(() =>
      seen.some(
        (r) =>
          r.path === "/git/upstream" && r.body.name === "main" && r.body.ref === "origin/release",
      ),
    )
    .toBe(true)
  await main.getByRole("button", { name: "More actions", exact: true }).click()
  await page.getByRole("menuitem", { name: /^Stop tracking upstream/ }).click()
  await expect
    .poll(() => seen.some((r) => r.path === "/git/upstream" && r.body.ref === ""))
    .toBe(true)
})

test("a conflict is resolved in the preview and the merge can continue", async ({ page }) => {
  const seen = await fixture(page, false, "conflict")
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await expect(page.getByRole("button", { name: "Continue merge", exact: true })).toBeDisabled()
  await expect(page.getByRole("button", { name: "Stage all", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "a.txt", exact: true }).last().click()
  await page.getByRole("tab", { name: "Incoming", exact: true }).click()
  await expect(page.locator("pre").filter({ hasText: "incoming" })).toBeVisible()
  await page.getByRole("button", { name: "Use incoming", exact: true }).click()
  await page.getByRole("button", { name: "Save and mark resolved", exact: true }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/conflict/resolve")?.body)
    .toEqual({ file: "a.txt", version: "conflict0", choice: "result", content: "incoming\n" })
  await page.getByRole("button", { name: "Continue merge", exact: true }).click()
  await expect.poll(() => seen.some((r) => r.path === "/git/operation/continue")).toBe(true)
})

test("aborting an operation requires its typed confirmation", async ({ page }) => {
  const seen = await fixture(page, false, "conflict")
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await page.getByRole("button", { name: "Abort", exact: true }).click()
  await expect(page.getByRole("button", { name: "Abort operation", exact: true })).toBeDisabled()
  await page.getByRole("textbox").filter({ visible: true }).last().fill("abort operation")
  await page.getByRole("button", { name: "Abort operation", exact: true }).click()
  await expect.poll(() => seen.some((r) => r.path === "/git/operation/abort")).toBe(true)
})

test("individual line staging sends only the selected line and snapshot", async ({ page }) => {
  const seen = await fixture(page, false, "partial")
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await expect(page.getByRole("button", { name: "Stage all", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "a.txt", exact: true }).last().click()
  await page.getByRole("checkbox", { name: "Select added line 2", exact: true }).check()
  await page.getByRole("button", { name: "Stage selected (1)", exact: true }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/patch/stage")?.body)
    .toEqual({ file: "a.txt", staged: false, version: "patch-0", lines: [5] })
})

test("a chunk can be selected for unstaging", async ({ page }) => {
  const seen = await fixture(page, false, "staged")
  await page.goto("/git?repo=%2Fsrv%2Fapp")
  await expect(page.getByRole("button", { name: "Unstage all", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "a.txt", exact: true }).last().click()
  await page.getByRole("checkbox", { name: /^Select chunk at/ }).check()
  await page.getByRole("button", { name: "Unstage selected (2)", exact: true }).click()
  await expect
    .poll(() => seen.find((r) => r.path === "/git/patch/stage")?.body)
    .toEqual({ file: "a.txt", staged: true, version: "patch-0", lines: [5, 6] })
})
