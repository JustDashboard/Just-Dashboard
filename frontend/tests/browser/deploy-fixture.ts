import { expect } from "@playwright/test"
import type { Page, Route } from "@playwright/test"
import type {
  ArchivedDeployment,
  BackupJob,
  BackupRun,
  Container,
  ContainerHistory,
  ContainerSparkline,
  ContainerStats,
  DbConnection,
  DbProvisionOption,
  DeploymentBuildEvidence,
  DeploymentConfiguration,
  DeploymentCredential,
  DeploymentDatabaseLink,
  DeploymentDraftSource,
  DeploymentEngineRun,
  DeploymentHostnameSuggestion,
  DeploymentOperations,
  DeploymentPendingChange,
  DeploymentPreviewApproval,
  DeploymentRecentRun,
  DeploymentRunState,
  DeploymentRuntimeServices,
  DeploymentSchedule,
  DeploymentSummary,
  DeploymentTrigger,
  DeploymentTriggerDelivery,
  DeploymentVariable,
  GitHubAppRepository,
  GitHubAppStatus,
  GitHubRepoSummary,
  NotificationChannel,
  NotificationDelivery,
  ReleaseComparisonResponse,
  SourceIdentity,
  TrafficPulse,
  VolumeDetail,
} from "../../src/lib/types"

/**
 * The deployment API, mocked.
 *
 * One fixture for every deployment spec: the same project, the same run, the
 * same catalogue, so a test in one area can be read next to a test in
 * another. Register your own `page.route` after calling one of these to
 * override an endpoint — Playwright consults the newest route first.
 *
 * Extracted from the pre-rebuild `deploy-ui.spec.ts`; the endpoints did not
 * change with the redesign, only the screens that read them.
 */

export const now = "2026-09-03T12:00:00Z"

