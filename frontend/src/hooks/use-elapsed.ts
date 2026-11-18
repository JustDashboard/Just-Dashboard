"use client"

import { useNow } from "@/hooks/use-now"

/**
 * How long something has been running, in milliseconds, for a figure that
 * counts up while a request is out ("Stop · 3 s", "Running for 12 s"). 0 when
 * `since` is null, and the clock stops then too, so a page with nothing in
 * flight does not re-render every tick.
 *
 * A caller that holds a boolean records the time it turned true and passes
 * that. `tick` is how often it moves: 250 ms for a figure in seconds, which
 * then never lags a whole second behind, and 1000 for one that is coarser.
 */
export function useElapsed(since: number | null, tick = 250): number {
  const now = useNow(tick, since !== null)
  return since === null ? 0 : Math.max(0, now - since)
}
