import type { Page, Route } from "@playwright/test"
import type {
  GitHubBranch,
  GitHubBranchProtection,
  GitHubCollaborators,
  GitHubCommit,
  GitHubIssue,
  GitHubLabel,
  GitHubPull,
  GitHubPullDetail,
  GitHubRepository,
  GitHubRun,
  GitHubWorkflow,
} from "../../src/lib/github-types"
import {
  apiError,
  iso,
  json,
  mockGitHub,
  now,
  repositories,
  repositoryDetail,
  type GitHubMockOptions,
} from "./github-fixture-data"

/**
 * One repository's page against `github-fixture-data.ts`: every route under
 * `…/repos/{owner}/{repo}` the views read and write, answered from the
 * repository list (so a rename lands on a repository that exists), and
 * every write recorded in `calls` for a spec to assert what was sent.
 *
 * The names are the shared fixture's made-up ones; nothing here is real.
 */

export type RepoCall = {
  method: string
  path: string
  body?: unknown
  confirm?: string
  query: Record<string, string>
}

const minutes = (n: number) => iso(now - n * 60_000)

export const HEAD_SHA = "4f1c2d9e8b7a6f5e4d3c2b1a0f9e8d7c6b5a4f3e"

export const branches: GitHubBranch[] = [
  {
    name: "feature/invoices",
    sha: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
    protected: false,
    default: false,
  },
  { name: "main", sha: HEAD_SHA, protected: true, default: true },
  {
    name: "release/2.4",
    sha: "0f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e",
    protected: true,
    default: false,
  },
]

export const protection: GitHubBranchProtection = {
  branch: "main",
  protected: true,
  protection: {
    requiredReviews: 1,
    requiredChecks: ["build", "test"],
    strictChecks: true,
    linearHistory: true,
  },
  protectionReadable: true,
  rules: [{ type: "deletion", summary: "Deletion is blocked", rulesetName: "Keep main" }],
  rulesReadable: true,
}

export const commits: GitHubCommit[] = [
  {
    sha: HEAD_SHA,
    message: "Bill annual plans on the renewal date\n\nThe old date drifted by a day.",
    author: { name: "Mira", login: "mira-dev", date: minutes(60) },
    htmlUrl: `https://github.com/northwind/api/commit/${HEAD_SHA}`,
    verified: true,
    parents: 1,
  },
  {
    sha: "1234567890abcdef1234567890abcdef12345678",
    message: "Retry the payment webhook twice",
    author: { name: "Sam K", date: minutes(600) },
    htmlUrl: "https://github.com/northwind/api/commit/1234567890abcdef1234567890abcdef12345678",
    parents: 1,
  },
]

export const labels: GitHubLabel[] = [
  { name: "bug", color: "d73a4a", description: "Something is wrong" },
  { name: "billing", color: "0e8a16" },
]

const pull = (number: number, over: Partial<GitHubPull> = {}): GitHubPull => ({
  number,
  title: `Pull request ${number}`,
  state: "open",
  draft: false,
  author: "sam-k",
  createdAt: minutes(3000),
  updatedAt: minutes(90),
  head: { ref: "feature/invoices", sha: HEAD_SHA },
  base: { ref: "main" },
  fork: false,
  htmlUrl: `https://github.com/northwind/api/pull/${number}`,
  labels: [],
  ...over,
})

export const pulls: GitHubPull[] = [
  pull(42, { title: "Send invoices as PDF", labels: [labels[1]] }),
  pull(41, { title: "Draft: new tax tables", draft: true, author: "mira-dev" }),
  pull(38, { title: "Old refactor", state: "closed", closedAt: minutes(9000) }),
  pull(37, { title: "Move to Go 1.26", state: "merged", mergedAt: minutes(8000) }),
]

export function pullDetail(number: number): GitHubPullDetail | undefined {
  const base = pulls.find((p) => p.number === number)
  if (!base) return undefined
  return {
    ...base,
    body: "Invoices go out as **PDF** now.\n\n- one per customer\n- signed",
    mergeable: true,
    mergeableState: "clean",
    additions: 120,
    deletions: 14,
    changedFiles: 6,
    commits: 3,
    reviews: [
      {
        id: 1,
        author: "mira-dev",
        state: "APPROVED",
        submittedAt: minutes(80),
        htmlUrl: "https://github.com/northwind/api/pull/42#review-1",
      },
    ],
    checks: [
      { name: "build", status: "completed", conclusion: "success", source: "check" },
      { name: "test", status: "in_progress", source: "check" },
    ],
    checksReadable: true,
    mergeMethods: ["squash", "merge"],
  }
}

