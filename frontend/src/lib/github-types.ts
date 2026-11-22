/**
 * The GitHub page's API, `/api/v1/github`: the shapes the backend's
 * `internal/githubrepos` answers with, written by hand as `lib/types.ts` is.
 * They live apart from that file because the page is one area with its own
 * forty types, and nothing else reads them. Times are ISO strings; a field
 * the backend leaves out is absent, never an empty or zero placeholder.
 */

/** GitHub's own words for an account: a person or an organisation. */
export type GitHubOwnerType = "User" | "Organization"

export type GitHubOwner = { login: string; type: GitHubOwnerType }

/** This dashboard's repository App, which every connection signs in through. */
export type GitHubSetup = {
  configured: boolean
  app?: {
    id: number
    slug: string
    name: string
    /** The GitHub account that owns the App. */
    owner: string
    htmlUrl: string
    /** Where the App's settings are changed on GitHub (callback URLs, device flow). */
    settingsUrl: string
    /** Where it is installed on another account. */
    installUrl: string
    /** Installable by any account, not only its owner. */
    public: boolean
    /** The sign-in addresses GitHub was told about: `<origin>/github/callback`. */
    callbackUrls: string[]
    /** What the App asks every installation for, GitHub's names and levels. */
    permissions: Record<string, string>
    createdAt?: string
  }
}

/** What the page posts to GitHub to create the App (the manifest flow). */
export type GitHubManifestStart = {
  /** The GitHub page the form posts to. */
  action: string
  state: string
  manifest: Record<string, unknown>
}

export type GitHubInstallation = {
  id: number
  account: GitHubOwner
  repositorySelection: "all" | "selected"
  /** The App's grant here, e.g. `{ administration: "write", contents: "write" }`. */
  permissions: Record<string, string>
  /** Where its repositories and permissions are changed on GitHub. */
  settingsUrl: string
  suspended: boolean
}

export type GitHubConnectionStatus = "active" | "expired" | "revoked"

/** One GitHub account a dashboard account connected. Never anyone else's. */
export type GitHubConnection = {
  id: number
  login: string
  name?: string
  htmlUrl: string
  status: GitHubConnectionStatus
  /** Why it is not active, in words. */
  statusMessage?: string
  /** How it was signed in. */
  method: "web" | "device"
  createdAt: string
  updatedAt?: string
  /** The installations this account can see, as last read. */
  installations: GitHubInstallation[]
  installationsReadAt?: string
}

export type GitHubDeviceStart = {
  id: string
  userCode: string
  verificationUri: string
  expiresIn: number
  interval: number
}

export type GitHubDeviceState = {
  status: "pending" | "complete" | "denied" | "expired" | "failed"
  connection?: GitHubConnection
  message?: string
}

/** What `POST /github/callback` finished: creating the App, or connecting an account. */
export type GitHubCallbackResult = {
  kind: "app" | "connection"
  connection?: GitHubConnection
}

export type GitHubRole = "admin" | "maintain" | "write" | "triage" | "read"

export type GitHubAbility =
  "administer" | "delete" | "push" | "merge" | "pulls" | "issues" | "createIssues" | "actions"

/**
 * What the page may offer on one repository: the person's role there and the
 * App's grant on the installation. A false ability has its reason in
 * `reasons`, which a disabled control shows.
 */
export type GitHubAbilities = Record<GitHubAbility, boolean> & {
  reasons?: Partial<Record<GitHubAbility, string>>
}

export type GitHubRepository = {
  id: number
  name: string
  fullName: string
  owner: GitHubOwner
  description?: string
  visibility: "public" | "private" | "internal"
  private: boolean
  language?: string
  stars: number
  forks: number
  openIssues: number
  archived: boolean
  fork: boolean
  isTemplate: boolean
  defaultBranch: string
  htmlUrl: string
  cloneUrl: string
  sshUrl: string
  topics?: string[]
  createdAt?: string
  updatedAt?: string
  pushedAt?: string
  installationId: number
  role: GitHubRole
  /** Owned by the connected account, by an organisation, or by someone who added it as a collaborator. */
  relation: "owned" | "organization" | "collaborator"
  can: GitHubAbilities
}

