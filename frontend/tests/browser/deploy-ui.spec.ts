import { expect, test, type Page, type Route } from "@playwright/test"
import type {
  DeploymentOperations,
  DeploymentRuntimeServices,
  ReleaseComparisonResponse,
} from "../../src/lib/types"

const now = "2026-09-03T12:00:00Z"

for (const failImport of [false, true]) {
  test(`quick setup hands edited settings and environment to the wizard (${failImport ? "recovery" : "success"})`, async ({
    page,
  }) => {
    const quick = await mockQuickDeploy(page)
    if (failImport) {
      await page.route("**/api/v1/deploy/77/environments/78/variables/import", (route) =>
        route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "unavailable", message: "Environment import unavailable" },
          }),
        }),
      )
    }
    await page.goto("/deploy/new")
    await page.getByText("From GitHub", { exact: true }).click()
    await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
    await page.getByRole("button", { name: "Continue", exact: true }).click()
    await page.getByRole("textbox", { name: "Name", exact: true }).fill("edited-site")
    await page.getByText("Import .env", { exact: true }).click()
    await page
      .getByRole("textbox", { name: "Environment variables", exact: true })
      .fill("API_TOKEN=handoff-secret")
    await page.getByRole("button", { name: "Open in full wizard" }).click()
    await expect(page).toHaveURL(/draft=journey-draft.*step=configuration/)
    await expect(page.getByRole("textbox", { name: "Environment variables" })).toHaveValue(
      "API_TOKEN=handoff-secret",
    )
    await expect(page.getByRole("heading", { name: "edited-site", exact: true })).toBeVisible()
    expect(page.url()).not.toContain("handoff-secret")
    expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
      "handoff-secret",
    )
    await page.getByRole("button", { name: "Run preflight" }).click()
    await page.getByRole("button", { name: "Save deployment", exact: true }).click()
    if (failImport) {
      await expect(page.getByText("Environment import unavailable", { exact: true })).toBeVisible()
      await expect(page.getByRole("link", { name: "Open saved deployment" })).toHaveAttribute(
        "href",
        "/deploy/77?tab=variables",
      )
      await expect(page.getByRole("button", { name: "Save deployment", exact: true })).toHaveCount(
        0,
      )
      await expect(page.getByRole("textbox", { name: "Environment variables" })).toHaveValue(
        "API_TOKEN=handoff-secret",
      )
    } else {
      await expect(page).toHaveURL(/\/deploy\/77$/)
      expect(quick.imported()).toMatchObject({
        dotenv: "API_TOKEN=handoff-secret",
        sensitivity: "secret",
        scopes: ["runtime", "build"],
      })
    }
    expect(quick.commits()).toBe(1)
  })
}

for (const stage of ["variables/import", "runs"]) {
  test(`quick creation recovers an existing project after ${stage} fails`, async ({ page }) => {
    const quick = await mockQuickDeploy(page)
    await page.route(`**/api/v1/deploy/77/environments/78/${stage}`, (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "unavailable", message: "Setup service unavailable" },
        }),
      }),
    )
    await page.goto("/deploy/new")
    await page.getByText("From GitHub", { exact: true }).click()
    await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
    await page.getByRole("button", { name: "Continue", exact: true }).click()
    await page.getByText("Import .env", { exact: true }).click()
    await page
      .getByRole("textbox", { name: "Environment variables", exact: true })
      .fill("API_TOKEN=keep-this-value")
    await page.getByRole("button", { name: "Deploy", exact: true }).click()
    await expect(page.getByRole("heading", { name: "Deployment created" })).toBeVisible()
    await expect(page.getByText("Setup service unavailable", { exact: true })).toBeVisible()
    expect(quick.commits()).toBe(1)
    await expect(page.getByRole("button", { name: "Deploy", exact: true })).toHaveCount(0)
    if (stage === "variables/import") {
      await page.getByText("Keep a copy of your environment variables").click()
      await expect(
        page.getByRole("textbox", { name: "Unsaved environment variables" }),
      ).toHaveValue("API_TOKEN=keep-this-value")
      await expect(page.getByRole("link", { name: "Finish environment setup" })).toHaveAttribute(
        "href",
        "/deploy/77?tab=variables",
      )
    } else {
      await expect(
        page.getByRole("link", { name: "Open deployment", exact: true }),
      ).toHaveAttribute("href", "/deploy/77?tab=deployments")
    }
  })
}

test("fleet grid and list preserve filters and fit multiple projects", async ({
  page,
}, testInfo) => {
  await mockDashboard(page)
  const names = [
    "storefront",
    "payments-api",
    "background-worker",
    "documentation",
    "staging-site",
    "status-page",
  ]
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: names.map((name, index) => ({
        ...deployment,
        id: index + 1,
        name,
        endpoint: `https://${name}.example.test`,
        activeRun: undefined,
      })),
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/deploy")
  const projects = page.getByRole("list", { name: "Deployment projects" })
  await expect(projects.locator(":scope > li")).toHaveCount(6)
  await expect(page.getByRole("button", { name: "Grid view" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await testInfo.attach("fleet-project-grid", {
    body: await page.screenshot({ path: testInfo.outputPath("fleet-grid.png"), fullPage: true }),
    contentType: "image/png",
  })
  await page.getByRole("textbox", { name: "Search deployments" }).fill("payments")
  await expect(projects.locator(":scope > li")).toHaveCount(1)
  await page.getByRole("button", { name: "List view" }).click()
  await expect(page.getByRole("table")).toBeVisible()
  await expect(page.getByRole("table").getByText("payments-api", { exact: true })).toBeVisible()
  await expect(page.getByRole("table").getByText("storefront", { exact: true })).toHaveCount(0)
  await page.getByRole("textbox", { name: "Search deployments" }).fill("")
  await expect(page.getByRole("table").getByRole("row")).toHaveCount(7)
  await page.setViewportSize({ width: 390, height: 900 })
  await expect(projects).toBeVisible()
  await expect(projects.locator(":scope > li")).toHaveCount(6)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
})

test("website preview loads inside a constrained frame", async ({ page }) => {
  await mockDashboard(page)
  let requests = 0
  await page.route("**/api/v1/deploy/7/preview-frame", (route) => {
    requests++
    return route.fulfill({
      contentType: "text/html",
      headers: {
        "Content-Security-Policy":
          "default-src 'none'; frame-src https://preview.example.test; frame-ancestors 'self'",
        "X-Frame-Options": "SAMEORIGIN",
      },
      body: '<iframe title="Deployed website" src="https://preview.example.test" sandbox="allow-scripts allow-same-origin allow-forms" referrerpolicy="no-referrer"></iframe>',
    })
  })
  await page.route("https://preview.example.test/", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: '<h1 id="app"></h1><script>document.getElementById("app").textContent="My deployed website";try{parent.parent.document.body.innerHTML="Unexpected access"}catch{document.body.insertAdjacentHTML("beforeend","<p>Dashboard isolated</p>")}</script>',
    }),
  )
  await page.goto("/deploy/7")
  await expect.poll(() => requests).toBe(1)
  const preview = page.frameLocator('iframe[title="Website preview for api-production"]')
  await expect(
    preview
      .frameLocator('iframe[title="Deployed website"]')
      .getByRole("heading", { name: "My deployed website" }),
  ).toBeVisible()
  await expect(
    preview.frameLocator('iframe[title="Deployed website"]').getByText("Dashboard isolated"),
  ).toBeVisible()
  await page.getByRole("button", { name: "Mobile width", exact: true }).click()
  const width = await page
    .locator('iframe[title="Website preview for api-production"]')
    .evaluate((frame) => frame.getBoundingClientRect().width)
  expect(width).toBeLessThanOrEqual(384)
  await page.getByRole("button", { name: "Reload website preview" }).click()
  await expect.poll(() => requests).toBe(2)
  await page.getByRole("button", { name: "Close preview" }).click()
  await expect(page.locator('iframe[title="Website preview for api-production"]')).toHaveCount(0)
  await expect(page).toHaveURL(/\/deploy\/7$/)
})

for (const normalized of [false, true]) {
  test(`delete project is accessible and confirmed for ${normalized ? "normalized" : "legacy"} deployments`, async ({
    page,
  }) => {
    await mockDashboard(page, { normalized })
    let deleted = 0
    await page.route("**/api/v1/deploy/7", async (route) => {
      if (route.request().method() !== "DELETE") return route.fallback()
      deleted++
      await route.fulfill({ status: 204 })
    })
    await page.goto("/deploy/7")
    await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
    await page.getByRole("menuitem", { name: /Archive deployment/ }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Running containers, routes, and persistent data remain")
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
    expect(deleted).toBe(0)
    await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
    await page.getByRole("menuitem", { name: /Archive deployment/ }).click()
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Archive deployment", exact: true })
      .click()
    await expect(page).toHaveURL(/\/deploy$/)
    expect(deleted).toBe(1)
  })
}

test("delete project keeps the project open when the server refuses", async ({ page }) => {
  await mockDashboard(page, { normalized: true })
  await page.route("**/api/v1/deploy/7", async (route) => {
    if (route.request().method() !== "DELETE") return route.fallback()
    await route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "conflict", message: "Project is busy" } }),
    })
  })
  await page.goto("/deploy/7")
  await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
  await page.getByRole("menuitem", { name: /Archive deployment/ }).click()
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Archive deployment", exact: true })
    .click()
  await expect(page.getByText("Project is busy", { exact: true })).toBeVisible()
  await expect(page.getByRole("dialog")).toBeVisible()
  await expect(page).toHaveURL(/\/deploy\/7$/)
})

test("delete project is hidden without destructive capability", async ({ page }) => {
  await mockDashboard(page, { normalized: true })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto("/deploy/7")
  await expect(page.getByRole("heading", { name: /api-production/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "Archive deployment", exact: true })).toHaveCount(0)
})

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

