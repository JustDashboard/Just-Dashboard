"use client"

import { rate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Sparkline } from "@/components/metrics/sparkline"
import type { NetworkLivePoint } from "@/lib/types"

export const RX = "var(--chart-5)"
export const TX = "var(--chart-2)"

/**
 * A device's in and out as two figures in the network chart's colours, with
 * its last two minutes under them — what a row in a list of devices reads
 * at a glance: which way the bytes go, how many, and whether that is new.
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
  const tail = (points ?? []).slice(-60)
  return (
    <span className={cn("flex items-center gap-3", className)}>
      {tail.length > 1 && (
        <span className="relative hidden h-6 w-20 sm:block">
          <Sparkline
            values={tail.map((p) => p.rx)}
            color={RX}
            width={80}
            height={24}
            className="absolute inset-0 h-6 w-20"
            label="Received over the last two minutes"
          />
          <Sparkline
            values={tail.map((p) => p.tx)}
            color={TX}
            width={80}
            height={24}
            className="absolute inset-0 h-6 w-20 opacity-80"
            label="Sent over the last two minutes"
          />
        </span>
      )}
      <span className="numeric grid min-w-[5.5rem] text-right font-mono text-micro leading-tight">
        <span style={{ color: RX }}>↓ {rate(rx)}</span>
        <span style={{ color: TX }}>↑ {rate(tx)}</span>
      </span>
    </span>
  )
}