export type GitHubRateLimit = { limit: number; remaining: number; resetAt?: string }

/** `GET /github/connections/{id}/repositories` */
export type GitHubRepositoryList = {
  connection: GitHubConnection
  repositories: GitHubRepository[]
  /** More repositories exist than were read (3,000 at most). */
  truncated: boolean
  /** Installations whose listing failed; the rest still came back. */
  partial: { installationId: number; account: string; message: string }[]
  readAt: string
  rateLimit?: GitHubRateLimit
}

/** Settings GitHub only states to people who may change them; absent otherwise. */
export type GitHubRepositorySettings = {
  hasIssues: boolean
  hasWiki: boolean
  hasProjects: boolean
  hasDiscussions: boolean
  allowMergeCommit?: boolean
  allowSquashMerge?: boolean
  allowRebaseMerge?: boolean
  allowAutoMerge?: boolean
  deleteBranchOnMerge?: boolean
  allowUpdateBranch?: boolean
  webCommitSignoffRequired?: boolean
}

/** `GET /github/connections/{id}/repos/{owner}/{repo}` */
export type GitHubRepositoryDetail = GitHubRepository & {
  homepage?: string
  settings: GitHubRepositorySettings
  installation: GitHubInstallation
  /** The repository this one was forked from. */
  parent?: { fullName: string; htmlUrl: string }
}

/** `PATCH /github/connections/{id}/repos/{owner}/{repo}`: only the fields sent change. */
export type GitHubRepositoryUpdate = Partial<
  {
    name: string
    description: string
    homepage: string
    defaultBranch: string
    topics: string[]
    isTemplate: boolean
  } & GitHubRepositorySettings
>

/** `GET /github/connections/{id}/create-options` */
export type GitHubCreateOptions = {
  owners: {
    login: string
    type: GitHubOwnerType
    installationId: number
    repositorySelection: "all" | "selected"
    canCreate: boolean
    /** Why not, or what to expect (a selected-repositories installation). */
    note?: string
  }[]
  gitignoreTemplates: string[]
  licenses: { key: string; name: string; spdxId?: string }[]
}

/** `POST /github/connections/{id}/repositories` */
export type GitHubCreateRepository = {
  owner: string
  name: string
  description?: string
  visibility: "public" | "private"
  autoInit: boolean
  gitignoreTemplate?: string
  licenseTemplate?: string
}

export type GitHubCreateResult = {
  repository: GitHubRepository
  /** False when the App cannot reach the new repository yet (selected repositories only). */
  reachable: boolean
  notice?: string
}

/** `POST …/repos/{owner}/{repo}/clone` — a job whose console the page shows. */
export type GitHubCloneRequest = { parent: string; name?: string; branch?: string; depth?: number }
export type GitHubCloneStart = { jobId: string; target: string }

export type GitHubBranch = { name: string; sha: string; protected: boolean; default: boolean }
export type GitHubBranchPage = { branches: GitHubBranch[]; hasMore: boolean }

/** `GET …/branches/protection?name=` — classic protection and the rulesets that apply. */
export type GitHubBranchProtection = {
  branch: string
  protected: boolean
  protection?: {
    requiredReviews?: number
    dismissStaleReviews?: boolean
    requireCodeOwnerReviews?: boolean
    requiredChecks: string[]
    strictChecks?: boolean
    enforceAdmins?: boolean
    linearHistory?: boolean
    allowForcePushes?: boolean
    allowDeletions?: boolean
    conversationResolution?: boolean
  }
  /** False when GitHub would not say (it needs admin); `protectionMessage` says why. */
  protectionReadable: boolean
  protectionMessage?: string
  rules: { type: string; summary: string; rulesetId?: number; rulesetName?: string }[]
  rulesReadable: boolean
  rulesMessage?: string
}

export type GitHubCommit = {
  sha: string
  message: string
  author: { name: string; login?: string; date?: string }
  committer?: { name: string; login?: string; date?: string }
  htmlUrl: string
  verified?: boolean
  parents: number
}
export type GitHubCommitPage = { commits: GitHubCommit[]; hasMore: boolean }

export type GitHubLabel = { name: string; color: string; description?: string }

