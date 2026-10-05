"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import {
  ArrowLeft,
  ArrowUpRight,
  External,
  Link as LinkGlyph,
  RefreshClockwise,
  StopCircle,
  Warning,
} from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEngineRun,
  DeploymentOperations,
  DeploymentRelease,
  DeploymentRunSettingsDrift,
  DeploymentRunSnapshot,
  DeploymentRunsPage,
  DeploymentSummary,
} from "@/lib/types"
import type { ProjectDetail } from "@/components/deploy/project-context"
import { Page, PageContext, Section } from "@/components/page"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"
import {
  RUN_LABELS,
  deploymentURL,
  hostOf,
  isActiveRun,
  isCancellable,
  isRetryable,
  latestAttempts,
  projectProduct,
  runFailed,
  sentence,
  stepName,
  useNow,
} from "@/components/deploy/vocabulary"
import { BuildConsole, consoleRows, transcriptSummary } from "@/components/deploy/build-console"
import { ReleasePipeline } from "@/components/deploy/run-pipeline"
import { RunDetails, initialStep } from "@/components/deploy/run-steps"
import { RunHeader } from "@/components/deploy/run-header"
import { RunFailure, RunReady } from "@/components/deploy/run-outcome"
import { useRunStream } from "@/components/deploy/run-stream"
import { useProjectNavScope } from "@/components/deploy/project-shell"
import { releaseVerbs } from "@/components/deploy/run-verbs"
import {
  deployWithCurrentSettings,
  driftLine,
  failureCause,
  fixTarget,
  runPlanIsStale,
} from "@/components/deploy/failure-cause"
import { RollbackDialog } from "@/components/deploy/rollback-dialog"
import { useCheckedDeploy } from "@/components/deploy/deploy-check"
import { ReleaseComparisonSheet } from "@/components/deploy/release-comparison-sheet"

/**
 * The deployment's own destination — a breadcrumb back to the project, not a
 * tab of it, exactly as a Vercel deployment page sits outside the project
 * shell it was built from.
 *
 * It opens on the header every page of its project opens on, saying what the
 * run is instead (`RunHeader`): the project's tile, the commit the run built,
 * its state and how long it took, the verbs at the far end, and one line of
 * provenance. Then the release path, how the run ended, and the run itself in
 * two parts, one under the other: its build logs, and its details — every
 * step it took beside what the picked one recorded.
 *
 * There were four views behind a strip. Runtime logs and Metrics went at the
 * operator's request: what the release does once it runs is the project's
 * Logs and Runtime pages, which read it live, and on a run that never started
 * a container they were a sentence saying so. With two left, a strip hid one
 * of them behind a press for nothing, so both are on the page.
 *
 * How it ended is said once, in the shape its meaning takes (§14): a failure
 * is the one block that asks for a decision (`RunFailure`) — what broke on
 * the tile of the step it broke in, the engine's reason, the last lines the
 * step wrote and what to do; a release that went live is a state and an
 * address; one that has been replaced since says which is live now.
 *
 * The header's menu carries what can be done to the release this run made —
 * compare it, roll back to it, pin it — declared once with the Deployments
 * rows' (`run-verbs.tsx`), so the run is not a dead end once it has finished.
 */
