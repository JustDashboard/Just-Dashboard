/**
 * Whether a chart's plot may keep the frame it last drew, as the comparison
 * `memo` asks for: true is "do not draw again".
 *
 * A plot out of reach of the view holds still whatever its page hands it — it
 * is compared against what it last *drew*, so the rows that arrived while it
 * was away are a difference the moment it is near again. Near the view, any
 * prop that changed is a reason to draw, and none that did not is: a panel
 * re-rendered for the live figure in its head hands its plot the same rows.
 */
export function plotHeld<P extends { near: boolean }>(drawn: P, next: P): boolean {
  if (!next.near) return true
  const keys = new Set([...Object.keys(drawn), ...Object.keys(next)]) as Set<keyof P>
  for (const key of keys) if (!Object.is(drawn[key], next[key])) return false
  return true
}
