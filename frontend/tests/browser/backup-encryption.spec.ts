import { createHash } from "node:crypto"
import { expect, test, type Locator, type Page, type Route } from "@playwright/test"
import { jobs, mockWorkbench, run } from "./workbench-fixture"

/**
 * Encrypted backups: a job seals what it writes to the server's backup key,
 * which the Backups page draws with what depends on it and offers to export
 * behind a confirmation; a sealed run says so on the rail, in its inspector
 * and on its download; and the job form encrypts what leaves the server
 * unless the operator says otherwise.
 */

const now = Date.now()
const MIN = 60_000
const iso = (ms: number) => new Date(ms).toISOString()
const digest = (seed: string) => `sha256:${createHash("sha256").update(seed).digest("hex")}`
const RECIPIENT = "age1qk7e2zt0v9g8mlxw4a6sdyhr5u3c8fj2np0wtq9ezlx3kv7m4ya6sl2hjd"

const sealedJobs = jobs.map((job) => ({ ...job, encrypt: job.targetKind !== "local" }))
const nightly = sealedJobs.find((job) => job.id === 2)!

const keyStatus = (exported: boolean) => ({
  exists: true,
  recipient: RECIPIENT,
  createdAt: iso(now - 12 * 24 * 60 * MIN - 37 * MIN),
  exportedAt: exported ? iso(now - 3 * 24 * 60 * MIN - 41 * MIN) : undefined,
  encryptingJobs: 2,
  encryptedArchives: 22,
})

/** Nightly runs of the database job, the newest sealed and one from before it encrypted. */
const sealedRuns = Array.from({ length: 8 }, (_, i) => {
  const base = run(100 + i, 2, (i * 24 * 60 + 17) * MIN, "success", 0)
  const plaintext = 905_000_000 - i * 3_100_000
  const encrypted = i < 6
  return {
    ...base,
    artifact: `db/Databases-nightly-job2-run${100 + i}-20261016-023000-K7QXW2M4${encrypted ? ".tar.gz.age" : ".tar.gz"}`,
    sizeBytes: plaintext + (encrypted ? 13_800 : 0),
    log: [
      "dumped database LOL (postgres, lol) with pg_dump: 811204118 bytes",
      "dumped database Main (postgres, main) with pg_dump: 93011422 bytes",
      "archiving 0 source(s) and 2 database dump(s)",
      "archived 2 file(s), 905018337 bytes",
      ...(encrypted ? ["encrypted with this server's backup key before leaving the server"] : []),
      "upload complete",
    ].join("\n"),
    manifest: {
      version: 1,
      artifactDigest: digest(`stored-${i}`),
      complete: true,
      sources: [],
      databaseDumps: [
        {
          connectionId: 1,
          name: "LOL",
          driver: "postgres",
          database: "lol",
          method: "pg_dump",
          file: "lol.dump",
          archivePath: "database-0001/lol.dump",
          digest: digest(`lol-${i}`),
          bytes: 811_204_118,
        },
        {
          connectionId: 2,
          name: "Main",
          driver: "postgres",
          database: "main",
          method: "pg_dump",
          file: "main.dump",
          archivePath: "database-0002/main.dump",
          digest: digest(`main-${i}`),
          bytes: 93_011_422,
        },
      ],
      ...(encrypted
        ? {
            encryption: {
              format: "age",
              recipient: RECIPIENT,
              plaintextDigest: digest(`inside-${i}`),
              plaintextBytes: plaintext,
            },
          }
        : {}),
    },
  }
})

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })

async function mockEncryption(
  page: Page,
  { key = keyStatus(false) as unknown, list = sealedJobs as unknown[] } = {},
) {
  const state = { exported: false, exports: 0 }
  await page.route("**/api/v1/backups/", (route) =>
    route.request().method() === "GET" ? json(route, list) : json(route, nightly, 201),
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/key",
    (route) => json(route, state.exported ? keyStatus(true) : key),
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/key/export",
    (route) => {
      state.exported = true
      state.exports++
      return route.fulfill({
        status: 200,
        headers: {
          "Content-Type": "text/plain; charset=utf-8",
          "Content-Disposition": "attachment; filename=just-dashboard-backup-key.txt",
          "Cache-Control": "no-store",
        },
        body: `# Just Dashboard backup key\n# public key: ${RECIPIENT}\nAGE-SECRET-KEY-1FIXTURE\n`,
      })
    },
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/2",
    (route) => json(route, nightly),
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/2/runs",
    (route) => json(route, { runs: sealedRuns, running: false }),
  )
  return state
}

const keyPanel = (page: Page) => page.getByRole("region", { name: "Backup key" })
const card = (page: Page, name: string) =>
  page.locator("[data-slot=choice-row]").filter({ hasText: name })
const settled = (locator: Locator) =>
  locator.evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)))
const noSidewaysScroll = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)

test.beforeEach(async ({ page }) => {
  await mockWorkbench(page)
})

