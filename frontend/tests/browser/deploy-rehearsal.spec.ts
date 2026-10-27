import { expect, test, type Page } from "@playwright/test"
import {
  expectSaved,
  json,
  mockProject,
  now,
  run as fixtureRun,
  saveSettings,
  steps as fixtureSteps,
  user,
} from "./deploy-fixture"
import type {
  DeploymentEngineRun,
  DeploymentStep,
  DeploymentVariable,
  RehearsalReport,
} from "../../src/lib/types"

/**
 * Migration rehearsal (`deploy/rehearsal.go`): its terms in Build settings,
 * beside the release tasks they rehearse, and its report on the run page —
 * a passing rehearsal read as what was restored, how the tasks went and how
 * long they held a table, a failing one as the reason the release stopped
 * before production.
 */

test.use({ timezoneId: "UTC" })

const run = fixtureRun as DeploymentEngineRun
const baseSteps = fixtureSteps as unknown as DeploymentStep[]

const passed: RehearsalReport = {
  rehearsed: true,
  database: { connectionId: 5, name: "orders-db", engine: "postgres", database: "app", image: "" },
  source: { kind: "dump", backupRunId: 41, takenAt: "2026-09-03T02:15:00Z", bytes: 48_213 },
  restoreMs: 4423,
  tasks: [{ name: "migrate", durationMs: 3401, exitCode: 0 }],
  locks: {
    read: true,
    samples: 14,
    maxWaiting: 0,
    waitedMs: 0,
    // An ALTER TABLE and the CREATE INDEX in its transaction: one table, two modes.
    held: [
      { relation: "items", mode: "ShareLock", heldMs: 2003 },
      { relation: "items", mode: "AccessExclusiveLock", heldMs: 2003 },
    ],
  },
  isolation: {
    network: "jd-e12-run84-rehearsal",
    internal: true,
    published: false,
    volumesMounted: false,
  },
  cleanedUp: true,
  totalMs: 7900,
}

const failedMessage =
  'Release task "migrate" failed against a copy of orders-db restored from the backup of 2026-09-03 02:15 UTC (exit code 1), so the release stopped before production was touched: ERROR:  column "name" of relation "items" contains null values'

/** The run's steps with the rehearsal between the backup check and the release task. */
function withRehearsal(
  state: DeploymentStep["state"],
  report: RehearsalReport,
  error?: { code: string; message: string },
) {
  const out: DeploymentStep[] = []
  for (const step of baseSteps) {
    if (step.key === "release_task") {
      out.push({
        ...step,
        id: 120,
        key: "rehearse_migration",
        state,
        endedAt: "2026-09-03T12:00:08Z",
        evidence: report as unknown as Record<string, unknown>,
        errorCode: error?.code,
        errorMessage: error?.message,
      })
      out.push(error ? { ...step, state: "pending", startedAt: undefined } : step)
      continue
    }
    out.push(error && step.ordinal > 7 ? { ...step, state: "pending", startedAt: undefined } : step)
  }
  return out
}

