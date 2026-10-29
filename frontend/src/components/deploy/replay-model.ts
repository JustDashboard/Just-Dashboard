import type { ReplayDrift, ReplayPlan, ReplayReport } from "@/lib/types"
import { plural } from "@/lib/format"

/**
 * The words and figures of the traffic replay (`deploy/replay.go`), kept out
 * of the components so the settings, the run page and the tests read them
 * the same way.
 */

/** What an absent term runs as (`DefaultReplay…` in `replay.go`). */
export const REPLAY_DEFAULTS = {
  requests: 50,
  maxStatusDrift: 2,
  latencyFactor: 2,
  timeoutSeconds: 60,
} as const

/** The bounds the server holds the terms to (`validateReplayPlan`). */
export const REPLAY_LIMITS = {
  requests: 500,
  maxStatusDrift: 100,
  latencyFactor: 10,
  timeoutSeconds: 300,
  paths: 32,
  pathLength: 256,
} as const

/** The terms a plan runs with, each absent one at its default. */
export function replayTerms(plan: ReplayPlan | undefined) {
  return {
    requests: plan?.requests || REPLAY_DEFAULTS.requests,
    maxStatusDrift: plan?.maxStatusDrift || REPLAY_DEFAULTS.maxStatusDrift,
    latencyFactor: plan?.latencyFactor || REPLAY_DEFAULTS.latencyFactor,
    timeoutSeconds: plan?.timeoutSeconds || REPLAY_DEFAULTS.timeoutSeconds,
    paths: plan?.paths ?? [],
  }
}

/**
 * The plan's replay as the server stores it: nothing at all when it asks for
 * nothing, so a switch turned on and off again is not an edit
 * (`canonicalReplayPlan`); terms typed while it is off are kept for the day
 * it is turned on.
 */
export function storedReplay(plan: ReplayPlan | undefined): ReplayPlan | undefined {
  if (!plan) return undefined
  const kept: ReplayPlan = { enabled: plan.enabled }
  if (plan.requests) kept.requests = plan.requests
  if (plan.paths?.length) kept.paths = plan.paths
  if (plan.maxStatusDrift) kept.maxStatusDrift = plan.maxStatusDrift
  if (plan.latencyFactor) kept.latencyFactor = plan.latencyFactor
  if (plan.timeoutSeconds) kept.timeoutSeconds = plan.timeoutSeconds
  return Object.keys(kept).length === 1 && !kept.enabled ? undefined : kept
}

/** The allow-list typed one prefix per line: trimmed, each once, in order. */
export function replayPathsOf(text: string) {
  const seen = new Set<string>()
  for (const line of text.split("\n")) {
    const prefix = line.trim()
    if (prefix) seen.add(prefix)
  }
  return [...seen]
}

