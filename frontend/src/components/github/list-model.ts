import { ApiError } from "@/lib/api"
import { plural } from "@/lib/format"
import type { GitHubConnection, GitHubRepository } from "@/lib/github-types"
import type { GitHubListState } from "@/lib/github-list"

/**
 * The GitHub list page's words and rules that need no browser: what GitHub
 * will call a new repository, what this server will call a clone, the
 * identity line's facts, which account the page is on, and how a failed read
 * is told apart. The page and its dialogs draw these; `list-model.test.js`
 * holds them.
 */

/** How many rows the list draws at first, and how many more each Show more adds. */
export const GITHUB_PAGE_ROWS = 30

/**
 * The name GitHub will create from what was typed: it keeps letters, digits,
 * `.`, `-` and `_`, and turns every run of anything else into one `-` (its
 * own form says "Your new repository will be created as …"). `problem` is
 * the refusal GitHub would give, said before the request is sent.
 */
export function githubRepoName(typed: string): { name: string; problem?: string } {
  const name = typed.trim().replace(/[^A-Za-z0-9._-]+/g, "-")
  if (!name) return { name, problem: "Name the repository." }
  if (name.length > 100) return { name, problem: "GitHub allows at most 100 characters." }
  if (name === "." || name === "..") return { name, problem: `GitHub does not allow “${name}”.` }
  if (/^-+$/.test(name)) return { name, problem: "Use at least one letter or digit." }
  return { name }
}

const FOLDER_CHARS = /^[A-Za-z0-9._\-@+~^]+$/

/**
 * Why the server would refuse this as the clone's folder name, or nothing:
 * `gitx.validateDirName`'s rule, one path segment that is not hidden and
 * cannot be read as an option, in the characters a ref may use.
 */
export function cloneFolderProblem(name: string): string | undefined {
  if (!name) return "Name the folder."
  if (name.length > 255) return "At most 255 characters."
  if (name.includes("/") || name.includes("\\")) return "One folder, without a slash."
  if (name.startsWith(".")) return "A folder that starts with a dot is hidden; pick another name."
  if (name.startsWith("-")) return "A folder cannot start with a dash."
  if (!FOLDER_CHARS.test(name)) return "Letters, digits and . _ - @ + ~ ^ only."
  return undefined
}

/** `@login · 9 repositories · 5 private` — the identity line's facts, in words. */
export function accountFacts(
  login: string,
  counts: Record<GitHubListState, number> | undefined,
): string[] {
  const facts = [`@${login}`]
  if (!counts) return facts
  facts.push(plural(counts.all, "repository", "repositories"))
  if (counts.private > 0) facts.push(`${counts.private} private`)
  if (counts.archived > 0) facts.push(`${counts.archived} archived`)
  return facts
}

/**
 * The connection the page is on: the one the address names when it is this
 * account's, else the first active one, else the first. A stale or foreign
 * id in `?account=` is not an error page; it is the default account.
 */
export function pickConnection(
  connections: GitHubConnection[] | undefined,
  account: string | null,
): GitHubConnection | undefined {
  if (!connections?.length) return undefined
  const wanted = Number(account)
  return (
    (account && connections.find((connection) => connection.id === wanted)) ||
    connections.find((connection) => connection.status === "active") ||
    connections[0]
  )
}

export type ListFailure =
  | { kind: "reconnect"; message: string }
  | { kind: "rate"; message: string; until?: Date }
  | { kind: "failed" }

/**
 * What a failed repository list asks of the reader. A spent rate limit says
 * when it lifts (from `Retry-After`), an account GitHub stopped accepting
 * offers its reconnect, and anything else is the ordinary failed read.
 */
export function listFailure(error: unknown, now = Date.now()): ListFailure {
  if (error instanceof ApiError) {
    if (error.code === "github_reconnect") return { kind: "reconnect", message: error.message }
    if (error.code === "github_rate_limited") {
      return {
        kind: "rate",
        message: error.message,
        until: error.retryAfterMs ? new Date(now + error.retryAfterMs) : undefined,
      }
    }
  }
  return { kind: "failed" }
}

/** "14:05", the clock time a wait ends at, in the reader's own zone. */
export function clockTime(at: Date): string {
  return at.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
}

const ROLE_WORD: Partial<Record<GitHubRepository["role"], string>> = {
  read: "Read only",
  triage: "Triage",
  write: "Write",
}

/**
 * The tags a row carries after its name: what kind of repository it is, and
 * how far the reader's own hand reaches on it. A role at maintain or admin
 * is the expected answer and not said; one below is, since it is why a
 * control on the repository's page is off.
 */
export function rowTags(repo: GitHubRepository): string[] {
  const tags: string[] = []
  if (repo.archived) tags.push("Archived")
  if (repo.fork) tags.push("Fork")
  if (repo.isTemplate) tags.push("Template")
  if (repo.relation === "collaborator") tags.push("Shared with you")
  const role = ROLE_WORD[repo.role]
  if (role) tags.push(role)
  return tags
}

/** What GitHub's grant names mean, in the order a person cares about them. */
const PERMISSION_WORDS: [key: string, words: Record<string, string>][] = [
  ["metadata", { read: "See the repositories it is installed on" }],
  ["contents", { read: "Read code", write: "Read and push code, create and delete branches" }],
  ["pull_requests", { read: "Read pull requests", write: "Open, merge and close pull requests" }],
  ["issues", { read: "Read issues", write: "Open, edit and close issues and their labels" }],
  ["actions", { read: "Read workflow runs", write: "Start, re-run and cancel workflow runs" }],
  [
    "administration",
    {
      read: "Read repository settings",
      write: "Create, rename, archive and delete repositories, and change their settings",
    },
  ],
  ["checks", { read: "Read check results", write: "Write check results" }],
  ["statuses", { read: "Read commit statuses", write: "Write commit statuses" }],
]

/** The App's permissions as sentences, GitHub's names left for an `Explain`. */
export function permissionWords(permissions: Record<string, string>): string[] {
  const known = new Set(PERMISSION_WORDS.map(([key]) => key))
  const words = PERMISSION_WORDS.flatMap(([key, levels]) => {
    const level = permissions[key]
    return level && levels[level] ? [levels[level]] : []
  })
  const rest = Object.entries(permissions)
    .filter(([key]) => !known.has(key))
    .map(([key, level]) => `${key.replace(/_/g, " ")}: ${level}`)
  return [...words, ...rest]
}

/**
 * The permissions the manifest asks for, as `CONTRACT.md` decides them, for
 * the setup dialog to say before the App exists. Once it exists the page says
 * what the App itself reports.
 */
export const MANIFEST_PERMISSIONS: Record<string, string> = {
  metadata: "read",
  contents: "write",
  pull_requests: "write",
  issues: "write",
  actions: "write",
  administration: "write",
  checks: "read",
  statuses: "read",
}

/** A GitHub login as GitHub allows it: letters, digits and single inner dashes, 39 at most. */
export function loginProblem(login: string): string | undefined {
  if (!login) return "Name the organisation."
  if (login.length > 39 || !/^[A-Za-z0-9](?:[A-Za-z0-9]|-(?=[A-Za-z0-9]))*$/.test(login)) {
    return "Not a GitHub organisation name."
  }
  return undefined
}