export const user = {
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

/**
 * The dashboard's own labels, as they arrive on a container event. `run-id` is
 * what lets a row link to the run that put the release on the server.
 */
export const releaseOwner = {
  "environment-id": "12",
  "release-id": "20",
  "release-number": "20",
  "run-id": "84",
}

/**
 * A complete backup job. The Databases settings page reads a job's schedule,
 * its runs and what it has stored, so a partial literal in a test is a crash
 * rather than a thinner assertion.
 */
export function backupJob(fields: { id: number; name: string } & Record<string, unknown>) {
  return {
    sources: ["/srv/app"],
    excludes: [],
    targetKind: "local",
    target: { path: "/var/backups" },
    schedule: "0 3 * * *",
    retention: 7,
    retentionDays: 0,
    enabled: true,
    createdAt: now,
    hasCredentials: false,
    overdue: false,
    stored: { runs: 3, bytes: 1024 },
    databaseDumps: [],
    ...fields,
  }
}

export const run = {
  id: 84,
  runNumber: 1,
  projectId: 7,
  environmentId: 12,
  state: "verifying",
  operation: "deploy",
  trigger: "manual",
  actor: "operator",
  requestedAt: now,
  queuedAt: now,
  claimedAt: now,
  cancelRequested: false,
  planRevision: 3,
  priority: 500,
  slotClass: "heavy",
  metadata: {},
}

export const deployment = {
  id: 7,
  name: "api-production",
  profile: "web",
  environmentId: 12,
  environmentName: "Production",
  environmentKind: "production",
  desiredRevision: 3,
  liveReleaseId: 20,
  livePlanRevision: 2,
  strategy: "blue_green",
  expectedDowntime: false,
  sourceKind: "git",
  buildMethod: "recipe",
  sourceRef: "main",
  sourceRevision: "a12bc34d56ef7890",
  endpoint: "https://api.example.test",
  internalPort: 3000,
  health: "unavailable",
  pendingChanges: true,
  stopped: false,
  lastRun: run,
  activeRun: run,
  updatedAt: now,
  sourceRepository: "acme/api",
  sourceRemote: "https://github.com/acme/api",
  recipe: "node",
  framework: "nextjs",
}

export const project = {
  id: 7,
  name: "api-production",
  profile: "web",
  repoPath: "/srv/api-production",
  branch: "main",
  composeFile: "compose.yml",
  hookId: "api-production-hook",
  hookUrl: "/api/v1/hooks/deploy/api-production-hook",
  enabled: true,
  currentSha: "a12bc34d56ef7890",
  createdAt: now,
  updatedAt: now,
  envVarCount: 1,
}

/** What the live release's build recorded, as `build_artifact` keeps it. */
export const buildEvidence: DeploymentBuildEvidence = {
  result: {
    image: {
      reference: "jd/api-production:r20",
      digest: `sha256:${"a".repeat(64)}`,
      sizeBytes: 187_000_000,
      os: "linux",
      architecture: "amd64",
    },
    prepared: {
      method: "recipe",
      recipe: "node",
      toolchain: "node 22",
      baseImages: [{ reference: "oven/bun:1-alpine", digest: `sha256:${"c".repeat(64)}` }],
      dockerfilePreview:
        "FROM oven/bun:1-alpine AS build\nWORKDIR /app\nCOPY . .\nRUN bun install --frozen-lockfile && bun run build\n",
      cachePolicy: "cached",
    },
  },
}

export const steps = [
  [101, "resolve_source", 1, "passed"],
  [102, "acquire_source", 2, "passed"],
  [103, "analyze_plan", 3, "passed"],
  [104, "prepare_context", 4, "passed"],
  [105, "build_artifact", 5, "passed"],
  [106, "render_runtime", 6, "passed"],
  [107, "release_task", 7, "passed"],
  [108, "backup_gate", 8, "skipped"],
  [109, "start_candidate", 9, "passed"],
  [110, "verify_readiness", 10, "running"],
  [111, "verify_smoke", 11, "pending"],
  [112, "activate", 12, "pending"],
  [113, "retire_previous", 13, "pending"],
  [114, "record_release", 14, "pending"],
  [115, "notify", 15, "pending"],
].map(([id, key, ordinal, state]) => ({
  id,
  runId: 84,
  key,
  ordinal,
  state,
  attempt: 1,
  timeoutSeconds: 300,
  startedAt: now,
  // The one step whose record the Build settings page reads back: how long
  // the build took and the image it made.
  endedAt: key === "build_artifact" ? "2026-09-03T12:01:12Z" : undefined,
  evidence: key === "build_artifact" ? buildEvidence : {},
  lastSeq: Number(ordinal),
}))

/**
 * The fixture's steps for one run, with times that add up: each starts when
 * the one before it ended, the build takes `buildSeconds` and every other step
 * a few, a skipped step takes none, and a step the run never reached has no
 * start. `stateOf` says how each step went, from the default run's state.
 */
function timedSteps(
  runId: number,
  start: string,
  stateOf: (key: string, state: string) => string,
  buildSeconds = 72,
) {
  let at = Date.parse(start)
  return steps.map((step) => {
    const state = stateOf(String(step.key), String(step.state))
    if (state === "pending")
      return { ...step, runId, state, startedAt: undefined, endedAt: undefined }
    const seconds = state === "skipped" ? 0 : step.key === "build_artifact" ? buildSeconds : 4
    const startedAt = new Date(at).toISOString()
    at += seconds * 1000
    return {
      ...step,
      runId,
      state,
      startedAt,
      endedAt: state === "running" ? undefined : new Date(at).toISOString(),
    }
  })
}

/** The showcase's run in flight, at Readiness checks, its steps timed. */
export const showcaseSteps = timedSteps(84, now, (_, state) => state)

export const healthyOperations: DeploymentOperations = {
  observedAt: now,
  releaseId: 20,
  evidence: "release",
  runtime: { status: "available", observedAt: now, services: [] },
  domains: {
    status: "available",
    siteName: "just-dashboard-env-12.conf",
    domains: [
      {
        hostname: "api.example.test",
        https: true,
        ownership: "managed",
        route: "served",
        servedBy: "just-dashboard-env-12.conf",
        certificate: "valid",
        certificateName: "api.example.test",
        certificateDaysLeft: 70,
        certificateIssuer: "R10",
        deepLink: "/proxy/sites?site=just-dashboard-env-12.conf",
        certificateLink: "/proxy/certificates",
      },
    ],
  },
  storage: {
    status: "available",
    mounts: [
      {
        source: "api-data",
        target: "/data",
        kind: "volume",
        ownership: "linked",
        status: "present",
        deepLink: "/docker/volumes?volume=api-data",
      },
    ],
  },
  backups: {
    status: "available",
    jobs: [
      {
        resourceId: "4",
        required: true,
        status: "present",
        lastStatus: "success",
        fresh: true,
        deepLink: "/backups",
      },
    ],
  },
  dependencies: { status: "available", items: [] },
  diagnosis: { status: "assessed", findings: [], silences: [] },
}

/**
 * One project taken out of the fleet, with the facts the archived list reads
 * beside the project record. The archived specs route their own list; this is
 * what every other page, and the captures, see.
 */
export const archivedProject: ArchivedDeployment = {
  id: 9,
  name: "retired-api",
  profile: "web",
  repoPath: "/srv/retired-api",
  branch: "main",
  composeFile: "",
  hookId: "retired-api-hook",
  enabled: false,
  createdAt: "2026-06-11T09:00:00Z",
  updatedAt: "2026-08-20T16:30:00Z",
  archivedAt: "2026-08-20T16:30:00Z",
  envVarCount: 4,
  sourceKind: "git",
  sourceRef: "main",
  sourceRepository: "acme/retired-api",
  buildMethod: "recipe",
  recipe: "python",
  framework: "django",
}

export const releaseComparison: ReleaseComparisonResponse = {
  comparison: {
    fromReleaseId: 19,
    toReleaseId: 20,
    changes: { source: true, build: true, runtime: false, variables: true },
    detail: {
      status: "available",
      fields: [
        {
          field: "source revision",
          from: "99887766554433221100",
          to: "a12bc34d56ef7890",
          changed: true,
        },
        { field: "command", from: "bun start", to: "bun start", changed: false },
        { field: "internal port", from: "3000", to: "8080", changed: true },
      ],
      variables: [
        {
          name: "API_TOKEN",
          change: "changed",
          from: "111111111111 · secret",
          to: "222222222222 · secret",
          detail: "Compared by value digest; values are never read here.",
          secret: true,
        },
      ],
      dependencies: [
        {
          name: "backup_job 4",
          change: "unchanged",
          from: "linked · backup",
          to: "linked · backup",
        },
      ],
      checks: [{ name: "ready", change: "added", to: "http · readiness · required" }],
      domains: [
        {
          name: "api.example.test",
          change: "unchanged",
          from: "https · managed",
          to: "https · managed",
        },
      ],
    },
    artifacts: [
      {
        kind: "image",
        reference: "example.test/api:v2",
        digest: `sha256:${"a".repeat(64)}`,
        sizeBytes: 4096,
        state: "available",
        retained: true,
        reason: "retained for rollback to release 2",
      },
    ],
  },
  update: {
    status: "available",
    reference: "example.test/api:v2",
    state: "outdated",
    localDigest: `sha256:${"a".repeat(64)}`,
    remoteDigest: `sha256:${"b".repeat(64)}`,
    checkedAt: now,
  },
  artifacts: [
    {
      kind: "image",
      reference: "example.test/api:v2",
      digest: `sha256:${"a".repeat(64)}`,
      sizeBytes: 4096,
      state: "available",
      retained: true,
      reason: "retained for rollback to release 2",
    },
  ],
}

export const draft = {
  id: "browser-draft",
  ownerUsername: "operator",
  currentStep: "intent",
  revision: 1,
  data: {},
  findings: [],
  planPreview: "",
  updatedAt: now,
  expiresAt: "2026-09-04T12:00:00Z",
}

export const blueprintCatalogue = [
  {
    id: "minecraft-java",
    version: "1.0.0",
    name: "Minecraft (Java Edition)",
    category: "game",
    profile: "game",
    description: "A Minecraft Java Edition server.",
    iconId: "game",
    docsUrl: "https://docker-minecraft-server.readthedocs.io/en/latest/",
    license: "Apache-2.0",
    maintainer: "Just Dashboard",
    reviewedAt: "2026-09-11",
    access: {
      kind: "open",
      note: "Players join with the Java Edition client; the whitelist is off until you turn it on.",
    },
    image: "itzg/minecraft-server:2026.9.1-java21",
    memoryMb: 2048,
    requiresAcceptance: true,
  },
  {
    id: "uptime-kuma",
    version: "1.0.0",
    name: "Uptime Kuma",
    category: "http",
    profile: "web",
    description: "Self-hosted uptime monitoring.",
    iconId: "monitoring",
    docsUrl: "https://github.com/louislam/uptime-kuma/wiki",
    license: "MIT",
    maintainer: "Just Dashboard",
    reviewedAt: "2026-09-11",
    access: {
      kind: "setup",
      note: "The first visitor is shown a Create-admin form, so open it the moment it is ready.",
    },
    image: "louislam/uptime-kuma:1.23.16",
    memoryMb: 512,
    deploymentSupported: true,
  },
  {
    id: "vaultwarden",
    version: "1.0.0",
    name: "Vaultwarden",
    category: "http",
    profile: "web",
    description: "A password manager that needs its own public URL.",
    iconId: "lock",
    docsUrl: "https://github.com/dani-garcia/vaultwarden/wiki",
    license: "AGPL-3.0",
    maintainer: "Just Dashboard",
    reviewedAt: "2026-09-11",
    access: {
      kind: "token",
      secretVariable: "ADMIN_TOKEN",
      path: "/admin",
      note: "Sign in at /admin with the generated admin token and invite your own account.",
    },
    image: "vaultwarden/server:1.32.7-alpine",
    memoryMb: 512,
    deploymentSupported: true,
  },
  {
    id: "postgresql",
    version: "1.0.0",
    name: "PostgreSQL",
    category: "database",
    profile: "database",
    description: "The default relational database.",
    iconId: "database",
    docsUrl: "https://www.postgresql.org/docs/16/index.html",
    license: "PostgreSQL",
    maintainer: "Just Dashboard",
    reviewedAt: "2026-09-11",
    access: {
      kind: "client",
      usernameVariable: "POSTGRES_USER",
      secretVariable: "POSTGRES_PASSWORD",
      note: "Nothing signs in through a browser; other containers connect with these over the Docker network.",
    },
    image: "postgres:16-alpine",
    memoryMb: 512,
    deploymentSupported: true,
  },
]

export const postgresBlueprint = {
  ...blueprintCatalogue[3],
  image: { reference: "postgres:16-alpine", tagPolicy: "pinned", pullPolicy: "missing" },
  provenance: {
    maintainer: "Just Dashboard",
    license: "PostgreSQL",
    upstreamUrl: "https://hub.docker.com/_/postgres",
    reviewedAt: "2026-09-11",
    minimumDashboard: "0.6.7",
  },
  resources: { memoryMb: 512, minMemoryMb: 256 },
  update: { detector: "registry", backupFirst: true, notes: "" },
  inputs: [
    {
      name: "database",
      kind: "text",
      label: "Database name",
      default: "app",
      required: true,
      variable: "POSTGRES_DB",
    },
    {
      name: "username",
      kind: "text",
      label: "Database user",
      default: "app",
      required: true,
      variable: "POSTGRES_USER",
    },
  ],
  secrets: [
    { name: "password", variable: "POSTGRES_PASSWORD", label: "Database password", length: 40 },
  ],
}

/**
 * The shape the eight definitions that need their own public URL share: one
 * required `domain` and nothing else the operator has to know. Its name is
 * written into the application's own configuration *and* becomes the plan's
 * domain, which is why the page asks for it once.
 */
export const vaultwardenBlueprint = {
  ...blueprintCatalogue[2],
  image: {
    reference: "vaultwarden/server:1.32.7-alpine",
    tagPolicy: "pinned",
    pullPolicy: "missing",
  },
  provenance: {
    maintainer: "Just Dashboard",
    license: "AGPL-3.0",
    upstreamUrl: "https://github.com/dani-garcia/vaultwarden",
    reviewedAt: "2026-09-11",
    minimumDashboard: "0.6.7",
  },
  resources: { memoryMb: 512, minMemoryMb: 256 },
  update: { detector: "registry", backupFirst: true, notes: "" },
  inputs: [
    {
      name: "domain",
      kind: "domain",
      label: "Public domain",
      required: true,
      description:
        "Vaultwarden needs its exact public URL; WebAuthn and app links break without it.",
    },
  ],
  secrets: [],
}

export const postgresRenderedConfiguration = {
  build: { method: "image", noCache: false, secrets: [], releaseTasks: [] },
  runtime: {
    image: "postgres:16-alpine",
    command: [],
    internalPort: 5432,
    hostPort: 0,
    bindAddress: "127.0.0.1",
    strategy: "stop_first",
    privileged: false,
    hostNetwork: false,
    capabilities: [],
    devices: [],
    memoryMb: 512,
    stopSignal: "SIGINT",
    mounts: [
      {
        source: "shop-db-0123456789abcdef",
        target: "/var/lib/postgresql/data",
        ownership: "managed",
      },
    ],
  },
  variables: [
    { name: "POSTGRES_DB", sensitivity: "plain", scopes: ["runtime"], value: "shop" },
    {
      name: "POSTGRES_PASSWORD",
      sensitivity: "secret",
      scopes: ["runtime"],
      required: true,
      generate: 40,
    },
    { name: "POSTGRES_USER", sensitivity: "plain", scopes: ["runtime"], value: "app" },
  ],
  dependencies: [
    {
      kind: "storage",
      ownership: "managed",
      resourceKind: "docker_volume",
      resourceId: "shop-db-0123456789abcdef",
      config: { purpose: "Every table this server holds", data: true, backup: true },
    },
  ],
  checks: [
    {
      name: "Accepts connections",
      kind: "command",
      phase: "readiness",
      required: true,
      config: { command: ["pg_isready", "-U", "postgres"], timeoutSeconds: 10, attempts: 6 },
    },
  ],
  domains: [],
}

export const minecraftBlueprint = {
  ...blueprintCatalogue[0],
  image: {
    reference: "itzg/minecraft-server:2026.9.1-java21",
    tagPolicy: "pinned",
    pullPolicy: "missing",
  },
  provenance: {
    maintainer: "Just Dashboard",
    license: "Apache-2.0",
    upstreamUrl: "https://github.com/itzg/docker-minecraft-server",
    reviewedAt: "2026-09-11",
    minimumDashboard: "0.6.7",
  },
  resources: { memoryMb: 2048, minMemoryMb: 1024 },
  update: {
    detector: "minecraft-java",
    notes: "Updating the server build never replaces the world.",
    backupFirst: true,
  },
  inputs: [
    {
      name: "server-type",
      kind: "choice",
      label: "Server software",
      default: "VANILLA",
      required: true,
      variable: "TYPE",
      choices: [
        { value: "VANILLA", label: "Vanilla", description: "The official server." },
        { value: "PAPER", label: "Paper", description: "Accepts Bukkit plugins." },
      ],
    },
    { name: "memory", kind: "memory", label: "Memory", default: "2048", minimum: 1024 },
    {
      name: "max-players",
      kind: "number",
      label: "Maximum players",
      default: "20",
      minimum: 1,
      maximum: 1000,
      variable: "MAX_PLAYERS",
    },
    {
      name: "online-mode",
      kind: "boolean",
      label: "Verify accounts with Mojang",
      default: "true",
      variable: "ONLINE_MODE",
      advanced: true,
    },
    {
      name: "eula",
      kind: "accept",
      label: "I accept the Minecraft EULA",
      required: true,
      variable: "EULA",
      acceptUrl: "https://aka.ms/MinecraftEULA",
      description: "The server refuses to start without this.",
    },
  ],
  secrets: [],
}

export const minecraftVersions = {
  status: "available",
  source: "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json",
  checkedAt: now,
  recommended: "1.21.4",
  versions: [
    { id: "1.21.4", kind: "release", releasedAt: now, recommended: true, latest: true },
    { id: "1.21.3", kind: "release", releasedAt: now },
  ],
}

/**
 * The showcase: a fleet and a project with something in every place a page
 * can draw, for `mockProject(page, { showcase: true })`.
 *
 * The default fixture is one git project with one variable, one domain and
 * nothing linked, because dozens of specs assert the texts it produces. That
 * is the right thing to test against and the wrong thing to look at: a
 * capture of it shows every product mark, strip and reading the redesign
 * added as an empty state. This is the other half — varied sources, a stack,
 * a failure, a stopped server, two databases, three channels, four
 * credentials — so a screenshot shows what the pages look like in use. No
 * spec asserts against it, so it can grow with the pages.
 */

const ago = (hours: number) => new Date(Date.parse(now) - hours * 3_600_000).toISOString()

/** The run states that are over: a run in one of these is at no step. */
const ENDED = new Set<string>(["succeeded", "failed", "cancelled", "rolled_back", "superseded"])

function engineRun(
  fields: Pick<DeploymentEngineRun, "id" | "runNumber" | "projectId" | "environmentId" | "state"> &
    Partial<DeploymentEngineRun>,
): DeploymentEngineRun {
  return {
    operation: "deploy",
    trigger: "manual",
    actor: "operator",
    requestedAt: now,
    cancelRequested: false,
    planRevision: 1,
    priority: 500,
    slotClass: "heavy",
    metadata: {},
    ...fields,
  }
}

const COMMITS = [
  { subject: "Retry checkout when the card network times out", author: "Alex" },
  { subject: "Cache product images at the edge", author: "Mira" },
  { subject: "Move sessions to Redis", author: "Alex" },
  { subject: "Upgrade Next.js to 16.3", author: "Dan" },
  { subject: "Add the returns page", author: "Mira" },
]

/**
 * A project's older runs, newest first, a few hours apart, each ended in the
 * state `states` gives it. Git projects carry the commit each one built.
 */
function history(
  projectId: number,
  environmentId: number,
  firstId: number,
  states: DeploymentRunState[],
  git: boolean,
): DeploymentEngineRun[] {
  return states.map((state, index) => {
    const id = firstId - index
    const requestedAt = ago(6 + index * 9)
    const commit = COMMITS[index % COMMITS.length]
    return engineRun({
      id,
      runNumber: states.length - index,
      projectId,
      environmentId,
      state,
      // Only a repository is pushed to: an image's or a template's runs are
      // someone's.
      trigger: git && index % 3 === 0 ? "git_push" : "manual",
      actor: git && index % 3 === 0 ? "git-watch" : index % 2 ? "mira" : "operator",
      requestedAt,
      queuedAt: requestedAt,
      claimedAt: requestedAt,
      endedAt: new Date(Date.parse(requestedAt) + (80 + index * 7) * 1000).toISOString(),
      planRevision: Math.max(1, 2 - Math.floor(index / 6)),
      terminalCode: state === "failed" ? "health_gate_failed" : undefined,
      terminalReason:
        state === "failed" ? "The readiness check answered 502 six times in a row." : undefined,
      metadata: git
        ? { commit: { sha: `${(id * 7919).toString(16).padEnd(12, "0")}ab34`, ...commit } }
        : {},
    })
  })
}

/**
 * A run as one square of a card's strip. Loosely typed on the way in, because
 * the default fixture's `run` is a plain literal whose state is a string.
 */
function recent(run: {
  id: number
  runNumber: number
  state: string
  operation: string
  requestedAt: string
  endedAt?: string
}): DeploymentRecentRun {
  return {
    id: run.id,
    runNumber: run.runNumber,
    state: run.state as DeploymentRunState,
    operation: run.operation,
    requestedAt: run.requestedAt,
    endedAt: run.endedAt,
  }
}

/** The commit the showcase's live run is building. */
const showcaseCommit = {
  sha: "a12bc34d56ef7890a12bc34d56ef7890a12bc34d",
  subject: COMMITS[0].subject,
  author: "Alex",
  authoredAt: ago(1),
}

/**
 * Project 7's thirteen runs before the one in flight. The newest recorded
 * release 20, the one that is live, so the pages have a live run to name.
 */
const showcaseHistory = history(
  7,
  12,
  83,
  [
    "succeeded",
    "succeeded",
    "failed",
    "succeeded",
    "succeeded",
    "succeeded",
    "cancelled",
    "succeeded",
    "succeeded",
    "rolled_back",
    "succeeded",
    "succeeded",
    "succeeded",
  ],
  true,
).map((run, index) => (index === 0 ? { ...run, releaseId: 20 } : run))

/** The commit the showcase's `index`th newest ended run built. */
const showcaseRevision = (index: number) =>
  (showcaseHistory[index].metadata.commit as { sha: string }).sha

/**
 * An ended showcase run's steps: a failed run stopped at its readiness check,
 * a cancelled one during its build, the rest went all the way. The build
 * takes what the run's other steps leave of its time, so the waterfall adds
 * up to the run's own duration.
 */
function endedSteps(run: DeploymentEngineRun) {
  const start = run.claimedAt ?? run.requestedAt
  const seconds = (Date.parse(run.endedAt ?? start) - Date.parse(start)) / 1000
  const stop =
    run.state === "failed"
      ? "verify_readiness"
      : run.state === "cancelled"
        ? "build_artifact"
        : undefined
  const at = steps.findIndex((step) => step.key === stop)
  return timedSteps(
    run.id,
    start,
    (key) => {
      const index = steps.findIndex((step) => step.key === key)
      if (key === "backup_gate") return "skipped"
      if (at < 0 || index < at) return "passed"
      if (index === at) return run.state === "failed" ? "failed" : "cancelled"
      return "pending"
    },
    Math.max(20, seconds - 4 * 13),
  )
}

/** What the showcase's website preview frames: a shop's front page. */
const SHOWCASE_SITE =
  "<!doctype html><body style='margin:0;font-family:system-ui,sans-serif;background:#0b1020;color:#fff'><header style='padding:24px 48px;display:flex;justify-content:space-between'><b>Acme</b><span>Shop · Returns · Sign in</span></header><main style='padding:80px 48px'><h1 style='font-size:64px;margin:0'>Ship faster.</h1><p style='font-size:22px;opacity:.7'>Everything your store needs.</p></main></body>"

function summary(
  fields: Pick<DeploymentSummary, "id" | "name" | "sourceKind" | "buildMethod"> &
    Partial<DeploymentSummary>,
): DeploymentSummary {
  return {
    profile: "web",
    environmentId: fields.id + 100,
    environmentName: "Production",
    environmentKind: "production",
    desiredRevision: 2,
    liveReleaseId: fields.id * 10,
    livePlanRevision: 2,
    strategy: "blue_green",
    expectedDowntime: false,
    health: "healthy",
    pendingChanges: false,
    stopped: false,
    serviceCount: 1,
    updatedAt: ago(2),
    ...fields,
  }
}

/** A finished project: its runs, and the strip and last run they make. */
function withRuns(project: DeploymentSummary, runs: DeploymentEngineRun[]): DeploymentSummary {
  return { ...project, lastRun: runs[0], recentRuns: runs.map(recent) }
}

/**
 * The rest of the fleet beside project 7: a blueprint, an image, a Compose
 * stack of three, a failed build, a stopped game server and a worker with
 * changes waiting.
 */
export const showcaseFleet: DeploymentSummary[] = [
  withRuns(
    summary({
      id: 8,
      name: "automations",
      sourceKind: "blueprint",
      buildMethod: "image",
      sourceRepository: "n8n@1.2.3",
      images: ["docker.n8n.io/n8nio/n8n:1.2.3"],
      endpoint: "https://n8n.example.test",
      internalPort: 5678,
    }),
    history(8, 108, 169, ["succeeded", "succeeded", "succeeded", "succeeded"], false),
  ),
  withRuns(
    summary({
      id: 10,
      name: "status-page",
      sourceKind: "image",
      buildMethod: "image",
      sourceRepository: "louislam/uptime-kuma:1.23.16",
      images: ["louislam/uptime-kuma:1.23.16"],
      endpoint: "https://status.example.test",
      internalPort: 3001,
    }),
    history(10, 110, 259, ["succeeded", "succeeded", "failed", "succeeded"], false),
  ),
  withRuns(
    summary({
      id: 11,
      name: "shop-stack",
      profile: "compose",
      sourceKind: "git",
      buildMethod: "compose",
      sourceRef: "main",
      sourceRevision: "9f8e7d6c5b4a39281706",
      sourceRepository: "acme/shop",
      sourceRemote: "https://gitlab.com/acme/shop",
      images: ["registry.gitlab.com/acme/shop:2.4.0", "postgres:16-alpine", "redis:7-alpine"],
      serviceCount: 3,
      endpoint: "https://shop.example.test",
      internalPort: 8080,
    }),
    history(11, 111, 349, ["succeeded", "succeeded", "succeeded", "failed", "succeeded"], true),
  ),
  withRuns(
    summary({
      id: 12,
      name: "docs-site",
      profile: "static",
      sourceKind: "git",
      buildMethod: "recipe",
      recipe: "node",
      framework: "astro",
      sourceRef: "main",
      sourceRepository: "acme/docs",
      sourceRemote: "git@codeberg.org:acme/docs.git",
      health: "failed",
      endpoint: "https://docs.example.test",
    }),
    history(12, 112, 439, ["failed", "failed", "succeeded", "succeeded"], true).map((run, index) =>
      index === 0
        ? {
            ...run,
            terminalCode: "build_failed",
            terminalReason:
              "bun run build exited with status 1: Cannot find module 'astro:content'",
          }
        : run,
    ),
  ),
  withRuns(
    summary({
      id: 13,
      name: "survival",
      profile: "game",
      sourceKind: "blueprint",
      buildMethod: "image",
      sourceRepository: "minecraft-java@1.0.0",
      images: ["itzg/minecraft-server:2026.9.1-java21"],
      stopped: true,
      health: "unavailable",
      hostPort: 25565,
      internalPort: 25565,
    }),
    history(13, 113, 529, ["succeeded", "succeeded"], false).map((run, index) =>
      index === 0 ? { ...run, operation: "stop" } : run,
    ),
  ),
  withRuns(
    summary({
      id: 14,
      name: "billing-worker",
      profile: "worker",
      sourceKind: "git",
      buildMethod: "recipe",
      recipe: "python",
      framework: "fastapi",
      sourceRef: "main",
      sourceRepository: "acme/billing",
      sourceRemote: "https://github.com/acme/billing",
      pendingChanges: true,
      desiredRevision: 4,
      livePlanRevision: 3,
    }),
    history(14, 114, 619, ["succeeded", "succeeded", "succeeded"], true),
  ),
]

/** The last hour of each web project in the fleet, for the cards' lines. */
const showcaseTraffic: Record<string, TrafficPulse> = Object.fromEntries(
  [
    ["8", 3.2, 0],
    ["10", 11.8, 0.002],
    ["11", 42.5, 0.004],
    ["12", 0.4, 0.21],
  ].map(([id, perMinute, errorRate]) => [
    id,
    {
      status: "available",
      perMinute: Number(perMinute),
      errorRate: Number(errorRate),
      pages: Math.round(Number(perMinute) * 18),
      points: Array.from(
        { length: 60 },
        (_, i) => Number(perMinute) * (0.7 + 0.3 * Math.sin((i + Number(id)) / 6)),
      ),
    },
  ]),
)

const API_CONTAINER = "c0ffee".padEnd(64, "1")
const DB_CONTAINER = "d00d1e".padEnd(64, "2")

/** Project 7's live release: the app and the database beside it. */
export const showcaseRuntime: DeploymentRuntimeServices = {
  status: "available",
  observedAt: now,
  services: [
    {
      containerId: API_CONTAINER,
      name: "api-production-r20",
      releaseId: 20,
      liveRelease: true,
      state: "running",
      health: "healthy",
      imageId: `sha256:${"a".repeat(64)}`,
      image: "ghcr.io/acme/api:2",
      stack: "api-production",
      service: "web",
      startedAt: ago(5),
    },
    {
      containerId: DB_CONTAINER,
      name: "api-production-postgres",
      releaseId: 20,
      liveRelease: true,
      state: "running",
      health: "healthy",
      imageId: `sha256:${"d".repeat(64)}`,
      image: "postgres:16-alpine",
      stack: "api-production",
      service: "postgres",
      startedAt: ago(52),
    },
  ],
}

export const showcaseOperations: DeploymentOperations = {
  ...healthyOperations,
  runtime: showcaseRuntime,
  domains: {
    status: "available",
    siteName: "just-dashboard-env-12.conf",
    domains: [
      ...healthyOperations.domains.domains,
      {
        hostname: "www.example.test",
        https: true,
        ownership: "linked",
        route: "served",
        servedBy: "just-dashboard-env-12.conf",
        certificate: "expiring",
        certificateName: "www.example.test",
        certificateDaysLeft: 9,
        certificateIssuer: "E5",
        protected: true,
        deepLink: "/proxy/sites?site=just-dashboard-env-12.conf",
        certificateLink: "/proxy/certificates",
      },
      {
        hostname: "staging.example.test",
        https: false,
        ownership: "managed",
        route: "missing",
        certificate: "not requested",
      },
    ],
  },
  storage: {
    status: "available",
    mounts: [
      ...healthyOperations.storage.mounts,
      {
        source: "/srv/uploads",
        target: "/app/uploads",
        kind: "bind",
        ownership: "managed",
        readOnly: true,
        status: "missing",
        detail: "The path does not exist on the host.",
      },
    ],
  },
  backups: {
    status: "available",
    jobs: [{ ...healthyOperations.backups.jobs[0], deepLink: "/backups/4" }],
  },
  // As the observer reports them: a database connection's status is its
  // name, and backup jobs, volumes and bind paths are left to their own blocks.
  dependencies: {
    status: "available",
    items: [
      {
        kind: "database",
        resourceKind: "database_connection",
        resourceId: "11",
        available: true,
        status: "jd-postgres",
        deepLink: "/databases/connection?conn=11",
      },
      {
        kind: "database",
        resourceKind: "database_connection",
        resourceId: "12",
        available: true,
        status: "jd-redis",
        deepLink: "/databases/connection?conn=12",
      },
    ],
  },
  diagnosis: {
    status: "assessed",
    findings: [
      {
        code: "certificate.expiring",
        severity: "warning",
        title: "A certificate expires in 9 days",
        measured: "www.example.test · Let's Encrypt E5 · 9 days left",
        means: "Visitors see a certificate warning once it lapses.",
        action: "Renew it from the proxy's certificates.",
        owner: "domains",
        deepLink: "/proxy/certificates",
      },
    ],
    silences: [],
  },
}

/** Eight variables: secrets, plain values, two database references and a build token. */
const showcaseVariables: DeploymentVariable[] = (
  [
    ["API_TOKEN", "secret", ["runtime"], true],
    ["DATABASE_URL", "secret", ["runtime", "release_task"], false, "11"],
    ["REDIS_URL", "secret", ["runtime"], false, "12"],
    ["NEXT_PUBLIC_API_URL", "plain", ["build", "runtime"], false],
    ["STRIPE_SECRET_KEY", "secret", ["runtime"], true],
    ["SENTRY_DSN", "plain", ["build", "runtime"], false],
    ["NPM_TOKEN", "secret", ["build"], false],
    ["LOG_LEVEL", "plain", ["runtime"], false],
  ] as const
).map(([name, sensitivity, scopes, pending, database], index) => ({
  name,
  revision: index + 1,
  sensitivity,
  scopes: [...scopes],
  masked: sensitivity === "secret" ? "••••••••" : name === "LOG_LEVEL" ? "info" : "configured",
  valueDigest: `sha256:${String(index).repeat(64)}`,
  reference: database ? { kind: "database", target: database } : undefined,
  pending,
  createdBy: index % 3 === 1 ? "mira" : "operator",
  createdAt: ago(index * 30),
  environmentId: 12,
  desiredRevision: 3,
}))

/** Everything the settings pages edit, filled in. */
const showcaseConfiguration: Omit<DeploymentConfiguration, "variables"> = {
  build: {
    method: "recipe",
    recipe: "node",
    framework: "nextjs",
    packageManager: "bun",
    buildCommand: "bun run build",
    startCommand: "bun start",
    secrets: [{ variable: "NPM_TOKEN", step: "install" }],
    releaseTasks: [
      {
        name: "Migrate the database",
        command: "bun run db:migrate",
        timeoutSeconds: 300,
        env: ["DATABASE_URL"],
      },
    ],
  },
  runtime: {
    image: "",
    command: ["bun", "start"],
    internalPort: 3000,
    hostPort: 0,
    bindAddress: "127.0.0.1",
    strategy: "blue_green",
    memoryMb: 512,
    cpus: 1,
    pidsLimit: 256,
    restartPolicy: "on-failure",
    mounts: [
      { source: "api-data", target: "/data", ownership: "linked" },
      { source: "/srv/uploads", target: "/app/uploads", ownership: "managed", readOnly: true },
    ],
  },
  dependencies: [
    {
      kind: "database",
      ownership: "linked",
      resourceKind: "database_connection",
      resourceId: "11",
      config: {},
    },
    {
      kind: "database",
      ownership: "linked",
      resourceKind: "database_connection",
      resourceId: "12",
      config: {},
    },
    {
      kind: "backup",
      ownership: "linked",
      resourceKind: "backup_job",
      resourceId: "4",
      config: { requiredBeforeDeploy: true, maxAgeSeconds: 86400, requireRestoreTest: false },
    },
    {
      kind: "storage",
      ownership: "linked",
      resourceKind: "docker_volume",
      resourceId: "api-data",
      config: {},
    },
  ],
  checks: [
    {
      name: "Answers on /health",
      kind: "http",
      phase: "readiness",
      required: true,
      config: { path: "/health", attempts: 20, intervalSeconds: 3, timeoutSeconds: 5 },
    },
    {
      name: "Checkout page renders",
      kind: "http",
      phase: "smoke",
      required: false,
      config: { path: "/checkout", attempts: 3, timeoutSeconds: 10 },
    },
  ],
  domains: [
    { hostname: "api.example.test", https: true, ownership: "managed" },
    {
      hostname: "www.example.test",
      https: true,
      ownership: "linked",
      protection: { username: "team", hash: "$2a$10$showcase" },
    },
    { hostname: "staging.example.test", https: false, ownership: "managed" },
  ],
}

const showcaseSource: DeploymentDraftSource = {
  kind: "git",
  mode: "connected_repository",
  url: "https://github.com/acme/api.git",
  provider: "github",
  repository: "acme/api",
  ref: "main",
  credentialId: 21,
}

const showcaseIdentity: SourceIdentity = {
  kind: "git",
  remote: "https://github.com/acme/api.git",
  repository: "acme/api",
  ref: "main",
  revision: showcaseCommit.sha,
}

/** What the showcase has saved and not yet deployed. */
const showcasePending: DeploymentPendingChange[] = [
  { kind: "variable", name: "API_TOKEN", change: "changed" },
  { kind: "variable", name: "STRIPE_SECRET_KEY", change: "added" },
  { kind: "check", name: "health checks", change: "changed" },
  { kind: "build", name: "build plan", change: "changed" },
]

/** The GitHub App, a GitLab token, a Docker Hub login and an SSH key. */
export const showcaseCredentials: DeploymentCredential[] = [
  {
    id: 21,
    name: "acme (GitHub App)",
    kind: "github_app",
    target: "github.com",
    createdAt: ago(900),
    updatedAt: ago(900),
    lastUsedAt: ago(1),
    usedBy: 3,
    usedByProjectIds: [7, 14],
  },
  {
    id: 22,
    name: "GitLab deploy token",
    kind: "git_bearer",
    target: "gitlab.com",
    username: "gitlab+deploy-token-4412",
    createdAt: ago(600),
    updatedAt: ago(200),
    lastUsedAt: ago(30),
    usedBy: 1,
    usedByProjectIds: [11],
  },
  {
    id: 23,
    name: "Docker Hub",
    kind: "registry",
    target: "docker.io",
    username: "acmebot",
    createdAt: ago(400),
    updatedAt: ago(400),
    lastUsedAt: ago(52),
    usedBy: 2,
    usedByProjectIds: [8, 10],
  },
  {
    id: 24,
    name: "Codeberg deploy key",
    kind: "git_ssh",
    target: "codeberg.org",
    createdAt: ago(300),
    updatedAt: ago(300),
    usedBy: 0,
    usedByProjectIds: [],
  },
]

const showcaseGitHubApp: GitHubAppStatus = {
  configured: true,
  app: {
    id: 912345,
    slug: "just-dashboard-acme",
    name: "Just Dashboard (acme)",
    owner: "acme",
    htmlUrl: "https://github.com/apps/just-dashboard-acme",
    createdAt: ago(900),
  },
  installations: [
    {
      id: 5501,
      account: "acme",
      accountType: "Organization",
      htmlUrl: "https://github.com/organizations/acme/settings/installations/5501",
      repositorySelection: "selected",
      credentialId: 21,
    },
  ],
  installUrl: "https://github.com/apps/just-dashboard-acme/installations/new",
  webhookUrl: "https://dash.example.test/api/v1/deploy/github-app/webhook",
}

const showcaseAppRepositories: GitHubAppRepository[] = [
  ["api", "TypeScript", "The storefront's API", 1],
  ["billing", "Python", "Invoices and payment retries", 20],
  ["docs", "MDX", "Product documentation", 45],
  ["design-tokens", "TypeScript", undefined, 300],
].map(([name, language, description, hours]) => ({
  installationId: 5501,
  account: "acme",
  nameWithOwner: `acme/${name}`,
  name: String(name),
  description: description as string | undefined,
  language: String(language),
  private: name !== "docs",
  defaultBranch: "main",
  cloneUrl: `https://github.com/acme/${name}.git`,
  htmlUrl: `https://github.com/acme/${name}`,
  pushedAt: ago(Number(hours)),
  credentialId: 21,
}))

const showcaseCliRepositories: GitHubRepoSummary[] = [
  ["Wayy01", "wesmokefish", "TypeScript", "A Next.js site"],
  ["Wayy01", "dotfiles", "Shell", undefined],
].map(([owner, name, language, description]) => ({
  nameWithOwner: `${owner}/${name}`,
  name: String(name),
  owner: String(owner),
  description,
  url: `https://github.com/${owner}/${name}`,
  cloneUrl: `https://github.com/${owner}/${name}.git`,
  defaultBranch: "main",
  language,
  private: false,
  fork: false,
  archived: false,
  pushedAt: ago(48),
}))

/** Fourteen outcomes, oldest first, with the failures where `failed` puts them. */
function outcomes(failed: number[], test: number[] = []) {
  return Array.from({ length: 14 }, (_, index) => ({
    status: failed.includes(index) ? "failed" : "delivered",
    createdAt: ago((14 - index) * 7),
    test: test.includes(index),
  }))
}

/** An enabled Discord with deliveries, a paused e-mail and a webhook that is failing. */
export const showcaseChannels: NotificationChannel[] = [
  {
    id: 61,
    name: "Team alerts",
    kind: "discord",
    url: "https://discord.com/api/webhooks/123456/••••",
    target: "https://discord.com/api/webhooks/123456/••••",
    events: ["run.failed", "run.succeeded"],
    enabled: true,
    createdAt: ago(700),
    updatedAt: ago(90),
    lastDelivery: {
      status: "delivered",
      event: "run.succeeded",
      responseClass: "2xx",
      createdAt: ago(6),
    },
    recent: outcomes([4], [0]),
  },
  {
    id: 62,
    name: "On-call mail",
    kind: "email",
    url: "oncall@acme.test",
    target: "oncall@acme.test",
    events: ["run.failed"],
    enabled: false,
    via: "smtp.fastmail.com",
    createdAt: ago(500),
    updatedAt: ago(48),
    lastDelivery: {
      status: "delivered",
      event: "run.failed",
      responseClass: "2xx",
      createdAt: ago(60),
    },
    recent: outcomes([]).slice(9),
  },
  {
    id: 63,
    name: "Status hook",
    kind: "webhook",
    url: "https://hooks.acme.test/deploys",
    target: "https://hooks.acme.test/deploys",
    events: ["run.started", "run.succeeded", "run.failed", "run.cancelled"],
    enabled: true,
    createdAt: ago(300),
    updatedAt: ago(300),
    lastDelivery: {
      status: "failed",
      event: "run.started",
      responseClass: "5xx",
      createdAt: ago(0.2),
      nextAttemptAt: ago(-0.1),
    },
    recent: outcomes([9, 11, 12, 13]),
  },
]

/** A channel's delivery log, newest first, each naming the run it announced. */
function showcaseDeliveries(channelId: number): NotificationDelivery[] {
  const channel = showcaseChannels.find((one) => one.id === channelId)
  return [...(channel?.recent ?? [])].reverse().map((outcome, index) => {
    const run = [...showcaseHistory][index % showcaseHistory.length]
    return {
      id: channelId * 100 + index,
      channelId,
      runId: outcome.test ? undefined : run.id,
      projectId: outcome.test ? undefined : 7,
      projectName: outcome.test ? undefined : "api-production",
      runNumber: outcome.test ? undefined : run.runNumber,
      event: outcome.test ? "test" : run.state === "failed" ? "run.failed" : "run.succeeded",
      attempt: outcome.status === "failed" ? 3 : 1,
      status: outcome.status,
      responseClass: outcome.status === "failed" ? "5xx" : "2xx",
      createdAt: outcome.createdAt,
      completedAt: outcome.createdAt,
    }
  })
}

/** Fourteen trigger decisions, oldest first. */
function decisions(pattern: string[]) {
  return Array.from({ length: 14 }, (_, index) => {
    const decision = pattern[index % pattern.length]
    return {
      decision,
      reason:
        decision === "suppressed"
          ? "No watched path changed"
          : decision === "rejected"
            ? "Signature did not match"
            : undefined,
      receivedAt: ago((14 - index) * 5),
    }
  })
}

const showcaseTriggers: DeploymentTrigger[] = [
  {
    id: 31,
    projectId: 7,
    environmentId: 12,
    name: "GitHub pushes",
    kind: "github",
    provider: "github",
    config: {
      repository: "acme/api",
      ref: "main",
      events: ["push", "pull_request"],
      preview: true,
      previewQuota: 3,
      previewDomain: "pr-{number}.preview.example.test",
      delivery: "app",
    },
    hookId: "provider-hook",
    enabled: true,
    lastDeliveryAt: ago(1),
    lastStatus: "accepted",
    lastDelivery: {
      deliveryId: "72d1b0a0-5b1e-11f0-9e7b-acme00000001",
      event: "push",
      ref: "refs/heads/main",
      decision: "accepted",
      runId: 84,
      receivedAt: ago(1),
    },
    recent: decisions(["accepted", "accepted", "suppressed", "accepted", "accepted"]),
  },
  {
    id: 32,
    projectId: 7,
    environmentId: 12,
    name: "Release API",
    kind: "api",
    config: {},
    hookId: "release-api-hook",
    enabled: true,
    lastDeliveryAt: ago(26),
    lastStatus: "rejected",
    lastDelivery: {
      deliveryId: "api-3301",
      event: "deploy",
      decision: "rejected",
      reason: "Signature did not match",
      receivedAt: ago(26),
    },
    recent: decisions(["accepted", "rejected", "accepted", "accepted"]),
  },
]

function showcaseTriggerDeliveries(triggerId: number): DeploymentTriggerDelivery[] {
  const trigger = showcaseTriggers.find((one) => one.id === triggerId)
  return [...(trigger?.recent ?? [])].reverse().map((outcome, index) => ({
    deliveryId: `${triggerId}-${1000 - index}`,
    event: trigger?.kind === "api" ? "deploy" : index % 4 === 3 ? "pull_request" : "push",
    ref: trigger?.kind === "api" ? undefined : "refs/heads/main",
    decision: outcome.decision,
    reason: outcome.reason,
    runId: outcome.decision === "accepted" ? showcaseHistory[index % 13].id : undefined,
    receivedAt: outcome.receivedAt,
  }))
}

/** A nightly deploy in the operator's own zone, and a paused weekly backup. */
const showcaseSchedules: DeploymentSchedule[] = [
  {
    id: 41,
    environmentId: 12,
    name: "Nightly deploy",
    expression: "0 3 * * *",
    timezone: "Europe/Chisinau",
    enabled: true,
    nextRunAt: "2026-09-04T00:00:00Z",
    nextRuns: [4, 5, 6, 7, 8].map((day) => `2026-09-0${day}T00:00:00Z`),
    steps: [{ action: "deploy", config: {}, required: true }],
  },
  {
    id: 42,
    environmentId: 12,
    name: "Weekly backup",
    expression: "30 2 * * 0",
    timezone: "UTC",
    enabled: false,
    steps: [{ action: "backup", config: { backupJobId: 4 }, required: true }],
  },
]

const showcaseApprovals: DeploymentPreviewApproval[] = [
  {
    configured: true,
    id: 71,
    triggerId: 31,
    providerRef: "43",
    revision: "f00dbabe5eed1234f00dbabe5eed1234f00dbabe",
    repository: "acme/api",
    headRepository: "mira/api",
    headRef: "feature/login",
    author: "mira",
    state: "pending",
    updatedAt: ago(3),
  },
]

const showcaseLinks: DeploymentDatabaseLink[] = [
  {
    connectionId: 11,
    name: "jd-postgres",
    driver: "postgres",
    database: "app",
    network: "jd-env-12",
    hostname: "db-11.jd.internal",
    status: "connected",
    checkedAt: now,
  },
  {
    connectionId: 12,
    name: "jd-redis",
    driver: "redis",
    database: "0",
    network: "jd-env-12",
    hostname: "db-12.jd.internal",
    status: "stale",
    detail: "The network alias has not been confirmed since the last restart.",
    checkedAt: ago(3),
  },
]

const showcaseConnections: DbConnection[] = [
  {
    id: 11,
    name: "jd-postgres",
    driver: "postgres",
    host: "127.0.0.1",
    port: "5432",
    user: "app",
    database: "app",
    createdAt: ago(800),
  },
  {
    id: 12,
    name: "jd-redis",
    driver: "redis",
    host: "127.0.0.1",
    port: "6379",
    user: "",
    database: "0",
    createdAt: ago(700),
  },
]

const showcaseProvisionOptions: DbProvisionOption[] = [
  { engine: "postgres", label: "PostgreSQL", image: "postgres:17-alpine", driver: "postgres" },
  { engine: "mysql", label: "MySQL", image: "mysql:8.4", driver: "mysql" },
  { engine: "mariadb", label: "MariaDB", image: "mariadb:11", driver: "mysql" },
  { engine: "redis", label: "Redis", image: "redis:7-alpine", driver: "redis" },
  { engine: "mongodb", label: "MongoDB", image: "mongo:8", driver: "mongodb" },
]

/** Fourteen nightly runs of the job the release gates on, newest first, one failed. */
const showcaseBackupRuns: BackupRun[] = Array.from({ length: 14 }, (_, index) => ({
  id: 100 - index,
  jobId: 4,
  startedAt: ago(9 + index * 24),
  endedAt: ago(9 + index * 24 - 0.05),
  status: index === 3 ? "failed" : "success",
  artifact: index === 3 ? "" : `/var/backups/api-nightly-${100 - index}.tar.zst`,
  sizeBytes: index === 3 ? 0 : 1024 * 1024 * (40 + index),
  log: "",
  trigger: "schedule",
  duration: "3m 2s",
}))

const showcaseBackupJobs: BackupJob[] = [
  {
    id: 4,
    name: "api-nightly",
    sources: ["/var/lib/docker/volumes/api-data/_data"],
    excludes: [],
    targetKind: "local",
    target: { path: "/var/backups" },
    schedule: "0 3 * * *",
    retention: 14,
    retentionDays: 0,
    enabled: true,
    createdAt: ago(900),
    hasCredentials: false,
    overdue: false,
    databaseDumps: [11],
    lastSuccessAt: ago(9),
    nextRun: ago(-15),
    lastRun: showcaseBackupRuns[0],
    stored: { runs: 13, bytes: 13 * 1024 * 1024 * 46 },
  },
  {
    id: 5,
    name: "uploads-offsite",
    sources: ["/srv/uploads"],
    excludes: [],
    targetKind: "b2",
    target: { bucket: "acme-backups", prefix: "uploads/" },
    schedule: "0 4 * * 0",
    retention: 8,
    retentionDays: 0,
    enabled: true,
    createdAt: ago(600),
    hasCredentials: true,
    overdue: false,
    databaseDumps: [],
    stored: { runs: 8, bytes: 8 * 1024 * 1024 * 1024 },
  },
]

function container(
  id: string,
  name: string,
  image: string,
  fields: Partial<Container> = {},
): Container {
  return {
    id,
    names: [`/${name}`],
    name,
    image,
    imageId: `sha256:${id.slice(0, 12)}`,
    command: "",
    state: "running",
    status: "Up 5 hours (healthy)",
    health: "healthy",
    createdAt: ago(5),
    startedAt: ago(5),
    uptimeSeconds: 5 * 3600,
    ports: [],
    labels: {},
    networks: ["jd-env-12"],
    composeStack: "api-production",
    exposure: [],
    hasHealthcheck: true,
    restartPolicy: "on-failure",
    inspected: true,
    ...fields,
  }
}

const showcaseContainers: Container[] = [
  container(API_CONTAINER, "api-production-r20", "ghcr.io/acme/api:2", {
    command: "bun start",
    composeService: "web",
    ports: [{ ip: "127.0.0.1", privatePort: 3000, publicPort: 41020, type: "tcp" }],
    exposure: [
      {
        hostIp: "127.0.0.1",
        hostPort: 41020,
        containerPort: 3000,
        protocol: "tcp",
        scope: "loopback",
        label: "loopback",
        summary: "Only this server can reach it; the proxy forwards to it.",
      },
    ],
    memoryLimit: 512 * 1024 * 1024,
    cpuLimit: 1,
    labels: releaseOwner,
  }),
  container(DB_CONTAINER, "api-production-postgres", "postgres:16-alpine", {
    command: "docker-entrypoint.sh postgres",
    composeService: "postgres",
    status: "Up 2 days (healthy)",
    startedAt: ago(52),
    uptimeSeconds: 52 * 3600,
    memoryLimit: 1024 * 1024 * 1024,
  }),
]

/** An hour of one container, a point a minute, with a climb in the middle. */
function containerHistory(name: string, cpu: number, memBytes: number): ContainerHistory {
  const points = Array.from({ length: 60 }, (_, index) => {
    const swell = 1 + 0.35 * Math.sin(index / 7) + (index > 30 && index < 38 ? 0.6 : 0)
    return {
      ts: new Date(Date.parse(now) - (60 - index) * 60_000).toISOString(),
      samples: 6,
      cpu: cpu * swell,
      cpuPeak: cpu * swell * 1.4,
      mem: (memBytes * swell * 100) / (512 * 1024 * 1024),
      memPeak: (memBytes * swell * 110) / (512 * 1024 * 1024),
      memBytes: memBytes * swell,
      memBytesPeak: memBytes * swell * 1.1,
      memLimit: 512 * 1024 * 1024,
      pids: 24,
      netRx: 42_000 * swell,
      netTx: 118_000 * swell,
      blockRead: 1200,
      blockWrite: 8400 * swell,
    }
  })
  return {
    name,
    from: points[0].ts,
    to: points[59].ts,
    stepSeconds: 60,
    sampleIntervalSeconds: 10,
    retentionSeconds: 7 * 86400,
    earliest: ago(24 * 6),
    points,
  }
}

function containerStats(id: string, name: string, cpu: number, memUsage: number): ContainerStats {
  return {
    id,
    name,
    ts: now,
    cpuPercent: cpu,
    memUsage,
    memLimit: 512 * 1024 * 1024,
    memPercent: (memUsage * 100) / (512 * 1024 * 1024),
    netRx: 48_000_000,
    netTx: 212_000_000,
    blockRead: 12_000_000,
    blockWrite: 88_000_000,
    memLimited: true,
    memHostPercent: 3.1,
    hostCpus: 4,
    cpuLimit: 1,
    pids: 24,
    onlineCpus: 4,
    cpuTotal: 0,
    systemCpu: 0,
  }
}

const showcaseVolumes: VolumeDetail[] = [
  {
    name: "api-data",
    driver: "local",
    mountpoint: "/var/lib/docker/volumes/api-data/_data",
    createdAt: ago(900),
    scope: "local",
    labels: {},
    size: 1024 * 1024 * 312,
    refCount: 1,
    inUse: true,
    usedBy: [
      {
        id: API_CONTAINER,
        name: "api-production-r20",
        state: "running",
        destination: "/data",
        readOnly: false,
        stack: "api-production",
      },
    ],
  },
]

/** Project 7's live release as the Metrics view reads it: ten minutes either side of activation. */
function metricWindow(releaseId: number, cpu: number, memBytes: number) {
  const points = containerHistory("api-production-r20", cpu, memBytes).points.slice(0, 10)
  return {
    releaseId,
    status: "available",
    history: { series: [{ containerId: API_CONTAINER, points }] },
    sources: [
      { containerId: API_CONTAINER, name: "api-production-r20", image: "ghcr.io/acme/api:2" },
    ],
  }
}

const showcaseHostname = (hostname: string): DeploymentHostnameSuggestion => ({
  hostname,
  covered: false,
  certificateMethod: "caddy",
  method: "custom",
  address: "203.0.113.7",
  resolves: hostname.endsWith("example.test"),
  detail: `Point an A record for ${hostname} at 203.0.113.7; a certificate is issued on the first request.`,
})

/**
 * Every read the showcase answers that the default fixture leaves to 503:
 * the pages around a project — credentials, the GitHub App, Docker, backups,
 * databases — and the logs the delivery rows and run views open.
 */
function showcaseRead(path: string, url: URL): unknown {
  if (path === "/deploy/credentials") return showcaseCredentials
  if (path === "/deploy/github-app/") return showcaseGitHubApp
  if (path === "/deploy/github-app/repositories") return showcaseAppRepositories
  if (path === "/git/github/") {
    return { available: true, account: { loggedIn: true, login: "Wayy01", name: "Wayy" } }
  }
  if (path === "/git/github/repos") return showcaseCliRepositories
  if (path === "/git/github/branches") return [{ name: "main", default: true }, { name: "preview" }]
  if (path === "/deploy/drafts") return []
  if (path === "/deploy/blueprints/") return blueprintCatalogue
  if (path === "/deploy/hostname") {
    return showcaseHostname(url.searchParams.get("hostname") ?? "app.example.test")
  }
  const channel = path.match(/^\/deploy\/notifications\/(\d+)\/deliveries$/)
  if (channel) return showcaseDeliveries(Number(channel[1]))
  const trigger = path.match(/^\/deploy\/7\/environments\/12\/triggers\/(\d+)\/deliveries$/)
  if (trigger) return showcaseTriggerDeliveries(Number(trigger[1]))
  if (/^\/deploy\/7\/environments\/12\/schedules\/\d+\/runs$/.test(path)) {
    return { runs: showcaseHistory.slice(0, 4).map((run) => ({ ...run, trigger: "schedule" })) }
  }
  const past = path.match(/^\/deploy\/7\/runs\/(\d+)$/)
  const ended = past && showcaseHistory.find((entry) => entry.id === Number(past[1]))
  if (ended) return { run: ended, steps: endedSteps(ended) }
  if (path === "/deploy/7/runs/84/logs") {
    return {
      status: "available",
      activationCompletedAt: "2026-09-03T11:30:00Z",
      sources: showcaseRuntime.services.map((service) => ({
        containerId: service.containerId,
        name: service.name,
        image: service.image,
        liveUrl: `/logs?source=docker%3A${service.containerId}`,
        activationUrl: `/logs?source=docker%3A${service.containerId}&mode=search&since=2026-09-03T11:25:00Z&until=2026-09-03T11:35:00Z`,
      })),
    }
  }
  if (path === "/deploy/7/runs/84/metrics") {
    const host = containerHistory("host", 18, 2_400_000_000).points.slice(0, 10)
    return {
      status: "available",
      before: metricWindow(19, 9, 180_000_000),
      after: metricWindow(20, 12, 210_000_000),
      hostBefore: { points: host },
      hostAfter: { points: host },
    }
  }
  if (path === "/backups/") return showcaseBackupJobs
  if (path === "/backups/4") return showcaseBackupJobs[0]
  if (/^\/backups\/\d+\/runs$/.test(path)) return { running: false, runs: showcaseBackupRuns }
  if (path === "/databases/") return showcaseConnections
  if (path === "/databases/provision/options") return showcaseProvisionOptions
  if (path === "/docker/containers/") return showcaseContainers
  if (path === "/docker/containers/stats") {
    return [
      containerStats(API_CONTAINER, "api-production-r20", 12.4, 210_000_000),
      containerStats(DB_CONTAINER, "api-production-postgres", 3.1, 96_000_000),
    ] satisfies ContainerStats[]
  }
  if (path === "/docker/containers/stats/history") {
    return showcaseContainers.map((one) => {
      const api = one.id === API_CONTAINER
      const points = containerHistory(one.name, api ? 12 : 3, api ? 210_000_000 : 96_000_000).points
      return {
        name: one.name,
        cpu: points.map((point) => point.cpu),
        mem: points.map((point) => point.mem),
        cpuPeak: Math.max(...points.map((point) => point.cpuPeak)),
        memPeak: Math.max(...points.map((point) => point.memPeak)),
      }
    }) satisfies ContainerSparkline[]
  }
  const history = path.match(/^\/docker\/containers\/([^/]+)\/stats\/history$/)
  if (history) {
    const database = decodeURIComponent(history[1]) === DB_CONTAINER
    return database
      ? containerHistory("api-production-postgres", 3, 96_000_000)
      : containerHistory("api-production-r20", 12, 210_000_000)
  }
  const anomalies = path.match(/^\/docker\/containers\/([^/]+)\/anomalies$/)
  if (anomalies) return { container: anomalies[1], window: "24h", samples: 8640, anomalies: [] }
  if (path === "/docker/volumes/") return showcaseVolumes
  return undefined
}

export async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

export async function mockProject(
  page: Page,
  options: {
    normalized?: boolean
    runtime?: DeploymentRuntimeServices
    operations?: DeploymentOperations
    comparison?: ReleaseComparisonResponse
    /**
     * Serve the showcase instead of the bare project: a varied fleet, a
     * two-service runtime, linked databases, channels, credentials, triggers
     * and schedules — for captures and for specs about what a full page
     * draws. `runtime` and `operations` still win when given.
     */
    showcase?: boolean
  } = {},
) {
  const showcase = Boolean(options.showcase)
  // The showcase runs on its own clock, two and a half minutes after the
  // moment its records were written, so every age on its pages reads as the
  // fixture meant it rather than as however long ago that date is today. The
  // bare fixture keeps the real clock, which its specs measure against.
  const clock = showcase ? Date.parse(now) + 150_000 : undefined
  const minutesAgo = (minutes: number) =>
    new Date((clock ?? Date.now()) - minutes * 60_000).toISOString()
  if (clock !== undefined) {
    await page.clock.install({ time: clock })
    await page.clock.resume()
  }
  let runState = run.state
  let mutationCount = 0
  const actions: string[] = []
  let configurationRevision = 3
  let configurationPending = true
  let automationTriggers: Record<string, unknown>[] = showcase ? [...showcaseTriggers] : []
  let gitPolicy = {
    automatic: true,
    commitStatuses: true,
    revision: 0,
    watchInclude: [] as string[],
    watchExclude: [] as string[],
  }
  let automationSchedules: Record<string, unknown>[] = showcase ? [...showcaseSchedules] : []
  let notificationChannels: Record<string, unknown>[] = showcase ? [...showcaseChannels] : []
  let trafficAlerts: Record<string, unknown>[] = [
    {
      id: 91,
      projectId: 7,
      environmentId: 12,
      kind: "error_rate",
      threshold: 1,
      windowMinutes: 5,
      channels: [],
      enabled: true,
      state: "firing",
      stateSince: minutesAgo(12),
      observed: 4.2,
      checkedAt: now,
      firedAt: minutesAgo(12),
      createdAt: now,
      updatedAt: now,
    },
  ]
  let notificationTests = 0
  let scopedVariables: DeploymentVariable[] = showcase
    ? [...showcaseVariables]
    : [
        {
          name: "API_TOKEN",
          revision: 1,
          sensitivity: "secret",
          scopes: ["runtime"],
          masked: "••••••••",
          valueDigest: `sha256:${"1".repeat(64)}`,
          pending: true,
          createdBy: "operator",
          createdAt: now,
          environmentId: 12,
          desiredRevision: 3,
        },
      ]
  let normalizedConfiguration: Omit<DeploymentConfiguration, "variables"> = showcase
    ? structuredClone(showcaseConfiguration)
    : {
        build: { method: "recipe", recipe: "node" },
        runtime: {
          image: "",
          command: ["bun", "start"],
          internalPort: 3000,
          hostPort: 0,
          bindAddress: "127.0.0.1",
          strategy: "blue_green",
          mounts: [{ source: "api-data", target: "/data", ownership: "linked" }],
        },
        dependencies: [
          {
            kind: "backup",
            ownership: "linked",
            resourceKind: "backup_job",
            resourceId: "4",
            config: {
              requiredBeforeDeploy: true,
              maxAgeSeconds: 86400,
              requireRestoreTest: false,
            },
          },
        ],
        checks: [],
        domains: [{ hostname: "api.example.test", https: true, ownership: "managed" }],
      }
  const configurationBody = () => ({
    ...normalizedConfiguration,
    revision: configurationRevision,
    variables: scopedVariables.map((variable) => ({
      ...variable,
      pending: configurationPending && variable.pending,
      desiredRevision: configurationRevision,
    })),
    pending: {
      pending: configurationPending,
      desiredRevision: configurationRevision,
      liveReleaseId: 20,
      livePlanRevision: configurationPending ? 2 : configurationRevision,
      changes: configurationPending
        ? showcase
          ? showcasePending
          : [{ kind: "variable", name: "API_TOKEN", change: "changed" }]
        : [],
    },
    ...(showcase ? { source: showcaseSource, identity: showcaseIdentity } : {}),
  })
  // The list reads name the step a run still in flight is at; an ended run
  // has none.
  const liveRun = () => ({
    ...run,
    state: runState,
    currentStep: ENDED.has(runState)
      ? undefined
      : { key: "verify_readiness", label: "Readiness checks", state: "running" },
    // The showcase's history is thirteen runs deep, so the one in flight is the fourteenth.
    ...(showcase ? { runNumber: 14, metadata: { commit: showcaseCommit } } : {}),
  })
  const projectRuns = () => [liveRun(), ...(showcase ? showcaseHistory : [])]
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (method !== "GET") mutationCount += 1
    let body: unknown

    if (path === "/auth/session") body = user
    else if (path === "/dashboard/update") body = { current: "0.6.7", latest: "0.6.7" }
    else if (path === "/deploy/" && url.searchParams.get("view") === "fleet") {
      body = {
        deployments: [
          {
            ...deployment,
            lastRun: liveRun(),
            activeRun: liveRun(),
            recentRuns: projectRuns().map(recent),
          },
          ...(showcase ? showcaseFleet : []),
        ],
        activeWork: [
          {
            run: liveRun(),
            projectName: "api-production",
            environment: "Production",
            currentStep: "verify_readiness",
            currentStatus: "running",
          },
        ],
        slots: { heavyUsed: 1, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
      }
    } else if (path === "/deploy/" && url.searchParams.get("view") === "archived") {
      body = [archivedProject]
    } else if (path === "/deploy/7") {
      body = {
        project,
        running: showcase,
        runtime: options.runtime ?? (showcase ? showcaseRuntime : undefined),
        deployment: {
          ...deployment,
          buildMethod: options.normalized === false ? "legacy_compose" : "recipe",
          activeRun: undefined,
          recentRuns: projectRuns().map(recent),
          ...(showcase
            ? {
                // The fleet's card and the project's own header read one run
                // in flight, so the two cannot say Deploying and Ready.
                activeRun: ENDED.has(runState) ? undefined : liveRun(),
                lastRun: liveRun(),
                health: "healthy",
                serviceCount: 2,
                images: ["ghcr.io/acme/api:2", "postgres:16-alpine"],
              }
            : {}),
        },
      }
    } else if (path === "/deploy/7/operations") {
      body = options.operations ?? (showcase ? showcaseOperations : healthyOperations)
    } else if (path === "/deploy/7/environments/12/releases/20/comparison") {
      body = options.comparison ?? releaseComparison
    } else if (path === "/deploy/7/environments/12/releases") {
      body =
        options.normalized !== false
          ? [
              {
                id: 20,
                projectId: 7,
                environmentId: 12,
                number: 2,
                runId: 84,
                predecessorReleaseId: 19,
                state: "live",
                planRevision: 2,
                sourceRevision: "a12bc34d56ef7890",
                imageDigest: `sha256:${"a".repeat(64)}`,
                configDigest: `sha256:${"b".repeat(64)}`,
                variablesDigest: `sha256:${"c".repeat(64)}`,
                strategy: "blue_green",
                expectedDowntime: false,
                createdAt: now,
                activatedAt: now,
                pinned: false,
                // The showcase's live release is its newest succeeded run's,
                // so the header, the wiring and the version dialog name one
                // commit for it.
                ...(showcase ? { runId: 83, sourceRevision: showcaseRevision(0) } : {}),
              },
              {
                id: 19,
                projectId: 7,
                environmentId: 12,
                number: 1,
                runId: 83,
                state: "retained",
                planRevision: 1,
                sourceRevision: "99887766554433221100",
                imageDigest: `sha256:${"d".repeat(64)}`,
                configDigest: `sha256:${"e".repeat(64)}`,
                variablesDigest: `sha256:${"f".repeat(64)}`,
                strategy: "blue_green",
                expectedDowntime: false,
                createdAt: "2026-09-02T12:00:00Z",
                retiredAt: now,
                pinned: true,
                ...(showcase ? { runId: 82, sourceRevision: showcaseRevision(1) } : {}),
              },
            ]
          : []
    } else if (path === "/deploy/7/runs" && url.searchParams.get("view") === "engine") {
      body = { runs: projectRuns(), running: true }
    } else if (path === "/deploy/7/insights") {
      const days = Number(url.searchParams.get("days") ?? "30")
      body = {
        projectId: 7,
        windowDays: days,
        generatedAt: now,
        runs: days === 7 ? 3 : 12,
        succeeded: days === 7 ? 2 : 9,
        failed: days === 7 ? 1 : 3,
        rolledBack: 1,
        cancelled: 0,
        successRate: days === 7 ? 2 / 3 : 0.75,
        failureStreak: 1,
        medianDurationSeconds: 95,
        p95DurationSeconds: 240,
        deploysPerWeek: days === 7 ? 2 : 2.1,
        meanRecoverySeconds: 5400,
        recoveredFailures: 2,
        lastSuccessAt: "2026-09-02T09:00:00Z",
        lastFailureAt: "2026-09-03T11:00:00Z",
        daily: Array.from({ length: days + 1 }, (_, index) => ({
          date: `2026-08-${String(4 + (index % 27)).padStart(2, "0")}`,
          succeeded: index % 5 === 0 ? 1 : 0,
          failed: index % 9 === 0 ? 1 : 0,
          cancelled: 0,
          medianDurationSeconds: index % 5 === 0 ? 95 : 0,
        })),
        topFailures: [
          { code: "health_gate_failed", count: 2 },
          { code: "build_failed", count: 1 },
        ],
      }
    } else if (path === "/deploy/7/requests") {
      body = deploymentRequests(url)
    } else if (path === "/deploy/7/alerts" && method === "GET") {
      body = { alerts: trafficAlerts, kinds: ["error_rate", "latency", "silence"] }
    } else if (path === "/deploy/7/alerts" && method === "POST") {
      const request = route.request().postDataJSON() as Record<string, unknown>
      trafficAlerts.push({
        id: 100 + trafficAlerts.length,
        projectId: 7,
        environmentId: 12,
        kind: request.kind as string,
        threshold: Number(request.threshold ?? 0),
        windowMinutes: Number(request.windowMinutes ?? 5),
        channels: (request.channels as number[]) ?? [],
        enabled: true,
        state: "ok",
        observed: 0,
        createdAt: now,
        updatedAt: now,
      })
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify(trafficAlerts[trafficAlerts.length - 1]),
      })
      return
    } else if (path.startsWith("/deploy/7/alerts/") && path.endsWith("/test")) {
      body = { delivered: 1, failed: 0, channels: 1 }
    } else if (path.startsWith("/deploy/7/alerts/") && method === "DELETE") {
      const id = Number(path.split("/").pop())
      trafficAlerts = trafficAlerts.filter((alert) => alert.id !== id)
      await route.fulfill({ status: 204 })
      return
    } else if (path === "/deploy/traffic") {
      body = {
        "7": {
          status: "available",
          perMinute: 21.4,
          errorRate: 13 / 1284,
          pages: 402,
          points: Array.from({ length: 60 }, (_, i) => 15 + (i % 7)),
        },
        ...(showcase ? showcaseTraffic : {}),
      }
    } else if (path === "/deploy/7/runs/84/traffic") {
      body = {
        status: "available",
        activationCompletedAt: "2026-09-03T11:30:00Z",
        windowMinutes: 30,
        latency: true,
        before: {
          requests: 600,
          pages: 200,
          perMinute: 20,
          errorRate: 0.002,
          p95: 55,
          from: "2026-09-03T11:00:00Z",
          until: "2026-09-03T11:30:00Z",
        },
        after: {
          requests: 640,
          pages: 210,
          perMinute: 21.3,
          errorRate: 0.02,
          p95: 240,
          from: "2026-09-03T11:30:00Z",
          until: "2026-09-03T12:00:00Z",
        },
      }
    } else if (path === "/deploy/7/lifecycle") {
      body = {
        status: "available",
        watching: true,
        since: minutesAgo(6 * 60),
        events: [
          {
            time: minutesAgo(12),
            type: "container",
            action: "die",
            name: "api-production-r20",
            id: "c0ffee",
            exitCode: "137",
            message: "api-production-r20 exited with status 137",
            level: "error",
            source: "daemon",
            owner: releaseOwner,
          },
          {
            time: minutesAgo(11),
            type: "container",
            action: "start",
            name: "api-production-r20",
            id: "c0ffee",
            message: "api-production-r20 started",
            level: "info",
            source: "docker",
            owner: releaseOwner,
          },
          // Correlated against the audit log: the one distinction an operator
          // wants on an unexplained restart is whether this dashboard did it.
          {
            time: minutesAgo(13),
            type: "container",
            action: "create",
            name: "api-production-r20",
            id: "c0ffee",
            message: "api-production-r20 was created",
            level: "info",
            source: "dashboard",
            owner: releaseOwner,
            trigger: {
              auditId: 918,
              action: "deploy.run",
              actor: "wayy",
              confidence: "likely",
            },
          },
          {
            time: minutesAgo(40),
            type: "network",
            action: "destroy",
            name: "jd-db-e12",
            message: "deleted network jd-db-e12",
            level: "notice",
            source: "docker",
            owner: { "environment-id": "12" },
          },
        ],
      }
    } else if (path === "/deploy/7/runs/84" && method === "GET") {
      body = { run: liveRun(), steps: showcase ? showcaseSteps : steps }
    } else if (path === "/deploy/7/environments/12/configuration" && method === "GET") {
      body = configurationBody()
    } else if (path === "/deploy/7/environments/12/git-watch" && method === "GET") {
      body = {
        automatic: gitPolicy.automatic,
        branch: "main",
        status: gitPolicy.automatic ? "watching" : "manual_only",
        intervalSeconds: 5,
        policy: gitPolicy,
      }
    } else if (path === "/deploy/7/environments/12/git-policy" && method === "PUT") {
      const input = request.postDataJSON() as typeof gitPolicy
      gitPolicy = { ...input, revision: input.revision + 1 }
      body = gitPolicy
    } else if (path === "/deploy/7/environments/12/database-links" && method === "GET") {
      body = showcase ? showcaseLinks : []
    } else if (path === "/deploy/7/environments/12/triggers" && method === "GET") {
      body = automationTriggers
    } else if (path === "/deploy/7/environments/12/triggers" && method === "POST") {
      const input = request.postDataJSON() as Record<string, unknown>
      const trigger = {
        id: 31,
        projectId: 7,
        environmentId: 12,
        hookId: "provider-hook",
        lastStatus: "",
        ...input,
      }
      automationTriggers = [trigger]
      body = { trigger, secret: "one-time-provider-secret" }
    } else if (path === "/deploy/7/environments/12/schedules" && method === "GET") {
      body = automationSchedules
    } else if (path === "/deploy/7/environments/12/schedules" && method === "POST") {
      const input = request.postDataJSON() as Record<string, unknown>
      const schedule = {
        id: 41,
        projectId: 7,
        environmentId: 12,
        nextRunAt: "2026-09-08T03:00:00Z",
        nextRuns: [8, 9, 10, 11, 12].map(
          (day) => `2026-09-${String(day).padStart(2, "0")}T03:00:00Z`,
        ),
        ...input,
      }
      automationSchedules = [schedule]
      body = schedule
    } else if (path === "/deploy/7/previews/approvals") {
      body = showcase ? showcaseApprovals : []
    } else if (path === "/deploy/7/previews") {
      body = [
        {
          id: 51,
          triggerId: 31,
          providerRef: "42",
          environmentId: 52,
          environmentSlug: "pr-42",
          state: "open",
          updatedAt: now,
        },
      ]
    } else if (path === "/deploy/notifications" && method === "GET") {
      body = notificationChannels
    } else if (path === "/deploy/notifications" && method === "POST") {
      const input = request.postDataJSON() as Record<string, unknown>
      const config = (input.config ?? {}) as Record<string, unknown>
      const target =
        input.kind === "discord"
          ? "https://discord.com/api/webhooks/123456/••••"
          : input.kind === "telegram"
            ? `Telegram chat ${config.chatId}`
            : input.url
      const channel = {
        id: 61,
        target,
        createdAt: now,
        updatedAt: now,
        ...input,
        config: undefined,
        url: input.kind === "webhook" ? input.url : target,
      }
      notificationChannels = [channel]
      body = { channel, secret: input.kind === "webhook" ? "one-time-notification-secret" : "" }
    } else if (path === "/deploy/notifications/61/enabled" && method === "PUT") {
      const input = request.postDataJSON() as { enabled: boolean }
      notificationChannels = notificationChannels.map((channel) => ({ ...channel, ...input }))
      body = notificationChannels[0]
    } else if (path === "/deploy/notifications/61/test" && method === "POST") {
      notificationTests += 1
      body = { delivered: true }
    } else if (path === "/deploy/notifications/61/deliveries" && method === "GET") {
      body = showcase
        ? showcaseDeliveries(61)
        : [
            {
              id: 1,
              channelId: 61,
              runId: 84,
              projectId: 7,
              projectName: "api-production",
              runNumber: 1,
              event: "run.failed",
              attempt: 1,
              status: "delivered",
              responseClass: "2xx",
              createdAt: now,
              completedAt: now,
            },
          ]
    } else if (path === "/deploy/notifications/61" && method === "DELETE") {
      notificationChannels = []
      await route.fulfill({ status: 204 })
      return
    } else if (path === "/deploy/7/environments/12/configuration" && method === "PUT") {
      const requestBody = request.postDataJSON() as typeof normalizedConfiguration
      normalizedConfiguration = {
        build: requestBody.build,
        runtime: requestBody.runtime,
        dependencies: requestBody.dependencies,
        checks: requestBody.checks,
        // The server seals a domain password on the way in and only ever
        // reads back its hash; the mock does the same so the page is
        // exercised against what it really receives.
        domains: (requestBody.domains as Record<string, unknown>[]).map((domain) => {
          const protection = domain.protection as
            { username?: string; password?: string; hash?: string } | undefined
          return protection?.password
            ? {
                ...domain,
                protection: {
                  username: protection.username,
                  hash: "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy",
                },
              }
            : domain
        }) as typeof normalizedConfiguration.domains,
      }
      configurationRevision += 1
      configurationPending = true
      body = configurationBody()
    } else if (/^\/deploy\/7\/environments\/12\/variables\/[^/]+\/reveal$/.test(path)) {
      body = { name: path.split("/").at(-2), value: "revealed-browser-secret" }
    } else if (/^\/deploy\/7\/environments\/12\/variables\/[^/]+\/rotate$/.test(path)) {
      configurationRevision += 1
      configurationPending = true
      body = { generatedValue: "rotated-browser-secret", desiredRevision: configurationRevision }
    } else if (/^\/deploy\/7\/environments\/12\/variables\/[^/]+\/generate$/.test(path)) {
      const variableName = decodeURIComponent(path.split("/").at(-2) ?? "")
      configurationRevision += 1
      configurationPending = true
      scopedVariables = [
        ...scopedVariables.filter((variable) => variable.name !== variableName),
        {
          name: variableName,
          revision: 1,
          sensitivity: "secret",
          scopes: ["runtime"],
          masked: "••••••••",
          valueDigest: `sha256:${"2".repeat(64)}`,
          pending: true,
          createdBy: "operator",
          createdAt: now,
          environmentId: 12,
          desiredRevision: configurationRevision,
        },
      ]
      body = { generatedValue: "generated-browser-secret", desiredRevision: configurationRevision }
    } else if (/^\/deploy\/7\/environments\/12\/variables\/[^/]+$/.test(path) && method === "PUT") {
      const variableName = decodeURIComponent(path.split("/").at(-1) ?? "")
      const requestBody = request.postDataJSON() as {
        sensitivity: "plain" | "secret"
        scopes: DeploymentVariable["scopes"]
      }
      configurationRevision += 1
      configurationPending = true
      scopedVariables = [
        ...scopedVariables.filter((variable) => variable.name !== variableName),
        {
          name: variableName,
          revision: 1,
          sensitivity: requestBody.sensitivity,
          scopes: requestBody.scopes,
          masked: requestBody.sensitivity === "secret" ? "••••••••" : "configured",
          valueDigest: `sha256:${"3".repeat(64)}`,
          pending: true,
          createdBy: "operator",
          createdAt: now,
          environmentId: 12,
          desiredRevision: configurationRevision,
        },
      ]
      body = { variable: scopedVariables.at(-1), desiredRevision: configurationRevision }
    } else if (path === "/deploy/7/removal-plan" && method === "POST") {
      body = {
        deploymentId: 7,
        archived: false,
        targets: [
          {
            id: "docker_volume:api-data",
            kind: "docker_volume",
            resourceId: "api-data",
            displayName: "api-data",
            owner: "docker",
            ownership: "managed",
            data: true,
            requiresAdmin: true,
            confirmationType: "typed",
            confirmationPhrase: "api-data",
          },
        ],
        digest: `sha256:${"9".repeat(64)}`,
        generatedAt: now,
      }
    } else if (path === "/deploy/7/runs/84/cancel" && method === "POST") {
      runState = "cancelling"
      body = liveRun()
    } else if (path === "/deploy/7/runs/84/retry" && method === "POST") {
      body = { ...liveRun(), id: 85, retryOfRunId: 84, state: "queued" }
    } else if (path === "/deploy/7/env") {
      body = [{ key: "API_TOKEN", masked: "••••••••", updatedAt: now }]
    } else if (path === "/deploy/7/commits") {
      body = [
        {
          sha: "a12bc34d56ef7890",
          short: "a12bc34",
          author: "Alex",
          date: now,
          subject: "Current release",
        },
        {
          sha: "99887766554433221100",
          short: "9988776",
          author: "Alex",
          date: "2026-09-02T12:00:00Z",
          subject: "Known-good release",
        },
      ]
    } else if (path === "/deploy/7/rollback" && method === "POST") {
      body = { started: true, runId: 86, state: "queued" }
    } else if (path === "/deploy/7/environments/12/runs" && method === "POST") {
      const operation = (request.postDataJSON() as { operation: string }).operation
      actions.push(operation)
      if (operation === "deploy" || operation === "force_build") configurationPending = false
      body = { ...run, id: operation === "restart" ? 87 : 88, operation, state: "queued" }
    } else if (path === "/deploy/7/environments/12/rollback" && method === "POST") {
      actions.push("rollback")
      body = { ...run, id: 90, operation: "rollback", state: "queued" }
    } else if (showcase && path === "/deploy/7/preview-frame") {
      await route.fulfill({ status: 200, contentType: "text/html", body: SHOWCASE_SITE })
      return
    } else if (path === "/deploy/drafts" && method === "POST") {
      body = draft
    } else if (path === "/deploy/drafts/browser-draft") body = draft
    else {
      body = showcase && method === "GET" ? showcaseRead(path, url) : undefined
      if (body === undefined) {
        await route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
        })
        return
      }
    }
    await json(route, body)
  })
  // The showcase's containers answer their stats socket once, so the usage
  // tiles have a live figure; one frame and no interval, so a page still
  // settles. A spec that routes the same socket afterwards wins.
  if (showcase)
    await page.routeWebSocket(/\/api\/v1\/docker\/containers\/[^/]+\/stats\/stream/, (socket) => {
      // Handling the page's close is what keeps the mocked socket open after
      // its frame; without a handler the tiles never read as live.
      socket.onClose(() => {})
      const database = socket.url().includes(DB_CONTAINER)
      socket.send(
        JSON.stringify({
          type: "stats",
          ts: clock ?? Date.now(),
          data: database
            ? containerStats(DB_CONTAINER, "api-production-postgres", 3.1, 96_000_000)
            : containerStats(API_CONTAINER, "api-production-r20", 12.4, 210_000_000),
        }),
      )
    })
  return {
    setRunState(state: string) {
      runState = state
    },
    mutationCount() {
      return mutationCount
    },
    actions() {
      return [...actions]
    },
    notificationTests() {
      return notificationTests
    },
    gitPolicy() {
      return gitPolicy
    },
    configuration() {
      return normalizedConfiguration
    },
  }
}

