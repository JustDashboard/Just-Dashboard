import { expect, test, type Locator, type Page } from "@playwright/test"
import { LONG_NAME, folder, mockContextFiles } from "./files-context-fixture"

/**
 * Files that know the server: each folder and file says what it is to this
 * server, from what the other pages already know.
 *
 *   a row says the most telling fact in a phrase after its name, with the
 *   thing's own logo, and a row with it is the height of a row without it at
 *   a desk and on a phone; where there is no room the phrase gives way first;
 *
 *   a tile carries the fact's logo in its corner with the phrase as its
 *   tooltip, at the size of every other tile;
 *
 *   the details pane says everything known as sentences that lead to the page
 *   owning each thing, and for the folder on screen that it is not backed up;
 *
 *   the listing is drawn whether the context route fails or never answers.
 */

const row = (page: Page, name: string) => page.locator(`tr[data-entry-path='${folder}/${name}']`)
const tile = (page: Page, name: string) =>
  page.locator(`[data-file-grid] [data-entry-path='${folder}/${name}']`)
const inspector = (page: Page) => page.locator("[data-files-inspector]")

async function openFolder(page: Page) {
  await page.goto(`/files?path=${encodeURIComponent(folder)}`)
  await row(page, "plain").waitFor()
}

async function height(locator: Locator) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  return Math.round(box!.height)
}

test.describe("Files that know the server", () => {
  test("a row says the most telling fact, and the details pane says all of it", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockContextFiles(page)
    await openFolder(page)

    const phrase = row(page, "pgdata").locator("[data-context-phrase]")
    await expect(phrase).toHaveText("Postgres volume of shop-db")
    await expect(phrase.locator("img")).toHaveAttribute("src", /\/logos\//)
    await expect(row(page, "compose.yaml").locator("[data-context-phrase]")).toHaveText(
      "The stack shop's Compose file",
    )
    // Nothing known, nothing drawn; and "not backed up" is never a row's.
    await expect(row(page, "plain").locator("[data-context-phrase]")).toHaveCount(0)
    await expect(page.locator("[data-context-phrase]", { hasText: "Not backed up" })).toHaveCount(0)

    // The folder on screen, with nothing chosen: it is not backed up, and a
    // source still being read is named.
    const section = inspector(page).getByRole("region", { name: "On this server" })
    await expect(section.getByRole("link", { name: /Not backed up/ })).toHaveAttribute(
      "href",
      "/backups",
    )
    await expect(section).toContainText("Still reading Git")

    // A chosen entry: every fact as a sentence leading to its page.
    await row(page, "pgdata").locator("[data-file-name]").click()
    const volume = section.getByRole("link", { name: /Postgres volume of shop-db/ })
    await expect(volume).toHaveText("Postgres volume of shop-db, Docker volume shop_pgdata")
    await expect(volume).toHaveAttribute("href", "/docker/volumes/shop_pgdata")
    await expect(section.getByRole("link", { name: /Not backed up/ })).toBeVisible()

    await row(page, "shop").locator("[data-file-name]").click()
    await expect(section.getByRole("link", { name: /Compose stack shop/ })).toHaveAttribute(
      "href",
      "/docker/stacks/shop",
    )
  })

  for (const viewport of [
    { width: 1280, height: 900 },
    { width: 390, height: 844 },
  ]) {
    test(`a row with a phrase is one height with a row without, at ${viewport.width}`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport)
      await mockContextFiles(page)
      await openFolder(page)
      await expect(row(page, LONG_NAME).locator("[data-context-phrase]")).toBeAttached()
      const plain = await height(row(page, "plain"))
      expect(await height(row(page, "pgdata"))).toBe(plain)
      expect(await height(row(page, LONG_NAME))).toBe(plain)
      expect(await height(row(page, "notes.md"))).toBe(await height(row(page, "compose.yaml")))

      if (viewport.width === 1280) {
        // A long name and a long phrase: the phrase is cut, the name is not.
        const name = row(page, LONG_NAME).locator("[data-file-name] span")
        const words = row(page, LONG_NAME).locator("[data-context-phrase] .truncate")
        const cut = (l: Locator) => l.evaluate((el) => el.scrollWidth > el.clientWidth + 1)
        expect(await cut(name)).toBe(false)
        expect(await cut(words)).toBe(true)
      }
    })
  }

  test("a tile carries the fact's logo, the phrase as its tooltip, at every tile's size", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await mockContextFiles(page)
    await openFolder(page)
    await page.getByRole("radio", { name: "Tiles", exact: true }).click()
    const mark = tile(page, "pgdata").locator("[data-context-mark]")
    await expect(mark).toHaveAttribute("title", "Postgres volume of shop-db")
    await expect(mark).toHaveAttribute("aria-label", "Postgres volume of shop-db")
    await expect(tile(page, "plain").locator("[data-context-mark]")).toHaveCount(0)
    const a = await tile(page, "pgdata").boundingBox()
    const b = await tile(page, "plain").boundingBox()
    expect(Math.round(a!.height)).toBe(Math.round(b!.height))
    expect(Math.round(a!.width)).toBe(Math.round(b!.width))
  })

  for (const answer of ["fail", "hang"] as const) {
    test(`the listing is drawn when the context route ${answer === "fail" ? "fails" : "never answers"}`, async ({
      page,
    }) => {
      const errors: string[] = []
      page.on("pageerror", (error) => errors.push(error.message))
      await page.setViewportSize({ width: 1280, height: 900 })
      await mockContextFiles(page, answer)
      await openFolder(page)
      for (const name of ["pgdata", "shop", LONG_NAME, "plain", "compose.yaml", "notes.md"]) {
        await expect(row(page, name)).toBeVisible()
      }
      await row(page, "pgdata").locator("[data-file-name]").click()
      await expect(inspector(page).getByRole("heading", { name: "pgdata" })).toBeVisible()
      await expect(page.locator("[data-context-phrase]")).toHaveCount(0)
      await expect(inspector(page).getByRole("region", { name: "On this server" })).toHaveCount(0)
      expect(errors).toEqual([])
    })
  }
})

test.describe("Files that know the server, on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test("the details pane says what an entry is", async ({ page }) => {
    await mockContextFiles(page)
    await openFolder(page)
    await expect(row(page, "pgdata").locator("[data-context-phrase]")).toBeAttached()
    await row(page, "pgdata").locator("[data-file-name]").click()
    const bar = page.getByRole("navigation", { name: "Files", exact: true })
    await bar.getByRole("button", { name: "Details", exact: true }).click()
    const section = inspector(page).getByRole("region", { name: "On this server" })
    await expect(section.getByRole("link", { name: /Postgres volume of shop-db/ })).toBeVisible()
    // One pane: the listing is not drawn under it.
    await expect(row(page, "plain")).toBeHidden()
  })
})
