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

/**
 * A dialog's action in flight, drawn as part of `Modal`'s footer rather than
 * as a box of its own: the words take the footer's free side beside the
 * buttons, and the footer's top hairline becomes the track. The track is
 * positioned against the footer, so this only makes sense rendered there.
 *
 * A wait is not a percentage: only a measured count fills the track.
 */
export function OperationProgress({
  label,
  detail,
  completed,
  total,
  startedAt,
  showCount = true,
}: OperationReading & { startedAt?: number | string }) {
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
  const count = measured && showCount ? `${completed} of ${total} processed` : undefined

  return (
    // Where the buttons leave it less than its minimum — a phone, a long
    // confirm label — it takes the footer's first line and they the second.
    <div data-slot="operation-progress" className="mr-auto min-w-32 flex-1">
      <p role="status" className="text-body leading-tight">
        <TextShimmer className="block truncate">{label}</TextShimmer>
      </p>
      <p className="truncate text-hint text-muted-foreground">
        <span aria-hidden className="numeric">
          {duration(Math.max(0, Math.floor((now - since) / 1000)))} elapsed
        </span>
        {count && (
          <>
            {" · "}
            <span className="numeric">{count}</span>
          </>
        )}
        {detail && (
          <>
            {" · "}
            <span>{detail}</span>
          </>
        )}
      </p>
      <div
        role="progressbar"
        aria-label={label}
        aria-valuemin={measured ? 0 : undefined}
        aria-valuemax={measured ? total : undefined}
        aria-valuenow={measured ? Math.max(0, Math.min(total, completed)) : undefined}
        className="absolute inset-x-0 -top-px h-0.5 overflow-hidden"
      >
        <span
          className={cn(
            "block h-full bg-brand",
            measured
              ? "transition-[width]"
              : "absolute inset-y-0 left-0 w-1/3 animate-sweep motion-reduce:w-full motion-reduce:animate-none",
          )}
          style={measured ? { width: `${percent}%` } : undefined}
        />
      </div>
    </div>
  )
}