/** Why the server would refuse the allow-list, or nothing when it would not. */
export function replayPathsRefusal(paths: string[]) {
  if (paths.length > REPLAY_LIMITS.paths)
    return `Keep at most ${REPLAY_LIMITS.paths} path prefixes.`
  for (const prefix of paths) {
    if (
      !prefix.startsWith("/") ||
      prefix.length > REPLAY_LIMITS.pathLength ||
      /[\s?#\u0000-\u001f]/.test(prefix)
    )
      return `${prefix} is not a path prefix: one begins with / and has no space, query or fragment.`
  }
  return undefined
}

/**
 * The Runtime form's draft of the terms: numbers as typed, so a field can be
 * emptied and "0." typed on the way to "0.5", and the allow-list as the text
 * of its lines.
 */
export type ReplayDraft = {
  enabled: boolean
  requests: string
  maxStatusDrift: string
  latencyFactor: string
  timeoutSeconds: string
  paths: string
}

const text = (value: number | undefined) => (value ? String(value) : "")

export function replayDraftOf(plan: ReplayPlan | undefined): ReplayDraft {
  return {
    enabled: plan?.enabled ?? false,
    requests: text(plan?.requests),
    maxStatusDrift: text(plan?.maxStatusDrift),
    latencyFactor: text(plan?.latencyFactor),
    timeoutSeconds: text(plan?.timeoutSeconds),
    paths: (plan?.paths ?? []).join("\n"),
  }
}

/** A term's text as the plan holds it: blank or zero is the term left out, the default. */
function term(value: string) {
  const parsed = Number(value.trim())
  return value.trim() === "" || !Number.isFinite(parsed) || parsed === 0 ? undefined : parsed
}

/** The draft as the plan saves it (`storedReplay`). */
export function replayPlanOf(draft: ReplayDraft): ReplayPlan | undefined {
  return storedReplay({
    enabled: draft.enabled,
    requests: term(draft.requests),
    maxStatusDrift: term(draft.maxStatusDrift),
    latencyFactor: term(draft.latencyFactor),
    timeoutSeconds: term(draft.timeoutSeconds),
    paths: replayPathsOf(draft.paths),
  })
}

/**
 * Why the server would refuse the draft (`validateReplayPlan`), naming the
 * field, or nothing when it would not — said under the field at save rather
 * than clamped away, since a count quietly cut from 900 to 500 is a replay
 * the operator did not ask for.
 */
export function replayRefusal(
  draft: ReplayDraft,
): { field: keyof ReplayDraft; message: string } | undefined {
  const whole = (field: "requests" | "timeoutSeconds", max: number, what: string) => {
    const value = term(draft[field])
    if (draft[field].trim() !== "" && value === undefined && Number(draft[field]) !== 0)
      return { field, message: `${what} is a number.` }
    if (value !== undefined && (!Number.isInteger(value) || value < 1 || value > max))
      return { field, message: `${what} is a whole number from 1 to ${max}.` }
    return undefined
  }
  const share = (
    field: "maxStatusDrift" | "latencyFactor",
    min: number,
    max: number,
    what: string,
  ) => {
    const value = term(draft[field])
    if (draft[field].trim() !== "" && value === undefined && Number(draft[field]) !== 0)
      return { field, message: `${what} is a number.` }
    if (value !== undefined && (value < min || value > max))
      return { field, message: `${what} is between ${min} and ${max}.` }
    return undefined
  }
  const paths = replayPathsRefusal(replayPathsOf(draft.paths))
  return (
    whole("requests", REPLAY_LIMITS.requests, "The count") ??
    share("maxStatusDrift", 0, REPLAY_LIMITS.maxStatusDrift, "The drift allowed") ??
    share("latencyFactor", 1, REPLAY_LIMITS.latencyFactor, "The p95 factor") ??
    whole("timeoutSeconds", REPLAY_LIMITS.timeoutSeconds, "The time limit") ??
    (paths ? { field: "paths", message: paths } : undefined)
  )
}

/** The settings head's line: what the next release is held to. */
export function replayPolicyLine(plan: ReplayPlan | undefined) {
  if (!plan?.enabled) return "Off · releases go live on their health checks alone"
  const terms = replayTerms(plan)
  const scope =
    terms.paths.length > 0 ? ` under ${plural(terms.paths.length, "prefix", "prefixes")}` : ""
  return `On · the last ${terms.requests} requests${scope}, ${terms.maxStatusDrift}% drift, p95 within ${terms.latencyFactor}×`
}

/** "< 1 ms", "12 ms", "1.4 s": a replayed request's time. */
export function replayMs(ms: number | undefined | null) {
  if (ms === undefined || ms === null || !Number.isFinite(ms) || ms < 0) return "—"
  if (ms < 1) return "< 1 ms"
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(1)} s`
}

/**
 * The step's evidence as a report: every replay_traffic step records one,
 * the ones that did not replay with only their reason.
 */
export function replayOf(evidence: Record<string, unknown> | undefined) {
  if (!evidence || !Array.isArray(evidence.drifts) || typeof evidence.replayed !== "boolean")
    return undefined
  return evidence as unknown as ReplayReport
}

/** A drifted request's line, as the record has it: "GET /pricing?tab=2". */
export function driftRequest(drift: ReplayDrift) {
  return `${drift.method} ${drift.path}${drift.query ? `?${drift.query}` : ""}`
}

/** What the candidate said instead: "404", or "no answer (timeout)". */
export function driftAnswer(drift: ReplayDrift) {
  if (drift.candidateStatus > 0) return String(drift.candidateStatus)
  return drift.error === "out_of_time"
    ? "not asked in time"
    : `no answer (${drift.error ?? "none"})`
}

/** The verdict as a tone: a line crossed is the reason the release stopped. */
export function replayTone(report: ReplayReport) {
  return report.verdict.tripped ? "danger" : "success"
}

/**
 * The step row's line while another step is open: how many were replayed,
 * how many drifted, and the two p95s — what the replay is for.
 */
export function replaySummary(report: ReplayReport) {
  if (!report.replayed) return report.reason ? `Not replayed: ${report.reason}` : undefined
  const parts = [
    plural(report.requests, "request"),
    report.drifted === 0 ? "none drifted" : `${report.drifted} drifted`,
  ]
  if (report.latencyJudged && report.candidateP95 !== undefined) {
    parts.push(`p95 ${replayMs(report.candidateP95)} (${replayMs(report.recordedP95)} recorded)`)
  }
  return parts.join(" · ")
}

/** Why the p95 was or was not compared, for the figure's line. */
export function latencyHint(report: ReplayReport) {
  if (report.latencyJudged) return `${replayMs(report.recordedP95)} recorded`
  if (!report.latencyRecorded) return "the ingress records no times"
  return "too few timed requests"
}
