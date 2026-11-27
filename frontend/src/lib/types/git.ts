/*
 * Git and GitHub: checkouts and their status, commits, branches, tags,
 * stashes, diffs, blame and conflicts, and the GitHub account with its
 * repositories, pull requests, checks, issues and workflow runs.
 */

import type { DeploymentPreview } from "./deploy"

// --- Git ---

export type GitRepo = {
  path: string
  name: string
  branch: string
  remote?: string
  /** The branch this one tracks, e.g. origin/main. */
  upstream?: string
  head?: string
  subject?: string
  author?: string
  commitAt?: string
  dirty: boolean
  changes: number
  staged: number
  untracked: number
  conflicts: number
  ahead: number
  behind: number
  detached: boolean
  /** The upstream is configured but no longer exists on the remote. */
  gone?: boolean
  /** No commits yet. */
  empty?: boolean
  /** A linked worktree: a second checkout of the repository at `main`. */
  worktree?: boolean
  /** The main checkout a linked worktree belongs to. */
  main?: string
  /** What the tracked files are written in, by share of bytes, largest first. */
  languages?: { name: string; share: number }[]
}

/**
 * What deleting a checkout from the server would take with it — everything
 * that exists only on this disk — and, in `protected`, why it may not be
 * deleted at all.
 */
export type GitRemoval = {
  path: string
  name: string
  worktree?: boolean
  main?: string
  /** Linked worktrees still attached to a main checkout. */
  worktrees: { path: string; branch: string; dirty: boolean }[]
  /** Other checkouts inside its folder, which deleting it would take along. */
  nested: string[]
  changes: number
  untracked: number
  conflicts: number
  stashes: number
  /** Commits on local branches that no remote-tracking ref contains. */
  unpushed: number
  localBranches: { name: string; unpushed: number; upstream?: string }[]
  remotes: number
  protected?: string
}

/**
 * One entry of `git status`. A file staged and then edited again is one
 * entry with both `staged` and `unstaged` set, and is listed under both
 * headings; `index` and `worktree` carry each side's status letter.
 */
export type GitFileChange = {
  path: string
  index: string
  worktree: string
  label: string
  staged: boolean
  unstaged: boolean
  /** The previous name of a renamed or copied path. */
  from?: string
}

export type GitIdentity = {
  name?: string
  email?: string
}

export type GitStatus = {
  repo: GitRepo
  files: GitFileChange[]
  clean: boolean
  stashes: number
  identity: GitIdentity
  /** A merge, rebase, revert, cherry-pick or bisect a shell left half-finished. */
  operation?: string
}

export type GitChangedFile = {
  path: string
  from?: string
  status: string
  insertions: number
  deletions: number
  binary?: boolean
}

/** One commit with its message and what it changed — the diff is fetched per file. */
export type GitCommitDetail = GitCommit & {
  body?: string
  committer?: string
  committedAt?: string
  changes: GitChangedFile[]
}

export type GitTag = {
  name: string
  commit?: string
  at?: string
  message?: string
  annotated: boolean
  tagger?: string
}

export type GitStash = {
  index: number
  sha: string
  at?: string
  branch?: string
  message: string
}

export type GitRemote = {
  name: string
  fetchUrl: string
  pushUrl?: string
}

/** What merging `head` into `base` would bring. */
export type GitComparison = {
  base: string
  head: string
  ahead: number
  behind: number
  commits: GitCommit[]
  files: number
  insertions: number
  deletions: number
  baseSha?: string
  headSha?: string
  changes?: GitChangedFile[]
}

export type GitReflogEntry = {
  sha: string
  selector: string
  message: string
  author: string
  at: string
}

export type GitBlame = {
  ref: string
  file: string
  hasMore: boolean
  lines: {
    sha: string
    line: number
    originalLine: number
    author: string
    at: string
    subject: string
    content: string
  }[]
}

export type GitSignature = {
  status: string
  signer?: string
  key?: string
  fingerprint?: string
}

export type GitWorktree = {
  path: string
  head: string
  branch: string
  current: boolean
  main: boolean
  locked: boolean
  prunable: boolean
  accessible: boolean
}

export type GitConflictSide = {
  present: boolean
  content: string
  binary: boolean
  mode?: string
  object?: string
}

export type GitConflict = {
  file: string
  version: string
  base: GitConflictSide
  ours: GitConflictSide
  theirs: GitConflictSide
  result: string
  editable: boolean
  operation: string
}

export type GitPartialDiff = {
  file: string
  staged: boolean
  body: string
  version: string
  lines: number[]
  reason?: string
}

export type GitCommit = {
  sha: string
  short: string
  subject: string
  author: string
  email: string
  at: string
  refs?: string
  insertions: number
  deletions: number
  files: number
  isMerge: boolean
  parents?: string[]
}

/** One node in the branch graph: a commit plus the lane its dot sits in. */
export type GitGraphCommit = GitCommit & {
  col: number
  /** The lane each edge to a parent travels down, index for index with `parents`. */
  parentLanes?: number[]
}

export type GitGraph = {
  commits: GitGraphCommit[]
  /** How many lanes wide the busiest row gets — the canvas is sized from this. */
  lanes: number
  hasMore?: boolean
  skip?: number
  /** How many commits the query reaches in all — sent with the first page only. */
  total?: number
}

export type GitBranch = {
  name: string
  current: boolean
  remote: boolean
  upstream?: string
  head?: string
  subject?: string
  at?: string
  ahead: number
  behind: number
  /** Another worktree that has this branch checked out; git refuses to delete
   *  or switch it even with -D. */
  worktree?: string
  /** A local branch whose upstream no longer exists on the remote. */
  gone?: boolean
  /** Every commit is already reachable from HEAD: safe to delete. */
  merged?: boolean
  /** A remote-tracking branch's two halves: the remote, and the local name. */
  remoteName?: string
  local?: string
}

