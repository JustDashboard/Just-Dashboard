import { expect } from "@playwright/test"
import type { Page, Route } from "@playwright/test"
import type {
  DeploymentOperations,
  DeploymentRuntimeServices,
  ReleaseComparisonResponse,
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
  evidence: {},
  lastSeq: Number(ordinal),
}))

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
  } = {},
) {
  let runState = run.state
  let mutationCount = 0
  const actions: string[] = []
  let configurationRevision = 3
  let configurationPending = true
  let automationTriggers: Record<string, unknown>[] = []
  let gitPolicy = {
    automatic: true,
    commitStatuses: true,
    revision: 0,
    watchInclude: [] as string[],
    watchExclude: [] as string[],
  }
  let automationSchedules: Record<string, unknown>[] = []
  let notificationChannels: Record<string, unknown>[] = []
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
      stateSince: new Date(Date.now() - 12 * 60_000).toISOString(),
      observed: 4.2,
      checkedAt: now,
      firedAt: new Date(Date.now() - 12 * 60_000).toISOString(),
      createdAt: now,
      updatedAt: now,
    },
  ]
  let notificationTests = 0
  let scopedVariables = [
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
  let normalizedConfiguration = {
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
      pending: configurationPending,
      desiredRevision: configurationRevision,
    })),
    pending: {
      pending: configurationPending,
      desiredRevision: configurationRevision,
      liveReleaseId: 20,
      livePlanRevision: configurationPending ? 2 : configurationRevision,
      changes: configurationPending
        ? [{ kind: "variable", name: "API_TOKEN", change: "changed" }]
        : [],
    },
  })
  const liveRun = () => ({ ...run, state: runState })
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
        deployments: [{ ...deployment, lastRun: liveRun(), activeRun: liveRun() }],
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
    } else if (path === "/deploy/7") {
      body = {
        project,
        running: false,
        runtime: options.runtime,
        deployment: {
          ...deployment,
          buildMethod: options.normalized === false ? "legacy_compose" : "recipe",
          activeRun: undefined,
        },
      }
    } else if (path === "/deploy/7/operations") {
      body = options.operations ?? healthyOperations
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
              },
            ]
          : []
    } else if (path === "/deploy/7/runs" && url.searchParams.get("view") === "engine") {
      body = { runs: [liveRun()], running: true }
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
        since: new Date(Date.now() - 6 * 3_600_000).toISOString(),
        events: [
          {
            time: new Date(Date.now() - 12 * 60_000).toISOString(),
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
            time: new Date(Date.now() - 11 * 60_000).toISOString(),
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
            time: new Date(Date.now() - 13 * 60_000).toISOString(),
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
            time: new Date(Date.now() - 40 * 60_000).toISOString(),
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
      body = { run: liveRun(), steps }
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
      body = []
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
        ...input,
      }
      automationSchedules = [schedule]
      body = schedule
    } else if (path === "/deploy/7/previews/approvals") {
      body = []
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
      body = [
        {
          id: 1,
          channelId: 61,
          runId: 84,
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
        scopes: string[]
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
    } else if (path === "/deploy/drafts" && method === "POST") {
      body = draft
    } else if (path === "/deploy/drafts/browser-draft") body = draft
    else {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
      })
      return
    }
    await json(route, body)
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
        { value: "/healthz", count: 720, errors: 0, p95: 1 },
        { value: "/api/items", count: 402, errors: 0, p95: 61 },
        { value: "/api/checkout", count: 92, errors: 13, p95: 2840 },
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
