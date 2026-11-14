import { expect, test } from "@playwright/test"
import { mockProject } from "./deploy-fixture"

/**
 * A tooltip opened by the keyboard stays open while the page scrolls to it.
 *
 * Tabbing to an info mark below the fold focuses it, which opens its tooltip,
 * and scrolls the page to it. Radix closes a tooltip when anything around its
 * trigger scrolls, so that scroll closed the tooltip a frame after it opened
 * and a keyboard reader never read it (`components/ui/tooltip.tsx`).
 */
test("tabbing to an info mark below the fold shows its tooltip, and it stays", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 560 })
  await mockProject(page)
  await page.goto("/deploy/7/settings/runtime")
  await expect(page.locator("[data-slot=setting]").first()).toBeVisible()

  // The first info mark the window does not show, and the control before it
  // in the tab order, which takes the focus without the page moving.
  const before = await page.evaluate(() => {
    const tabbable = [
      ...document.querySelectorAll<HTMLElement>(
        "a[href], button:not([disabled]), input:not([disabled]), select, textarea, [tabindex]:not([tabindex='-1'])",
      ),
    ].filter((el) => el.tabIndex >= 0 && el.getClientRects().length > 0)
    const index = tabbable.findIndex(
      (el) =>
        el.dataset.slot === "tooltip-trigger" &&
        el.querySelector("svg") !== null &&
        el.getBoundingClientRect().top > window.innerHeight + 40,
    )
    if (index < 1) return null
    tabbable[index - 1].focus({ preventScroll: true })
    const mark = tabbable[index]
    mark.dataset.underTest = "true"
    return { top: mark.getBoundingClientRect().top, height: window.innerHeight }
  })
  expect(before, "an info mark below the fold").not.toBeNull()

  await page.keyboard.press("Tab")
  const mark = page.locator("[data-under-test=true]")
  await expect(mark).toBeFocused()
  // The page did scroll to it: this is the scroll that used to close it.
  await expect
    .poll(() => mark.evaluate((el) => el.getBoundingClientRect().bottom))
    .toBeLessThanOrEqual(before!.height)
  const tooltip = page.getByRole("tooltip")
  await expect(tooltip).toBeVisible()
  // And it is still there once the scroll has long settled.
  await page.waitForTimeout(600)
  await expect(tooltip).toBeVisible()
  await expect(mark).toHaveAttribute("data-state", /open/)

  // What closes a tooltip still does: Escape, and the focus moving on.
  await page.keyboard.press("Escape")
  await expect(tooltip).toHaveCount(0)
  await page.keyboard.press("Shift+Tab")
  await page.keyboard.press("Tab")
  await expect(tooltip).toBeVisible()
  await page.keyboard.press("Tab")
  await expect(mark).not.toBeFocused()
  await expect(mark).toHaveAttribute("data-state", "closed")
})
