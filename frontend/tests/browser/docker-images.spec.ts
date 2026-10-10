import { expect, test, type Page } from "@playwright/test"
import { IMAGES, mockImages } from "./docker-images-fixture"

/**
 * /docker/images after its overhaul: the engine's identity line, a band of
 * what Docker holds on disk beside what the registries say, and the images as
 * one framed table whose rows open a sheet. The fixture is a self-hosting
 * server with fourteen images, two of them behind their registry.
 */

const body = (page: Page) => page.locator("[data-slot=table-body] tr")
const row = (page: Page, name: RegExp) => page.getByRole("row", { name })

test("the page opens on the engine, what it holds and what the registries say", async ({
  page,
}) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/images")

  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity.getByText("29.8.0")).toBeVisible()
  await expect(identity.getByText("14 images")).toBeVisible()
  await expect(identity.getByText("2 updates available")).toBeVisible()

  const disk = page.getByRole("region", { name: "Docker on disk" })
  await expect(disk.getByRole("img", { name: /^On disk: Images/ })).toBeVisible()
  await expect(disk.getByText("48 entries · 0 in use")).toBeVisible()
  const registry = page.getByRole("region", { name: "What the registries say" })
  await expect(registry.getByRole("button", { name: /update available/ })).toContainText("2")
  await expect(registry.getByRole("button", { name: /pinned by digest/ })).toContainText("1")

  for (const name of ["Image", "Containers", "Registry", "Created", "Size"]) {
    await expect(page.getByRole("columnheader", { name, exact: true })).toBeVisible()
  }
  await expect(body(page)).toHaveCount(IMAGES.length)
  // Largest first: the 1.1 GB workflow engine leads.
  await expect(body(page).first()).toContainText("n8nio/n8n")

  const nginx = row(page, /nginx:1\.27-alpine/)
  await expect(nginx.getByRole("link", { name: "web" })).toHaveAttribute(
    "href",
    "/docker/containers/1111111111111111",
  )
  await expect(nginx.getByRole("link", { name: "docs" })).toBeVisible()
  await expect(nginx.getByText("update available")).toBeVisible()
  await expect(nginx.locator("img")).toHaveAttribute("src", "/logos/nginx.svg")
})

test("a registry answer, the verdict and the use chips each narrow the table", async ({ page }) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/images")

  await page.getByRole("button", { name: "Only images that are update available" }).click()
  await expect(body(page)).toHaveCount(2)
  await expect(body(page).nth(0)).toContainText("prom/prometheus")
  await expect(body(page).nth(1)).toContainText("nginx")
  await page.getByRole("button", { name: "Clear registry filter" }).click()
  await expect(body(page)).toHaveCount(IMAGES.length)

  await page.getByRole("button", { name: "Only the 2 images with an update" }).click()
  await expect(body(page)).toHaveCount(2)
  await page.getByRole("button", { name: "Clear registry filter" }).click()

  await page.getByRole("button", { name: /^Not in use/ }).click()
  await expect(body(page)).toHaveCount(3)
  await page.getByRole("button", { name: /^Untagged/ }).click()
  await expect(body(page)).toHaveCount(2)
  await expect(body(page).first()).toContainText("untagged")

  await page.getByRole("button", { name: /^All/ }).click()
  await page.getByRole("button", { name: "Order: Largest first" }).click()
  await expect(body(page).first()).toContainText("acme/api")
})

test("a row opens its sheet: who runs it and where the size went", async ({ page }) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/images")

  await row(page, /nginx:1\.27-alpine/)
    .getByRole("button", { name: "nginx:1.27-alpine", exact: true })
    .click()
  await expect(page).toHaveURL(/\?image=sha256%3Aa1a1|\?image=sha256:a1a1/)
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("Where the size went")).toBeVisible()
  await expect(sheet.getByText("5 layers · 48.0 MB")).toBeVisible()
  // In build order: the base layer first.
  const steps = sheet.locator("ol").first().locator("li")
  await expect(steps.first()).toContainText("ADD")
  await expect(sheet.getByRole("link", { name: "Open web" })).toHaveAttribute(
    "href",
    "/docker/containers/1111111111111111",
  )
  await expect(sheet.getByText("Update available")).toBeVisible()
  // In use: the sheet draws Remove, and Docker would refuse it.
  await expect(sheet.getByRole("button", { name: "Remove" })).toBeDisabled()
  await expect(sheet.getByRole("button", { name: "Pull" })).toBeEnabled()
})

