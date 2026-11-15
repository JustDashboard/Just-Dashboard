"use client"

import { useEffect, useState, type ReactNode } from "react"
import { LoaderCircle } from "@/components/icons"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

/**
 * The spinning ring. Inside a pending `Button` it is decoration: the button
 * sets `aria-busy` and keeps its label. Standing alone it says nothing to a
 * screen reader unless it is given `label`, which it then speaks once, as a
 * status ("Loading the containers").
 */
export function Spinner({ className, label }: { className?: string; label?: string }) {
  const ring = <LoaderCircle aria-hidden className={cn("size-4 animate-spin", className)} />
  if (!label) return ring
  return (
    <>
      {ring}
      <Said label={label} />
    </>
  )
}

/** What a skeleton stands for, said once to a screen reader and not drawn. */
function Said({ label }: { label: string }) {
  return (
    <span role="status" data-slot="loading-label" className="sr-only">
      {label}
    </span>
  )
}

/**
 * Rows of skeleton: the fallback for a list whose shape is not known yet. A
 * list of `Row`s waits as `RowsSkeleton` (`row-list.tsx`), a `ChoiceList` as
 * `ChoiceListSkeleton` (`choice-row.tsx`).
 *
 * `hold` keeps what is written under them, and the blocks of
 * the page under the one they are in, off the page while they stand (`Hold`):
 * for a block with blocks under it whose answer is not the skeleton's height.
 *
 * `label` is what is being read, said to a screen reader as a status while
 * the rows stand ("Loading the accounts"): grey bars are silent, and a reader
 * who cannot see them heard an empty page.
 */
export function LoadingRows({
  rows = 5,
  className,
  hold,
  label,
}: {
  rows?: number
  className?: string
  hold?: boolean
  label?: string
}) {
  return (
    <>
      <div
        data-slot="loading-rows"
        aria-busy={label ? true : undefined}
        className={cn("space-y-2", className)}
      >
        {label && <Said label={label} />}
        {Array.from({ length: rows }).map((_, i) => (
          <Skeleton key={i} className="h-9 w-full rounded-md" />
        ))}
      </div>
      {hold && <Hold until={false} />}
    </>
  )
}

/**
 * The bar for work whose progress cannot be known: a third of the track
 * crossing it (`animate-sweep`), for a pull, a prune, a query that has not
 * answered. It was copied seventeen times with three fills and three answers
 * to reduced motion — the whole track, nothing, or a third frozen at the left
 * edge, which reads as a third done.
 *
 * Reduced motion has one answer now, the same whether it was asked for in the
 * system or by the product's own "Pause animations" (`data-motion="paused"`):
 * the bar stops and fills the track in the faint ink, a line that says "under
 * way" without moving and without claiming a fraction.
 *
 * The caller places it with `className` (`absolute inset-x-0 bottom-0 h-px`
 * under a pane's head, `h-0.5 w-24` under a splash). `track` draws the
 * channel the bar runs in; `quiet` is the foreground fill for a bar drawn over
 * a reading that is already coloured. With `label` it is a `progressbar` of
 * that name with no value, which is how an indeterminate one is announced;
 * without, it is decoration beside words that say what is happening.
 */
export function Sweep({
  tone = "brand",
  rounded,
  track = true,
  label,
  className,
}: {
  tone?: "brand" | "quiet"
  rounded?: boolean
  track?: boolean
  label?: string
  className?: string
}) {
  return (
    <div
      data-slot="sweep"
      role={label ? "progressbar" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      className={cn(
        "relative overflow-hidden",
        track && "bg-meter-track",
        rounded && "rounded-full",
        className,
      )}
    >
      <span
        className={cn(
          "absolute inset-y-0 left-0 block w-1/3 animate-sweep",
          tone === "brand" ? "bg-brand" : "bg-foreground/20",
          rounded && "rounded-full",
          SWEEP_STILL,
        )}
      />
    </div>
  )
}

/**
 * Sweep's one reduced-motion rule, for both ways of asking: the bar stops and
 * fills the track in the faint ink. Exported for the unit test that holds it.
 */
export const SWEEP_STILL = [
  "motion-reduce:w-full motion-reduce:animate-none motion-reduce:bg-faint",
  "[:root[data-motion=paused]_&]:w-full [:root[data-motion=paused]_&]:animate-none [:root[data-motion=paused]_&]:bg-faint",
].join(" ")

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
 * `hold` and `label` are `LoadingRows`' own: what is under the panel waits for
 * it, and what it stands for is said to a screen reader.
 */
export function LoadingPanel({
  rows = 6,
  plain,
  className,
  hold,
  label,
}: {
  rows?: number
  plain?: boolean
  className?: string
  hold?: boolean
  label?: string
}) {
  const gutter = plain ? "px-0" : "px-5"
  return (
    <>
      <div
        data-slot="loading-panel"
        aria-busy={label ? true : undefined}
        className={cn("min-w-0 overflow-hidden", !plain && "rounded-xl border bg-card", className)}
      >
        {label && <Said label={label} />}
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
