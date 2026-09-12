"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { CheckCircle, CrossCircle, Slash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Job, JobLine } from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/state"
import { Pane, PaneHeader } from "@/components/panel"

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
  const scrollRef = useRef<HTMLDivElement>(null)
  const [following, setFollowing] = useState(true)

  useEffect(() => {
    if (!following) return
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [lines, following])

  if (!job) return null
  const running = job.status === "running"
  // The buffer is bounded at both ends, so a very long run is truncated and
  // says so rather than quietly showing a middle.
  const dropped = job.lines - lines.length

  return (
    <Pane className={cn("bg-surface-sunken", className)}>
      <PaneHeader className="flex-wrap gap-2 px-3">
        <JobIcon status={job.status} />
        <span className="min-w-0 flex-1 truncate text-xs font-medium">
          {job.title}
          {!running && (
            <span className="ml-2 font-normal text-muted-foreground">
              {job.status} {job.endedAt && relativeTime(job.endedAt)}
            </span>
          )}
        </span>
        {running && onCancel && (
          <Button size="xs" variant="ghost" className="text-destructive" onClick={onCancel}>
            <Slash className="size-3" />
            Stop
          </Button>
        )}
        {!running && onDismiss && (
          <Button size="xs" variant="ghost" onClick={onDismiss}>
            Dismiss
          </Button>
        )}
      </PaneHeader>

      {job.error && (
        <p className="border-b border-hairline bg-wash-danger px-3 py-1.5 text-hint text-destructive">
          {job.error}
        </p>
      )}
      {running && (
        <p className="border-b border-hairline px-3 py-1.5 text-hint text-muted-foreground">
          This keeps running if you close the page — reopen it from the recent list.
        </p>
      )}

      <div
        ref={scrollRef}
        onScroll={() => {
          const el = scrollRef.current
          if (!el) return
          setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
        }}
        className="max-h-80 min-h-24 overflow-auto p-2.5 font-mono text-hint leading-relaxed"
      >
        {dropped > 0 && (
          <div className="mb-1.5 text-muted-foreground">
            … {dropped} earlier {dropped === 1 ? "line" : "lines"} dropped …
          </div>
        )}
        {lines.length === 0 ? (
          <p className="text-muted-foreground">{running ? "Starting…" : "No output."}</p>
        ) : (
          lines.map((line) => (
            <div
              key={line.seq}
              className={cn(
                "break-all whitespace-pre-wrap",
                // A status line is the runner's own voice rather than the
                // command's, so it reads as a heading in the stream.
                line.stream === "status" && "mt-1.5 font-medium text-primary",
                line.stream === "stderr" && "text-warning",
              )}
            >
              {line.text}
            </div>
          ))
        )}
      </div>
    </Pane>
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
}: {
  /** Only these kinds, so the Proxy page does not list package upgrades. */
  kinds: string[]
  onOpen: (id: string) => void
}) {
  const [jobs, setJobs] = useState<Job[]>([])
  const key = kinds.join(",")

  useEffect(() => {
    let cancelled = false
    const wanted = key.split(",")
    const load = () =>
      get<Job[]>("/jobs/")
        .then(
          (all) =>
            !cancelled && setJobs(all.filter((j) => wanted.some((k) => j.kind.startsWith(k)))),
        )
        .catch(() => {})
    load()
    const timer = setInterval(load, 5000)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [key])

  if (jobs.length === 0) return null

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="eyebrow">recent</span>
      {jobs.slice(0, 6).map((job) => (
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
          {/* The target makes the better label when it is a domain or a
              package set: it is short, and it is the thing that differs
              between runs. A file path is neither — every SSH apply writes
              the same drop-in, so a row of chips all read as the same
              truncated path and the list stops being a way to find a run. */}
          <span className="truncate">
            {job.target && !job.target.startsWith("/") ? job.target : job.title}
          </span>
        </button>
      ))}
    </div>
  )
}
