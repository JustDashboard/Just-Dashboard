import { expect, test, type Page } from "@playwright/test"
import { jobs, mockWorkbench } from "./workbench-fixture"

/**
 * The two destinations beyond a disk and an S3 bucket: an SFTP server, signed
 * in to with a password or a key and pinned to the host key it first showed,
 * and any storage service rclone speaks, given as the remote's pasted
 * configuration. Credentials go out once and are never read back.
 */

const FINGERPRINT = "SHA256:Zk3HqW0p7m4b4V2y9r8M5t1aQe6uJx0LcNdPgHs2fYw"

const sftpJob = {
  ...jobs[0],
  id: 9,
  revision: 3,
  name: "Offsite to the NAS",
  targetKind: "sftp",
  target: {
    host: "nas.example.test",
    port: 2222,
    username: "backup",
    path: "/volume1/backups",
    hostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample",
    hostKeyFingerprint: FINGERPRINT,
  },
  hasCredentials: true,
  encrypt: true,
}

const rcloneJob = {
  ...jobs[0],
  id: 10,
  revision: 1,
  name: "Drive copy",
  targetKind: "rclone",
  target: { path: "backups/this-server", remoteType: "drive" },
  hasCredentials: true,
  encrypt: true,
}

async function withRemoteJobs(page: Page) {
  await mockWorkbench(page)
  await page.route("**/api/v1/backups/", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify([...jobs, sftpJob, rcloneJob]),
    })
  })
  for (const job of [sftpJob, rcloneJob]) {
    await page.route(`**/api/v1/backups/${job.id}`, async (route) => {
      if (route.request().method() === "GET") {
        return route.fulfill({ contentType: "application/json", body: JSON.stringify(job) })
      }
      return route.fallback()
    })
  }
}

test("an SFTP destination asks for the server, the account and a way in, and keeps the password out of the page", async ({
  page,
}) => {
  await withRemoteJobs(page)
  await page.goto("/backups")
  await page.getByRole("button", { name: "New backup", exact: true }).first().click()
  const dialog = page.getByRole("dialog", { name: "New backup" })
  await dialog.getByRole("textbox", { name: "Name", exact: true }).fill("Offsite configuration")
  await dialog.getByRole("textbox", { name: "Sources (one per line)" }).fill("/etc/nginx")
  await dialog.getByRole("combobox", { name: "Destination" }).click()
  await page.getByRole("option", { name: "An SFTP server" }).click()

  const create = dialog.getByRole("button", { name: "Create", exact: true })
  await dialog.getByRole("textbox", { name: "Server", exact: true }).fill("nas.example.test")
  await dialog.getByRole("textbox", { name: "Port", exact: true }).fill("2222")
  await dialog.getByRole("textbox", { name: "Account", exact: true }).fill("backup")
  await dialog.getByRole("textbox", { name: "Directory", exact: true }).fill("/volume1/backups")
  // Nothing to sign in with yet: a new server has no saved credentials to
  // fall back on.
  await expect(create).toBeDisabled()
  await dialog.getByLabel("Password", { exact: true }).fill("fixture-sftp-password")
  await expect(dialog.getByText(/pinned the first time it is reached/)).toBeVisible()
  // Leaving the server is encrypted by default.
  await expect(
    dialog.getByRole("switch", { name: "Encrypt archives with this server's backup key" }),
  ).toBeChecked()

  const created = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/backups/") && request.method() === "POST",
  )
  await create.click()
  expect((await created).postDataJSON()).toMatchObject({
    targetKind: "sftp",
    target: { host: "nas.example.test", port: 2222, username: "backup", path: "/volume1/backups" },
    secrets: { password: "fixture-sftp-password", privateKey: "" },
    encrypt: true,
  })
  const storage = await page.evaluate(() =>
    JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
  )
  expect(storage).not.toContain("fixture-sftp-password")
})

test("a pinned SFTP job names its server and fingerprint, can be pinned again, and Test says a new pin", async ({
  page,
}) => {
  await withRemoteJobs(page)
  await page.route("**/api/v1/backups/9/test", (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ ok: true, hostKeyFingerprint: FINGERPRINT, hostKeyPinned: true }),
    }),
  )
  await page.goto("/backups/9")
  await expect(
    page.getByText("SFTP · backup@nas.example.test:2222:/volume1/backups").first(),
  ).toBeVisible()

  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const edit = page.getByRole("dialog", { name: "Edit Offsite to the NAS" })
  await expect(edit.getByText(FINGERPRINT)).toBeVisible()
  // Saved credentials stand behind empty fields.
  await expect(edit.getByLabel("Password", { exact: true })).toHaveAttribute(
    "placeholder",
    "unchanged",
  )
  await edit.getByRole("switch", { name: "Pin the server again" }).click()
  const saved = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/backups/9") && request.method() === "PUT",
  )
  await edit.getByRole("button", { name: "Save", exact: true }).click()
  const body = (await saved).postDataJSON() as Record<string, unknown>
  expect(body).toMatchObject({ forgetHostKey: true, expectedRevision: 3, targetKind: "sftp" })
  expect(body.secrets).toBeUndefined()

  await page
    .getByRole("button", { name: /actions/i })
    .first()
    .click()
  await page.getByRole("menuitem", { name: "Test destination" }).click()
  await expect(page.getByText(`Pinned the server's host key ${FINGERPRINT}`)).toBeVisible()
})

test("an rclone destination takes a pasted remote and is named for its service", async ({
  page,
}) => {
  await withRemoteJobs(page)
  await page.goto("/backups")
  await expect(page.getByText("Google Drive · backups/this-server").first()).toBeVisible()

  await page.getByRole("button", { name: "New backup", exact: true }).first().click()
  const dialog = page.getByRole("dialog", { name: "New backup" })
  await dialog.getByRole("textbox", { name: "Name", exact: true }).fill("OneDrive copy")
  await dialog.getByRole("textbox", { name: "Sources (one per line)" }).fill("/etc/nginx")
  await dialog.getByRole("combobox", { name: "Destination" }).click()
  await page.getByRole("option", { name: /through rclone/ }).click()
  const create = dialog.getByRole("button", { name: "Create", exact: true })
  await expect(create).toBeDisabled()
  const config =
    '[onedrive]\ntype = onedrive\ndrive_type = personal\ntoken = {"access_token":"fixture-token"}'
  await dialog.getByRole("textbox", { name: "Remote configuration" }).fill(config)
  await dialog.getByRole("textbox", { name: "Folder", exact: true }).fill("backups/this-server")
  const created = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/backups/") && request.method() === "POST",
  )
  await create.click()
  expect((await created).postDataJSON()).toMatchObject({
    targetKind: "rclone",
    target: { path: "backups/this-server" },
    secrets: { rcloneConfig: config },
  })

  // Editing keeps the saved remote unless a new one is pasted.
  await page.goto("/backups/10")
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  const edit = page.getByRole("dialog", { name: "Edit Drive copy" })
  await expect(edit.getByText(/Saved for Google Drive/)).toBeVisible()
  const saved = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/backups/10") && request.method() === "PUT",
  )
  await edit.getByRole("button", { name: "Save", exact: true }).click()
  expect(((await saved).postDataJSON() as Record<string, unknown>).secrets).toBeUndefined()
})
