"use client"

import { useEffect, useState, type ReactNode } from "react"
import { LoaderCircle } from "@/components/icons"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle aria-hidden className={cn("size-4 animate-spin", className)} />
}

/**
 * Rows of skeleton. `hold` keeps what is written under them, and the blocks of
 * the page under the one they are in, off the page while they stand (`Hold`):
 * for a block with blocks under it whose answer is not the skeleton's height.
 */
export function LoadingRows({
  rows = 5,
  className,
  hold,
}: {
  rows?: number
  className?: string
  hold?: boolean
}) {
  return (
    <>
      <div className={cn("space-y-2", className)}>
        {Array.from({ length: rows }).map((_, i) => (
          <Skeleton key={i} className="h-9 w-full rounded-md" />
        ))}
      </div>
      {hold && <Hold until={false} />}
    </>
  )
}

/** How long a block is held for the read above it: past this the page shows what it has. */
export const HOLD_AT_MOST_MS = 4000

/**
 * Holds what is written under it off the page until the read above it has had
 * its first answer, so that nothing already on the page moves when that answer
 * lands taller than its skeleton, or lands as a block that was not there.
 *
 *     {day.data && <DayReadings day={day.data} />}
 *     <Hold until={!day.loading} />
 *     <Panel>…</Panel>
 *
 * Its children are what stands there meanwhile, a skeleton, and go when it
 * lets go. What is held is mounted, so its own reads start with the page, and it is not
 * drawn (the rule beside `jd-rise` in globals.css). Put it after the block
 * that is still loading: in a page it holds every block of the page after the
 * one it stands in, and anywhere else what follows it in its own container.
 *
 * It lets go once and for good: a filter or a refresh that makes the read
 * above load again does not take the page under it away. It also lets go
 * after `HOLD_AT_MOST_MS`, because a read that hangs on a troubled host must
 * not keep the rest of the page from the person who came to look at it.
 */
export function Hold({ until, children }: { until: boolean; children?: ReactNode }) {
  const [open, setOpen] = useState(until)
  if (until && !open) setOpen(true)
  useEffect(() => {
    if (open) return
    const timer = setTimeout(() => setOpen(true), HOLD_AT_MOST_MS)
    return () => clearTimeout(timer)
  }, [open])
  if (open) return null
  return (
    <>
      {children}
      <span data-hold="" hidden />
    </>
  )
}

/**
 * The skeleton for a panel that is about to hold a table.
 *
 * Its first row is heavier than the rest so the placeholder has the same
 * silhouette as the thing arriving — a header strip over rows — rather than
 * an undifferentiated stack of grey bars that jumps when the data lands.
 *
 * `plain` is the same silhouette with no frame, for a page whose blocks arrive
 * as plain panels (§2): a framed box that gives way to unframed rows is the
 * jump this placeholder exists to prevent, one edge later. Its title and rows
 * start on the page's own edge, where the plain block's will.
 *
 * `hold` is `LoadingRows`' own: what is under the panel waits for it.
 */
export function LoadingPanel({
  rows = 6,
  plain,
  className,
  hold,
}: {
  rows?: number
  plain?: boolean
  className?: string
  hold?: boolean
}) {
  const gutter = plain ? "px-0" : "px-5"
  return (
    <>
      <div
        className={cn("min-w-0 overflow-hidden", !plain && "rounded-xl border bg-card", className)}
      >
        <div className={cn("flex min-h-12 items-center border-b border-hairline py-3", gutter)}>
          <Skeleton className="h-4 w-40" />
        </div>
        <div className="divide-y divide-hairline">
          {Array.from({ length: rows }).map((_, i) => (
            <div key={i} className={cn("flex items-center gap-3 py-3.5", gutter)}>
              <Skeleton className="h-3.5 flex-1" style={{ maxWidth: `${34 + ((i * 13) % 26)}%` }} />
              <Skeleton className="h-3.5 w-16" />
              <Skeleton className="h-3.5 w-24" />
            </div>
          ))}
        </div>
      </div>
      {hold && <Hold until={false} />}
    </>
  )
}
