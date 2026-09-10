import { expect, test, type Page, type Route } from "@playwright/test"
import type { DeploymentRuntimeServices } from "../../src/lib/types"

const now = "2026-09-03T12:00:00Z"

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

async function mockDashboard(
  page: Page,
  options: { normalized?: boolean; runtime?: DeploymentRuntimeServices } = {},
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
    await page.goto("/deploy/7")
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
    if (width === 375 || width === 1440) {
      await list.screenshot({ path: testInfo.outputPath(`runtime-${width}-dark.png`) })
      await page.getByRole("button", { name: "Switch to light" }).click()
      await list.screenshot({ path: testInfo.outputPath(`runtime-${width}-light.png`) })
      await page.getByRole("button", { name: "Switch to dark" }).click()
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

  await page.goto("/deploy/7")
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
  await page.goto("/deploy/7")
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
  const link = page.getByRole("link", { name: "Activation logs for preview-web" })
  await expect(link).toHaveAttribute("href", `/logs?${query}`)
  await link.focus()
  await page.keyboard.press("Enter")
  await expect.poll(() => searches.length).toBe(1)
  expect(searches[0].searchParams.get("source")).toBe("docker:preview")
  expect(searches[0].searchParams.get("since")).toBe(since)
  expect(searches[0].searchParams.get("until")).toBe(until)
  activated = false
  await page.goto("/deploy/7/runs/84")
  await expect(
    page.getByText("This run has no completed activation evidence.", { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("link", { name: "Activation logs for preview-web" })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Live logs for preview-web" })).toBeVisible()
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
  await page.goto("/deploy/new")

  await expect(page.getByRole("heading", { name: "Deploy something" })).toBeVisible()
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
  await page.goto("/deploy/new")
  await page.getByRole("textbox", { name: "Deployment name" }).fill("public-web")
  await page.getByRole("button", { name: "Continue" }).click()
  await expect(page.getByRole("heading", { name: "Connect the source" })).toBeVisible()
  await page.getByRole("textbox", { name: "Git URL" }).fill("https://example.test/public/web.git")
  await page.getByRole("button", { name: "Inspect source" }).click()
  await expect(page.getByRole("heading", { name: "Review evidence" })).toBeVisible()
  await page.getByRole("button", { name: "Use this detection" }).click()
  await expect(page.getByRole("heading", { name: "Set runtime decisions" })).toBeVisible()
  await page.getByRole("textbox", { name: "Public domain" }).fill("web.example.test")
  await expect(page.getByRole("combobox", { name: "Automatic recipe" })).toContainText("Node.js")
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
  await page.goto("/deploy/new")
  await page.getByRole("textbox", { name: "Deployment name" }).fill("minecraft-family")
  await page.getByText("Game server", { exact: true }).click()
  await page.getByRole("button", { name: "Continue" }).click()
  await expect(page.getByRole("textbox", { name: "Image" })).toHaveValue(
    "itzg/minecraft-server:java21",
  )
  await page.getByRole("button", { name: "Inspect source" }).click()
  await page.getByRole("button", { name: "Use this detection" }).click()
  await expect(page.getByRole("spinbutton", { name: "Application port" })).toHaveValue("25565")
  const eula = page.getByRole("checkbox", { name: /I accept the Minecraft EULA/ })
  await expect(eula).not.toBeChecked()
  await eula.check()
  await expect(page.getByRole("button", { name: /Advanced/ })).toHaveAttribute(
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
  await expect(page.getByRole("button", { name: "Redeploy live" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Restart" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Force build" })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Immutable releases" })).toBeVisible()
  await expect(page.getByText("Release #2", { exact: true })).toBeVisible()
  await expect(page.getByText("Live", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Redeploy live" }).click()
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

  await expect(page.getByRole("heading", { name: "Scoped variables" })).toBeVisible()
  await expect(page.getByText("••••••••")).toBeVisible()
  await expect(page.getByText("revealed-browser-secret")).toHaveCount(0)
  await page.getByRole("textbox", { name: "Variable name" }).fill("DATABASE_TOKEN")
  await page.getByLabel("Value").fill("browser-only-input-secret")
  await page.getByRole("button", { name: "Save variable" }).click()
  await expect(page.getByText("DATABASE_TOKEN", { exact: true })).toBeVisible()
  await expect(page.getByText("browser-only-input-secret")).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Pending deployment" }).first()).toBeVisible()

  const apiTokenRow = page.getByRole("listitem").filter({ hasText: "API_TOKEN" })
  await apiTokenRow.getByRole("button", { name: "Rotate" }).click()
  await expect(page.getByText("rotated-browser-secret")).toBeVisible()

  await page.getByRole("link", { name: /Domains & ports/ }).click()
  await page.getByRole("button", { name: "Add domain" }).click()
  await page.getByRole("textbox", { name: "Domain 2" }).fill("next.example.test")
  await page.getByRole("button", { name: "Save network plan" }).click()
  await expect(page.getByText("Certificate required")).toBeVisible()

  await page.getByRole("link", { name: /Configuration/ }).click()
  await page.getByRole("button", { name: "Preview managed targets" }).click()
  await expect(page.getByText("api-data", { exact: true })).toBeVisible()
  await expect(page.getByText(/typed confirmation/)).toBeVisible()

  await page.getByRole("button", { name: "Deploy changes" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/88$/)
  await page.goBack()
  await expect(page.getByRole("heading", { name: "Desired plan is live" })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )

  await page.setViewportSize({ width: 667, height: 375 })
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" })
  await expect(page.getByRole("heading", { name: "Desired plan is live" })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" })
  await expect(page.getByRole("heading", { name: "Runtime configuration" })).toBeVisible()
})

test("automation workspace creates provider, schedule, preview and signed notification policy", async ({
  page,
}) => {
  await mockDashboard(page, { normalized: true })
  await page.setViewportSize({ width: 375, height: 900 })
  await page.goto("/deploy/7?tab=automations")
  await expect(page.getByRole("heading", { name: "Source automations" })).toBeVisible()
  await expect(page.getByText("pr-42", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Add automation" }).click()
  await page.getByLabel("Repository").fill("acme/api")
  await page.getByLabel("Watched paths").fill("services/api/**")
  await page.getByRole("button", { name: "Create automation" }).click()
  await expect(page.getByText("one-time-provider-secret", { exact: true })).toBeVisible()
  await expect(page.getByText(/acme\/api/)).toBeVisible()

  await page.getByRole("button", { name: "Add", exact: true }).click()
  await page.getByLabel("Cron expression").fill("30 2 * * *")
  await page.getByLabel("IANA timezone").fill("America/New_York")
  await page.getByRole("button", { name: "Create schedule" }).click()
  await expect(page.getByText(/America\/New_York/)).toBeVisible()

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

  await expect(page.getByRole("heading", { name: "Deployment #84" })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Verify Readiness" })).toBeVisible()
  await expect(page.getByText("Active", { exact: true }).first()).toBeVisible()
  await expect
    .poll(() =>
      page
        .locator('[data-slot="page-header"] svg.animate-spin')
        .evaluate((icon) => getComputedStyle(icon).animationName),
    )
    .toBe("none")
  const smoke = page.getByRole("button", { name: /Verify Smoke/ })
  await smoke.focus()
  await page.keyboard.press("Enter")
  await expect(smoke).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("heading", { name: "Verify Smoke" })).toBeVisible()
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
