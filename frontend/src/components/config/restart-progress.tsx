"use client"

import { useEffect, useRef } from "react"
import {
  CheckCircle,
  ChevronDown,
  CrossCircle,
  External,
  RotateCounterClockwise,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { relativeTime } from "@/lib/format"
import type { DashboardConfigRun } from "@/lib/types"
import { configPhaseLabel } from "@/hooks/use-self-config"
import { Well } from "@/components/panel"
import { Notice, Spinner } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * A restart, watched rather than waited for — and watched across the moment
 * the thing serving this page is replaced.
 *
 * Polled rather than streamed, for the reason the update panel is: a socket to
 * a backend that is about to be destroyed and recreated cannot survive the one
 * event the operator most wants to see. The record is on disk, written by a
 * container that outlives the restart.
 *
 * The state this component exists for is the fourth one. A restart can succeed,
 * fail, or be *rolled back* — the new configuration did not come up, the
 * previous one was put back, and the dashboard is running exactly as it was.
 * That is a failure of the change and a success of the safety net, and showing
 * it as either alone would misreport what happened.
 */
export function RestartProgress({
  run,
  log,
  restarting,
  onDismiss,
  className,
}: {
  run: DashboardConfigRun
  log?: string
  restarting?: boolean
  onDismiss?: () => void
  className?: string
}) {
  const running = run.status === "running" || run.status === "pending"
  const moved = run.endpoint && run.changes?.some((c) => c.key === "JD_PORT" || c.key === "JD_SITE")

  return (
    <div className={cn("min-w-0 space-y-3", className)}>
      <div className="flex min-w-0 items-start gap-2.5">
        <span className="pt-0.5">
          {running && <Spinner className="size-4 text-primary" />}
          {run.status === "success" && <CheckCircle className="size-4 text-success" />}
          {run.status === "failed" && <CrossCircle className="size-4 text-destructive" />}
          {run.status === "rolled_back" && (
            <RotateCounterClockwise className="size-4 text-warning" />
          )}
        </span>
        <div className="min-w-0 flex-1 space-y-0.5">
          <p className="text-body leading-tight font-medium">
            {running
              ? restarting
                ? "Restarting the dashboard"
                : configPhaseLabel(run)
              : run.status === "success"
                ? headline(run)
                : run.status === "rolled_back"
                  ? "Change undone — the dashboard is as it was"
                  : `${headline(run)} failed`}
          </p>
          <p className="text-hint text-muted-foreground">
            started {relativeTime(run.startedAt)}
            {run.actor ? ` by ${run.actor}` : ""}
            {run.finishedAt ? ` · finished ${relativeTime(run.finishedAt)}` : ""}
          </p>
        </div>
        {!running && onDismiss && (
          <Button variant="ghost" size="sm" onClick={onDismiss}>
            Dismiss
          </Button>
        )}
      </div>

      {run.changes && run.changes.length > 0 && (
        <Well plain>
          <ul className="space-y-1 text-xs">
            {run.changes.map((change) => (
              <li key={change.key} className="flex flex-wrap items-baseline gap-x-2">
                <span className="font-medium">{change.label}</span>
                <span className="font-mono text-hint text-muted-foreground line-through">
                  {change.from || "—"}
                </span>
                <span className="text-muted-foreground">→</span>
                <span className="font-mono text-hint">{change.to || "—"}</span>
              </li>
            ))}
          </ul>
        </Well>
      )}

      {/* One banner while it runs, not three. The only thing the reader has to
          do is not reload, and the only thing they have to know is where it
          comes back — so that is all this says. */}
      {running && (
        <Notice title="Do not reload this page" tone="warning">
          {moved ? (
            <>
              The stack is being recreated. It comes back at{" "}
              <code className="font-mono">{run.endpoint}</code>, so open that address when this
              finishes rather than reloading here.
            </>
          ) : (
            <>
              The stack is being recreated, this tab&rsquo;s server included. It already has
              everything it needs to keep watching; a reload is the one thing that cannot recover
              itself.
            </>
          )}
        </Notice>
      )}

      {run.status === "success" && moved && run.endpoint && (
        <Notice title="The dashboard has moved" tone="success">
          <p>
            It now answers at <code className="font-mono">{run.endpoint}</code>. This tab is still
            on the old address.
          </p>
          <Button className="mt-2" size="sm" variant="outline" asChild>
            <a href={run.endpoint}>
              <External className="size-3.5" />
              Open the new address
            </a>
          </Button>
        </Notice>
      )}

      {/* A caveat on a run that otherwise worked — today, a Tailscale
          certificate that could not be issued, so the address moved but the
          padlock has not arrived yet. */}
      {run.note && run.status !== "rolled_back" && (
        <Notice title="Worth knowing" tone="warning">
          <span className="break-words whitespace-pre-wrap">{run.note}</span>
        </Notice>
      )}

      {run.status === "rolled_back" && (
        <Notice title="The change was undone" tone="warning">
          <p className="mb-1">
            The new configuration did not answer, so the previous one was put back. Nothing about
            how you reach the dashboard has changed.
          </p>
          {run.error && <span className="break-words whitespace-pre-wrap">{run.error}</span>}
        </Notice>
      )}

      {run.status === "failed" && run.error && (
        <Notice title="What went wrong" tone="danger">
          <span className="break-words whitespace-pre-wrap">{run.error}</span>
        </Notice>
      )}

      {/* The transcript is the answer to "why did that fail", which is a
          question nobody has while it is working. It stays open while the run
          is live and folds itself away once there is an outcome to read
          instead. */}
      {log &&
        (running ? (
          <Transcript text={log} follow />
        ) : (
          <details className="group min-w-0">
            <summary className="inline-flex cursor-pointer list-none items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground">
              <ChevronDown className="size-3.5 transition-transform group-open:rotate-180" />
              Transcript
            </summary>
            <div className="pt-2">
              <Transcript text={log} />
            </div>
          </details>
        ))}
    </div>
  )
}

function headline(run: DashboardConfigRun) {
  switch (run.action) {
    case "rebuild":
      return "Rebuilt and restarted"
    case "apply":
      return "New settings applied"
    default:
      return "Restarted"
  }
}

/**
 * The sibling's output. Bounded on the server to the last 64 KB, because the
 * part that says what went wrong is the end of it.
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
