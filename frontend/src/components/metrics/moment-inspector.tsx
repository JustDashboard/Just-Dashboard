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

/**
 * The pinned instant and what can be done with it, held at the top of the
 * scroll while a moment is pinned — the chart it was pinned from is usually a
 * screen below the page's top. Nothing at all while no moment is: the
 * sentence that stood here ("Click a chart to pin a moment") was a caption on
 * every visit, and how to pin one is in the page's shortcuts.
 */
export function MomentInspector({ samples }: { samples: { ts: number }[] }) {
  const moment = useSyncExternalStore(subscribeCrosshair, getCrosshair, getServerCrosshair)
  if (!moment.pinned || moment.ts === null) return null
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
    <div
      data-slot="moment-inspector"
      className="sticky top-0 z-20 -my-3 flex min-w-0 flex-wrap items-center gap-2 border-b border-hairline bg-background py-3"
    >
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
