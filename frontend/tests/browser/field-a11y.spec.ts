import { expect, test, type Locator, type Page } from "@playwright/test"
import { mockProject } from "./deploy-fixture"

/**
 * A `Field` wires its label, hint and error to its control, and the reasoning
 * behind its ⓘ opens however the reader reaches it (`components/form.tsx`,
 * `components/explain.tsx`).
 *
 * The page is a project's General settings: the name field there has an ⓘ, a
 * check on blur that writes an error under it, and an `InputGroup` between the
 * field and its input, so the wiring is held through a wrapper.
 */

const WHY = "Used in URLs, container labels and release history."

async function open(page: Page) {
  await mockProject(page)
  await page.goto("/deploy/7/settings/general")
  const card = page.getByRole("form", { name: "Project name" })
  await expect(card).toBeVisible()
  return card
}

/** The ⓘ beside the name field's label. */
const markOf = (card: Locator) => card.getByRole("button", { name: "What this setting does" })

test.describe("a field and its control", () => {
  test("the label names the control, and the error describes it and marks it wrong", async ({
    page,
  }) => {
    const card = await open(page)
    const name = card.getByLabel("Project name")
    await expect(name).toHaveAttribute("id", "project-name")
    await expect(name).not.toHaveAttribute("aria-invalid", "true")

    await name.fill("not a name!")
    await name.blur()
    const error = card.getByRole("alert")
    await expect(error).toBeVisible()
    await expect(name).toHaveAttribute("aria-invalid", "true")
    const errorId = await error.getAttribute("id")
    expect(errorId).toBeTruthy()
    const describedBy = (await name.getAttribute("aria-describedby"))?.split(/\s+/) ?? []
    expect(describedBy).toContain(errorId)
    // The description a screen reader reads on the field is the error itself.
    await expect(name).toHaveAccessibleDescription(await error.innerText())

    // Corrected, it stops being wrong, and nothing points at a line that went.
    await name.fill("payments-api")
    await expect(error).toHaveCount(0)
    await expect(name).not.toHaveAttribute("aria-invalid", "true")
    expect((await name.getAttribute("aria-describedby")) ?? "").not.toContain(errorId!)
  })

  test("a hint under a field is the control's description", async ({ page }) => {
    await open(page)
    // Every visible hint paragraph under a Field is pointed at by the control it sits under.
    const unlinked = await page.evaluate(() =>
      [...document.querySelectorAll<HTMLElement>("p[id], div[id]")]
        .filter((hint) => hint.id.endsWith("hint") && hint.getClientRects().length > 0)
        .filter((hint) => {
          const field = hint.parentElement!
          const control = field.querySelector("input, textarea, [data-slot=select-trigger]")
          if (!control) return false
          const ids = (control.getAttribute("aria-describedby") ?? "").split(/\s+/)
          return !ids.includes(hint.id)
        })
        .map((hint) => hint.textContent),
    )
    expect(unlinked).toEqual([])
  })
})

test.describe("an explanation at 1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  test("opens on hover, stays while the pointer moves onto it, and closes on Escape", async ({
    page,
  }) => {
    const card = await open(page)
    const mark = markOf(card)
    await mark.hover()
    const tip = page.getByRole("tooltip")
    await expect(tip).toHaveText(WHY)
    // Hoverable (WCAG 1.4.13): the pointer can go onto the bubble to read it.
    const box = (await tip.boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 4 })
    await page.waitForTimeout(300)
    await expect(tip).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(tip).toHaveCount(0)
    // And leaving it closes it.
    await mark.hover()
    await expect(tip).toBeVisible()
    await page.mouse.move(0, 0)
    await expect(tip).toHaveCount(0)
  })

  test("opens on keyboard focus as the mark's description, and closes as the focus moves on", async ({
    page,
  }) => {
    const card = await open(page)
    const mark = markOf(card)
    await card.getByLabel("Project name").focus()
    // The tab order from the name field back to the mark before it.
    await page.keyboard.press("Shift+Tab")
    await expect(mark).toBeFocused()
    const tip = page.getByRole("tooltip")
    await expect(tip).toHaveText(WHY)
    await expect(mark).toHaveAccessibleDescription(WHY)
    await page.keyboard.press("Escape")
    await expect(tip).toHaveCount(0)
    await expect(mark).toBeFocused()
    await page.keyboard.press("Tab")
    await page.keyboard.press("Shift+Tab")
    await expect(tip).toBeVisible()
    await page.keyboard.press("Tab")
    await expect(tip).toHaveCount(0)
  })

  test("a press pins it as a dialog, which Escape closes back onto the mark", async ({ page }) => {
    const card = await open(page)
    const mark = markOf(card)
    await mark.click()
    const pinned = page.getByRole("dialog", { name: "What this setting does" })
    await expect(pinned).toHaveText(WHY)
    // It stays once the pointer has gone.
    await page.mouse.move(0, 0)
    await page.waitForTimeout(300)
    await expect(pinned).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(pinned).toHaveCount(0)
    await expect(mark).toBeFocused()
    // Pressed again, it closes.
    await mark.click()
    await expect(pinned).toBeVisible()
    await mark.click()
    await expect(pinned).toHaveCount(0)
  })
})

test.describe("an explanation on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test("a tap opens it, a tap outside closes it, and its press box is a finger's", async ({
    page,
  }) => {
    const card = await open(page)
    const mark = markOf(card)
    const box = (await mark.boundingBox())!
    expect(Math.min(box.width, box.height)).toBeGreaterThanOrEqual(44)
    // The glyph itself did not grow, and neither did the label's row.
    const glyph = (await mark.locator("svg").boundingBox())!
    expect(Math.round(glyph.width)).toBe(14)
    const label = (await card.locator("label[for=project-name]").boundingBox())!
    expect(label.height).toBeLessThan(30)

    await mark.tap()
    const pinned = page.getByRole("dialog", { name: "What this setting does" })
    await expect(pinned).toHaveText(WHY)
    // The next section's title, clear of the bubble.
    await page.getByRole("heading", { name: "Source" }).tap()
    await expect(pinned).toHaveCount(0)

    await mark.tap()
    await expect(pinned).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(pinned).toHaveCount(0)
  })
})
