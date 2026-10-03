/**
 * The instant the pointer is over, shared by every chart on the page.
 *
 * This is the one thing Netdata does that nothing else in this class does at
 * all, and it is the difference between a page of graphs and a page you can
 * actually read. Hovering the CPU chart at 03:14 puts a line at 03:14 on the
 * memory chart, the network chart and the disk chart at the same time, so the
 * question "what else was happening then" is answered by moving the mouse
 * instead of by squinting across four independently-scaled x-axes.
 *
 * Held outside React for the same reason the live metrics buffer is: a pointer
 * moving across a chart fires continuously, and routing that through a context
 * above the router would re-render the terminal and the log tail on every
 * mousemove. Explicit subscribers mean only the parts that draw a crosshair
 * re-render — which is deliberately *not* the charts themselves, see
 * `SyncedCrosshair` in `components/metrics/metric-chart.tsx`.
 *
 * The value is a timestamp in epoch milliseconds rather than a row index,
 * because the charts on a page do not share a row array — the storage chart
 * has its own buckets, and a container chart has its own window entirely. A
 * time is the only thing they all agree on.
 */

export type Crosshair = {
  ts: number | null
  pinned?: boolean
  /**
   * Which chart the pointer is actually in.
   *
   * Recharts draws its own cursor on that one, so it is the one chart that
   * must *not* draw a synced line as well — two lines a pixel apart read as a
   * rendering fault rather than as a feature.
   */
  source: string | null
}

const EMPTY: Crosshair = { ts: null, source: null }

let current: Crosshair = EMPTY
let pending: Crosshair | null = null
let frame = 0

const listeners = new Set<() => void>()

export function subscribeCrosshair(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function getCrosshair(): Crosshair {
  return current
}

/**
 * The server render has no pointer, and must not disagree with the client.
 *
 * A constant rather than a fresh object: `useSyncExternalStore` compares
 * snapshots by identity and loops forever on a getter that allocates.
 */
export function getServerCrosshair(): Crosshair {
  return EMPTY
}

/**
 * Moves the crosshair, at most once per animation frame.
 *
 * Recharts fires its mouse handler on every pointer event, and on a dense
 * chart the hovered bucket changes every two or three pixels — fast enough
 * that a synchronous notify per event asks React to do a round of work the
 * browser has no frame to paint it in. Coalescing to one update per frame
 * caps the rate at the display's own, and lets a fast drag across the page
 * collapse into a single render instead of forty.
 */
export function setCrosshair(ts: number | null, source: string | null = null) {
  if (current.pinned) return
  if (current.ts === ts && current.source === source) {
    // Already showing this. Drop any queued update that would undo it.
    pending = null
    return
  }
  pending = { ts, source }
  if (frame !== 0) return
  // No rAF outside the browser, and no pointer there either.
  if (typeof requestAnimationFrame === "undefined") {
    flushCrosshair()
    return
  }
  frame = requestAnimationFrame(flushCrosshair)
}

function flushCrosshair() {
  frame = 0
  const next = pending
  pending = null
  if (!next) return
  if (current.ts === next.ts && current.source === next.source) return
  current = next
  for (const listener of listeners) listener()
}

/**
 * Clears the crosshair now rather than on the next frame.
 *
 * Leaving a chart is the one transition worth not deferring: a line left
 * behind under a pointer that has moved on to a table reads as a stuck UI,
 * and there is no smoothness to preserve on the way out.
 */
export function clearCrosshair() {
  if (current.pinned) return
  pending = null
  if (frame !== 0 && typeof cancelAnimationFrame !== "undefined") {
    cancelAnimationFrame(frame)
    frame = 0
  }
  if (current === EMPTY) return
  current = EMPTY
  for (const listener of listeners) listener()
}

export function pinCrosshair(ts: number) {
  pending = null
  if (frame !== 0 && typeof cancelAnimationFrame !== "undefined") cancelAnimationFrame(frame)
  frame = 0
  current = { ts, source: null, pinned: true }
  for (const listener of listeners) listener()
}

export function unpinCrosshair() {
  current = EMPTY
  clearCrosshair()
  for (const listener of listeners) listener()
}