async function openRun(
  page: Page,
  overrides: Partial<DeploymentEngineRun>,
  steps: DeploymentStep[],
) {
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({
        type: "snapshot",
        data: { run: { ...run, ...overrides }, steps },
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")
}

test("a passing rehearsal reads as the copy, the tasks and the lock they held", async ({
  page,
}) => {
  await mockProject(page)
  await openRun(page, {}, withRehearsal("passed", passed))

  const list = page.getByRole("list", { name: "Deployment steps" })
  const step = list.getByRole("button", { name: /Migration rehearsal/ })
  // Its row says what it concluded while another step is open.
  await expect(step).toContainText("orders-db · migrate passed · items locked 2.0 s")
  // It is part of the release stage, between the backup check and the task.
  const release = list.getByRole("list", { name: "Release", exact: true })
  await expect(release.getByRole("button", { name: /Migration rehearsal/ })).toBeVisible()

  await step.click()
  const record = page.getByRole("region", { name: "Migration rehearsal" })
  const report = record.getByRole("region", { name: "Rehearsal report" })
  await expect(report.getByText("4.4 s", { exact: true })).toBeVisible()
  await expect(report.getByText("dump of 02:15", { exact: true })).toBeVisible()
  await expect(report.getByText("Passed", { exact: true })).toBeVisible()
  // The longest lock, as a figure, and again in the line that says what it means.
  await expect(report.getByText("2.0 s", { exact: true })).toHaveCount(2)
  await expect(report.getByText("ACCESS EXCLUSIVE", { exact: true }).first()).toBeVisible()
  await expect(report.getByText("Removed", { exact: true })).toBeVisible()
  // The weaker lock the ALTER's covered is not read twice.
  const locks = report.getByRole("list", { name: "Locks held against the copy" })
  await expect(locks.getByRole("listitem")).toHaveCount(1)
  await expect(locks).toContainText("ACCESS EXCLUSIVE on items for about 2.0 s")
  await expect(locks).toContainText(
    "production's reads and writes of items will wait about as long",
  )
  await expect(report.getByText(/an internal network with no way out/)).toBeVisible()
  // The report's own fields are not listed a second time as raw facts.
  await expect(record.getByText("Restore ms", { exact: true })).toHaveCount(0)
})

test("a failing rehearsal says the release stopped before production", async ({ page }) => {
  await mockProject(page)
  const failed: RehearsalReport = {
    ...passed,
    tasks: [
      {
        name: "migrate",
        durationMs: 1168,
        exitCode: 1,
        error: 'ERROR:  column "name" of relation "items" contains null values',
      },
    ],
    locks: { ...passed.locks, held: [] },
  }
  await openRun(
    page,
    {
      state: "failed",
      endedAt: now,
      terminalCode: "rehearsal_failed",
      terminalReason: failedMessage,
    },
    withRehearsal("failed", failed, { code: "rehearsal_failed", message: failedMessage }),
  )

  await expect(page.getByText("Migration failed on a copy of the database").first()).toBeVisible()
  const list = page.getByRole("list", { name: "Deployment steps" })
  const step = list.getByRole("button", { name: /Migration rehearsal/ })
  await step.click()
  const record = page.getByRole("region", { name: "Migration rehearsal" })
  await expect(record.getByText("rehearsal_failed", { exact: true })).toBeVisible()
  const report = record.getByRole("region", { name: "Rehearsal report" })
  await expect(report.getByText("Failed", { exact: true })).toBeVisible()
  await expect(report.getByText("exit 1", { exact: true })).toHaveCount(2)
  await expect(report.getByRole("list", { name: "Release tasks against the copy" })).toContainText(
    "contains null values",
  )
  // The release task itself never ran.
  await expect(list.getByRole("button", { name: /^Release task/ })).toContainText("Waiting")
})

const databaseVariable = (connectionId: number, name: string): DeploymentVariable => ({
  name,
  revision: 1,
  sensitivity: "secret",
  scopes: ["runtime", "release_task"],
  masked: "postgres://…",
  valueDigest: `sha256:${String(connectionId).repeat(8)}`,
  reference: { kind: "database", target: `${connectionId}.url` },
  pending: false,
  createdBy: "operator",
  createdAt: now,
  environmentId: 12,
  desiredRevision: 3,
})

type ReleaseTask = {
  name: string
  command: string
  timeoutSeconds: number
  env: string[]
  runner?: "image"
}

const migrate: ReleaseTask = {
  name: "migrate",
  command: "bun run db:migrate",
  timeoutSeconds: 300,
  env: ["DATABASE_URL"],
  runner: "image",
}

/** The Build page over a plan whose release tasks reach `databases`. */
async function openBuild(page: Page, databases: number[], tasks: ReleaseTask[] = [migrate]) {
  await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, {
      build: {
        method: "recipe",
        recipe: "node",
        packageManager: "bun",
        buildCommand: "bun run build",
        startCommand: "bun start",
        secrets: [],
        releaseTasks: tasks,
      },
      runtime: {
        image: "",
        command: ["bun", "start"],
        internalPort: 3000,
        hostPort: 0,
        bindAddress: "127.0.0.1",
        strategy: "blue_green",
        mounts: [],
      },
      dependencies: databases.map((id) => ({
        kind: "database",
        ownership: "linked",
        resourceKind: "database_connection",
        resourceId: String(id),
      })),
      checks: [],
      domains: [{ hostname: "api.example.test", https: true, ownership: "managed" }],
      revision: 3,
      variables: databases.map((id, index) =>
        databaseVariable(id, index === 0 ? "DATABASE_URL" : `DATABASE_${id}_URL`),
      ),
      pending: { pending: false, desiredRevision: 3, changes: [] },
    })
  })
  // The saved connections name them; the environment's links exist only once
  // a release has joined their networks.
  await page.route("**/api/v1/databases/", (route) =>
    json(
      route,
      databases.map((id) => ({
        id,
        name: id === 5 ? "orders-db" : "analytics-db",
        driver: "postgres",
        host: `db-${id}.jd.internal`,
        port: "5432",
        user: "app",
        database: "app",
        createdAt: now,
        environment: "production",
        readOnly: false,
      })),
    ),
  )
  await page.goto("/deploy/7/settings/build")
  return page.getByRole("form", { name: "Migration rehearsal" })
}