export type GitHubPull = {
  number: number
  title: string
  state: "open" | "closed" | "merged"
  draft: boolean
  author: string
  createdAt: string
  updatedAt: string
  closedAt?: string
  mergedAt?: string
  head: { ref: string; sha: string; repo?: string }
  base: { ref: string }
  /** The head is in another repository. */
  fork: boolean
  htmlUrl: string
  labels: GitHubLabel[]
}
export type GitHubPullPage = { pulls: GitHubPull[]; hasMore: boolean }

export type GitHubReview = {
  id: number
  author: string
  state: "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED" | "DISMISSED" | "PENDING"
  submittedAt?: string
  htmlUrl: string
}

export type GitHubCheck = {
  name: string
  status: "queued" | "in_progress" | "completed" | "pending"
  conclusion?: string
  detailsUrl?: string
  /** A check run, or a commit status folded into the same shape. */
  source: "check" | "status"
  startedAt?: string
  completedAt?: string
}

export type GitHubPullDetail = GitHubPull & {
  body: string
  /** GitHub works it out lazily: null until it has. */
  mergeable: boolean | null
  mergeableState: string
  additions: number
  deletions: number
  changedFiles: number
  commits: number
  reviews: GitHubReview[]
  checks: GitHubCheck[]
  checksReadable: boolean
  checksMessage?: string
  /** The merge methods the repository allows. */
  mergeMethods: ("merge" | "squash" | "rebase")[]
}

export type GitHubMergeRequest = {
  method: "merge" | "squash" | "rebase"
  /** The head the reader saw; GitHub refuses the merge if it moved. */
  sha: string
  title?: string
  message?: string
}
export type GitHubMergeResult = { merged: boolean; sha?: string; message: string }

export type GitHubIssue = {
  number: number
  title: string
  state: "open" | "closed"
  stateReason?: "completed" | "not_planned" | "reopened"
  author: string
  labels: GitHubLabel[]
  comments: number
  assignees: string[]
  createdAt: string
  updatedAt: string
  closedAt?: string
  htmlUrl: string
  /** Only on `GET …/issues/{number}`. */
  body?: string
}
export type GitHubIssuePage = { issues: GitHubIssue[]; hasMore: boolean }

export type GitHubIssueCreate = { title: string; body?: string; labels?: string[] }
export type GitHubIssueUpdate = {
  title?: string
  body?: string
  state?: "open" | "closed"
  stateReason?: "completed" | "not_planned" | "reopened"
  labels?: string[]
}

export type GitHubWorkflowInput = {
  name: string
  description?: string
  required: boolean
  default?: string
  type: "string" | "boolean" | "choice" | "number" | "environment"
  options?: string[]
}

export type GitHubWorkflow = {
  id: number
  name: string
  path: string
  state: string
  htmlUrl: string
  /** Whether it can be started from here (a workflow_dispatch trigger), and its inputs. */
  dispatch: { available: boolean; inputs: GitHubWorkflowInput[]; message?: string }
}
export type GitHubWorkflowList = {
  workflows: GitHubWorkflow[]
  readable: boolean
  message?: string
}

export type GitHubRun = {
  id: number
  name: string
  title: string
  workflowId: number
  event: string
  status: string
  conclusion?: string
  branch: string
  sha: string
  actor?: string
  runNumber: number
  runAttempt: number
  createdAt: string
  updatedAt: string
  htmlUrl: string
}
export type GitHubRunPage = { runs: GitHubRun[]; hasMore: boolean }

export type GitHubDispatchRequest = { ref: string; inputs: Record<string, string> }
export type GitHubDispatchResult = { runId?: number; htmlUrl?: string }

export type GitHubCollaborator = { login: string; type: GitHubOwnerType | "Bot"; role: string }
export type GitHubInvitation = {
  id: number
  invitee: string
  role: string
  createdAt: string
  inviter?: string
  expired?: boolean
}
export type GitHubCollaborators = {
  collaborators: GitHubCollaborator[]
  readable: boolean
  message?: string
  invitations: GitHubInvitation[]
  invitationsReadable: boolean
}
export type GitHubCollaboratorRole = "read" | "triage" | "write" | "maintain" | "admin"
export type GitHubCollaboratorResult = { invited: boolean }