const run = {
  id: 84,
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

const deployment = {
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
  lastRun: run,
  activeRun: run,
  updatedAt: now,
}

const project = {
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

const steps = [
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

const healthyOperations: DeploymentOperations = {
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

const releaseComparison: ReleaseComparisonResponse = {
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

async function mockDashboard(
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
  let automationSchedules: Record<string, unknown>[] = []
  let notificationChannels: Record<string, unknown>[] = []
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
          buildMethod: options.normalized ? "recipe" : "legacy_compose",
          activeRun: undefined,
        },
      }
    } else if (path === "/deploy/7/operations") {
      body = options.operations ?? healthyOperations
    } else if (path === "/deploy/7/environments/12/releases/20/comparison") {
      body = options.comparison ?? releaseComparison
    } else if (path === "/deploy/7/environments/12/releases") {
      body = options.normalized
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
    } else if (path === "/deploy/7/runs/84" && method === "GET") {
      body = { run: liveRun(), steps }
    } else if (path === "/deploy/7/environments/12/configuration" && method === "GET") {
      body = configurationBody()
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
      const channel = { id: 61, ...input }
      notificationChannels = [channel]
      body = { channel, secret: "one-time-notification-secret" }
    } else if (path === "/deploy/7/environments/12/configuration" && method === "PUT") {
      const requestBody = request.postDataJSON() as typeof normalizedConfiguration
      normalizedConfiguration = {
        build: requestBody.build,
        runtime: requestBody.runtime,
        dependencies: requestBody.dependencies,
        checks: requestBody.checks,
        domains: requestBody.domains,
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
  }
}

test("runtime services hand off to exact Docker panels and survive history and reload", async ({
  page,
}, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  const runtime: DeploymentRuntimeServices = {
    status: "available",
    observedAt: now,
    services: [
      {
        containerId: "abc123",
        name: "web-live",
        releaseId: 20,
        liveRelease: true,
        state: "running",
        health: "healthy",
        imageId: "sha256:abc",
        stack: "jd-e12",
        service: "web",
      },
      {
        containerId: "def456",
        name: "web-candidate",
        releaseId: 21,
        liveRelease: false,
        state: "exited",
        health: "unavailable",
        imageId: "sha256:def",
      },
    ],
  }
  const dashboard = await mockDashboard(page, { normalized: true, runtime })
  await page.route("**/api/v1/docker/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/v1/docker/ping") return json(route, { available: true })
    if (path === "/api/v1/docker/stacks/") return json(route, [])
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "not_found", message: `Runtime no longer exists: ${path}` },
      }),
    })
  })
  for (const width of [375, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7?tab=runtime")
    const list = page.getByRole("list", { name: "Runtime services" })
    await expect(list.getByText("Live release", { exact: true })).toBeVisible()
    await expect(list.getByText("Other release", { exact: true })).toBeVisible()
    await expect(list.getByText("Health: Not observed")).toBeVisible()
    await expect(list.getByRole("link", { name: "web-live", exact: true })).toHaveAttribute(
      "href",
      "/docker/containers?container=abc123",
    )
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    // One theme now, so one shot. The product ships dark only — there is no
    // toggle to drive and no second palette to check against.
    if (width === 375 || width === 1440) {
      await list.screenshot({ path: testInfo.outputPath(`runtime-${width}.png`) })
    }
  }
  const containerLink = page.getByRole("link", { name: "web-live", exact: true })
  await containerLink.focus()
  await expect(containerLink).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/\/docker\/containers\?container=abc123$/)
  await expect(page.getByRole("dialog")).toContainText(
    "Runtime no longer exists: /api/v1/docker/containers/abc123",
  )
  await page.reload()
  await expect(page.getByRole("dialog")).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page).toHaveURL(/\/docker\/containers$/)
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await page.goBack()
  await expect(page.getByRole("dialog")).toBeVisible()
  await page.goForward()
  await expect(page.getByRole("dialog")).toHaveCount(0)

  await page.goto("/deploy/7?tab=runtime")
  await page.getByRole("link", { name: "Open stack jd-e12 · web" }).click()
  await expect(page).toHaveURL(/\/docker\/stacks\?stack=jd-e12$/)
  await expect(page.getByRole("dialog")).toContainText(
    "Runtime no longer exists: /api/v1/docker/stacks/jd-e12",
  )
  await page.reload()
  await expect(page.getByRole("dialog")).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page).toHaveURL(/\/docker\/stacks$/)
  await expect(page.getByRole("dialog")).toHaveCount(0)
  expect(dashboard.mutationCount()).toBe(0)
})

test("runtime evidence distinguishes unavailable Docker from an empty managed inventory", async ({
  page,
}) => {
  const runtime: DeploymentRuntimeServices = {
    status: "unavailable",
    observedAt: now,
    reason: "Docker cannot be reached. Open Docker to check the connection.",
    services: [],
  }
  await mockDashboard(page, { normalized: true, runtime })
  await page.goto("/deploy/7?tab=runtime")
  await expect(page.getByText("Runtime unavailable", { exact: true })).toBeVisible()
  await expect(page.getByText(runtime.reason!)).toBeVisible()
  await expect(page.getByText("No managed runtime services", { exact: true })).toHaveCount(0)
  runtime.status = "available"
  await page.reload()
  await expect(page.getByText("No managed runtime services", { exact: true })).toBeVisible()
  await expect(page.getByText("Runtime unavailable", { exact: true })).toHaveCount(0)
})

test.describe("runtime log handoffs", () => {
  test.use({ timezoneId: "America/New_York" })

  test("log links preserve exact activation windows and never substitute a missing source", async ({
    page,
  }) => {
    await mockDashboard(page)
    const searches: URL[] = []
    await page.route("**/api/v1/logs/**", async (route) => {
      const url = new URL(route.request().url())
      if (url.pathname.endsWith("/sources"))
        return json(route, {
          sources: [
            { id: "file:/var/log/syslog", label: "syslog", kind: "system", rotated: false },
            { id: "docker:abc123", label: "web-live", kind: "docker", rotated: false },
          ],
          units: [],
          roots: ["/var/log"],
          missing: {},
        })
      if (url.pathname.endsWith("/search")) {
        searches.push(url)
        return json(route, {
          lines: [{ text: "activation evidence", source: "docker:abc123" }],
          scanned: 1,
          matched: 1,
          truncated: false,
          complete: true,
          files: [],
          histogram: [],
          tookMillis: 1,
        })
      }
      return json(route, {})
    })
    const since = "2026-11-01T06:25:30.123Z"
    const until = "2026-11-01T06:35:30.456Z"
    await page.goto(`/logs?${new URLSearchParams({ source: "docker:abc123", since, until })}`)
    await expect.poll(() => searches.length).toBe(1)
    expect(searches[0].searchParams.get("source")).toBe("docker:abc123")
    expect(searches[0].searchParams.get("since")).toBe(since)
    expect(searches[0].searchParams.get("until")).toBe(until)
    await expect(page.getByText("activation evidence", { exact: true })).toBeVisible()
    await page.reload()
    await expect.poll(() => searches.length).toBe(2)
    expect(searches[1].searchParams.get("since")).toBe(since)
    expect(searches[1].searchParams.get("until")).toBe(until)

    await page.goto("/logs?source=docker:removed&mode=search")
    await expect(page.getByText("Requested log source unavailable", { exact: true })).toBeVisible()
    expect(new URL(page.url()).searchParams.get("source")).toBe("docker:removed")
    expect(searches).toHaveLength(2)

    for (const bounds of [
      { since: "invalid", until },
      { since: until, until: since },
    ]) {
      await page.goto(`/logs?${new URLSearchParams({ source: "docker:abc123", ...bounds })}`)
      await expect(page.getByText("Invalid log window", { exact: true })).toBeVisible()
      expect(searches).toHaveLength(2)
    }
    await page.getByRole("button", { name: "Use last 24 hours" }).click()
    await expect.poll(() => searches.length).toBe(3)
    expect(searches[2].searchParams.get("source")).toBe("docker:abc123")
    expect(searches[2].searchParams.has("until")).toBe(false)
  })
})

