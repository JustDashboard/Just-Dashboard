/**
 * How far a part has to pass the one above it before they change places. A
 * ranked bar re-sorted on every poll swaps two neighbours that are within a
 * rounding of each other back and forth, and everything keyed to the order
 * hops with them.
 */
export const OVERTAKE = 1.25

/** Readings a part's trail keeps: about a minute and a half on a two-second feed. */
export const TRAIL_LENGTH = 45

/**
 * The order to draw the parts in, given the order they were last drawn in.
 * Parts that remain keep their places unless one has clearly overtaken the
 * one above it; a new part joins by its value; a part that has gone leaves.
 */
export function settleOrder(
  previous: string[],
  parts: { key: string; value: number }[],
  overtake = OVERTAKE,
): string[] {
  const value = new Map(parts.map((part) => [part.key, part.value]))
  const held = previous.filter((key) => value.has(key))
  const known = new Set(held)
  const order = [...held]
  for (const part of parts) {
    if (known.has(part.key)) continue
    const at = order.findIndex((key) => (value.get(key) ?? 0) < part.value)
    order.splice(at === -1 ? order.length : at, 0, part.key)
  }
  for (let i = 1; i < order.length; i++) {
    for (let j = i; j > 0; j--) {
      const above = value.get(order[j - 1]) ?? 0
      const below = value.get(order[j]) ?? 0
      if (below <= above * overtake) break
      ;[order[j - 1], order[j]] = [order[j], order[j - 1]]
    }
  }
  return order
}

export function sameOrder(a: string[], b: string[]) {
  return a.length === b.length && a.every((key, i) => key === b[i])
}

/** The parts' values as one string, so a poll that changed nothing adds nothing to a trail. */
export function trailSignature(parts: { key: string; value: number }[]) {
  return parts.map((part) => `${part.key}=${part.value}`).join("\n")
}

/**
 * Each part's recent values, the latest reading added and the oldest beyond
 * `length` dropped. A part no longer drawn is forgotten, so a long-open page
 * does not keep a trail for every container that ever passed through its top
 * five, and one that comes back starts a new trail rather than joining a line
 * across the time it was gone.
 */
export function pushTrails(
  trails: Map<string, number[]>,
  parts: { key: string; value: number }[],
  length = TRAIL_LENGTH,
): Map<string, number[]> {
  const next = new Map<string, number[]>()
  for (const part of parts) {
    next.set(part.key, [...(trails.get(part.key) ?? []), part.value].slice(-length))
  }
  return next
}

/**
 * A trail worth drawing: enough readings for a shape, and the shape is not a
 * flat line, which would say nothing and read as a full meter (§10).
 */
export function trailMoves(trail: number[] | undefined): trail is number[] {
  if (!trail || trail.length < 3) return false
  return trail.some((value) => value !== trail[0])
}

/** A span's width on the bar, as a percentage. A stale total cannot push it past the end. */
export function spanPercent(value: number, capacity: number) {
  if (capacity <= 0 || value <= 0) return 0
  return Math.min((value / capacity) * 100, 100)
}

/** A tick's place on its axis, as a percentage held inside the track. */
export function tickPercent(at: number) {
  return Math.min(Math.max(at, 0), 1) * 100
}

/**
 * Where a moment falls on an axis ending at `now`, 0 the far end and 1 the
 * present. Anything older than the window sits on the far end, where it is
 * still seen and still reachable, rather than off the track.
 */
export function timeAt(at: number, now: number, windowMs: number) {
  if (windowMs <= 0) return 1
  return Math.min(Math.max(1 - (now - at) / windowMs, 0), 1)
}
