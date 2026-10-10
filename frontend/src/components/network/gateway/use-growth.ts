import { useState } from "react"
import type { GatewayView } from "@/lib/types"
import { counters, grown } from "@/components/network/gateway/reading"

/**
 * Which entries carried a packet since the gateway was last read: the answer
 * to "is this wire alive", which the gateway can only give as a counter that
 * went up between two polls.
 *
 * The previous reading is kept as state and compared while rendering, when a
 * new read arrives — React's own pattern for deriving from the render before,
 * as `useArrivals` does — rather than as a ref written during render or an
 * effect that sets state a frame late. The answer is held until the next read
 * and is keyed on the read itself rather than on its counters, so a poll that
 * brought identical counters says "nothing moved" instead of repeating the
 * last poll's yes. Empty on the first read, which has no past to grow from.
 */
export function useGrowth(view: GatewayView | undefined): Set<string> {
  const [seen, setSeen] = useState<{
    view: GatewayView | undefined
    counters: Record<string, number>
    moved: Set<string>
  }>({ view: undefined, counters: {}, moved: new Set() })
  if (!view || seen.view === view) return seen.moved
  const next = counters(view.forwards, view.nat)
  const moved = grown(seen.counters, next)
  setSeen({ view, counters: next, moved })
  return moved
}
