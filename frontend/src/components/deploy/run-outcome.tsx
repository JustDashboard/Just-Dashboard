"use client"

import Link from "next/link"
import { CheckCircle, Globe, LockClosed, Logs, SettingsSliders } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  DeploymentEngineRun,
  DeploymentFailureCause,
  DeploymentRelease,
  DeploymentStep,
  DeploymentSummary,
} from "@/lib/types"
import { StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { TranscriptRow } from "@/components/transcript-line"
import { Button } from "@/components/ui/button"
import { formatDuration, stepName, stepSeconds } from "@/components/deploy/vocabulary"
import { causeHeadline, causeTitle } from "@/components/deploy/failure-cause"
import type { ConsoleRow } from "@/components/deploy/build-console"
import { StepTile } from "@/components/deploy/run-evidence"

/** How many of the failing step's last lines stand under the failure. */
const TAIL = 4

/**
 * How a run that failed or rolled back ended, said once and in the order it
 * is read: what broke, drawn as the step it broke in on that step's tile;
 * where in the run, how far it got and when; the engine's reason in its own
 * words; the last lines the step wrote, so "nothing is clear" is answered on
 * the page rather than three presses away; and what to do — the fix where the
 * evidence names one, the line in the build logs, the step's whole record.
 *
 * It was a banner of a title, a sentence and one button, and the sentence was
 * the engine's message at the size of a hint. A failure is the one thing on
 * the page that needs a decision, so it keeps the danger wash (§14) — amber
 * for a rollback, which left a release serving — and spends it on the parts
 * that say what happened.
 */
export function RunFailure({
  run,
  step,
  steps,
  cause,
  kept,
  rows,
  deployment,
  now,
  changedSince,
  fix,
  fixIsCommand,
  onShowOutput,
  onShowStep,
}: {
  run: DeploymentEngineRun
  /** The step the run stopped on, when one failed. */
  step?: DeploymentStep
  /** Every step, for how far the run got. */
  steps: DeploymentStep[]
  cause?: DeploymentFailureCause
  /** The release that stayed live when the run rolled back. */
  kept?: DeploymentRelease
  rows: ConsoleRow[]
  deployment?: DeploymentSummary
  now: number
  /** What changed in the settings since the run, when they did. */
  changedSince?: string
  fix?: { href: string; label: string }
  /** The fix is the way forward rather than a place to check. */
  fixIsCommand: boolean
  /** The build console on the failing step — at the cause's line where it names one. */
  onShowOutput: () => void
  onShowStep: () => void
}) {
  const rolledBack = run.state === "rolled_back"
  const headline = rolledBack
    ? `Rolled back — ${kept ? `release #${kept.number}` : "the previous release"} stayed live`
    : cause
      ? causeHeadline(cause)
      : causeTitle(run.terminalCode || "deployment_failed")
  const tail = step ? rows.filter((row) => row.stepId === step.id).slice(-TAIL) : []
  const seconds = step ? stepSeconds(step, now) : undefined
  const reached = step ? steps.findIndex((candidate) => candidate.id === step.id) + 1 : 0
  return (
    <section
      aria-label={rolledBack ? "Why this deployment rolled back" : "Why this deployment failed"}
      className={cn(
        "min-w-0 animate-rise overflow-hidden rounded-lg border",
        rolledBack ? "border-rule-warning bg-wash-warning" : "border-rule-danger bg-wash-danger",
      )}
    >
      <div className="flex min-w-0 flex-col gap-4 p-4 sm:flex-row sm:items-start">
        <div className="flex min-w-0 flex-1 gap-3.5">
          {step ? (
            <StepTile step={step} state={step.state} deployment={deployment} size="md" />
          ) : (
            <span className="flex size-10 shrink-0 items-center justify-center">
              <StatusDot tone={rolledBack ? "warning" : "danger"} />
            </span>
          )}
          <div className="min-w-0 space-y-1.5">
            <p className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
              <span className="text-title leading-tight font-semibold tracking-tight">
                {headline}
              </span>
              {run.terminalCode && <Tag mono>{run.terminalCode}</Tag>}
            </p>
            {step && (
              <p className="numeric text-xs text-muted-foreground">
                {rolledBack ? "Stopped at " : "Failed at "}
                <span className="font-medium text-foreground">{stepName(step.key)}</span>
                {` · step ${reached} of ${steps.length}`}
                {seconds !== undefined && ` · after ${formatDuration(seconds)}`}
                {run.endedAt && ` · ${relativeTime(run.endedAt)}`}
              </p>
            )}
            {cause?.subjects && cause.subjects.length > 0 && (
              <p className="flex flex-wrap gap-1.5 pt-0.5" aria-label="Named by the failure">
                {cause.subjects.map((subject) => (
                  <Tag key={subject} mono>
                    {subject}
                  </Tag>
                ))}
              </p>
            )}
            {run.terminalReason && (
              <p className="pt-0.5 text-body leading-relaxed text-foreground/90">
                {run.terminalReason}
              </p>
            )}
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2 max-sm:pl-13.5">
          {fix && (
            <Button size="sm" variant={fixIsCommand ? "default" : "outline"} asChild>
              <Link href={fix.href}>{fix.label}</Link>
            </Button>
          )}
          {step && (
            <Button variant="outline" size="sm" onClick={onShowOutput}>
              <Logs className="size-3.5" />
              {cause?.lineSeq ? "Show the line" : "Show in build logs"}
            </Button>
          )}
          {step && (
            <Button variant="ghost" size="sm" onClick={onShowStep}>
              Step details
            </Button>
          )}
        </div>
      </div>
      {(tail.length > 0 || changedSince) && (
        <div className="space-y-3 px-4 pb-4">
          {tail.length > 0 && (
            <ol
              aria-label={`Last lines from ${step ? stepName(step.key) : "the run"}`}
              className="overflow-hidden rounded-md border border-hairline bg-surface-sunken py-1.5 font-mono text-xs leading-6"
            >
              {tail.map((row) => (
                <TranscriptRow
                  key={row.id}
                  line={row.line}
                  wrap
                  needle=""
                  tokens
                  marked={cause?.lineSeq !== undefined && row.id.startsWith(`${cause.lineSeq}:`)}
                />
              ))}
            </ol>
          )}
          {changedSince && (
            <p className="flex min-w-0 items-start gap-2 text-xs text-muted-foreground">
              <SettingsSliders aria-hidden className="mt-px size-3.5 shrink-0" />
              <span>{changedSince}</span>
            </p>
          )}
        </div>
      )}
    </section>
  )
}

/**
 * How a run that succeeded stands now: the release it made is live, at its
 * address, since when — or another has replaced it, and which.
 */
export function RunReady({
  live,
  url,
  release,
  liveRelease,
  projectId,
}: {
  live: boolean
  url?: string
  release?: DeploymentRelease
  liveRelease?: DeploymentRelease
  projectId: number
}) {
  return (
    <section
      aria-label="Outcome"
      className="flex min-w-0 animate-rise flex-wrap items-center justify-between gap-4"
    >
      <div className="flex min-w-0 items-center gap-3.5">
        <span
          aria-hidden
          className={cn(
            "flex size-10 shrink-0 items-center justify-center rounded-lg border",
            live ? "border-rule-success bg-wash-success" : "border-hairline",
          )}
        >
          {live ? <CheckCircle className="size-5 text-success" /> : <StatusDot tone="stopped" />}
        </span>
        <div className="min-w-0 space-y-1">
          <p className="text-title leading-tight font-semibold tracking-tight">
            {live
              ? "Your release is ready"
              : `Superseded — release #${liveRelease?.number ?? "—"} is live now`}
          </p>
          {live && (
            <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
              {url ? (
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-sm font-mono text-foreground/85 focus-ring hover:text-foreground hover:underline"
                >
                  {url.startsWith("https:") ? (
                    <LockClosed aria-hidden className="size-3.5 shrink-0 text-success" />
                  ) : (
                    <Globe aria-hidden className="size-3.5 shrink-0" />
                  )}
                  <span className="truncate">{url}</span>
                </a>
              ) : (
                <span className="font-mono">Private service on your server</span>
              )}
              {release && <span className="numeric">Release #{release.number}</span>}
              {release?.activatedAt && <span>live since {relativeTime(release.activatedAt)}</span>}
            </p>
          )}
        </div>
      </div>
      {!live && (
        <Button variant="outline" size="sm" asChild className="max-sm:w-full">
          <Link href={`/deploy/${projectId}`}>Open project</Link>
        </Button>
      )}
    </section>
  )
}
