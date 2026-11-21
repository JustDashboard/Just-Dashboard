/**
 * The arithmetic of `animated-beam.tsx`'s lines, apart from React so it is
 * tested in `bun test src`.
 */

export type Point = { x: number; y: number }

/**
 * A line that runs level from `from`, bends between `left` and `right` and
 * runs level into `to`. The bend stays inside the two ends, so a gap that
 * reaches past one of them is cut there.
 */
export function levelPath(from: Point, to: Point, left: number, right: number) {
  const low = Math.min(from.x, to.x)
  const high = Math.max(from.x, to.x)
  const a = Math.min(Math.max(left, low), high)
  const b = Math.min(Math.max(right, low), high)
  const [near, far] = from.x <= to.x ? [a, b] : [b, a]
  const mid = (near + far) / 2
  // Ends within a pixel of each other are on one level: the line is drawn
  // straight rather than with a bend too small to be meant.
  if (Math.abs(from.y - to.y) < 1) return `M ${from.x},${from.y} L ${to.x},${from.y}`
  return `M ${from.x},${from.y} L ${near},${from.y} C ${mid},${from.y} ${mid},${to.y} ${far},${to.y} L ${to.x},${to.y}`
}
