import { expect, test, type Page } from "@playwright/test"
import { CONTAINERS, VOLUMES, mockVolumes } from "./docker-volumes-fixture"
import { JOBS, mockJobs } from "./docker-volume-page-fixture"
import { risen } from "./risen"

/**
 * /docker/volumes/[name]: a volume as a page of its own. It was a sheet over
 * the Volumes table, and this address a redirect to it. The page opens on the
 * volume's identity line, a picture of who mounts it and where it is backed
 * up, and its views in the address (Files, Mounts, Backups, Configuration),
 * with the table as a rail beside it from `xl`.
 */

const identity = (page: Page, name: string) => page.getByRole("region", { name: `About ${name}` })
const picture = (page: Page) => page.locator("[data-slot=wire-flow]")
const rail = (page: Page) => page.locator("[data-slot=volume-rail]")
const railRows = (page: Page) => rail(page).getByRole("listitem").getByRole("button")
const tab = (page: Page, name: string) => page.getByRole("tab", { name })

async function open(page: Page, path: string) {
  await mockVolumes(page)
  await mockJobs(page)
  await page.goto(path)
}

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
})

test("it opens on the volume: its identity line, its picture and its files", async ({ page }) => {
  await open(page, "/docker/volumes/shop_uploads")

  const line = identity(page, "shop_uploads")
  await expect(line).toHaveAttribute("data-layout", "project")
  await expect(line.getByRole("button", { name: "in use" })).toBeVisible()
  await expect(line.getByText("6.1 GB")).toBeVisible()
  await expect(line.getByText("on this server")).toBeVisible()
  await expect(line.getByRole("link", { name: "shop" })).toHaveAttribute(
    "href",
    "/docker/stacks/shop",
  )
  await expect(line.getByText(/^created 96d/)).toBeVisible()
  await expect(line.getByRole("button", { name: /^backed up 3h/ })).toBeVisible()
  // One command with the brand's face, the rest behind the menu.
  await expect(line.getByRole("button", { name: "Open in Files" })).toHaveAttribute(
    "data-variant",
    "default",
  )
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  await expect(page.getByRole("dialog")).toHaveCount(0)

  // The picture: who mounts it on the left, where it is copied to on the right.
  const mounted = picture(page).getByRole("region", { name: "Mounted by" })
  await expect(mounted.getByRole("link", { name: "Open shop-web-1" })).toHaveAttribute(
    "href",
    `/docker/containers/${CONTAINERS[0].id}`,
  )
  await expect(mounted.getByText("/app/uploads")).toHaveCount(2)
  await expect(mounted.getByText("read-only")).toHaveCount(1)
  const copied = picture(page).getByRole("region", { name: "Backed up by" })
  await expect(copied.getByRole("link", { name: "Open Shop data" })).toHaveAttribute(
    "href",
    "/backups/3",
  )
  await expect(copied.getByText("Backblaze B2")).toBeVisible()
  await expect(copied.getByText(/^backed up 3h/)).toBeVisible()

  // Files is the view it opens on, and the address carries no ?tab= for it.
  await expect(tab(page, "Files")).toHaveAttribute("aria-selected", "true")
  await expect(page).toHaveURL(/\/docker\/volumes\/shop_uploads$/)
  const files = page.locator("[data-slot=pane]")
  await expect(files.getByRole("button", { name: "config.json" })).toBeVisible()
  await expect(files.getByRole("link", { name: "Open in Files" })).toHaveAttribute(
    "href",
    "/files?path=%2Fvar%2Flib%2Fdocker%2Fvolumes%2Fshop_uploads%2F_data",
  )
})

