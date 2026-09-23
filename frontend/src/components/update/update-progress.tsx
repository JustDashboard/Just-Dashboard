"use client"

import { CheckCircle, CrossCircle, Information } from "@/components/icons"
import { cn } from "@/lib/utils"
import { duration, relativeTime } from "@/lib/format"
import { transcriptLines } from "@/lib/transcript"
import type { UpdateRun } from "@/lib/types"
import { phaseLabel } from "@/hooks/use-self-update"
import { RunPhases, phaseStates } from "@/components/run-phases"
import { RunTranscript } from "@/components/run-transcript"
import { Spinner } from "@/components/state"

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
 *
 * Drawn as a restart on Configuration is (`RestartProgress`): a mark and a
 * headline, one line of what to know, a bar of the stages, and the transcript
 * in a console — no banner, because nothing here asks the reader to decide.
 */
export function UpdateProgress({
  run,
  log,
  restarting,
  transcriptClassName,
  className,
}: {
  run: UpdateRun
  log?: string
  restarting?: boolean
  /** The console's height: the side sheet has less room than the page. */
  transcriptClassName?: string
  className?: string
}) {
  const running = run.status === "running" || run.status === "pending"
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
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <p className="text-sm leading-tight font-semibold tracking-tight">
            {running
              ? restarting
                ? "Restarting the dashboard"
                : phaseLabel(run)
              : run.status === "success"
                ? `Updated to ${run.toVersion}`
                : `Update to ${run.toVersion} failed`}
          </p>
          <p className="text-hint text-muted-foreground">
            <span className="numeric">
              {run.fromVersion} → {run.toVersion}
            </span>{" "}
            · started {relativeTime(run.startedAt)}
            {run.actor ? ` by ${run.actor}` : ""}
            {took ? ` · took ${took}` : ""}
          </p>
        </div>
      </div>

      {running && (
        <p className="flex min-w-0 items-start gap-2 text-xs leading-relaxed text-muted-foreground">
          <Information aria-hidden className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0">
            Every container is rebuilt, this one included, so the page loses the server for a moment
            and picks back up on its own. Keep this tab open — a reload during the restart is the
            one thing that cannot recover itself.
          </span>
        </p>
      )}

      {run.status === "failed" && run.error && (
        <p className="border-l-2 border-destructive pl-3 font-mono text-hint leading-relaxed break-words whitespace-pre-wrap text-destructive">
          {run.error}
        </p>
      )}

      <RunPhases phases={phases(run, log)} />

      {log && (
        <RunTranscript
          text={log}
          live={running}
          source="/dashboard/update/log"
          label="Update transcript"
          className={transcriptClassName}
        />
      )}
    </div>
  )
}

/**
 * The upgrade's stages, from the phase the record is in. A failed run's
 * record says only `finished`, so where it stopped is read off the transcript:
 * the first `docker` command is the build starting, and the wait for an answer
 * is the updater's own line.
 */
function phases(run: UpdateRun, log: string | undefined) {
  const labels = ["Fetch the new version", "Build and restart", "Wait for an answer"]
  if (run.status === "success") return phaseStates(labels, labels.length, "success")
  if (run.status === "failed") {
    const lines = log ? transcriptLines(log) : []
    const built = lines.some((line) => line.kind === "command" && line.text.startsWith("$ docker"))
    const waited = lines.some((line) => line.text.startsWith("waiting for the dashboard to answer"))
    return phaseStates(labels, waited ? 2 : built ? 1 : 0, "failed")
  }
  const at = run.phase === "building" ? 1 : run.phase === "restarting" ? 2 : 0
  return phaseStates(labels, at, "running")
}
