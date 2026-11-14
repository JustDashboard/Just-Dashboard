import { expect, type Locator, type Page } from "@playwright/test"

/**
 * Reads what stands behind a control's tooltip, by the keyboard's way in.
 *
 * A tooltip closes when the page scrolls under it, and focusing a control that
 * is below the fold scrolls it into view: so the control is brought into view
 * first, and asked again if the page moved while it opened.
 */
export async function readTip(page: Page, control: Locator, words: RegExp) {
  await control.scrollIntoViewIfNeeded()
  await expect(async () => {
    await control.blur()
    await control.focus()
    await expect(page.getByText(words).first()).toBeVisible({ timeout: 1500 })
  }).toPass({ timeout: 15_000 })
  await page.keyboard.press("Escape")
}
