import { expect, test, type Locator } from "@playwright/test"
import { mockPanel } from "./git-panel-fixture"

/**
 * The workspace as it was complained about: a description whose pictures were
 * the word "image", a history of initials for people GitHub has faces for, and
 * a phone that got three short scrolling boxes inside a fourth.
 */
const address = "/git?repo=%2Fsrv%2Fjust-dashboard"

const pictureOf = (within: Locator, part: string) =>
  within.locator(`img[src*="${encodeURIComponent(part)}"]`)

test.describe("a pull request's media", () => {
  test.use({ viewport: { width: 1440, height: 1000 } })

  test("pictures are drawn through this server, folds and all, and enlarge", async ({ page }) => {
    const seen = await mockPanel(page)
    await page.goto(`${address}&pull=411`)
    const preview = page.locator("[data-slot='git-preview']")
    await expect(preview.getByRole("heading", { name: "What it is" })).toBeVisible()

    const band = preview.getByRole("img", { name: "The Docker overview's band" })
    await expect(band).toBeVisible()
    expect(await band.getAttribute("src")).toMatch(/^\/api\/v1\/git\/github\/media\?url=/)

    // HTML in the description is what it means, not the words of its tags.
    await expect(preview.getByText("<code>")).toHaveCount(0)
    await expect(preview.locator("summary code").first()).toHaveText("/docker")
    await expect(pictureOf(preview, "after-docker.png").first()).toBeVisible()

    // A closed fold fetches nothing until it opens.
    const closed = preview.locator("details").nth(1)
    await expect(closed).not.toHaveAttribute("open", "")
    await closed.locator("summary").click()
    await expect(closed.locator("img").first()).toBeVisible()

    // A picture from anywhere else never reaches the browser's image loader.
    await expect(preview.getByRole("link", { name: "a tracker" })).toHaveAttribute(
      "href",
      "https://tracker.example/pixel.png",
    )
    expect(seen.some((path) => path.includes("tracker.example"))).toBe(false)
    await expect(page.locator('img[src*="tracker.example"]')).toHaveCount(0)

    await preview.getByRole("button", { name: "Enlarge The Docker overview's band" }).click()
    const box = page.getByRole("dialog")
    await expect(box).toContainText("1 of 3")
    await page.keyboard.press("ArrowRight")
    await expect(box).toContainText("2 of 3")
    await box.getByRole("button", { name: "Close" }).click()
    await expect(box).toHaveCount(0)
  })
})

test.describe("the lists", () => {
  test.use({ viewport: { width: 1440, height: 1000 } })

  test("a commit carries its author's GitHub face when GitHub knows them", async ({ page }) => {
    await mockPanel(page)
    await page.goto(address)
    await page.getByRole("tab", { name: "History", exact: true }).click()
    await expect(page.getByRole("button", { name: /Release 0.8.4/ })).toBeVisible()
    // The merges name what they merged, with GitHub's sentence kept for hover.
    const merge = page.getByTitle("Merge pull request #409 from JustDashboard/patch/0.8.4")
    await expect(merge).toHaveText("Merged #409 from patch/0.8.4")
    await expect(page.locator('img[src*="account=Wayy01"]').first()).toBeVisible()
    await expect(page.getByText("Mira Stone")).toBeVisible()
    await expect(page.locator('img[src*="account=Mira"]')).toHaveCount(0)
  })

  test("a row says only what is not the expected answer", async ({ page }) => {
    await mockPanel(page)
    await page.goto(address)
    await page.getByRole("tab", { name: "GitHub" }).click()
    const list = page.locator("[data-pull]")
    await expect(list).toHaveCount(3)
    const feature = list.filter({ hasText: "Replace the rows" })
    // Not into the default branch, so it says where.
    await expect(feature).toContainText("→ patch/0.8.5")
    const fix = list.filter({ hasText: "Fix the sign-in" })
    await expect(fix).not.toContainText("→ main")
    await expect(fix).not.toContainText("fix/redirect")
    await expect(fix.getByText("changes requested")).toBeVisible()
  })
})

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test("one pane at a time, the whole screen, with every list on the bar", async ({ page }) => {
    await mockPanel(page)
    await page.goto(address)
    const bar = page.getByRole("navigation", { name: "Repository" })
    const message = page.getByPlaceholder(/message|describe/i).first()

    // The commit box is on screen without scrolling anything.
    await expect(message).toBeInViewport()
    for (const name of ["Changes", "History", "Branches", "GitHub", "Files"]) {
      await expect(bar.getByRole("tab", { name, exact: true })).toBeVisible()
    }
    const box = await bar.boundingBox()
    expect(Math.round((box?.y ?? 0) + (box?.height ?? 0))).toBe(844)
    await expect(page.locator("html")).toHaveJSProperty("scrollWidth", 390)

    await bar.getByRole("tab", { name: "Files" }).click()
    await expect(page.getByText("README.md")).toBeVisible()
    await expect(message).toBeHidden()

    await bar.getByRole("tab", { name: "History" }).click()
    await expect(page.getByText("README.md")).toBeHidden()

    await bar.getByRole("tab", { name: "GitHub" }).click()
    await page.locator("[data-pull='411'] button[aria-pressed]").click()
    const heading = page.getByRole("heading", { name: "What it is" })
    await expect(heading).toBeVisible()
    await expect(page.getByRole("button", { name: "Merge" })).toBeVisible()

    // The page opened from a list owns the screen, and back returns to the list.
    await expect(bar).toBeHidden()
    await expect(page.getByRole("button", { name: "Back to repositories" })).toBeHidden()
    await expect(page.locator("html")).toHaveJSProperty("scrollWidth", 390)
    await page.getByRole("button", { name: "Back to the list" }).click()
    await expect(heading).toBeHidden()
    await expect(bar).toBeVisible()
    await expect(page.locator("[data-pull='411']")).toBeVisible()
  })

  test("the graph, which has no column of its own, is in the menu", async ({ page }) => {
    await mockPanel(page)
    await page.goto(address)
    await page.getByRole("button", { name: "More git actions" }).click()
    await page.getByRole("menuitem", { name: "Branch graph" }).click()
    await expect(page.getByRole("navigation", { name: "Repository" })).toBeHidden()
  })
})
