import { useState } from "react"

/**
 * The keys of a polled list that were not in it the last time it changed —
 * the rows a poll just brought, for them to take `animate-rise` (§11
 * *arrived*).
 *
 * A new deployment, a delivery, a container event used to appear in its list
 * with nothing to say it had not been there a moment ago, so a list that
 * gained a row read exactly like one that had merely re-rendered. Only the
 * run transcript let its new lines rise.
 *
 * Empty on the first render: the page's own rise covers what arrived with it,
 * and forty rows rising at once is not forty arrivals. The answer is held
 * until the keys change again rather than recomputed on every render, so a
 * re-render for some other reason halfway through a rise does not take the
 * class away and cut the motion short. It is kept as state, adjusted during
 * render when the keys change, which is React's own pattern for deriving from
 * a previous render and costs no effect and no second paint. Reduced motion
 * needs nothing here: the root rule in `globals.css` collapses the keyframe.
 */
export function useArrivals(keys: string[]): Set<string> {
  const signature = keys.join("\n")
  const [seen, setSeen] = useState(() => ({
    signature,
    keys: new Set(keys),
    arrived: new Set<string>(),
  }))
  if (seen.signature === signature) return seen.arrived
  const arrived = new Set(keys.filter((key) => !seen.keys.has(key)))
  setSeen({ signature, keys: new Set(keys), arrived })
  return arrived
}