test("run runtime logs open the server-provided activation window and withhold unproven windows", async ({
  page,
}) => {
  await mockDashboard(page)
  const since = "2026-11-01T06:25:30.123Z"
  const until = "2026-11-01T06:35:30.123Z"
  const query = new URLSearchParams({ source: "docker:preview", mode: "search", since, until })
  let activated = true
  await page.route("**/api/v1/deploy/7/runs/84/logs", (route) =>
    json(route, {
      status: "available",
      windowReason: activated ? "" : "This run has no completed activation evidence.",
      sources: [
        {
          containerId: "preview",
          name: "preview-web",
          liveUrl: "/logs?source=docker%3Apreview",
          activationUrl: activated ? `/logs?${query}` : undefined,
        },
      ],
    }),
  )
  const streams: string[] = []
  await page.routeWebSocket(/\/api\/v1\/logs\/stream/, (socket) => {
    streams.push(new URL(socket.url()).searchParams.get("source") || "")
    socket.send(
      JSON.stringify({
        type: "logs",
        data: [{ text: "GET /api/health 200", level: "info" }],
        ts: Date.now(),
      }),
    )
  })
  const searches: URL[] = []
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith("/sources"))
      return json(route, {
        sources: [{ id: "docker:preview", label: "preview-web", kind: "docker", rotated: false }],
        units: [],
        roots: [],
        missing: {},
      })
    if (url.pathname.endsWith("/search")) {
      searches.push(url)
      return json(route, {
        lines: [],
        scanned: 0,
        matched: 0,
        truncated: false,
        complete: true,
        files: [],
        histogram: [],
        tookMillis: 1,
      })
    }
    return json(route, {})
  })
  await page.goto("/deploy/7/runs/84")
  await page
    .getByRole("group", { name: "Run views" })
    .getByRole("button", { name: "Runtime logs", exact: true })
    .click()
  await expect(page.getByText("GET /api/health 200", { exact: true })).toBeVisible()
  expect(streams).toContain("docker:preview")
  const link = page.getByRole("button", { name: "Around activation", exact: true })
  await expect(link).toBeVisible()
  await link.focus()
  await page.keyboard.press("Enter")
  await expect.poll(() => searches.length).toBe(1)
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/84$/)
  expect(searches[0].searchParams.get("source")).toBe("docker:preview")
  expect(searches[0].searchParams.get("since")).toBe(since)
  expect(searches[0].searchParams.get("until")).toBe(until)
  activated = false
  await page.goto("/deploy/7/runs/84")
  await page
    .getByRole("group", { name: "Run views" })
    .getByRole("button", { name: "Runtime logs", exact: true })
    .click()
  await expect(
    page.getByText("This run has no completed activation evidence.", { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Around activation", exact: true })).toHaveCount(0)
  await expect(page.getByRole("combobox", { name: "Runtime log source" })).toHaveText("preview-web")
})

const draft = {
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

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

const blueprintCatalogue = [
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
    image: "itzg/minecraft-server:2025.1.1-java21",
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
    image: "louislam/uptime-kuma:1.23.16",
    memoryMb: 512,
  },
]

const minecraftBlueprint = {
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

const minecraftVersions = {
  status: "available",
  source: "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json",
  checkedAt: now,
  recommended: "1.21.4",
  versions: [
    { id: "1.21.4", kind: "release", releasedAt: now, recommended: true, latest: true },
    { id: "1.21.3", kind: "release", releasedAt: now },
  ],
}

async function mockWizardJourney(page: Page) {
  let revision = 1
  let currentStep = "intent"
  let data: Record<string, unknown> = {}
  let commits = 0
  const currentDraft = () => ({
    id: "journey-draft",
    ownerUsername: "operator",
    currentStep,
    revision,
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
    if (path === "/deploy/blueprints/") return json(route, blueprintCatalogue)
    if (path === "/deploy/blueprints/minecraft-java") return json(route, minecraftBlueprint)
    if (path === "/deploy/blueprints/minecraft-java/versions") return json(route, minecraftVersions)
    if (path === "/deploy/drafts" && method === "POST") return json(route, currentDraft())
    if (path === "/deploy/drafts/journey-draft" && method === "GET") {
      return json(route, currentDraft())
    }
    if (path === "/deploy/drafts/journey-draft" && method === "PUT") {
      const body = request.postDataJSON() as {
        step: string
        intent?: unknown
        source?: unknown
        configuration?: unknown
      }
      revision += 1
      currentStep = body.step
      if (body.intent) data = { ...data, intent: body.intent }
      if (body.source) data = { ...data, source: body.source }
      if (body.configuration) data = { ...data, configuration: body.configuration }
      return json(route, currentDraft())
    }
    if (path === "/deploy/drafts/journey-draft/detect" && method === "POST") {
      const intent = data.intent as { profile: string }
      const source = data.source as { kind: string; url?: string; image?: string }
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
    configuration: () => data.configuration as Record<string, unknown> | undefined,
  }
}

/**
 * The quick path, which is what /deploy/new answers with.
 *
 * It drives the same draft endpoints the wizard does, so the journey mock is
 * reused; what is added here is the three things quick deploy asks of the
 * server that the wizard never did — the repository list, the generated public
 * hostname, and the run it starts at the end.
 */
async function mockQuickDeploy(page: Page) {
  const journey = await mockWizardJourney(page)
  let imported: Record<string, unknown> | undefined
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
  await page.route("**/api/v1/deploy/77/environments/78/variables/import", (route) => {
    imported = route.request().postDataJSON() as Record<string, unknown>
    return json(route, { desiredRevision: 9, variables: [] })
  })
  await page.route("**/api/v1/deploy/77/environments/78/runs", (route) => {
    runs += 1
    return json(route, { id: 84, state: "queued" })
  })
  return { ...journey, imported: () => imported, runs: () => runs }
}

test("quick deploy takes a GitHub repository to a running release without the wizard", async ({
  page,
}) => {
  const quick = await mockQuickDeploy(page)
  // On a phone, because the first deploy is as likely to be started from one as
  // the wizard is, and every primary action here is a 44px target.
  await page.setViewportSize({ width: 375, height: 850 })
  await page.goto("/deploy/new")

  // Three outcomes, not eight: the lane is the only classification asked for.
  await expect(page.getByRole("heading", { name: "Import Git repository" })).toBeVisible()
  await page.getByText("From GitHub", { exact: true }).click()

  // The repository is picked from what the signed-in credential can reach,
  // rather than typed as a clone URL from memory.
  await expect(page.getByRole("button", { name: /Wayy01/ }).first()).toBeVisible()
  await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
  await expect(page.getByRole("combobox", { name: "Branch" })).toContainText("main")
  await page.getByRole("button", { name: "Continue" }).click()

  // Detection fills the build in, and the public hostname is already chosen.
  await page.locator("summary").filter({ hasText: "Build settings" }).click()
  await expect(page.getByRole("textbox", { name: "Build command" })).toHaveValue("bun run build")
  await expect(page.getByRole("textbox", { name: "Start command" })).toHaveValue("bun start")
  await expect(page.getByRole("spinbutton", { name: /Port/ })).toHaveValue("3000")
  await expect(page.getByRole("textbox", { name: "Hostname" })).toHaveValue(
    "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
  )
  // Nothing to press: the certificate is the run's own work, before it starts
  // anything, and the screen says so rather than offering a button.
  await expect(page.getByText("A certificate will be issued during the deploy")).toBeVisible()
  await expect(page.getByRole("button", { name: /certificate/i })).toHaveCount(0)

  await page.getByText("Import .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables" })
    .fill("NEXT_PUBLIC_SITE_URL=https://example.test")
  const deployButton = page.getByRole("button", { name: "Deploy", exact: true })
  expect((await deployButton.boundingBox())?.height).toBeGreaterThanOrEqual(44)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await deployButton.click()

  // One press: plan saved, preflight run, environment applied, release started.
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  const savedChecks = quick.configuration()?.checks as Array<{ config: Record<string, unknown> }>
  expect(savedChecks[0].config).not.toHaveProperty("port")
  expect(quick.commits()).toBe(1)
  expect(quick.runs()).toBe(1)
  expect(quick.imported()).toMatchObject({
    dotenv: "NEXT_PUBLIC_SITE_URL=https://example.test",
    scopes: ["runtime", "build"],
  })
  expect(quick.configuration()).toMatchObject({
    build: { buildCommand: "bun run build", startCommand: "bun start" },
    runtime: { internalPort: 3000 },
    domains: [
      { hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io", https: true, ownership: "managed" },
    ],
    checks: [{ kind: "http", phase: "readiness", required: true }],
  })
})

test("deployment fleet stays useful across the responsive contract", async ({ page }, testInfo) => {
  await mockDashboard(page)

  for (const width of [375, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy")
    await expect(page.getByRole("heading", { name: "Deployments", exact: true })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Active work" })).toBeVisible()
    await expect(page.getByText("api-production", { exact: true }).first()).toBeVisible()
    await expect(page.getByText("Not observed").filter({ visible: true }).first()).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      `horizontal viewport overflow at ${width}px`,
    ).toBe(true)
    await testInfo.attach(`fleet-${width}`, {
      body: await page.screenshot({ fullPage: true }),
      contentType: "image/png",
    })
  }
})

test("wizard exposes outcome choices and focuses a linked validation summary", async ({ page }) => {
  await mockDashboard(page)
  await page.setViewportSize({ width: 375, height: 850 })
  await page.goto("/deploy/new?mode=advanced")

  await expect(page.getByRole("heading", { name: "New project" })).toBeVisible()
  await expect(page.getByRole("radio", { name: /Web app or API/ })).toBeChecked()
  await page.getByRole("button", { name: "Continue" }).click()
  const alert = page.getByRole("alert", { name: "There is a problem" })
  await expect(alert).toContainText("Use 1–64 letters")
  await expect(alert).toBeFocused()
  const continueBox = await page.getByRole("button", { name: "Continue" }).boundingBox()
  expect(continueBox?.height).toBeGreaterThanOrEqual(44)
  await alert.getByRole("link").click()
  await expect(page.getByRole("textbox", { name: "Deployment name" })).toBeFocused()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
})

test("public Git and expert settings reach a reviewed plan without leaving the wizard", async ({
  page,
}) => {
  const journey = await mockWizardJourney(page)
  await page.goto("/deploy/new?mode=advanced")
  await page.getByRole("textbox", { name: "Deployment name" }).fill("public-web")
  await page.getByRole("button", { name: "Continue" }).click()
  await expect(page.getByRole("heading", { name: "Connect the source" })).toBeVisible()
  await page.getByRole("textbox", { name: "Git URL" }).fill("https://example.test/public/web.git")
  await page.getByRole("button", { name: "Inspect source" }).click()
  await expect(page.getByRole("heading", { name: "Review evidence" })).toBeVisible()
  await page.getByRole("button", { name: "Use this detection" }).click()
  await expect(page.getByRole("heading", { name: "Set runtime decisions" })).toBeVisible()
  await page.locator("summary").filter({ hasText: "Runtime and route" }).click()
  await page.getByRole("textbox", { name: "Public domain" }).fill("web.example.test")
  await expect(page.getByRole("combobox", { name: "Automatic recipe" })).toContainText("Node.js")
  await page.getByText("Variable references & scopes", { exact: true }).click()
  await page.getByRole("button", { name: "Add variable" }).click()
  await page.getByRole("textbox", { name: "Variable 1 name" }).fill("NPM_TOKEN")
  await page.getByRole("checkbox", { name: "Build", exact: true }).check()
  await page.getByRole("checkbox", { name: "Release Task", exact: true }).check()
  const advanced = page.getByRole("button", { name: /Advanced/ })
  await advanced.click()
  await expect(advanced).toHaveAttribute("aria-expanded", "true")
  await page.getByRole("textbox", { name: "Target platform" }).fill("linux/amd64")
  await page.getByRole("button", { name: "Add build secret" }).click()
  await page.getByRole("combobox", { name: "Build secret 1 variable" }).fill("NPM_TOKEN")
  await page.getByRole("button", { name: "Add release task" }).click()
  await page.getByRole("textbox", { name: "Release task 1 name" }).fill("Database migration")
  await page.getByRole("textbox", { name: "Release task 1 working directory" }).fill("app")
  await page.getByRole("textbox", { name: "Release task 1 command" }).fill("./bin/migrate")
  await page.getByRole("checkbox", { name: "NPM_TOKEN", exact: true }).check()
  for (const label of [
    "Command argv",
    "Linux capabilities",
    "Host devices",
    "Mounts JSON",
    "Dependencies JSON",
    "Readiness and smoke checks JSON",
    "Additional domains JSON",
  ]) {
    await expect(page.getByRole("textbox", { name: label })).toBeVisible()
  }
  await page.getByRole("button", { name: "Run preflight" }).click()
  await expect(page.getByRole("heading", { name: "Check the release path" })).toBeVisible()
  await expect(page.getByText("Ready to save, not execute")).toBeVisible()
  expect(journey.configuration()).toMatchObject({
    build: {
      recipe: "node",
      targetPlatform: "linux/amd64",
      secrets: [{ variable: "NPM_TOKEN", step: "install" }],
      releaseTasks: [
        {
          name: "Database migration",
          command: "./bin/migrate",
          workingDirectory: "app",
          timeoutSeconds: 300,
          env: ["NPM_TOKEN"],
        },
      ],
    },
  })
  await page.getByRole("button", { name: "Save deployment" }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(journey.commits()).toBe(1)
})

test("Minecraft reaches a safe reviewed plan with explicit EULA acceptance and no Advanced fields", async ({
  page,
}) => {
  const journey = await mockWizardJourney(page)
  await page.goto("/deploy/new?mode=advanced")
  await page.getByRole("textbox", { name: "Deployment name" }).fill("minecraft-family")
  await page.getByText("Game server", { exact: true }).click()
  await page.getByRole("button", { name: "Continue" }).click()

  // The reviewed catalogue is what a game server starts from, and it says who
  // reviewed it and under which licence before anything is chosen.
  await expect(page.getByRole("radio", { name: /Minecraft \(Java Edition\)/ })).toBeChecked()
  await expect(page.getByText("itzg/minecraft-server:2025.1.1-java21")).toBeVisible()
  await expect(page.getByText(/Reviewed 2026-09-11 by Just Dashboard/)).toBeVisible()
  // A game blueprint offers only game blueprints; a web application is not one.
  await expect(page.getByRole("radio", { name: /Uptime Kuma/ })).toHaveCount(0)

  // Versions come from the upstream manifest, never from a guess.
  await expect(page.getByText("2 releases read from the upstream manifest.")).toBeVisible()

  // The EULA is accepted against its own linked agreement, in the open, and it
  // is not an Advanced field.
  const eula = page.getByRole("checkbox", { name: /I accept the Minecraft EULA/ })
  await expect(eula).not.toBeChecked()
  await expect(page.getByRole("link", { name: /Read the agreement/ })).toHaveAttribute(
    "href",
    "https://aka.ms/MinecraftEULA",
  )
  await eula.check()

  // Advanced blueprint settings stay folded away until asked for.
  const advancedToggle = page.getByRole("button", { name: /advanced blueprint settings/ })
  await expect(advancedToggle).toHaveAttribute("aria-expanded", "false")
  await expect(page.getByText("Verify accounts with Mojang")).toHaveCount(0)
  await advancedToggle.click()
  await expect(page.getByText("Verify accounts with Mojang")).toBeVisible()
  await advancedToggle.click()

  await page.getByRole("button", { name: "Inspect source" }).click()
  await page.getByRole("button", { name: "Use this detection" }).click()
  await expect(page.getByRole("spinbutton", { name: "Application port" })).toHaveValue("25565")
  await expect(page.getByRole("button", { name: /Advanced/ }).first()).toHaveAttribute(
    "aria-expanded",
    "false",
  )
  await page.getByRole("button", { name: "Run preflight" }).click()
  await expect(page.getByText("Ready to save, not execute")).toBeVisible()
  await page.getByRole("button", { name: "Save deployment" }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(journey.commits()).toBe(1)
})

test("project workspace keeps pending state and permanent run links visible", async ({ page }) => {
  await mockDashboard(page)
  await page.goto("/deploy/7")

  await expect(page.getByRole("heading", { name: /api-production/ })).toBeVisible()
  await expect(page.getByText("Pending deployment", { exact: true })).toBeVisible()
  await expect(page.getByText("Not observed").first()).toBeVisible()
  await page.getByRole("link", { name: "Deployments", exact: true }).last().click()
  await expect(page.getByRole("link", { name: /Run #84/ })).toHaveAttribute(
    "href",
    "/deploy/7/runs/84",
  )
  await page.getByRole("button", { name: "Roll back" }).click()
  const rollback = page.getByRole("dialog", { name: "Roll back" })
  await expect(rollback).toBeVisible()
  await rollback.getByRole("button", { name: "Roll back" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/86$/)
})

test("normalized workspace exposes distinct immutable release actions and ordinary rollback confirmation", async ({
  page,
}) => {
  const dashboard = await mockDashboard(page, { normalized: true })
  await page.goto("/deploy/7?tab=deployments")

  await expect(page.getByRole("button", { name: "Deploy changes" })).toBeVisible()
  await page.getByRole("button", { name: "Deployment actions" }).click()
  await expect(page.getByRole("menuitem", { name: /Redeploy live/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Restart/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Rebuild without cache/ })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("heading", { name: "Immutable releases" })).toBeVisible()
  await expect(page.getByText("Release #2", { exact: true })).toBeVisible()
  await expect(page.getByText("Live", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Deployment actions" }).click()
  await page.getByRole("menuitem", { name: /Redeploy live/ }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/88$/)
  await page.goBack()
  await expect(page.getByText("Pending deployment", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Roll back" }).click()
  const rollback = page.getByRole("dialog", { name: "Roll back to release #1" })
  await expect(rollback).toBeVisible()
  await expect(rollback.getByRole("textbox")).toHaveCount(0)
  await rollback.getByRole("button", { name: "Roll back" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/90$/)
  expect(dashboard.actions()).toEqual(["redeploy", "rollback"])
})

test("normalized configuration joins keep secrets masked and saved changes pending until deployment", async ({
  page,
}) => {
  await mockDashboard(page, { normalized: true })
  await page.setViewportSize({ width: 375, height: 900 })
  await page.goto("/deploy/7?tab=variables")

  await expect(page.getByRole("heading", { name: "Environment variables" })).toBeVisible()
  await expect(page.getByText("••••••••")).toBeVisible()
  await expect(page.getByText("revealed-browser-secret")).toHaveCount(0)
  await page.getByRole("button", { name: "Add variable", exact: true }).click()
  await page.getByRole("textbox", { name: "Variable name" }).fill("DATABASE_TOKEN")
  await page.getByLabel("Value").fill("browser-only-input-secret")
  await page.getByRole("button", { name: "Save variable" }).click()
  await expect(page.getByText("DATABASE_TOKEN", { exact: true })).toBeVisible()
  await expect(page.getByText("browser-only-input-secret")).toHaveCount(0)
  await expect(
    page.getByText("Saved changes will apply on your next deployment", { exact: true }).first(),
  ).toBeVisible()

  const apiTokenRow = page.getByRole("listitem").filter({ hasText: "API_TOKEN" })
  await apiTokenRow.getByRole("button", { name: "Rotate" }).click()
  await expect(page.getByText("rotated-browser-secret")).toBeVisible()

  await page.getByRole("link", { name: /Domains & ports/ }).click()
  await page.getByRole("button", { name: "Add domain" }).click()
  await page.getByRole("textbox", { name: "Domain 2" }).fill("next.example.test")
  await page.getByRole("button", { name: "Save network plan" }).click()
  await expect(page.getByText("Certificate required")).toBeVisible()

  await page.getByRole("link", { name: /Lifecycle/ }).click()
  // The save toast overlaps the bottom-right of the page while it is showing.
  // Waiting it out is the honest fix: forcing the click would test a button an
  // operator could not have pressed either.
  await page.locator("[data-sonner-toast]").first().waitFor({ state: "detached", timeout: 15000 })
  await page.getByRole("button", { name: "Preview managed targets" }).click()
  await expect(page.getByText("api-data", { exact: true })).toBeVisible()
  await expect(page.getByText(/typed confirmation/)).toBeVisible()

  await page.getByRole("button", { name: "Deploy changes" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/88$/)
  await page.goBack()
  await page.getByRole("link", { name: "Runtime settings", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Runtime configuration" })).toBeVisible()
  await expect(
    page.getByText("Saved changes will apply on your next deployment", { exact: true }),
  ).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )

  await page.setViewportSize({ width: 667, height: 375 })
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" })
  await page.getByRole("link", { name: "Runtime settings", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Runtime configuration" })).toBeVisible()
  await expect(
    page.getByText("Saved changes will apply on your next deployment", { exact: true }),
  ).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" })
  await page.getByRole("link", { name: "Runtime settings", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Runtime configuration" })).toBeVisible()
})

test("automation workspace creates provider, schedule, preview and signed notification policy", async ({
  page,
}) => {
  await mockDashboard(page, { normalized: true })
  await page.setViewportSize({ width: 375, height: 900 })
  await page.goto("/deploy/7?tab=automations")
  await expect(page.getByRole("heading", { name: "Source automations" })).toBeVisible()
  await page.getByRole("button", { name: "Preview environments", exact: true }).click()
  await expect(page.getByText("pr-42", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Git & webhooks", exact: true }).click()

  await page.getByRole("button", { name: "Add automation" }).click()
  await page.getByLabel("Repository").fill("acme/api")
  await page.getByLabel("Watched paths").fill("services/api/**")
  await page.getByRole("button", { name: "Create automation" }).click()
  await expect(page.getByText("one-time-provider-secret", { exact: true })).toBeVisible()
  await expect(page.getByText(/acme\/api/)).toBeVisible()

  await page.getByRole("button", { name: "Schedules", exact: true }).click()
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await page.getByLabel("Cron expression").fill("30 2 * * *")
  await page.getByLabel("IANA timezone").fill("America/New_York")
  await page.getByRole("button", { name: "Create schedule" }).click()
  await expect(page.getByText(/America\/New_York/)).toBeVisible()

  await page.getByRole("button", { name: "Notifications", exact: true }).click()
  await page.getByRole("button", { name: "Add channel" }).click()
  await page.getByLabel("HTTPS endpoint").fill("https://hooks.example.test/deploy")
  await page.getByRole("button", { name: "Create channel" }).click()
  await expect(page.getByText("one-time-notification-secret", { exact: true })).toBeVisible()
  await expect(
    page.getByRole("paragraph").filter({ hasText: "https://hooks.example.test/deploy" }),
  ).toBeVisible()
  await expect(page.locator("main")).not.toHaveCSS("overflow-x", "scroll")
})

test("run page renders persisted release evidence and keyboard-selectable transcript steps", async ({
  page,
}) => {
  await mockDashboard(page)
  await page.emulateMedia({ reducedMotion: "reduce" })
  await page.goto("/deploy/7/runs/84")

  await expect(page.getByRole("heading", { name: "Run #84" })).toBeVisible()
  await expect(page.getByText("Verify Readiness…", { exact: true })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Build logs", exact: true })).toBeVisible()
  await expect
    .poll(() =>
      page
        .locator('[data-slot="page-header"] svg.animate-spin')
        .evaluate((icon) => getComputedStyle(icon).animationName),
    )
    .toBe("none")
  await page.getByRole("button", { name: "Execution details", exact: true }).click()
  const smoke = page.getByRole("button", { name: /Verify Smoke/ })
  await smoke.focus()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toHaveText("Verify Smoke")
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toBeFocused()
  await expect(page.getByRole("heading", { name: "Build logs", exact: true })).toBeVisible()
})

test("run transcript resumes from the last WebSocket sequence after a disconnect", async ({
  page,
}) => {
  await mockDashboard(page)
  const connections: string[] = []
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    connections.push(socket.url())
    const after = Number(new URL(socket.url()).searchParams.get("after") ?? 0)
    socket.send(JSON.stringify({ type: "snapshot", data: { run, steps }, ts: Date.now() }))
    if (after < 16) {
      socket.send(
        JSON.stringify({
          type: "events",
          data: [
            {
              seq: 16,
              type: "step.log",
              runId: 84,
              stepId: 110,
              ts: now,
              data: { stream: "stdout", text: "readiness attempt one\n", truncated: false },
            },
          ],
          ts: Date.now(),
        }),
      )
      setTimeout(() => void socket.close({ code: 1012, reason: "test reconnect" }), 100)
      return
    }
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          {
            seq: 17,
            type: "step.log",
            runId: 84,
            stepId: 110,
            ts: now,
            data: { stream: "stdout", text: "readiness recovered\n", truncated: false },
          },
        ],
        ts: Date.now(),
      }),
    )
  })

  await page.goto("/deploy/7/runs/84")
  await expect(page.getByText("readiness attempt one", { exact: true })).toBeVisible()
  await expect(page.getByText("readiness recovered", { exact: true })).toBeVisible()
  await expect.poll(() => connections.length).toBeGreaterThanOrEqual(2)
  expect(new URL(connections.at(-1)!).searchParams.get("after")).toBe("16")
})

test("run reload restores closed and active states, then cancel and retry stay keyboard reachable", async ({
  page,
}) => {
  const dashboard = await mockDashboard(page)
  const header = page.locator('[data-slot="page-header"]')
  for (const [state, label] of [
    ["queued", "Queued"],
    ["running", "Deploying"],
    ["failed", "Failed"],
    ["succeeded", "Succeeded"],
  ]) {
    dashboard.setRunState(state)
    await page.goto("/deploy/7/runs/84")
    await expect(page.getByRole("heading", { name: "Run #84", exact: true })).toBeVisible()
    await expect(header.getByText(label, { exact: true })).toBeVisible()
    await page.reload()
    await expect(header.getByText(label, { exact: true })).toBeVisible()
  }
  expect(dashboard.mutationCount()).toBe(0)

  dashboard.setRunState("queued")
  await page.reload()
  const cancel = page.getByRole("button", { name: "Cancel" })
  await cancel.focus()
  await page.keyboard.press("Enter")
  await expect(header.getByText("Cancelling", { exact: true })).toBeVisible()

  dashboard.setRunState("failed")
  await page.reload()
  const retry = page.getByRole("button", { name: "Retry" })
  await retry.focus()
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/85$/)
  expect(dashboard.mutationCount()).toBe(2)
})

test("operational findings name what was measured and hand off to the owning module", async ({
  page,
}, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  const operations: DeploymentOperations = {
    ...healthyOperations,
    domains: {
      status: "available",
      siteName: "just-dashboard-env-12.conf",
      domains: [
        {
          hostname: "api.example.test",
          https: true,
          ownership: "managed",
          route: "foreign",
          servedBy: "legacy.conf",
          certificate: "expired",
          certificateName: "api.example.test",
          deepLink: "/proxy/sites?site=legacy.conf",
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
          status: "missing",
          detail: "Docker volume was not found",
          deepLink: "/docker/volumes?volume=api-data",
        },
      ],
    },
    backups: {
      status: "unavailable",
      reason: "Backups inventory is unavailable. Open Backups to check the module.",
      jobs: [],
    },
    diagnosis: {
      status: "partial",
      findings: [
        {
          code: "storage_missing",
          severity: "critical",
          title: "Persistent storage for /data is not present",
          measured: "The storage owner could not find api-data.",
          means: "Data written to this path is not in the location the release declared.",
          action: "Open the storage owner to confirm whether the volume or path was removed.",
          owner: "docker",
          deepLink: "/docker/volumes?volume=api-data",
        },
        {
          code: "domain_foreign_route",
          severity: "critical",
          title: "api.example.test is served by another site",
          measured:
            "Site legacy.conf claims this server name; this deployment's generated site does not.",
          means:
            "Traffic for this hostname reaches whatever that site points at, not this release.",
          action: "Open the conflicting site in Proxy and decide which one owns the hostname.",
          owner: "proxy",
          deepLink: "/proxy/sites?site=legacy.conf",
        },
        {
          code: "plan_pending",
          severity: "notice",
          title: "Saved changes are not live",
          measured: "Saved plan revision 3 is not the live release's revision 2.",
          means: "What this page shows as configuration is not what is currently running.",
          action: "Review the pending changes and deploy when they are ready.",
          owner: "deploy",
        },
      ],
      silences: [
        {
          subject: "backups",
          reason: "Backups inventory is unavailable. Open Backups to check the module.",
        },
      ],
    },
  }
  const dashboard = await mockDashboard(page, { normalized: true, operations })
  for (const width of [375, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7?tab=diagnostics")
    await expect(page.getByText("Persistent storage for /data is not present")).toBeVisible()
    await expect(page.getByText("The storage owner could not find api-data.")).toBeVisible()
    await expect(page.getByText("api.example.test is served by another site")).toBeVisible()
    // A silenced owner is stated as an unanswered question, never as a clean result.
    await expect(page.getByText("Not assessed")).toBeVisible()
    await expect(
      page.getByText("Backups inventory is unavailable. Open Backups to check the module.").first(),
    ).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    if (width === 375 || width === 1440) {
      const findings = page.getByRole("region", { name: "Current findings" })
      const panel = (await findings.count()) ? findings : page.locator("body")
      await panel.screenshot({ path: testInfo.outputPath(`findings-${width}.png`) })
    }
  }

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/deploy/7?tab=diagnostics")
  const domains = page.getByRole("list", { name: "Deployment domains" })
  await expect(domains.getByText("Another site", { exact: true })).toBeVisible()
  await expect(domains.getByText("Certificate expired", { exact: true })).toBeVisible()
  await expect(domains.getByText("Served by legacy.conf")).toBeVisible()

  const mounts = page.getByRole("region", { name: "Persistent storage" })
  await expect(mounts.getByText("missing", { exact: true })).toBeVisible()

  // Keyboard-only handoff: the finding's own link opens the owning module.
  const storageLink = page.getByRole("link", { name: "Open docker" }).first()
  await storageLink.focus()
  await expect(storageLink).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/\/docker\/volumes\?volume=api-data$/)

  await page.goto("/deploy/7?tab=diagnostics")
  await page.getByRole("link", { name: "Open the serving site" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\?site=legacy\.conf$/)
  expect(dashboard.mutationCount()).toBe(0)
})

test("a healthy deployment reports nothing and an absent module never reads as success", async ({
  page,
}) => {
  const dashboard = await mockDashboard(page, { normalized: true })
  await page.goto("/deploy/7?tab=diagnostics")
  await expect(page.getByText("Nothing to report")).toBeVisible()
  await expect(
    page.getByText("Runtime, domains, storage, backups and dependencies were all read"),
  ).toBeVisible()
  await expect(page.getByText("Not assessed")).toHaveCount(0)
  const domains = page.getByRole("list", { name: "Deployment domains" })
  await expect(domains.getByText("Routed here", { exact: true })).toBeVisible()
  await expect(domains.getByText("Certificate valid", { exact: true })).toBeVisible()

  const degraded: DeploymentOperations = {
    observedAt: now,
    evidence: "none",
    reason: "This deployment has no live release.",
    runtime: {
      status: "unavailable",
      observedAt: now,
      reason: "Docker runtime evidence is unavailable. Open Docker to check the connection.",
      services: [],
    },
    domains: { status: "unavailable", reason: "No live release names them.", domains: [] },
    storage: { status: "unavailable", reason: "No live release names them.", mounts: [] },
    backups: { status: "unavailable", reason: "No live release names them.", jobs: [] },
    dependencies: { status: "unavailable", reason: "No live release names them.", items: [] },
    diagnosis: {
      status: "partial",
      findings: [],
      silences: [
        {
          subject: "runtime",
          reason:
            "This deployment has no live release, so no runtime, domain or storage claim can be made.",
        },
      ],
    },
  }
  await page.unrouteAll({ behavior: "ignoreErrors" })
  await mockDashboard(page, { normalized: true, operations: degraded })
  await page.goto("/deploy/7?tab=diagnostics")
  await expect(page.getByText("Domain evidence unavailable")).toBeVisible()
  await expect(page.getByText("Storage evidence unavailable")).toBeVisible()
  await expect(page.getByText("Backup evidence unavailable")).toBeVisible()
  await expect(page.getByText("No live release names them.").first()).toBeVisible()
  expect(dashboard.mutationCount()).toBe(0)
})

test("release comparison names what changed and never renders a variable value", async ({
  page,
}) => {
  const dashboard = await mockDashboard(page, { normalized: true })
  await page.goto("/deploy/7?tab=deployments")
  await expect(page.getByText("Release 19 compared with release 20")).toBeVisible()
  await expect(page.getByText("source revision")).toBeVisible()
  await expect(page.getByText("99887766554433221100 → a12bc34d56ef7890")).toBeVisible()
  await expect(page.getByText("3000 → 8080")).toBeVisible()
  // Unchanged fields stay out of the way: the panel answers "what changed".
  await expect(page.getByText("bun start → bun start")).toHaveCount(0)

  const variables = page.getByRole("region", { name: "Variables" })
  await expect(variables.getByText("API_TOKEN")).toBeVisible()
  await expect(variables.getByText("digest only")).toBeVisible()
  await expect(variables.getByText("111111111111 · secret → 222222222222 · secret")).toBeVisible()
  await expect(
    variables.getByText("Compared by value digest. No variable value is read to build this list."),
  ).toBeVisible()

  await expect(page.getByText("outdated", { exact: true })).toBeVisible()
  await expect(page.getByText("The upstream tag now points at a different image.")).toBeVisible()

  const artifacts = page.getByRole("region", { name: "Retained artifacts" })
  await expect(artifacts.getByText("example.test/api:v2")).toBeVisible()
  await expect(artifacts.getByText("retained", { exact: true })).toBeVisible()
  await expect(artifacts.getByText("retained for rollback to release 2")).toBeVisible()
  expect(dashboard.mutationCount()).toBe(0)
})

test("the game workspace sends commands, moderates players and edits only declared settings", async ({
  page,
}, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  const sent: string[] = []
  let written: Record<string, string> | null = null
  let players = {
    supported: true,
    status: "available",
    online: 1,
    maximum: 20,
    names: ["Notch"],
    observedAt: now,
  }
  await mockDashboard(page, { normalized: true })
  await page.route("**/api/v1/deploy/7/game**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/deploy/7/game") {
      return json(route, {
        status: "available",
        blueprintId: "minecraft-java",
        edition: "java",
        containerId: "mc123",
        address: "play.example.test:25565",
        console: true,
        players,
        files: [],
      })
    }
    if (path === "/deploy/7/game/players" && request.method() === "GET") {
      return json(route, players)
    }
    if (path === "/deploy/7/game/console") {
      const body = request.postDataJSON() as { command: string }
      sent.push(body.command)
      return json(route, {
        command: body.command,
        output: "There are 1 of a max of 20 players online: Notch",
        exitCode: 0,
        executedAt: now,
      })
    }
    if (path.startsWith("/deploy/7/game/players/")) {
      const action = path.split("/").at(-1)
      sent.push(`${action}:${(request.postDataJSON() as { name: string }).name}`)
      players = { ...players, online: 0, names: [] }
      return json(route, { command: action, output: "", exitCode: 0, executedAt: now })
    }
    if (path === "/deploy/7/game/properties" && request.method() === "GET") {
      return json(route, {
        status: "available",
        path: "/data/server.properties",
        raw: "#Minecraft server properties\nmotd=Old name\nmax-players=20\nexperimental=keep-me\n",
        values: { motd: "Old name", "max-players": "20", experimental: "keep-me" },
        restartRequired: true,
        known: [
          { key: "motd", kind: "text", label: "Server list description" },
          {
            key: "max-players",
            kind: "number",
            label: "Maximum players",
            minimum: 1,
            maximum: 1000,
          },
          {
            key: "difficulty",
            kind: "choice",
            label: "Difficulty",
            choices: [
              { value: "easy", label: "Easy" },
              { value: "normal", label: "Normal" },
            ],
          },
        ],
      })
    }
    if (path === "/deploy/7/game/properties" && request.method() === "PUT") {
      written = (request.postDataJSON() as { changes: Record<string, string> }).changes
      return json(route, { applied: Object.keys(written), restartRequired: true })
    }
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })
  // The workspace shows game tabs only for a game deployment.
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: { ...deployment, profile: "game", buildMethod: "image", activeRun: undefined },
    }),
  )

  await page.goto("/deploy/7?tab=console")
  await expect(page.getByRole("log", { name: "Console transcript" })).toBeVisible()
  const field = page.getByRole("textbox", { name: "Command" })
  await field.fill("list")
  await page.getByRole("button", { name: "Send" }).click()
  await expect(page.getByText("There are 1 of a max of 20 players online: Notch")).toBeVisible()
  expect(sent).toContain("list")
  // The previous command comes back with the up arrow rather than being retyped.
  await field.focus()
  await page.keyboard.press("ArrowUp")
  await expect(field).toHaveValue("list")
  await expect(page.getByText("Only plain game commands are accepted.")).toBeVisible()

  await page.goto("/deploy/7?tab=players")
  const list = page.getByRole("list", { name: "Online players" })
  await expect(list.getByText("Notch")).toBeVisible()
  await list.getByRole("button", { name: "Kick" }).click()
  expect(sent).toContain("kick:Notch")

  await page.goto("/deploy/7?tab=settings")
  await expect(page.getByLabel("Server list description")).toHaveValue("Old name")
  // A key the blueprint does not declare stays in the raw preview and gets no
  // control of its own.
  await page.getByText("View the file on disk", { exact: true }).click()
  await expect(page.getByText("experimental=keep-me")).toBeVisible()
  await expect(page.getByLabel("experimental")).toHaveCount(0)
  await page.getByLabel("Server list description").fill("New name")
  await expect(page.getByText("The server reads this file on start.")).toBeVisible()
  await page.getByRole("button", { name: /^Save/ }).click()
  await expect.poll(() => written).toEqual({ motd: "New name" })

  for (const width of [375, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7?tab=console")
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`game-console-${width}.png`) })
  }
})

test("importing an existing Minecraft server previews it before anything is copied", async ({
  page,
}) => {
  const journey = await mockWizardJourney(page)
  await page.route("**/api/v1/deploy/game/import/preview", async (route) => {
    const path = (route.request().postDataJSON() as { path: string }).path
    if (path !== "/srv/minecraft") {
      return route.fulfill({
        status: 400,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_import",
            message: "no Minecraft server was found in that directory",
          },
        }),
      })
    }
    return json(route, {
      root: "/srv/minecraft",
      edition: "java",
      software: "paper",
      version: "1.21.1",
      evidence: [{ path: "paper-1.21.1-42.jar", reason: "the jar name identifies a paper server" }],
      worldPaths: ["world", "world_nether"],
      modPaths: ["plugins"],
      configPaths: ["server.properties", "eula.txt"],
      logPaths: ["logs"],
      ignoredPaths: ["logs — regenerated by the server on every start"],
      properties: { motd: "Imported server", "max-players": "40", difficulty: "hard" },
      port: 25570,
      totalBytes: 1048576,
      eulaAccepted: true,
      warnings: ["More than one world directory was found."],
    })
  })

  await page.goto("/deploy/new?mode=advanced")
  await page.getByRole("textbox", { name: "Deployment name" }).fill("imported-survival")
  await page.getByText("Game server", { exact: true }).click()
  await page.getByRole("button", { name: "Continue" }).click()

  await page.getByRole("button", { name: "I already have a server on this machine" }).click()
  const pathField = page.getByRole("textbox", { name: "Server directory" })

  // A directory that holds no server says so instead of guessing.
  await pathField.fill("/srv/not-a-server")
  await page.getByRole("button", { name: "Inspect", exact: true }).click()
  await expect(page.getByText("That directory was not usable")).toBeVisible()

  await pathField.fill("/srv/minecraft")
  await page.getByRole("button", { name: "Inspect", exact: true }).click()
  await expect(page.getByText("paper", { exact: true })).toBeVisible()
  await expect(page.getByText("1.21.1", { exact: true })).toBeVisible()
  await expect(page.getByText("port 25570", { exact: true })).toBeVisible()
  await expect(page.getByText("EULA accepted", { exact: true })).toBeVisible()
  await expect(page.getByText("world, world_nether")).toBeVisible()
  // Runtime output is named as left behind rather than silently copied.
  await expect(page.getByText("logs — regenerated by the server on every start")).toBeVisible()
  // The reasoning is shown so the operator can disagree with it.
  await expect(page.getByText("the jar name identifies a paper server")).toBeVisible()
  await expect(page.getByText("More than one world directory was found.")).toBeVisible()

  // What it found fills the fields in, and nothing has been committed. The
  // imported version stays selected even though the upstream list no longer
  // offers it: importing a server is not upgrading it.
  await expect(page.getByRole("combobox", { name: "Minecraft version" })).toContainText("1.21.1")
  await expect(page.getByLabel("Maximum players")).toHaveValue("40")
  expect(journey.commits()).toBe(0)
})

test("quick deploy shows the actual HTTPS blocker instead of assuming Certbot is missing", async ({
  page,
}) => {
  await mockQuickDeploy(page)
  const reason = "Port 80 is already used by caddy; configure challenge routing in that web server."
  await page.route("**/api/v1/deploy/hostname**", (route) =>
    json(route, {
      hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
      covered: false,
      certificateIssue: reason,
      method: "sslip",
      detail: reason,
    }),
  )
  await page.goto("/deploy/new")
  await page.getByText("From GitHub", { exact: true }).click()
  await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
  await page.getByRole("button", { name: "Continue" }).click()
  await expect(page.getByText("Automatic HTTPS needs attention")).toBeVisible()
  await expect(page.getByText(reason, { exact: false }).last()).toBeVisible()
  await expect(page.getByText("Install certbot", { exact: false })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Certificates page" })).toBeVisible()
})

test("quick deploy keeps HTTPS automatic when Docker Caddy owns the public ports", async ({
  page,
}) => {
  await mockQuickDeploy(page)
  await page.route("**/api/v1/deploy/hostname**", (route) =>
    json(route, {
      hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
      covered: false,
      certificateMethod: "caddy",
      method: "sslip",
      detail: "The public Caddy ingress manages HTTPS automatically.",
    }),
  )
  await page.goto("/deploy/new")
  await page.getByText("From GitHub", { exact: true }).click()
  await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
  await page.getByRole("button", { name: "Continue" }).click()
  await expect(
    page.getByText("Caddy handles renewal automatically.", { exact: false }),
  ).toBeVisible()
  await expect(page.getByText("Automatic HTTPS needs attention")).toHaveCount(0)
  await expect(page.getByText("Install certbot", { exact: false })).toHaveCount(0)
})

test("deployment metrics show scoped live readings and history without leaving the page", async ({
  page,
}) => {
  const runtime: DeploymentRuntimeServices = {
    status: "available",
    observedAt: now,
    services: [
      {
        containerId: "metrics-web",
        name: "web",
        releaseId: 20,
        liveRelease: true,
        state: "running",
        health: "healthy",
        imageId: "sha256:web",
      },
      {
        containerId: "metrics-worker",
        name: "worker",
        releaseId: 20,
        liveRelease: true,
        state: "running",
        health: "healthy",
        imageId: "sha256:worker",
      },
    ],
  }
  await mockDashboard(page, { normalized: true, runtime })
  const requested: string[] = []
  await page.routeWebSocket(/\/docker\/containers\/.*\/stats\/stream/, (socket) => {
    const worker = socket.url().includes("metrics-worker")
    socket.send(
      JSON.stringify({
        type: "stats",
        data: {
          ts: now,
          cpuPercent: worker ? 37 : 12,
          memUsage: 104857600,
          memLimit: 0,
          memLimited: false,
          pids: worker ? 6 : 3,
        },
      }),
    )
  })
  await page.route("**/api/v1/docker/containers/**", (route) => {
    const path = new URL(route.request().url()).pathname
    requested.push(path)
    if (path.endsWith("/anomalies")) return json(route, { anomalies: [] })
    return json(route, { points: [], sampleIntervalSeconds: 15 })
  })
  await page.goto("/deploy/7?tab=metrics")
  await expect(page.getByText("12.0%", { exact: true })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Processor", exact: true })).toBeVisible()
  await page.getByRole("combobox", { name: "Metrics service" }).click()
  await page.getByRole("option", { name: "worker", exact: true }).click()
  await expect(page.getByText("37.0%", { exact: true })).toBeVisible()
  await expect
    .poll(() => requested.some((path) => path.includes("metrics-worker/stats/history")))
    .toBe(true)
  await expect(page).toHaveURL(/\/deploy\/7\?tab=metrics$/)
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await expect(page.getByRole("combobox", { name: "Metrics service" })).toBeVisible()
    const activeSection = page
      .getByRole("navigation", { name: "Deployment sections" })
      .getByRole("link", { name: "Metrics", exact: true })
    await expect
      .poll(async () => {
        const bounds = await activeSection.boundingBox()
        return Boolean(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width)
      })
      .toBe(true)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.screenshot({ path: `test-results/deployment-metrics-${width}.png`, fullPage: true })
  }
})

test("creation entry points select the matching workload defaults", async ({ page }) => {
  await mockWizardJourney(page)
  for (const [label, profile] of [
    ["Compose stack", "compose"],
    ["Application template", "service"],
    ["Game server", "game"],
    ["Worker or bot", "worker"],
    ["Static website", "static"],
    ["Existing workload", "imported"],
  ]) {
    await page.goto("/deploy/new")
    await page.getByText(label, { exact: true }).click()
    await expect(page.locator(`input[name="profile"][value="${profile}"]`)).toBeChecked()
  }
})

test("creation choices fit mobile and desktop and name errors appear beside the input", async ({
  page,
}) => {
  await mockQuickDeploy(page)
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await page.goto("/deploy/new")
    await expect(
      page.getByRole("heading", { name: "Start with something ready", exact: true }),
    ).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.screenshot({ path: `test-results/deployment-create-${width}.png`, fullPage: true })
  }
  await page.getByText("From GitHub", { exact: true }).click()
  await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  const name = page.getByRole("textbox", { name: "Name", exact: true })
  await name.fill("invalid name")
  await name.blur()
  await expect(name).toHaveAttribute("aria-invalid", "true")
  await expect(page.getByText("Start with a letter or number.", { exact: false })).toBeVisible()
  await name.fill("valid-name")
  await expect(name).toHaveAttribute("aria-invalid", "false")
})

test("archived deployments can be permanently deleted with confirmation and errors remain reviewable", async ({
  page,
}) => {
  await mockDashboard(page)
  let deleted = false
  let refuse = true
  let calls = 0
  await page.route("**/api/v1/deploy/?view=archived", (route) =>
    json(route, deleted ? [] : [{ id: 7, name: "retired-api", archivedAt: now }]),
  )
  await page.route("**/api/v1/deploy/7/permanent", (route) => {
    calls++
    if (refuse)
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "already_running", message: "A deployment run is still active" },
        }),
      })
    deleted = true
    return route.fulfill({ status: 204 })
  })
  await page.goto("/deploy")
  await page.getByRole("link", { name: "Archived", exact: true }).click()
  await expect(page.getByRole("link", { name: "retired-api" })).toBeVisible()
  await page.getByRole("button", { name: "Delete permanently" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("persistent data remain on the server")
  await dialog.getByRole("button", { name: "Cancel" }).click()
  expect(calls).toBe(0)
  await page.getByRole("button", { name: "Delete permanently" }).click()
  await dialog.getByRole("button", { name: "Delete permanently" }).click()
  await expect(page.getByText("A deployment run is still active", { exact: true })).toBeVisible()
  await expect(dialog).toBeVisible()
  refuse = false
  await dialog.getByRole("button", { name: "Delete permanently" }).click()
  await expect(page.getByText("No archived deployments", { exact: true })).toBeVisible()
  expect(calls).toBe(2)
})

test("redesign connects a new database before deploying without leaving setup", async ({
  page,
}, testInfo) => {
  const quick = await mockQuickDeploy(page)
  let provisioned = 0
  const connection = {
    id: 42,
    name: "project-postgres",
    driver: "postgres",
    host: "127.0.0.1",
    port: "5432",
    user: "jd",
    database: "app",
    createdAt: now,
  }
  const connectionURL = "postgres://jd:setup-secret@172.17.0.4:5432/app?sslmode=disable"
  await page.route("**/api/v1/databases/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace("/api/v1", "")
    if (path === "/databases/provision/options")
      return json(route, [
        {
          engine: "postgres",
          label: "PostgreSQL 16",
          image: "postgres:16-alpine",
          driver: "postgres",
        },
      ])
    if (path === "/databases/provision") {
      provisioned += 1
      return json(route, { container: "project-postgres" })
    }
    if (path === "/databases/adopt") return json(route, connection)
    if (path.endsWith("/ping")) return json(route, { ok: true })
    if (path.endsWith("/url")) {
      expect(url.searchParams.get("target")).toBe("container")
      return json(route, { url: connectionURL })
    }
    return json(route, [connection])
  })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/deploy/new")
  await expect(page.getByRole("heading", { name: "Import Git repository" })).toBeVisible()
  await expect(page.getByText("Wayy01/wesmokefish", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: /Compose stack/ })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("create-desktop.png"), fullPage: true })
  await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await page.getByRole("textbox", { name: "Key", exact: true }).fill("API_KEY")
  await page.locator("#env-value-0").fill("another-secret")
  await page.getByRole("button", { name: "Add database", exact: true }).click()
  await page.getByText("PostgreSQL 16", { exact: true }).click()
  await page.getByRole("button", { name: "Create database", exact: true }).click()
  await expect(page.getByRole("heading", { name: "project-postgres is ready" })).toBeVisible()
  await expect(page.locator("#database-connection-string")).toHaveAttribute("type", "password")
  await page.getByRole("button", { name: "Use this database" }).click()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(page.locator("#env-key-1")).toHaveValue("DATABASE_URL")
  await expect(page.locator("#env-value-1")).toHaveValue(connectionURL)
  await page.screenshot({ path: testInfo.outputPath("configure-desktop.png"), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath("configure-mobile.png"), fullPage: true })
  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    "setup-secret",
  )
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  expect(quick.configuration()).toMatchObject({
    dependencies: [
      {
        kind: "database",
        ownership: "linked",
        resourceKind: "database_connection",
        resourceId: "42",
      },
    ],
  })
  expect(quick.imported()?.dotenv).toContain(connectionURL)
  expect(quick.imported()?.dotenv).toContain('API_KEY="another-secret"')
  expect(provisioned).toBe(1)
  expect(quick.commits()).toBe(1)
})

test("redesign overview and build logs stay focused across screen sizes", async ({
  page,
}, testInfo) => {
  await mockDashboard(page, { normalized: true })
  await page.route("**/api/v1/deploy/7/preview-frame", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: '<!doctype html><html><body style="margin:0;background:#f8fafc;color:#16283e;font:16px system-ui;padding:36px"><p style="font-size:12px;letter-spacing:2px">API EXAMPLE · PREVIEW FIXTURE</p><h1 style="font-size:34px;font-weight:600">Build something useful.</h1><p>Your application preview appears here.</p><hr style="border:0;border-top:1px solid #ccd7e4;margin:28px 0"><p style="font-size:13px">Documentation &nbsp; / &nbsp; API reference &nbsp; / &nbsp; Status</p></body></html>',
    }),
  )
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/deploy/7")
  await expect(
    page.getByRole("heading", { name: "Production deployment", exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("heading", { name: "Recent deployments" })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Runtime configuration" })).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Release path" })).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath("overview-desktop.png"), fullPage: true })
  await page
    .getByRole("navigation", { name: "Deployment sections" })
    .getByRole("link", { name: "Settings", exact: true })
    .click()
  await page.getByRole("link", { name: "Environment variables", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Variable name" })).toHaveCount(0)
  await page.getByRole("button", { name: "Add variable", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "Add or update a variable" })).toBeVisible()
  await page.keyboard.press("Escape")
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/deploy/7")
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath("overview-mobile.png"), fullPage: true })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByRole("heading", { name: "Build logs", exact: true })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Steps", exact: true })).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Application runtime logs" })).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath("build-mobile.png"), fullPage: true })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: testInfo.outputPath("build-desktop.png"), fullPage: true })
  await page.getByRole("button", { name: "Execution details", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Execution details", exact: true })).toBeVisible()
  await page.getByRole("button", { name: /Verify Smoke/ }).click()
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toHaveText("Verify Smoke")
})

