"use client"

import { useEffect, useRef } from "react"
import { CheckCircle, CrossCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { relativeTime } from "@/lib/format"
import type { UpdateRun } from "@/lib/types"
import { phaseLabel } from "@/hooks/use-self-update"
import { Well } from "@/components/panel"
import { Notice, Spinner } from "@/components/state"

/**
 * An upgrade, watched rather than waited for — and watched across the moment
 * the thing serving this page is replaced.
 *
 * The transcript is polled rather than streamed, and that is not a shortcut.
 * Every other long command in this dashboard is followed over a WebSocket, but
 * a socket to a backend that is about to be destroyed and rebuilt cannot
 * survive the one event the operator most wants to see. The record is on disk,
 * written by a container that outlives the restart, so a poll picks the story
 * up wherever it left off — including from a backend that did not exist when
 * the upgrade began.
 *
 * `restarting` is the state that has to be shown as progress rather than as a
 * fault: the API being unreachable during an upgrade is the upgrade happening.
 */
export function UpdateProgress({
  run,
  log,
  restarting,
  className,
}: {
  run: UpdateRun
  log?: string
  restarting?: boolean
  className?: string
}) {
  const running = run.status === "running" || run.status === "pending"

  return (
    <div className={cn("min-w-0 space-y-3", className)}>
      <div className="flex min-w-0 items-start gap-2.5">
        <span className="pt-0.5">
          {running && <Spinner className="size-4 text-primary" />}
          {run.status === "success" && <CheckCircle className="size-4 text-success" />}
          {run.status === "failed" && <CrossCircle className="size-4 text-destructive" />}
        </span>
        <div className="min-w-0 flex-1 space-y-0.5">
          <p className="text-body leading-tight font-medium">
            {running
              ? restarting
                ? "Restarting the dashboard"
                : phaseLabel(run)
              : run.status === "success"
                ? `Updated to ${run.toVersion}`
                : `Update to ${run.toVersion} failed`}
          </p>
          <p className="text-hint text-muted-foreground">
            {run.fromVersion} → {run.toVersion} · started {relativeTime(run.startedAt)}
            {run.actor ? ` by ${run.actor}` : ""}
          </p>
        </div>
      </div>

      {running && (
        <Notice title="This takes a few minutes, and the dashboard restarts itself">
          Every container in the stack is rebuilt, this one included, so the page will lose contact
          with the server for a moment and pick back up on its own.{" "}
          <b className="text-foreground">Do not reload</b> — the tab already has everything it needs
          to keep watching, and a reload during the restart is the one thing that cannot recover
          itself.
        </Notice>
      )}

      {run.status === "failed" && run.error && (
        <Notice title="What went wrong" tone="danger">
          <span className="break-words whitespace-pre-wrap">{run.error}</span>
        </Notice>
      )}

      {log && <Transcript text={log} follow={running} />}
    </div>
  )
}

/**
 * The updater's output. Bounded on the server to the last 64 KB, because the
 * part that says what went wrong is the end of it and a build log is otherwise
 * a megabyte on every poll.
 */
function Transcript({ text, follow }: { text: string; follow?: boolean }) {
  const ref = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)

  useEffect(() => {
    const el = ref.current
    if (!el || !follow || !pinned.current) return
    el.scrollTop = el.scrollHeight
  }, [text, follow])

  return (
    <Well
      ref={ref}
      onScroll={() => {
        const el = ref.current
        if (!el) return
        // Following stops the moment the reader scrolls up, or a log that is
        // still growing yanks them back down every two seconds.
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
      }}
      className="max-h-64 min-h-16 text-hint break-all whitespace-pre-wrap"
    >
      {text.trimEnd()}
    </Well>
  )
}
