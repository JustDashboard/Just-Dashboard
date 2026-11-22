"use client"

import { usualRunSeconds } from "@/components/deploy/build-timing"
import Link from "next/link"
import { Stopwatch } from "@/components/icons"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { promotionOrigin } from "@/lib/pull-requests"
import { cn } from "@/lib/utils"
import type {
  DeploymentEngineRun,
  DeploymentPromotion,
  DeploymentRelease,
  DeploymentStep,
  DeploymentSummary,
} from "@/lib/types"
import { hasProductLogo } from "@/components/product-logo"
import { IdentityHeader } from "@/components/identity-header"
import { Fact } from "@/components/fact-line"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { AuthorMark, BranchChip, ShortSha } from "@/components/git/marks"
import { ProjectMark } from "@/components/project-mark"
import { RunActorMark } from "@/components/deploy/run-marks"
import {
  formatDuration,
  isActiveRun,
  operationLabel,
  runCommit,
  runDurationSeconds,
  runRef,
  runRevision,
  RunStatus,
  runSubject,
  runTriggerLine,
  shortIdentity,
  shortRevision,
  sourceProduct,
  stepName,
  stepOperation,
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
 * the right for one reading, and the tallest header in the section. Then it
 * hand-built the project header's grid as a copy; it is that header now
 * (`IdentityHeader`), so the state, the verbs and the phone's order are the
 * project's own, and its facts are a `FactLine`.
 */
export function RunHeader({
  run,
  release,
  deployment,
  environment,
  product,
  branch: configured,
  steps,
  now,
  rollsBackTo,
  promotion,
  projectId,
  verbs,
}: {
  run: DeploymentEngineRun
  /** The release the run made, whose commit names a run that recorded none. */
  release?: DeploymentRelease
  deployment?: DeploymentSummary
  /** The environment the run deployed to, when it is not the project's own — a preview's. */
  environment?: string
  /** What the project is, as its own pages read it. */
  product?: string
  branch?: string
  steps: DeploymentStep[]
  now: number
  /** The release number a rollback run returns to. */
  rollsBackTo?: number
  /** The release a promotion takes its image from: staging's, or a pull request preview's. */
  promotion?: DeploymentPromotion
  projectId: number
  /** What can be done to the run, at the line's end. */
  verbs: React.ReactNode
}) {
  const commit = runCommit(run)
  const origin = promotionOrigin(promotion)
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
  // How long a run like this one usually takes, from the project's recent
  // runs the page already holds, beside the time it has taken so far.
  const usual = active && !unclaimed ? usualRunSeconds(deployment?.recentRuns, run) : undefined
  const forge = deployment && git ? sourceProduct(deployment) : undefined

  return (
    <IdentityHeader
      label={`About deployment #${run.runNumber}`}
      slot="run-identity"
      logo={
        deployment ? (
          <ProjectMark deployment={deployment} product={product} />
        ) : (
          <span aria-hidden className="size-10 rounded-lg border border-hairline" />
        )
      }
      title={
        <span className={cn("truncate", titleIsLiteral && "font-mono text-body")}>{title}</span>
      }
      state={
        <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <RunStatus state={run.state} operation={run.operation} live />
          {working && (
            <TextShimmer className="font-medium">
              {stepName(working.key, stepOperation(run))}
            </TextShimmer>
          )}
          <span className="inline-flex items-center gap-1.5 text-muted-foreground">
            <Stopwatch aria-hidden className="size-3.5" />
            <span>{unclaimed ? "Queued for" : active ? "Running for" : "Took"}</span>
            <span className="numeric font-medium text-foreground">{formatDuration(seconds)}</span>
            {usual !== undefined && (
              <span data-slot="run-usual" className="numeric">
                · usually {formatDuration(usual)}
              </span>
            )}
          </span>
        </span>
      }
      command={verbs}
      facts={
        <>
          <Fact className="numeric font-medium text-foreground">Deployment #{run.runNumber}</Fact>
          {git && title !== branch && (
            <Fact>
              <BranchChip branch={branch} className="max-w-48" />
            </Fact>
          )}
          {git
            ? revision && (
                <Fact product={hasProductLogo(forge) ? forge : undefined}>
                  <ShortSha sha={shortRevision(revision)} />
                </Fact>
              )
            : kind && revision && <Fact mono>{shortIdentity(revision)}</Fact>}
          {commit?.author && (
            <Fact>
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <AuthorMark name={commit.author} />
                <span className="truncate max-sm:hidden">{commit.author}</span>
              </span>
            </Fact>
          )}
          {commit?.authoredAt && (
            <Fact className="max-sm:hidden">{relativeTime(commit.authoredAt)}</Fact>
          )}
          <Fact>
            <span className="inline-flex min-w-0 items-center gap-1.5">
              <RunActorMark run={run} remote={deployment?.sourceRemote} size="xs" />
              <span className="font-medium text-foreground">{operationLabel(run.operation)}</span>
              <span>{runTriggerLine(run)}</span>
            </span>
          </Fact>
          {(environment || deployment) && <Fact>{environment ?? deployment?.environmentName}</Fact>}
          <Fact>
            <time dateTime={run.requestedAt} title={relativeTime(run.requestedAt)}>
              {timestamp(run.requestedAt)}
            </time>
          </Fact>
          <Fact className="numeric">{slot}</Fact>
          {rollsBackTo !== undefined && (
            <Fact className="numeric">rolls back to release #{rollsBackTo}</Fact>
          )}
          {/* Where the image comes from, since a promotion built nothing: the
              staging release — or for staging, the pull request whose preview
              built it, at its commit — and the image's own id, which the
              target runs. */}
          {promotion && origin && (
            <Fact>
              <span data-slot="run-promotion" className="inline-flex min-w-0 items-center gap-1.5">
                <Link
                  href={`/deploy/${projectId}/runs/${promotion.runId}`}
                  className="numeric rounded-sm focus-ring hover:text-foreground hover:underline"
                  title={origin.approvedBy ? `Approved by ${origin.approvedBy}` : undefined}
                >
                  {origin.kind === "preview" ? (
                    <>
                      promoted from {origin.from}
                      {origin.sha && <span className="font-mono"> ({origin.sha})</span>}
                    </>
                  ) : (
                    <>from {origin.from}</>
                  )}
                </Link>
                <span className="truncate font-mono" title={promotion.imageId}>
                  image {shortIdentity(promotion.imageId)}
                </span>
              </span>
            </Fact>
          )}
          {run.retryOfRunId && (
            <Fact>
              <Link
                href={`/deploy/${projectId}/runs/${run.retryOfRunId}`}
                className="rounded-sm focus-ring hover:text-foreground hover:underline"
              >
                retry of an earlier run
              </Link>
            </Fact>
          )}
          {run.supersededBy && (
            <Fact>
              <Link
                href={`/deploy/${projectId}/runs/${run.supersededBy}`}
                className="rounded-sm focus-ring hover:text-foreground hover:underline"
              >
                superseded by a newer run
              </Link>
            </Fact>
          )}
          {changedPaths > 0 && (
            <Fact className="numeric">{plural(changedPaths, "path")} changed</Fact>
          )}
        </>
      }
    />
  )
}
