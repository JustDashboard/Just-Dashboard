import { ApiError } from "@/lib/api"
import type {
  GitHubAbilities,
  GitHubAbility,
  GitHubInstallation,
  GitHubRepositoryDetail,
  GitHubRepositorySettings,
  GitHubRepositoryUpdate,
  GitHubRole,
  GitHubRun,
} from "@/lib/github-types"

/**
 * The repository page's pure logic: what its views are, what a settings
 * draft sends, what a run's state is called, and how a failed read is told
 * apart. Kept out of the components so `bun test` reaches it.
 */

export const REPO_TABS = [
  "overview",
  "branches",
  "commits",
  "pulls",
  "issues",
  "actions",
  "collaborators",
  "settings",
] as const
export type RepoTab = (typeof REPO_TABS)[number]

export const REPO_TAB_LABEL: Record<RepoTab, string> = {
  overview: "Overview",
  branches: "Branches",
  commits: "Commits",
  pulls: "Pull requests",
  issues: "Issues",
  actions: "Actions",
  collaborators: "Collaborators",
  settings: "Settings",
}

/** The reason an ability is false, or undefined when it is not. */
export function reasonFor(can: GitHubAbilities, ability: GitHubAbility): string | undefined {
  if (can[ability]) return undefined
  return can.reasons?.[ability] ?? "GitHub does not allow this here."
}

const ABILITY_WORDS: Record<GitHubAbility, string> = {
  administer: "Change settings, visibility and collaborators",
  delete: "Delete the repository",
  push: "Create and delete branches",
  merge: "Merge pull requests",
  pulls: "Close and reopen pull requests",
  issues: "Label, close and reopen issues",
  createIssues: "Open issues",
  actions: "Run, re-run and cancel workflows",
}

/** Every ability in words, the ones you have first. */
export function abilityList(
  can: GitHubAbilities,
): { key: GitHubAbility; words: string; allowed: boolean; reason?: string }[] {
  const keys = Object.keys(ABILITY_WORDS) as GitHubAbility[]
  return keys
    .map((key) => ({
      key,
      words: ABILITY_WORDS[key],
      allowed: can[key],
      reason: reasonFor(can, key),
    }))
    .sort((a, b) => Number(b.allowed) - Number(a.allowed))
}

const ROLE_WORD: Record<GitHubRole, string> = {
  admin: "admin",
  maintain: "maintainer",
  write: "write access",
  triage: "triage",
  read: "read only",
}

export function roleWord(role: GitHubRole): string {
  return ROLE_WORD[role] ?? role
}

const LEVEL_WORD: Record<string, string> = { read: "read", write: "change", admin: "administer" }
const PERMISSION_WORD: Record<string, string> = {
  administration: "settings",
  contents: "code",
  issues: "issues",
  pull_requests: "pull requests",
  actions: "workflows",
  checks: "checks",
  statuses: "statuses",
  metadata: "metadata",
}

/**
 * What the App was granted on one installation, in words grouped by level:
 * "change code, issues and pull requests · read settings". Metadata, which
 * every App has, is left out.
 */
export function grantWords(permissions: Record<string, string>): string {
  const byLevel = new Map<string, string[]>()
  for (const [name, level] of Object.entries(permissions)) {
    if (name === "metadata") continue
    const list = byLevel.get(level) ?? []
    list.push(PERMISSION_WORD[name] ?? name.replace(/_/g, " "))
    byLevel.set(level, list)
  }
  const order = ["admin", "write", "read"]
  return [...byLevel.entries()]
    .sort((a, b) => order.indexOf(a[0]) - order.indexOf(b[0]))
    .map(([level, names]) => `${LEVEL_WORD[level] ?? level} ${joinWords(names)}`)
    .join(" · ")
}

/** "a", "a and b", "a, b and c". */
export function joinWords(words: string[]): string {
  if (words.length <= 1) return words[0] ?? ""
  return `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`
}

