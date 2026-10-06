import { expect, test, type Page } from "@playwright/test"
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
  // Only jobs are findings: what is not covered is the coverage list's.
  await expect(page.getByText("Databases nightly failed", { exact: false }).first()).toBeVisible()
  await expect(page.getByText(/things on this server have no backup/)).toHaveCount(0)
  // The totals the Stored tile carried are the jobs' header now, and the picture's middle.
  await expect(page.getByText("3 jobs · 3.7 GB in 29 archives")).toBeVisible()
  await expect(page.getByText("3 jobs · 3.7 GB", { exact: true })).toBeVisible()
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

  await expect(page.locator("[data-slot=stat-tile]").filter({ hasText: "Holding" })).toContainText(
    "1.7 GB",
  )
  await expect(page.getByText("14 runs · 2 failed")).toBeVisible()

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
