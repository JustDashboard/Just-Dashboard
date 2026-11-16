/**
 * Motion a component decides in script, held to the same two requests the
 * stylesheet honours.
 *
 * The root rule in `globals.css` stops every CSS animation and sets
 * `scroll-behavior: auto` under `prefers-reduced-motion` and under the
 * product's own pause (`data-motion="paused"` on `<html>`). Neither reaches a
 * `scrollIntoView({ behavior: "smooth" })` or a canvas that animates a fit for
 * 400ms: an explicit behaviour overrides the CSS by specification, and a
 * duration handed to a library is not CSS at all. So script that moves
 * something asks here first.
 */

const REDUCE = "(prefers-reduced-motion: reduce)"

/** Whether the reader asked for less motion: in the system, or with the product's pause. */
export function reducedMotion(): boolean {
  if (typeof window === "undefined") return false
  if (document.documentElement.dataset.motion === "paused") return true
  return window.matchMedia?.(REDUCE).matches ?? false
}

/**
 * A duration for motion run from script (a canvas fit, a fly-to): the given
 * one, or none when motion is reduced, so the view lands where it was going
 * without the travel.
 */
export function motionMs(ms: number): number {
  return reducedMotion() ? 0 : ms
}

type Reveal = {
  /** Where the target lands in its scroller. Default `start`. */
  block?: ScrollLogicalPosition
  inline?: ScrollLogicalPosition
  /**
   * Move the keyboard there too. A press that scrolls to a section and leaves
   * focus on the button makes the next Tab continue from the button, back
   * where the reader came from. The target is made focusable for the script
   * only (`tabindex="-1"`), so it gains no stop of its own.
   */
  focus?: boolean
}

/**
 * Brings an element into view: glided, or at once when motion is reduced.
 * The one way script scrolls to a section, in place of
 * `scrollIntoView({ behavior: "smooth" })`.
 */
export function scrollIntoViewRespectingMotion(
  target: Element | null | undefined,
  { block = "start", inline, focus = false }: Reveal = {},
): void {
  if (!target) return
  if (focus && "focus" in target) {
    const el = target as HTMLElement
    if (!el.hasAttribute("tabindex")) el.setAttribute("tabindex", "-1")
    // Focus first and without its own scroll: the browser's jump to a focused
    // element is instant and would cut the glide short.
    el.focus({ preventScroll: true })
  }
  target.scrollIntoView({ block, inline, behavior: reducedMotion() ? "instant" : "smooth" })
}
