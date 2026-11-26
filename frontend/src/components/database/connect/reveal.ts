import { breakpointQuery } from "@/lib/breakpoints"
import { scrollIntoViewRespectingMotion } from "@/lib/motion"

/**
 * Brings one of a flow screen's two columns into view where they are stacked.
 * Side by side, both are on screen and nothing moves.
 */
export function reveal(id: string) {
  if (window.matchMedia(breakpointQuery("xl")).matches) return
  requestAnimationFrame(() => {
    const panel = document.getElementById(id)
    panel?.focus({ preventScroll: true })
    scrollIntoViewRespectingMotion(panel)
  })
}
