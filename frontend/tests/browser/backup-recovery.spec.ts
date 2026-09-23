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

const resources = {
  resources: [
    {
      kind: "volume",
      id: "shop_data",
      name: "shop_data",
      detail: "Mounted by shop-db",
      paths: ["/var/lib/docker/volumes/shop_data/_data"],
      suggest: {
        name: "Volume shop_data",
        sources: ["/var/lib/docker/volumes/shop_data/_data"],
        pauseContainers: ["shop-db"],
      },
      coveredBy: [],
      protected: false,
    },
    {
      kind: "dashboard",
      id: "dashboard",
      name: "Just Dashboard",
      detail: "Settings, accounts, saved connections and deployment records",
      paths: ["/var/lib/just-dashboard"],
      suggest: {
        name: "Dashboard data",
        sources: ["/var/lib/just-dashboard"],
        excludes: ["/var/lib/just-dashboard/staging"],
        sqlitePaths: ["/var/lib/just-dashboard/vpsd.db"],
      },
      coveredBy: [{ jobId: 5, jobName: "Application backup", enabled: true }],
      protected: true,
      lastBackupAt: now,
    },
  ],
  unavailable: {},
}

async function mockBackups(page: Page, existing = false) {
  let saved: Record<string, unknown> | null = null
  let checks = 0
  let verification: Record<string, unknown> | undefined
  const restores: { body: Record<string, unknown>; confirm: string | undefined }[] = []
  const enabled: boolean[] = []
  const job = {
    id: 5,
    name: "Application backup",
    sources: ["/srv/app"],
    excludes: [],
    targetKind: "local",
    target: { path: "/var/backups/app" },
    schedule: "0 3 * * *",
    retention: 3,
    retentionDays: 0,
    enabled: true,
    createdAt: now,
    hasCredentials: false,
    recovery: plan,
    databaseDumps: existing ? [3] : [],
    overdue: false,
    stored: { runs: 1, bytes: 1234 },
    lastSuccessAt: now,
  }
  const manifest = {
    version: 1,
    artifactDigest: image,
    complete: true,
    sources: [{ path: "/srv/app", archivePath: "source-0001" }],
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
    } else if (path === "/backups/resources") {
      result = resources
    } else if (path === "/backups/5/enabled") {
      const body = route.request().postDataJSON() as { enabled: boolean }
      enabled.push(body.enabled)
      result = { ...job, enabled: body.enabled }
    } else if (path === "/backups/runs/8/contents") {
      result = [
        { name: "source-0001", size: 0, mode: "0755", isDir: true },
        { name: "source-0001/config.yml", size: 120, mode: "0644", isDir: false },
        { name: "source-0001/uploads", size: 0, mode: "0755", isDir: true },
        { name: "source-0001/uploads/logo.png", size: 2048, mode: "0644", isDir: false },
      ]
    } else if (path === "/backups/runs/8/restore") {
      // The client URI-encodes the phrase so a header can carry any text.
      restores.push({
        body: route.request().postDataJSON(),
        confirm: decodeURIComponent(route.request().headers()["x-confirm"] ?? ""),
      })
      result = { runId: 8, destination: "", entries: 2, bytes: 120, targets: ["/srv/app"] }
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
    } else if (path === "/backups/5") {
      // The job's own page reads the job itself rather than finding it in the
      // list it used to be opened from.
      result = job
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
  return {
    saved: () => saved,
    checks: () => checks,
    restores: () => restores,
    enabled: () => enabled,
  }
}

/** The job's page, entered from its name in the list. */
async function openJob(page: Page) {
  // The job is a card that opens its page, so its name is a link.
  await page.getByRole("link", { name: "Application backup", exact: true }).click()
  await expect(page).toHaveURL(/\/backups\/5$/)
  await expect(page.getByRole("heading", { name: "Application backup" })).toBeVisible()
}

async function runMenu(page: Page, item: RegExp) {
  // A menu still playing its exit animation would take the trigger press as
  // a click outside and dismiss the menu it was opening.
  await expect(page.getByRole("menu")).toHaveCount(0)
  await page.getByRole("button", { name: "More actions for run 8" }).click()
  await page.getByRole("menuitem", { name: item }).click()
}

test("backup setup records native SQLite sources and an application recovery check", async ({
  page,
}) => {
  const api = await mockBackups(page)
  await page.goto("/backups")
  await page.getByRole("button", { name: "New backup" }).first().click()
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("Application backup")
  await page.getByRole("textbox", { name: "Sources (one per line)", exact: true }).fill("/srv/app")
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
    schedule: "0 3 * * *",
    retention: 7,
    retentionDays: 0,
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
  await openJob(page)
  await runMenu(page, /Verify restore/)
  await expect(page.getByText("Restore check: failed", { exact: true })).toBeVisible()
  await expect(page.getByText("The restored canary was missing.", { exact: true })).toBeVisible()
  await runMenu(page, /Verify restore/)
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
  await expect(page.getByText("1 database dump").first()).toBeVisible()
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const shop = page.getByRole("checkbox", { name: "Dump shop-db" })
  const cache = page.getByRole("checkbox", { name: "Dump cache" })
  await expect(shop).toBeChecked()
  await expect(cache).not.toBeChecked()
  await cache.click()
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect(page.getByText("Job updated", { exact: true })).toBeVisible()
  expect(api.saved()).toBeNull()

  await openJob(page)
  await runMenu(page, /Restore database/)
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

test("coverage lists what the server has and writes a job for an unprotected volume", async ({
  page,
}) => {
  const api = await mockBackups(page, true)
  await page.goto("/backups")
  // What is not covered is the coverage list's to say, under its meter; the
  // attention list is for jobs that failed or went quiet.
  await expect(page.getByText("1 of 2 protected", { exact: true })).toBeVisible()
  await expect(page.getByText("shop_data", { exact: true })).toBeVisible()
  await expect(page.getByText("shop_data has no backup", { exact: true })).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await page.getByRole("button", { name: "Back up", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(
    "Volume shop_data",
  )
  await expect(page.getByRole("textbox", { name: "Sources (one per line)" })).toHaveValue(
    "/var/lib/docker/volumes/shop_data/_data",
  )
  await expect(page.getByRole("checkbox", { name: "Pause shop-db" })).toBeChecked()
  // Picking the dashboard adds its file, its exclusion and its SQLite snapshot too.
  await page.getByRole("button", { name: "Pick from what this server has" }).click()
  await page.getByRole("checkbox", { name: "Back up Just Dashboard" }).click()
  await expect(page.getByRole("textbox", { name: "Sources (one per line)" })).toHaveValue(
    "/var/lib/docker/volumes/shop_data/_data\n/var/lib/just-dashboard",
  )
  await expect(
    page.getByRole("textbox", { name: "SQLite files to snapshot (one per line)" }),
  ).toHaveValue("/var/lib/just-dashboard/vpsd.db")
  await page.getByRole("button", { name: "Create", exact: true }).click()
  await expect(page.getByText("Job created", { exact: true })).toBeVisible()
  expect(api.saved()).toMatchObject({
    name: "Volume shop_data",
    sources: ["/var/lib/docker/volumes/shop_data/_data", "/var/lib/just-dashboard"],
    excludes: ["/var/lib/just-dashboard/staging"],
    sqlitePaths: ["/var/lib/just-dashboard/vpsd.db"],
    pauseContainers: ["shop-db"],
  })
})

test("a job is paused from its menu and files are put back in place or restored as a subset", async ({
  page,
}) => {
  const api = await mockBackups(page, true)
  await page.goto("/backups")
  await page.getByRole("button", { name: "More actions for Application backup" }).click()
  await page.getByRole("menuitem", { name: /Pause schedule/ }).click()
  await expect(page.getByText("Application backup paused", { exact: true })).toBeVisible()
  expect(api.enabled()).toEqual([false])

  await openJob(page)
  await runMenu(page, /Restore files/)
  await page.getByRole("combobox", { name: "Where" }).click()
  await page.getByRole("option", { name: "Back where the files came from" }).click()
  await expect(page.getByText("source-0001 → /srv/app", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await page.getByRole("textbox", { name: /Type/ }).fill("restore in place")
  await page.getByRole("button", { name: "Restore", exact: true }).click()
  await expect(page.getByText("Restored 2 entries (120 B)", { exact: true })).toBeVisible()
  expect(api.restores()).toEqual([
    { body: { inPlace: true, paths: [] }, confirm: "restore in place" },
  ])

  // One file out of the archive, into a directory of the operator's choosing.
  await runMenu(page, /Browse files/)
  await expect(page.getByText("4 entries", { exact: true })).toBeVisible()
  await page.getByRole("checkbox", { name: "Select source-0001/config.yml" }).click()
  await page.getByRole("button", { name: "Restore 1 selected…" }).click()
  await page.getByRole("textbox", { name: "Destination directory" }).fill("/srv/restore")
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await page.getByRole("textbox", { name: /Type/ }).fill("/srv/restore")
  await page.getByRole("button", { name: "Restore", exact: true }).click()
  await expect(page.getByText("Restored 2 entries (120 B)", { exact: true }).last()).toBeVisible()
  expect(api.restores().at(-1)).toEqual({
    body: { destination: "/srv/restore", inPlace: false, paths: ["source-0001/config.yml"] },
    confirm: "/srv/restore",
  })
})
