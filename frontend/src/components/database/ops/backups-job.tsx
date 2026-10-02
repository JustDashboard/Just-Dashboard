"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { ApiError, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job, JobLine } from "@/lib/types"
import { JobConsole } from "@/components/job-console"
import { FormNote } from "@/components/form"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { resultWords, transferKind, transferResult } from "@/components/database/ops/backups-model"
import type { DbTransferResult } from "@/components/database/ops/backups-types"

/** How often a running job is read. It is one cheap read of memory on the server. */
const EVERY_MS = 1000

export type WatchedJob = {
  job: Job
  lines: JobLine[]
  /** What it produced, once it has said. */
  result: DbTransferResult | undefined
}

/**
 * One dump, restore or copy, watched to its end.
 *
 * A transfer is a job on the server: it goes on whether or not this page is
 * open, and its lines are kept there. So the page attaches to one by id
 * rather than starting a stream — the one the listing says is running, the
 * one a form on this page just began, or one named in the address — and
 * reads it every second while it runs. When it ends the last reading stays
 * on screen with what it produced, until the reader dismisses it.
 *
 * `onEnd` fires once per job, on the reading that first shows it finished —
 * for a job that was running when the page attached to it (`live`). One that
 * was already over, opened from a link, is shown and not announced: nothing
 * ended while the reader watched.
 */
export function useTransferJob(
  watched: string,
  live: boolean,
  onEnd: (ended: WatchedJob) => void,
): { current: WatchedJob | undefined; missing: boolean; stop: () => Promise<void> } {
  const [state, setState] = useState<{ id: string; reading?: WatchedJob; missing?: boolean }>()
  const reported = useRef<string | undefined>(undefined)
  const onEndRef = useRef(onEnd)
  const liveRef = useRef(live)
  useEffect(() => {
    onEndRef.current = onEnd
    liveRef.current = live
  })

  useEffect(() => {
    if (!watched) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const controller = new AbortController()
    // Read once at attach: the job is the same one whatever `live` says later.
    let sawRunning = liveRef.current

    const read = async () => {
      try {
        const answer = await get<{ job: Job; lines: JobLine[] }>(
          `/jobs/${encodeURIComponent(watched)}`,
          undefined,
          controller.signal,
        )
        if (cancelled) return
        const reading: WatchedJob = {
          job: answer.job,
          lines: answer.lines,
          result: transferResult(answer.lines),
        }
        setState({ id: watched, reading })
        if (answer.job.status === "running") {
          sawRunning = true
          timer = setTimeout(read, EVERY_MS)
        } else if (sawRunning && reported.current !== watched) {
          reported.current = watched
          onEndRef.current(reading)
        }
      } catch (err) {
        if (cancelled || controller.signal.aborted) return
        // The server keeps its last fifty jobs in memory: one it no longer
        // has is gone, not failing. Anything else is a read that may recover.
        if (err instanceof ApiError && err.status === 404) {
          setState({ id: watched, missing: true })
          return
        }
        timer = setTimeout(read, EVERY_MS * 3)
      }
    }
    void read()
    return () => {
      cancelled = true
      controller.abort()
      clearTimeout(timer)
    }
  }, [watched])

  const stop = useCallback(async () => {
    if (!watched) return
    try {
      await post(`/jobs/${encodeURIComponent(watched)}/cancel`, {})
    } catch (err) {
      notify.error("Could not stop it", err)
    }
  }, [watched])

  const mine = state?.id === watched ? state : undefined
  return { current: mine?.reading, missing: Boolean(mine?.missing), stop }
}

/**
 * The operation in flight, where the dumps are: its own lines as the server
 * prints them, a way to stop it, and — once it has ended — what it produced.
 *
 * It is the product's job console (a pane of output you read), with a beam on
 * its edge for as long as the work runs.
 */
export function TransferProgress({
  watched,
  canStop,
  onStop,
  onDismiss,
  after,
}: {
  watched: WatchedJob
  /** The role may stop a job (`service.control`). */
  canStop: boolean
  onStop: () => void
  onDismiss: () => void
  /** What can be done with the result: open the new database, restore the safety dump. */
  after?: React.ReactNode
}) {
  const { job, lines, result } = watched
  const running = job.status === "running"
  const kind = transferKind(job)
  const produced = resultWords(kind, result)
  return (
    <section
      aria-label={running ? `${job.title}, running` : `${job.title}, ${job.status}`}
      data-slot="transfer-progress"
      className="min-w-0 animate-rise space-y-2"
    >
      <div className="relative rounded-xl">
        <JobConsole
          job={job}
          // The result line is the job's answer to the page, not output.
          lines={lines.filter((line) => line.stream !== "result")}
          onCancel={running && canStop ? onStop : undefined}
          onDismiss={running ? undefined : onDismiss}
        />
        {running && (
          <span aria-hidden className="pointer-events-none absolute inset-0 rounded-xl">
            <BorderBeam size={96} duration={4} />
          </span>
        )}
      </div>
      {!running && (produced || after) && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
          {produced && (
            <FormNote className="min-w-0 flex-1 font-mono break-words">{produced}</FormNote>
          )}
          {after}
        </div>
      )}
    </section>
  )
}

/** A quiet command under a finished job: "Open shop_copy". */
export function AfterAction({
  children,
  pending,
  onClick,
}: {
  children: React.ReactNode
  pending?: boolean
  onClick: () => void
}) {
  return (
    <Button size="sm" variant="outline" pending={pending} onClick={onClick}>
      {children}
    </Button>
  )
}