/** What one installation lets the App reach, in a sentence. */
export function installationWords(installation: GitHubInstallation): string {
  const who = installation.account.login
  if (installation.suspended) return `The App is suspended on ${who}.`
  return installation.repositorySelection === "all"
    ? `Installed on ${who} for every repository.`
    : `Installed on ${who} for selected repositories only.`
}

/** The settings a draft can change, every one optional as GitHub states only some. */
export type SettingsDraft = {
  name: string
  description: string
  homepage: string
  topics: string[]
  defaultBranch: string
  isTemplate: boolean
} & Partial<GitHubRepositorySettings>

const SETTING_KEYS: (keyof GitHubRepositorySettings)[] = [
  "hasIssues",
  "hasWiki",
  "hasProjects",
  "hasDiscussions",
  "allowMergeCommit",
  "allowSquashMerge",
  "allowRebaseMerge",
  "allowAutoMerge",
  "deleteBranchOnMerge",
  "allowUpdateBranch",
  "webCommitSignoffRequired",
]

/** The draft a repository's settings start from: what GitHub said, and nothing it did not. */
export function draftOf(repo: GitHubRepositoryDetail): SettingsDraft {
  const draft: SettingsDraft = {
    name: repo.name,
    description: repo.description ?? "",
    homepage: repo.homepage ?? "",
    topics: [...(repo.topics ?? [])],
    defaultBranch: repo.defaultBranch,
    isTemplate: repo.isTemplate,
  }
  for (const key of SETTING_KEYS) {
    const value = repo.settings[key]
    if (typeof value === "boolean") draft[key] = value
  }
  return draft
}

const sameList = (a: string[], b: string[]) =>
  a.length === b.length && a.every((value, index) => value === b[index])

/**
 * What a save sends: only the fields the draft changed, and only settings
 * GitHub stated. An unchanged field is left out rather than sent as it was,
 * so a save never writes over a change made on GitHub meanwhile.
 */
export function settingsChanges(
  repo: GitHubRepositoryDetail,
  draft: SettingsDraft,
): GitHubRepositoryUpdate {
  const saved = draftOf(repo)
  const out: GitHubRepositoryUpdate = {}
  const name = draft.name.trim()
  if (name !== saved.name) out.name = name
  if (draft.description.trim() !== saved.description) out.description = draft.description.trim()
  if (draft.homepage.trim() !== saved.homepage) out.homepage = draft.homepage.trim()
  if (!sameList(draft.topics, saved.topics)) out.topics = [...draft.topics]
  if (draft.defaultBranch !== saved.defaultBranch) out.defaultBranch = draft.defaultBranch
  if (draft.isTemplate !== saved.isTemplate) out.isTemplate = draft.isTemplate
  for (const key of SETTING_KEYS) {
    if (typeof saved[key] !== "boolean") continue
    if (draft[key] !== saved[key]) out[key] = draft[key]
  }
  return out
}

/** How many fields a draft changed: the save bar's count. */
export function changeCount(update: GitHubRepositoryUpdate): number {
  return Object.keys(update).length
}

/** GitHub's own rule for a repository name, said before the save rather than after. */
export function nameProblem(name: string): string | undefined {
  const trimmed = name.trim()
  if (!trimmed) return "A repository needs a name."
  if (trimmed.length > 100) return "At most 100 characters."
  if (!/^[A-Za-z0-9._-]+$/.test(trimmed)) return "Letters, digits, '.', '-' and '_' only."
  if (trimmed === "." || trimmed === "..") return "Not '.' or '..'."
  return undefined
}

/** Topics as GitHub takes them: lower case, letters, digits and hyphens, 50 characters. */
export function topicProblem(topic: string): string | undefined {
  if (!/^[a-z0-9][a-z0-9-]{0,49}$/.test(topic)) {
    return "A topic is lower-case letters, digits and hyphens, starting with a letter or digit."
  }
  return undefined
}

/** Splits what was typed into topics: commas or spaces between them, lower-cased, no repeats. */
export function parseTopics(text: string): string[] {
  const seen = new Set<string>()
  for (const part of text.split(/[\s,]+/)) {
    const topic = part.trim().toLowerCase()
    if (topic) seen.add(topic)
  }
  return [...seen]
}

