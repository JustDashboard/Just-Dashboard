"use client"

import { useMemo, useRef, useState } from "react"
import type {
  DeploymentEngineRun,
  DeploymentRunEvent,
  DeploymentRunSnapshot,
  DeploymentStepState,
} from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { isActiveRun, latestAttempts } from "@/components/deploy/vocabulary"
import {
  consoleRows,
  transcriptSummary,
  type BuildLogEvent,
} from "@/components/deploy/build-console"

/**
 * A run's stream, read: its snapshot as the events move it, and the build
 * transcript as lines. The run page reads it with everything else it follows
 * about a run; the project's Builds view reads only this, for the run the
 * reader picked, through `useRunTranscript`.
 */

/** The most transcript events a reader holds; past it the oldest go, and the console says so. */
export const TRANSCRIPT_CAP = 5000

/**
 * One run's transcript as the stream sends it: a snapshot, the retained
 * events after it, then live ones until the run ends and the server closes.
 * Resumes from the last sequence on a reconnect, and stops reconnecting once
 * a finished run's stream has closed. Mount it per run (a `key`): a
 * different run is a different stream, and nothing of the last one carries.
 */
export function useRunTranscript(projectId: number, runId: number) {
  const [snapshot, setSnapshot] = useState<DeploymentRunSnapshot>()
  const [events, setEvents] = useState<BuildLogEvent[]>([])
  const [complete, setComplete] = useState(false)
  const lastSeq = useRef(0)
  const state = useRef<DeploymentEngineRun["state"] | undefined>(undefined)

  const onMessage = (envelope: Envelope) => {
    if (envelope.type === "snapshot" && isSnapshot(envelope.data)) {
      state.current = envelope.data.run.state
      setSnapshot(envelope.data)
      return
    }
    if (envelope.type !== "events" || !Array.isArray(envelope.data)) return
    const incoming = envelope.data.filter(isRunEvent)
    if (incoming.length === 0) return
    for (const event of incoming) {
      lastSeq.current = Math.max(lastSeq.current, event.seq)
      if (event.type === "run.state" && typeof event.data.state === "string")
        state.current = event.data.state as DeploymentEngineRun["state"]
    }
    const resync = incoming.find((event) => event.type === "resync")
    if (resync && isSnapshot(resync.data.snapshot)) {
      setSnapshot(resync.data.snapshot)
      setEvents([])
    }
    setSnapshot((current) => incoming.reduce((next, event) => applyEvent(next, event), current))
    const lines = incoming.flatMap(logEvent)
    if (lines.length > 0) {
      setEvents((current) => dedupeEvents([...current, ...lines]).slice(-TRANSCRIPT_CAP))
    }
  }

  const socket = useSocket(`/deploy/${projectId}/runs/${runId}/stream`, {
    enabled: !complete,
    query: () => ({ after: lastSeq.current }),
    onMessage,
    onClose: () => {
      if (state.current && !isActiveRun(state.current)) setComplete(true)
    },
  })

  const rows = useMemo(() => consoleRows(events), [events])
  const summary = useMemo(() => transcriptSummary(rows), [rows])
  const steps = useMemo(() => latestAttempts(snapshot?.steps ?? []), [snapshot?.steps])
  return {
    snapshot,
    rows,
    summary,
    steps,
    capped: events.length >= TRANSCRIPT_CAP,
    connected: socket.state === "open",
  }
}

export function applyEvent(
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

export function logEvent(event: DeploymentRunEvent): BuildLogEvent[] {
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

export function dedupeEvents(events: BuildLogEvent[]) {
  const bySequence = new Map<number, BuildLogEvent>()
  for (const event of events) bySequence.set(event.seq, event)
  return [...bySequence.values()].sort((a, b) => a.seq - b.seq)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

export function isSnapshot(value: unknown): value is DeploymentRunSnapshot {
  if (!isRecord(value) || !isRecord(value.run) || !Array.isArray(value.steps)) return false
  return typeof value.run.id === "number"
}

export function isRunEvent(value: unknown): value is DeploymentRunEvent {
  if (!isRecord(value) || !isRecord(value.data)) return false
  return typeof value.seq === "number" && typeof value.type === "string"
}
