"use client"

import { cn } from "@/lib/utils"
import { bytes, percent, rate } from "@/lib/format"
import type { MountStats } from "@/lib/types"
import { Meter, utilisationTone } from "@/components/meter"
import { Tag } from "@/components/tag"

/**
 * One disk in the order the question is asked: which one and how much is
 * left, the bar, then what it is and what it is doing. The free space takes
 * the bar's tone, because past the warning line that figure is the finding.
 *
 * The Overview draws these after its readings (`overview/resources.tsx`).
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