export async function mockDraftJourney(page: Page) {
  let revision = 1
  let currentStep = "intent"
  let data: Record<string, unknown> = {}
  let commits = 0
  let started = 0
  let commitBody: Record<string, unknown> | undefined
  const discarded: string[] = []
  let environment: Record<string, string> = {}
  let staged: Record<string, unknown> | undefined
  const currentDraft = () => ({
    id: "journey-draft",
    ownerUsername: "operator",
    currentStep,
    revision,
    environmentKeys: Object.keys(environment).sort(),
    data,
    findings: [],
    planPreview: revision > 4 ? "source -> build -> verify -> route" : "",
    updatedAt: now,
    expiresAt: "2026-09-04T12:00:00Z",
  })

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (path === "/auth/session") return json(route, user)
    if (path === "/dashboard/update") return json(route, { current: "0.6.7", latest: "0.6.7" })
    // The import picker asks both GitHub identities who they can reach. Left
    // unmocked these fell through to the 503 below, so every run of this
    // fixture drew "Not mocked" across the top of the repository list — the
    // page's own report of an optional integration being unreachable.
    if (path === "/deploy/github-app/") return json(route, { configured: false, installations: [] })
    if (path === "/deploy/github-app/repositories") return json(route, [])
    if (path === "/deploy/blueprints/") return json(route, blueprintCatalogue)
    if (path === "/deploy/blueprints/minecraft-java") return json(route, minecraftBlueprint)
    if (path === "/deploy/blueprints/minecraft-java/versions") return json(route, minecraftVersions)
    if (path === "/deploy/blueprints/postgresql") return json(route, postgresBlueprint)
    if (path === "/deploy/blueprints/vaultwarden") return json(route, vaultwardenBlueprint)
    if (path === "/deploy/blueprints/uptime-kuma") {
      return json(route, {
        ...blueprintCatalogue[1],
        image: { reference: "louislam/uptime-kuma:1.23.16", tagPolicy: "pinned" },
        provenance: postgresBlueprint.provenance,
        resources: { memoryMb: 512, minMemoryMb: 256 },
        update: { detector: "registry", notes: "" },
        inputs: [],
      })
    }
    if (path === "/deploy/drafts" && method === "POST") {
      started += 1
      return json(route, currentDraft())
    }
    if (path.startsWith("/deploy/drafts/") && method === "DELETE") {
      discarded.push(path.slice("/deploy/drafts/".length))
      return route.fulfill({ status: 204, body: "" })
    }
    if (path === "/deploy/drafts/journey-draft" && method === "GET") {
      return json(route, currentDraft())
    }
    if (path === "/deploy/drafts/journey-draft" && method === "PUT") {
      const body = request.postDataJSON() as {
        step: string
        intent?: unknown
        source?: unknown
        configuration?: unknown
        dotenv?: string
        retainEnvironmentKeys?: string[]
      }
      revision += 1
      currentStep = body.step
      if (body.intent) data = { ...data, intent: body.intent }
      if (body.source) data = { ...data, source: body.source }
      if (body.configuration) data = { ...data, configuration: structuredClone(body.configuration) }
      if (body.dotenv !== undefined) {
        environment = Object.fromEntries(
          (body.retainEnvironmentKeys ?? [])
            .filter((key) => key in environment)
            .map((key) => [key, environment[key]]),
        )
        for (const line of body.dotenv.split("\n")) {
          const match = line.match(/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$/)
          if (match) environment[match[1]] = match[2]
        }
        staged = { dotenv: body.dotenv, retainEnvironmentKeys: body.retainEnvironmentKeys }
      }
      if (body.configuration) {
        const configuration = data.configuration as {
          variables: Array<Record<string, unknown>>
          domains: Array<{ protection?: { username: string; password?: string; hash?: string } }>
        }
        for (const domain of configuration.domains) {
          if (domain.protection?.password) {
            domain.protection.hash = "fixture-sealed-password"
            delete domain.protection.password
          }
        }
        for (const name of Object.keys(environment)) {
          const variable = configuration.variables.find((entry) => entry.name === name)
          if (!variable)
            configuration.variables.push({
              name,
              sensitivity: "secret",
              scopes: ["runtime", "build"],
            })
        }
      }
      return json(route, currentDraft())
    }
    if (path === "/deploy/drafts/journey-draft/detect" && method === "POST") {
      const intent = data.intent as { profile: string }
      const source = data.source as {
        kind: string
        url?: string
        image?: string
        blueprintId?: string
        blueprintInputs?: Record<string, string>
      }
      if (source.kind === "blueprint" && source.blueprintId === "vaultwarden") {
        revision += 1
        currentStep = "detection"
        const hostname = source.blueprintInputs?.domain ?? ""
        data = {
          ...data,
          // `Render` puts a `domain` input in the plan's own domains, so the
          // route the release publishes is the name the application was told.
          // The rest is the shape every reviewed template renders to — a
          // managed data volume, a generated secret, a readiness check — which
          // is what Review reads back.
          configuration: {
            ...structuredClone(postgresRenderedConfiguration),
            runtime: {
              ...structuredClone(postgresRenderedConfiguration.runtime),
              image: "vaultwarden/server:1.32.7-alpine",
              internalPort: 80,
              strategy: "stop_first",
              mounts: [
                {
                  source: "vaultwarden-0123456789abcdef-data",
                  target: "/data",
                  ownership: "managed",
                },
              ],
            },
            variables: [
              {
                name: "DOMAIN",
                sensitivity: "plain",
                scopes: ["runtime"],
                value: `https://${hostname}`,
              },
              {
                name: "SIGNUPS_ALLOWED",
                sensitivity: "plain",
                scopes: ["runtime"],
                value: "false",
              },
              {
                name: "ADMIN_TOKEN",
                sensitivity: "secret",
                scopes: ["runtime"],
                required: true,
                generate: 48,
              },
            ],
            dependencies: [
              {
                kind: "storage",
                ownership: "managed",
                resourceKind: "docker_volume",
                resourceId: "vaultwarden-0123456789abcdef-data",
                config: { purpose: "Everything this vault holds", data: true, backup: true },
              },
            ],
            checks: [
              {
                name: "Vault answers",
                kind: "http",
                phase: "readiness",
                required: true,
                config: { path: "/alive", attempts: 30, intervalSeconds: 2, timeoutSeconds: 10 },
              },
            ],
            domains: [{ hostname, https: true, ownership: "managed" }],
          },
          detection: {
            source: {
              kind: "blueprint",
              repository: "docker.io/vaultwarden/server:1.32.7-alpine",
              ref: "vaultwarden@1.0.0",
              revision: `sha256:${"e".repeat(64)}`,
              digest: `sha256:${"f".repeat(64)}`,
              platforms: ["linux/amd64"],
            },
            candidates: [
              {
                id: "vaultwarden",
                name: "Vaultwarden",
                root: "",
                profile: "web",
                buildMethod: "image",
                confidence: "high",
                port: 80,
                evidence: [
                  {
                    path: "vaultwarden@1.0.0",
                    reason: "reviewed blueprint shipped with this dashboard",
                  },
                ],
                needsDecision: [],
              },
            ],
            selectedId: "vaultwarden",
            scannedFiles: 0,
            scannedBytes: 0,
            truncated: false,
            gitRequirements: { submodules: false, lfs: false },
          },
        }
        return json(route, currentDraft())
      }
      if (source.kind === "blueprint" && source.blueprintId === "postgresql") {
        revision += 1
        currentStep = "detection"
        const candidate = {
          id: "postgresql",
          name: "PostgreSQL",
          root: "",
          profile: "service",
          buildMethod: "image",
          confidence: "high",
          port: 5432,
          evidence: [
            { path: "postgresql@1.0.0", reason: "reviewed blueprint shipped with this dashboard" },
            {
              path: "docker.io/library/postgres:16-alpine",
              reason: `registry digest sha256:${"c".repeat(64)}`,
            },
          ],
          needsDecision: [],
        }
        const rendered = structuredClone(postgresRenderedConfiguration)
        rendered.variables[0].value = source.blueprintInputs?.database ?? "app"
        data = {
          ...data,
          configuration: rendered,
          detection: {
            source: {
              kind: "blueprint",
              repository: "docker.io/library/postgres:16-alpine",
              ref: "postgresql@1.0.0",
              revision: `sha256:${"d".repeat(64)}`,
              digest: `sha256:${"c".repeat(64)}`,
              platforms: ["linux/amd64"],
            },
            candidates: [candidate],
            selectedId: candidate.id,
            scannedFiles: 0,
            scannedBytes: 0,
            truncated: false,
            gitRequirements: { submodules: false, lfs: false },
          },
        }
        return json(route, currentDraft())
      }
      if (source.kind === "image" && intent.profile !== "game") {
        revision += 1
        currentStep = "detection"
        const candidate = {
          id: "image-candidate",
          name: source.image,
          root: "",
          profile: "image",
          buildMethod: "image",
          confidence: "high",
          port: 8080,
          evidence: [
            { path: source.image, reason: `registry digest sha256:${"a".repeat(64)}` },
            { path: source.image, reason: "image exposes 8080/tcp" },
          ],
          // The exposure answered the only question an image is asked, which is
          // what detection now reports — so this lands on Review, not step one.
          needsDecision: [],
        }
        data = {
          ...data,
          detection: {
            source: {
              kind: "image",
              repository: source.image,
              digest: `sha256:${"a".repeat(64)}`,
              platforms: ["linux/amd64"],
            },
            candidates: [candidate],
            selectedId: candidate.id,
            scannedFiles: 0,
            scannedBytes: 0,
            truncated: false,
            gitRequirements: { submodules: false, lfs: false },
          },
        }
        return json(route, currentDraft())
      }
      const image = intent.profile === "game"
      const candidate = {
        id: `${intent.profile}-candidate`,
        name: image ? "Minecraft Java image" : "Next.js web application",
        root: "",
        profile: intent.profile,
        buildMethod: image ? "image" : "recipe",
        recipe: image ? undefined : "node",
        confidence: "high",
        framework: image ? undefined : "Next.js",
        buildCommand: image ? undefined : "bun run build",
        startCommand: image ? undefined : "bun start",
        port: image ? 25565 : 3000,
        evidence: [
          {
            path: image ? source.image : "package.json",
            reason: image ? "registry digest resolved" : "contains next dependency",
          },
        ],
        needsDecision: [],
      }
      revision += 1
      currentStep = "detection"
      data = {
        ...data,
        detection: {
          source: image
            ? {
                kind: "image",
                repository: source.image,
                digest: `sha256:${"a".repeat(64)}`,
                platforms: ["linux/amd64"],
              }
            : {
                kind: "git",
                remote: source.url,
                ref: "main",
                revision: "a12bc34d56ef7890a12bc34d56ef7890a12bc34d",
              },
          candidates: [candidate],
          selectedId: candidate.id,
          scannedFiles: image ? 0 : 12,
          scannedBytes: image ? 0 : 4096,
          truncated: false,
          gitRequirements: { submodules: false, lfs: false },
        },
      }
      return json(route, currentDraft())
    }
    if (path === "/deploy/drafts/journey-draft/preflight" && method === "POST") {
      const findings = [
        { code: "source.ok", severity: "pass", title: "Source identity resolved", owner: "source" },
        { code: "build.ok", severity: "pass", title: "Build plan is valid", owner: "build" },
        { code: "runtime.ok", severity: "pass", title: "Runtime plan is valid", owner: "runtime" },
        { code: "checks.ok", severity: "pass", title: "Readiness is configured", owner: "checks" },
      ]
      revision += 1
      currentStep = "preflight"
      const saved = {
        ...currentDraft(),
        findings,
        planPreview: "source -> build -> verify -> route",
      }
      return json(route, {
        draft: saved,
        preflight: {
          revision,
          findings,
          preview: saved.planPreview,
          digest: `sha256:${"b".repeat(64)}`,
          plan: { actions: [] },
        },
      })
    }
    if (path === "/deploy/drafts/journey-draft/commit" && method === "POST") {
      commits += 1
      commitBody = request.postDataJSON() as Record<string, unknown>
      return json(route, {
        projectId: 77,
        environmentId: 78,
        planRevision: revision,
        created: true,
      })
    }
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })
  return {
    commits: () => commits,
    commitBody: () => commitBody,
    discarded: () => discarded,
    /** Drafts the server was asked to create — one per press that reached it. */
    started: () => started,
    source: () => data.source as Record<string, unknown> | undefined,
    intent: () => data.intent as Record<string, unknown> | undefined,
    staged: () => staged,
    environmentKeys: () => Object.keys(environment).sort(),
    configuration: () => data.configuration as Record<string, unknown> | undefined,
  }
}

