import { expect, test, type Page } from "@playwright/test"
import { APP, BLOG, CACHE, NOTES, SHOP } from "./database-fixture"
import { mockFleet, refusal, type FleetMock } from "./database-fleet-fixture"

/**
 * When the control center says a database was last backed up.
 *
 * The fleet's `lastBackup` is the newer of the database's newest dump and the
 * newest backup job run that dumped it; the dump summary lists only the dump
 * directories, and is read every few seconds while a dump runs. The page takes
 * the later of the two, so a database a nightly job protects is not called
 * never backed up, or a week old, because nobody pressed Back up now — and a
 * dump taken since the fleet was last read still counts at once.
 */

const hoursAgo = (hours: number) => new Date(Date.now() - hours * 3_600_000).toISOString()
const card = (page: Page, name: string) =>
  page
    .locator("[data-card=database]")
    .filter({ has: page.getByRole("link", { name: `Open ${name}` }) })
const band = (page: Page) => page.locator("[data-slot=fleet-band]")
const backups = (page: Page) => band(page).getByRole("region", { name: "Backups" })
const tableRow = (page: Page, name: string) =>
  page
    .getByRole("table")
    .getByRole("row")
    .filter({ has: page.getByRole("link", { name: `Open ${name}` }) })
const scroller = (page: Page) => page.locator("[data-slot=page]").first().locator("xpath=..")
/** An age as the page writes it: "5h ago", or "5h 12m ago" once the clock has moved on. */
const backedUp = (hours: number) => new RegExp(`backed up ${hours}h(?: \\d+[ms])? ago`)

/**
 * shop's newest dump is ten days old and a job dumped it five hours ago;
 * cache is dumped by its job alone; blog was dumped by hand an hour ago,
 * after the fleet last read its job's run; notes was dumped three hours ago;
 * nothing backs app up. Each time is some minutes past the hour, as a real
 * one would be.
 */
const JOBS: FleetMock = {
  dumps: { [SHOP.id]: 240.6, [CACHE.id]: null, [BLOG.id]: 1.2, [APP.id]: null, [NOTES.id]: 3.4 },
  fleet: {
    [SHOP.id]: { lastBackup: hoursAgo(5.2) },
    [CACHE.id]: { lastBackup: hoursAgo(2.7) },
    [BLOG.id]: { lastBackup: hoursAgo(20.4) },
  },
}

const NOTHING: FleetMock = {
  dumps: { [SHOP.id]: null, [CACHE.id]: null, [BLOG.id]: null, [APP.id]: null, [NOTES.id]: null },
}

test.describe("the last backup the control center reads", () => {
  test("a backup job's run is a backup on the card, in the band and in the table", async ({
    page,
  }) => {
    await mockFleet(page, JOBS)
    await page.goto("/databases")

    // Not the ten-day-old dump, and not drawn as a concern.
    await expect(card(page, "shop")).toContainText(backedUp(5))
    await expect(card(page, "shop").getByText(backedUp(5))).not.toHaveClass(/text-warning/)
    await expect(card(page, "cache")).toContainText(backedUp(2))
    await expect(card(page, "cache")).not.toContainText("never backed up")
    // The dump the summary heard of first is newer than the job's run.
    await expect(card(page, "blog")).toContainText(backedUp(1))
    await expect(card(page, "app").getByText("never backed up")).toHaveClass(/text-warning/)

    await expect(backups(page)).toContainText(/4\s*of 5 in the last day/)
    await expect(
      backups(page).getByRole("img", {
        name: /^Backups: 1 never, 4 today$/,
      }),
    ).toBeVisible()
    // Never backed up first; each standing opens the oldest in it.
    expect(
      await backups(page)
        .getByRole("list", { name: "Databases by last backup" })
        .getByRole("link")
        .evaluateAll((links) => links.map((link) => link.getAttribute("aria-label"))),
    ).toEqual(["Backups of app", "Backups of shop"])

    await page.getByRole("radio", { name: "Table" }).click()
    await expect(tableRow(page, "shop")).toContainText(/5h(?: \d+[ms])? ago/)
    await expect(tableRow(page, "cache")).toContainText(/2h(?: \d+[ms])? ago/)
    await expect(tableRow(page, "app")).toContainText("never")
    await page.getByRole("radio", { name: "Cards" }).click()
  })

  test("a database neither dumped nor in a backup job is still never backed up", async ({
    page,
  }) => {
    await mockFleet(page, NOTHING)
    await page.goto("/databases")
    for (const name of ["shop", "blog", "cache", "app", "notes"]) {
      await expect(card(page, name).getByText("never backed up")).toBeVisible()
    }
    await expect(backups(page).getByRole("img", { name: /^Backups: 5 never$/ })).toBeVisible()
  })

  test("when the dump directories cannot be read, the fleet still says when each was backed up", async ({
    page,
  }) => {
    const server = await mockFleet(page, JOBS)
    server.answer("GET /databases/backups/summary", () =>
      refusal(500, "internal", "the dump directories could not be read"),
    )
    await page.goto("/databases")
    await expect(card(page, "shop")).toContainText(backedUp(5))
    await expect(card(page, "cache")).toContainText(backedUp(2))
    // Without the summary, blog's newest is the fleet's reading.
    await expect(card(page, "blog")).toContainText(backedUp(20))
    await expect(card(page, "app")).toContainText("never backed up")
  })

  test("a role that only reads is told the same, and offered no backup", async ({ page }) => {
    await mockFleet(page, { ...JOBS, viewer: true })
    await page.goto("/databases")
    await expect(card(page, "shop")).toContainText(backedUp(5))
    await expect(card(page, "cache")).toContainText(backedUp(2))
    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await expect(page.getByRole("menuitem")).toHaveText(["Settings"])
  })

  test("at a phone's width the cards say it without scrolling sideways", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockFleet(page, JOBS)
    await page.goto("/databases")
    await card(page, "shop").scrollIntoViewIfNeeded()
    await expect(card(page, "shop")).toContainText(backedUp(5))
    await expect(card(page, "cache")).toContainText(backedUp(2))
    expect(await scroller(page).evaluate((el) => el.scrollWidth > el.clientWidth + 1)).toBe(false)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth),
    ).toBe(false)
  })

  test("the readings a backup job's run changes @shots", async ({ page }) => {
    test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the screenshots go to")
    const shot = (name: string) => page.screenshot({ path: `${process.env.JD_SHOTS}/${name}.png` })
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockFleet(page, JOBS)
    await page.goto("/databases")
    await expect(backups(page)).toContainText(/4\s*of 5 in the last day/)
    await backups(page).scrollIntoViewIfNeeded()
    await shot("fleet-band-1440")

    // The shelves from their top, so every card is in the picture rather than
    // the band with the first row under it.
    await expect(card(page, "shop")).toContainText(backedUp(5))
    await page
      .getByRole("list", { name: "Containers" })
      .locator("xpath=../..")
      .evaluate((shelves) => shelves.scrollIntoView({ block: "start" }))
    await scroller(page).evaluate((el) => el.scrollBy(0, -16))
    await shot("fleet-cards-1440")

    await page.getByRole("radio", { name: "Table" }).click()
    await expect(tableRow(page, "shop")).toContainText(/5h(?: \d+[ms])? ago/)
    await page.getByRole("table").scrollIntoViewIfNeeded()
    await shot("fleet-table-1440")
    await page.getByRole("radio", { name: "Cards" }).click()

    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto("/databases")
    await card(page, "shop").scrollIntoViewIfNeeded()
    await expect(card(page, "shop")).toContainText(backedUp(5))
    await shot("fleet-cards-390")
  })
})
