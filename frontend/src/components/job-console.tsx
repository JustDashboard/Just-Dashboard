"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { CheckCircle, CrossCircle, Slash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { recentLabels } from "@/lib/recent-jobs"
import { cn } from "@/lib/utils"
import type { Job, JobLine } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { Spinner } from "@/components/loading"
import { ConsoleView, type ConsoleState } from "@/components/console-view"
import { Tag } from "@/components/tag"
import { jobLineToTranscript } from "@/components/transcript-line"

/**
 * Watching an operation that outlives the page.
 *
 * The compose runner already streams, and its socket owns the command: closing
 * the tab kills it, and it refuses to reconnect because reconnecting would run
 * it again. That is right for `docker compose up` and wrong for a certificate
 * issuance or a half-hour package upgrade — neither should die because a VPN
 * blinked, and neither should start over because somebody reopened the page.
 *
 * So this console attaches to a job by id rather than starting one.
 * Reconnecting is not merely allowed here, it is the point: the socket resumes
 * from the last sequence it saw, so a dropped connection costs nothing and a
 * console opened ten minutes late still shows everything from the beginning.
 */
export function useJobConsole(options?: { onSuccess?: () => void }) {
  const [job, setJob] = useState<Job | null>(null)
  const [lines, setLines] = useState<JobLine[]>([])
  // The highest sequence seen, kept in a ref because a reconnect must ask for
  // what it actually missed rather than for what the first render knew about.
  const lastSeq = useRef(0)
  // The status we last saw, so the running → succeeded edge can fire a
  // callback (a page refreshing its data) without the page watching `job` in
  // an effect. Held in a ref, and the callback too, so neither rebuilds the
  // socket.
  const lastStatus = useRef<Job["status"] | undefined>(undefined)
  const onSuccessRef = useRef(options?.onSuccess)
  useEffect(() => {
    onSuccessRef.current = options?.onSuccess
  })

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "output") {
      // Deduplicated by sequence rather than by asking the server to resume
      // from one. A reconnect re-sends the buffer, which costs a few thousand
      // lines once and keeps the socket's identity stable — putting the
      // sequence in the query would rebuild the socket on every batch, which
      // is a reconnect loop rather than a resume.
      const batch = (envelope.data as JobLine[]).filter((l) => l.seq > lastSeq.current)
      if (batch.length === 0) return
      lastSeq.current = batch[batch.length - 1].seq
      setLines((prev) => {
        const next = [...prev, ...batch]
        // Bounded for the same reason the log panel is: an upgrade of two
        // hundred packages emits thousands of lines, and holding all of them
        // in React state makes the tab unusable long before it runs out of
        // memory. The server keeps its own buffer either way.
        return next.length > 4000 ? next.slice(next.length - 4000) : next
      })
    } else if (envelope.type === "job") {
      const next = envelope.data as Job
      // The edge into a finished run, however short — a job that goes
      // pending → succeeded with no observed running frame still counts.
      if (lastStatus.current !== "succeeded" && next.status === "succeeded") {
        onSuccessRef.current?.()
      }
      lastStatus.current = next.status
      setJob(next)
    }
  }, [])

  const running = job?.status === "running"
  useSocket(job ? `/jobs/${job.id}/stream` : "", {
    onMessage,
    // Reconnecting re-attaches rather than re-running, so the socket's own
    // backoff is exactly what is wanted here — the opposite of the compose
    // runner, which has to refuse it.
    enabled: Boolean(job) && Boolean(running),
  })

  /** Attaches to a job, replacing whatever was on screen. */
  const attach = useCallback((next: Job) => {
    lastSeq.current = 0
    lastStatus.current = next.status
    setLines([])
    setJob(next)
  }, [])

  /** Re-opens a job that finished earlier, output and all. */
  const open = useCallback(async (id: string) => {
    try {
      const res = await get<{ job: Job; lines: JobLine[] }>(`/jobs/${id}`)
      lastSeq.current = res.lines.length > 0 ? res.lines[res.lines.length - 1].seq : 0
      lastStatus.current = res.job.status
      setLines(res.lines)
      setJob(res.job)
    } catch (err) {
      notify.error("Could not open that operation", err)
    }
  }, [])

  const dismiss = useCallback(() => {
    setJob(null)
    setLines([])
    lastSeq.current = 0
    lastStatus.current = undefined
  }, [])

  const cancel = useCallback(async () => {
    if (!job) return
    try {
      await post(`/jobs/${job.id}/cancel`, {})
    } catch (err) {
      notify.error("Could not stop it", err)
    }
  }, [job])

  return { job, lines, attach, open, dismiss, cancel, running: Boolean(running) }
}

