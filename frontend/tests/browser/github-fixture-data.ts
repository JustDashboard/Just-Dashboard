import type { Page, Route } from "@playwright/test"
import type {
  GitHubAbilities,
  GitHubAbility,
  GitHubConnection,
  GitHubInstallation,
  GitHubRepository,
  GitHubRepositoryDetail,
  GitHubRepositoryList,
  GitHubRole,
  GitHubSetup,
} from "../../src/lib/github-types"

/**
 * One dashboard account's GitHub, shared by the GitHub page's specs: an App,
 * two connected accounts (one whose sign-in expired), three installations
 * that grant different things, and repositories that cover every row the
 * list draws — owned, an organisation's, somebody else's; public, private,
 * internal, archived, a fork; every role from admin to read.
 *
 * Every login and address here is made up. The lanes' own fixtures
 * (`github-fixture.ts`, `github-repo-fixture.ts`) add their routes through
 * `handle` and keep these shapes, which are the API's
 * (`src/lib/github-types.ts`).
 */

export const now = Date.now()
export const iso = (ms: number) => new Date(ms).toISOString()
const days = (n: number) => iso(now - n * 86_400_000)

export const adminCapabilities = [
  "read",
  "service.control",
  "file.write",
  "terminal",
  "destructive",
  "system.admin",
]

export function sessionFor(capabilities: string[] = adminCapabilities, role = "admin") {
  return {
    authenticated: true,
    needsTotp: false,
    needsEnrollment: false,
    require2fa: false,
    capabilities,
    user: {
      id: 1,
      username: "operator",
      role,
      totpEnabled: true,
      disabled: false,
      mustChangePassword: false,
      lastLoginAt: iso(now),
      createdAt: iso(now),
    },
  }
}

export const setup: GitHubSetup = {
  configured: true,
  app: {
    id: 4100,
    slug: "jd-repos-3f2a",
    name: "Just Dashboard Repos 3f2a",
    owner: "mira-dev",
    htmlUrl: "https://github.com/apps/jd-repos-3f2a",
    settingsUrl: "https://github.com/settings/apps/jd-repos-3f2a",
    installUrl: "https://github.com/apps/jd-repos-3f2a/installations/new",
    public: true,
    callbackUrls: ["http://127.0.0.1:43117/github/callback"],
    permissions: {
      administration: "write",
      contents: "write",
      issues: "write",
      pull_requests: "write",
      actions: "write",
      checks: "read",
      statuses: "read",
      metadata: "read",
    },
    createdAt: days(30),
  },
}

const full = { ...setup.app!.permissions }
const noAdministration = { ...full, administration: "read", actions: "read" }

export const installations: GitHubInstallation[] = [
  {
    id: 11,
    account: { login: "mira-dev", type: "User" },
    repositorySelection: "all",
    permissions: full,
    settingsUrl: "https://github.com/settings/installations/11",
    suspended: false,
  },
  {
    id: 12,
    account: { login: "northwind", type: "Organization" },
    repositorySelection: "selected",
    permissions: full,
    settingsUrl: "https://github.com/organizations/northwind/settings/installations/12",
    suspended: false,
  },
  {
    id: 13,
    account: { login: "acme-labs", type: "Organization" },
    repositorySelection: "all",
    permissions: noAdministration,
    settingsUrl: "https://github.com/organizations/acme-labs/settings/installations/13",
    suspended: false,
  },
]

export const connections: GitHubConnection[] = [
  {
    id: 1,
    login: "mira-dev",
    name: "Mira",
    htmlUrl: "https://github.com/mira-dev",
    status: "active",
    method: "web",
    createdAt: days(20),
    updatedAt: days(1),
    installations,
    installationsReadAt: iso(now - 60_000),
  },
  {
    id: 2,
    login: "mira-work",
    htmlUrl: "https://github.com/mira-work",
    status: "expired",
    statusMessage: "GitHub no longer accepts the sign-in for mira-work; connect it again",
    method: "device",
    createdAt: days(200),
    installations: [],
  },
]

const ROLE_RANK: Record<GitHubRole, number> = {
  read: 1,
  triage: 2,
  write: 3,
  maintain: 4,
  admin: 5,
}
const LEVEL: Record<string, number> = { read: 1, write: 2, admin: 3 }

