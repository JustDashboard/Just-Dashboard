"use client"

import { useCallback } from "react"
import { bytes } from "@/lib/format"
import type { MountStats } from "@/lib/types"
import type { MetricsState } from "@/lib/metrics-store"
import { useMetricsValue } from "@/hooks/use-metrics"
import { Meter, utilisationTone } from "@/components/meter"
import { mountOf } from "@/components/files/media"

/** The three figures the footer draws; a frame that moves none of them draws nothing. */
function sameRoom(a: MountStats | undefined, b: MountStats | undefined) {
  return (
    a === b ||
    (!!a &&
      !!b &&
      a.mountpoint === b.mountpoint &&
      a.free === b.free &&
      a.usedPercent === b.usedPercent)
  )
}

/**
 * How much room is left on the disk the browsed folder is on, from the metrics
 * stream the shell already keeps open: a cloud says how much room is left, and
 * so does this.
 *
 * Its own component because the reading arrives every two seconds. Read by the
 * page, each frame drew the page and every row of the folder again (170 to
 * 200 ms of a 521-row folder, every two seconds, with nobody touching it);
 * read here, a frame draws these two elements, and only when a figure moved.
 */
export function DiskRoom({ path }: { path: string | null }) {
  const mount = useMetricsValue(
    useCallback((state: MetricsState) => mountOf(state.snapshot?.mounts, path), [path]),
    sameRoom,
  )
  if (!mount) return null
  return (
    <span className="flex min-w-0 shrink-0 items-center gap-2">
      <Meter
        value={mount.usedPercent}
        tone={utilisationTone(mount.usedPercent)}
        size="thin"
        label={`Disk used on ${mount.mountpoint}`}
        className="w-20"
      />
      <span className="numeric truncate">
        {bytes(mount.free)} free on {mount.mountpoint}
      </span>
    </span>
  )
}
