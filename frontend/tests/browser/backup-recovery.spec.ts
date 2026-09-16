import { expect, test, type Page } from "@playwright/test"

const now = "2026-09-16T12:00:00Z"
const image = `sha256:${"a".repeat(64)}`
const plan = {
  image,
  command: ["/app/check-recovery", "/restore/source-0001"],
  schemaVersion: "schema-v1",
  expectedOutputDigest: `sha256:${"b".repeat(64)}`,
  timeoutSeconds: 60,
  maxBytes: 16 * 1024 ** 3,
  automatic: false,
}

const connections = [
  {
    id: 3,
    name: "shop-db",
    driver: "postgres",
    host: "127.0.0.1",
    port: "5432",
    user: "app",
    database: "shop",
    createdAt: now,
  },
  {
    id: 4,
    name: "cache",
    driver: "redis",
    host: "127.0.0.1",
    port: "6379",
    user: "",
    database: "0",
    createdAt: now,
  },
]

async function mockBackups(page: Page, existing = false) {
  let saved: Record<string, unknown> | null = null
  let checks = 0
  let verification: Record<string, unknown> | undefined
  const restores: { body: Record<string, unknown>; confirm: string | undefined }[] = []
  const job = {
    id: 5,
    name: "Application backup",
    sources: ["/srv/app"],
    excludes: [],
    targetKind: "local",
    target: { path: "/var/backups/app" },
    schedule: "",
    retention: 3,
    enabled: false,
    createdAt: now,
    hasCredentials: false,
    recovery: plan,
    databaseDumps: existing ? [3] : [],
  }
  const manifest = {
    version: 1,
    artifactDigest: image,
    complete: true,
    databaseDumps: [
      {
        connectionId: 3,
        name: "shop-db",
        driver: "postgres",
        database: "shop",
        method: "pg_dump",
        file: "shop-postgres.dump",
        archivePath: "database-0001/shop-postgres.dump",
        digest: image,
        bytes: 4096,
      },
    ],
  }
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    let result: unknown = []
    let status = 200
    if (path === "/auth/session") {
      result = {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read", "system.admin", "service.control", "destructive"],
        user: {
          id: 1,
          username: "operator",
          role: "admin",
          totpEnabled: false,
          disabled: false,
          mustChangePassword: false,
          createdAt: now,
        },
      }
    } else if (path === "/updates/self") {
      result = { current: "0.6.7", latest: "0.6.7" }
    } else if (path === "/databases/") {
      result = connections
    } else if (path === "/backups/runs/8/restore-database") {
      restores.push({
        body: route.request().postDataJSON(),
        confirm: route.request().headers()["x-confirm"],
      })
      result = { connection: "shop-db", database: "shop_drill", output: "restored 1 table" }
    } else if (path === "/backups/") {
      if (route.request().method() === "POST") {
        saved = route.request().postDataJSON()
        result = { ...job, ...saved }
        status = 201
      } else {
        result = existing ? [job] : []
      }
    } else if (path === "/backups/5/runs") {
      result = {
        running: false,
        runs: [
          {
            id: 8,
            jobId: 5,
            startedAt: now,
            endedAt: now,
            status: "success",
            artifact: "/var/backups/app/run-8.tgz",
            sizeBytes: 1234,
            log: "archive saved",
            trigger: "manual",
            duration: "1s",
            manifest: existing ? manifest : undefined,
            restoreVerification: verification,
          },
        ],
      }
    } else if (path === "/backups/runs/8/verify-restore") {
      checks++
      verification = {
        id: checks,
        runId: 8,
        state: checks === 1 ? "failed" : "passed",
        startedAt: now,
        endedAt: now,
        artifactDigest: image,
        manifestDigest: image,
        planDigest: image,
        applicationImage: image,
        schemaVersion: "schema-v1",
        entries: 2,
        bytes: 100,
        cleanupComplete: true,
        detail:
          checks === 1
            ? "The restored canary was missing."
            : "The application verified the restored canary.",
      }
      status = checks === 1 ? 502 : 200
      result =
        checks === 1
          ? { error: { code: "restore_verification_failed", message: verification.detail } }
          : verification
    }
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(result) })
  })
  return { saved: () => saved, checks: () => checks, restores: () => restores }
}