export async function mockNewProject(page: Page) {
  const journey = await mockDraftJourney(page)
  let runs = 0
  await page.route("**/api/v1/git/github**", (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/git/github/repos")
      return json(route, [
        {
          nameWithOwner: "Wayy01/wesmokefish",
          name: "wesmokefish",
          owner: "Wayy01",
          description: "A Next.js site",
          url: "https://github.com/Wayy01/wesmokefish",
          cloneUrl: "https://github.com/Wayy01/wesmokefish.git",
          defaultBranch: "main",
          language: "TypeScript",
          private: false,
          fork: false,
          archived: false,
        },
      ])
    if (path === "/git/github/branches")
      return json(route, [{ name: "main", default: true }, { name: "preview" }])
    return json(route, {
      available: true,
      account: { loggedIn: true, login: "Wayy01", gitConfigured: true },
    })
  })
  await page.route("**/api/v1/deploy/hostname**", (route) =>
    json(route, {
      hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
      base: "203-0-113-7.sslip.io",
      covered: false,
      certificateMethod: "nginx",
      method: "sslip",
      address: "203.0.113.7",
      detail:
        "Resolves to 203.0.113.7 with no DNS record to create. No certificate covers it yet; one can be issued for it from here.",
    }),
  )
  await page.route("**/api/v1/deploy/77/environments/78/runs", (route) => {
    runs += 1
    return json(route, { id: 84, state: "queued" })
  })
  return { ...journey, runs: () => runs }
}

