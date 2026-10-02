/**
 * Puts the keyboard on an element that may not be drawn yet, and keeps it
 * there through the moment after.
 *
 * A control that has just gone — a dialog closed, a row deleted, a field
 * taken away — leaves the keyboard on the page's body, and what should have
 * it next is often drawn a frame later, or has the focus taken from it again
 * by whatever is still closing. So the element is looked for over a few
 * frames, and given the keyboard again if it fell back to nowhere; once the
 * reader has moved it somewhere of their own, it is left alone.
 */
export function focusSoon(
  find: () => HTMLElement | null | undefined,
  /** Where the keyboard goes when what was looked for never came. */
  otherwise?: () => HTMLElement | null | undefined,
  frames = 20,
) {
  let left = frames
  let held = false
  const tick = () => {
    const target = find()
    const active = document.activeElement
    const nowhere = active === null || active === document.body
    if (target) {
      if (active === target) held = true
      else if (!held || nowhere) {
        target.focus()
        held = held || document.activeElement === target
      }
      // It was given, and has since been moved somewhere of the reader's own: theirs.
      else return
    }
    if (--left > 0) requestAnimationFrame(tick)
    else if (
      !held &&
      (document.activeElement === null || document.activeElement === document.body)
    ) {
      otherwise?.()?.focus()
    }
  }
  requestAnimationFrame(tick)
}