test("the key is drawn with what depends on it, and says so while no copy has left", async ({
  page,
}) => {
  await mockEncryption(page)
  await page.goto("/backups")
  const panel = keyPanel(page)
  await expect(panel).toContainText("This server's backup key")
  await expect(panel).toContainText(RECIPIENT)
  await expect(panel).toContainText("22 archives · 2 jobs encrypting")
  await expect(panel).toContainText("not exported yet")
  await expect(panel).toContainText("Keep a copy of the key off this server")
  await expect(panel.getByRole("button", { name: "Export key…" })).toBeEnabled()
  // Which jobs seal what they write is on their cards.
  await expect(card(page, "Databases nightly")).toContainText("wayy-backups · encrypted")
  await expect(card(page, "Nginx configuration")).not.toContainText("encrypted")
})

test("exporting the key asks first, then saves the file and records when", async ({ page }) => {
  const state = await mockEncryption(page)
  await page.goto("/backups")
  await keyPanel(page).getByRole("button", { name: "Export key…" }).click()
  const dialog = page.getByRole("dialog", { name: "Export the backup key" })
  await expect(dialog).toContainText(RECIPIENT)
  await expect(dialog).toContainText("22 kept now")
  await expect(dialog).toContainText("recorded in the audit log")
  // Cancelling sends nothing.
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toHaveCount(0)
  expect(state.exports).toBe(0)

  await keyPanel(page).getByRole("button", { name: "Export key…" }).click()
  const download = page.waitForEvent("download")
  await dialog.getByRole("button", { name: "Export key", exact: true }).click()
  expect((await download).suggestedFilename()).toBe("just-dashboard-backup-key.txt")
  expect(state.exports).toBe(1)
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText("Backup key exported")).toBeVisible()
  await expect(keyPanel(page)).toContainText("exported 3d")
  await expect(keyPanel(page)).not.toContainText("Keep a copy of the key off this server")
})

test("a sealed run says so on the rail, in its inspector and on its download", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1100 })
  await mockEncryption(page)
  await page.goto("/backups/2")
  await expect(page.locator("[data-slot=host-identity]")).toContainText(
    "encrypted with this server's backup key",
  )
  await expect(
    page.getByRole("button", { name: /^Run 100, success, .*, encrypted$/ }),
  ).toBeVisible()
  // The run from before the job encrypted is listed as it was written.
  await expect(page.getByRole("button", { name: /^Run 107, success, .*\d$/ })).toBeVisible()

  const inspector = page.getByRole("region", { name: "Run 100" })
  await expect(inspector).toContainText("encrypted")
  const sealed = inspector.getByRole("region", { name: "Encryption" })
  await expect(sealed).toContainText(RECIPIENT)
  await expect(sealed).toContainText(digest("inside-0"))
  await expect(sealed).toContainText("age --decrypt")
  await inspector.getByRole("button", { name: "More actions for run 100" }).click()
  await expect(page.getByRole("menuitem", { name: "Download decrypted archive" })).toBeVisible()
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: /^Run 107,/ }).click()
  const clear = page.getByRole("region", { name: "Run 107" })
  await expect(clear.getByRole("region", { name: "Encryption" })).toHaveCount(0)
  await clear.getByRole("button", { name: "More actions for run 107" }).click()
  await expect(page.getByRole("menuitem", { name: "Download archive", exact: true })).toBeVisible()
})

test("the job form encrypts what leaves the server unless the operator says not to", async ({
  page,
}) => {
  await mockEncryption(page)
  await page.goto("/backups")
  await page.getByRole("button", { name: "New backup", exact: true }).first().click()
  const dialog = page.getByRole("dialog", { name: "New backup" })
  const option = dialog.getByRole("switch", {
    name: "Encrypt archives with this server's backup key",
  })
  await expect(option).not.toBeChecked()
  await dialog.getByRole("combobox", { name: "Destination" }).click()
  await page.getByRole("option", { name: "Amazon S3 or any S3-compatible bucket" }).click()
  await expect(option).toBeChecked()
  // A choice the operator made holds when the destination changes again.
  await option.click()
  await dialog.getByRole("combobox", { name: "Destination" }).click()
  await page.getByRole("option", { name: "Backblaze B2" }).click()
  await expect(option).not.toBeChecked()
  await dialog.getByRole("textbox", { name: "Name", exact: true }).fill("Offsite configuration")
  await dialog.getByRole("textbox", { name: "Sources (one per line)" }).fill("/etc/nginx")
  await dialog.getByRole("textbox", { name: "Bucket", exact: true }).fill("wayy-config")
  const created = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/backups/") && request.method() === "POST",
  )
  await dialog.getByRole("button", { name: "Create", exact: true }).click()
  expect((await created).postDataJSON()).toMatchObject({ targetKind: "b2", encrypt: false })

  await page.goto("/backups/2")
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const edit = page.getByRole("dialog", { name: "Edit Databases nightly" })
  await expect(
    edit.getByRole("switch", { name: "Encrypt archives with this server's backup key" }),
  ).toBeChecked()
  const saved = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/backups/2") && request.method() === "PUT",
  )
  await edit.getByRole("button", { name: "Save", exact: true }).click()
  expect((await saved).postDataJSON()).toMatchObject({ encrypt: true, expectedRevision: 1 })
})

