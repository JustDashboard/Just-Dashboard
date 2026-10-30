import { expect, test, type Page } from "@playwright/test"
import { mockWorkbench } from "./workbench-fixture"
import { OLD_KEY, ORIGIN, catalogue, mockImport } from "./backup-import-fixture"

/**
 * Import from a destination: a fresh server reads what a bucket holds that it
 * did not write, grouped by the server and job that took it, imports a job's
 * history, brings the key a sealed archive needs, and restores an imported run
 * in place through the same dialog as its own, which says first what it will
 * have to make on this host.
 */

const importDialog = (page: Page) => page.getByRole("dialog", { name: "Import from a destination" })

test.beforeEach(async ({ page }) => {
  await mockWorkbench(page)
})

test("a fresh server reads a destination's catalogue, grouped by server and job, and imports a job's history", async ({
  page,
}) => {
  const state = await mockImport(page)
  await page.goto("/backups")
  await page.getByRole("button", { name: "Import…" }).click()
  const dialog = importDialog(page)
  await expect(dialog).toBeVisible()
  // This server's own destinations are the choices, a bucket's prefix apart.
  await expect(
    dialog.getByRole("button", { name: "Read Backblaze B2 wayy-backups/db" }),
  ).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Read Amazon S3 offsite" })).toBeVisible()
  await dialog.getByRole("button", { name: "Read Backblaze B2 wayy-backups/db" }).click()

  const view = dialog.getByTestId("backup-catalogue")
  await expect(view).toContainText(
    "4 archives this server did not write · 12 archives of this server's own left out",
  )
  const old = view.getByRole("region", { name: "old-vps" })
  await expect(old).toContainText("App files")
  await expect(old).toContainText("2 archives · 785.8 MB")
  await expect(old).toContainText("1 path · 1 volume")
  await expect(old).toContainText("can import")
  await expect(old).toContainText("Postgres nightly")
  await expect(old).toContainText("2 database dumps")
  await expect(old).toContainText("needs its key")
  await expect(old).toContainText(`sealed to ${OLD_KEY.slice(0, 12)}`)
  await expect(
    view.getByRole("region", { name: "Written before archives carried their manifest" }),
  ).toContainText("no manifest")
  await expect(view).toContainText("Sealed to 1 key this server does not hold")
  expect(state.catalogueBodies).toEqual([{ jobId: 2 }])

  await old.getByRole("button", { name: "Import all 2" }).click()
  await expect(page.getByText("Imported 2 archives into App files (old-vps)")).toBeVisible()
  expect(state.importBodies).toEqual([{ jobId: 2, originId: ORIGIN, originJobId: 3 }])
  // The catalogue is read again and says what is here now.
  await expect(old.getByText("imported", { exact: true }).first()).toBeVisible()
  await expect(old).toContainText("imported here")

  await page.keyboard.press("Escape")
  const card = page.locator("[data-slot=choice-row]").filter({ hasText: "App files (old-vps)" })
  await expect(card).toContainText("imported from old-vps · job App files")
  // There is nothing to run or edit on a record of another server's archives.
  await card.getByRole("button", { name: "More actions for App files (old-vps)" }).click()
  await expect(page.getByRole("menuitem", { name: "Remove imported job" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Run now" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Edit" })).toHaveCount(0)
})

test("a sealed archive names the key it needs, and imports once that key is brought", async ({
  page,
}) => {
  const state = await mockImport(page)
  await page.goto("/backups")
  await page.getByRole("button", { name: "Import…" }).click()
  const dialog = importDialog(page)
  await dialog.getByRole("button", { name: "Read Backblaze B2 wayy-backups/db" }).click()
  const view = dialog.getByTestId("backup-catalogue")
  await view.getByRole("button", { name: "Import a key…" }).click()
  await expect(view).toContainText(OLD_KEY)
  await view
    .getByRole("textbox", { name: "Key file" })
    .fill(`# public key: ${OLD_KEY}\nAGE-SECRET-KEY-1FIXTURE\n`)
  await view.getByRole("button", { name: "Import key" }).click()
  await expect(page.getByText("Imported to open the archives sealed to it")).toBeVisible()
  expect(state.keyBodies).toEqual([{ key: `# public key: ${OLD_KEY}\nAGE-SECRET-KEY-1FIXTURE\n` }])
  // Read again with the key here: the sealed one can be imported now.
  const postgres = view.getByRole("region", { name: "old-vps" })
  await expect(postgres).not.toContainText("needs its key")
  await expect(postgres).toContainText("sealed · a key here opens it")
  await expect(view).not.toContainText("this server does not hold")
  expect(state.catalogueBodies).toHaveLength(2)
})

