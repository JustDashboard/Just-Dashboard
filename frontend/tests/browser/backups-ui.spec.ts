import { expect, test, type Page } from "@playwright/test"
import { risen } from "./risen"
import { jobs, mockWorkbench, run } from "./workbench-fixture"

/**
 * The Backups page after its 0.7.1 overhaul: a picture of where the server's
 * data goes over the failures, each job a card drawn as what it covers with
 * its last runs as a strip, and every thing on the server a lit card under a
 * meter of how much of it is covered. A job's own page opens on its identity
 * line and its own picture, with its runs as a rail beside the one it shows.
 */

test.beforeEach(async ({ page }) => {
  await mockWorkbench(page)
})

const jobCards = (page: Page) =>
  page.locator("[data-slot=choice-list]").first().locator("[data-slot=choice-row]")
const picture = (page: Page, name: string) => page.getByRole("group", { name })

test("the page opens on where the data goes, not on four figures", async ({ page }) => {
  await page.goto("/backups")
  await expect(page.getByRole("heading", { name: "Backups" })).toBeAttached()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(jobCards(page).first()).toContainText("Databases nightly")
  await expect(jobCards(page).first()).toContainText("failed")
  await expect(page.getByRole("region", { name: "Attention", exact: true })).toHaveCount(0)
  await expect(page.getByText(/things on this server have no backup/)).toHaveCount(0)
  // The totals the Stored tile carried are the identity line's now, and are
  // said there once: the picture's middle no longer repeats them under it.
  const identity = page.getByRole("region", { name: "About backups" })
  await expect(identity).toContainText("3 jobs")
  await expect(identity).toContainText("3.7 GB in 29 archives")
  await expect(identity).toContainText(/next backup \d+h/)
  await expect(page.getByText("3 jobs", { exact: false })).toHaveCount(1)
  await expect(page.getByText(/next backup/)).toHaveCount(1)
})

test("the identity line says how the jobs stand and carries the page's commands", async ({
  page,
}) => {
  await page.goto("/backups")
  const identity = page.getByRole("region", { name: "About backups" })
  await expect(identity).toHaveAttribute("data-layout", "project")
  // One job's last run failed and one is paused: the worst is said first.
  await expect(identity).toContainText("1 job failed")
  await expect(identity).toContainText("1 paused")
  // New backup is the one command; the Jobs title line keeps its count.
  await expect(identity.getByRole("button", { name: "New backup", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "New backup", exact: true })).toHaveCount(1)
  await expect(page.getByRole("button", { name: /^Import/ })).toHaveCount(0)
  await identity.getByRole("button", { name: "New backup", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "New backup" })).toBeVisible()
  await page.keyboard.press("Escape")
  // Import is the menu's.
  await identity.getByRole("button", { name: "Backup actions" }).click()
  await page.getByRole("menuitem", { name: "Import from a destination…" }).click()
  await expect(page.getByRole("dialog", { name: "Import from a destination" })).toBeVisible()
})

test("the identity line stands where it will while the jobs are read", async ({ page }) => {
  let gate: Promise<void> = Promise.resolve()
  await page.route("**/api/v1/backups/", async (route) => {
    await gate
    return route.fulfill({ contentType: "application/json", body: JSON.stringify(jobs) })
  })
  for (const width of [1280, 390]) {
    let release!: () => void
    gate = new Promise<void>((resolve) => {
      release = resolve
    })
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/backups")
    const identity = page.getByRole("region", { name: "About backups" })
    await expect(identity).toBeVisible()
    await expect(identity).not.toContainText("3 jobs")
    await risen(page)
    const before = await identity.boundingBox()
    release()
    await expect(identity).toContainText("3 jobs")
    await expect(jobCards(page).first()).toBeVisible()
    await risen(page)
    expect(await identity.boundingBox(), `the line at ${width}`).toEqual(before)
  }
})

test("a read-only account is offered neither New backup nor Import", async ({ page }) => {
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read"],
        user: { id: 2, username: "viewer", role: "viewer", totpEnabled: true },
      }),
    }),
  )
  await page.goto("/backups")
  const identity = page.getByRole("region", { name: "About backups" })
  await expect(identity).toContainText("3 jobs")
  await expect(page.getByRole("button", { name: "New backup", exact: true })).toHaveCount(0)
  await identity.getByRole("button", { name: "Backup actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Refresh" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Import/ })).toHaveCount(0)
  await page.keyboard.press("Escape")
  // A job's own verbs are the same question: nothing a reader cannot use.
  await expect(jobCards(page).first().getByRole("button", { name: "Run now" })).toHaveCount(0)
  await expect(jobCards(page).first().getByRole("button", { name: "Edit" })).toHaveCount(0)
})

