/**
 * How far a scroll port has to move to show the whole of a region inside it.
 *
 * A table capped to `100svh - 22rem` is sized to fit the screen *under* the
 * page's header, so as soon as the page sits anywhere but its own top the
 * table's last rows — or its column names — are below the fold while the rows
 * between them scroll. The wheel over the table then moves a grid the reader
 * can only half see. Bringing the region into view first is what the cap was
 * for in the first place.
 *
 * The move is the smallest one that shows the region, the same answer as
 * `scrollIntoView({ block: "nearest" })`, so a region clipped at the top moves
 * down and one clipped at the bottom moves up. A region taller than the port
 * shows its top, where a sticky header names the columns. The answer is
 * clamped to the room the port has, and applying it makes the next answer 0,
 * which is what lets a caller ask again on every wheel event without the page
 * fighting itself.
 */

/** A vertical extent in viewport pixels, as `getBoundingClientRect` gives it. */
export type Span = { top: number; bottom: number }

/** Air between the region and the port's edge, so it does not land flush. */
export const REVEAL_MARGIN = 12

export function revealDelta(
  region: Span,
  port: Span,
  room: { up: number; down: number },
  margin = REVEAL_MARGIN,
): number {
  const top = port.top + margin
  const bottom = port.bottom - margin
  if (region.top >= top && region.bottom <= bottom) return 0
  const delta =
    region.bottom - region.top >= bottom - top || region.top < top
      ? region.top - top
      : region.bottom - bottom
  const clamped = Math.min(Math.max(delta, -room.up), room.down)
  return Math.abs(clamped) < 1 ? 0 : clamped
}