test("build transcript formats chunks, filters lines and shows only its live release URL", async ({
  page,
}, testInfo) => {
  await mockDashboard(page, { normalized: true })
  let completed = false
  let superseded = false
  let finish: (() => void) | undefined
  const snapshot = () => ({
    run: {
      ...run,
      state: completed ? "succeeded" : "verifying",
      releaseId: completed ? 21 : undefined,
    },
    steps,
  })
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot()))
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      deployment: { ...deployment, liveReleaseId: superseded ? 22 : completed ? 21 : 20 },
    }),
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
    const event = {
      seq: 20,
      type: "step.log",
      runId: 84,
      stepId: 105,
      ts: now,
      data: {
        stream: "stdout",
        text: "Installing dependencies\r\n\u001b[33mWarning: optional cache unavailable\u001b[0m\nCache restore failed; continuing\nBuild complete\n",
        truncated: false,
      },
    }
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          event,
          event,
          { ...event, seq: 21, stepId: 110, data: { ...event.data, text: "GET /health 200\n" } },
        ],
        ts: Date.now(),
      }),
    )
    finish = () => {
      completed = true
      socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
    }
  })
  await page.setViewportSize({ width: 1440, height: 1050 })
  await page.goto("/deploy/7/runs/84")
  const transcript = page.getByRole("list", { name: "Deployment transcript" })
  await expect(transcript.getByRole("listitem")).toHaveCount(5)
  await expect(transcript).not.toContainText("\u001b")
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toHaveText("All stages")
  await page.getByRole("textbox", { name: "Search build logs" }).fill("warning")
  await expect(transcript.getByRole("listitem")).toHaveCount(1)
  await page.getByRole("textbox", { name: "Search build logs" }).fill("")
  await page.getByRole("button", { name: "Errors", exact: true }).click()
  await expect(transcript.getByRole("listitem")).toHaveCount(1)
  await expect(transcript).toContainText("Cache restore failed; continuing")
  await page.getByRole("button", { name: "Errors", exact: true }).click()
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"])
  await page.getByRole("button", { name: "Copy build logs" }).click()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied.split("\n")).toHaveLength(5)
  finish!()
  await expect(page.getByText("Your release is ready", { exact: true })).toBeVisible({
    timeout: 10000,
  })
  await expect(page.getByRole("link", { name: "Visit", exact: true })).toHaveAttribute(
    "href",
    "https://api.example.test/",
  )
  await page.screenshot({
    path: testInfo.outputPath("completed-build-desktop.png"),
    fullPage: true,
  })
  await page.setViewportSize({ width: 390, height: 1000 })
  await expect(transcript).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath("completed-build-mobile.png"), fullPage: true })
  superseded = true
  await page.reload()
  await expect(page.getByText("This run finished successfully", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Visit", exact: true })).toHaveCount(0)
})