/** The same policy as `githubrepos.ComputeAbilities`, for the fixtures only. */
export function abilitiesFor(
  role: GitHubRole,
  installation: GitHubInstallation,
  repo: { fullName: string; archived: boolean },
  hasIssues = true,
): GitHubAbilities {
  const reasons: Partial<Record<GitHubAbility, string>> = {}
  const check = (
    key: GitHubAbility,
    min: GitHubRole,
    permission: string,
    level: string,
    unarchived: boolean,
  ) => {
    if (ROLE_RANK[role] < ROLE_RANK[min]) {
      reasons[key] = `Your role on ${repo.fullName} is ${role}; this needs ${min}.`
    } else if ((LEVEL[installation.permissions[permission]] ?? 0) < LEVEL[level]) {
      reasons[key] =
        `The GitHub App was not granted ${permission}: ${level} on ${installation.account.login}. Accept the permission on GitHub, then refresh.`
    } else if (unarchived && repo.archived) {
      reasons[key] = `${repo.fullName} is archived, so it is read-only until it is unarchived.`
    } else return true
    return false
  }
  const can = {
    administer: check("administer", "admin", "administration", "write", false),
    delete: check("delete", "admin", "administration", "write", false),
    push: check("push", "write", "contents", "write", true),
    merge: check("merge", "write", "contents", "write", true),
    pulls: check("pulls", "triage", "pull_requests", "write", true),
    issues: check("issues", "triage", "issues", "write", true),
    createIssues: check("createIssues", "read", "issues", "write", true) && hasIssues,
    actions: check("actions", "write", "actions", "write", true),
  }
  return Object.keys(reasons).length ? { ...can, reasons } : can
}

function repository(
  fullName: string,
  installation: GitHubInstallation,
  role: GitHubRole,
  over: Partial<GitHubRepository> = {},
): GitHubRepository {
  const [login, name] = fullName.split("/")
  const owner = { login, type: installation.account.type }
  const base: GitHubRepository = {
    id: 5000 + [...fullName].reduce((sum, c, i) => sum + c.charCodeAt(0) * (i + 1), 0),
    name,
    fullName,
    owner,
    visibility: "public",
    private: false,
    stars: 0,
    forks: 0,
    openIssues: 0,
    archived: false,
    fork: false,
    isTemplate: false,
    defaultBranch: "main",
    htmlUrl: `https://github.com/${fullName}`,
    cloneUrl: `https://github.com/${fullName}.git`,
    sshUrl: `git@github.com:${fullName}.git`,
    createdAt: days(400),
    updatedAt: days(3),
    pushedAt: days(3),
    installationId: installation.id,
    role,
    relation:
      owner.type === "Organization"
        ? "organization"
        : login === "mira-dev"
          ? "owned"
          : "collaborator",
    can: {} as GitHubAbilities,
    ...over,
  }
  base.can = abilitiesFor(role, installation, base)
  return base
}

const [mine, northwind, acme] = installations
const sam: GitHubInstallation = { ...mine, id: 14, account: { login: "sam-k", type: "User" } }

export const repositories: GitHubRepository[] = [
  repository("northwind/api", northwind, "admin", {
    visibility: "private",
    private: true,
    language: "Go",
    stars: 21,
    forks: 2,
    openIssues: 4,
    description: "Orders and billing service",
    topics: ["billing", "grpc"],
    pushedAt: iso(now - 3_600_000),
    updatedAt: iso(now - 3_600_000),
  }),
  repository("mira-dev/dotfiles", mine, "admin", {
    language: "Shell",
    stars: 12,
    forks: 3,
    description: "Shell, editor and terminal setup",
    pushedAt: days(1),
  }),
  repository("northwind/storefront", northwind, "write", {
    visibility: "private",
    private: true,
    language: "TypeScript",
    stars: 48,
    forks: 6,
    openIssues: 17,
    description: "The shop's web front end",
    defaultBranch: "trunk",
    pushedAt: days(2),
  }),
  repository("acme-labs/design-system", acme, "read", {
    language: "TypeScript",
    stars: 310,
    forks: 41,
    openIssues: 52,
    description: "Components and tokens for every acme product",
    isTemplate: true,
    pushedAt: days(5),
  }),
  repository("mira-dev/notes", mine, "admin", {
    visibility: "private",
    private: true,
    description: "Plain-text notes",
    pushedAt: days(9),
  }),
  repository("acme-labs/internal-tools", acme, "maintain", {
    visibility: "internal",
    private: true,
    language: "Python",
    stars: 4,
    pushedAt: days(14),
  }),
  repository("sam-k/sideproject", sam, "write", {
    language: "Rust",
    stars: 7,
    description: "A weekend game engine",
    pushedAt: days(21),
  }),
  repository("mira-dev/site", mine, "admin", {
    language: "Astro",
    fork: true,
    description: "Personal site, forked from a starter",
    pushedAt: days(60),
  }),
  repository("acme-labs/legacy-portal", acme, "admin", {
    language: "PHP",
    archived: true,
    stars: 2,
    description: "Retired customer portal",
    pushedAt: days(700),
    updatedAt: days(600),
  }),
]

export function repositoryList(
  connection = connections[0],
  repos = repositories,
): GitHubRepositoryList {
  return {
    connection,
    repositories: repos,
    truncated: false,
    partial: [],
    readAt: iso(now),
    rateLimit: { limit: 5000, remaining: 4870, resetAt: iso(now + 1_800_000) },
  }
}

