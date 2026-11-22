import type {
  DeploymentEngineRun,
  DeploymentRunEvent,
  DeploymentRunSnapshot,
  DeploymentStep,
  DeploymentStepState,
} from "@/lib/types"

/**
 * A run's snapshot moved on by one of its stream's events. The page reads the
 * snapshot once and follows the events after it, so everything it draws from
 * then on — a step's state and its clock, the run's state and how long it has
 * taken — is what the events say.
 *
 * The engine sends the times it records (`startedAt`/`endedAt` on a step,
 * `claimedAt`/`endedAt` on the run). An engine from before it did sends none,
 * and the event's own instant stands in: it is written in the transaction that
 * moved the state, so it is the same second.
 */
export function applyRunEvent(
  snapshot: DeploymentRunSnapshot | undefined,
  event: DeploymentRunEvent,
): DeploymentRunSnapshot | undefined {
  if (!snapshot || event.type === "resync") return snapshot
  if (event.type === "run.state" && typeof event.data.state === "string") {
    const state = event.data.state as DeploymentEngineRun["state"]
    const run = snapshot.run
    return {
      ...snapshot,
      run: {
        ...run,
        state,
        terminalCode: typeof event.data.code === "string" ? event.data.code : run.terminalCode,
        terminalReason:
          typeof event.data.reason === "string" ? event.data.reason : run.terminalReason,
        claimedAt:
          instant(event.data.claimedAt) ??
          run.claimedAt ??
          (state === "preparing" ? event.ts : undefined),
        endedAt:
          instant(event.data.endedAt) ?? run.endedAt ?? (terminalRun(state) ? event.ts : undefined),
      },
    }
  }
  if (event.type !== "step.state") return snapshot
  const key = typeof event.data.key === "string" ? event.data.key : ""
  const attempt = typeof event.data.attempt === "number" ? event.data.attempt : 1
  const state =
    typeof event.data.state === "string" ? (event.data.state as DeploymentStepState) : undefined
  if (!state) return snapshot
  const errorCode = typeof event.data.errorCode === "string" ? event.data.errorCode : undefined
  const errorMessage =
    typeof event.data.errorMessage === "string" ? event.data.errorMessage : undefined
  let found = false
  const steps = snapshot.steps.map((step) => {
    if (step.id !== event.stepId) return step
    found = true
    return {
      ...step,
      state,
      attempt,
      ...stepTimes(step, state, event),
      evidence: isRecord(event.data.evidence) ? event.data.evidence : step.evidence,
      errorCode: errorCode ?? step.errorCode,
      errorMessage: errorMessage ?? step.errorMessage,
      lastSeq: event.seq,
    }
  })
  if (!found && key) {
    // A new attempt: the step's place and limits carry over from the one
    // before it, its times and its record do not.
    const previous = [...snapshot.steps].reverse().find((step) => step.key === key)
    const base: DeploymentStep = previous ?? {
      id: event.stepId ?? event.seq,
      runId: event.runId,
      key,
      ordinal: snapshot.steps.length,
      state,
      attempt,
      timeoutSeconds: 0,
      evidence: {},
      lastSeq: event.seq,
    }
    const fresh = { ...base, startedAt: undefined, endedAt: undefined }
    steps.push({
      ...fresh,
      id: event.stepId ?? event.seq,
      state,
      attempt,
      ...stepTimes(fresh, state, event),
      evidence: isRecord(event.data.evidence) ? event.data.evidence : {},
      errorCode,
      errorMessage,
      lastSeq: event.seq,
    })
  }
  return { ...snapshot, steps }
}

/** A step's times after an event moved it to `state`. */
function stepTimes(step: DeploymentStep, state: DeploymentStepState, event: DeploymentRunEvent) {
  const startedAt =
    instant(event.data.startedAt) ?? step.startedAt ?? (state === "running" ? event.ts : undefined)
  const endedAt =
    instant(event.data.endedAt) ?? step.endedAt ?? (terminalStep(state) ? event.ts : undefined)
  return { startedAt, endedAt }
}

const TERMINAL_RUN = new Set<string>([
  "succeeded",
  "failed",
  "cancelled",
  "rolled_back",
  "superseded",
])
const TERMINAL_STEP = new Set<string>([
  "passed",
  "warning",
  "failed",
  "skipped",
  "cancelled",
  "unavailable",
])

function terminalRun(state: string) {
  return TERMINAL_RUN.has(state)
}

// The engine's `stepAttemptTerminal`: the states an attempt records its end in.
function terminalStep(state: string) {
  return TERMINAL_STEP.has(state)
}

function instant(value: unknown) {
  return typeof value === "string" && !Number.isNaN(Date.parse(value)) ? value : undefined
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
