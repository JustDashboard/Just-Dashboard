import type { RollbackPolicy, RollbackReading, RollbackWatch } from "@/lib/types"
import type { Tone } from "@/components/tone"

/**
 * The words and figures of a warm-release window (`deploy/rollback_watch.go`),
 * kept out of the components so the page, the settings and the tests read
 * them the same way.
 */

/** What an environment nobody configured gets: `DefaultRollbackPolicy`. */
export const DEFAULT_ROLLBACK_POLICY: RollbackPolicy = {
  keepWarm: true,
  windowSeconds: 600,
  automatic: true,
  errorRateMargin: 5,
  latencyFactor: 2,
  minRequests: 20,
}

/** A rolled-back window stays on the overview this long after it ended. */
export const ROLLED_BACK_SHOWN_MS = 60 * 60 * 1000

export function watchOpen(watch: RollbackWatch | null | undefined): boolean {
  return Boolean(
    watch &&
    (watch.state === "watching" || watch.state === "rolling_back" || watch.state === "closing"),
  )
}

/**
 * Whether the overview draws the window: while it is open, and for an hour
 * after one ended in a rollback, which is the news a reader arriving later
 * most needs. A window that closed clean is the expected answer and is not
 * drawn at all.
 */
export function watchShown(watch: RollbackWatch | null | undefined, now = Date.now()) {
  if (watchOpen(watch)) return true
  if (!watch || watch.state !== "rolled_back" || !watch.endedAt) return false
  return now - Date.parse(watch.endedAt) < ROLLED_BACK_SHOWN_MS
}

/** Whole seconds left in the window; zero once it has passed. */
export function windowRemaining(watch: RollbackWatch, now = Date.now()) {
  return Math.max(0, Math.ceil((Date.parse(watch.expiresAt) - now) / 1000))
}

/** "7:05", the window's countdown. */
export function clock(seconds: number) {
  const minutes = Math.floor(seconds / 60)
  return `${minutes}:${String(seconds % 60).padStart(2, "0")}`
}

/** A reading's 5xx share as a percentage, or undefined with nothing to read. */
export function shareOf(reading: RollbackReading | undefined) {
  if (!reading || reading.requests === 0) return undefined
  return reading.errorRate * 100
}

/** The share the previous release is compared at, as the server trusts it. */
export function trustedBaseline(watch: RollbackWatch) {
  const baseline = watch.baseline
  if (!baseline || baseline.requests < watch.policy.minRequests) return undefined
  return baseline
}

/**
 * The 5xx share's tone against its own line: red past it, amber past half
 * the margin, plain otherwise. The line is the previous release's share plus
 * the margin, or the margin alone when the previous release served too little
 * to be trusted — the same line the server judges against.
 */
export function shareTone(watch: RollbackWatch): Tone {
  const observed = watch.observed
  const now = shareOf(observed)
  if (!observed || now === undefined) return "default"
  const before = shareOf(trustedBaseline(watch)) ?? 0
  // Red is what the server would roll back for, which also needs enough
  // requests and five failing answers; past the line short of those, amber.
  const decides = observed.requests >= watch.policy.minRequests && observed.failures >= 5
  if (now > before + watch.policy.errorRateMargin) return decides ? "danger" : "warning"
  if (now > before + watch.policy.errorRateMargin / 2) return "warning"
  return "default"
}

/** The p95's tone: red past the factor, amber past halfway to it. */
export function latencyTone(watch: RollbackWatch): Tone {
  const was = trustedBaseline(watch)?.p95
  const is = watch.observed?.p95
  if (was === undefined || is === undefined || !watch.verdict?.latencyJudged) return "default"
  if (is > was * watch.policy.latencyFactor && is - was >= 250) return "danger"
  if (is > was * (1 + (watch.policy.latencyFactor - 1) / 2) && is - was >= 125) return "warning"
  return "default"
}

/** The panel's title, which is what the window is doing. */
export function watchTitle(watch: RollbackWatch) {
  switch (watch.state) {
    case "rolled_back":
      return `Release #${watch.releaseNumber} rolled back`
    case "rolling_back":
      return `Rolling back to release #${watch.warmReleaseNumber}`
    case "closing":
      return `Release #${watch.releaseNumber} kept`
    default:
      return `Release #${watch.releaseNumber} on watch`
  }
}

/**
 * The terms in one sentence, as the settings and the panel's tip say them:
 * "Rolls back when 5xx passes the previous release's by 5 points or p95
 * reaches 2× it, over at least 20 requests."
 */
export function policyLine(policy: RollbackPolicy) {
  if (!policy.keepWarm) return "The previous release is removed as soon as a new one is live."
  const minutes = Math.round(policy.windowSeconds / 60)
  const window =
    policy.windowSeconds % 60 === 0
      ? `${minutes} ${minutes === 1 ? "minute" : "minutes"}`
      : `${policy.windowSeconds} seconds`
  if (!policy.automatic)
    return `The previous release is kept warm for ${window}, to switch back to by hand.`
  return `The previous release is kept warm for ${window}. Traffic goes back to it when the 5xx share passes its own by ${trim(policy.errorRateMargin)} points or the p95 reaches ${trim(policy.latencyFactor)}× its own, over at least ${policy.minRequests} requests.`
}

function trim(value: number) {
  return Number.isInteger(value) ? String(value) : String(Number(value.toFixed(2)))
}

/** Why a closed window closed, in the words of its end reason. */
export function endLabel(reason: string | undefined) {
  switch (reason) {
    case "rolled_back":
      return "Rolled back"
    case "window_passed":
      return "Window passed"
    case "superseded":
      return "Another run started"
    case "kept":
      return "Kept"
    case "memory":
      return "Host memory low"
    case "warm_lost":
      return "Warm container lost"
    case "rollback_failed":
      return "Rollback failed"
    default:
      return reason ?? ""
  }
}
