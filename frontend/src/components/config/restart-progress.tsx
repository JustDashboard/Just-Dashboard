"use client"

import {
  CheckCircle,
  CrossCircle,
  External,
  Information,
  RotateCounterClockwise,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { duration, relativeTime } from "@/lib/format"
import { transcriptLines } from "@/lib/transcript"
import type { DashboardConfigRun } from "@/lib/types"
import { configPhaseLabel } from "@/hooks/use-self-config"
import { Disclosure } from "@/components/form"
import { RunPhases, phaseStates, type RunPhase } from "@/components/run-phases"
import { RunTranscript } from "@/components/run-transcript"
import { Spinner } from "@/components/state"
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
 *
 * It used to say all of that in tinted banners — an amber one while it ran, a
 * green one when the address moved, amber again for a caveat and red for the
 * error — four boxes stacked over a 256px well, none of which asked the reader
 * to decide anything (§14). A state is now a mark beside the headline, what the
 * reader should know is one line under it, where the run is is a bar of its
 * stages, and why it failed is the line in the transcript that failed, washed
 * where it sits.
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
  const took =
    run.finishedAt &&
    duration((new Date(run.finishedAt).getTime() - new Date(run.startedAt).getTime()) / 1000)

  return (
    <div className={cn("min-w-0 space-y-4", className)}>
      <div className="flex min-w-0 items-start gap-3">
        <span className="pt-0.5">
          {running && <Spinner className="size-4 text-primary" />}
          {run.status === "success" && <CheckCircle className="size-4 text-success" />}
          {run.status === "failed" && <CrossCircle className="size-4 text-destructive" />}
          {run.status === "rolled_back" && (
            <RotateCounterClockwise className="size-4 text-warning" />
          )}
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <p className="text-sm leading-tight font-semibold tracking-tight">
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
            {took ? ` · took ${took}` : ""}
          </p>
        </div>
        {!running && onDismiss && (
          <Button variant="ghost" size="sm" onClick={onDismiss}>
            Dismiss
          </Button>
        )}
      </div>

      <Outcome run={run} running={running} moved={Boolean(moved)} />

      {run.changes && run.changes.length > 0 && (
        <ul className="flex min-w-0 flex-wrap gap-x-5 gap-y-1 text-xs">
          {run.changes.map((change) => (
            <li key={change.key} className="flex min-w-0 items-baseline gap-1.5">
              <span className="text-muted-foreground">{change.label}</span>
              <span className="truncate font-mono text-hint text-muted-foreground line-through">
                {change.from || "—"}
              </span>
              <span className="text-muted-foreground">→</span>
              <span className="truncate font-mono text-hint">{change.to || "—"}</span>
            </li>
          ))}
        </ul>
      )}

      <RunPhases phases={phases(run, log)} />

      {/* Open while the run is live, because that is when it is read; folded
          once there is an outcome above it — unless the outcome is a failure,
          where the transcript is the answer to the only question left. */}
      {log &&
        (running || run.status === "failed" ? (
          <RunTranscript
            text={log}
            live={running}
            source="/dashboard/config/log"
            label={transcriptLabel(run)}
          />
        ) : (
          <Disclosure quiet summary="Transcript" facts="the whole run, line by line">
            <RunTranscript
              text={log}
              live={false}
              source="/dashboard/config/log"
              label={transcriptLabel(run)}
            />
          </Disclosure>
        ))}
    </div>
  )
}

