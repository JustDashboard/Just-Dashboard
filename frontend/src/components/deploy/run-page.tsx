"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import {
  ArrowLeft,
  ArrowUpRight,
  External,
  GitCommit,
  Globe,
  Link as LinkGlyph,
  LockClosed,
  RefreshClockwise,
  StopCircle,
  Warning,
} from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { Envelope, useSocket } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEngineRun,
  DeploymentOperations,
  DeploymentRelease,
  DeploymentRunEvent,
  DeploymentRunSettingsDrift,
  DeploymentRunSnapshot,
  DeploymentRunsPage,
  DeploymentStepState,
  DeploymentSummary,
} from "@/lib/types"
import type { ProjectDetail } from "@/components/deploy/project-context"
import { FlowSteps } from "@/components/flow"
import { Page, PageHeader } from "@/components/page"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbMenu, type Verb } from "@/components/verbs"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { AuthorMark, BranchChip, ShortSha } from "@/components/git/marks"
import { Button } from "@/components/ui/button"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"
import {
  CREATION_SPINE,
  CREATION_STEPS,
  RUN_LABELS,
  RunStatus,
  deploymentURL,
  formatDuration,
  hostOf,
  isActiveRun,
  isCancellable,
  isRetryable,
  latestAttempts,
  operationLabel,
  projectProduct,
  runCommit,
  runDurationSeconds,
  runFailed,
  runRef,
  runRevision,
  runSubject,
  runTriggerLine,
  sentence,
  shortIdentity,
  shortRevision,
  sourceProduct,
  stepName,
  useNow,
} from "@/components/deploy/vocabulary"
import {
  BuildConsole,
  consoleRows,
  transcriptSummary,
  type BuildLogEvent,
} from "@/components/deploy/build-console"
import { ReleasePipeline } from "@/components/deploy/run-pipeline"
import { RunSteps } from "@/components/deploy/run-steps"
import { RunLogs } from "@/components/deploy/run-logs"
import { useProjectNavScope } from "@/components/deploy/project-shell"
import { RunMetrics } from "@/components/deploy/run-metrics"
import { RunActorMark } from "@/components/deploy/run-marks"
import { releaseVerbs } from "@/components/deploy/run-verbs"
import {
  causeHeadline,
  causeTitle,
  deployWithCurrentSettings,
  driftLine,
  failureCause,
  fixTarget,
  runPlanIsStale,
} from "@/components/deploy/failure-cause"
import { RollbackDialog } from "@/components/deploy/rollback-dialog"
import { ReleaseComparisonSheet } from "@/components/deploy/release-comparison-sheet"

const VIEWS = [
  ["build", "Build logs"],
  ["runtime", "Runtime logs"],
  ["details", "Details"],
  ["metrics", "Metrics"],
] as const
type View = (typeof VIEWS)[number][0]

