"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import {
  ArrowLeft,
  ArrowRight,
  Check,
  Clock,
  CloudUpload,
  Copy,
  Logs,
  RefreshClockwise,
  StopCircle,
  Warning,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { clock,  timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { Envelope, useSocket } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEngineRun,
  DeploymentRunEvent,
  DeploymentRunSnapshot,
  DeploymentStep,
  DeploymentStepState,
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
} from "@/components/deploy/deployment-ui"
import { Button } from "@/components/ui/button"
import { DeploymentRunLogs } from "@/components/deploy/deployment-run-logs"
import { DeploymentRunMetrics } from "@/components/deploy/deployment-run-metrics"
import { copyText } from "@/lib/clipboard"

type TranscriptLine = {
  seq: number
  stepId: number
  ts: string
  stream: string
  text: string
  truncated: boolean
}

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
  const [follow, setFollow] = useState(true)
  const [working, setWorking] = useState<"cancel" | "retry">()
  const [lastEventSequence, setLastEventSequence] = useState(0)
  const [streamComplete, setStreamComplete] = useState(false)
  const lastSeq = useRef(0)
  const streamedRunState = useRef<DeploymentEngineRun["state"] | undefined>(undefined)
  const logPane = useRef<HTMLDivElement>(null)
  const snapshot = liveSnapshot ?? initial.data

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
    setLastEventSequence(newest)
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
  const visibleLines = selected ? lines.filter((line) => line.stepId === selected.id) : lines

  useEffect(() => {
    if (!follow || !logPane.current) return
    logPane.current.scrollTop = logPane.current.scrollHeight
  }, [follow, visibleLines])

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

  const copyTranscript = () =>
    copyText(
      visibleLines.map((line) => `[${clock(line.ts)}] ${line.text}`).join(""),
      "Transcript copied",
    )

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

  // Not a `fill` page: the transcript is followed by the runtime-log and metrics
  // panels, so a viewport-height frame left the steps rail and the transcript
  // sharing whatever was left over — one visible row each. The reading pane
  // carries a definite height of its own and the page scrolls.
  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link
            href={`/deploy/${projectID}`}
            className="inline-flex items-center gap-1 hover:underline"
          >
            <ArrowLeft className="size-3" /> Deployment {projectID}
          </Link>
        }
        title={`Deployment #${run.id}`}
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

      <MetricStrip className="rounded-lg border border-hairline bg-card px-4 py-3">
        <Metric label="Environment" value={`#${run.environmentId}`} />
        <Metric label="Plan revision" value={String(run.planRevision)} />
        <Metric label="Slot" value={humanize(run.slotClass)} />
        <Metric label="Requested" value={timestamp(run.requestedAt)} />
        <Metric
          label="Stream"
          value={socket.state === "open" ? "Live" : humanize(socket.state)}
          hint={lastEventSequence ? `Through event ${lastEventSequence}` : "Waiting for events"}
        />
      </MetricStrip>

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

      <Panel>
        <PanelHeader
          icon={CloudUpload}
          title="Release path"
        />
        <PanelBody>
          <ReleasePath steps={attempts} />
        </PanelBody>
      </Panel>

      <div className="grid min-w-0 gap-4 xl:h-[34rem] xl:grid-cols-[17rem_minmax(0,1fr)]">
        <Panel className="max-h-[20rem] min-h-0 xl:max-h-none">
          <PanelHeader icon={Clock} title="Steps" />
          <PanelBody flush scroll className="min-h-0">
            <ol className="divide-y divide-hairline">
              {attempts.map((step) => (
                <li key={step.id}>
                  <button
                    type="button"
                    aria-pressed={selected?.id === step.id}
                    onClick={() => setSelectedStepID(step.id)}
                    className={cn(
                      "flex min-h-11 w-full min-w-0 items-center gap-3 px-3 py-2 text-left focus-ring-inset hover:bg-row-hover",
                      selected?.id === step.id && "bg-accent",
                    )}
                  >
                    <StepMarker state={step.state} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium">
                        {humanize(step.key)}
                      </span>
                      <span className="block text-hint text-muted-foreground">
                        {stepStateLabel(step.state)}
                        {step.attempt > 1 ? ` · attempt ${step.attempt}` : ""}
                      </span>
                    </span>
                    <ArrowRight className="size-3 text-muted-foreground" />
                  </button>
                </li>
              ))}
            </ol>
          </PanelBody>
        </Panel>

        <Panel className="min-h-[28rem] xl:min-h-0">
          <PanelHeader
            icon={Logs}
            title={selected ? humanize(selected.key) : "Transcript"}
            actions={
              <>
                <Button
                  variant={follow ? "secondary" : "outline"}
                  size="xs"
                  aria-pressed={follow}
                  onClick={() => setFollow((value) => !value)}
                >
                  Follow tail
                </Button>
                <Button
                  variant="outline"
                  size="icon-xs"
                  className="size-11 sm:size-6"
                  onClick={copyTranscript}
                  disabled={visibleLines.length === 0}
                  aria-label="Copy selected step transcript"
                >
                  <Copy />
                </Button>
              </>
            }
          />
          <PanelBody
            ref={logPane}
            scroll
            onScroll={(event) => {
              const pane = event.currentTarget
              const atBottom = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 24
              if (!atBottom && follow) setFollow(false)
            }}
            className="h-[25rem] min-h-0 bg-surface-sunken p-0 xl:h-auto"
          >
            {selected?.errorMessage && (
              <div className="border-b border-rule-danger bg-wash-danger px-4 py-3 text-xs">
                <p className="font-medium text-destructive">
                  {selected.errorCode || "Step failed"}
                </p>
                <p className="mt-1 break-words text-muted-foreground">{selected.errorMessage}</p>
              </div>
            )}
            {visibleLines.length === 0 ? (
              <div className="flex min-h-48 items-center justify-center px-6 text-center text-xs text-muted-foreground">
                {selected
                  ? `No retained transcript for ${humanize(selected.key)}.`
                  : "The transcript will appear when a step begins."}
              </div>
            ) : (
              <ol
                className="min-w-max py-2 font-mono text-xs leading-5"
                aria-label="Deployment transcript"
              >
                {visibleLines.map((line) => (
                  <li
                    key={line.seq}
                    className={cn(
                      "grid grid-cols-[5.5rem_minmax(0,1fr)] gap-3 px-4",
                      line.stream === "stderr" && "text-destructive",
                    )}
                  >
                    <time className="text-muted-foreground select-none" dateTime={line.ts}>
                      {clock(line.ts)}
                    </time>
                    <span className="break-words whitespace-pre-wrap">{line.text}</span>
                  </li>
                ))}
              </ol>
            )}
          </PanelBody>
        </Panel>
      </div>
      <DeploymentRunLogs projectID={projectID} runID={runID} />
      <DeploymentRunMetrics projectID={projectID} runID={runID} />
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
