import { expect, test, type Locator, type Page } from "@playwright/test"
import { mockFleet } from "./database-fleet-fixture"
import { mockProxy } from "./proxy-fixtures"
import { mockServices } from "./services-fixture"

/**
 * Forced colours (Windows High Contrast), as Chromium draws them: every fill
 * repainted as the page and every ink as the text's. Each switcher keeps its
 * chosen mark there as the system's selection, the way the page's tabs do,
 * and a focused one still shows its ring.
 */

const TRANSPARENT = "rgba(0, 0, 0, 0)"

const paint = (locator: Locator) =>
  locator.evaluate((node) => {
    const style = getComputedStyle(node)
    return {
      fill: style.backgroundColor,
      ink: style.color,
      // A line with no style is no line, whatever colour it is given.
      ring: style.outlineStyle === "none" ? "rgba(0, 0, 0, 0)" : style.outlineColor,
    }
  })

const ground = (page: Page) =>
  page.locator("body").evaluate((node) => getComputedStyle(node).backgroundColor)

/** Chosen against not: a fill of their own, apart from the page, and an ink of their own. */
async function expectChosenMark(page: Page, chosen: Locator, other: Locator, face = chosen) {
  const on = { ...(await paint(chosen)), fill: (await paint(face)).fill }
  const off = await paint(other)
  // Forced colours had painted both the same: the page's fill, the text's ink.
  expect(on.fill).not.toBe(off.fill)
  expect(on.fill).not.toBe(await ground(page))
  expect(on.ink).not.toBe(off.ink)
  return on
}

/**
 * Reached by the keyboard, the chosen one shows a ring, in a colour apart from
 * what it is drawn on: the page for a ring outside the control, the chosen
 * face for one inside it.
 */
async function expectRingSeen(page: Page, chosen: Locator, drawnOn: Locator | "page") {
  await chosen.focus()
  await page.keyboard.press("Tab")
  await page.keyboard.press("Shift+Tab")
  await expect(chosen).toBeFocused()
  const { ring } = await paint(chosen)
  expect(ring).not.toBe(TRANSPARENT)
  expect(ring).not.toBe(drawnOn === "page" ? await ground(page) : (await paint(drawnOn)).fill)
}

test.beforeEach(async ({ page }) => {
  test.setTimeout(120_000)
  await page.emulateMedia({ forcedColors: "active" })
})

test("under forced colours the chosen filter chip is the system's selection", async ({ page }) => {
  await mockServices(page)
  await page.goto("/processes/services")
  const strip = page.locator("[aria-label='State']")
  const failed = strip.getByRole("button", { name: /^Failed/ })
  await failed.click()
  await expect(failed).toHaveAttribute("aria-pressed", "true")
  await page.mouse.move(0, 0)
  await expectChosenMark(page, failed, strip.locator("button[aria-pressed=false]").first())
  await expectRingSeen(page, failed, "page")
})

test("under forced colours the chosen segment is the system's selection", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockFleet(page)
  await page.goto("/databases")
  const group = page.locator("[data-slot=toggle-group]", { hasText: "Where it runs" })
  const chosen = group.locator("[data-state=on]")
  // Measured, the chosen fill is the face that slides under the segments.
  const face = group.locator("[data-slot=toggle-group-face]")
  await expect(face).toBeVisible({ timeout: 60_000 })
  await expectChosenMark(page, chosen, group.locator("[data-state=off]").first(), face)
  await expectRingSeen(page, chosen, face)
})

test("under forced colours the chosen tab of the shared kit is the system's selection", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/sites/app.example.com?tab=settings&view=file")
  const list = page.locator("[data-slot=tabs-list]").first()
  const chosen = list.locator("[data-state=active]")
  await expect(chosen).toBeVisible({ timeout: 60_000 })
  await expectChosenMark(page, chosen, list.locator("[data-state=inactive]").first())
  await expectRingSeen(page, chosen, chosen)
})

test("under forced colours the shared ring is drawn only on what has the keyboard", async ({
  page,
}) => {
  // Forced colours painted the resting transparent outline in the text's
  // colour: every control drawn with the ring wore it at once.
  await mockServices(page)
  await page.goto("/processes/services")
  const strip = page.locator("[aria-label='State']")
  await expect(strip.getByRole("button").first()).toBeVisible()
  const ringed = () =>
    page.evaluate(() =>
      [...document.querySelectorAll<HTMLElement>("[class*='focus-ring']")]
        .filter((el) => el.offsetParent && !el.contains(document.activeElement))
        .map((el) => {
          const style = getComputedStyle(el)
          return {
            what: `${el.tagName} ${el.getAttribute("aria-label") ?? el.textContent?.trim().slice(0, 30)}`,
            shown: style.outlineStyle !== "none" && style.outlineColor !== "rgba(0, 0, 0, 0)",
          }
        }),
    )
  const resting = await ringed()
  expect(resting.length).toBeGreaterThan(10)
  expect(resting.filter((one) => one.shown)).toEqual([])

  // Reached by the keyboard, a control shows it, and only that one.
  const chip = strip.getByRole("button").first()
  await chip.focus()
  await page.keyboard.press("Tab")
  await page.keyboard.press("Shift+Tab")
  await expect(chip).toBeFocused()
  expect(await chip.evaluate((el) => getComputedStyle(el).outlineStyle)).not.toBe("none")
  expect((await ringed()).filter((one) => one.shown)).toEqual([])
})
