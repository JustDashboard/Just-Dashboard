import { expect, type Locator, type Page } from "@playwright/test"

/**
 * A table row whose name is its control (`ROW_NAME`) is one Tab stop, that
 * name: the row is no stop of its own, Tab from the name goes straight on to
 * the row's next control, and Shift+Tab from there comes back to the name.
 * Before `RowLink` passed `ROW_NAME` on, every such row was two presses of Tab.
 */
export async function expectRowIsOneStop(page: Page, row: Locator) {
  const item = await row.getAttribute("data-workspace-item")
  const label = await row.getAttribute("data-workspace-name")
  expect(label, "the row names itself").toBeTruthy()
  const name = row.getByRole("button", { name: label!, exact: true })
  await expect(name).toHaveAttribute("data-workspace-primary", "")
  await expect(row).not.toHaveAttribute("tabindex")
  const at = () =>
    page.evaluate(() => {
      const el = document.activeElement as HTMLElement
      return {
        tag: el.tagName,
        row: el.closest("[data-workspace-item]")?.getAttribute("data-workspace-item") ?? null,
        primary: el.hasAttribute("data-workspace-primary"),
      }
    })

  // Back from the name is never the row around it, and on from there is the
  // name again.
  await name.focus()
  await page.keyboard.press("Shift+Tab")
  await expect(row).not.toBeFocused()
  expect((await at()).tag).not.toBe("TR")
  await page.keyboard.press("Tab")
  await expect(name).toBeFocused()

  // On from the name is the row's next control, inside the row.
  await page.keyboard.press("Tab")
  const next = await at()
  expect(next.tag).not.toBe("TR")
  expect(next.row).toBe(item)
  expect(next.primary).toBe(false)
  await page.keyboard.press("Shift+Tab")
  await expect(name).toBeFocused()
}