/** What the reader should know about this run, in a line rather than a banner. */
function Outcome({
  run,
  running,
  moved,
}: {
  run: DashboardConfigRun
  running: boolean
  moved: boolean
}) {
  if (running) {
    return (
      <Line>
        {moved ? (
          <>
            It comes back at <code className="font-mono text-foreground">{run.endpoint}</code> —
            open that when this finishes rather than reloading here.
          </>
        ) : (
          <>
            Keep this tab open: it follows the stack across the restart, its own server included,
            and a reload is the one thing that cannot recover itself.
          </>
        )}
      </Line>
    )
  }
  if (run.status === "success" && moved && run.endpoint) {
    return (
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
        <Line>
          Now answering at <code className="font-mono text-foreground">{run.endpoint}</code>; this
          tab is still on the old address.
        </Line>
        <Button size="sm" asChild>
          <a href={run.endpoint}>
            <External className="size-3.5" />
            Open the new address
          </a>
        </Button>
      </div>
    )
  }
  if (run.status === "rolled_back") {
    return (
      <div className="space-y-1">
        <Line>
          The new configuration did not answer, so the previous one was put back. Nothing about how
          you reach the dashboard has changed.
        </Line>
        {run.error && <Detail tone="warning">{run.error}</Detail>}
      </div>
    )
  }
  if (run.status === "failed" && run.error) return <Detail tone="danger">{run.error}</Detail>
  // A caveat on a run that otherwise worked — today, a Tailscale certificate
  // that could not be issued, so the address moved but the padlock has not
  // arrived yet.
  if (run.note) return <Detail tone="warning">{run.note}</Detail>
  return null
}

function Line({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex min-w-0 items-start gap-2 text-xs leading-relaxed text-muted-foreground">
      <Information aria-hidden className="mt-0.5 size-3.5 shrink-0" />
      <span className="min-w-0">{children}</span>
    </p>
  )
}

/** The runner's own sentence about an outcome, coloured by it — text, not a box. */
function Detail({ tone, children }: { tone: "warning" | "danger"; children: React.ReactNode }) {
  return (
    <p
      className={cn(
        "border-l-2 pl-3 font-mono text-hint leading-relaxed break-words whitespace-pre-wrap",
        tone === "danger" ? "border-destructive text-destructive" : "border-warning text-warning",
      )}
    >
      {children}
    </p>
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

function transcriptLabel(run: DashboardConfigRun) {
  switch (run.action) {
    case "rebuild":
      return "Rebuild transcript"
    case "apply":
      return "Apply transcript"
    default:
      return "Restart transcript"
  }
}

/**
 * The run's stages. The record says only which phase it is in, and a rebuild
 * spends one phase on two jobs — compiling the images, then recreating the
 * containers — so the transcript says which: the `compose up` command is the
 * line between them. A finished run that failed failed at the last stage its
 * transcript reached.
 */
function phases(run: DashboardConfigRun, log: string | undefined) {
  const labels = [
    ...(run.action === "rebuild" ? ["Build the images"] : []),
    ...(run.action === "apply" ? ["Write the settings"] : []),
    "Recreate the containers",
    "Wait for an answer",
    ...(run.status === "rolled_back" || run.phase === "rollback" ? ["Put the previous back"] : []),
  ]
  const lines = log ? transcriptLines(log) : []
  const upAt = lines.some((line) => line.kind === "command" && / up\b/.test(line.text))
  const waited = lines.some((line) => line.text.startsWith("waiting for the dashboard to answer"))
  const recreate = labels.indexOf("Recreate the containers")
  // Where the applying phase is: an apply has written its settings before the
  // runner starts, a rebuild is compiling until compose is asked to come up.
  const applying = run.action === "rebuild" && !upAt ? 0 : recreate
  const last = labels.length - 1
  // The stage the change was undone after reads as a warning, not a failure:
  // the safety net did what it is for.
  const undone = (states: RunPhase[]) =>
    states.map((phase, index) =>
      index === last - 1 ? { ...phase, state: "warning" as const } : phase,
    )

  switch (run.status) {
    case "success":
      return phaseStates(labels, labels.length, "success")
    case "rolled_back":
      return undone(phaseStates(labels, labels.length, "success"))
    case "failed":
      return phaseStates(labels, waited ? recreate + 1 : applying, "failed")
  }
  switch (run.phase) {
    case "queued":
      return phaseStates(labels, 0, "running")
    case "waiting":
      return phaseStates(labels, recreate + 1, "running")
    case "rollback":
      return undone(phaseStates(labels, last, "running"))
    default:
      return phaseStates(labels, applying, "running")
  }
}