test("a page whose jobs all write in the clear has no key to show", async ({ page }) => {
  await mockEncryption(page, {
    key: { exists: false, encryptingJobs: 0, encryptedArchives: 0 },
    list: jobs.map((job) => ({ ...job, encrypt: false })),
  })
  await page.goto("/backups")
  await expect(card(page, "Databases nightly")).toBeVisible()
  await expect(keyPanel(page)).toHaveCount(0)
  await expect(page.locator("[data-slot=choice-row]").filter({ hasText: "encrypted" })).toHaveCount(
    0,
  )
})

test("a key that cannot be read says so and can be read again", async ({ page }) => {
  await mockEncryption(page)
  let failing = true
  await page.route(
    (url) => url.pathname === "/api/v1/backups/key",
    (route) =>
      failing
        ? json(
            route,
            { error: { code: "internal", message: "The backup key could not be read" } },
            500,
          )
        : json(route, keyStatus(false)),
  )
  await page.goto("/backups")
  const panel = keyPanel(page)
  await expect(panel.getByRole("alert")).toContainText("The backup key could not be read")
  await expect(panel.getByRole("button", { name: "Export key…" })).toBeDisabled()
  failing = false
  await panel.getByRole("button", { name: "Try again" }).click()
  await expect(panel).toContainText(RECIPIENT)
  await expect(panel.getByRole("alert")).toHaveCount(0)
})

test("a reader sees the key's state and cannot export it", async ({ page }) => {
  await mockEncryption(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, {
      authenticated: true,
      needsTotp: false,
      needsEnrollment: false,
      require2fa: false,
      capabilities: ["read"],
      user: {
        id: 3,
        username: "reader",
        role: "readonly",
        totpEnabled: true,
        disabled: false,
        mustChangePassword: false,
        lastLoginAt: iso(now - 9 * MIN),
        createdAt: iso(now - 40 * 24 * 60 * MIN),
      },
    }),
  )
  await page.goto("/backups")
  await expect(keyPanel(page)).toContainText("not exported yet")
  await expect(keyPanel(page).getByRole("button", { name: "Export key…" })).toHaveCount(0)
  await page.goto("/backups/2")
  await expect(page.getByRole("region", { name: "Run 100" })).toContainText("encrypted")
  await expect(page.getByRole("button", { name: "More actions for run 100" })).toHaveCount(0)
})

test("at 390px neither page scrolls sideways", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockEncryption(page)
  await page.goto("/backups")
  await expect(keyPanel(page)).toContainText("not exported yet")
  expect(await noSidewaysScroll(page)).toBe(false)
  await page.goto("/backups/2")
  await expect(page.getByRole("region", { name: "Encryption" })).toContainText(RECIPIENT)
  expect(await noSidewaysScroll(page)).toBe(false)
})

test("encrypted backups, for the pull request @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go in")
  const shots = process.env.JD_SHOTS
  const shot = (name: string) => page.screenshot({ path: `${shots}/${name}` })
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockEncryption(page)

  await page.goto("/backups")
  const panel = keyPanel(page)
  await expect(panel).toContainText("not exported yet")
  await expect(card(page, "Databases nightly")).toContainText("encrypted")
  await panel.evaluate((el) => el.scrollIntoView({ block: "center" }))
  await shot("backup-key-1440.png")

  await panel.getByRole("button", { name: "Export key…" }).click()
  const dialog = page.getByRole("dialog", { name: "Export the backup key" })
  await expect(dialog).toContainText(RECIPIENT)
  await settled(dialog)
  await shot("export-key-1440.png")
  await dialog.getByRole("button", { name: "Export key", exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(panel).toContainText("exported 3d")
  await page.getByText("Backup key exported").waitFor({ state: "hidden", timeout: 15_000 })
  await panel.evaluate((el) => el.scrollIntoView({ block: "center" }))
  await shot("backup-key-exported-1440.png")

  await page.goto("/backups/2")
  const sealed = page.getByRole("region", { name: "Encryption" })
  await expect(sealed).toContainText(RECIPIENT)
  // The rail and the run it shows are one frame with a scroll of its own:
  // the frame stands whole in the window and only the inspector scrolls.
  await sealed.evaluate((el) => {
    const pane = el.closest(".overflow-y-auto") as HTMLElement
    pane.parentElement?.scrollIntoView({ block: "center" })
    pane.scrollTop += el.getBoundingClientRect().top - pane.getBoundingClientRect().top - 24
  })
  await shot("sealed-run-1440.png")

  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const edit = page.getByRole("dialog", { name: "Edit Databases nightly" })
  const option = edit.getByRole("switch", {
    name: "Encrypt archives with this server's backup key",
  })
  await option.scrollIntoViewIfNeeded()
  await settled(edit)
  await shot("job-form-encrypt-1440.png")
  await page.keyboard.press("Escape")

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/backups")
  await expect(keyPanel(page)).toContainText("not exported yet")
  await keyPanel(page).evaluate((el) => el.scrollIntoView({ block: "center" }))
  await shot("backup-key-390.png")
})