test("the picture draws each kind by its products and each destination as its service", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.goto("/backups")
  const has = picture(page, "What this server has")
  await expect(has.getByRole("button", { name: "Show databases in coverage" })).toBeVisible()
  await expect(has).toContainText("2 of 3 backed up")
  await expect(has).toContainText("none of 3 backed up · 1 paused")
  await expect(has.locator('img[src="/logos/redis.svg"]')).toBeVisible()

  const goes = picture(page, "Where the archives go")
  await expect(goes).toContainText("Backblaze B2")
  await expect(goes).toContainText("wayy-backups")
  await expect(goes).toContainText("This server's disk")
  // An S3 job with no endpoint is Amazon's, because that is where it writes.
  await expect(goes).toContainText("Amazon S3")
  await expect(goes.locator('img[src="/logos/aws.svg"]')).toBeVisible()
  await expect(goes).toContainText("failed")

  // Pressing a kind narrows the coverage list to everything of that kind.
  await has.getByRole("button", { name: "Show databases in coverage" }).click()
  await expect(page.getByRole("button", { name: /^Everything\s*13$/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByRole("button", { name: /^Databases\s*3$/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByRole("link", { name: "LOL: open Databases nightly" })).toHaveAttribute(
    "href",
    "/backups/2",
  )
  await expect(page.getByRole("button", { name: "Back up cache" })).toBeVisible()
  await expect(page.getByText("trilium-data", { exact: true })).toHaveCount(0)
})

test("each job is its products, its outcome, its destination and its runs", async ({ page }) => {
  await page.goto("/backups")
  const cards = jobCards(page)
  // Worst first: the failing job leads.
  await expect(cards.first()).toContainText("Databases nightly")
  await expect(cards.first()).toContainText("failed")
  await expect(cards.first().locator('img[src="/logos/backblaze.svg"]')).toBeVisible()
  await expect(cards.first().locator('img[src="/logos/postgresql.svg"]')).toBeAttached()
  await expect(cards.first().getByRole("img", { name: "Last 14 runs: 2 failed" })).toBeVisible()

  const nginx = cards.filter({ hasText: "Nginx configuration" })
  await expect(nginx.locator('img[src="/logos/nginx.svg"]')).toBeVisible()
  await expect(nginx).toContainText("backed up")
  await expect(nginx).toContainText("50.3 KB in 7 archives")
  await expect(
    page.getByRole("link", { name: "Nginx configuration", exact: true }),
  ).toHaveAttribute("href", "/backups/1")
  await expect(
    cards.filter({ hasText: "n8n data" }).locator('img[src="/logos/aws.svg"]'),
  ).toBeVisible()
})

test("a job taking a backup is lit on its card and named in the picture", async ({ page }) => {
  const running = jobs.map((job) =>
    job.id === 1 ? { ...job, lastRun: run(72, 1, 20_000, "running", 0) } : job,
  )
  await page.route("**/api/v1/backups/", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify(running) }),
  )
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.goto("/backups")
  await expect(page.getByText("Backing up Nginx configuration")).toBeVisible()
  await expect(page.getByRole("region", { name: "About backups" })).toContainText("Backing up now")
  await expect(picture(page, "What this server has")).toContainText("backing up now")
  const nginx = jobCards(page).filter({ hasText: "Nginx configuration" })
  await expect(nginx).toContainText("running now")
  await expect(nginx.getByRole("button", { name: "Run now" })).toBeDisabled()
})

test("what the server has is drawn as its products, under a meter of the cover", async ({
  page,
}) => {
  await page.goto("/backups")
  await expect(page.getByText("3 of 13 protected", { exact: true })).toBeVisible()
  // Unprotected first: the covered databases wait behind Everything.
  await expect(page.getByText("LOL", { exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: /^Everything\s*13$/ }).click()
  const row = (name: string) =>
    page.locator("li", { has: page.getByText(name, { exact: true }) }).last()
  await expect(row("LOL").locator('img[src="/logos/postgresql.svg"]')).toBeVisible()
  await expect(row("cache").locator('img[src="/logos/redis.svg"]')).toBeVisible()
  await expect(row("Caddy configuration").locator('img[src="/logos/caddy.svg"]')).toBeVisible()
  // A volume is the product of the container that keeps its data there, and
  // one mounted by a database container run from a bare id is its engine.
  await expect(row("trilium-data").locator('img[src="/logos/trilium.svg"]')).toBeVisible()
  await expect(row("LOL-data").locator('img[src="/logos/postgresql.svg"]')).toBeVisible()
  await expect(row("Just-Dashboard").locator('img[src="/logos/git.svg"]')).toBeVisible()
})

test("a thing nothing covers opens the form filled in for it", async ({ page }) => {
  await page.goto("/backups")
  await page.getByRole("button", { name: "Back up Caddy configuration" }).click()
  await expect(page.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(
    "Caddy configuration",
  )
})

test("the form names each database dump for what it is, and a Redis one is not an RDB file", async ({
  page,
}) => {
  await page.goto("/backups")
  await page.getByRole("button", { name: "Back up Caddy configuration" }).click()
  const dumps = page.getByRole("dialog").getByRole("group", { name: "Database dumps" })
  await expect(dumps).toContainText(
    "pg_dump, mysqldump, mongodump — or the dashboard's built-in dump",
  )
  await expect(dumps).toContainText(
    "Redis is always the built-in one: its keys as JSON Lines that the dashboard restores, not an RDB file.",
  )
  await expect(dumps).not.toContainText("Redis snapshot")
})

test("the dump formats in the job form, for the record @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names where the screenshots go")
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/backups")
  await page.getByRole("button", { name: "Back up Caddy configuration" }).click()
  const dumps = page.getByRole("dialog").getByRole("group", { name: "Database dumps" })
  await dumps.scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${process.env.JD_SHOTS}/backup-dump-formats-1440.png` })
})

test("a delayed database handoff opens a saveable native-dump-only job and consumes the URL", async ({
  page,
}) => {
  let release!: () => void
  const waiting = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/backups/resources", async (route) => {
    await waiting
    return route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        resources: [
          {
            kind: "database",
            id: "remote",
            name: "Remote database",
            connectionId: 3,
            suggest: { name: "Remote database", sources: [], databaseDumps: [3] },
            coveredBy: [],
            protected: false,
          },
        ],
        unavailable: {},
      }),
    })
  })
  await page.route("**/api/v1/databases/", (route) =>
    route.fulfill({ contentType: "application/json", body: "[]" }),
  )
  await page.goto("/backups?database=3&keep=1")
  await expect(page.getByRole("heading", { name: "Backups" })).toBeAttached()
  expect(new URL(page.url()).searchParams.get("database")).toBe("3")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  release()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(
    "Remote database",
  )
  await expect(dialog.getByRole("textbox", { name: "Sources (one per line)" })).toHaveValue("")
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeEnabled()
  await expect.poll(() => new URL(page.url()).searchParams.get("database")).toBeNull()
  expect(new URL(page.url()).searchParams.get("keep")).toBe("1")
  const request = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/backups/") && request.method() === "POST",
  )
  await dialog.getByRole("button", { name: "Create", exact: true }).click()
  expect((await request).postDataJSON()).toMatchObject({ sources: [], databaseDumps: [3] })
  await expect(dialog).toHaveCount(0)
})

test("resource unpicks preserve manual paths and the settings another pick shares", async ({
  page,
}) => {
  await page.route("**/api/v1/backups/resources", (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        resources: ["App", "Shared"].map((name) => ({
          kind: "deployment",
          id: name,
          name,
          paths: ["/srv/app"],
          suggest: { name, sources: ["/srv/app"], excludes: ["*.log"], pauseContainers: ["app"] },
          coveredBy: [],
          protected: false,
        })),
        unavailable: {},
      }),
    }),
  )
  await page.goto("/backups?source=%2Fsrv%2Fapp")
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Pick from what this server has" }).click()
  const app = dialog.getByRole("checkbox", { name: "Back up App", exact: true })
  const shared = dialog.getByRole("checkbox", { name: "Back up Shared", exact: true })
  await expect(app).not.toBeChecked()
  await app.check()
  await shared.check()
  await app.uncheck()
  await expect(dialog.getByRole("textbox", { name: "Sources (one per line)" })).toHaveValue(
    "/srv/app",
  )
  await expect(dialog.getByRole("textbox", { name: "Excludes (glob, one per line)" })).toHaveValue(
    "*.log",
  )
  await expect(dialog.getByRole("checkbox", { name: "Pause app", exact: true })).toBeChecked()
  await shared.uncheck()
  await expect(dialog.getByRole("textbox", { name: "Sources (one per line)" })).toHaveValue(
    "/srv/app",
  )
  await expect(dialog.getByRole("textbox", { name: "Excludes (glob, one per line)" })).toHaveValue(
    "",
  )
})

test("closing the detail edit discards its draft and reopening uses the saved definition", async ({
  page,
}) => {
  await page.goto("/backups/1")
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("textbox", { name: "Name", exact: true }).fill("Discard this edit")
  await page.keyboard.press("Escape")
  await expect(dialog).toHaveCount(0)
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(jobs[0].name)
})

test("two editors keep the stale draft and keys until an explicit review resolves the conflict", async ({
  page,
  context,
}) => {
  const other = await context.newPage()
  await mockWorkbench(other)
  let current = { ...jobs[1] }
  const saves: Record<string, unknown>[] = []
  for (const tab of [page, other]) {
    await tab.route("**/api/v1/backups/2", async (route) => {
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON() as Record<string, unknown>
        saves.push(body)
        if (body.expectedRevision !== current.revision) {
          return route.fulfill({
            status: 409,
            contentType: "application/json",
            body: JSON.stringify({
              error: { code: "backup_job_conflict", message: "This job changed in another tab" },
            }),
          })
        }
        current = {
          ...current,
          name: String(body.name),
          retention: Number(body.retention),
          revision: current.revision + 1,
        }
      }
      return route.fulfill({ contentType: "application/json", body: JSON.stringify(current) })
    })
    await tab.goto("/backups/2")
    await tab.getByRole("button", { name: "Edit", exact: true }).click()
  }
  const first = page.getByRole("dialog")
  const stale = other.getByRole("dialog")
  await first.getByRole("spinbutton", { name: "Keep the last", exact: true }).fill("10")
  await stale.getByRole("textbox", { name: "Name", exact: true }).fill("Second editor draft")
  await stale.getByRole("textbox", { name: "Access key ID", exact: true }).fill("fixture-new-key")
  await stale.getByLabel("Secret access key", { exact: true }).fill("fixture-new-secret")
  await first.getByRole("button", { name: "Save", exact: true }).click()
  await expect(first).toHaveCount(0)
  await stale.getByRole("button", { name: "Save", exact: true }).click()
  await expect(stale.getByText("This job changed while you were editing")).toBeVisible()
  await expect(stale.getByRole("button", { name: "Save", exact: true })).toBeDisabled()
  await expect(stale.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(
    "Second editor draft",
  )
  await expect(stale.getByLabel("Secret access key", { exact: true })).toHaveValue(
    "fixture-new-secret",
  )
  expect(saves.map((body) => body.expectedRevision)).toEqual([1, 1])
  expect(current.retention).toBe(10)
  const storage = await other.evaluate(() =>
    JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
  )
  expect(storage).not.toContain("fixture-new-key")
  expect(storage).not.toContain("fixture-new-secret")
  await stale.getByRole("button", { name: "Review current job", exact: true }).click()
  const differences = stale.getByRole("table")
  await expect(differences.getByRole("row", { name: "Archives to keep 10 14" })).toBeVisible()
  // A second save after the review is still protected by the reviewed revision.
  current = { ...current, revision: current.revision + 1 }
  await stale.getByRole("button", { name: "Save draft over reviewed job" }).click()
  await expect(stale.getByRole("button", { name: "Save draft over reviewed job" })).toHaveCount(0)
  expect(saves.at(-1)?.expectedRevision).toBe(2)
  await expect(stale.getByLabel("Secret access key", { exact: true })).toHaveValue(
    "fixture-new-secret",
  )
  await stale.getByRole("button", { name: "Review current job", exact: true }).click()
  // The operator keeps the other editor's retention after reviewing it.
  await stale.getByRole("spinbutton", { name: "Keep the last", exact: true }).fill("10")
  await stale.getByRole("button", { name: "Save draft over reviewed job" }).click()
  await expect(stale).toHaveCount(0)
  expect(saves.at(-1)).toMatchObject({
    expectedRevision: 3,
    name: "Second editor draft",
    retention: 10,
    secrets: { accessKeyId: "fixture-new-key", secretAccessKey: "fixture-new-secret" },
  })
  await other.getByRole("button", { name: "Edit", exact: true }).click()
  await expect(stale.getByLabel("Secret access key", { exact: true })).toHaveValue("")
  await other.close()
})

test("a held backup draft keeps its original revision after dashboard navigation", async ({
  page,
}) => {
  let current = { ...jobs[1] }
  const saves: Record<string, unknown>[] = []
  await page.route("**/api/v1/backups/2", async (route) => {
    if (route.request().method() === "PUT") {
      saves.push(route.request().postDataJSON() as Record<string, unknown>)
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "backup_job_conflict", message: "This job changed" },
        }),
      })
    }
    return route.fulfill({ contentType: "application/json", body: JSON.stringify(current) })
  })
  await page.goto("/backups/2")
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("textbox", { name: "Name", exact: true }).fill("Held draft")
  await dialog.getByLabel("Secret access key", { exact: true }).fill("fixture-held-secret")
  // Invoke the actual SPA link while an editor is open, as a page-owned
  // navigation command can do; a reload would discard RAM by design.
  await page
    .locator('a[href="/files"]')
    .first()
    .evaluate((link) => (link as HTMLElement).click())
  await expect(page).toHaveURL(/\/files$/)
  current = { ...current, revision: 2, retention: 10 }
  await page.locator('a[href="/backups"]').first().click()
  await page.getByRole("link", { name: "Databases nightly", exact: true }).click()
  await expect(page).toHaveURL(/\/backups\/2$/)
  await expect(page.getByRole("heading", { name: "Databases nightly", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).toHaveValue("Held draft")
  await dialog.getByRole("button", { name: "Save", exact: true }).click()
  await expect(dialog.getByText("This job changed while you were editing")).toBeVisible()
  expect(saves[0].expectedRevision).toBe(1)
  await page.evaluate(() => window.dispatchEvent(new Event("jd-working-state-cleared")))
  await expect(dialog).toHaveCount(0)
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  await expect(dialog.getByLabel("Secret access key", { exact: true })).toHaveValue("")
  await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(current.name)
})

test("failed run history shows its read error and retries without claiming there are no runs", async ({
  page,
}) => {
  let failed = true
  await page.route("**/api/v1/backups/1/runs", (route) =>
    failed
      ? route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "unavailable", message: "Run history unavailable" },
          }),
        })
      : route.fulfill({
          contentType: "application/json",
          body: JSON.stringify({ runs: [run(400, 1, 20_000, "success", 1000)], running: false }),
        }),
  )
  await page.goto("/backups/1")
  await expect(
    page.getByRole("alert").filter({ hasText: "Run history unavailable" }),
  ).toContainText("Run history unavailable")
  await expect(page.getByText("No runs yet. Run now takes the first one.")).toHaveCount(0)
  failed = false
  await page.getByRole("button", { name: "Retry run history" }).click()
  await expect(page.getByRole("button", { name: /^Run 400,/ })).toBeVisible()
  await expect(page.getByRole("alert").filter({ hasText: "Run history unavailable" })).toHaveCount(
    0,
  )
  failed = true
  await expect(page.getByText("Run history could not refresh")).toBeVisible({ timeout: 10_000 })
  await expect(page.getByRole("button", { name: /^Run 400,/ })).toBeVisible()
  await expect(
    page.getByText("Showing the last successful reading.", { exact: false }),
  ).toBeVisible()
})

test("a job's page opens on its identity, its picture and the run it last took", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.goto("/backups/2")
  await expect(page.getByRole("heading", { name: "Databases nightly" })).toBeAttached()
  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity).toContainText("2 database dumps")
  await expect(identity).toContainText("Backblaze B2 · wayy-backups")
  await expect(identity).toContainText("failed")

  const takes = picture(page, "What Databases nightly takes")
  await expect(takes).toContainText("LOL")
  await expect(takes).toContainText("Main")
  await expect(takes.locator('img[src="/logos/postgresql.svg"]')).toHaveCount(2)
  await expect(picture(page, "Where its archives go")).toContainText("wayy-backups")

  // What it holds is one sentence with its trend, not four tiles. How the
  // last run went is the identity line's end, and when it runs next is the
  // picture's middle: each is said once.
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  const holding = page.locator("[data-slot=reading-line]")
  await expect(holding).toHaveCount(1)
  await expect(holding).toContainText("Holding")
  await expect(holding).toContainText("1.7 GB in 12 archives")
  await expect(holding).toContainText("Keeps last 14 archives and 30 days · last archive")
  await expect(
    holding.getByRole("img", { name: "Archive size, oldest run to newest" }),
  ).toBeVisible()
  await expect(page.getByText(/^next \d+h/)).toHaveCount(1)
  await expect(page.getByText("14 runs · 2 failed")).toBeVisible()

  // A run is one line of the rail, its archive's size a figure and no bar.
  const rail = page.getByRole("list", { name: "Runs" })
  await expect(rail.locator("[data-slot=run-size]")).toHaveCount(14)
  await expect(rail.locator("[data-slot=run-size]").nth(1)).toHaveText(/^\d+(\.\d)? (B|KB|MB|GB)$/)
  await expect(rail.locator("[role=presentation], [class*=bg-meter-track]")).toHaveCount(0)
  const heights = await rail
    .getByRole("button")
    .evaluateAll((runs) => runs.map((run) => Math.round(run.getBoundingClientRect().height)))
  expect(new Set(heights).size, `rail rows ${heights}`).toBe(1)

  // With none picked the newest run is shown, its failing line washed.
  const inspector = page.getByRole("region", { name: "Run 100" })
  await expect(inspector).toContainText("FAILED: pg_dump: connection refused")
  await page.getByRole("button", { name: /^Run 101,/ }).click()
  await expect(page).toHaveURL(/run=101/)
  await expect(page.getByRole("region", { name: "Run 101" })).toContainText("archived 1,204 files")
  await expect(page.getByRole("button", { name: /^Run 101,/ })).toHaveAttribute(
    "aria-current",
    "true",
  )
})

test("a job that pauses containers names them on its line and still says when it runs next", async ({
  page,
}) => {
  await page.route("**/api/v1/backups/1", (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ ...jobs[0], pauseContainers: ["web", "worker"] }),
    }),
  )
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.goto("/backups/1")
  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity).toContainText("pauses web, worker while it archives")
  await expect(identity).not.toContainText("pauses 2 containers")
  // The picture's middle is the schedule over the next run, whatever the job pauses.
  await expect(page.getByText(/^next \d+h/)).toBeVisible()
  await expect(page.getByText("Every day at 03:00")).toBeVisible()
})

test("a read-only account reads a job and is offered none of its verbs", async ({ page }) => {
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read"],
        user: { id: 2, username: "viewer", role: "viewer", totpEnabled: true },
      }),
    }),
  )
  await page.goto("/backups/1")
  await expect(page.locator("[data-slot=reading-line]")).toContainText("50.3 KB in 7 archives")
  await expect(page.getByRole("button", { name: /^Run 100,/ })).toBeVisible()
  for (const name of ["Run now", "Edit", "More actions for Nginx configuration"]) {
    await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0)
  }
  // The run it shows can be read and browsed, not restored or downloaded.
  await expect(page.getByRole("button", { name: "Browse files" })).toBeVisible()
  await expect(page.getByRole("region", { name: "Run 100" }).getByRole("button")).toHaveCount(1)
})

for (const width of [1280, 390]) {
  test(`a job's opening stands where it will while its runs are read, at ${width}`, async ({
    page,
  }) => {
    let release!: () => void
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    await page.route("**/api/v1/backups/1/runs", async (route) => {
      await gate
      return route.fallback()
    })
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/backups/1")
    const holding = page.locator("[data-slot=reading-line]")
    await expect(holding).toContainText("50.3 KB in 7 archives")
    await risen(page)
    const before = await holding.boundingBox()
    const runs = await page.getByRole("heading", { name: "Runs" }).boundingBox()
    release()
    await expect(page.getByRole("button", { name: /^Run 100,/ })).toBeVisible()
    // The last archive's size and the trend land in the line, which keeps its height.
    await expect(holding).toContainText("last archive")
    await risen(page)
    expect(await holding.boundingBox()).toEqual(before)
    expect(await page.getByRole("heading", { name: "Runs" }).boundingBox()).toEqual(runs)
  })
}

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1600 })
    await page.goto("/backups")
    await expect(page.locator("[data-slot=choice-row]").first()).toBeVisible()
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
    await page.screenshot({ path: `test-results/backups-${width}.png`, fullPage: true })
  })
}

test("a phone draws the picture as marks and scrolls nowhere sideways", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/backups")
  await expect(picture(page, "What this server has").getByRole("button")).toHaveCount(7)
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)
})