export function RunPage() {
  const route = useParams<{ id: string; run: string }>()
  const router = useRouter()
  const { can } = useAuth()
  const projectId = Number(route.id)
  const runId = Number(route.run)
  const validIds = projectId > 0 && runId > 0

  const initial = usePoll(
    (signal) => get<DeploymentRunSnapshot>(`/deploy/${projectId}/runs/${runId}`, undefined, signal),
    0,
    [projectId, runId],
    { enabled: validIds },
  )
  // The stage the build console is narrowed to, and the step Details shows:
  // two choices, since reading a step's record is no reason to hide the rest
  // of the transcript.
  const [selectedStepId, setSelectedStepId] = useState<number>()
  const [inspectedStepId, setInspectedStepId] = useState<number>()
  const [working, setWorking] = useState<"cancel" | "retry" | "redeploy" | "deploy" | "pin">()
  const checkedDeploy = useCheckedDeploy(projectId)
  // The transcript line a failure's cause points at, and a count so pressing
  // "Show the line" twice scrolls to it twice.
  const [focusLine, setFocusLine] = useState<{ seq: number; nonce: number }>()
  const [rollbackOpen, setRollbackOpen] = useState(false)
  const [compare, setCompare] = useState<{
    open: boolean
    releaseId?: number
    fromReleaseId?: number
  }>({ open: false })
  const stream = useRunStream(projectId, runId, { enabled: validIds, initial: initial.data })
  const setLiveSnapshot = stream.setSnapshot
  const events = stream.events
  const snapshot = stream.snapshot ?? initial.data

  // The project is read separately, on its own poll, purely to answer "is
  // this run's release still the one live today" — the run itself never
  // changes once it exists, but the project's live release does.
  const project = usePoll(
    (signal) => get<ProjectDetail>(`/deploy/${projectId}`, undefined, signal),
    5000,
    [projectId],
    { enabled: validIds },
  )
  // Worth asking once the run has a release to place — the one it made, the
  // one it is making, or the one it rolls back to:
  // resolving a release id to the number an operator recognises ("release
  // #12") needs the environment's release list, which nothing else on this
  // page reads.
  const targetReleaseId = numberOf(snapshot?.run.metadata?.targetReleaseId)
  const releases = usePoll(
    (signal) =>
      get<DeploymentRelease[]>(
        `/deploy/${projectId}/environments/${snapshot?.run.environmentId ?? 0}/releases`,
        { limit: 30 },
        signal,
      ),
    10000,
    [projectId, snapshot?.run.environmentId],
    {
      enabled:
        validIds &&
        Boolean(
          snapshot &&
          (snapshot.run.releaseId ||
            snapshot.run.candidateReleaseId ||
            targetReleaseId ||
            snapshot.run.state === "succeeded"),
        ),
    },
  )
  // Rolling back from here reads what the Deployments page already holds:
  // the runs that made each release, for their commits, and the domains that
  // move — asked for only once the dialog is open.
  const rollbackRuns = usePoll(
    (signal) =>
      get<DeploymentRunsPage>(
        `/deploy/${projectId}/runs`,
        { view: "engine", environment: snapshot?.run.environmentId, limit: 30 },
        signal,
      ),
    0,
    [projectId, snapshot?.run.environmentId],
    { enabled: validIds && rollbackOpen },
  )
  const operations = usePoll(
    (signal) => get<DeploymentOperations>(`/deploy/${projectId}/operations`, undefined, signal),
    0,
    [projectId],
    { enabled: validIds && rollbackOpen },
  )
  // Whether the settings have moved on since a run that can be retried: then
  // a retry replays what failed, and the page offers the current settings
  // first. Read again whenever a newer plan is saved.
  const drift = usePoll(
    (signal) =>
      get<DeploymentRunSettingsDrift>(
        `/deploy/${projectId}/runs/${runId}/settings-drift`,
        undefined,
        signal,
      ),
    0,
    [projectId, runId, project.data?.deployment.desiredRevision],
    { enabled: validIds && isRetryable(snapshot?.run.state) },
  )
  // A run is one of its project's Deployments, so the rail keeps the
  // project's panel the reader opened it from.
  useProjectNavScope(
    project.data && {
      deployment: project.data.deployment,
      name: project.data.project.name,
      archived: Boolean(project.data.project.archivedAt),
    },
  )

  const attempts = useMemo(() => latestAttempts(snapshot?.steps ?? []), [snapshot?.steps])
  // The transcript is parsed once, here, for the console and for what
  // Details and the failure read from it.
  const rows = useMemo(() => consoleRows(events), [events])
  const transcript = useMemo(() => transcriptSummary(rows), [rows])
  const now = useNow(1000, isActiveRun(snapshot?.run.state))
  const releaseNumbers = useMemo(
    () => new Map((releases.data ?? []).map((release) => [release.id, release.number])),
    [releases.data],
  )

  // A release that goes live while somebody is watching it build gets a
  // burst of paper. Only that: arriving at a page that already succeeded
  // is not the moment, and neither is a retry that is still running.
  const confetti = useRef<ConfettiRef>(null)
  const runState = snapshot?.run.state
  const wasActive = useRef(isActiveRun(runState))
  useEffect(() => {
    const activeNow = isActiveRun(runState)
    if (wasActive.current && !activeNow && runState === "succeeded") confetti.current?.fire()
    wasActive.current = activeNow
  }, [runState])

  if (initial.loading && !snapshot) {
    return (
      <Page>
        <PageContext eyebrow="Deployments" title={`Deployment #${route.run}`} />
        <LoadingPanel rows={7} plain />
      </Page>
    )
  }
  if (initial.error || !snapshot) {
    return (
      <Page>
        <PageContext eyebrow="Deployments" title="Deployment unavailable" />
        {initial.error && <ErrorState error={initial.error} />}
        <Button variant="outline" size="sm" asChild className="w-fit">
          <Link href={`/deploy/${projectId}`}>
            <ArrowLeft className="size-3.5" /> Back to deployment
          </Link>
        </Button>
      </Page>
    )
  }

  const run = snapshot.run
  const active = isActiveRun(run.state)
  // A project carried over from the old engine has a run #1 that predates the
  // draft flow entirely, and its one `legacy_pipeline` step is how that run is
  // told apart from a release this engine planned.
  const legacy = attempts.some((step) => step.key === "legacy_pipeline")
  const deployment = project.data?.deployment
  const url = deploymentURL(deployment?.endpoint)
  const isLiveRelease = Boolean(run.releaseId) && deployment?.liveReleaseId === run.releaseId
  const release = releases.data?.find((candidate) => candidate.id === run.releaseId)
  const liveRelease = releases.data?.find((candidate) => candidate.id === deployment?.liveReleaseId)
  // The release that stayed live when this run rolled back is the one its
  // candidate replaced — not whichever is live today, which a later deploy
  // may have changed since.
  const candidate = releases.data?.find((entry) => entry.id === run.candidateReleaseId)
  const kept = releases.data?.find((entry) => entry.id === candidate?.predecessorReleaseId)
  // A finished run's clock stops where the run did, so no step of it reads
  // as still going.
  const clock = !active && run.endedAt ? Math.min(now, Date.parse(run.endedAt)) : now
  const canRun = can("service.control")
  const failure = failureCause(attempts)
  const failedStep = failure?.step
  const cause = failure?.cause
  // A remedy is a settings change, which only an administrator can save.
  const fix = cause?.fix && can("system.admin") ? fixTarget(projectId, cause.fix) : undefined
  // A commit a force-push removed cannot be fetched again, so retrying it
  // fails the same way: what can be deployed is the branch as it is now.
  const commitGone = run.terminalCode === "source_revision_unavailable"
  const stale =
    isRetryable(run.state) &&
    (commitGone || (drift.data ? drift.data.changed : runPlanIsStale(run, deployment)))
  const changedSince = isRetryable(run.state) ? driftLine(drift.data) : undefined

  const current =
    attempts.find((step) => step.state === "running" || step.state === "failed") ?? attempts.at(-1)

  const cancel = async () => {
    if (working) return
    setWorking("cancel")
    try {
      const updated = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/runs/${runId}/cancel`,
        {},
      )
      setLiveSnapshot((current) => (current ? { ...current, run: updated } : current))
      notify.success("Cancellation requested", {
        description: "Cleanup progress remains visible on this page.",
      })
    } catch (error) {
      notify.error("Could not cancel deployment", error)
    } finally {
      setWorking(undefined)
    }
  }

  const retry = async () => {
    if (working) return
    setWorking("retry")
    try {
      const created = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/runs/${runId}/retry`,
        {},
      )
      router.push(`/deploy/${projectId}/runs/${created.id}`)
    } catch (error) {
      notify.error("Could not retry deployment", error)
      setWorking(undefined)
    }
  }

  const deployCurrent = async () => {
    if (working) return
    setWorking("deploy")
    const request = commitGone
      ? { operation: "deploy" as const }
      : deployWithCurrentSettings(run, deployment, drift.data)
    const deploy = async () => {
      try {
        const created = await post<DeploymentEngineRun>(
          `/deploy/${projectId}/environments/${run.environmentId}/runs`,
          request,
        )
        router.push(`/deploy/${projectId}/runs/${created.id}`)
      } catch (error) {
        notify.error("Could not start deployment", error)
        setWorking(undefined)
      }
    }
    const asked = await checkedDeploy.start(
      run.environmentId,
      "sourceRevision" in request ? { sourceRevision: request.sourceRevision } : {},
      deploy,
    )
    // A check that asks leaves the press to its dialog.
    if (asked) setWorking(undefined)
  }

  const redeploy = async () => {
    if (working) return
    setWorking("redeploy")
    try {
      const created = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/environments/${run.environmentId}/runs`,
        { operation: "redeploy" },
      )
      router.push(`/deploy/${projectId}/runs/${created.id}`)
    } catch (error) {
      notify.error("Could not start deployment", error)
      setWorking(undefined)
    }
  }

  const togglePin = async (target: DeploymentRelease) => {
    const pinned = !target.pinned
    setWorking("pin")
    try {
      await put(
        `/deploy/${projectId}/environments/${run.environmentId}/releases/${target.id}/pin`,
        { pinned },
      )
      notify.success(pinned ? "Release pinned" : "Release unpinned")
      releases.refresh()
    } catch (error) {
      notify.error(pinned ? "Could not pin release" : "Could not unpin release", error)
    } finally {
      setWorking(undefined)
    }
  }

  // Both parts are on the page, so leading to one is narrowing it and
  // bringing it into view.
  const showOutput = (stepId: number) => {
    setSelectedStepId(stepId)
    document.getElementById("build-logs")?.scrollIntoView({ behavior: "smooth", block: "start" })
  }

  const showFailure = () => {
    if (!failedStep) return
    showOutput(failedStep.id)
    if (cause?.lineSeq)
      setFocusLine((focus) => ({ seq: cause.lineSeq!, nonce: (focus?.nonce ?? 0) + 1 }))
  }

  const showFailedStep = () => {
    if (!failedStep) return
    setInspectedStepId(failedStep.id)
    document.getElementById("run-details")?.scrollIntoView({ behavior: "smooth", block: "start" })
  }

  // The header's buttons are the run's own verbs; the menu is the release's,
  // and the two ways out of the page.
  const menu: Verb[] = [
    // With the settings changed since, a retry is the exception rather than
    // the way forward, and it says what it replays.
    ...(canRun && stale
      ? [
          {
            key: "retry",
            label: commitGone ? "Retry the recorded commit" : "Retry with the settings it used",
            icon: RefreshClockwise,
            progressive: "Starting…",
            disabled: working === "retry",
            run: () => void retry(),
          } satisfies Verb,
        ]
      : []),
    {
      key: "project",
      label: "Open project",
      icon: ArrowUpRight,
      run: () => router.push(`/deploy/${projectId}`),
    },
    {
      key: "link",
      label: "Copy link",
      icon: LinkGlyph,
      run: () => void copyText(window.location.href, "Link copied"),
    },
    ...releaseVerbs({
      release,
      liveReleaseId: deployment?.liveReleaseId,
      can,
      working: working === "pin" ? "pin" : undefined,
      on: {
        changes: (target) => setCompare({ open: true, releaseId: target.id }),
        compare: (target) =>
          setCompare({
            open: true,
            releaseId: deployment?.liveReleaseId,
            fromReleaseId: target.id,
          }),
        rollback: () => setRollbackOpen(true),
        pin: (target) => void togglePin(target),
      },
    }),
  ]

  // The verbs that act on the run, drawn on its identity line beside its
  // state rather than in a bar above the page. The bar held a way back, one
  // button at the far end and, on a first run, the creation spine — a row of
  // things the rail's panel and the menu already say, standing between the
  // reader and the run. Beside the state each verb reads as the answer to
  // it: Building, Cancel; Live, Visit; Failed, Retry.
  const verbs = (
    <>
      {canRun && isCancellable(run.state) && !run.cancelRequested && (
        <Button variant="outline" size="sm" pending={working === "cancel"} onClick={cancel}>
          <StopCircle className="size-3.5" />
          {working === "cancel" ? "Cancelling…" : "Cancel"}
        </Button>
      )}
      {canRun && isRetryable(run.state) && !stale && (
        <Button size="sm" pending={working === "retry"} onClick={retry}>
          <RefreshClockwise className="size-3.5" />
          {working === "retry" ? "Starting…" : "Retry"}
        </Button>
      )}
      {canRun && stale && (
        <Button size="sm" pending={working === "deploy"} onClick={deployCurrent}>
          <RefreshClockwise className="size-3.5" />
          {working === "deploy"
            ? "Starting…"
            : commitGone
              ? "Deploy the branch head"
              : "Deploy with current settings"}
        </Button>
      )}
      {canRun && run.state === "succeeded" && isLiveRelease && (
        <Button variant="outline" size="sm" pending={working === "redeploy"} onClick={redeploy}>
          <RefreshClockwise className="size-3.5" />
          {working === "redeploy" ? "Redeploying…" : "Redeploy"}
        </Button>
      )}
      {run.state === "succeeded" && isLiveRelease && url && (
        <Button size="sm" asChild>
          <a href={url} target="_blank" rel="noopener noreferrer">
            <External className="size-3.5" /> Visit
          </a>
        </Button>
      )}
      <VerbMenu verbs={menu} label={`More actions for Deployment #${run.runNumber}`} />
    </>
  )

  return (
    <Page className="animate-rise pt-4 md:pt-5">
      <Confetti ref={confetti} className="pointer-events-none fixed inset-0 z-50 size-full" />
      {checkedDeploy.gate}
      {/* The name is for assistive technology alone (§15): the header is the
          first thing drawn. */}
      <PageContext title={`Deployment #${run.runNumber}`} />

      <p className="sr-only" aria-live="polite" aria-atomic="true">
        Deployment state: {RUN_LABELS[run.state] ?? sentence(run.state)}
        {current ? `. Current step: ${stepName(current.key)}, ${current.state}` : ""}
      </p>

      <RunHeader
        run={run}
        release={release}
        deployment={deployment}
        product={deployment && projectProduct(deployment)}
        branch={project.data?.project.branch}
        steps={attempts}
        now={clock}
        rollsBackTo={targetReleaseId ? releaseNumbers.get(targetReleaseId) : undefined}
        projectId={projectId}
        verbs={verbs}
      />

      {legacy ? (
        <p className="text-body text-muted-foreground">Compatibility pipeline</p>
      ) : (
        <div className="flex flex-col gap-3">
          <ReleasePipeline steps={attempts} now={clock} />
          {/* The path lights the stage at work; before there is one, this
              says why nothing has started. */}
          {active && attempts.length === 0 && (
            <p role="status" className="text-hint text-muted-foreground">
              Waiting for a build slot…
            </p>
          )}
        </div>
      )}

      {run.cancelRequested && active ? (
        <Notice tone="warning" icon={Warning} title="Cancellation requested">
          The current operation will stop safely and run its cleanup.
        </Notice>
      ) : runFailed(run.state) && (run.terminalReason || run.terminalCode) ? (
        <RunFailure
          run={run}
          step={failedStep}
          steps={attempts}
          cause={cause}
          kept={kept}
          rows={rows}
          deployment={deployment}
          now={clock}
          changedSince={changedSince}
          fix={fix}
          // Once the settings have moved on, the header's deploy is the way
          // forward and the fix is a place to check, not the command.
          fixIsCommand={!stale}
          onShowOutput={showFailure}
          onShowStep={showFailedStep}
        />
      ) : (run.state === "cancelled" || run.state === "superseded") && run.terminalReason ? (
        // Nothing to decide, so a reading rather than a notice: why it stopped.
        <p className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
          <StatusDot tone="stopped" />
          {run.terminalReason}
        </p>
      ) : null}

      {/* project.data is a separate poll from the run snapshot; reading
          isLiveRelease before it lands would flash "Superseded" for every
          successful run while the project fetch is still in flight. */}
      {run.state === "succeeded" && project.data && (
        <RunReady
          live={isLiveRelease}
          url={url}
          release={release}
          liveRelease={liveRelease}
          projectId={projectId}
        />
      )}

      <Section
        title="Build logs"
        actions={
          transcript.lines > 0 && (
            <p className="numeric text-xs text-muted-foreground">
              {transcript.lines.toLocaleString()} lines
              {transcript.errors > 0 && (
                <span className="text-destructive"> · {plural(transcript.errors, "error")}</span>
              )}
            </p>
          )
        }
      >
        <div id="build-logs" className="min-w-0 scroll-mt-16">
          <BuildConsole
            rows={rows}
            summary={transcript}
            capped={stream.capped}
            steps={attempts}
            active={active}
            outcome={run.state}
            connected={stream.socket === "open"}
            startedAt={run.claimedAt ?? run.requestedAt}
            runNumber={run.runNumber}
            now={clock}
            selectedStep={selectedStepId}
            onSelectStep={setSelectedStepId}
            focusLine={focusLine}
          />
        </div>
      </Section>

      {attempts.length > 0 && (
        <RunDetails
          projectId={projectId}
          deployment={deployment}
          steps={attempts}
          now={clock}
          rows={rows}
          lineCounts={transcript.perStep}
          selected={inspectedStepId ?? initialStep(attempts)?.id}
          onSelect={setInspectedStepId}
          onShowOutput={showOutput}
        />
      )}

      <RollbackDialog
        open={rollbackOpen}
        onOpenChange={setRollbackOpen}
        projectId={projectId}
        environmentId={run.environmentId}
        liveRelease={liveRelease}
        releases={releases.data ?? []}
        runs={rollbackRuns.data?.runs ?? [run]}
        domains={rollbackDomains(operations.data, deployment)}
        remote={deployment?.sourceRemote}
        initialReleaseId={release?.id}
      />
      <ReleaseComparisonSheet
        open={compare.open}
        onOpenChange={(open) => setCompare((current) => ({ ...current, open }))}
        projectId={projectId}
        environmentId={run.environmentId}
        releaseId={compare.releaseId}
        fromReleaseId={compare.fromReleaseId}
        releases={releases.data}
      />
    </Page>
  )
}

/** The hostnames that move with the live release: what the proxy serves, else the endpoint's. */
function rollbackDomains(
  operations: DeploymentOperations | undefined,
  deployment: DeploymentSummary | undefined,
) {
  const observed = operations?.domains.domains.map((domain) => domain.hostname) ?? []
  if (observed.length > 0) return observed
  const url = deploymentURL(deployment?.endpoint)
  const host = url && hostOf(url)
  return host ? [host] : []
}

function numberOf(value: unknown) {
  return typeof value === "number" && value > 0 ? value : undefined
}