test("a destination typed in is read with its keys, and a copy can be kept here", async ({
  page,
}) => {
  const state = await mockImport(page)
  await page.goto("/backups")
  await page.getByRole("button", { name: "Import…" }).click()
  const dialog = importDialog(page)
  await dialog.getByRole("button", { name: "Read another destination" }).click()
  const read = dialog.getByRole("button", { name: "Read destination" })
  await expect(read).toBeDisabled()
  await dialog.getByLabel("Bucket").fill("old-server-backups")
  await dialog.getByLabel("Prefix").fill("nightly")
  await dialog.getByLabel("Endpoint").fill("https://minio.example.com")
  await dialog.getByLabel("Access key ID").fill("AKIAOLD")
  await dialog.getByLabel("Secret access key").fill("old-secret")
  await read.click()
  await expect(dialog.getByTestId("backup-catalogue")).toBeVisible()
  expect(state.catalogueBodies).toEqual([
    {
      targetKind: "s3",
      target: {
        bucket: "old-server-backups",
        region: "",
        endpoint: "https://minio.example.com",
        prefix: "nightly",
      },
      secrets: { accessKeyId: "AKIAOLD", secretAccessKey: "old-secret" },
    },
  ])
  await dialog.getByRole("switch", { name: "Keep a copy of each archive on this server" }).click()
  await dialog.getByLabel("Copy into").fill("/srv/imported")
  await dialog
    .getByRole("button", { name: /^Import the archive of / })
    .first()
    .click()
  await expect(page.getByText("Imported 2 archives into App files (old-vps)")).toBeVisible()
  expect(state.importBodies[0]).toMatchObject({
    targetKind: "s3",
    keys: [catalogue().origins[0].jobs[0].archives[0].key],
    copyTo: "/srv/imported",
  })
})

test("an imported run restores in place through the same dialog, which says what it makes first", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 })
  const state = await mockImport(page)
  await page.goto("/backups/9")
  await expect(page.locator("[data-slot=host-identity]")).toContainText(
    "imported from old-vps · job App files",
  )
  const inspector = page.getByRole("region", { name: "Run 902" })
  await expect(inspector).toContainText("run 41 of App files on old-vps")
  await expect(inspector).toContainText("the destination, on every restore")
  await expect(page.getByRole("button", { name: "Run now" })).toHaveCount(0)

  await inspector.getByRole("button", { name: "More actions for run 902" }).click()
  await page.getByRole("menuitem", { name: "Restore files…" }).click()
  const dialog = page.getByRole("dialog", { name: "Restore files from run 902" })
  await dialog.getByRole("combobox", { name: "Where" }).click()
  await page.getByRole("option", { name: "Back where the files came from" }).click()
  const plan = dialog.getByRole("list", { name: "Where each source goes" })
  await expect(plan).toContainText("source-0001 → volume app_data")
  await expect(plan).toContainText("the volume is made through Docker with the labels it had")
  await expect(plan).toContainText("source-0002 → /srv/app")
  await expect(plan).toContainText("made with the directories above it")
  await dialog.getByRole("button", { name: "Continue" }).click()

  const confirm = page.getByRole("dialog", { name: "Put the files back where they came from" })
  await expect(confirm).toContainText("Makes on this server first")
  await expect(confirm).toContainText("volume app_data")
  await expect(confirm).not.toContainText("Overwrites")
  await confirm.getByRole("button", { name: "Restore", exact: true }).click()
  await expect(page.getByText("Restored 1204 entries")).toBeVisible()
  expect(state.restoreBodies).toEqual([{ inPlace: true, paths: [] }])
})
