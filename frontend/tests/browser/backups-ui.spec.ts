import { expect, test } from "@playwright/test"
import { mockWorkbench } from "./workbench-fixture"

/**
 * The Backups page after its 0.7.0 pass: no readings across the top, the
 * failures first, each job a card drawn as what it covers with its last runs
 * as a strip, and every thing on the server drawn as the product it is under a
 * meter of how much of it is covered.
 */

test.beforeEach(async ({ page }) => {
  await mockWorkbench(page)
})

test("the page opens on what needs doing, not on four figures", async ({ page }) => {
  await page.goto("/backups")
  await expect(page.getByRole("heading", { name: "Backups" })).toBeVisible()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  // Only jobs are findings: what is not covered is the coverage list's.
  await expect(page.getByText("Databases nightly failed", { exact: false }).first()).toBeVisible()
  await expect(page.getByText(/things on this server have no backup/)).toHaveCount(0)
  // The totals the Stored tile carried are the jobs' header now.
  await expect(page.getByText("3 jobs · 3.7 GB in 29 archives")).toBeVisible()
})

test("each job is its products, its outcome, its destination and its runs", async ({ page }) => {
  await page.goto("/backups")
  const cards = page.locator("[data-slot=choice-row]")
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
})

test("what the server has is drawn as its products, under a meter of the cover", async ({
  page,
}) => {
  await page.goto("/backups")
  await expect(page.getByText("1 of 13 protected", { exact: true })).toBeVisible()
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
