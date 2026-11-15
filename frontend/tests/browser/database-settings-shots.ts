import type { Page } from "@playwright/test"

/**
 * A picture of a whole page, for the `@shots` tests of a database's settings.
 *
 * The dashboard's shell scrolls its content in a pane of its own, so the
 * window is never taller than the view and `fullPage` catches only what the
 * pane shows. The view is made as tall as the pane's content for the picture
 * and put back afterwards; the width, which is what a state is taken at, is
 * left alone.
 */
export async function tallShot(page: Page, path: string) {
  const view = page.viewportSize()
  if (!view) throw new Error("the page has no viewport to grow")
  const hidden = () =>
    page.evaluate(() => {
      let most = 0
      for (const el of document.querySelectorAll("body *")) {
        if (!/(auto|scroll)/.test(getComputedStyle(el).overflowY) || el.clientHeight < 300) continue
        most = Math.max(most, el.scrollHeight - el.clientHeight)
      }
      return most
    })
  const more = await hidden()
  if (more > 0) {
    await page.setViewportSize({ width: view.width, height: Math.min(view.height + more, 6000) })
    // What sits at the foot of the view (the save bar) follows it down.
    await page.waitForTimeout(300)
  }
  await page.screenshot({ path, fullPage: true })
  if (more > 0) await page.setViewportSize(view)
}