test("a pull draws its layers and does not start again once it ends", async ({ page }) => {
  const mocks = await mockImages(page, { pull: "finish" })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/images")

  await page.getByRole("button", { name: "Pull image" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Pull an image" })
  await dialog.getByRole("textbox", { name: "Image reference" }).fill("valkey/valkey:8")
  await dialog.getByRole("button", { name: "Pull", exact: true }).click()

  const progress = dialog.getByRole("region", { name: "Pull progress" })
  await expect(progress.getByText("already here")).toBeVisible()
  await expect(progress.getByText("6.29MB / 31.4MB")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Done" })).toBeVisible()

  // The server closed the socket when the pull ended. A socket left enabled
  // reconnected a second later, and every reconnection was another pull.
  await page.waitForTimeout(2500)
  expect(mocks.pulls).toBe(1)
  await dialog.getByRole("button", { name: "Done" }).click()
  // The pulled image has arrived in the table.
  await expect(row(page, /valkey\/valkey:8/)).toBeVisible()
  await page.waitForTimeout(1500)
  expect(mocks.pulls).toBe(1)
})

test("each disk line reclaims only its own kind", async ({ page }) => {
  const mocks = await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/images")
  const disk = page.getByRole("region", { name: "Docker on disk" })

  await disk.getByRole("button", { name: "Reclaim", exact: true }).first().click()
  await page
    .getByRole("dialog", { name: "Remove unused images" })
    .getByRole("button", { name: "Remove" })
    .click()
  await expect.poll(() => mocks.calls).toContain("POST /docker/images/prune?all=true")

  await disk.getByRole("button", { name: "Reclaim", exact: true }).nth(1).click()
  await page
    .getByRole("dialog", { name: "Empty the build cache" })
    .getByRole("button", { name: "Empty it" })
    .click()
  await expect.poll(() => mocks.calls).toContain("POST /docker/build-cache/prune")

  // Neither line ran the sweep, which also removes stopped containers.
  expect(mocks.calls.some((c) => c.startsWith("POST /docker/prune"))).toBe(false)
})

test("an image nobody runs can be removed, and any image can be tagged", async ({ page }) => {
  const mocks = await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/images")

  await expect(row(page, /postgres:16/).getByRole("button", { name: "Remove" })).toBeDisabled()
  await row(page, /mongo:7/)
    .getByRole("button", { name: "Remove" })
    .click()
  await page
    .getByRole("dialog", { name: "Delete image" })
    .getByRole("button", { name: "Delete" })
    .click()
  await expect.poll(() => mocks.calls.join("\n")).toContain("DELETE /docker/images/sha256%3Afafa")
  await expect(row(page, /mongo:7/)).toHaveCount(0)

  await row(page, /nginx:1\.27-alpine/)
    .getByRole("button", { name: "More actions for nginx:1.27-alpine" })
    .click()
  await page.getByRole("menuitem", { name: "Tag…" }).click()
  const dialog = page.getByRole("dialog", { name: "Tag nginx:1.27-alpine" })
  await expect(dialog.getByRole("textbox", { name: "New tag" })).toHaveValue(
    "nginx:1.27-alpine-previous",
  )
  await dialog.getByRole("button", { name: "Tag", exact: true }).click()
  await expect
    .poll(() => mocks.calls.join("\n"))
    .toContain('/tag {"tag":"nginx:1.27-alpine-previous"}')
})

test("a reader without control sees the readings and no verbs", async ({ page }) => {
  await mockImages(page, { capabilities: ["read"] })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/images")

  await expect(body(page)).toHaveCount(IMAGES.length)
  await expect(page.getByRole("button", { name: "Pull image" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Reclaim/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Prune untagged" })).toHaveCount(0)
})

test("on a phone the table keeps the name, its answers, the size and the menu", async ({
  page,
}) => {
  await mockImages(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/images")

  await expect(page.getByRole("columnheader")).toHaveCount(3)
  const nginx = row(page, /nginx:1\.27-alpine/)
  await expect(nginx.getByTitle("web, docs")).toHaveText("2")
  await expect(nginx.getByText("update available")).toBeVisible()
  await expect(
    nginx.getByRole("button", { name: "More actions for nginx:1.27-alpine" }),
  ).toBeVisible()
})

for (const width of [390, 768, 1024, 1280, 1720]) {
  test(`the images page does not scroll sideways at ${width}px`, async ({ page }) => {
    await mockImages(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/docker/images")
    await expect(body(page)).toHaveCount(IMAGES.length)
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow).toBeLessThanOrEqual(1)
    // The table fits its frame: no row's verbs sit past an edge.
    const clipped = await page
      .locator("[data-slot=table-container]")
      .evaluate((el) => el.scrollWidth - el.clientWidth)
    expect(clipped).toBeLessThanOrEqual(1)
  })
}
