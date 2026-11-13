import { percent } from "@/lib/format"
import { LATENCY_SHARE, latencyWords } from "@/lib/latency-words"
import { latency } from "@/lib/requests"
import type { RollbackPolicy, RollbackReading, RollbackWatch } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"
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
 * "Traffic goes back to it when the 5xx share passes its own by 5 points or
 * 19 in 20 requests take 2× as long as its own did, over at least 20
 * requests."
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
  return `The previous release is kept warm for ${window}. Traffic goes back to it when the 5xx share passes its own by ${trim(policy.errorRateMargin)} points or 19 in 20 requests take ${trim(policy.latencyFactor)}× as long as its own did, over at least ${policy.minRequests} requests.`
}

function trim(value: number) {
  return Number.isInteger(value) ? String(value) : String(Number(value.toFixed(2)))
}

/**
 * What the warm release is doing, under the window's countdown: paused, or —
 * kept running for skew protection — how many of its own visitors' requests
 * it answered since the cutover.
 */
export function warmLine(watch: RollbackWatch) {
  if (!watch.warmRunning) return `release #${watch.warmReleaseNumber} paused`
  const served = watch.warmServed?.requests ?? 0
  return `release #${watch.warmReleaseNumber} answered ${served.toLocaleString()} of its visitors' ${served === 1 ? "request" : "requests"}`
}

/** Who ended a window that rolled back: the watch itself, or a person. */
export function rolledBackBy(watch: RollbackWatch) {
  return watch.endMessage?.includes("Rolled back instantly by")
    ? "Rolled back by hand"
    : "Rolled back automatically"
}

/**
 * The window as the Overview's blocks open (§16): the reading as one sentence,
 * what it was judged as under it, and the state for the dot on the mark.
 *
 * The sentence is the new release's own figures, said the way a person would
 * ask — how much of it fails, how long 19 in 20 take — and the previous
 * release's stand beside them in the picture under it. The judgement is the
 * server's own sentence wherever that says something the figures do not; a
 * reading inside the baseline is said here, because the server's sentence for
 * it only repeats the figures.
 */
export function watchLead(watch: RollbackWatch): { title: string; hint?: string; tone: DotTone } {
  const verdict = watch.verdict
  if (watch.state === "rolled_back")
    return { title: rolledBackBy(watch), hint: watch.endMessage, tone: "danger" }
  if (watch.state === "rolling_back")
    return {
      title: `Traffic is going back to release #${watch.warmReleaseNumber}`,
      hint: verdict?.reason,
      tone: "danger",
    }
  if (watch.state === "closing")
    return {
      title: `Release #${watch.releaseNumber} stays live; release #${watch.warmReleaseNumber} is being removed`,
      tone: "running",
    }
  // No route, or no record to read: the server's sentence is the whole of it.
  if (verdict && (verdict.code === "no_route" || verdict.code === "no_record"))
    return { title: verdict.reason, tone: "unknown" }

  const observed = watch.observed
  const manual = watch.policy.automatic
    ? undefined
    : "Nothing is rolled back automatically in this environment."
  if (!observed || observed.requests === 0)
    return {
      title: `Release #${watch.releaseNumber} has answered no requests yet`,
      hint: join(`${watch.policy.minRequests} requests decide.`, manual),
      tone: "unknown",
    }

  const failing =
    observed.failures === 0
      ? `has failed none of its ${observed.requests.toLocaleString()} ${observed.requests === 1 ? "request" : "requests"}`
      : `fails ${percent(shareOf(observed), 1)} of its requests`
  const speed =
    observed.p95 === undefined
      ? ""
      : `; ${LATENCY_SHARE.p95} finish inside ${latency(observed.p95)}`
  const worst = [shareTone(watch), latencyTone(watch)]
  return {
    title: `Release #${watch.releaseNumber} ${failing}${speed}`,
    hint: join(judgement(watch), manual),
    tone: worst.includes("danger")
      ? "danger"
      : worst.includes("warning")
        ? "warning"
        : verdict?.judged
          ? "running"
          : "unknown",
  }
}

/** What the reading was judged as, in a sentence, or nothing before a verdict. */
function judgement(watch: RollbackWatch) {
  const verdict = watch.verdict
  if (!verdict) return undefined
  const before = `release #${watch.warmReleaseNumber}`
  if (verdict.code === "latency" && watch.observed?.p95 !== undefined) {
    const was = trustedBaseline(watch)?.p95
    return `${LATENCY_SHARE.p95} requests now take up to ${latency(watch.observed.p95)}${
      was === undefined ? "" : `, from ${latency(was)} on ${before}`
    }.`
  }
  if (verdict.code !== "within_baseline") return verdict.reason
  if (verdict.latencyJudged) return `No worse than ${before} was.`
  return trustedBaseline(watch)
    ? `Failing no more than ${before} did. This ingress records no durations, so its speed is not judged.`
    : `Inside the margin. ${before[0].toUpperCase()}${before.slice(1)} served too little to compare with, so its speed is not judged.`
}

const join = (...parts: (string | undefined)[]) => parts.filter(Boolean).join(" ") || undefined

/**
 * One release's row of the picture: how much of it failed and how long 19 in
 * 20 took, in the sentence's own words, or what there is to say with nothing
 * to read.
 */
export function readingLine(reading: RollbackReading | undefined) {
  if (!reading || reading.requests === 0) return "no requests"
  const failed = reading.failures === 0 ? "none failed" : `${percent(shareOf(reading), 1)} failed`
  return reading.p95 === undefined
    ? failed
    : `${failed} · ${latencyWords("p95", latency(reading.p95))}`
}

/**
 * Which proxy wrote the record the window is read from, for the block's mark.
 * The watch does not carry the driver, but its readings do say it: an nginx
 * record holds no durations, so a release that answered requests with no time
 * among them was read from nginx. The Docker ingress is Caddy, and the answer
 * with nothing read yet.
 */
export function watchProxy(watch: RollbackWatch): "caddy" | "nginx" {
  const untimed = (reading: RollbackReading | undefined) =>
    Boolean(reading && reading.requests > 0 && reading.p95 === undefined)
  return untimed(watch.observed) || untimed(watch.baseline) ? "nginx" : "caddy"
}

/** The window's own line: how long it has left and what the warm release is doing. */
export function windowLine(watch: RollbackWatch, remaining: number) {
  const warm = warmLine(watch)
  return watch.state === "watching" ? `${clock(remaining)} left to decide · ${warm}` : warm
}

/** Skew protection in one sentence, as the settings say it back. */
export function skewLine(on: boolean, policy: RollbackPolicy) {
  if (!on) return undefined
  if (!policy.keepWarm)
    return "A visitor stays on the release that answered them only while it drains, since it is not kept warm."
  const minutes = Math.round(policy.windowSeconds / 60)
  return `A visitor stays on the release that answered them for up to ${minutes} ${minutes === 1 ? "minute" : "minutes"} after a cutover.`
}