/** One local branch in a `GitMatchPlan`. */
export type GitMatchBranch = {
  name: string
  /** The commit the plan was read at; a discard only applies while it still sits here. */
  head: string
  /** The remote branch this one becomes; absent when it is deleted. */
  target?: string
  behind?: number
  ahead?: number
  /** Commits no ref on the remote reaches: what discarding this would lose. */
  commits?: number
  worktree?: string
}

/** What matching a checkout to its remote would do, read before confirming. */
export type GitMatchPlan = {
  remote: string
  /** The remote's default branch, where the checkout ends up. */
  branch?: string
  switch?: boolean
  stash: number
  delete: GitMatchBranch[]
  update: GitMatchBranch[]
  unique: GitMatchBranch[]
  inUse: GitMatchBranch[]
  staleRefs: string[]
  staleRemotes: string[]
  blocked?: string
}

export type GitResult = {
  command: string
  output: string
  ok: boolean
}

/**
 * A pull that did not start, answered as `409 pull_blocked` beside the error:
 * the files git would not overwrite, or the folders git — running as `owner`
 * — could not write the pulled files into.
 */
export type GitPullBlocked = {
  changed?: string[]
  untracked?: string[]
  unwritable?: { path: string; owner: string }[]
  owner?: string
}

/**
 * Who this server is, to GitHub, in one repository.
 *
 * The answer is per repository and not global, and `owner` is why: gh keeps
 * the token under the home of the host account that owns the checkout, which
 * is the same account git runs as when it pushes. Signing in for /srv/app does
 * not sign in for a repository owned by somebody else.
 */
export type GitHubAccount = {
  loggedIn: boolean
  host?: string
  login?: string
  name?: string
  avatarUrl?: string
  profileUrl?: string
  scopes?: string[]
  protocol?: string
  owner?: string
  /** Whether a push and a commit would actually use the account. */
  gitConfigured: boolean
  committerName?: string
  committerEmail?: string
  /** How origin is reached: an ssh remote never asks the token for anything. */
  remoteProtocol?: string
  /** gh's own words for why nobody is signed in. */
  reason?: string
}

export type GitHubStatus = {
  available: boolean
  account?: GitHubAccount
}

export type GitHubRepo = {
  nameWithOwner: string
  defaultBranch: string
  url: string
  private: boolean
  permission?: string
}

/**
 * One repository the signed-in account can deploy, as the chooser lists them.
 * Distinct from GitHubRepo, which describes the checkout a page is already in.
 */
export type GitHubRepoSummary = {
  nameWithOwner: string
  name: string
  owner: string
  description?: string
  url: string
  cloneUrl: string
  defaultBranch?: string
  language?: string
  private: boolean
  fork: boolean
  archived: boolean
  pushedAt?: string
}

export type GitHubBranch = {
  name: string
  protected?: boolean
  default?: boolean
}

/** The code to type into github.com, and where to type it. */
export type GitHubDeviceStart = {
  id: string
  userCode: string
  verificationUri: string
  expiresIn: number
  interval: number
}

export type GitHubDeviceState = {
  status: "pending" | "complete" | "denied" | "expired"
  account?: GitHubAccount
  message?: string
}

export type GitPullRequest = {
  headSha?: string
  baseSha?: string
  number: number
  title: string
  url: string
  state: string
  draft: boolean
  head: string
  base: string
  author?: string
  createdAt?: string
  comments: number
  /** approved, changes_requested, review_required, or empty. */
  review?: string
  /** success, failure, pending, or empty where there are no checks. */
  checks?: string
  /** mergeable, conflicting, or unknown — only on the detail read. */
  mergeable?: string
  additions?: number
  deletions?: number
  files?: number
  body?: string
  /** owner/name of the head branch's repository; differs from the base repository on a fork. */
  headRepository?: string
  /** The head lives in another repository: its code is not this repository's own. */
  fork?: boolean
  updatedAt?: string
  labels?: string[]
  merged?: boolean
  /** The preview environment built from this pull request, when a deploy project has one. */
  preview?: DeploymentPreview | null
}

export type GitHubCheckRun = {
  name: string
  status: "queued" | "in_progress" | "completed"
  /** success, failure, neutral, cancelled, skipped, timed_out, action_required, pending, or empty while running. */
  conclusion?: string
  url?: string
  app?: string
  startedAt?: string
  completedAt?: string
}

export type GitHubIssue = {
  number: number
  title: string
  url: string
  state: string
  author?: string
  createdAt: string
  updatedAt: string
  comments: number
  labels?: string[]
  assignees?: string[]
}

/** The open pull requests of every GitHub-backed checkout the Git page lists, each joined to its previews. */
export type GitPullRequestSummary = {
  available: boolean
  repos: {
    path: string
    repository: string
    pulls: GitPullRequest[]
    deployments: { projectId: number; name: string; environmentId: number }[]
    /** Why this checkout's pull requests could not be read, in gh's words. */
    error?: string
  }[]
}

export type GitHubWorkflowRun = {
  id: number
  name: string
  workflow?: string
  status: string
  conclusion?: string
  url: string
  branch?: string
  sha?: string
  event?: string
  createdAt?: string
}

/**
 * The answer to "is this shell sitting inside a checkout" for a terminal's
 * working directory. `inRoots` is false for a real repository that falls
 * outside JD_GIT_ROOTS: it can be named but not operated on, since every other
 * git route is gated on those roots.
 */
export type GitDetect = {
  available: boolean
  inRoots?: boolean
  root?: string
  repo?: GitRepo
}