test("database setup retries the created container and reports connection errors immediately", async ({
  page,
}) => {
  await mockQuickDeploy(page)
  let provisioned = 0
  let failAddress = true
  await page.route("**/api/v1/databases/**", (route) => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith("/provision/options"))
      return json(route, [{ engine: "postgres", label: "PostgreSQL", image: "postgres:17" }])
    if (path.endsWith("/provision")) {
      provisioned++
      return json(route, { container: "started-db" })
    }
    if (path.endsWith("/adopt"))
      return json(route, { id: 42, name: "started-db", driver: "postgres" })
    if (path.endsWith("/ping")) return json(route, { ok: true })
    if (path.endsWith("/url"))
      return failAddress
        ? route.fulfill({
            status: 400,
            contentType: "application/json",
            body: JSON.stringify({
              error: { code: "bad_request", message: "Database needs a shared network" },
            }),
          })
        : json(route, { url: "postgres://app:secret@172.17.0.4:5432/app" })
    return json(route, [])
  })
  await page.goto("/deploy/new")
  await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await page.getByRole("button", { name: "Add database" }).click()
  await page.getByRole("button", { name: /PostgreSQL/ }).click()
  await page.getByRole("button", { name: "Create database", exact: true }).click()
  await expect(page.getByText("Database needs a shared network", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.getByText(/Database started-db has started/)).toBeVisible()
  failAddress = false
  await page.getByRole("button", { name: "Add database" }).click()
  await page.getByRole("button", { name: "Retry connection setup", exact: true }).click()
  await page.getByRole("button", { name: "Use this database", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Key", exact: true }).last()).toHaveValue(
    "DATABASE_URL",
  )
  expect(provisioned).toBe(1)
})