const turnedOn = {
  enabled: true,
  connectionId: 0,
  whenUnavailable: "block" as const,
  restoreTimeoutSeconds: 600,
  maxBackupMb: 2048,
}

function rehearsalSaved(page: Page) {
  return page.waitForRequest(
    (request) =>
      request.url().endsWith("/deploy/7/environments/12/rehearsal") && request.method() === "PUT",
  )
}

test("the rehearsal terms are saved apart from the plan, from the Build page", async ({ page }) => {
  await mockProject(page)
  const form = await openBuild(page, [5])
  await expect(form).toContainText("Off · release tasks run against production directly")

  const saved = rehearsalSaved(page)
  await form
    .getByRole("switch", { name: "Rehearse release tasks on a copy of the database" })
    .click()
  await expect(form).toContainText("On · a release that cannot be rehearsed is stopped")
  await expect(form).toContainText("Rehearses against a copy of orders-db · PostgreSQL")
  // One database is reached, so there is nothing to choose.
  await expect(form.getByRole("combobox", { name: "Database" })).toHaveCount(0)
  await form.getByRole("radio", { name: "Continue with a warning" }).click()
  await form.getByLabel("Restore limit").fill("15")
  await saveSettings(page)
  expect((await saved).postDataJSON()).toEqual({
    enabled: true,
    connectionId: 0,
    whenUnavailable: "warn",
    restoreTimeoutSeconds: 900,
    maxBackupMb: 2048,
  })
  await expectSaved(page)
  await expect(form).toContainText(
    "On · a release that cannot be rehearsed continues with a warning",
  )
})

test("several databases ask which one the migration rehearses against", async ({ page }) => {
  await mockProject(page, { rehearsal: turnedOn })
  const form = await openBuild(page, [5, 9])
  await expect(form.getByText("Choose the database the release tasks migrate.")).toBeVisible()
  await form.getByRole("combobox", { name: "Database" }).click()
  await page.getByRole("option", { name: "analytics-db · PostgreSQL" }).click()
  await expect(form.getByText("Choose the database the release tasks migrate.")).toHaveCount(0)
  const saved = rehearsalSaved(page)
  await saveSettings(page)
  expect((await saved).postDataJSON()).toMatchObject({ enabled: true, connectionId: 9 })
})

test("a task the rehearsal cannot run is named before anyone deploys", async ({ page }) => {
  await mockProject(page, { rehearsal: turnedOn })
  const form = await openBuild(
    page,
    [],
    [{ name: "seed", command: "./seed.sh", timeoutSeconds: 60, env: [] }],
  )
  await expect(form).toContainText(
    "seed runs in the dashboard's shell, and only a task that runs in the release image can be rehearsed, so every deployment would be stopped.",
  )
  await expect(form).toContainText("No variable the release tasks receive references a database")
})

test("an administrator alone changes the terms", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "readonly" } }),
  )
  const form = await openBuild(page, [5])
  await expect(
    form.getByRole("switch", { name: "Rehearse release tasks on a copy of the database" }),
  ).toBeDisabled()
})
