"use client"

import { useState } from "react"
import { useNow } from "@/hooks/use-now"
import { duration } from "@/lib/format"
import { Sweep } from "@/components/loading"
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
 * A wait is not a percentage: only a measured count fills the track. Anything
 * else is the product's one indeterminate bar, `Sweep`, with its one answer
 * to reduced motion.
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
  const now = useNow()
  const parsed = typeof startedAt === "string" ? Date.parse(startedAt) : startedAt
  const since = parsed !== undefined && Number.isFinite(parsed) ? parsed : mountedAt
  const measured = total !== undefined && total > 0 && completed !== undefined
  const percent = measured ? Math.max(0, Math.min(100, (completed / total) * 100)) : undefined
  const count = measured && showCount ? `${completed} of ${total} processed` : undefined

  return (
    // Where the buttons leave it less than its minimum — a narrow phone, a
    // long confirm label — it takes the footer's first line and they the
    // second. At 390px, Cancel and a pending Delete sat within a few pixels of
    // a 128px minimum, so a renderer whose glyphs ran wider wrapped the footer
    // and moved the dialog; 112px leaves them room to spare.
    <div data-slot="operation-progress" className="mr-auto min-w-28 flex-1">
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
      {measured ? (
        <div
          role="progressbar"
          aria-label={label}
          aria-valuemin={0}
          aria-valuemax={total}
          aria-valuenow={Math.max(0, Math.min(total, completed))}
          className="absolute inset-x-0 -top-px h-0.5 overflow-hidden"
        >
          <span
            className="block h-full bg-brand transition-[width]"
            style={{ width: `${percent}%` }}
          />
        </div>
      ) : (
        // The footer's hairline is the track, so the bar runs on its own.
        <Sweep label={label} track={false} className="absolute inset-x-0 -top-px h-0.5" />
      )}
    </div>
  )
}
