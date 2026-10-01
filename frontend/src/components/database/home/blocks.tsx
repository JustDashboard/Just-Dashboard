"use client"

import Link from "next/link"
import { ArrowRight, Warning } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import type { PollState } from "@/hooks/use-poll"

/**
 * One block of a database's home: a title, a hairline, what was read.
 *
 * Every block on the page is plain (§2) — the gap between two of them is what
 * separates them — and every one answers the same three questions the same
 * way: not read yet is the silhouette of what is coming, could not be read
 * says so with a way to try again, and a poll that fails over something
 * already shown leaves it on screen and says only that it has stopped
 * updating.
 */
export function Block({
  title,
  actions,
  stale,
  className,
  bodyClassName,
  children,
}: {
  title: string
  actions?: React.ReactNode
  /** The last poll failed over what is drawn: it is the reading before, not now. */
  stale?: Error
  className?: string
  bodyClassName?: string
  children: React.ReactNode
}) {
  return (
    <Panel plain aria-label={title} className={className}>
      <PanelHeader
        title={title}
        actions={
          (stale || actions) && (
            <>
              {stale && <NotUpdating error={stale} />}
              {actions}
            </>
          )
        }
      />
      <PanelBody className={bodyClassName}>{children}</PanelBody>
    </Panel>
  )
}

/** The quiet way out of a block, to the page that holds all of what it shows the top of. */
export function BlockLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
    >
      {children} <ArrowRight className="size-3" />
    </Link>
  )
}

/** A reading that is the one before: the poll after it failed. */
export function NotUpdating({ error }: { error: Error }) {
  return (
    // A flex box: a bare span keeps a 16px line box and sets the status a
    // pixel under the centre line of the header it sits in.
    <span className="flex" title={errorMessage(error)}>
      <Status verdict="warning" label="Not updating" />
    </span>
  )
}

/**
 * A read that failed with nothing to show in its place. It says what could
 * not be read and why, in the server's words, and always offers the retry:
 * the reader is looking at a hole in the page and the only other way to fill
 * it is to reload everything around it.
 */
export function CouldNotRead({
  what,
  error,
  onRetry,
  className,
}: {
  /** What the block would have shown: "the busiest statements". */
  what: string
  error: Error
  onRetry: () => void
  className?: string
}) {
  return (
    <div role="status" className={className}>
      <Notice tone="warning" icon={Warning} title={`Could not read ${what}`}>
        <p className="wrap-anywhere">{errorMessage(error)}</p>
        <Button size="xs" variant="outline" className="mt-2" onClick={onRetry}>
          Try again
        </Button>
      </Notice>
    </div>
  )
}

/**
 * The three answers of one read, in one place: what it holds once it has
 * answered — and for as long as it has, whatever the polls after it do — the
 * failure when it never has, and the silhouette until either.
 */
export function Read<T>({
  poll,
  what,
  skeleton,
  children,
}: {
  poll: PollState<T>
  what: string
  skeleton: React.ReactNode
  children: (data: T) => React.ReactNode
}) {
  if (poll.data !== undefined) return <div className="animate-rise">{children(poll.data)}</div>
  if (poll.error) return <CouldNotRead what={what} error={poll.error} onRetry={poll.refresh} />
  return <>{skeleton}</>
}

/** The failure a block's header reports while its content stays: a poll that failed over data. */
export function staleOf<T>(poll: PollState<T>): Error | undefined {
  return poll.data !== undefined ? poll.error : undefined
}

/** A ranked list that has not arrived: names of uneven length over their bars. */
export function BarsSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="flex flex-col" aria-hidden>
      {Array.from({ length: rows }).map((_, index) => (
        <div key={index} className="flex items-center gap-3 px-2 py-1.5">
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <Skeleton className="h-3" style={{ width: `${62 - ((index * 17) % 34)}%` }} />
            <Skeleton className="h-1 w-full rounded-full" />
          </div>
          <Skeleton className="h-3 w-10 shrink-0" />
        </div>
      ))}
    </div>
  )
}

/** A list of rows that has not arrived: a mark, two lines, a figure. */
export function RowsSkeleton({ rows = 3, mark = true }: { rows?: number; mark?: boolean }) {
  return (
    <div className="divide-y divide-hairline" aria-hidden>
      {Array.from({ length: rows }).map((_, index) => (
        <div key={index} className="flex items-center gap-3 py-3">
          {mark && <Skeleton className="size-8 shrink-0 rounded-lg" />}
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <Skeleton className="h-3.5" style={{ width: `${44 - ((index * 11) % 20)}%` }} />
            <Skeleton className="h-3" style={{ width: `${58 - ((index * 7) % 22)}%` }} />
          </div>
          <Skeleton className="h-3 w-14 shrink-0" />
        </div>
      ))}
    </div>
  )
}

/**
 * Cards that have not arrived: a mark and two lines in each. Stacked unless
 * the caller lays them out as the cards themselves will be.
 */
export function CardsSkeleton({ count = 3, className }: { count?: number; className?: string }) {
  return (
    <div className={className ?? "space-y-2"} aria-hidden>
      {Array.from({ length: count }).map((_, index) => (
        <div key={index} className="flex min-h-14 items-center gap-3 rounded-xl border px-3 py-2.5">
          <Skeleton className="size-8 shrink-0 rounded-lg" />
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <Skeleton className="h-3.5" style={{ width: `${52 - index * 9}%` }} />
            <Skeleton className="h-3" style={{ width: `${70 - index * 12}%` }} />
          </div>
        </div>
      ))}
    </div>
  )
}

/** A list of facts that has not arrived: labels down one side, values down the other. */
export function FactsSkeleton({ rows = 4, className }: { rows?: number; className?: string }) {
  return (
    <div
      className={cn("grid grid-cols-[6rem_minmax(0,1fr)] gap-x-4 gap-y-2.5", className)}
      aria-hidden
    >
      {Array.from({ length: rows }).map((_, index) => (
        <div key={index} className="contents">
          <Skeleton className="h-3" style={{ width: `${70 - ((index * 13) % 30)}%` }} />
          <Skeleton className="h-3" style={{ width: `${64 - ((index * 19) % 38)}%` }} />
        </div>
      ))}
    </div>
  )
}

/** A sentence where a list would be: nothing to rank, and what would put something there. */
export function Quiet({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p className={cn("text-body leading-relaxed text-muted-foreground", className)} {...props} />
  )
}
