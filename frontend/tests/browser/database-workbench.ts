import type { Page } from "@playwright/test"

/**
 * The chrome the four workbenches of a database share (Data, Query, Schema,
 * Diagram): one head in the frame, with one menu, and on a phone one pane
 * with a bar at the foot.
 */

/** The frame's head: which database, the page's verbs, the one menu. */
export const head = (page: Page) => page.locator("[data-slot=database-strip][data-head=workbench]")

/** Presses a verb of the head's menu: the icon-only controls live there as words. */
export async function headMenu(page: Page, name: string | RegExp) {
  await head(page)
    .getByRole("button", { name: /^Actions for / })
    .click()
  await page.getByRole("menuitem", { name }).click()
}

/** The bar at the foot of the frame on a phone. */
export const paneBar = (page: Page) => page.locator("[data-slot=pane-bar]")

/** Shows one of the frame's panes on a phone. */
export async function showPane(page: Page, name: string) {
  await paneBar(page).getByRole("tab", { name, exact: true }).click()
}
