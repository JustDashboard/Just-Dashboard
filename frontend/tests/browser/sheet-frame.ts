import type { Page } from "@playwright/test"

/**
 * The frames inside the page's sheet that run along its edge.
 *
 * From `md` up the shell lays every page on a sheet with its own edge and
 * corners (`app/(dashboard)/layout.tsx`, 0.9.1). The workbenches had each
 * drawn a rounded frame of their own from `lg`, 12px in from the window; on
 * the sheet that became a second edge 12px inside the first, a frame in a
 * frame, on the maps, the terminal, Files, Git, a board, the network tools
 * and a database's Data, Query, Schema and Diagram. Nothing said so, because
 * every one of them still rendered.
 *
 * A frame is a box with a border on three sides or four. It runs along the
 * sheet when it is tall and its left or right side lies within `GUTTER` of
 * the sheet's, or it is wide and its foot lies within `GUTTER` of the end of
 * the sheet's content. A reading page's panels stand on its 32px gutter and
 * pass; what a page lays flush against the sheet, a pane divided by
 * hairlines, has no frame to find. Distances are taken in the scrolled
 * content, so a long table whose foot happens to cross the window's edge is
 * not counted, and what floats (`position: fixed`: a dock, a save bar) is
 * left alone.
 */
export const GUTTER = 20

export type FrameAlongTheSheet = {
  /** The element: its tag, its `data-slot` and the head of its classes. */
  frame: string
  /** Which of the sheet's edges it runs along, and how far in. */
  along: string[]
}

export function framesAlongTheSheet(page: Page): Promise<FrameAlongTheSheet[]> {
  return page.evaluate((gutter) => {
    const sheet = document.querySelector<HTMLElement>("[data-workspace-shell-scroll]")
    if (!sheet) return []
    const box = sheet.getBoundingClientRect()
    const left = box.left + sheet.clientLeft
    const right = left + sheet.clientWidth
    const top = box.top + sheet.clientTop
    const drawn = (style: CSSStyleDeclaration, side: string) => {
      if (parseFloat(style.getPropertyValue(`border-${side}-width`)) === 0) return false
      if (style.getPropertyValue(`border-${side}-style`) === "none") return false
      const colour = style.getPropertyValue(`border-${side}-color`)
      if (colour === "transparent") return false
      const alpha = colour.match(/\/\s*([\d.]+)(%?)\s*\)$/) ?? colour.match(/,\s*([\d.]+)()\)$/)
      return !alpha || parseFloat(alpha[1]) > 0
    }
    const found: { frame: string; along: string[] }[] = []
    for (const el of Array.from(sheet.querySelectorAll<HTMLElement>("*"))) {
      const r = el.getBoundingClientRect()
      const tall = r.height >= sheet.clientHeight * 0.4
      const wide = r.width >= sheet.clientWidth * 0.4
      if (!tall && !wide) continue
      const style = getComputedStyle(el)
      if (style.visibility !== "visible" || style.position === "fixed") continue
      if (["top", "right", "bottom", "left"].filter((side) => drawn(style, side)).length < 3)
        continue
      const foot = sheet.scrollHeight - (r.bottom - top + sheet.scrollTop)
      const along = [
        tall &&
          r.left - left >= 0 &&
          r.left - left < gutter &&
          `left ${Math.round(r.left - left)}px`,
        tall &&
          right - r.right >= 0 &&
          right - r.right < gutter &&
          `right ${Math.round(right - r.right)}px`,
        wide && foot >= 0 && foot < gutter && `foot ${Math.round(foot)}px`,
      ].filter((edge): edge is string => Boolean(edge))
      if (along.length === 0) continue
      const classes = typeof el.className === "string" ? el.className : ""
      found.push({
        frame: `${el.tagName.toLowerCase()}${el.dataset.slot ? `[data-slot=${el.dataset.slot}]` : ""} ${classes.slice(0, 120)}`,
        along,
      })
    }
    return found
  }, GUTTER)
}