/**
 * A deployment's request record, as the ingress would have written it.
 *
 * Two families and one slow route, because the page's job is to make a 5xx and
 * a tail latency findable — a fixture of two hundred identical 200s would pass
 * every assertion while proving none of that.
 */
function deploymentRequests(url: URL) {
  const limit = Number(url.searchParams.get("limit") ?? "500")
  const base = Date.parse("2026-09-03T11:40:00Z")
  const entries = [
    {
      seq: 9917,
      time: "2026-09-03T11:59:31Z",
      method: "POST",
      path: "/api/checkout",
      status: 500,
      size: 412,
      remoteIp: "198.51.100.23",
      host: "api.example.com",
      proto: "HTTP/2.0",
      userAgent: "Mozilla/5.0 Chrome/140.0",
      durationMs: 2840,
      tls: true,
    },
    {
      seq: 9916,
      time: "2026-09-03T11:59:12Z",
      method: "GET",
      path: "/api/items",
      query: "page=2",
      status: 200,
      size: 18400,
      remoteIp: "198.51.100.7",
      host: "api.example.com",
      proto: "HTTP/2.0",
      userAgent: "curl/8.5.0",
      durationMs: 24,
      tls: true,
    },
    {
      seq: 9915,
      time: "2026-09-03T11:58:40Z",
      method: "GET",
      path: "/healthz",
      status: 200,
      size: 2,
      remoteIp: "127.0.0.1",
      host: "api.example.com",
      proto: "HTTP/1.1",
      durationMs: 1,
    },
    {
      seq: 9914,
      time: "2026-09-03T11:57:02Z",
      method: "GET",
      path: "/admin",
      status: 404,
      size: 120,
      remoteIp: "203.0.113.55",
      host: "api.example.com",
      proto: "HTTP/1.1",
      userAgent: "Mozilla/5.0 (compatible; Googlebot/2.1)",
      durationMs: 6,
    },
  ]
  return {
    status: "available",
    driver: "docker-caddy",
    format: "caddy-json",
    latency: true,
    complete: true,
    observedAt: now,
    entries: entries.slice(0, Math.max(limit, 1)),
    slowest: [entries[0]],
    coverage: {
      exists: true,
      from: "2026-09-01T00:00:00Z",
      to: "2026-09-03T11:59:31Z",
      held: 9917,
      complete: true,
      cursor: 9917,
      refreshedAt: now,
    },
    summary: {
      total: 1284,
      scanned: 1310,
      classes: { "2xx": 1201, "3xx": 14, "4xx": 56, "5xx": 13 },
      errorRate: 13 / 1284,
      clientErrorRate: 56 / 1284,
      bytes: 41_284_112,
      perMinute: 21.4,
      pages: 402,
      latency: { p50: 24, p75: 61, p90: 190, p95: 412, p99: 2840, max: 3120, mean: 78 },
      methods: [
        { value: "GET", count: 1180, errors: 4 },
        { value: "POST", count: 92, errors: 9 },
        { value: "HEAD", count: 12, errors: 0 },
      ],
      statuses: [
        { value: "200", count: 1201, errors: 0 },
        { value: "404", count: 56, errors: 0 },
        { value: "500", count: 13, errors: 13 },
      ],
      paths: [
        { value: "/healthz", count: 720, errors: 0, p95: 1, bytes: 1_440 },
        { value: "/api/items", count: 402, errors: 0, p95: 61, bytes: 7_396_800 },
        { value: "/api/checkout", count: 92, errors: 13, p95: 2840, bytes: 37_904 },
      ],
      hosts: [{ value: "api.example.com", count: 1284, errors: 13 }],
      clients: [
        { value: "127.0.0.1", count: 720, errors: 0 },
        { value: "198.51.100.7", count: 402, errors: 9 },
        { value: "203.0.113.55", count: 40, errors: 0, refused: 40, probes: 12 },
        // 172.217 is Google and 172.16 is RFC 1918. They differ by one octet
        // and only one of them is worth offering a firewall rule for.
        { value: "172.217.0.1", count: 31, errors: 0 },
        { value: "172.16.4.9", count: 12, errors: 0 },
      ],
      agents: [
        { value: "Chrome", count: 402, errors: 9 },
        { value: "Googlebot", count: 56, errors: 0 },
      ],
      referers: [{ value: "www.google.com", count: 88, errors: 0 }],
      probes: [
        { value: "/.env", count: 5, errors: 0, refused: 5, probes: 5 },
        { value: "/wp-login.php", count: 4, errors: 0, refused: 4, probes: 4 },
        { value: "/xmlrpc.php", count: 3, errors: 0, refused: 3, probes: 3 },
      ],
      scanners: [{ value: "203.0.113.55", count: 40, errors: 0, refused: 40, probes: 12 }],
      buckets: Array.from({ length: 20 }, (_, index) => ({
        start: new Date(base + index * 60_000).toISOString(),
        total: 60 + index,
        counts: { "2xx": 56 + index, "4xx": 3, "5xx": index === 19 ? 1 : 0 },
        p95: 40 + index * 12,
        bytes: (60 + index) * 32_000 + (index % 4) * 180_000,
      })),
      bucketSeconds: 60,
      first: "2026-09-03T11:40:00Z",
      last: "2026-09-03T11:59:31Z",
      truncated: false,
    },
  }
}