export type RunTone = "success" | "danger" | "warning" | "notice" | "neutral"

/**
 * A run's state as one word and its tone: what it is doing while it runs,
 * how it ended once it has. GitHub's `status` and `conclusion` are two
 * fields, and a list that showed both said "completed · success" on every row.
 */
export function runState(run: Pick<GitHubRun, "status" | "conclusion">): {
  word: string
  tone: RunTone
  live: boolean
} {
  if (run.status !== "completed") {
    const word =
      run.status === "in_progress"
        ? "running"
        : run.status === "queued" || run.status === "requested" || run.status === "pending"
          ? "queued"
          : run.status === "waiting"
            ? "waiting"
            : run.status.replace(/_/g, " ")
    return { word, tone: "notice", live: run.status === "in_progress" }
  }
  switch (run.conclusion) {
    case "success":
      return { word: "succeeded", tone: "success", live: false }
    case "failure":
    case "startup_failure":
      return { word: "failed", tone: "danger", live: false }
    case "timed_out":
      return { word: "timed out", tone: "danger", live: false }
    case "cancelled":
      return { word: "cancelled", tone: "neutral", live: false }
    case "skipped":
      return { word: "skipped", tone: "neutral", live: false }
    case "action_required":
      return { word: "needs approval", tone: "warning", live: false }
    case "neutral":
      return { word: "neutral", tone: "neutral", live: false }
    default:
      return { word: run.conclusion?.replace(/_/g, " ") || "completed", tone: "neutral", live: false }
  }
}

/** A run that is finished can be run again; one waiting or running can be cancelled. */
export function runVerbs(run: Pick<GitHubRun, "status" | "conclusion">): {
  rerun: boolean
  rerunFailed: boolean
  cancel: boolean
} {
  const done = run.status === "completed"
  const failed = done && ["failure", "timed_out", "cancelled", "startup_failure"].includes(run.conclusion ?? "")
  return {
    rerun: done,
    rerunFailed: failed,
    cancel: ["queued", "in_progress", "waiting", "requested", "pending"].includes(run.status),
  }
}

/** How a failed read of the repository is drawn. */
export type RepoFailure =
  | { kind: "missing"; message: string }
  | { kind: "reconnect"; message: string }
  | { kind: "rate"; message: string; until?: number }
  | { kind: "other"; error: Error }

export function repoFailure(error: Error): RepoFailure {
  if (error instanceof ApiError) {
    if (error.code === "github_not_found" || error.code === "github_connection") {
      return { kind: "missing", message: error.message }
    }
    if (error.code === "github_reconnect") return { kind: "reconnect", message: error.message }
    if (error.code === "github_rate_limited") {
      return {
        kind: "rate",
        message: error.message,
        until: error.retryAfterMs ? Date.now() + error.retryAfterMs : undefined,
      }
    }
  }
  return { kind: "other", error }
}

/** A label's colour as GitHub stores it (hex without `#`), or undefined when it is not one. */
export function labelColour(color: string | undefined): string | undefined {
  return color && /^[0-9a-fA-F]{6}$/.test(color) ? `#${color}` : undefined
}

/** A branch name git would take: the rules `git check-ref-format` holds, said in one line. */
export function branchProblem(name: string): string | undefined {
  const value = name.trim()
  if (!value) return "A branch needs a name."
  if (
    value.startsWith("-") ||
    value.startsWith("/") ||
    value.endsWith("/") ||
    value.endsWith(".") ||
    value.endsWith(".lock") ||
    value.includes("..") ||
    value.includes("//") ||
    value.includes("@{") ||
    value === "@" ||
    /[\s~^:?*[\\\x00-\x1f\x7f]/.test(value)
  ) {
    return "Not a name git accepts: no spaces, '..', '~', '^', ':', '?', '*', '[' or '\\'."
  }
  return undefined
}
