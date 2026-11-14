import { expect, test, type Page } from "@playwright/test"
import { mockStacks, user } from "./stacks-fixture"
import { risen } from "./risen"

/**
 * The Stacks page: Docker's identity line with the verdict, the band of what
 * each stack takes of the machine and what just happened, and the stacks as
 * one-line cards that open them.
 */

const rows = (page: Page) =>
  page.getByRole("list", { name: "Stacks" }).locator("[data-slot=choice-row]")
const stackRow = (page: Page, name: string) =>
  rows(page).filter({ has: page.locator(`[data-workspace-item="stack:${name}"]`) })

/** Opens the page and waits for the stacks to land, past the layout's own Docker check. */
async function open(page: Page) {
  await page.goto("/docker/stacks")
  await expect(rows(page).first()).toBeVisible({ timeout: 20_000 })
}

/** A press that leaves the page loads the next route's code first. */
const NAVIGATION = { timeout: 15_000 }

async function readOnly(page: Page) {
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        ...user,
        capabilities: ["read"],
        user: { ...user.user, role: "readonly" },
      }),
    }),
  )
}

test("the page opens on Docker and the native stack states", async ({ page }) => {
  await mockStacks(page)
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page)

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toHaveAttribute("data-layout", "project")
  await expect(identity).toContainText("Docker 29.8.0")
  await expect(identity).toContainText("4 stacks running")
  await expect(identity).toContainText("5 of 6 stacks deployed")
  await expect(identity).not.toContainText("need attention")
  await expect(identity.getByRole("button", { name: "Create stack" })).toBeVisible()

  // Running, stopped and never deployed, each by name.
  await expect(rows(page)).toHaveCount(6)
  const order = await page
    .getByRole("list", { name: "Stacks" })
    .locator("[data-workspace-item]")
    .evaluateAll((all) => all.map((each) => each.getAttribute("data-workspace-item")))
  expect(order).toEqual(
    ["analytics", "monitoring", "n8n", "shop", "mailserver", "wiki"].map((n) => `stack:${n}`),
  )

  // The orphan is counted apart from the services the file declares.
  await expect(stackRow(page, "analytics")).toContainText("2 of 2 services up")
  await expect(stackRow(page, "analytics")).toContainText("1 orphan")
  await expect(stackRow(page, "wiki")).toContainText("2 services defined")
  await expect(page.getByText("0/0")).toHaveCount(0)

  // Cards under a title and a hairline: no tiles, no table, no frame round the section.
  await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
  await expect(page.getByRole("table")).toHaveCount(0)
  await expect(page.getByRole("region", { name: "Stacks", exact: true })).toHaveAttribute(
    "data-plain",
    "",
  )
})

test("a stack's card sums its containers and opens the stack", async ({ page }) => {
  await mockStacks(page)
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page)

  const shop = stackRow(page, "shop")
  await expect(shop).toContainText("Running")
  await expect(shop).toContainText("4 of 4 services up")
  await expect(shop).toContainText("4 ports published")
  await expect(shop).toContainText("762.0 MB")
  await expect(shop).toContainText("/srv/shop")
  await expect(shop.getByRole("img", { name: /api running/ })).toBeVisible()
  // Up, but failing its own check, is said as what it means.
  await expect(stackRow(page, "n8n")).toContainText("Degraded")
  await expect(stackRow(page, "n8n")).toContainText("1 unhealthy")
  // A stack with nothing running has no figures to give.
  await expect(stackRow(page, "mailserver").locator("[data-stack-memory]")).toHaveText("")

  await shop.getByRole("link", { name: "Open shop" }).click()
  await expect(page).toHaveURL(/\/docker\/stacks\/shop$/, NAVIGATION)
})

