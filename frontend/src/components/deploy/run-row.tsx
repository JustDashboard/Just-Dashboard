"use client"

import { relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DeploymentEngineRun, DeploymentRelease, DeploymentSummary } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ChoiceRow } from "@/components/flow"
import { Meter } from "@/components/meter"
import { FactDot } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { ProductGlyph, imageProduct } from "@/components/product-logo"
import { AuthorMark, BranchChip, ShortSha } from "@/components/git/marks"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { RunActorMark } from "@/components/deploy/run-marks"
import { MiniReleasePath } from "@/components/deploy/run-pipeline"
import {
  RunStatus,
  formatDuration,
  isActiveRun,
  runCommit,
  runDurationSeconds,
  runFailed,
  runRef,
  runRevision,
  runSubject,
  runTitle,
  runTriggerLine,
  shortRevision,
  terminalLabel,
  useNow,
} from "@/components/deploy/vocabulary"

type RunSource = Pick<
  DeploymentSummary,
  "sourceKind" | "sourceRef" | "sourceRepository" | "sourceRemote"
>

/**
 * One run, as a thing you open.
 *
 * The Overview's recent deployments, the Deployments list and the wiring's
 * history each drew a run their own way — a status dot in front of "#4
 * Deploy", a grey line of `main · sha · by operator · 3h ago` — and every one
 * of them was a row with an `href` drawn as a row you read (§15 pass 3). This
 * is the one shape: who or what started it as their face or product, the run's
 * name with its commit's subject, where it came from, and how it went, with the
 * lit edge of a destination and a light running round it while it is in
 * flight.
 *
 * The readings at the right sit in fixed measures, so a column of runs is a
 * column: the state, how long it took (ticking while it goes), and when. On a
 * phone they go under the name instead — chosen once with a media query, as
 * the backup job card does, so each reading is in the document once.
 *
 * A run in flight carries the stage it is at, in the run page's own bar with
 * the stage at work lit, so it reads the same wherever the run is listed.
 * `tags` are the run's standing beside those readings — the live release, a
 * pinned one, a preview's pull request — and `actions` its verbs, laid out in
 * the row's own slot (`VerbActions dim`).
 *
 * `release` is the release the run made, whose commit names a run that did
 * not record its own; `live` says that release is the one serving now, which
 * the state column reads in place of "Ready" — one state, not two dots.
 * `meter` draws the run's time against the others in the list under its
 * duration, so a column of runs is also a picture of how long they take and
 * the one that took twice as long is seen, not read.
 */
