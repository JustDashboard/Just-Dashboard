"use client"

import { useEffect, useState } from "react"
import { cn } from "@/lib/utils"
import { duration } from "@/lib/format"
import { TextShimmer } from "@/components/ui/text-shimmer"

export type OperationReading = {
  label: string
  detail?: string
  completed?: number
  total?: number
  showCount?: boolean
}

/** A wait is not a percentage: only a measured count fills the track. */
export function OperationProgress({
  label,
  detail,
  completed,
  total,
  startedAt,
  showCount = true,
  className,
}: OperationReading & { startedAt?: number | string; className?: string }) {
  const [mountedAt] = useState(() => Date.now())
  const [now, setNow] = useState(mountedAt)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  const parsed = typeof startedAt === "string" ? Date.parse(startedAt) : startedAt
  const since = parsed !== undefined && Number.isFinite(parsed) ? parsed : mountedAt
  const measured = total !== undefined && total > 0 && completed !== undefined
  const percent = measured ? Math.max(0, Math.min(100, (completed / total) * 100)) : undefined

  return (
    <div data-slot="operation-progress" className={cn("min-w-0 space-y-2", className)}>
      <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <p role="status" className="min-w-0 text-body">
          <TextShimmer>{label}</TextShimmer>
        </p>
        <span aria-hidden className="numeric shrink-0 text-hint text-muted-foreground">
          {duration(Math.max(0, Math.floor((now - since) / 1000)))} elapsed
        </span>
      </div>
      <div
        role="progressbar"
        aria-label={label}
        aria-valuemin={measured ? 0 : undefined}
        aria-valuemax={measured ? total : undefined}
        aria-valuenow={measured ? Math.max(0, Math.min(total, completed)) : undefined}
        className="relative h-1 overflow-hidden rounded-full bg-meter-track"
      >
        <span
          className={cn(
            "block h-full rounded-full bg-brand",
            measured
              ? "transition-[width]"
              : "absolute inset-y-0 left-0 w-1/3 animate-sweep motion-reduce:w-full motion-reduce:animate-none",
          )}
          style={measured ? { width: `${percent}%` } : undefined}
        />
      </div>
      {(detail || (measured && showCount)) && (
        <p className="min-w-0 text-hint break-words text-muted-foreground">
          {measured && showCount && (
            <span className="numeric">
              {completed} of {total} processed{detail ? " · " : ""}
            </span>
          )}
          {detail}
        </p>
      )}
    </div>
  )
}
