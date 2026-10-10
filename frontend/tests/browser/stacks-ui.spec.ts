import { expect, test, type Page } from "@playwright/test"
import { mockStacks } from "./stacks-fixture"

/**
 * The Stacks page after its overhaul: the server's identity line with the
 * verdict, the band of what each stack takes of the machine and what just
 * happened, and the stacks as one table of their containers.
 */

const table = (page: Page) => page.getByRole("table")
const stackRow = (page: Page, name: string) => page.locator(`[data-stack-row="${name}"]`)

/** Opens the page and waits for the stacks to land, past the layout's own Docker check. */
async function open(page: Page) {
  await page.goto("/docker/stacks")
  await expect(page.locator("[data-stack-row]").first()).toBeVisible({ timeout: 20_000 })
}

/** A press that leaves the page loads the next route's code first. */
const NAVIGATION = { timeout: 15_000 }

test("the page opens on the server, the stacks needing attention and the table worst first", async ({
  page,
}) => {
  await mockStacks(page)
  await open(page)

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("Docker 29.8.0")
  await expect(identity).toContainText("5 of 6 stacks deployed")
  await expect(identity).toContainText("3 stacks need attention")

  // Attention first, then running, stopped and never deployed — each by name.
  await expect(page.locator("[data-stack-row]")).toHaveCount(6)
  const order = await page
    .locator("[data-stack-row]")
    .evaluateAll((rows) => rows.map((row) => row.getAttribute("data-stack-row")))
  expect(order).toEqual(["analytics", "monitoring", "n8n", "shop", "mailserver", "wiki"])

  // The orphan is counted apart from the services the file declares.
  await expect(stackRow(page, "analytics")).toContainText("2 of 2 services up")
  await expect(stackRow(page, "wiki")).toContainText("2 services defined")
  await expect(page.getByText("0/0")).toHaveCount(0)
})

test("a container is a row of live readings under its stack", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  const api = table(page).getByRole("row").filter({ hasText: "ghcr.io/acme/shop-api:2.4.1" })
  await expect(api).toContainText("Running")
  await expect(api).toContainText("38.6%")
  await expect(api).toContainText("286.0 MB")
  await expect(api).toContainText("28% of 1 GB")
  await expect(api).toContainText("3000 → 3000")
  await expect(api.locator("svg[aria-label='CPU over the last hour']")).toBeVisible()

  // Killed for memory is not the same reading as stopped by hand.
  const alertmanager = table(page).getByRole("row").filter({ hasText: "prom/alertmanager" })
  await expect(alertmanager).toContainText("killed · exit 137")
  const mail = table(page).getByRole("row").filter({ hasText: "docker-mailserver:14.0" })
  await expect(mail).toContainText("exit 0")

  // Up, but failing its own check, is said as what it means.
  const n8n = table(page).getByRole("row").filter({ hasText: "n8nio/n8n" })
  await expect(n8n).toContainText("Unhealthy")
  await expect(n8n).toContainText("failing its check")
  await expect(n8n).toContainText("85% of 768 MB")

  // A service the file declares with no container behind it.
  await expect(
    table(page).getByRole("row").filter({ hasText: "declared in compose.yaml" }),
  ).toHaveCount(2)
})

test("the stack's row sums its containers and opens the stack", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  const shop = stackRow(page, "shop")
  await expect(shop).toContainText("4 of 4 services up")
  await expect(shop).toContainText("4 ports published")
  await expect(shop).toContainText("762.0 MB")

  await shop.getByRole("button", { name: "shop", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/stacks\/shop$/, NAVIGATION)
})