export function repositoryDetail(repo: GitHubRepository): GitHubRepositoryDetail {
  const installation = [...installations, sam].find((i) => i.id === repo.installationId)!
  const admin = repo.role === "admin"
  return {
    ...repo,
    homepage: repo.fullName === "mira-dev/dotfiles" ? "https://mira.example.net" : undefined,
    installation,
    parent: repo.fork
      ? { fullName: "astro-starters/blog", htmlUrl: "https://github.com/astro-starters/blog" }
      : undefined,
    settings: {
      hasIssues: true,
      hasWiki: !repo.private,
      hasProjects: false,
      hasDiscussions: false,
      ...(admin
        ? {
            allowMergeCommit: true,
            allowSquashMerge: true,
            allowRebaseMerge: false,
            allowAutoMerge: false,
            deleteBranchOnMerge: true,
            allowUpdateBranch: true,
            webCommitSignoffRequired: false,
          }
        : {}),
    },
  }
}

/** The checkouts Local Git finds on this server; two of them are clones of the list's repositories. */
export const localRepos = [
  {
    path: "/srv/northwind-api",
    name: "northwind-api",
    branch: "main",
    remote: "git@github.com:northwind/api.git",
    dirty: false,
    changes: 0,
    staged: 0,
    untracked: 0,
    conflicts: 0,
    ahead: 0,
    behind: 1,
    detached: false,
  },
  {
    path: "/home/mira/dotfiles",
    name: "dotfiles",
    branch: "main",
    remote: "https://github.com/Mira-Dev/dotfiles",
    dirty: true,
    changes: 2,
    staged: 0,
    untracked: 1,
    conflicts: 0,
    ahead: 0,
    behind: 0,
    detached: false,
  },
  {
    path: "/srv/api",
    name: "api",
    branch: "main",
    remote: "https://gitlab.example.net/other/api.git",
    dirty: false,
    changes: 0,
    staged: 0,
    untracked: 0,
    conflicts: 0,
    ahead: 0,
    behind: 0,
    detached: false,
  },
]

export async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

export async function apiError(route: Route, status: number, code: string, message: string) {
  await json(route, { error: { code, message } }, status)
}

export type GitHubMockOptions = {
  capabilities?: string[]
  role?: string
  setup?: GitHubSetup
  connections?: GitHubConnection[]
  /** The repositories each connection lists, by connection id. */
  repositories?: Record<number, GitHubRepository[]>
  /**
   * A lane's own routes: return true once the route is answered. Called
   * first, with the path under /api/v1.
   */
  handle?: (route: Route, path: string, url: URL) => Promise<boolean> | boolean
}

/**
 * The session, the App, the connections, each connection's repository list,
 * Local Git's list and roots, and every repository's detail. A `/github`
 * route nothing answers is a 404 the page shows, so a missing mock is seen.
 */
export async function mockGitHub(page: Page, options: GitHubMockOptions = {}) {
  const conns = options.connections ?? connections
  const lists = options.repositories ?? { 1: repositories, 2: [] }
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (options.handle && (await options.handle(route, path, url))) return
    const method = route.request().method()
    if (path === "/auth/session") return json(route, sessionFor(options.capabilities, options.role))
    if (path === "/git/") return json(route, { available: true, repos: localRepos })
    if (path === "/git/roots") return json(route, { roots: ["/srv", "/home/mira"] })
    if (path === "/github/app" && method === "GET") return json(route, options.setup ?? setup)
    if (path === "/github/connections" && method === "GET") return json(route, conns)
    const list = path.match(/^\/github\/connections\/(\d+)\/repositories$/)
    if (list && method === "GET") {
      const connection = conns.find((c) => c.id === Number(list[1]))
      if (!connection) return apiError(route, 404, "github_connection", "not connected")
      if (connection.status !== "active") {
        return apiError(route, 409, "github_reconnect", connection.statusMessage ?? "reconnect")
      }
      return json(route, repositoryList(connection, lists[connection.id] ?? []))
    }
    const one = path.match(/^\/github\/connections\/(\d+)\/repos\/([^/]+)\/([^/]+)$/)
    if (one && method === "GET") {
      const fullName = `${decodeURIComponent(one[2])}/${decodeURIComponent(one[3])}`.toLowerCase()
      const repo = (lists[Number(one[1])] ?? []).find((r) => r.fullName.toLowerCase() === fullName)
      if (!repo) return apiError(route, 404, "github_not_found", "GitHub has no such thing.")
      return json(route, repositoryDetail(repo))
    }
    if (path.startsWith("/github/")) {
      return apiError(route, 404, "not_found", `no mock for ${method} ${path}`)
    }
    return json(route, [])
  })
}
