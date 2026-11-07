import type { Page, Route } from "@playwright/test"

/**
 * A checkout on GitHub as the operator meets one: a merged run of pull
 * requests, an open one whose description embeds pictures in folds and a
 * table, and commits whose emails GitHub can and cannot name.
 */
export const now = new Date().toISOString()
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()
const sha = "de985f77".padEnd(40, "0")

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

export const repo = {
  path: "/srv/just-dashboard",
  name: "just-dashboard",
  branch: "main",
  upstream: "origin/main",
  remote: "https://github.com/JustDashboard/Just-Dashboard.git",
  head: sha,
  subject: "Merge pull request #409 from JustDashboard/patch/0.8.4",
  author: "Wayy",
  commitAt: now,
  dirty: true,
  changes: 3,
  staged: 1,
  untracked: 1,
  conflicts: 0,
  ahead: 1,
  behind: 2,
  detached: false,
}

const media = (name: string) =>
  `https://raw.githubusercontent.com/JustDashboard/Just-Dashboard/0dc98260f7f5c9e53b761a93b0c78b9660de50bc/${name}.png`

const body = `The band over the list pages was a bar with a list of rows under it. The rows reordered on every poll, and that pushed the table down.

## What it is

A head, the bar, **one readout line**, and **one strip of marks**. Same height in every state.

![The Docker overview's band](${media("after-docker-trail")})

- **Holds still.** Every block is 158px, measured on all 15 pages.
- **Alive.** Spans ease to each new width and figures glide.

## Every page

| Page | Blocks | Before | After |
|---|---|---|---|
| \`/docker\` | Processor, Memory, Recent | 255px | 158px |
| \`/proxy\` | Traffic, Certificates | 262px | 158px |

## Before and after

<details open><summary><code>/docker</code></summary>

**Before**

![](${media("before-docker")})

**After**

![](${media("after-docker")})

</details>

<details><summary><code>/docker/containers</code></summary>

![](${media("before-docker")}) ![](${media("after-docker")})

</details>

A picture from elsewhere stays a link: ![a tracker](https://tracker.example/pixel.png)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
`

const pull = (number: number, title: string, extra: Record<string, unknown> = {}) => ({
  number,
  title,
  url: `https://github.com/JustDashboard/Just-Dashboard/pull/${number}`,
  state: "open",
  draft: false,
  head: "feat/0.8.5-bar-reading",
  base: "patch/0.8.5",
  author: "Wayy01",
  createdAt: ago(250),
  updatedAt: ago(7),
  comments: 0,
  checks: "success",
  headSha: sha,
  ...extra,
})

export const open = [
  pull(411, "Replace the rows under every band's bar with a reading that holds still", {
    comments: 2,
    review: "approved",
    additions: 5410,
    deletions: 5307,
    files: 54,
    mergeable: "mergeable",
    body,
  }),
  pull(412, "Fix the sign-in redirect", {
    author: "octo",
    head: "fix/redirect",
    base: "main",
    checks: "failure",
    review: "changes_requested",
    updatedAt: ago(90),
  }),
  pull(413, "Draft: try a smaller bundle", {
    draft: true,
    author: "ada",
    head: "try/bundle",
    base: "main",
    checks: "pending",
    updatedAt: ago(600),
  }),
]

export const commits = [
  [
    "Merge pull request #409 from JustDashboard/patch/0.8.4",
    "tag: v0.8.4, HEAD -> main",
    true,
    490,
  ],
  [
    "Merge pull request #410 from JustDashboard/fix/0.8.4-plan-fold-open",
    "origin/patch/0.8.4",
    true,
    500,
  ],
  ["Open a plan step's fold once its step has rendered", "", false, 506],
  ["Release 0.8.4", "", false, 549],
  ["Install the announced release and go back if it does not come up", "", false, 566],
  ["Update Next.js and Go to current patch releases", "", false, 568],
].map(([subject, refs, isMerge, minutes], index) => ({
  sha: `${index}`.padEnd(40, "a"),
  short: `${index}`.padEnd(7, "a"),
  subject,
  refs,
  isMerge,
  author: "Wayy",
  email: "wayy@example.com",
  at: ago(minutes as number),
  insertions: 26,
  deletions: 14,
  files: 2,
  parents: [],
}))
commits.push({
  ...commits[3],
  sha: "b".repeat(40),
  short: "bbbbbbb",
  subject: "Fix a typo in the docs",
  author: "Mira Stone",
  email: "mira@example.com",
  isMerge: false,
  refs: "",
})

const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAEBgIApD5fRAAAAABJRU5ErkJggg==",
  "base64",
)

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