export function RunRow({
  run,
  deployment,
  release,
  index,
  tags,
  actions,
  meter,
  live,
}: {
  run: DeploymentEngineRun
  /** Where the project's source lives, for the provenance line and a push's host. */
  deployment: RunSource
  release?: Pick<DeploymentRelease, "sourceRevision">
  /** Position in the list, for the arrival stagger. */
  index?: number
  tags?: React.ReactNode
  actions?: React.ReactNode
  /** 0–100 of the list's longest run; `slow` when it took far longer than most. */
  meter?: { share: number; slow?: boolean }
  /** The release this run made is the live one. */
  live?: boolean
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const active = isActiveRun(run.state)
  const now = useNow(1000, active)
  const subject = runSubject(run)
  // Under a status that already says it failed, the code names only where.
  const failure =
    runFailed(run.state) && run.terminalCode ? terminalLabel(run.terminalCode) : undefined
  const state = live ? <Status tone="running" label="Live" /> : <RunStatus state={run.state} />
  const stage = active && run.currentStep && (
    <span className="flex items-center gap-2">
      <MiniReleasePath currentStep={run.currentStep.key} currentStatus={run.currentStep.state} />
      <TextShimmer className="text-hint">{run.currentStep.label}</TextShimmer>
    </span>
  )
  const duration = formatDuration(runDurationSeconds(run, now))
  const when = (
    <time dateTime={run.requestedAt} title={timestamp(run.requestedAt)}>
      {relativeTime(run.requestedAt)}
    </time>
  )

  return (
    <ChoiceRow
      verb={`Open ${runTitle(run)}`}
      href={`/deploy/${run.projectId}/runs/${run.id}`}
      busy={active}
      index={index}
      leading={<RunActorMark run={run} remote={deployment.sourceRemote} />}
      title={
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="shrink-0">{runTitle(run)}</span>
          {subject && (
            <span className="min-w-0 truncate font-normal text-muted-foreground">{subject}</span>
          )}
        </span>
      }
      description={<Provenance run={run} deployment={deployment} release={release} wide={wide} />}
      trailing={
        wide ? (
          <>
            {stage}
            {tags}
            <span className="flex w-28 min-w-0 flex-col">
              {state}
              {failure && <span className="truncate text-hint text-destructive">{failure}</span>}
            </span>
            <span className="flex w-16 flex-col items-end gap-1">
              <span
                className={cn(
                  "numeric text-hint text-muted-foreground",
                  meter?.slow && "text-warning",
                )}
              >
                {duration}
              </span>
              {meter && (
                <Meter
                  value={meter.share}
                  tone={meter.slow ? "warning" : "default"}
                  size="thin"
                  className="w-12"
                  label="Time against the longest run listed"
                />
              )}
            </span>
            <span className="numeric w-20 truncate text-right text-hint text-muted-foreground">
              {when}
            </span>
          </>
        ) : undefined
      }
      actions={actions}
    >
      {wide ? undefined : (
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-hint text-muted-foreground">
          {state}
          {failure && <span className="text-destructive">{failure}</span>}
          <FactDot />
          <span className="numeric">{duration}</span>
          <FactDot />
          <span className="numeric">{when}</span>
          {stage}
          {tags}
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * Where the run's source came from, in the order a forge writes a commit
 * line: the branch, the commit, its author, then who asked for the run. An
 * image or a template has no branch, and is drawn as its product with the
 * digest it resolved to.
 */
function Provenance({
  run,
  deployment,
  release,
  wide,
}: {
  run: DeploymentEngineRun
  deployment: RunSource
  release?: Pick<DeploymentRelease, "sourceRevision">
  wide: boolean
}) {
  const commit = runCommit(run)
  // Who wrote the commit, unless it is who ran it: one person is one mark in
  // one hue, and the row already leads with theirs and ends "by mira".
  const author =
    commit?.author && commit.author.toLowerCase() !== run.actor?.toLowerCase()
      ? commit.author
      : undefined
  const reference = deployment.sourceRepository || deployment.sourceRef || ""
  const revision = shortRevision(runRevision(run, release))
  const trigger = <span className="truncate">{runTriggerLine(run)}</span>
  switch (deployment.sourceKind) {
    case "git":
    case "local":
      return (
        <span className="flex min-w-0 items-center gap-1.5">
          <BranchChip
            branch={runRef(run, deployment.sourceRef)}
            className="max-w-28 shrink-0 sm:max-w-40"
          />
          <ShortSha sha={shortRevision(runRevision(run, release))} />
          {/* The author's square stays on a phone, as on the run page; only
              the name goes. */}
          {author && (
            <span className="flex min-w-0 shrink items-center gap-1">
              <AuthorMark name={author} />
              {wide && <span className="truncate">{author}</span>}
            </span>
          )}
          {author && <FactDot />}
          {trigger}
        </span>
      )
    case "image":
    case "compose":
    case "blueprint":
      return (
        <span className="flex min-w-0 items-center gap-1.5">
          <ProductGlyph
            id={
              deployment.sourceKind === "image"
                ? imageProduct(reference)
                : deployment.sourceKind === "compose"
                  ? "docker-compose"
                  : reference.split("@")[0]
            }
          />
          {revision && <span className="shrink-0 font-mono">{revision}</span>}
          {trigger}
        </span>
      )
    default:
      return trigger
  }
}
