import type {
  DeploymentCanary,
  DeploymentCanaryTerms,
  DeploymentConfiguration,
  DeploymentStrategy,
  RollbackReading,
} from "@/lib/types"
import type { Tone } from "@/components/tone"

/**
 * The words and figures of a canary release (`deploy/canary.go`): the
 * strategy's terms as the settings edit them, and a trial as the run page
 * reads it, the candidate beside the live release. Kept out of the components
 * so the settings, the run page and the tests read them the same way.
 */

/** The bounds `validateCanary` holds a plan to. */
export const CANARY_PERCENT = { min: 1, max: 50 }
export const CANARY_SECONDS = { min: 60, max: 3600 }

/** What choosing the canary strategy starts from: a tenth, for ten minutes, promoted when it does well. */
export const DEFAULT_CANARY_TERMS: DeploymentCanaryTerms = {
  percent: 10,
  durationSeconds: 600,
  autoPromote: true,
}

type RuntimePlan = DeploymentConfiguration["runtime"]

/**
 * The plan with a strategy chosen: a canary gets its terms, kept if it had
 * them; any other strategy leaves them out, since the server refuses terms
 * beside a strategy they do not belong to.
 */
export function withStrategy<T extends Pick<RuntimePlan, "strategy" | "canary">>(
  runtime: T,
  strategy: DeploymentStrategy,
): T {
  const next = { ...runtime, strategy }
  if (strategy === "canary") next.canary = runtime.canary ?? { ...DEFAULT_CANARY_TERMS }
  else delete next.canary
  return next
}

/** "10 minutes", "a minute", "90 seconds". */
export function windowWords(seconds: number) {
  if (seconds % 60 !== 0) return `${seconds} seconds`
  return seconds === 60 ? "a minute" : `${seconds / 60} minutes`
}

/** Whole seconds left in the trial's window; zero once it has passed. */
export function canaryRemaining(canary: DeploymentCanary, now = Date.now()) {
  return Math.max(0, Math.ceil((Date.parse(canary.endsAt) - now) / 1000))
}

/** How far through its window the trial is, 0–1. */
export function canaryProgress(canary: DeploymentCanary, now = Date.now()) {
  const start = Date.parse(canary.startedAt)
  const end = Date.parse(canary.endsAt)
  if (!(end > start)) return 1
  return Math.min(1, Math.max(0, (now - start) / (end - start)))
}

export function canaryOpen(canary: DeploymentCanary | null | undefined) {
  return Boolean(canary && (canary.state === "running" || canary.state === "holding"))
}

/** The panel's title: what the trial is doing. */
export function canaryTitle(canary: DeploymentCanary) {
  switch (canary.state) {
    case "promoted":
      return `Release #${canary.releaseNumber} promoted from its canary`
    case "aborted":
      return `Release #${canary.releaseNumber}'s canary aborted`
    case "holding":
      return `Release #${canary.releaseNumber} waits to be promoted`
    default:
      if (canary.decision === "promote") return `Promoting release #${canary.releaseNumber}`
      if (canary.decision === "abort") return `Aborting release #${canary.releaseNumber}`
      return `Release #${canary.releaseNumber} on ${canary.percent}% of the requests`
  }
}

/** A reading's 5xx share as a percentage, or undefined with nothing to read. */
export function share(reading: RollbackReading | undefined) {
  if (!reading || reading.requests === 0) return undefined
  return reading.errorRate * 100
}

/**
 * The candidate's 5xx tone against the live release's line, the one the
 * server aborts on: red past it with enough requests and failures to decide,
 * amber past it short of those or past half the margin.
 */
export function candidateShareTone(canary: DeploymentCanary): Tone {
  const candidate = canary.candidate
  const now = share(candidate)
  if (!candidate || now === undefined) return "default"
  const live =
    canary.live && canary.live.requests >= canary.policy.minRequests ? share(canary.live) : 0
  const line = (live ?? 0) + canary.policy.errorRateMargin
  const decides = candidate.requests >= canary.policy.minRequests && candidate.failures >= 5
  if (now > line) return decides ? "danger" : "warning"
  if (now > (live ?? 0) + canary.policy.errorRateMargin / 2) return "warning"
  return "default"
}

/** The candidate's p95 tone against the live release's, on the latency rule. */
export function candidateLatencyTone(canary: DeploymentCanary): Tone {
  const was = canary.live?.p95
  const is = canary.candidate?.p95
  if (was === undefined || is === undefined || !canary.verdict?.latencyJudged) return "default"
  if (is > was * canary.policy.latencyFactor && is - was >= 250) return "danger"
  if (is > was * (1 + (canary.policy.latencyFactor - 1) / 2) && is - was >= 125) return "warning"
  return "default"
}

/** Why a trial ended, in the words of its end reason. */
export function canaryEndLabel(reason: string | undefined) {
  switch (reason) {
    case "passed":
      return "Promoted on its figures"
    case "operator_promoted":
      return "Promoted by hand"
    case "failed":
      return "Aborted on its figures"
    case "operator_aborted":
      return "Aborted by hand"
    case "cancelled":
      return "Run cancelled"
    case "unanswered":
      return "Nobody promoted it"
    case "candidate_lost":
      return "Candidate stopped"
    case "route_failed":
      return "Route not written"
    default:
      return reason ?? ""
  }
}