test("manual Git import keeps branch and credentials and supports a Dockerfile worker", async ({
  page,
}) => {
  const quick = await mockQuickDeploy(page)
  let selectedSource: unknown
  page.on("request", (request) => {
    if (request.method() !== "PUT" || !request.url().endsWith("/deploy/drafts/journey-draft"))
      return
    const body = request.postDataJSON()
    if (body.step === "source") selectedSource = body.source
  })
  await page.goto("/deploy/new")
  await page
    .getByRole("textbox", { name: "Clone URL", exact: true })
    .fill("https://git.example.test/team/service.git")
  await page.getByText("Branch & authentication", { exact: true }).click()
  await page.getByRole("textbox", { name: "Branch or tag", exact: true }).fill("release/next")
  await page.getByRole("spinbutton", { name: "Saved credential ID", exact: true }).fill("12")
  await page.getByRole("button", { name: "Import", exact: true }).click()
  await page.getByRole("combobox", { name: "Project type", exact: true }).click()
  await page.getByRole("option", { name: "Worker or bot", exact: true }).click()
  await page.getByText(/Build settings ·/).click()
  await page.getByRole("combobox", { name: "Build method", exact: true }).click()
  await page.getByRole("option", { name: "Dockerfile", exact: true }).click()
  await page
    .getByRole("textbox", { name: "Dockerfile path", exact: true })
    .fill("deploy/worker.Dockerfile")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  expect(selectedSource).toMatchObject({
    kind: "git",
    mode: "git_url",
    url: "https://git.example.test/team/service.git",
    ref: "release/next",
    credentialId: 12,
  })
  expect(quick.configuration()).toMatchObject({
    build: { method: "dockerfile", dockerfile: "deploy/worker.Dockerfile" },
    runtime: { internalPort: 0 },
    domains: [],
  })
})

