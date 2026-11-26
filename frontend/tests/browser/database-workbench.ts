import { expect, type Page } from "@playwright/test"

/**
 * The chrome the four workbenches of a database share (Data, Query, Schema,
 * Diagram): one head in the frame, with one menu, and on a phone one pane
 * with a bar at the foot.
 */

/** The frame's head: which database, the page's verbs, the one menu. */
export const head = (page: Page) => page.locator("[data-slot=database-strip][data-head=workbench]")

/**
 * Presses a verb of the head's menu: the icon-only controls live there as
 * words. `opens: "dialog"` names a verb that opens a dialog, which keeps the
 * focus the menu would hand back.
 */
export async function headMenu(
  page: Page,
  name: string | RegExp,
  { opens }: { opens?: "dialog" } = {},
) {
  const trigger = head(page).getByRole("button", { name: /^Actions for / })
  await trigger.click()
  await page.getByRole("menuitem", { name }).click()
  // The menu hands focus back to its trigger in a timer Radix starts once the
  // menu has unmounted, so no menu on screen is not yet the end of it: on a
  // loaded runner the focus came back after the next press had opened a
  // popover, and the popover closed on losing it. The next press waits for
  // focus to be where the verb leaves it: on the trigger, or in the dialog it
  // opened, whose trap takes focus back from the trigger.
  await expect(page.getByRole("menu")).toHaveCount(0)
  if (opens === "dialog") {
    await expect(
      page.locator("[role=dialog]:focus-within, [role=alertdialog]:focus-within"),
    ).toHaveCount(1)
  } else {
    await expect(trigger).toBeFocused()
  }
}

/** The bar at the foot of the frame on a phone. */
export const paneBar = (page: Page) => page.locator("[data-slot=pane-bar]")

/** Shows one of the frame's panes on a phone. */
export async function showPane(page: Page, name: string) {
  await paneBar(page).getByRole("button", { name, exact: true }).click()
}