test("a container's row opens the container", async ({ page }) => {
  await mockStacks(page)
  await open(page)
  await table(page).getByRole("button", { name: "clickhouse", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/containers\/d2f3b5d9e6a7/, NAVIGATION)
})

test("the band names the heaviest stacks and narrows the table to one", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  const processor = page.getByRole("region", { name: "Processor by stack" })
  await expect(processor.getByRole("button", { name: "Only shop's containers" })).toBeVisible()
  const memory = page.getByRole("region", { name: "Memory by stack" })
  await memory.getByRole("button", { name: "Only analytics's containers" }).click()

  await expect(page.locator("[data-stack-row]")).toHaveCount(1)
  await expect(stackRow(page, "analytics")).toBeVisible()
  await page.getByRole("button", { name: "Showing analytics; press to show every stack" }).click()
  await expect(page.locator("[data-stack-row]")).toHaveCount(6)
})

test("recent says what Docker did to the stacks, and a line opens its stack", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  const recent = page.getByRole("region", { name: "Recent changes" })
  await expect(recent.getByText("unhealthy")).toBeVisible()
  // The oom and the exit it caused are one line.
  await expect(recent.getByText("killed for memory")).toHaveCount(1)
  await recent.getByRole("button", { name: /^Open monitoring: alertmanager/ }).click()
  await expect(page).toHaveURL(/\/docker\/stacks\/monitoring$/, NAVIGATION)
})

test("the state chips count and narrow, and the verdict presses the attention chip", async ({
  page,
}) => {
  await mockStacks(page)
  await open(page)

  const chip = page.getByRole("button", { name: /^Needs attention/ })
  await expect(chip).toContainText("3")
  await page
    .locator("[data-slot='host-identity']")
    .getByRole("button", { name: "3 stacks need attention" })
    .click()
  await expect(chip).toHaveAttribute("aria-pressed", "true")
  await expect(page.locator("[data-stack-row]")).toHaveCount(3)

  await page.getByRole("button", { name: /^Not deployed/ }).click()
  await expect(page.locator("[data-stack-row]")).toHaveCount(1)
  await expect(stackRow(page, "wiki")).toBeVisible()
})

test("searching an image narrows each stack to the services running it", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  await page.getByPlaceholder("Stack, service or image").fill("postgres")
  await expect(page.locator("[data-stack-row]")).toHaveCount(2)
  await expect(table(page).getByRole("button", { name: "db", exact: true })).toBeVisible()
  await expect(table(page).getByRole("button", { name: "cache", exact: true })).toHaveCount(0)
})

test("a stack folds its containers away and keeps them folded", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  await page.getByRole("button", { name: "Hide shop's containers" }).click()
  await expect(table(page).getByRole("button", { name: "cache", exact: true })).toHaveCount(0)
  await page.reload()
  await expect(page.getByRole("button", { name: "Show shop's containers" })).toBeVisible({
    timeout: 20_000,
  })
  await expect(table(page).getByRole("button", { name: "cache", exact: true })).toHaveCount(0)
})

test("a stack that is down deploys from the list", async ({ page }) => {
  const mocks = await mockStacks(page)
  await open(page)

  // The stack that was never deployed is the one brand command in the table.
  await stackRow(page, "wiki").getByRole("button", { name: "Deploy" }).click()
  await expect.poll(() => mocks.actions).toContain("wiki/up")
  await expect(stackRow(page, "shop").getByRole("button", { name: "Deploy" })).toHaveCount(0)
})

test("on a phone the stacks are drawn down the row with every reading kept", async ({ page }) => {
  await mockStacks(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page)

  await expect(page.getByRole("table")).toHaveCount(0)
  const api = page.locator("li").filter({ hasText: "ghcr.io/acme/shop-api:2.4.1" })
  await expect(api).toContainText("38.6% CPU")
  await expect(api).toContainText("286.0 MB")
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

test("a server with no stacks offers to create one", async ({ page }) => {
  await mockStacks(page, { stacks: [], containers: [], stats: [] })
  await page.goto("/docker/stacks")
  await expect(page.getByText("No compose stacks found")).toBeVisible({ timeout: 20_000 })
  await expect(page.getByRole("button", { name: "Create stack" })).toBeVisible()
})