test("focused settings keep build configuration and named dependencies in separate destinations", async ({
  page,
}, testInfo) => {
  await mockDashboard(page, { normalized: true })
  await page.route("**/api/v1/databases/", (route) =>
    json(route, [{ id: 42, name: "Production Postgres", driver: "postgres" }]),
  )
  await page.route("**/api/v1/backups/", (route) =>
    json(route, [{ id: 4, name: "Nightly snapshot" }]),
  )
  const changes: Record<string, unknown>[] = []
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith("/configuration"))
      changes.push(request.postDataJSON())
  })
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.goto("/deploy/7?tab=configuration")
  await expect(page.getByRole("heading", { name: "Build settings", exact: true })).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "Runtime configuration", exact: true }),
  ).toHaveCount(0)
  await page.getByRole("textbox", { name: "Build command", exact: true }).fill("bun run build")
  await page.getByRole("button", { name: "Save build settings", exact: true }).click()
  await expect.poll(() => changes.length).toBe(1)
  expect(changes[0].build).toMatchObject({ buildCommand: "bun run build" })
  expect(changes[0].dependencies).toMatchObject([{ kind: "backup", resourceId: "4" }])
  await page.screenshot({ path: testInfo.outputPath("build-settings-desktop.png"), fullPage: true })
  await page.getByRole("link", { name: "Databases & backups", exact: true }).click()
  await expect(page.getByRole("combobox", { name: "Backup job", exact: true })).toHaveText(
    "Nightly snapshot",
  )
  await page.getByRole("button", { name: "Link database", exact: true }).click()
  await page.getByRole("combobox", { name: "Database", exact: true }).click()
  await page.getByRole("option", { name: "Production Postgres", exact: true }).click()
  await page.getByRole("button", { name: "Save dependencies", exact: true }).click()
  await expect.poll(() => changes.length).toBe(2)
  expect(changes[1].dependencies).toMatchObject([
    { kind: "backup", resourceId: "4" },
    {
      kind: "database",
      resourceKind: "database_connection",
      resourceId: "42",
      ownership: "linked",
    },
  ])
  await expect(page.getByRole("heading", { name: "Persistent mounts", exact: true })).toHaveCount(0)
  await page.setViewportSize({ width: 390, height: 950 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath("dependencies-mobile.png"), fullPage: true })
})