const issue = (number: number, over: Partial<GitHubIssue> = {}): GitHubIssue => ({
  number,
  title: `Issue ${number}`,
  state: "open",
  author: "sam-k",
  labels: [],
  comments: 0,
  assignees: [],
  createdAt: minutes(5000),
  updatedAt: minutes(200),
  htmlUrl: `https://github.com/northwind/api/issues/${number}`,
  ...over,
})

export const issues: GitHubIssue[] = [
  issue(51, { title: "Refunds show the wrong currency", labels: [labels[0]], comments: 3 }),
  issue(49, { title: "Export invoices as CSV", labels: [labels[1]] }),
  issue(30, { title: "Old crash", state: "closed", stateReason: "completed" }),
]

export const workflows: GitHubWorkflow[] = [
  {
    id: 701,
    name: "Deploy",
    path: ".github/workflows/deploy.yml",
    state: "active",
    htmlUrl: "https://github.com/northwind/api/actions/workflows/deploy.yml",
    dispatch: {
      available: true,
      inputs: [
        { name: "environment", required: true, type: "choice", options: ["staging", "production"] },
        { name: "dry_run", required: false, type: "boolean", default: "true" },
        { name: "note", required: false, type: "string" },
      ],
    },
  },
  {
    id: 702,
    name: "CI",
    path: ".github/workflows/ci.yml",
    state: "active",
    htmlUrl: "https://github.com/northwind/api/actions/workflows/ci.yml",
    dispatch: {
      available: false,
      inputs: [],
      message: "It has no workflow_dispatch trigger, so it runs only on push and pull_request.",
    },
  },
]

const run = (id: number, over: Partial<GitHubRun> = {}): GitHubRun => ({
  id,
  name: "CI",
  title: "Bill annual plans on the renewal date",
  workflowId: 702,
  event: "push",
  status: "completed",
  conclusion: "success",
  branch: "main",
  sha: HEAD_SHA,
  actor: "mira-dev",
  runNumber: id - 9000,
  runAttempt: 1,
  createdAt: minutes(55),
  updatedAt: minutes(50),
  htmlUrl: `https://github.com/northwind/api/actions/runs/${id}`,
  ...over,
})

export const runs: GitHubRun[] = [
  run(9103, { status: "in_progress", conclusion: undefined }),
  run(9102, { conclusion: "failure", title: "Retry the payment webhook twice" }),
  run(9101),
]

export const collaborators: GitHubCollaborators = {
  collaborators: [
    { login: "mira-dev", type: "User", role: "admin" },
    { login: "sam-k", type: "User", role: "push" },
  ],
  readable: true,
  invitations: [
    { id: 301, invitee: "lee-ops", role: "read", createdAt: minutes(2000), inviter: "mira-dev" },
  ],
  invitationsReadable: true,
}

export type RepoMock = { calls: RepoCall[]; repos: GitHubRepository[] }

/**
 * `mockGitHub` with the repository routes added. `repos` is the first
 * connection's list, copied so a spec's rename does not leak into the next.
 */
