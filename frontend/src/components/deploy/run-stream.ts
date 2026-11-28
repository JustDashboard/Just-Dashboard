"use client"

import { useRef, useState } from "react"
import type { DeploymentEngineRun, DeploymentRunEvent, DeploymentRunSnapshot } from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { isActiveRun } from "@/components/deploy/vocabulary"
import { applyRunEvent } from "@/components/deploy/run-events"
import {
  EMPTY_TRANSCRIPT,
  appendTranscript,
  clearTranscript,
  type BuildLogEvent,
  type TranscriptBuffer,
} from "@/components/deploy/transcript-buffer"

/**
 * A run's stream, read: its snapshot as the events move it, and the build
 * transcript as lines. The run page reads it with everything else it follows
 * about a run (`useRunStream`); the project's Builds view reads only the
 * transcript, for the run the reader picked (`useRunTranscript`). One reader
 * for both, so the two consoles cannot come to disagree about the same run.
 */

/** The most transcript events a reader holds; past it the oldest go, and the console says so. */
const TRANSCRIPT_CAP = 5000

/**
 * One run's stream as the server sends it: a snapshot, the retained events
 * after it, then live ones until the run ends and the server closes. Resumes
 * from the last sequence on a reconnect, and stops reconnecting once a
 * finished run's stream has closed. Mount it per run (a `key`, or a page per
 * run): a different run is a different stream, and nothing of the last one
 * carries.
 *
 * `initial` is the run as a page already read it, which the first events
 * move on from when they arrive before the stream's own snapshot.
 */
export function useRunStream(
  projectId: number,
  runId: number,
  { enabled = true, initial }: { enabled?: boolean; initial?: DeploymentRunSnapshot } = {},
) {
  const [snapshot, setSnapshot] = useState<DeploymentRunSnapshot>()
  const [transcript, setTranscript] = useState<TranscriptBuffer>(EMPTY_TRANSCRIPT)
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
    // A resync replaces state rather than appending to it: the retained
    // transcript may have been compacted since our last sequence, so the
    // lines we were holding are no longer a prefix of the truth.
    const resync = incoming.find((event) => event.type === "resync")
    if (resync && isSnapshot(resync.data.snapshot)) {
      setSnapshot(resync.data.snapshot)
      setTranscript(clearTranscript)
    }
    setSnapshot((current) =>
      incoming.reduce((next, event) => applyRunEvent(next, event), current ?? initial),
    )
    const lines = incoming.flatMap(logEvent)
    if (lines.length > 0) {
      // Appended, not read again: the lines already held keep their rows.
      setTranscript((current) => appendTranscript(current, lines, TRANSCRIPT_CAP))
    }
  }

  const socket = useSocket(`/deploy/${projectId}/runs/${runId}/stream`, {
    enabled: enabled && !complete,
    query: () => ({ after: lastSeq.current }),
    onMessage,
    onClose: () => {
      if (state.current && !isActiveRun(state.current)) setComplete(true)
    },
  })

  return {
    snapshot,
    /** For a verb that changed the run and answered with it, ahead of the stream saying so. */
    setSnapshot,
    /** The transcript as lines with their counts, kept up to date as events land. */
    transcript,
    capped: transcript.events.length >= TRANSCRIPT_CAP,
    socket: socket.state,
  }
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
