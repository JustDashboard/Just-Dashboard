"use client"

import { cn } from "@/lib/utils"
import { bytes, percent, rate } from "@/lib/format"
import type { MountStats } from "@/lib/types"
import { Meter, utilisationTone } from "@/components/meter"
import { StatTile } from "@/components/stat-tile"
import { Tag } from "@/components/tag"
import { LiveBytes, SeriesKey } from "@/components/overview/readings"

/**
 * Past this many the fullest are drawn and the rest are on /metrics: a host
 * with a dozen mounts would otherwise push Health off the first screen.
 */
const SHOWN = 4

/**
 * What the disks hold, as a band under the readings that move.
 *
 * Storage was the fifth tile: the fullest filesystem's free space over a thin
 * meter, the one tile in the row that fills rather than moves, drawing a line
 * where its four neighbours drew an hour. How close a disk is to full is read
 * off a length better than off a figure, so each real filesystem is a wide
 * bar of its own, and a second disk that the tile could not mention is on the
 * page beside the root one. What the disks are doing is the
 * band's other half, a reading like the four above it — live, with its hour —
 * set under Network so the band keeps the tiles' columns.
 */
export function StorageBand({
  mounts,
  trend,
  color,
}: {
  mounts: MountStats[]
  /** The disks' read and write over the hour, drawn as the tiles draw theirs. */
  trend?: React.ReactNode
  color: string
}) {
  const read = mounts.reduce((sum, m) => sum + m.readRate, 0)
  const write = mounts.reduce((sum, m) => sum + m.writeRate, 0)
  const shown =
    mounts.length > SHOWN
      ? [...mounts]
          .sort((a, b) => b.usedPercent - a.usedPercent)
          .slice(0, SHOWN)
          .sort((a, b) => a.mountpoint.localeCompare(b.mountpoint))
      : mounts

  return (
    <div
      data-slot="storage-band"
      className="grid min-w-0 border-t border-hairline xl:grid-cols-4 [&>*]:min-w-0"
    >
      <div className="flex flex-col gap-3 px-5 py-4 xl:col-span-3">
        <div className="flex min-w-0 items-baseline justify-between gap-4">
          <p className="eyebrow">Storage</p>
          {mounts.length > shown.length && (
            <p className="numeric text-hint text-muted-foreground">
              {mounts.length - shown.length} more on Metrics
            </p>
          )}
        </div>

        {mounts.length === 0 ? (
          <p className="text-body text-muted-foreground">No filesystems reported</p>
        ) : (
          // Two to a row once there is a second disk, and centred in the
          // height the I/O reading beside them sets, so one root partition
          // is not a bar along the top of an empty box.
          <ul
            aria-label="Filesystems"
            className={cn(
              "grid min-w-0 flex-1 content-center gap-x-10 gap-y-4",
              shown.length > 1 && "lg:grid-cols-2",
            )}
          >
            {shown.map((mount) => (
              <Filesystem key={mount.mountpoint} mount={mount} />
            ))}
          </ul>
        )}
      </div>

      <StatTile
        className="border-t border-hairline xl:border-t-0 xl:border-l"
        label={
          <>
            <SeriesKey color={color} />
            Disk I/O
          </>
        }
        value={<LiveBytes value={read + write} suffix="/s" />}
        trend={trend}
        hint={`${rate(read)} read · ${rate(write)} write`}
      />
    </div>
  )
}

/**
 * One disk in the order the question is asked: which one and how much is
 * left, the bar, then what it is and what it is doing. The free space takes
 * the bar's tone, because past the warning line that figure is the finding.
 *
 * Metrics draws its filesystems with this row too, with its scan as the
 * `action` beside the name and the scan's answer as `children` under it.
 */
export function Filesystem({
  mount,
  action,
  children,
}: {
  mount: MountStats
  action?: React.ReactNode
  children?: React.ReactNode
}) {
  const tone = utilisationTone(mount.usedPercent)
  return (
    <li className="min-w-0 space-y-2">
      <div className="flex min-w-0 items-baseline justify-between gap-3">
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="truncate text-body font-medium">{mount.mountpoint}</span>
          <Tag>{mount.fstype}</Tag>
          {action}
        </span>
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          <span
            className={cn(
              "text-title font-semibold tracking-tight text-foreground",
              tone === "warning" && "text-warning",
              tone === "danger" && "text-destructive",
            )}
          >
            {bytes(mount.free)}
          </span>{" "}
          free
        </span>
      </div>
      <Meter
        value={mount.usedPercent}
        tone={tone}
        label={`${mount.mountpoint} used`}
        className="h-2"
      />
      <div className="numeric flex min-w-0 justify-between gap-3 text-hint text-muted-foreground">
        <span className="truncate">
          {percent(mount.usedPercent, 0)} of {bytes(mount.total)} · {mount.device}
        </span>
        <span className="shrink-0">
          {rate(mount.readRate)} read · {rate(mount.writeRate)} write
        </span>
      </div>
      {children}
    </li>
  )
}
