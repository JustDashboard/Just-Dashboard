"use client"

import { InOut, RX, TX } from "@/components/in-out"
import type { NetworkLivePoint } from "@/lib/types"

/** @deprecated import `RX` and `TX` from components/in-out. */
export { RX, TX }

/**
 * A device's in and out as two figures in the network chart's colours, with
 * its last two minutes beside them.
 *
 * @deprecated use `<InOut rx tx unit="rate" layout="stacked" trend={points} />`
 * from components/in-out, which draws this and every other in/out pair.
 */
export function RatePair({
  rx,
  tx,
  points,
  className,
}: {
  rx: number
  tx: number
  points?: NetworkLivePoint[]
  className?: string
}) {
  return <InOut rx={rx} tx={tx} unit="rate" layout="stacked" trend={points} className={className} />
}