for (const width of [1280, 390]) {
  test(`every stack is one line of one height at ${width}px, and nothing opens in place`, async ({
    page,
  }) => {
    await mockStacks(page)
    await page.setViewportSize({ width, height: 900 })
    await open(page)
    await risen(page)

    // Four services, one, and none ever created; with Deploy and without.
    const boxes = await rows(page).evaluateAll((all) =>
      all.map((row) => {
        const box = row.getBoundingClientRect()
        return { top: box.top, height: box.height }
      }),
    )
    expect(boxes).toHaveLength(6)
    expect(new Set(boxes.map((box) => box.height)).size).toBe(1)
    const steps = boxes.slice(1).map((box, index) => Math.round(box.top - boxes[index].top))
    expect(new Set(steps).size).toBe(1)

    // The services are the stack's page: no row lists them and none unfolds.
    await expect(page.getByText("ghcr.io/acme/shop-api:2.4.1")).toHaveCount(0)
    await expect(page.getByRole("button", { name: /^(Hide|Show) .+ containers$/ })).toHaveCount(0)

    if (width >= 1280) {
      // Read down their columns: every state starts on one edge, every figure ends on one.
      const edge = (selector: string, side: "left" | "right") =>
        rows(page)
          .locator(selector)
          .evaluateAll(
            (all, at) => all.map((each) => Math.round(each.getBoundingClientRect()[at])),
            side,
          )
      expect(new Set(await edge("[data-stack-state]", "left")).size).toBe(1)
      expect(new Set(await edge("[data-stack-cpu]", "right")).size).toBe(1)
      expect(new Set(await edge("[data-stack-memory]", "right")).size).toBe(1)
      const names = await rows(page)
        .locator("[data-workspace-item]")
        .evaluateAll((all) => all.map((each) => Math.round(each.getBoundingClientRect().left)))
      expect(new Set(names).size).toBe(1)
    } else {
      // On a phone the state and the sums are the card's second line.
      await expect(stackRow(page, "shop")).toContainText("4/4 up")
      await expect(stackRow(page, "shop")).toContainText("762.0 MB")
      await expect(stackRow(page, "wiki")).toContainText("Not deployed")
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow).toBeLessThanOrEqual(1)
      for (const name of ["analytics", "monitoring", "mailserver"]) {
        const box = await stackRow(page, name).boundingBox()
        expect(box!.x).toBeGreaterThanOrEqual(0)
        expect(box!.x + box!.width).toBeLessThanOrEqual(390)
      }
    }
  })
}

test("a deploy from the list does not change the card's height", async ({ page }) => {
  const mocks = await mockStacks(page)
  let finish!: () => void
  const running = new Promise<void>((resolve) => (finish = resolve))
  await page.route("**/api/v1/docker/stacks/wiki/up", async (route) => {
    await running
    await route.fallback()
  })
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page)
  await risen(page)

  const wiki = stackRow(page, "wiki")
  const before = await wiki.boundingBox()
  const next = await stackRow(page, "mailserver").boundingBox()
  // The stack that was never deployed is the one brand command in the list.
  await wiki.getByRole("button", { name: "Deploy" }).click()
  await expect(wiki).toContainText("Deploying…")
  expect(await wiki.boundingBox()).toEqual(before)
  expect(await stackRow(page, "mailserver").boundingBox()).toEqual(next)
  // A second press while it runs is not a second deploy.
  await expect(wiki.getByRole("button", { name: "Deploy" })).toBeDisabled()

  finish()
  await expect.poll(() => mocks.actions).toEqual(["wiki/up"])
  await expect(stackRow(page, "shop").getByRole("button", { name: "Deploy" })).toHaveCount(0)
})

test("a card's menu holds the ways into the stack", async ({ page }) => {
  await mockStacks(page)
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page)

  await stackRow(page, "shop").getByRole("button", { name: "More actions for shop" }).click()
  await expect(page.getByRole("menuitem", { name: "View" })).toHaveAttribute(
    "href",
    "/docker/stacks/shop",
  )
  await expect(page.getByRole("menuitem", { name: "Files" })).toHaveAttribute(
    "href",
    "/files?path=%2Fsrv%2Fshop",
  )
  await expect(page.getByRole("menuitem", { name: "Open shell" })).toHaveAttribute(
    "href",
    "/terminal?cwd=%2Fsrv%2Fshop",
  )
  // Opening the menu is not opening the stack.
  await expect(page).toHaveURL(/\/docker\/stacks$/)
})

test("the band names the heaviest stacks and narrows the list to one", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  const processor = page.getByRole("region", { name: "Processor by stack" })
  await expect(processor.getByRole("button", { name: "Only shop's containers" })).toBeVisible()
  const memory = page.getByRole("region", { name: "Memory by stack" })
  await memory.getByRole("button", { name: "Only analytics's containers" }).click()

  await expect(rows(page)).toHaveCount(1)
  await expect(stackRow(page, "analytics")).toBeVisible()
  await expect(page.getByRole("heading", { level: 2, name: /^Stacks/ })).toContainText("1 of 6")
  await page.getByRole("button", { name: "Showing analytics; press to show every stack" }).click()
  await expect(rows(page)).toHaveCount(6)
})

