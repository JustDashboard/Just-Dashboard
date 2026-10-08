import { expect, test, type Page } from "@playwright/test"
import { CONTAINERS, VOLUMES, mockVolumes } from "./docker-volumes-fixture"

/**
 * /docker/volumes after its overhaul: Docker's identity line with what a prune
 * would delete as its verdict, a band of who holds the data beside where each
 * volume stands, and the volumes as one framed table whose widest column is
 * the containers mounting each one. The fixture is a self-hosting server with
 * fifteen volumes, a stack taken down among them.
 */

const body = (page: Page) => page.locator("[data-slot=table-body] tr")
const row = (page: Page, name: string) => page.getByRole("row", { name: new RegExp(name) })

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
})

test("the page opens on the engine, who holds the data and where each volume stands", async ({
  page,
}) => {
  await mockVolumes(page)
  await page.goto("/docker/volumes")

  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity.getByText("29.8.0")).toBeVisible()
  await expect(identity.getByText("16 volumes")).toBeVisible()
  await expect(identity.getByText("31.4 GB stored")).toBeVisible()
  await expect(identity.getByText("11 mounted")).toBeVisible()
  await expect(identity.getByText("4 of 16 backed up")).toBeVisible()
  await expect(identity.getByText("4 volumes a prune would delete")).toBeVisible()

  const held = page.getByRole("region", { name: "Who holds the data" })
  await expect(held.getByRole("img", { name: /^Held by: minio 14\.2 GB, shop/ })).toBeVisible()
  await expect(
    held.getByRole("button", { name: "Only the volumes nextcloud holds" }),
  ).toContainText("stack is down")
  const mounts = page.getByRole("region", { name: "Where each volume stands" })
  await expect(mounts.getByRole("button", { name: /in use$/ })).toContainText("10")
  await expect(mounts.getByRole("button", { name: /held by stopped containers$/ })).toContainText(
    "1",
  )
  await expect(mounts.getByRole("button", { name: /left by a stack$/ })).toContainText("2")
  await expect(mounts.getByRole("button", { name: "Prune 2.9 GB" })).toBeVisible()

  for (const name of ["Volume", "Mounted by", "State", "Size"]) {
    await expect(page.getByRole("columnheader", { name, exact: true })).toBeVisible()
  }
  await expect(body(page)).toHaveCount(VOLUMES.length)
  // Largest first.
  await expect(body(page).first()).toContainText("minio-data")
})

/**
 * The page used to call a stopped container's volume one a prune would
 * delete. The daemon counts that mount and its prune keeps the volume; what a
 * prune takes is a volume nothing mounts, as a stack taken down leaves.
 */