test("the views are in the address, and each says what the sheet said", async ({ page }) => {
  await open(page, "/docker/volumes/shop_uploads")

  await tab(page, "Mounts").click()
  await expect(page).toHaveURL(/\/docker\/volumes\/shop_uploads\?tab=mounts$/)
  const mounts = page.getByRole("list", { name: "Containers mounting this volume" })
  const rows = mounts.locator("[data-slot=choice-row]")
  await expect(rows).toHaveCount(2)
  await expect(mounts.getByRole("link", { name: "Open shop-worker-1" })).toHaveAttribute(
    "href",
    `/docker/containers/${CONTAINERS[1].id}`,
  )
  await expect(rows.nth(0)).toContainText("read-write")
  await expect(rows.nth(1)).toContainText("read-only")
  await expect(rows.nth(1)).toContainText("/app/uploads")
  await expect(rows.nth(1)).toContainText("running")
  // One height, with and without what only some rows carry.
  const heights = await rows.evaluateAll((all) =>
    all.map((r) => Math.round(r.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)

  // A reload, or a link, opens on the same view.
  await page.reload()
  await expect(tab(page, "Mounts")).toHaveAttribute("aria-selected", "true")
  await expect(mounts.locator("[data-slot=choice-row]")).toHaveCount(2)

  await tab(page, "Backups").click()
  await expect(page).toHaveURL(/\?tab=backups$/)
  const jobs = page.getByRole("list", { name: "Backup jobs covering this volume" })
  await expect(jobs.getByRole("link", { name: "Open Shop data" })).toHaveAttribute(
    "href",
    "/backups/3",
  )
  await expect(jobs).toContainText("Backblaze B2 · acme-backups")
  await expect(jobs).toContainText("Every day at 03:00")
  await expect(jobs.getByText(/^backed up 3h/)).toBeVisible()

  await tab(page, "Configuration").click()
  await expect(page).toHaveURL(/\?tab=configuration$/)
  await expect(page.getByText("com.docker.compose.project")).toBeVisible()
  await expect(page.getByText("/var/lib/docker/volumes/shop_uploads/_data")).toBeVisible()

  await tab(page, "Files").click()
  await expect(page).toHaveURL(/\/docker\/volumes\/shop_uploads$/)

  // The view a volume was left on is the one the next opens on from its row,
  // and the address says which, so a link copied from it opens the same view.
  await tab(page, "Configuration").click()
  await page.locator("[data-slot=page]").getByRole("link", { name: "Volumes" }).click()
  await page.getByRole("link", { name: "nextcloud_db", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/volumes\/nextcloud_db\?tab=configuration$/)
  await expect(tab(page, "Configuration")).toHaveAttribute("aria-selected", "true")
})

test("a paused job the job list does not name still has its line, and says so", async ({
  page,
}) => {
  await mockVolumes(page)
  // The job list without the paused job: the coverage still names it.
  await mockJobs(
    page,
    JOBS.filter((job) => job.name !== "Object storage"),
  )
  await page.goto("/docker/volumes/minio-data?tab=backups")

  const line = identity(page, "minio-data")
  await expect(line.getByRole("button", { name: "backup paused" })).toBeVisible()
  // Not protected, so the one command is the step it is short of.
  await expect(line.getByRole("button", { name: "Back up…" })).toHaveAttribute(
    "data-variant",
    "default",
  )
  const jobs = page.getByRole("list", { name: "Backup jobs covering this volume" })
  await expect(jobs.locator("[data-slot=choice-row]")).toHaveCount(1)
  await expect(jobs.getByText("paused", { exact: true })).toBeVisible()
  await expect(
    picture(page).getByRole("region", { name: "Backed up by" }).getByText("paused"),
  ).toBeVisible()
})

test("a volume a stack left behind says what a prune does, and can be backed up or removed", async ({
  page,
}) => {
  const mocks = await mockVolumes(page)
  await mockJobs(page)
  await page.goto("/docker/volumes/nextcloud_db")

  const line = identity(page, "nextcloud_db")
  const state = line.getByRole("button", { name: "prune deletes it" })
  await expect(state).toBeVisible()
  await expect(line.getByRole("button", { name: "not backed up" })).toBeVisible()

  // The picture has a ring where a container would be, and one where a job would.
  const mounted = picture(page).getByRole("region", { name: "Mounted by" })
  await expect(mounted.getByText("Nothing mounts it")).toBeVisible()
  await expect(mounted.getByRole("link", { name: "Open nextcloud" })).toHaveAttribute(
    "href",
    "/docker/stacks/nextcloud",
  )
  const copied = picture(page).getByRole("region", { name: "Backed up by" })
  await expect(copied.getByText("No job covers it")).toBeVisible()
  await expect(copied.getByRole("button", { name: "Back up…" })).toBeVisible()

  // The state is the way to why.
  await state.click()
  await expect(page).toHaveURL(/\?tab=mounts$/)
  await expect(page.getByText("Nothing mounts this volume")).toBeVisible()
  await expect(page.getByText(/was taken down and left it behind/)).toBeVisible()
  await expect(page.getByText(/would delete it and the 1\.9 GB in it/)).toBeVisible()
  await expect(
    page.getByRole("link", { name: "Open nextcloud to deploy it again" }),
  ).toHaveAttribute("href", "/docker/stacks/nextcloud")

  await tab(page, "Backups").click()
  await expect(page.getByText("No backup job covers this volume")).toBeVisible()

  // Remove is in the menu, enabled because nothing mounts it; it deletes the
  // volume and goes back to the ones that are left.
  await line.getByRole("button", { name: "More actions for nextcloud_db" }).click()
  await page.getByRole("menuitem", { name: "Remove" }).click()
  await page
    .getByRole("dialog", { name: "Delete volume" })
    .getByRole("button", { name: "Delete" })
    .click()
  await expect.poll(() => mocks.calls).toContain("DELETE /docker/volumes/nextcloud_db")
  await expect(page).toHaveURL(/\/docker\/volumes$/)
  await expect(page.getByRole("row", { name: /nextcloud_db/ })).toHaveCount(0)
})

test("Back up… hands the volume's directory to the Backups form", async ({ page }) => {
  await open(page, "/docker/volumes/nextcloud_db")

  // The Backups page takes the source into its form and out of its address,
  // so what it was sent is read from the navigation itself.
  const asked: string[] = []
  page.on("request", (request) => {
    const url = new URL(request.url())
    if (url.pathname === "/backups") asked.push(url.searchParams.get("source") ?? "")
  })
  await identity(page, "nextcloud_db").getByRole("button", { name: "Back up…" }).click()
  await expect(page).toHaveURL(/\/backups/)
  expect(asked).toContain("/var/lib/docker/volumes/nextcloud_db/_data")
})

test("a mounted volume keeps Remove in its menu, refused with the reason", async ({ page }) => {
  await open(page, "/docker/volumes/shop_uploads")

  await identity(page, "shop_uploads")
    .getByRole("button", { name: "More actions for shop_uploads" })
    .click()
  const remove = page.getByRole("menuitem", { name: "Remove — mounted by 2 containers" })
  await expect(remove).toBeVisible()
  await expect(remove).toHaveAttribute("aria-disabled", "true")
  await expect(page.getByRole("menuitem", { name: "Copy name" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Copy path" })).toBeVisible()
})

test("a container stopping is seen without waiting for the volume to be read again", async ({
  page,
}) => {
  const mocks = await mockVolumes(page)
  await mockJobs(page)
  await page.goto("/docker/volumes/shop_pgdata?tab=mounts")

  const line = identity(page, "shop_pgdata")
  await expect(line.getByRole("button", { name: "in use" })).toBeVisible()
  // Postgres' own files under a running container get the warning on Files.
  mocks.frame(CONTAINERS.map((c) => (c.name === "shop-db-1" ? { ...c, state: "exited" } : c)))
  await expect(line.getByRole("button", { name: "stopped" })).toBeVisible()
  await expect(
    page.getByRole("list", { name: "Containers mounting this volume" }).getByText("exited"),
  ).toBeVisible()
})

test("a database's files carry the warning, and a file opens over the page", async ({ page }) => {
  await open(page, "/docker/volumes/shop_pgdata")

  await expect(page.getByText(/corrupts it in ways that only show up later/)).toBeVisible()
  const files = page.locator("[data-slot=pane]")
  await expect(files.getByRole("button", { name: "pg_wal" })).toBeVisible()
  await expect(files.getByText("3 folders, 3 files", { exact: false })).toBeVisible()
})

/**
 * The rail is the table: its search, its filters and its order, so a volume
 * opened from a narrowed table is beside the others it was narrowed to.
 */
test("the rail is the table as it was left, and Alt+↓ walks it with the view kept", async ({
  page,
}) => {
  await open(page, "/docker/volumes")

  await page.getByRole("button", { name: "Only the volumes shop holds" }).first().click()
  await page.getByRole("link", { name: "shop_uploads", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/volumes\/shop_uploads$/)

  await expect(railRows(page)).toHaveCount(3)
  await expect(rail(page)).toContainText("3 of 16 · shop")
  await expect(rail(page).getByRole("button", { name: "shop_uploads" })).toHaveAttribute(
    "aria-current",
    "page",
  )

  await tab(page, "Mounts").click()
  await page.keyboard.press("Alt+ArrowDown")
  // Largest first, as the table was ordered: uploads, pgdata, redis.
  await expect(page).toHaveURL(/\/docker\/volumes\/shop_pgdata\?tab=mounts$/)
  await expect(tab(page, "Mounts")).toHaveAttribute("aria-selected", "true")
  await expect(rail(page).getByRole("button", { name: "shop_pgdata" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await page.getByRole("button", { name: "Previous volume" }).click()
  await expect(page).toHaveURL(/\/docker\/volumes\/shop_uploads\?tab=mounts$/)
  await expect(page.getByRole("button", { name: "Previous volume" })).toBeDisabled()

  // Its filter can be let go from here, and its search narrows it.
  await rail(page).getByRole("button", { name: "Clear filters" }).click()
  await expect(railRows(page)).toHaveCount(VOLUMES.length)
  await expect(rail(page)).toContainText("16 volumes · largest first")
  await rail(page).getByLabel("Find volumes").fill("nextcloud")
  await expect(railRows(page)).toHaveCount(3)
  // The volume on screen stays first when the list no longer holds it.
  await expect(railRows(page).first()).toHaveAttribute("aria-label", "shop_uploads")
  await rail(page).getByRole("button", { name: "nextcloud_html" }).click()
  await expect(page).toHaveURL(/\/docker\/volumes\/nextcloud_html\?tab=mounts$/)
})

/**
 * The page has its shape before the volume is read: the identity line, the
 * picture and the views are where they will be, so nothing moves when it lands.
 */
for (const width of [1280, 390]) {
  test(`the page holds still while the volume is read at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockVolumes(page)
    await mockJobs(page)
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route("**/api/v1/docker/volumes/**", async (route) => {
      await held
      return route.fallback()
    })
    await page.goto("/docker/volumes/shop_uploads")

    const line = identity(page, "shop_uploads")
    await expect(line).toContainText("Reading shop_uploads…")
    const tabs = page.getByRole("tablist")
    await expect(tabs).toBeVisible()
    await risen(page)
    const before = {
      line: await line.boundingBox(),
      picture: await picture(page).boundingBox(),
      tabs: await tabs.boundingBox(),
    }

    release()
    await expect(line.getByRole("button", { name: "in use" })).toBeVisible()
    await expect(
      picture(page).getByRole("region", { name: "Backed up by" }).getByRole("link"),
    ).toHaveCount(1)
    await risen(page)
    expect(await line.boundingBox()).toEqual(before.line)
    expect(await picture(page).boundingBox()).toEqual(before.picture)
    expect(await tabs.boundingBox()).toEqual(before.tabs)
  })
}

test("the picture is one height whatever a volume has on either side", async ({ page }) => {
  await open(page, "/docker/volumes/shop_uploads")
  await expect(picture(page).getByRole("link", { name: "Open shop-web-1" })).toBeVisible()
  await risen(page)
  const two = await picture(page).boundingBox()

  // One container and a paused job, then nothing on either side.
  for (const name of ["minio-data", "scratch"]) {
    await rail(page).getByRole("button", { name }).click()
    await expect(identity(page, name)).toBeVisible()
    await risen(page)
    expect((await picture(page).boundingBox())?.height).toBe(two?.height)
    expect((await picture(page).boundingBox())?.y).toBe(two?.y)
  }
})

test("a reader sees the volume and none of the controls it cannot use", async ({ page }) => {
  await mockVolumes(page, { capabilities: ["read"], noBackups: true })
  await page.goto("/docker/volumes/nextcloud_db")

  const line = identity(page, "nextcloud_db")
  await expect(line.getByRole("button", { name: "prune deletes it" })).toBeVisible()
  await expect(line.getByRole("button", { name: "Open in Files" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Back up…" })).toHaveCount(0)
  // No coverage answer: no backup fact rather than a wrong one, and the
  // picture says it could not be read.
  await expect(line.getByText(/backed up/)).toHaveCount(0)
  await expect(
    picture(page).getByRole("region", { name: "Backed up by" }).getByText("Coverage not readable"),
  ).toBeVisible()

  await line.getByRole("button", { name: "More actions for nextcloud_db" }).click()
  await expect(page.getByRole("menuitem", { name: "Copy name" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /^Remove/ })).toHaveCount(0)
  await page.keyboard.press("Escape")

  await tab(page, "Backups").click()
  await expect(page.getByText("Backup coverage is not readable with this account.")).toBeVisible()
})

test("an address that names no volume says so, with the way back", async ({ page }) => {
  await open(page, "/docker/volumes/never-made")

  await expect(page.getByText("No volume named never-made on this server")).toBeVisible()
  await expect(page.getByText("No such volume")).toHaveCount(0)
  // The rail still lists what there is.
  await expect(railRows(page)).toHaveCount(VOLUMES.length)
  await page.getByRole("link", { name: "Back to the volumes" }).click()
  await expect(page).toHaveURL(/\/docker\/volumes$/)
})

test("a volume whose driver keeps it elsewhere says where its figures are from", async ({
  page,
}) => {
  await open(page, "/docker/volumes/jellyfin-media")

  const line = identity(page, "jellyfin-media")
  await expect(line.getByText("not measurable")).toBeVisible()
  await expect(line.getByText("rclone")).toBeVisible()
  // Another driver's volume is not a directory a job can archive.
  await expect(page.getByRole("button", { name: "Back up…" })).toHaveCount(0)
  await expect(
    picture(page).getByRole("region", { name: "Mounted by" }).getByText("read-only"),
  ).toBeVisible()
})

test("an anonymous volume is its short name on the line, and whole under Configuration", async ({
  page,
}) => {
  const hash = VOLUMES[8].name
  await open(page, `/docker/volumes/${hash}?tab=configuration`)

  const line = identity(page, hash)
  await expect(line.getByText(`${hash.slice(0, 12)}…`)).toBeVisible()
  await expect(line.getByRole("button", { name: "prune deletes it" })).toBeVisible()
  await expect(page.getByRole("tabpanel").getByText(hash, { exact: true })).toBeVisible()
  await expect(page.getByText("com.docker.volume.anonymous")).toBeVisible()
})

test("on a phone it is one pane: no rail, nothing cut, the picture as marks", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, "/docker/volumes/shop_uploads")

  const line = identity(page, "shop_uploads")
  await expect(line.getByRole("button", { name: "in use" })).toBeVisible()
  await expect(line.getByRole("button", { name: "Open in Files" })).toBeVisible()
  await expect(rail(page)).toBeHidden()
  // The way to the next volume is the two arrows beside the way back.
  await expect(page.getByRole("button", { name: "Next volume" })).toBeEnabled()
  // The marks are links still, named for a screen reader.
  await expect(picture(page).getByRole("link", { name: "Open shop-web-1" })).toBeVisible()
  await expect(picture(page).getByRole("link", { name: "Open Shop data" })).toBeVisible()

  for (const name of ["Mounts", "Backups", "Configuration", "Files"]) {
    await tab(page, name).click()
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, `${name} overflows`).toBeLessThanOrEqual(1)
  }
  await tab(page, "Mounts").click()
  const rows = page
    .getByRole("list", { name: "Containers mounting this volume" })
    .locator("[data-slot=choice-row]")
  const heights = await rows.evaluateAll((all) =>
    all.map((r) => Math.round(r.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)
  await expect(rows.nth(1).getByText("ro", { exact: true })).toBeVisible()
})
