"use client"

import { useSyncExternalStore } from "react"
import Link from "next/link"
import {
  getCrosshair,
  getServerCrosshair,
  subscribeCrosshair,
  pinCrosshair,
  unpinCrosshair,
} from "@/lib/metrics-crosshair"
import { timestamp } from "@/lib/format"
import { Button } from "@/components/ui/button"

export function MomentInspector({ samples }: { samples: { ts: number }[] }) {
  const moment = useSyncExternalStore(subscribeCrosshair, getCrosshair, getServerCrosshair)
  if (!moment.pinned || moment.ts === null)
    return <span className="text-hint text-muted-foreground">Click a chart to pin a moment</span>
  const at = samples.reduce(
    (best, row, index) =>
      Math.abs(row.ts - moment.ts!) < Math.abs(samples[best].ts - moment.ts!) ? index : best,
    0,
  )
  const query = new URLSearchParams({
    source: "journal:",
    mode: "search",
    since: new Date(moment.ts - 60000).toISOString(),
    until: new Date(moment.ts + 60000).toISOString(),
  })
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-2">
      <span role="status" className="numeric text-hint">
        Pinned {timestamp(new Date(moment.ts).toISOString())}
      </span>
      <Button
        size="xs"
        variant="ghost"
        disabled={at <= 0 || !samples.length}
        onClick={() => pinCrosshair(samples[at - 1].ts)}
      >
        Previous sample
      </Button>
      <Button
        size="xs"
        variant="ghost"
        disabled={!samples.length || at >= samples.length - 1}
        onClick={() => pinCrosshair(samples[at + 1].ts)}
      >
        Next sample
      </Button>
      <Button size="xs" variant="outline" asChild>
        <Link href={`/logs?${query}`}>Logs around this moment</Link>
      </Button>
      <Button size="xs" variant="ghost" onClick={unpinCrosshair}>
        Release moment
      </Button>
    </div>
  )
}