export async function mockGitHubRepo(
  page: Page,
  options: Omit<GitHubMockOptions, "handle" | "repositories"> & {
    /** Collaborators of a repository whose role cannot list them. */
    unreadableCollaborators?: boolean
  } = {},
): Promise<RepoMock> {
  const repos = repositories.map((repo) => ({ ...repo }))
  const calls: RepoCall[] = []
  const handle = async (route: Route, path: string, url: URL) => {
    const match = path.match(/^\/github\/connections\/(\d+)\/repos\/([^/]+)\/([^/]+)(\/.*)?$/)
    if (!match) return false
    const request = route.request()
    const method = request.method()
    const fullName = `${decodeURIComponent(match[2])}/${decodeURIComponent(match[3])}`
    const index = repos.findIndex((r) => r.fullName.toLowerCase() === fullName.toLowerCase())
    if (index < 0) {
      await apiError(route, 404, "github_not_found", "GitHub has no such thing.")
      return true
    }
    const repo = repos[index]
    const rest = match[4] ?? ""
    const body = request.postData() ? JSON.parse(request.postData()!) : undefined
    if (method !== "GET") {
      const confirm = request.headers()["x-confirm"]
      calls.push({
        method,
        path: rest,
        body,
        confirm: confirm ? decodeURIComponent(confirm) : undefined,
        query: Object.fromEntries(url.searchParams),
      })
    }
    const page1 = (url.searchParams.get("page") ?? "1") === "1"

    if (rest === "") {
      if (method === "GET") return false // the shared mock answers the detail
      if (method === "PATCH") {
        const next = { ...repo, ...body }
        if (body.name) next.fullName = `${repo.owner.login}/${body.name}`
        repos[index] = next
        await json(route, repositoryDetail(next))
        return true
      }
      if (method === "DELETE") {
        repos.splice(index, 1)
        await route.fulfill({ status: 204 })
        return true
      }
    }
    if (rest === "/archive" && method === "POST") {
      repos[index] = { ...repo, archived: body.archived }
      await json(route, repositoryDetail(repos[index]))
      return true
    }
    if (rest === "/visibility" && method === "POST") {
      repos[index] = { ...repo, visibility: body.visibility, private: body.visibility !== "public" }
      await json(route, repositoryDetail(repos[index]))
      return true
    }
    if (rest === "/branches") {
      if (method === "GET") await json(route, { branches: page1 ? branches : [], hasMore: false })
      else if (method === "POST") {
        await json(route, { name: body.name, sha: HEAD_SHA, protected: false, default: false })
      } else await route.fulfill({ status: 204 })
      return true
    }
    if (rest === "/branches/protection") {
      await json(route, { ...protection, branch: url.searchParams.get("name") ?? "main" })
      return true
    }
    if (rest === "/commits") {
      await json(route, { commits: page1 ? commits : [], hasMore: page1 })
      return true
    }
    if (rest === "/pulls") {
      const state = url.searchParams.get("state") ?? "open"
      await json(route, {
        pulls: pulls.filter((p) => state === "all" || p.state === state),
        hasMore: false,
      })
      return true
    }
    const onePull = rest.match(/^\/pulls\/(\d+)(\/merge|\/state)?$/)
    if (onePull) {
      const number = Number(onePull[1])
      if (onePull[2] === "/merge") {
        await json(route, {
          merged: true,
          sha: HEAD_SHA,
          message: "Pull Request successfully merged",
        })
      } else if (onePull[2] === "/state") {
        await json(route, { ...pulls.find((p) => p.number === number), state: body.state })
      } else {
        const detail = pullDetail(number)
        if (detail) await json(route, detail)
        else await apiError(route, 404, "github_not_found", "GitHub has no such thing.")
      }
      return true
    }
    if (rest === "/labels") {
      await json(route, labels)
      return true
    }
    if (rest === "/issues") {
      if (method === "POST") {
        await json(route, {
          ...issues[0],
          number: 60,
          title: body.title,
          body: body.body,
          labels: [],
        })
        return true
      }
      const state = url.searchParams.get("state") ?? "open"
      const label = url.searchParams.get("labels")
      await json(route, {
        issues: issues.filter(
          (i) =>
            (state === "all" || i.state === state) &&
            (!label || i.labels.some((l) => l.name === label)),
        ),
        hasMore: false,
      })
      return true
    }
    const oneIssue = rest.match(/^\/issues\/(\d+)$/)
    if (oneIssue) {
      const found = issues.find((i) => i.number === Number(oneIssue[1])) ?? {
        ...issues[0],
        number: Number(oneIssue[1]),
      }
      await json(route, { ...found, body: "It says **EUR** for a GBP refund.", ...(body ?? {}) })
      return true
    }
    if (rest === "/actions/workflows") {
      await json(route, { workflows, readable: true })
      return true
    }
    if (rest === "/actions/runs") {
      await json(route, { runs: page1 ? runs : [], hasMore: false })
      return true
    }
    if (/^\/actions\/workflows\/\d+\/dispatch$/.test(rest)) {
      await json(route, {})
      return true
    }
    if (/^\/actions\/runs\/\d+\/(rerun|cancel)$/.test(rest)) {
      await route.fulfill({ status: 204 })
      return true
    }
    if (rest === "/collaborators") {
      if (options.unreadableCollaborators || repo.role === "read" || repo.role === "triage") {
        await json(route, {
          collaborators: [],
          readable: false,
          message: "GitHub shows who has access only to people with push access or more.",
          invitations: [],
          invitationsReadable: false,
        })
      } else await json(route, collaborators)
      return true
    }
    if (rest.startsWith("/collaborators/")) {
      if (method === "PUT") await json(route, { invited: rest.endsWith("lee-new") })
      else await route.fulfill({ status: 204 })
      return true
    }
    if (rest.startsWith("/invitations/")) {
      await route.fulfill({ status: 204 })
      return true
    }
    return false
  }
  await mockGitHub(page, { ...options, repositories: { 1: repos, 2: [] }, handle })
  return { calls, repos }
}