/**
 * The four configure screens of `/deploy/new`, in the order the spine draws
 * them, and how a test reaches one.
 *
 * A source whose detection answered everything opens on Review — that is the
 * point of the split, and it is what keeps the two-press import — so a test
 * that wants an earlier answer says which screen holds it rather than
 * assuming the whole form is on one.
 */
export const CONFIGURE_STEPS = ["project", "runtime", "variables", "review"] as const

export type ConfigureStep = (typeof CONFIGURE_STEPS)[number]

/** Each screen's question, which is its `h1` — and how a test knows where it is. */
export const STEP_QUESTION: Record<ConfigureStep, string> = {
  project: "What are you building?",
  runtime: "How should it run?",
  variables: "What does it need to run?",
  review: "Ready to deploy?",
}

export async function currentStep(page: Page): Promise<ConfigureStep> {
  const heading = page.getByRole("heading", { level: 1 })
  await expect(heading).toHaveText(
    new RegExp(
      `^(${CONFIGURE_STEPS.map((key) => STEP_QUESTION[key].replace("?", "\\?")).join("|")})$`,
    ),
  )
  const text = ((await heading.textContent()) ?? "").trim()
  const at = CONFIGURE_STEPS.find((key) => STEP_QUESTION[key] === text)
  if (!at) throw new Error(`not on a configure step: ${text}`)
  return at
}

/** Walks the sequence to one screen, forwards or back. */
export async function gotoStep(page: Page, want: ConfigureStep) {
  for (let guard = 0; guard < CONFIGURE_STEPS.length; guard++) {
    const at = await currentStep(page)
    if (at === want) return
    const forward = CONFIGURE_STEPS.indexOf(want) > CONFIGURE_STEPS.indexOf(at)
    const next = CONFIGURE_STEPS[CONFIGURE_STEPS.indexOf(at) + (forward ? 1 : -1)]
    await page.getByRole("button", { name: forward ? "Continue" : "Back", exact: true }).click()
    await expect(page.getByRole("heading", { level: 1, name: STEP_QUESTION[next] })).toBeVisible()
  }
  throw new Error(`could not reach the ${want} step`)
}