export async function mockPanel(
  page: Page,
  /** Answers a media read; left out, every picture is one transparent pixel. */
  mediaBody?: (url: string) => Promise<Buffer | undefined>,
) {
  const seen: string[] = []
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    seen.push(path + url.search)
    switch (path) {
      case "/auth/session":
        return json(route, user)
      case "/git/":
        return json(route, { available: true, repos: [repo] })
      case "/git/github/":
        return json(route, {
          available: true,
          account: { loggedIn: true, login: "Wayy01", gitConfigured: true },
        })
      case "/git/github/authors":
        return json(route, { "wayy@example.com": "Wayy01" })
      case "/git/github/avatar":
        return route.fulfill({
          status: 200,
          contentType: "image/png",
          body: (await mediaBody?.(url.href)) ?? png,
        })
      case "/git/github/media": {
        const address = url.searchParams.get("url") ?? ""
        if (address.includes("missing")) return route.fulfill({ status: 404, body: "" })
        const answer = mediaBody ? await mediaBody(address) : png
        return route.fulfill({ status: 200, contentType: "image/png", body: answer ?? png })
      }
      case "/git/github/repo":
        return json(route, {
          nameWithOwner: "JustDashboard/Just-Dashboard",
          defaultBranch: "main",
          url: "https://github.com/JustDashboard/Just-Dashboard",
          private: false,
          permission: "ADMIN",
        })
      case "/git/github/pulls":
        return json(route, url.searchParams.get("state") === "open" ? open : [])
      case "/git/github/pulls/411":
        return json(route, open[0])
      case "/git/github/pulls/411/conversation":
        return json(route, {
          hasMore: false,
          entries: [
            { id: 1, kind: "comments", author: "octo", body: "Looks right to me.", at: ago(60) },
          ],
        })
      case "/git/github/pulls/411/checks":
        return json(route, [
          { name: "Verify", status: "completed", conclusion: "success", app: "GitHub Actions" },
          { name: "Browser", status: "completed", conclusion: "success", app: "GitHub Actions" },
        ])
      case "/git/github/pulls/411/files":
        return json(route, { files: [], hasMore: false, limited: false })
      case "/git/github/issues":
        return json(route, [])
      case "/git/github/runs":
        return json(route, [
          {
            id: 1,
            name: "Release",
            workflow: "Release",
            status: "completed",
            conclusion: "success",
            url: "https://github.com/x",
            sha,
            event: "workflow_run",
            createdAt: ago(471),
          },
          {
            id: 2,
            name: "Verify",
            workflow: "Verify",
            status: "completed",
            conclusion: "failure",
            url: "https://github.com/x",
            sha,
            event: "push",
            createdAt: ago(487),
          },
        ])
      case "/git/pull-requests":
        return json(route, {
          available: true,
          repos: [{ path: repo.path, name: repo.name, pulls: open, deployments: [] }],
        })
      case "/git/log":
        return json(route, commits)
      case "/git/status":
        return json(route, {
          repo,
          clean: false,
          stashes: 0,
          identity: { name: "Wayy", email: "wayy@example.com" },
          files: [
            {
              path: "frontend/src/components/git/history-panel.tsx",
              label: "modified",
              staged: true,
              unstaged: false,
              x: "M",
              y: " ",
            },
            {
              path: "frontend/src/components/git/github-panel.tsx",
              label: "modified",
              staged: false,
              unstaged: true,
              x: " ",
              y: "M",
            },
            {
              path: "frontend/src/components/git/media.tsx",
              label: "untracked",
              staged: false,
              unstaged: true,
              x: "?",
              y: "?",
            },
          ],
        })
      case "/git/branches":
        return json(route, [
          {
            name: "main",
            current: true,
            remote: false,
            ahead: 1,
            behind: 2,
            upstream: "origin/main",
            subject: "Release 0.8.4",
            at: ago(490),
          },
          {
            name: "patch/0.8.5",
            current: false,
            remote: false,
            ahead: 0,
            behind: 0,
            upstream: "origin/patch/0.8.5",
            subject: "Measure a band where the page rests",
            at: ago(120),
          },
        ])
      case "/files/list":
        return json(route, {
          path: repo.path,
          entries: [
            { name: "backend", path: `${repo.path}/backend`, isDir: true },
            { name: "frontend", path: `${repo.path}/frontend`, isDir: true },
            { name: "README.md", path: `${repo.path}/README.md`, isDir: false, size: 4096 },
          ],
        })
      case "/updates/self":
        return json(route, { current: "0.8.4", latest: "0.8.4" })
    }
    return json(route, [])
  })
  return seen
}