test("backup setup records native SQLite sources and an application recovery check", async ({
  page,
}) => {
  const api = await mockBackups(page)
  await page.goto("/backups")
  await page.getByRole("button", { name: "New job" }).click()
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("Application backup")
  await page.getByRole("textbox", { name: "Sources (one per line)", exact: true }).fill("/srv/app")
  await page.getByText("Consistency and recovery checks", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "SQLite files to snapshot (one per line)" })
    .fill("/srv/app/state.sqlite")
  await page.getByRole("switch", { name: "Application recovery check" }).click()
  await page.getByRole("textbox", { name: "Application image digest" }).fill(image)
  await page
    .getByRole("textbox", { name: "Checker executable and arguments (one per line)" })
    .fill("/app/check-recovery\n/restore/source-0001")
  await page.getByRole("textbox", { name: "Expected schema version" }).fill("schema-v1")
  await page.getByRole("textbox", { name: "Expected canary output" }).fill("schema-v1:known-record")
  await page.getByRole("switch", { name: "Verify after every successful backup" }).click()
  await page.getByRole("button", { name: "Create", exact: true }).click()
  await expect(page.getByText("Job created", { exact: true })).toBeVisible()
  expect(api.saved()).toMatchObject({
    sqlitePaths: ["/srv/app/state.sqlite"],
    expectedRecoveryOutput: "schema-v1:known-record",
    recovery: {
      image,
      command: ["/app/check-recovery", "/restore/source-0001"],
      schemaVersion: "schema-v1",
      automatic: true,
    },
  })
})

test("restore verification failure remains visible and a successful retry shows its evidence", async ({
  page,
}) => {
  const api = await mockBackups(page, true)
  await page.goto("/backups")
  await page.getByRole("button", { name: "History", exact: true }).click()
  await page.getByRole("button", { name: "Verify restore", exact: true }).click()
  await expect(page.getByText("Restore check: failed", { exact: true })).toBeVisible()
  await expect(page.getByText("The restored canary was missing.", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Verify restore", exact: true }).click()
  await expect(page.getByText("Restore check: passed", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Log", exact: true }).click()
  await expect(
    page.getByText("The application verified the restored canary.", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText("Removed", { exact: true })).toBeVisible()
  expect(api.checks()).toBe(2)
})

test("a backup job dumps saved databases natively and a run restores one into a drill database", async ({
  page,
}) => {
  const api = await mockBackups(page, true)
  await page.goto("/backups")
  await expect(page.getByText("1 native dump", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Edit" }).click()
  await page.getByText("Consistency and recovery checks", { exact: true }).click()
  const shop = page.getByRole("checkbox", { name: "Dump shop-db" })
  const cache = page.getByRole("checkbox", { name: "Dump cache" })
  await expect(shop).toBeChecked()
  await expect(cache).not.toBeChecked()
  await cache.click()
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect(page.getByText("Job updated", { exact: true })).toBeVisible()
  expect(api.saved()).toBeNull()

  await page.getByRole("button", { name: "History" }).click()
  await page.getByRole("button", { name: "Restore database" }).click()
  await expect(page.getByRole("combobox", { name: "Database dump" })).toContainText("shop-db")
  await page.getByRole("textbox", { name: "Target database" }).fill("shop_drill")
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByText(/replaces the contents of/)).toBeVisible()
  await page.getByRole("textbox", { name: /Type/ }).fill("shop_drill")
  await page.getByRole("button", { name: "Restore database", exact: true }).last().click()
  await expect(page.getByText("Restored shop-db into shop_drill", { exact: true })).toBeVisible()
  expect(api.restores()).toEqual([
    { body: { connectionId: 3, database: "shop_drill" }, confirm: "shop_drill" },
  ])
})
