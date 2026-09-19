"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import {
  ArrowLeft,
  External,
  RefreshClockwise,
  RotateCounterClockwise,
  StopCircle,
  Warning,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { Envelope, useSocket } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEngineRun,
  DeploymentRelease,
  DeploymentRunEvent,
  DeploymentRunSnapshot,
  DeploymentStep,
  DeploymentStepState,
  DeploymentSummary,
} from "@/lib/types"
import type { ProjectDetail } from "@/components/deploy/project-context"
import { Page, PageHeader, Metric, MetricStrip } from "@/components/page"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"
import {
  RunStatus,
  deploymentURL,
  formatDuration,
  humanize,
  isActiveRun,
  isCancellable,
  operationLabel,
  runDurationSeconds,
  isRetryable,
  runRef,
  runSubject,
  runTriggerLine,
  shortRevision,
  sourceLine,
  useNow,
} from "@/components/deploy/vocabulary"
import { BuildConsole, type TranscriptLine } from "@/components/deploy/build-console"
import { ReleasePipeline } from "@/components/deploy/run-pipeline"
import { RunSteps } from "@/components/deploy/run-steps"
import { RunLogs } from "@/components/deploy/run-logs"
import { RunMetrics } from "@/components/deploy/run-metrics"

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
  const [lines, setLines] = useState<TranscriptLine[]>([])
  const [selectedStepId, setSelectedStepId] = useState<number>()
  const [working, setWorking] = useState<"cancel" | "retry" | "redeploy">()
  const [streamComplete, setStreamComplete] = useState(false)
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
  // Only worth asking once the run has a release to place: resolving a
  // release id to the number an operator recognises ("release #12") needs
  // the environment's release list, which nothing else on this page reads.
  const releases = usePoll(
    (signal) =>
      get<DeploymentRelease[]>(
        `/deploy/${projectId}/environments/${snapshot?.run.environmentId ?? 0}/releases`,
        { limit: 5 },
        signal,
      ),
    10000,
    [projectId, snapshot?.run.environmentId],
    { enabled: validIds && snapshot?.run.state === "succeeded" },
  )

  const onMessage = (envelope: Envelope) => {
    if (envelope.type === "snapshot" && isSnapshot(envelope.data)) {
      streamedRunState.current = envelope.data.run.state
      setLiveSnapshot(envelope.data)
      return
    }
    if (envelope.type !== "events" || !Array.isArray(envelope.data)) return
    const events = envelope.data.filter(isRunEvent)
    if (events.length === 0) return
    let newest = lastSeq.current
    for (const event of events) newest = Math.max(newest, event.seq)
    for (const event of events) {
      if (event.type === "run.state" && typeof event.data.state === "string")
        streamedRunState.current = event.data.state as DeploymentEngineRun["state"]
    }
    lastSeq.current = newest
    // A resync replaces state rather than appending to it: the retained
    // transcript may have been compacted since our last sequence, so the
    // lines we were holding are no longer a prefix of the truth.
    const resync = events.find((event) => event.type === "resync")
    if (resync && isSnapshot(resync.data.snapshot)) {
      setLiveSnapshot(resync.data.snapshot)
      setLines([])
    }
    setLiveSnapshot((current) =>
      events.reduce((next, event) => applyEvent(next, event), current ?? initial.data),
    )
    const nextLines = events.flatMap(logLine)
    if (nextLines.length > 0) {
      setLines((current) => dedupeLines([...current, ...nextLines]).slice(-5000))
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
  const withOutput = useMemo(() => new Set(lines.map((line) => line.stepId)), [lines])
  const now = useNow(1000, isActiveRun(snapshot?.run.state))

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
        <LoadingPanel rows={7} />
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
  const deployment = project.data?.deployment
  const url = deploymentURL(deployment?.endpoint)
  const isLiveRelease = Boolean(run.releaseId) && deployment?.liveReleaseId === run.releaseId
  const liveReleaseNumber = releases.data?.find(
    (release) => release.id === deployment?.liveReleaseId,
  )?.number
  const canRun = can("service.control")
  const source = runSourceFacts(run, deployment, project.data?.project.branch)

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

  return (
    <Page>
      <Confetti ref={confetti} className="pointer-events-none fixed inset-0 z-50 size-full" />
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
            {canRun && isRetryable(run.state) && (
              <Button size="sm" pending={working === "retry"} onClick={retry}>
                <RefreshClockwise className="size-3.5" />
                {working === "retry" ? "Starting…" : "Retry"}
              </Button>
            )}
            {canRun && run.state === "succeeded" && isLiveRelease && (
              <Button
                variant="outline"
                size="sm"
                pending={working === "redeploy"}
                onClick={redeploy}
              >
                <RotateCounterClockwise className="size-3.5" />
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
          </>
        }
      />

      <p className="sr-only" aria-live="polite" aria-atomic="true">
        Deployment state: {humanize(run.state)}
        {selected ? `. Current step: ${humanize(selected.key)}, ${selected.state}` : ""}
      </p>

      <MetricStrip>
        <Metric
          label="Environment"
          value={deployment?.environmentName ?? `#${run.environmentId}`}
        />
        <Metric label="Operation" value={operationLabel(run.operation)} />
        <Metric label="Trigger" value={runTriggerLine(run)} />
        <Metric label="Started" value={timestamp(run.requestedAt)} />
        <Metric label="Duration" value={formatDuration(runDurationSeconds(run, now))} />
        <Metric
          label="Source"
          value={
            <span className="flex min-w-0 items-baseline gap-1.5">
              <span className="truncate">{source.label}</span>
              {source.sha && (
                <span className="shrink-0 font-mono text-xs text-muted-foreground">
                  {source.sha}
                </span>
              )}
            </span>
          }
          hint={source.subject}
        />
      </MetricStrip>

      {attempts.some((step) => step.key === "legacy_pipeline") ? (
        <p className="text-body text-muted-foreground">Compatibility pipeline</p>
      ) : (
        <div className="flex flex-col gap-3">
          <ReleasePipeline steps={attempts} now={now} />
          {active && (
            <p role="status" className="text-hint text-muted-foreground">
              {selected ? `${humanize(selected.key)}…` : "Waiting for a build slot…"}
            </p>
          )}
        </div>
      )}

      {(run.terminalReason || run.cancelRequested) && (
        <Notice
          tone={run.terminalReason ? "danger" : "warning"}
          icon={Warning}
          title={
            run.terminalReason ? run.terminalCode || "Deployment failed" : "Cancellation requested"
          }
        >
          {run.terminalReason || "The current operation will stop safely and run its cleanup."}
        </Notice>
      )}

      {/* project.data is a separate poll from the run snapshot; reading
          isLiveRelease before it lands would flash "Superseded" for every
          successful run while the project fetch is still in flight. */}
      {run.state === "succeeded" && project.data && (
        <div className="flex flex-wrap items-center justify-between gap-4 border-t border-hairline pt-5">
          <div className="min-w-0 space-y-1">
            <p className="text-body font-medium">
              {isLiveRelease
                ? "Your release is ready"
                : `Superseded — release #${liveReleaseNumber ?? "—"} is live now`}
            </p>
            {isLiveRelease &&
              (url ? (
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="truncate rounded-sm font-mono text-xs text-muted-foreground focus-ring hover:text-foreground hover:underline"
                >
                  {url}
                </a>
              ) : (
                <p className="truncate font-mono text-xs text-muted-foreground">
                  Private service on your server
                </p>
              ))}
          </div>
          {!isLiveRelease && (
            <Button variant="outline" size="sm" asChild>
              <Link href={`/deploy/${projectId}`}>Open project</Link>
            </Button>
          )}
        </div>
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
          </button>
        ))}
      </div>

      {/* Always mounted, only hidden: unmounting on every view switch reset
          Search/Wrap/Follow and the scroll position each time. */}
      <BuildConsole
        hidden={view !== "build"}
        lines={lines}
        steps={attempts}
        active={active}
        connected={socket.state === "open"}
        selectedStep={selectedStepId}
        onSelectStep={setSelectedStepId}
      />
      {view === "runtime" && <RunLogs projectId={projectId} runId={runId} />}
      {view === "details" && (
        <RunSteps
          steps={attempts}
          now={now}
          withOutput={withOutput}
          onSelectStep={(id) => {
            setSelectedStepId(id)
            setView("build")
          }}
        />
      )}
      {view === "metrics" && <RunMetrics projectId={projectId} runId={runId} />}
    </Page>
  )
}

/**
 * The branch (or image, or template), the run's own frozen commit, and its
 * subject when the engine recorded one — the run's Source fact. `sourceLine`
 * gives a project card either the subject or the revision; a run is one fixed
 * commit, so both are worth saying at once here.
 */
function runSourceFacts(
  run: DeploymentEngineRun,
  deployment: DeploymentSummary | undefined,
  branchFallback: string | undefined,
) {
  const isGit = deployment?.sourceKind === "git" || deployment?.sourceKind === "local"
  const branch = isGit ? runRef(run, deployment?.sourceRef || branchFallback) : undefined
  const sha = isGit ? shortRevision(run.sourceRevision) : undefined
  // Every non-git branch of sourceLine ignores its project/run arguments, so
  // the fallback label (image reference, compose, template name) needs
  // neither — only the git case, handled above, ever reads them.
  const fallback = deployment ? sourceLine(deployment).primary : "—"
  return { label: branch ?? fallback, sha, subject: runSubject(run) }
}

/** The most recent attempt of every step, in execution order. */
function latestAttempts(steps: DeploymentStep[]) {
  const byKey = new Map<string, DeploymentStep>()
  for (const step of steps) {
    const current = byKey.get(step.key)
    if (!current || step.attempt >= current.attempt) byKey.set(step.key, step)
  }
  return [...byKey.values()].sort((a, b) => a.ordinal - b.ordinal)
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

function logLine(event: DeploymentRunEvent): TranscriptLine[] {
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

function dedupeLines(lines: TranscriptLine[]) {
  const bySequence = new Map<number, TranscriptLine>()
  for (const line of lines) bySequence.set(line.seq, line)
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
