/**
 * Watching a thing at the foot of the screen for a change in how far up it
 * reaches, for the bottom stack (`components/dock.tsx`). Here rather than
 * beside the stack so the rule has a unit test without being one more
 * export of a shared component.
 */

/**
 * Calls `measure` whenever what an occupant reaches can change: its size, the
 * window's, and for a bar its own `style`. A bar is lifted by setting its
 * `bottom`, which changes its reach without changing its size, so a size
 * watch alone kept the reach it had before the lift and the stack's top fell
 * short by the bar under it. Returns the way to stop watching.
 */
export function watchOccupant(element: HTMLElement, layer: "bar" | "dock", measure: () => void) {
  const sized = new ResizeObserver(measure)
  sized.observe(element)
  const styled = layer === "bar" ? new MutationObserver(measure) : null
  styled?.observe(element, { attributes: true, attributeFilter: ["style"] })
  window.addEventListener("resize", measure)
  return () => {
    sized.disconnect()
    styled?.disconnect()
    window.removeEventListener("resize", measure)
  }
}
