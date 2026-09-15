"use client"

import { useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import {
  ArrowLeft,
  ArrowRight,
  Check,
  RefreshClockwise,
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
  DeploymentRunEvent,
  DeploymentRunSnapshot,
  DeploymentStep,
  DeploymentStepState,
  DeploymentSummary,
} from "@/lib/types"
import { Page, PageHeader, Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import {
  DeploymentStatus,
  ReleasePath,
  humanize,
  isActiveRun,
  stepStateLabel,
  deploymentURL,
} from "@/components/deploy/deployment-ui"
import { Button } from "@/components/ui/button"
import { DeploymentRunLogs } from "@/components/deploy/deployment-run-logs"
import { DeploymentRunMetrics } from "@/components/deploy/deployment-run-metrics"
import { BuildTranscript, type TranscriptLine } from "@/components/deploy/build-transcript"

const TERMINAL_RETRY = new Set(["failed", "cancelled"])

export function DeploymentRunWorkspace() {
  const route = useParams<{ id: string; run: string }>()
  const router = useRouter()
  const { can } = useAuth()
  const projectID = Number(route.id)
  const runID = Number(route.run)
  const validIDs = projectID > 0 && runID > 0
  const initial = usePoll(
    (signal) => get<DeploymentRunSnapshot>(`/deploy/${projectID}/runs/${runID}`, undefined, signal),
    0,
    [projectID, runID],
    { enabled: validIDs },
  )
  const [liveSnapshot, setLiveSnapshot] = useState<DeploymentRunSnapshot>()
  const [lines, setLines] = useState<TranscriptLine[]>([])
  const [selectedStepID, setSelectedStepID] = useState<number>()
  const [working, setWorking] = useState<"cancel" | "retry">()
  const [streamComplete, setStreamComplete] = useState(false)
  const lastSeq = useRef(0)
  const streamedRunState = useRef<DeploymentEngineRun["state"] | undefined>(undefined)
  const snapshot = liveSnapshot ?? initial.data
  const [view, setView] = useState("build")
  const project = usePoll(
    (signal) => get<{ deployment: DeploymentSummary }>(`/deploy/${projectID}`, undefined, signal),
    5000,
    [projectID],
    { enabled: validIDs },
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

  const socket = useSocket(`/deploy/${projectID}/runs/${runID}/stream`, {
    enabled: validIDs && !streamComplete,
    query: () => ({ after: lastSeq.current }),
    onMessage,
    onClose: () => {
      if (streamedRunState.current && !isActiveRun(streamedRunState.current)) {
        setStreamComplete(true)
      }
    },
  })

  const attempts = useMemo(() => latestAttempts(snapshot?.steps ?? []), [snapshot?.steps])
  const selected =
    attempts.find((step) => step.id === selectedStepID) ??
    attempts.find((step) => step.state === "running" || step.state === "failed") ??
    attempts.at(-1)
  const cancel = async () => {
    if (!snapshot) return
    setWorking("cancel")
    try {
      const run = await post<DeploymentEngineRun>(`/deploy/${projectID}/runs/${runID}/cancel`, {})
      setLiveSnapshot((current) => ({ ...(current ?? snapshot), run }))
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
    setWorking("retry")
    try {
      const run = await post<DeploymentEngineRun>(`/deploy/${projectID}/runs/${runID}/retry`, {})
      router.push(`/deploy/${projectID}/runs/${run.id}`)
    } catch (error) {
      notify.error("Could not retry deployment", error)
      setWorking(undefined)
    }
  }

  if (initial.loading && !snapshot) {
    return (
      <Page>
        <PageHeader eyebrow="Deployments" title={`Loading run #${runID}`} />
        <LoadingPanel rows={7} />
      </Page>
    )
  }
  if (initial.error || !snapshot) {
    return (
      <Page>
        <PageHeader eyebrow="Deployments" title="Deployment run unavailable" />
        {initial.error && <ErrorState error={initial.error} />}
        <Button variant="outline" size="sm" asChild>
          <Link href={`/deploy/${projectID}`}>
            <ArrowLeft className="size-3.5" /> Back to deployment
          </Link>
        </Button>
      </Page>
    )
  }

  const run = snapshot.run
  const active = isActiveRun(run.state)
  const canCancel = can("service.control") && active && !run.cancelRequested
  const canRetry = can("service.control") && TERMINAL_RETRY.has(run.state)

  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link
            href={`/deploy/${projectID}`}
            className="inline-flex items-center gap-1 hover:underline"
          >
            <ArrowLeft className="size-3" />{" "}
            {project.data?.deployment.name || `Deployment ${projectID}`}
          </Link>
        }
        title={`Run #${run.id}`}
        actions={
          <>
            <DeploymentStatus state={run.state} />
            {canCancel && (
              <Button variant="outline" size="sm" disabled={working === "cancel"} onClick={cancel}>
                <StopCircle className="size-3.5" />
                {working === "cancel" ? "Cancelling…" : "Cancel"}
              </Button>
            )}
            {canRetry && (
              <Button size="sm" disabled={working === "retry"} onClick={retry}>
                <RefreshClockwise className="size-3.5" />
                {working === "retry" ? "Starting…" : "Retry"}
              </Button>
            )}
          </>
        }
      />

      <p className="sr-only" aria-live="polite" aria-atomic="true">
        Deployment state: {humanize(run.state)}
        {selected ? `. Current step: ${humanize(selected.key)}, ${selected.state}` : ""}
      </p>

      <Panel>
        <PanelHeader
          title={
            active
              ? "Deploying your project"
              : run.state === "succeeded"
                ? "Deployment complete"
                : "Deployment summary"
          }
          actions={<DeploymentStatus state={run.state} />}
        />
        <PanelBody className="space-y-5 p-5">
          <MetricStrip>
            <Metric
              label="Project"
              value={project.data?.deployment.name || `Deployment ${projectID}`}
            />
            <Metric
              label="Environment"
              value={project.data?.deployment.environmentName || `#${run.environmentId}`}
            />
            <Metric label="Started" value={timestamp(run.requestedAt)} />
            <Metric label="Action" value={humanize(run.operation)} />
          </MetricStrip>
          {active && (
            <p className="text-sm text-muted-foreground" role="status">
              {selected ? `${humanize(selected.key)}…` : "Waiting for a build slot…"}
            </p>
          )}
          {run.state === "succeeded" && (
            <div className="flex flex-wrap items-center justify-between gap-4 border-t border-hairline pt-4">
              <div className="min-w-0">
                <p className="text-sm font-medium">
                  {Boolean(run.releaseId) &&
                  project.data?.deployment.liveReleaseId === run.releaseId
                    ? "Your release is ready"
                    : "This run finished successfully"}
                </p>
                <p className="mt-1 font-mono text-xs break-all text-muted-foreground">
                  {Boolean(run.releaseId) &&
                  project.data?.deployment.liveReleaseId === run.releaseId
                    ? deploymentURL(project.data?.deployment.endpoint) ||
                      "Private service on your server"
                    : "Open the project to see the current live release."}
                </p>
              </div>
              <div className="flex gap-2">
                <Button variant="outline" size="sm" asChild>
                  <Link href={`/deploy/${projectID}`}>Open project</Link>
                </Button>
                {Boolean(run.releaseId) &&
                  project.data?.deployment.liveReleaseId === run.releaseId &&
                  deploymentURL(project.data?.deployment.endpoint) && (
                    <Button size="sm" asChild>
                      <a
                        href={deploymentURL(project.data?.deployment.endpoint)}
                        target="_blank"
                        rel="noopener noreferrer"
                      >
                        Visit <ArrowRight className="size-3.5" />
                      </a>
                    </Button>
                  )}
              </div>
            </div>
          )}
        </PanelBody>
      </Panel>

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

      <div role="group" aria-label="Run views" className="flex flex-wrap gap-2">
        {[
          ["build", "Build logs"],
          ["runtime", "Runtime logs"],
          ["metrics", "Metrics"],
          ["details", "Execution details"],
        ].map(([key, label]) => (
          <Button
            key={key}
            variant={view === key ? "secondary" : "ghost"}
            size="sm"
            aria-pressed={view === key}
            onClick={() => setView(key)}
          >
            {label}
          </Button>
        ))}
      </div>
      {view === "build" && (
        <BuildTranscript
          lines={lines}
          steps={attempts}
          active={active}
          connected={socket.state === "open"}
          selectedStep={selectedStepID}
          onSelectStep={setSelectedStepID}
        />
      )}
      {view === "runtime" && <DeploymentRunLogs projectID={projectID} runID={runID} />}
      {view === "metrics" && <DeploymentRunMetrics projectID={projectID} runID={runID} />}
      {view === "details" && (
        <Panel>
          <PanelHeader title="Execution details" />
          <PanelBody>
            <ReleasePath steps={attempts} />
          </PanelBody>
          <PanelBody flush>
            <ol className="divide-y divide-hairline">
              {attempts.map((step) => (
                <li key={step.id}>
                  <button
                    type="button"
                    onClick={() => {
                      setSelectedStepID(step.id)
                      setView("build")
                    }}
                    className="flex min-h-14 w-full items-start gap-3 px-4 py-3 text-left focus-ring-inset hover:bg-row-hover"
                  >
                    <StepMarker state={step.state} />
                    <span className="min-w-0 flex-1">
                      <span className="block text-sm">{humanize(step.key)}</span>
                      {step.errorMessage && (
                        <span className="mt-1 block text-xs text-destructive">
                          {step.errorMessage}
                        </span>
                      )}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {stepStateLabel(step.state)}
                      {step.attempt > 1 ? ` · attempt ${step.attempt}` : ""}
                    </span>
                    <ArrowRight className="size-3.5 text-muted-foreground" />
                  </button>
                </li>
              ))}
            </ol>
          </PanelBody>
        </Panel>
      )}
    </Page>
  )
}

function StepMarker({ state }: { state: DeploymentStepState }) {
  if (state === "passed") return <Check className="size-3.5 shrink-0 text-success" />
  if (state === "running")
    return (
      <span className="size-2.5 shrink-0 animate-pulse rounded-full bg-warning motion-reduce:animate-none" />
    )
  if (state === "failed" || state === "blocked")
    return <span className="size-2.5 shrink-0 rounded-full bg-destructive" />
  return <span className="size-2.5 shrink-0 rounded-full border border-muted-foreground/50" />
}

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