test("a stopped container's volume is held, and a stack taken down is what a prune takes", async ({
  page,
}) => {
  const mocks = await mockVolumes(page)
  await page.goto("/docker/volumes")

  const n8n = row(page, "n8n_data")
  await expect(n8n.getByText("stopped", { exact: true })).toBeVisible()
  await expect(n8n.getByText(/prune/)).toHaveCount(0)
  await expect(
    n8n.getByRole("button", { name: /^Remove — mounted by 1 container$/ }),
  ).toBeDisabled()

  const left = row(page, "nextcloud_db")
  await expect(left.getByText("nextcloud is down")).toBeVisible()
  await expect(left.getByText("prune deletes it")).toBeVisible()

  // A plugin volume nothing mounts would not be taken either — but this one is mounted.
  await expect(row(page, "jellyfin-media").getByText("not measurable")).toBeVisible()
  await expect(row(page, "scratch").getByText("empty", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Prune 2.9 GB" }).click()
  const dialog = page.getByRole("dialog", { name: "Prune volumes" })
  await expect(dialog.getByText("Deletes 4 volumes and the 2.9 GB in them")).toBeVisible()
  const listed = dialog.getByRole("listitem")
  await expect(listed).toHaveCount(4)
  for (const name of ["nextcloud_db", "nextcloud_html", "scratch"]) {
    await expect(listed.filter({ hasText: name })).toHaveCount(1)
  }
  await expect(listed.filter({ hasText: "n8n_data" })).toHaveCount(0)
  await dialog.getByRole("button", { name: "Prune" }).click()
  await expect.poll(() => mocks.calls).toContain("POST /docker/volumes/prune")
  await expect(page.getByText("Deleted 4 volumes, reclaimed 2.9 GB")).toBeVisible()
  await expect(body(page)).toHaveCount(VOLUMES.length - 4)
  await expect(page.getByText("A prune would delete nothing", { exact: true })).toBeVisible()
})

test("the band, the verdict and the chips narrow the table, and Escape lets go", async ({
  page,
}) => {
  await mockVolumes(page)
  await page.goto("/docker/volumes")

  await page.getByRole("button", { name: "Only the volumes shop holds" }).first().click()
  await expect(body(page)).toHaveCount(3)
  await expect(page.getByRole("button", { name: "Clear holder filter" })).toBeVisible()
  await page.getByLabel("Find volumes").focus()
  await page.keyboard.press("Escape")
  await expect(body(page)).toHaveCount(VOLUMES.length)

  await page.getByRole("button", { name: "Only volumes: left by a stack" }).click()
  await expect(body(page)).toHaveCount(2)
  await page.getByRole("button", { name: "Clear mount filter" }).click()

  // The verdict narrows to exactly what it counts: the CIFS volume nothing
  // mounts is not mounted, and a prune leaves it alone.
  await page.getByRole("button", { name: "Only the 4 volumes a prune would delete" }).click()
  await expect(body(page)).toHaveCount(4)
  await expect(row(page, "nas-backups")).toHaveCount(0)
  await page.getByRole("button", { name: "Clear prune filter" }).click()
  await page.getByRole("button", { name: /^Not mounted/ }).click()
  await expect(body(page)).toHaveCount(5)

  await page.getByRole("button", { name: /^Not backed up/ }).click()
  await expect(body(page)).toHaveCount(12)
  await expect(row(page, "minio-data").getByText("backup paused")).toBeVisible()

  // A stack's name in a row narrows to it too.
  await page.getByRole("button", { name: /^All/ }).click()
  await row(page, "monitoring_grafana")
    .getByRole("button", { name: "Only the volumes monitoring holds" })
    .click()
  await expect(body(page)).toHaveCount(2)
})

test("every column heading orders the table", async ({ page }) => {
  await mockVolumes(page)
  await page.goto("/docker/volumes")

  const size = page.getByRole("columnheader", { name: "Size" })
  await expect(size).toHaveAttribute("aria-sort", "descending")
  await page.getByRole("button", { name: "Volume", exact: true }).click()
  await expect(page.getByRole("columnheader", { name: "Volume" })).toHaveAttribute(
    "aria-sort",
    "ascending",
  )
  // The anonymous volume's hash sorts first, then the rest by name.
  await expect(body(page).nth(1)).toContainText("jellyfin-config")
  await page.getByRole("button", { name: "Mounted by", exact: true }).click()
  await expect(body(page).first()).toContainText("shop_uploads")
})

test("the containers mounting a volume are named, with their path and access", async ({ page }) => {
  await mockVolumes(page)
  await page.goto("/docker/volumes")

  const uploads = row(page, "shop_uploads")
  await expect(uploads.getByRole("link", { name: "shop-web-1" })).toHaveAttribute(
    "href",
    `/docker/containers/${CONTAINERS[0].id}`,
  )
  await expect(uploads.getByRole("link", { name: "shop-worker-1" })).toBeVisible()
  await expect(uploads.getByText("/app/uploads")).toHaveCount(2)
  await expect(uploads.getByText("ro", { exact: true })).toBeVisible()
  await expect(uploads.getByText(/^backed up 3h \d+m ago$/)).toBeVisible()
})

test("a container stopping is seen without waiting for the list", async ({ page }) => {
  const mocks = await mockVolumes(page)
  await page.goto("/docker/volumes")

  const pg = row(page, "shop_pgdata")
  await expect(pg.getByText("in use", { exact: true })).toBeVisible()
  mocks.frame(CONTAINERS.map((c) => (c.name === "shop-db-1" ? { ...c, state: "exited" } : c)))
  await expect(pg.getByText("stopped", { exact: true })).toBeVisible()
})

test("a volume's sheet reads it, its containers and what a prune would do", async ({ page }) => {
  await mockVolumes(page)
  await page.goto("/docker/volumes?volume=shop_uploads")

  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("heading", { name: /shop_uploads/ })).toBeVisible()
  const readout = sheet.locator("[data-slot=volume-readout]")
  await expect(readout.getByText("6.1 GB")).toBeVisible()
  await expect(readout.getByText("all running")).toBeVisible()
  await expect(readout.getByText("Shop data")).toBeVisible()
  const mounted = sheet.getByRole("row", { name: /shop-worker-1/ })
  await expect(mounted.getByText("read-only")).toBeVisible()
  await expect(mounted.getByText("/app/uploads")).toBeVisible()
  await expect(sheet.getByText("com.docker.compose.project")).toBeVisible()
  await expect(sheet.getByRole("button", { name: /^Remove/ })).toBeDisabled()
  await page.keyboard.press("Escape")
  await expect(page).toHaveURL(/\/docker\/volumes$/)

  await page.getByRole("button", { name: "nextcloud_db", exact: true }).click()
  const notice = page.getByRole("dialog").getByText("Nothing mounts this volume")
  await expect(notice).toBeVisible()
  await expect(
    page.getByRole("dialog").getByText(/was taken down and left it behind/),
  ).toBeVisible()
  await expect(
    page.getByRole("dialog").getByRole("link", { name: "Open nextcloud to deploy it again" }),
  ).toHaveAttribute("href", "/docker/stacks/nextcloud")
  await expect(page.getByRole("dialog").getByRole("button", { name: "Back up…" })).toBeVisible()
})

test("removing a volume nothing mounts deletes it and its row leaves", async ({ page }) => {
  const mocks = await mockVolumes(page)
  await page.goto("/docker/volumes")

  await row(page, "scratch").getByRole("button", { name: "Remove", exact: true }).click()
  await page
    .getByRole("dialog", { name: "Delete volume" })
    .getByRole("button", { name: "Delete" })
    .click()
  await expect.poll(() => mocks.calls).toContain("DELETE /docker/volumes/scratch")
  await expect(row(page, "scratch")).toHaveCount(0)
})

test("a new volume rises into the table", async ({ page }) => {
  const mocks = await mockVolumes(page)
  await page.goto("/docker/volumes")

  await page.getByRole("button", { name: "Create volume" }).first().click()
  await page.getByLabel("Name").fill("photos")
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click()
  await expect.poll(() => mocks.calls).toContain("POST /docker/volumes/")
  await expect(row(page, "photos")).toHaveClass(/animate-rise/)
})

test("a reader sees the readings and none of the controls", async ({ page }) => {
  await mockVolumes(page, { capabilities: ["read"], noBackups: true })
  await page.goto("/docker/volumes")

  await expect(body(page)).toHaveCount(VOLUMES.length)
  await expect(page.getByRole("button", { name: "Create volume" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Prune/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Remove/ })).toHaveCount(0)
  // No coverage answer, so no backup chip or fact rather than a wrong one.
  await expect(page.getByRole("button", { name: /^Not backed up/ })).toHaveCount(0)
  await expect(page.getByText(/backed up/)).toHaveCount(0)
})

test("on a phone each row keeps its name, standing and size", async ({ page }) => {
  await mockVolumes(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/volumes")

  await expect(page.getByRole("columnheader", { name: "Mounted by" })).toHaveCount(0)
  const left = row(page, "nextcloud_db")
  await expect(left.getByText("prune deletes")).toBeVisible()
  await expect(left.getByText("1.9 GB")).toBeVisible()
  await expect(row(page, "shop_uploads").getByText("2 containers")).toBeVisible()
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

test("a volume another filesystem backs is kept by a prune, and its password is not shown", async ({
  page,
}) => {
  await mockVolumes(page)
  await page.goto("/docker/volumes")

  const nas = row(page, "nas-backups")
  await expect(nas.getByText("cifs mount")).toBeVisible()
  await expect(nas.getByText("not measurable")).toBeVisible()
  await expect(nas.getByText("not mounted", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "nas-backups", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText(/A prune leaves it alone/)).toBeVisible()
  await expect(
    sheet.getByText("addr=10.0.0.5,username=backup,password=••••••,vers=3.0"),
  ).toBeVisible()
  await expect(sheet.getByText(/hunter2/)).toHaveCount(0)
})

test("a volume Docker refuses to remove says why and stays", async ({ page }) => {
  await mockVolumes(page, { refuse: ["scratch"] })
  await page.goto("/docker/volumes")

  await row(page, "scratch").getByRole("button", { name: "Remove", exact: true }).click()
  await page
    .getByRole("dialog", { name: "Delete volume" })
    .getByRole("button", { name: "Delete" })
    .click()
  await expect(page.getByText("Delete volume failed")).toBeVisible()
  await expect(page.getByText(/volume is in use/)).toBeVisible()
  // The confirmation stays open on a refusal; once it is closed the row is still there.
  await page.keyboard.press("Escape")
  await expect(row(page, "scratch")).toHaveCount(1)
})

/**
 * Docker decides as the prune runs. A volume it deleted that the dialog did
 * not name — unmounted after the list was read — is said, not left to be found.
 */
test("a prune that deleted more than it named says which", async ({ page }) => {
  await mockVolumes(page, { pruned: ["nextcloud_db", "nextcloud_html", "scratch", "n8n_data"] })
  await page.goto("/docker/volumes")

  await page.getByRole("button", { name: "Prune 2.9 GB" }).click()
  await page
    .getByRole("dialog", { name: "Prune volumes" })
    .getByRole("button", { name: "Prune" })
    .click()
  await expect(page.getByText(/^Deleted 4 volumes/)).toBeVisible()
  await expect(page.getByText(/Not on the list.*n8n_data/)).toBeVisible()
  // And the one it named that Docker kept, mounted again by then.
  await expect(
    page.getByText(/Kept because a container mounted it by then: 3f9ad2c8/),
  ).toBeVisible()
})
