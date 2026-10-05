"use client"

import Link from "next/link"
import { Stopwatch } from "@/components/icons"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  DeploymentEngineRun,
  DeploymentRelease,
  DeploymentStep,
  DeploymentSummary,
} from "@/lib/types"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { AuthorMark, BranchChip, ShortSha } from "@/components/git/marks"
import { ProjectMark } from "@/components/deploy/project-mark"
import { RunActorMark } from "@/components/deploy/run-marks"
import {
  RunStatus,
  formatDuration,
  isActiveRun,
  operationLabel,
  runCommit,
  runDurationSeconds,
  runRef,
  runRevision,
  runSubject,
  runTriggerLine,
  shortIdentity,
  shortRevision,
  sourceProduct,
  stepName,
} from "@/components/deploy/vocabulary"

/**
 * The run's header, in the shape every page of its project opens on
 * (`ProjectShell`): one compact block under nothing, its tile, its title and
 * state on one line, the verbs at the far end, and one line of facts under
 * the title — so leaving a project's Deployments for one of them changes what
 * the header says and not how it is drawn.
 *
 * The tile is the project, as on its own pages; the title is the commit the
 * run built, else what it deployed; the state beside it is the run's, with
 * the step at work while it works and how long it has taken or took. The
 * facts are the run's provenance: its number, the branch and commit with the
 * forge they live on, who wrote the commit, what the run did and who or what
 * asked for it, the environment, when, and how long it waited for a slot.
 *
 * It replaced an identity line with a 48px tile, two lines of facts and the
 * duration as a 24px figure at the far end under the verbs — three rows on
 * the right for one reading, and the tallest header in the section.
 */