test("switching a Git project to a static website retains its build recipe", async ({ page }) => {
  const quick = await mockQuickDeploy(page)
  await page.goto("/deploy/new")
  await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await page.getByRole("combobox", { name: "Project type", exact: true }).click()
  await page.getByRole("option", { name: "Static website", exact: true }).click()
  await expect(page.getByRole("combobox", { name: "Build method", exact: true })).toHaveText(
    "Automatic recipe",
  )
  await page.getByRole("textbox", { name: "Static output directory", exact: true }).fill("out")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  expect(quick.configuration()).toMatchObject({
    build: { method: "recipe", recipe: "node", outputDirectory: "out" },
    runtime: { internalPort: 80 },
  })
  expect((quick.configuration()?.build as Record<string, unknown>).startCommand).toBeUndefined()
})

test("existing database setup uses a private application URL without provisioning another server", async ({
  page,
}) => {
  const quick = await mockQuickDeploy(page)
  let provisions = 0
  const connection = { id: 42, name: "Production Postgres", driver: "postgres" }
  await page.route("**/api/v1/databases/**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith("/provision")) provisions++
    if (url.pathname.endsWith("/url")) {
      expect(url.searchParams.get("target")).toBe("container")
      return json(route, { url: "postgres://app:existing-secret@172.17.0.4:5432/app" })
    }
    return json(
      route,
      url.pathname.endsWith("/provision/options")
        ? []
        : [connection, { id: 43, name: "Local SQLite", driver: "sqlite" }],
    )
  })
  await page.goto("/deploy/new")
  await page.getByText("Wayy01/wesmokefish", { exact: true }).click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await page.getByRole("button", { name: "Add database" }).click()
  await page.getByRole("button", { name: "Use existing", exact: true }).click()
  await page.getByRole("combobox", { name: "Existing database" }).click()
  await expect(page.getByRole("option", { name: /Local SQLite/ })).toHaveCount(0)
  await page.getByRole("option", { name: /Production Postgres/ }).click()
  await page.getByRole("button", { name: "Connect database", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Key", exact: true }).last()).toHaveValue(
    "DATABASE_URL",
  )
  await expect(page.getByLabel("Value", { exact: true }).last()).toHaveAttribute("type", "password")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  expect(provisions).toBe(0)
  expect(quick.imported()?.dotenv).toContain("existing-secret@172.17.0.4")
  expect(quick.configuration()?.dependencies).toMatchObject([
    { kind: "database", resourceId: "42" },
  ])
})

test("unavailable blueprints explain their status and cannot create a source plan", async ({
  page,
}) => {
  await mockWizardJourney(page)
  const reason = "Blueprint deployment is unavailable until runtime support is complete."
  let sourceWrites = 0
  page.on("request", (request) => {
    if (
      request.method() === "PUT" &&
      request.url().includes("/deploy/drafts/") &&
      request.postDataJSON()?.step === "source"
    )
      sourceWrites++
  })
  await page.route("**/api/v1/deploy/blueprints/", (route) =>
    json(
      route,
      blueprintCatalogue.map((entry) => ({
        ...entry,
        deploymentSupported: false,
        unavailableReason: reason,
      })),
    ),
  )
  await page.goto("/deploy/new?mode=advanced")
  await page.getByRole("textbox", { name: "Deployment name" }).fill("unavailable-game")
  await page.getByText("Game server", { exact: true }).click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByRole("radio", { name: /Minecraft \(Java Edition\)/ })).toBeDisabled()
  await expect(page.getByText(reason).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Inspect source", exact: true })).toBeDisabled()
  expect(sourceWrites).toBe(0)
})