/**
 * The deployment's own destination — a breadcrumb back to the project, not a
 * tab of it, exactly as a Vercel deployment page sits outside the project
 * shell it was built from.
 *
 * It opens on the identity line every page that describes one thing shares
 * (`HostIdentity`: the Overview's host, Version's install, the account's
 * profile): where the source lives, drawn as that forge or product; the commit
 * the run built as its subject, with its branch, sha and author as the Git
 * page draws a commit; and the run itself as a sentence — what it did, who or
 * what asked for it as their face or product, where, and when — beside the one
 * figure the page is watched for, how long it has taken. Then the release
 * path, how the run ended, and four views of it.
 *
 * How it ended is said once, in the shape its meaning takes (§14): a failure
 * that needs a decision is a `Notice` in the reader's words with the engine's
 * code as a literal beside them and a way to the step that failed; a release
 * that went live is a state and an address; one that has been replaced since
 * says which is live now.
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
  const [liveSnapshot, setLiveSnapshot] = useState<DeploymentRunSnapshot>()
  const [events, setEvents] = useState<BuildLogEvent[]>([])
  const [selectedStepId, setSelectedStepId] = useState<number>()
  const [working, setWorking] = useState<"cancel" | "retry" | "redeploy" | "deploy" | "pin">()
  // The transcript line a failure's cause points at, and a count so pressing
  // "Show the line" twice scrolls to it twice.
  const [focusLine, setFocusLine] = useState<{ seq: number; nonce: number }>()
  const [streamComplete, setStreamComplete] = useState(false)
  const [rollbackOpen, setRollbackOpen] = useState(false)
  const [compare, setCompare] = useState<{
    open: boolean
    releaseId?: number
    fromReleaseId?: number
  }>({ open: false })
  const [view, setView] = useSessionState<View>(`deploy.run.${projectId}.${runId}.view`, "build")
  const lastSeq = useRef(0)
  const streamedRunState = useRef<DeploymentEngineRun["state"] | undefined>(undefined)
  const snapshot = liveSnapshot ?? initial.data

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
  // one it is making, the one it rolls back to, or the two Metrics compares:
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
            snapshot.run.state === "succeeded" ||
            view === "metrics"),
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

  const onMessage = (envelope: Envelope) => {
    if (envelope.type === "snapshot" && isSnapshot(envelope.data)) {
      streamedRunState.current = envelope.data.run.state
      setLiveSnapshot(envelope.data)
      return
    }
    if (envelope.type !== "events" || !Array.isArray(envelope.data)) return
    const incoming = envelope.data.filter(isRunEvent)
    if (incoming.length === 0) return
    let newest = lastSeq.current
    for (const event of incoming) newest = Math.max(newest, event.seq)
    for (const event of incoming) {
      if (event.type === "run.state" && typeof event.data.state === "string")
        streamedRunState.current = event.data.state as DeploymentEngineRun["state"]
    }
    lastSeq.current = newest
    // A resync replaces state rather than appending to it: the retained
    // transcript may have been compacted since our last sequence, so the
    // lines we were holding are no longer a prefix of the truth.
    const resync = incoming.find((event) => event.type === "resync")
    if (resync && isSnapshot(resync.data.snapshot)) {
      setLiveSnapshot(resync.data.snapshot)
      setEvents([])
    }
    setLiveSnapshot((current) =>
      incoming.reduce((next, event) => applyEvent(next, event), current ?? initial.data),
    )
    const lines = incoming.flatMap(logEvent)
    if (lines.length > 0) {
      setEvents((current) => dedupeEvents([...current, ...lines]).slice(-5000))
    }
  }

  const socket = useSocket(`/deploy/${projectId}/runs/${runId}/stream`, {
    enabled: validIds && !streamComplete,
    query: () => ({ after: lastSeq.current }),
    onMessage,
    onClose: () => {
      if (streamedRunState.current && !isActiveRun(streamedRunState.current)) {
        setStreamComplete(true)
      }
    },
  })

  const attempts = useMemo(() => latestAttempts(snapshot?.steps ?? []), [snapshot?.steps])
  // The transcript is parsed once, here, for the console and for the counts
  // the view strip and Details read from it.
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
        <PageHeader eyebrow="Deployments" title={`Deployment #${route.run}`} />
        <LoadingPanel rows={7} plain />
      </Page>
    )
  }
  if (initial.error || !snapshot) {
    return (
      <Page>
        <PageHeader eyebrow="Deployments" title="Deployment unavailable" />
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
  const stale =
    isRetryable(run.state) && (drift.data ? drift.data.changed : runPlanIsStale(run, deployment))
  const changedSince = isRetryable(run.state) ? driftLine(drift.data) : undefined

  const selected =
    attempts.find((step) => step.id === selectedStepId) ??
    attempts.find((step) => step.state === "running" || step.state === "failed") ??
    attempts.at(-1)

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
    try {
      const created = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/environments/${run.environmentId}/runs`,
        deployWithCurrentSettings(run, deployment),
      )
      router.push(`/deploy/${projectId}/runs/${created.id}`)
    } catch (error) {
      notify.error("Could not start deployment", error)
      setWorking(undefined)
    }
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

  const showFailure = () => {
    if (!failedStep) return
    setSelectedStepId(failedStep.id)
    setView("build")
  }

  const showLine = () => {
    if (!cause?.lineSeq) return
    showFailure()
    setFocusLine((current) => ({ seq: cause.lineSeq!, nonce: (current?.nonce ?? 0) + 1 }))
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
            label: "Retry with the settings it used",
            detail: "Run this deployment again with its own plan and variables, unchanged.",
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
      detail: "Its overview, the live site and every other deployment.",
      icon: ArrowUpRight,
      run: () => router.push(`/deploy/${projectId}`),
    },
    {
      key: "link",
      label: "Copy link",
      detail: "This deployment's address, to send to someone.",
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

  // The spine the reader walked on /deploy/new, all of it done, and this
  // run as its last step: current while it builds and after it fails, done
  // once it succeeds.
  const spineStep = run.state === "succeeded" ? CREATION_SPINE.length : CREATION_STEPS.length

  return (
    <Page className="animate-rise">
      <Confetti ref={confetti} className="pointer-events-none fixed inset-0 z-50 size-full" />
      <div className="flex min-w-0 flex-col gap-4">
        <PageHeader
          eyebrow={
            <Link
              href={`/deploy/${projectId}`}
              className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
            >
              <ArrowLeft className="size-3" /> {deployment?.name || "Deployment"}
            </Link>
          }
          title={
            <span className="inline-flex max-w-full min-w-0 items-center gap-3">
              <span className="truncate">Deployment #{run.runNumber}</span>
              <RunStatus state={run.state} live className="shrink-0" />
            </span>
          }
          actions={
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
                  {working === "deploy" ? "Starting…" : "Deploy with current settings"}
                </Button>
              )}
              {canRun && run.state === "succeeded" && isLiveRelease && (
                <Button
                  variant="outline"
                  size="sm"
                  pending={working === "redeploy"}
                  onClick={redeploy}
                >
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
          }
        />
        {/* The sequence's last step, happening. §16 keeps this page in the
            reading register — you are watching, not deciding — and carries the
            spine across, so a reader who has just pressed Deploy watches the
            sequence continue instead of landing on a page with no trace of the
            screens behind it.

            Only the run the flow's own Deploy button enqueued, which is what
            these three clauses together are: the project's first, deployed
            rather than adopted — Configure starts a run only for `deploy`
            (`new-project/draft.ts`), so an import's run #1 is an `import_adopt`
            begun from the project page — and planned by this engine rather
            than carried over from the old one. Anywhere else there is no flow
            behind the run, and the spine would claim screens this reader never
            walked. */}
        {run.runNumber === 1 && run.operation === "deploy" && !legacy && (
          <FlowSteps steps={CREATION_SPINE} current={spineStep} />
        )}
      </div>

      <p className="sr-only" aria-live="polite" aria-atomic="true">
        Deployment state: {RUN_LABELS[run.state] ?? sentence(run.state)}
        {selected ? `. Current step: ${stepName(selected.key)}, ${selected.state}` : ""}
      </p>

      <RunIdentity
        run={run}
        release={release}
        deployment={deployment}
        branch={project.data?.project.branch}
        now={clock}
        releaseNumbers={releaseNumbers}
        targetReleaseId={targetReleaseId}
        projectId={projectId}
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
        <Notice
          tone={run.state === "rolled_back" ? "warning" : "danger"}
          icon={Warning}
          title={
            <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1">
              <span>
                {run.state === "rolled_back"
                  ? `Rolled back — ${kept ? `release #${kept.number}` : "the previous release"} stayed live`
                  : cause
                    ? causeHeadline(cause)
                    : causeTitle(run.terminalCode || "deployment_failed")}
              </span>
              {run.terminalCode && <Tag mono>{run.terminalCode}</Tag>}
            </span>
          }
        >
          {cause?.subjects && cause.subjects.length > 0 && (
            <p className="mb-1.5 flex flex-wrap gap-1.5" aria-label="Named by the failure">
              {cause.subjects.map((subject) => (
                <Tag key={subject} mono>
                  {subject}
                </Tag>
              ))}
            </p>
          )}
          {run.terminalReason && <p>{run.terminalReason}</p>}
          {changedSince && <p className="mt-1.5 text-hint text-muted-foreground">{changedSince}</p>}
          {(fix || failedStep) && (
            <div className="mt-2.5 flex flex-wrap gap-2">
              {fix && (
                // Once the settings have moved on, the header's deploy is the
                // way forward and the fix is a place to check, not the command.
                <Button
                  size="sm"
                  variant={stale ? "outline" : "default"}
                  asChild
                  className="max-sm:w-full"
                >
                  <Link href={fix.href}>{fix.label}</Link>
                </Button>
              )}
              {cause?.lineSeq ? (
                <Button variant="outline" size="sm" className="max-sm:w-full" onClick={showLine}>
                  Show the line
                </Button>
              ) : (
                failedStep && (
                  <Button
                    variant="outline"
                    size="sm"
                    className="max-sm:w-full"
                    onClick={showFailure}
                  >
                    Show the failing step
                  </Button>
                )
              )}
            </div>
          )}
        </Notice>
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
        <section
          aria-label="Outcome"
          className="flex flex-wrap items-center justify-between gap-4 border-t border-hairline pt-5"
        >
          <div className="min-w-0 space-y-1.5">
            <p className="flex min-w-0 items-center gap-2.5 text-title font-semibold tracking-tight">
              <StatusDot tone={isLiveRelease ? "running" : "stopped"} />
              <span className="min-w-0">
                {isLiveRelease
                  ? "Your release is ready"
                  : `Superseded — release #${liveRelease?.number ?? "—"} is live now`}
              </span>
            </p>
            {isLiveRelease && (
              // The run line's clipped dots (`Fact`): a phone wraps the release
              // under the address, and the dot does not lead the line.
              <p className="-my-1 ml-4 overflow-hidden py-1 text-xs text-muted-foreground">
                <span className="-ml-5 flex min-w-0 flex-wrap items-center gap-y-1">
                  <Fact className="max-w-full">
                    {url ? (
                      <a
                        href={url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex min-w-0 items-center gap-1.5 rounded-sm font-mono text-foreground/85 focus-ring hover:text-foreground hover:underline"
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
                  </Fact>
                  {release && <Fact className="numeric">Release #{release.number}</Fact>}
                  {release?.activatedAt && (
                    <Fact>live since {relativeTime(release.activatedAt)}</Fact>
                  )}
                </span>
              </p>
            )}
          </div>
          {!isLiveRelease && (
            <Button variant="outline" size="sm" asChild className="max-sm:w-full">
              <Link href={`/deploy/${projectId}`}>Open project</Link>
            </Button>
          )}
        </section>
      )}

      <div
        role="group"
        aria-label="Run views"
        className="flex flex-wrap gap-1 border-b border-hairline"
      >
        {VIEWS.map(([key, label]) => (
          <button
            key={key}
            type="button"
            aria-pressed={view === key}
            onClick={() => setView(key)}
            className={tabClasses(view === key, "h-10")}
          >
            {label}
            {/* Readings, not part of the name: the count beside a label (§4). */}
            {key === "build" && transcript.lines > 0 && (
              <span aria-hidden className="inline-flex items-center gap-1.5">
                <span className="max-sm:hidden">
                  <ChipCount>{transcript.lines.toLocaleString()}</ChipCount>
                </span>
                {transcript.errors > 0 && <StatusDot tone="danger" />}
              </span>
            )}
            {/* On a phone the four names fill the strip; the counts stay above it. */}
            {key === "details" && attempts.length > 0 && (
              <span aria-hidden className="max-sm:hidden">
                <ChipCount>
                  {attempts.filter((step) => step.state === "passed").length}/{attempts.length}
                </ChipCount>
              </span>
            )}
          </button>
        ))}
      </div>

      {/* Always mounted, only hidden: unmounting on every view switch reset
          Search/Wrap/Follow and the scroll position each time. */}
      <BuildConsole
        hidden={view !== "build"}
        rows={rows}
        summary={transcript}
        capped={events.length >= 5000}
        steps={attempts}
        active={active}
        outcome={run.state}
        connected={socket.state === "open"}
        startedAt={run.claimedAt ?? run.requestedAt}
        runNumber={run.runNumber}
        now={clock}
        selectedStep={selectedStepId}
        onSelectStep={setSelectedStepId}
        focusLine={focusLine}
      />
      {view === "runtime" && (
        <RunLogs
          projectId={projectId}
          runId={runId}
          kind={deployment?.sourceKind}
          product={deployment && projectProduct(deployment)}
        />
      )}
      {view === "details" && (
        <RunSteps
          steps={attempts}
          now={clock}
          lineCounts={transcript.perStep}
          onSelectStep={(id) => {
            setSelectedStepId(id)
            setView("build")
          }}
        />
      )}
      {view === "metrics" && (
        <RunMetrics
          projectId={projectId}
          runId={runId}
          releaseNumbers={releaseNumbers}
          kind={deployment?.sourceKind}
          product={deployment && projectProduct(deployment)}
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

/**
 * The run as one identity line: its source drawn as the product that holds
 * it, the commit it built as the title, and two lines of facts — the commit
 * (branch, sha, author, when it was written) and the run (what it did, who
 * asked, where and when) — with how long it took as the line's one figure.
 *
 * A git run with no recorded commit is titled by its repository and keeps the
 * branch among its facts; with neither, the branch is the title and is not
 * drawn twice. Until the project is read the source's kind is unknown, and
 * the line says only what the run itself holds rather than guessing git.
 */
function RunIdentity({
  run,
  release,
  deployment,
  branch: configured,
  now,
  releaseNumbers,
  targetReleaseId,
  projectId,
}: {
  run: DeploymentEngineRun
  /** The release the run made, whose commit names a run that recorded none. */
  release?: DeploymentRelease
  deployment?: DeploymentSummary
  branch?: string
  now: number
  releaseNumbers: Map<number, number>
  targetReleaseId?: number
  projectId: number
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
  const rollsBackTo = targetReleaseId ? releaseNumbers.get(targetReleaseId) : undefined
  // A run that has not reached a slot, or never did, has only been queued:
  // "took" would name its queue time as a build's.
  const unclaimed = claimed === undefined
  const slot =
    !unclaimed && !Number.isNaN(queued)
      ? `queued ${formatDuration(Math.max(0, (claimed - queued) / 1000))} · ${run.slotClass} build slot`
      : active
        ? `waiting for a ${run.slotClass} build slot`
        : "never reached a build slot"

  return (
    <div data-slot="run-identity" className="min-w-0">
      <HostIdentity
        mark={deployment ? sourceProduct(deployment) : undefined}
        fallback={GitCommit}
        className="pb-5"
        title={<span className={cn(titleIsLiteral && "font-mono text-body")}>{title}</span>}
        facts={
          <>
            {git && title !== branch && <BranchChip branch={branch} className="max-w-48" />}
            {git ? (
              <ShortSha sha={shortRevision(revision)} />
            ) : (
              kind && revision && <span className="font-mono">{shortIdentity(revision)}</span>
            )}
            {commit?.author && (
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <AuthorMark name={commit.author} />
                <span className="truncate max-sm:hidden">{commit.author}</span>
              </span>
            )}
            {commit?.authoredAt && <span>authored {relativeTime(commit.authoredAt)}</span>}
            {/* Each fact carries the dot before it, in one unit that does
                not break, and the row sits one dot's width to the left inside
                a box that clips: the fact that starts a line — the first, or
                one a phone wrapped — has its dot cut off, so a dot only ever
                stands between two facts. */}
            <span className="-my-1 basis-full overflow-hidden py-1">
              <span className="-ml-5 flex min-w-0 flex-wrap items-center gap-y-1">
                <Fact>
                  <span className="font-medium text-foreground/85">
                    {operationLabel(run.operation)}
                  </span>
                </Fact>
                <Fact>
                  <RunActorMark run={run} remote={deployment?.sourceRemote} size="xs" />
                  <span>{runTriggerLine(run)}</span>
                </Fact>
                {deployment && <Fact>{deployment.environmentName}</Fact>}
                <Fact>
                  <time dateTime={run.requestedAt} title={relativeTime(run.requestedAt)}>
                    {timestamp(run.requestedAt)}
                  </time>
                </Fact>
                {rollsBackTo !== undefined && (
                  <Fact className="numeric">rolls back to release #{rollsBackTo}</Fact>
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
              </span>
            </span>
          </>
        }
        aside={
          <div className="min-w-0 sm:text-right">
            <p className="eyebrow">{unclaimed ? "Queued for" : active ? "Running for" : "Took"}</p>
            <p className="numeric text-2xl leading-tight font-semibold tracking-tight">
              {formatDuration(seconds)}
            </p>
            <p className="numeric text-hint text-muted-foreground">{slot}</p>
          </div>
        }
      />
    </div>
  )
}

/**
 * One fact of the run's line, with the dot that parts it from the one before:
 * 20px ahead of the words (`pl-2`, the dot's `w-1`, `gap-2`), which is what
 * the row's `-ml-5` hides for the fact that starts a line.
 */
function Fact({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <span className={cn("inline-flex items-center gap-2 pl-2 whitespace-nowrap", className)}>
      <span aria-hidden className="inline-flex w-1 justify-center">
        <FactDot />
      </span>
      <span className="inline-flex min-w-0 items-center gap-1.5">{children}</span>
    </span>
  )
}

function numberOf(value: unknown) {
  return typeof value === "number" && value > 0 ? value : undefined
}

function applyEvent(
  snapshot: DeploymentRunSnapshot | undefined,
  event: DeploymentRunEvent,
): DeploymentRunSnapshot | undefined {
  if (!snapshot || event.type === "resync") return snapshot
  if (event.type === "run.state" && typeof event.data.state === "string") {
    return {
      ...snapshot,
      run: {
        ...snapshot.run,
        state: event.data.state as DeploymentEngineRun["state"],
        terminalCode:
          typeof event.data.code === "string" ? event.data.code : snapshot.run.terminalCode,
        terminalReason:
          typeof event.data.reason === "string" ? event.data.reason : snapshot.run.terminalReason,
      },
    }
  }
  if (event.type !== "step.state") return snapshot
  const key = typeof event.data.key === "string" ? event.data.key : ""
  const attempt = typeof event.data.attempt === "number" ? event.data.attempt : 1
  const state =
    typeof event.data.state === "string" ? (event.data.state as DeploymentStepState) : undefined
  if (!state) return snapshot
  let found = false
  const steps = snapshot.steps.map((step) => {
    if (step.id !== event.stepId) return step
    found = true
    return {
      ...step,
      state,
      attempt,
      evidence: isRecord(event.data.evidence) ? event.data.evidence : step.evidence,
      errorCode: typeof event.data.errorCode === "string" ? event.data.errorCode : step.errorCode,
      errorMessage:
        typeof event.data.errorMessage === "string" ? event.data.errorMessage : step.errorMessage,
      lastSeq: event.seq,
    }
  })
  if (!found && key) {
    const previous = [...snapshot.steps].reverse().find((step) => step.key === key)
    steps.push({
      ...(previous ?? {
        id: event.stepId ?? event.seq,
        runId: event.runId,
        key,
        ordinal: snapshot.steps.length,
        timeoutSeconds: 0,
        evidence: {},
      }),
      id: event.stepId ?? event.seq,
      state,
      attempt,
      errorCode: typeof event.data.errorCode === "string" ? event.data.errorCode : undefined,
      errorMessage:
        typeof event.data.errorMessage === "string" ? event.data.errorMessage : undefined,
      lastSeq: event.seq,
    })
  }
  return { ...snapshot, steps }
}

function logEvent(event: DeploymentRunEvent): BuildLogEvent[] {
  if (event.type !== "step.log" || typeof event.data.text !== "string") return []
  return [
    {
      seq: event.seq,
      stepId: event.stepId ?? 0,
      ts: event.ts,
      stream: typeof event.data.stream === "string" ? event.data.stream : "stdout",
      text: event.data.text,
      truncated: event.data.truncated === true,
    },
  ]
}

function dedupeEvents(events: BuildLogEvent[]) {
  const bySequence = new Map<number, BuildLogEvent>()
  for (const event of events) bySequence.set(event.seq, event)
  return [...bySequence.values()].sort((a, b) => a.seq - b.seq)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function isSnapshot(value: unknown): value is DeploymentRunSnapshot {
  if (!isRecord(value) || !isRecord(value.run) || !Array.isArray(value.steps)) return false
  return typeof value.run.id === "number"
}

function isRunEvent(value: unknown): value is DeploymentRunEvent {
  if (!isRecord(value) || !isRecord(value.data)) return false
  return typeof value.seq === "number" && typeof value.type === "string"
}
