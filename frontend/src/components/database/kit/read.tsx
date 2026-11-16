"use client"

import { useState } from "react"
import { duration } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { PollState } from "@/hooks/use-poll"
import { useElapsed } from "@/hooks/use-elapsed"
import { FormNote } from "@/components/form"
import { Sweep } from "@/components/loading"
import { Stale as StaleMark } from "@/components/stale"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The three answers of one read, in one place, for every reading block of a
 * database's pages: what it holds once it has answered — and for as long as
 * it has, whatever the polls after it do — the failure when it never has,
 * with the way to ask again, and the silhouette of what is coming until
 * either.
 *
 * The Home had `Read` and Performance `ViewRead`; this is both.
 *
 * - `patience` is Performance's: after a few seconds a wait says what is
 *   being waited for and for how long, and asking again after a failure shows
 *   the wait again rather than the old failure. Off by default, so a block
 *   that never had the waiting line does not gain one.
 * - `locking` says the read asks the engine for locks, so a long wait is most
 *   likely a wait behind one, and the failure after a long silence says so
 *   with the way to the view that shows who is in the way.
 * - `rise` lets what was read rise into place, as the Home's blocks do.
 */
export function Read<T>({
  poll,
  what,
  skeleton,
  patience = false,
  locking,
  rise = false,
  children,
}: {
  poll: PollState<T>
  /** What the block would have shown: "the busiest statements". */
  what: string
  skeleton: React.ReactNode
  patience?: boolean
  locking?: boolean
  rise?: boolean
  children: (data: T) => React.ReactNode
}) {
  if (poll.data !== undefined) {
    return rise ? <div className="animate-rise">{children(poll.data)}</div> : children(poll.data)
  }
  if (patience) {
    return <PatientRead poll={poll} what={what} skeleton={skeleton} locking={locking} />
  }
  if (poll.error) return <ErrorState what={what} error={poll.error} onRetry={poll.refresh} />
  return skeleton
}

function PatientRead<T>({
  poll,
  what,
  skeleton,
  locking,
}: {
  poll: PollState<T>
  what: string
  skeleton: React.ReactNode
  locking?: boolean
}) {
  const [opened] = useState(() => Date.now())
  const [retry, setRetry] = useState<{ after: Error; at: number } | null>(null)
  const error = poll.error
  // The failure on screen is the one before the retry until the retry settles.
  const asking = retry !== null && retry.after === error
  if (error && !asking) {
    return (
      <div className="space-y-3 group-data-[plain]/panel:pt-4">
        <ErrorState
          what={what}
          error={error}
          onRetry={() => {
            setRetry({ after: error, at: Date.now() })
            poll.refresh()
          }}
        />
        {locking && <GaveUp error={error} />}
      </div>
    )
  }
  return (
    <>
      <StillWaiting since={asking ? retry.at : opened} what={what} locking={locking} />
      {skeleton}
    </>
  )
}

/** How long a wait is before the page says it is one. */
export const PATIENCE_MS = 4000

/**
 * A read that has not answered in the time a read takes. It names what is
 * being waited for and counts, so a skeleton that stays is never mistaken for
 * a page that has stopped.
 */
export function StillWaiting({
  since,
  what,
  locking,
}: {
  since: number
  what: string
  locking?: boolean
}) {
  const { engine } = useDatabase()
  const waited = useElapsed(since, 1000)
  if (waited < PATIENCE_MS) return null
  return (
    <div role="status" className="animate-rise space-y-2 pb-4 group-data-[plain]/panel:pt-4">
      <p className="flex flex-wrap items-center gap-x-2 text-body">
        <TextShimmer>{`Waiting for ${engine.label} to answer`}</TextShimmer>
        <span aria-hidden className="numeric text-muted-foreground">
          {duration(waited / 1000)}
        </span>
      </p>
      <Sweep rounded className="h-0.5" />
      {locking ? (
        <BehindALock>
          It has been asked for {what} and has not answered yet. This read waits for a moment&apos;s
          lock on what it measures, so it stands behind any session that holds an exclusive lock or
          is queued for one.
        </BehindALock>
      ) : (
        <FormNote>It has been asked for {what} and has not answered yet.</FormNote>
      )}
    </div>
  )
}

/** How long a failed read had been waiting, where the read was timed (`performance-api`). */
function waitedSeconds(error: Error): number | undefined {
  const waited = (error as { waitedMs?: unknown }).waitedMs
  return typeof waited === "number" ? waited / 1000 : undefined
}

/** A failure that came after a long silence: the read was given up, not refused. */
function GaveUp({ error }: { error: Error }) {
  const waited = waitedSeconds(error)
  if (waited === undefined || waited < 15) return null
  return (
    <BehindALock>
      The read was given up after {duration(waited)} without an answer, which is what a read queued
      behind a lock looks like: it waits for a moment&apos;s lock on what it measures, behind any
      session that holds an exclusive lock or is queued for one.
    </BehindALock>
  )
}

/** A sentence about a read held up by a lock, with the way to the view that shows who holds it. */
function BehindALock({ children }: { children: React.ReactNode }) {
  const { engine, param, goto } = useDatabase()
  const elsewhere = engine.can("locks") && param("view") !== "locks"
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <FormNote className="max-w-[44rem] min-w-0 flex-1 basis-80">{children}</FormNote>
      {elsewhere && (
        <Button size="xs" variant="outline" onClick={() => goto("performance", { view: "locks" })}>
          See who is waiting on whom
        </Button>
      )}
    </div>
  )
}

/** The failure a block's header reports while its content stays: a poll that failed over data. */
export function staleOf<T>(poll: PollState<T>): Error | undefined {
  return poll.data !== undefined ? poll.error : undefined
}

/**
 * The mark a block's header carries while its last poll has failed over what
 * is drawn: the shared `Stale`, fed the poll.
 */
export function Stale<T>({ poll, className }: { poll: PollState<T>; className?: string }) {
  return <StaleMark error={staleOf(poll)} className={cn(className)} />
}