test("recent says what Docker did to the stacks, and a line opens its stack", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  const recent = page.getByRole("region", { name: "Recent changes" })
  const line = recent.locator("[data-slot=bar-reading-line]")
  await expect(line).toContainText("unhealthy")
  // The oom and the exit it caused are one mark.
  const alertmanager = recent.getByRole("button", { name: /^Open monitoring: alertmanager/ })
  await expect(alertmanager).toHaveCount(1)
  await alertmanager.hover()
  await expect(line).toContainText("killed for memory")
  await alertmanager.click()
  await expect(page).toHaveURL(/\/docker\/stacks\/monitoring$/, NAVIGATION)
})

test("the line and the band hold their place while the stacks are read", async ({ page }) => {
  await mockStacks(page)
  let land!: () => void
  const held = new Promise<void>((resolve) => (land = resolve))
  await page.route("**/api/v1/docker/stacks/", async (route) => {
    await held
    await route.fallback()
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/stacks")

  const identity = page.locator("[data-slot='host-identity']")
  const band = page.locator("[data-slot=stack-band]")
  const blocks = band.locator("[data-slot=bar-reading]")
  await expect(blocks).toHaveCount(3)
  await expect(page.getByLabel("Processor by stack")).toContainText("Reading the stacks…")
  await expect(page.getByRole("button", { name: "Showing analytics" })).toHaveCount(0)
  await risen(page)
  const heights = await blocks.evaluateAll((all) =>
    all.map((block) => block.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)
  const before = await band.boundingBox()
  const line = await identity.boundingBox()

  land()
  await expect(stackRow(page, "analytics")).toBeVisible()
  await expect(page.getByLabel("Processor by stack").getByRole("button").first()).toBeVisible()
  await risen(page)
  expect(await band.boundingBox()).toEqual(before)
  expect(await identity.boundingBox()).toEqual(line)
})

test("the native state chips count and narrow without an attention filter", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  await expect(page.getByRole("button", { name: /^Needs attention/ })).toHaveCount(0)

  await page.getByRole("button", { name: /^Not deployed/ }).click()
  await expect(rows(page)).toHaveCount(1)
  await expect(stackRow(page, "wiki")).toBeVisible()
  await page.getByRole("button", { name: "Show stacks in every state" }).click()
  await expect(rows(page)).toHaveCount(6)
})

test("searching an image finds the stacks running it, and each says which service", async ({
  page,
}) => {
  await mockStacks(page)
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page)
  await risen(page)
  const height = (await stackRow(page, "shop").boundingBox())!.height

  await page.getByPlaceholder("Stack, service or image").fill("postgres")
  await expect(rows(page)).toHaveCount(2)
  await expect(stackRow(page, "shop")).toContainText("matches db")
  await expect(stackRow(page, "n8n")).toContainText("matches postgres")
  // Said on the line the directory was on: the card is no taller for it.
  await expect(stackRow(page, "shop")).not.toContainText("/srv/shop")
  expect((await stackRow(page, "shop").boundingBox())!.height).toBe(height)

  await page.getByPlaceholder("Stack, service or image").fill("nothing-runs-this")
  await expect(page.getByText("Nothing matches those filters")).toBeVisible()
  await page.getByRole("button", { name: "Clear filters" }).click()
  await expect(rows(page)).toHaveCount(6)
})

test("a read-only account sees the stacks and no command it cannot use", async ({ page }) => {
  await mockStacks(page)
  await readOnly(page)
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page)

  await expect(rows(page)).toHaveCount(6)
  await expect(page.getByRole("button", { name: "Create stack" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Deploy", exact: true })).toHaveCount(0)
  await stackRow(page, "shop").getByRole("button", { name: "More actions for shop" }).click()
  await expect(page.getByRole("menuitem", { name: "View" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Open shell" })).toHaveCount(0)
})

test("Create stack is the line's one command and opens its form", async ({ page }) => {
  await mockStacks(page)
  await open(page)

  await page
    .locator("[data-slot='host-identity']")
    .getByRole("button", { name: "Create stack" })
    .click()
  const dialog = page.getByRole("dialog", { name: "Create stack" })
  await expect(dialog.getByLabel("Name")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Create" })).toBeDisabled()
})

test("the address that opened a stack's sheet lands on the stack's page", async ({ page }) => {
  await mockStacks(page)
  await page.goto("/docker/stacks?stack=shop")
  await expect(page).toHaveURL(/\/docker\/stacks\/shop$/, NAVIGATION)
})

test("a server with no stacks offers to create one", async ({ page }) => {
  await mockStacks(page, { stacks: [], containers: [], stats: [] })
  await page.goto("/docker/stacks")
  await expect(page.getByText("No compose stacks found")).toBeVisible({ timeout: 20_000 })
  await expect(page.getByRole("button", { name: "Create stack" }).first()).toBeVisible()
  await expect(page.getByLabel("Memory by stack")).toContainText("No stack is running.")
})