export function RunHeader({
  run,
  release,
  deployment,
  product,
  branch: configured,
  steps,
  now,
  rollsBackTo,
  projectId,
  verbs,
}: {
  run: DeploymentEngineRun
  /** The release the run made, whose commit names a run that recorded none. */
  release?: DeploymentRelease
  deployment?: DeploymentSummary
  /** What the project is, as its own pages read it. */
  product?: string
  branch?: string
  steps: DeploymentStep[]
  now: number
  /** The release number a rollback run returns to. */
  rollsBackTo?: number
  projectId: number
  /** What can be done to the run, at the line's end. */
  verbs: React.ReactNode
}) {
  const commit = runCommit(run)
  const kind = deployment?.sourceKind
  const git = kind === "git" || kind === "local"
  const branch = runRef(run, deployment?.sourceRef || configured)
  const reference = deployment?.sourceRepository || deployment?.sourceRef || ""
  const revision = runRevision(run, release)
  const title =
    runSubject(run) ??
    (!kind
      ? operationLabel(run.operation)
      : git
        ? deployment?.sourceRepository || branch
        : kind === "compose"
          ? "Compose stack"
          : kind === "import"
            ? "Adopted workload"
            : reference || operationLabel(run.operation))
  const titleIsLiteral = !runSubject(run) && (kind === "image" || kind === "blueprint")
  const active = isActiveRun(run.state)
  const seconds = runDurationSeconds(run, now)
  const claimed = run.claimedAt ? new Date(run.claimedAt).getTime() : undefined
  const queued = new Date(run.queuedAt ?? run.requestedAt).getTime()
  const changedPaths = Array.isArray(run.metadata?.changedPaths)
    ? run.metadata.changedPaths.length
    : 0
  // A run that has not reached a slot, or never did, has only been queued:
  // "took" would name its queue time as a build's.
  const unclaimed = claimed === undefined
  const slot =
    !unclaimed && !Number.isNaN(queued)
      ? `queued ${formatDuration(Math.max(0, (claimed - queued) / 1000))} · ${run.slotClass} slot`
      : active
        ? `waiting for a ${run.slotClass} build slot`
        : "never reached a build slot"
  const working = active ? steps.find((step) => step.state === "running") : undefined
  const forge = deployment && git ? sourceProduct(deployment) : undefined

  return (
    <section
      aria-label={`About deployment #${run.runNumber}`}
      data-slot="run-identity"
      className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-b border-hairline pb-4 sm:gap-y-0.5"
    >
      {deployment ? (
        <ProjectMark deployment={deployment} product={product} className="sm:row-span-2" />
      ) : (
        <span aria-hidden className="size-10 rounded-lg border border-hairline sm:row-span-2" />
      )}
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:contents">
        <p className="flex max-w-full min-w-0 text-title font-semibold tracking-tight max-sm:col-span-2">
          <span className={cn("truncate", titleIsLiteral && "font-mono text-body")}>{title}</span>
        </p>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs max-sm:order-1 max-sm:col-span-3">
          <RunStatus state={run.state} live />
          {working && <TextShimmer className="font-medium">{stepName(working.key)}</TextShimmer>}
          <span className="inline-flex items-center gap-1.5 text-muted-foreground">
            <Stopwatch aria-hidden className="size-3.5" />
            <span>{unclaimed ? "Queued for" : active ? "Running for" : "Took"}</span>
            <span className="numeric font-medium text-foreground">{formatDuration(seconds)}</span>
          </span>
        </div>
      </div>
      {/* On a phone the verbs take a row of their own under the state they
          answer: beside the title, "Deploy with current settings" left the
          title three letters. */}
      <div className="flex shrink-0 flex-wrap items-center gap-2 max-sm:order-2 max-sm:col-span-3 sm:row-span-2 sm:justify-end">
        {verbs}
      </div>
      <div className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground max-sm:order-3 sm:col-span-1 sm:col-start-2">
        <span className="numeric font-medium text-foreground">Deployment #{run.runNumber}</span>
        {git && title !== branch && <BranchChip branch={branch} className="max-w-48" />}
        {git
          ? revision && (
              <span className="inline-flex items-center gap-1.5">
                {hasProductLogo(forge) && <ProductGlyph id={forge} />}
                <ShortSha sha={shortRevision(revision)} />
              </span>
            )
          : kind && revision && <span className="font-mono">{shortIdentity(revision)}</span>}
        {commit?.author && (
          <span className="inline-flex min-w-0 items-center gap-1.5">
            <AuthorMark name={commit.author} />
            <span className="truncate max-sm:hidden">{commit.author}</span>
            {commit.authoredAt && (
              <span className="max-sm:hidden">· {relativeTime(commit.authoredAt)}</span>
            )}
          </span>
        )}
        <span className="inline-flex min-w-0 items-center gap-1.5">
          <RunActorMark run={run} remote={deployment?.sourceRemote} size="xs" />
          <span className="font-medium text-foreground/85">{operationLabel(run.operation)}</span>
          <span>{runTriggerLine(run)}</span>
        </span>
        {deployment && <span>{deployment.environmentName}</span>}
        <time dateTime={run.requestedAt} title={relativeTime(run.requestedAt)}>
          {timestamp(run.requestedAt)}
        </time>
        <span className="numeric">{slot}</span>
        {rollsBackTo !== undefined && (
          <span className="numeric">rolls back to release #{rollsBackTo}</span>
        )}
        {run.retryOfRunId && (
          <Link
            href={`/deploy/${projectId}/runs/${run.retryOfRunId}`}
            className="rounded-sm focus-ring hover:text-foreground hover:underline"
          >
            retry of an earlier run
          </Link>
        )}
        {run.supersededBy && (
          <Link
            href={`/deploy/${projectId}/runs/${run.supersededBy}`}
            className="rounded-sm focus-ring hover:text-foreground hover:underline"
          >
            superseded by a newer run
          </Link>
        )}
        {changedPaths > 0 && (
          <span className="numeric">{plural(changedPaths, "path")} changed</span>
        )}
      </div>
    </section>
  )
}