/** A job's status as the console's state. */
const JOB_STATE: Record<Job["status"], ConsoleState> = {
  running: "running",
  succeeded: "ok",
  failed: "failed",
  cancelled: "cancelled",
}

/**
 * A job's console (`ConsoleView`): its lines numbered from the job's own
 * sequence (`jobLineToTranscript`), its state as a word, Stop for a reader
 * the job's route would let start it, and a note while it runs that closing
 * the page does not stop it — the recent list beside it is the way back.
 */
export function JobConsole({
  job,
  lines,
  onDismiss,
  onCancel,
  className,
}: {
  job: Job | null
  lines: JobLine[]
  onDismiss?: () => void
  onCancel?: () => void
  className?: string
}) {
  const { can } = useAuth()
  const shown = useMemo(
    () => lines.map((line) => ({ key: line.seq, line: jobLineToTranscript(line) })),
    [lines],
  )
  if (!job) return null
  const state = JOB_STATE[job.status]
  const running = state === "running"
  return (
    <ConsoleView
      title={job.title}
      state={state}
      detail={!running && job.endedAt ? relativeTime(job.endedAt) : undefined}
      note={running ? <Tag>Keeps running if you leave</Tag> : undefined}
      lines={shown}
      // The buffer is bounded at both ends, so a very long run is truncated
      // and says so rather than quietly showing a middle.
      dropped={job.lines - lines.length}
      error={job.error}
      onCancel={onCancel && (!job.capability || can(job.capability)) ? onCancel : undefined}
      onDismiss={onDismiss}
      className={className}
    />
  )
}

function JobIcon({ status }: { status: Job["status"] }) {
  if (status === "running") return <Spinner className="size-3.5 text-muted-foreground" />
  if (status === "succeeded") return <CheckCircle className="size-3.5 text-success" />
  if (status === "cancelled") return <Slash className="size-3.5 text-muted-foreground" />
  return <CrossCircle className="size-3.5 text-destructive" />
}

/**
 * The operations that have run recently, so one started before lunch can be
 * read after it.
 *
 * A job outliving its console is only useful if there is a way back to it.
 */
export function RecentJobs({
  kinds,
  onOpen,
  className,
}: {
  /** Only these kinds, so the Proxy page does not list package upgrades. */
  kinds: string[]
  onOpen: (id: string) => void
  className?: string
}) {
  const key = kinds.join(",")
  // Keyed, so the page's own reading of the job list and this one are one
  // request: the certificates page ran two five-second loops over `/jobs`.
  // A failed read keeps the last list, as the loop this replaced did.
  const all = usePoll<Job[]>((signal) => get<Job[]>("/jobs/", undefined, signal), 5000, [], {
    key: "/jobs",
  }).data
  const jobs = useMemo(() => {
    const wanted = key.split(",")
    return (all ?? []).filter((j) => wanted.some((k) => j.kind.startsWith(k)))
  }, [all, key])

  if (jobs.length === 0) return null
  const shown = jobs.slice(0, 6)
  const labels = recentLabels(shown)

  return (
    <div className={cn("flex flex-wrap items-center gap-1.5", className)}>
      <span className="eyebrow">recent</span>
      {shown.map((job, i) => (
        <button
          key={job.id}
          onClick={() => onOpen(job.id)}
          title={`${job.title} · ${job.status}${job.startedBy ? ` · started by ${job.startedBy}` : ""}`}
          className={cn(
            "flex max-w-56 items-center gap-1.5 rounded-md border border-hairline bg-control px-2 py-1 text-hint transition-colors hover:bg-control-hover",
            job.status === "failed" ? "text-destructive" : "text-muted-foreground",
          )}
        >
          <JobIcon status={job.status} />
          <span className="truncate">{labels[i]}</span>
        </button>
      ))}
    </div>
  )
}
