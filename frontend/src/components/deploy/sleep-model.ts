import type { SleepPlan, SleepSpan, SleepStatus } from "@/lib/types"
import { plural } from "@/lib/format"

/**
 * The words and figures of scale-to-zero (`deploy/sleep.go`), kept out of the
 * components so the settings, the overview and the tests read them the same
 * way.
 */

/** What an absent term runs as (`DefaultSleep…` in `sleep.go`). */
export const SLEEP_DEFAULTS = { idleMinutes: 15, wakeTimeoutSeconds: 60 } as const

/** The bounds the server holds the terms to (`validateSleepPlan`). */
export const SLEEP_LIMITS = {
  minIdleMinutes: 5,
  maxIdleMinutes: 1440,
  minWakeTimeoutSeconds: 5,
  maxWakeTimeoutSeconds: 600,
  /** The longest a visitor's request is held before the waking page answers. */
  holdSeconds: 30,
} as const

/** The terms a plan runs with, each absent one at its default. */
export function sleepTerms(plan: SleepPlan | undefined) {
  return {
    idleMinutes: plan?.idleMinutes || SLEEP_DEFAULTS.idleMinutes,
    wakeTimeoutSeconds: plan?.wakeTimeoutSeconds || SLEEP_DEFAULTS.wakeTimeoutSeconds,
  }
}

/**
 * The plan's sleep as the server stores it: nothing when it asks for
 * nothing, so a switch turned on and off again is not an edit
 * (`canonicalSleepPlan`); terms typed while it is off are kept.
 */
export function storedSleep(plan: SleepPlan | undefined): SleepPlan | undefined {
  if (!plan) return undefined
  const kept: SleepPlan = { enabled: plan.enabled }
  if (plan.idleMinutes) kept.idleMinutes = plan.idleMinutes
  if (plan.wakeTimeoutSeconds) kept.wakeTimeoutSeconds = plan.wakeTimeoutSeconds
  return Object.keys(kept).length === 1 && !kept.enabled ? undefined : kept
}

/** The Runtime form's draft of the terms: the numbers as typed. */
export type SleepDraft = {
  enabled: boolean
  idleMinutes: string
  wakeTimeoutSeconds: string
}

const text = (value: number | undefined) => (value ? String(value) : "")

export function sleepDraftOf(plan: SleepPlan | undefined): SleepDraft {
  return {
    enabled: plan?.enabled ?? false,
    idleMinutes: text(plan?.idleMinutes),
    wakeTimeoutSeconds: text(plan?.wakeTimeoutSeconds),
  }
}

/** A term's text as the plan holds it: blank or zero is the default. */
function term(value: string) {
  const parsed = Number(value.trim())
  return value.trim() === "" || !Number.isFinite(parsed) || parsed === 0 ? undefined : parsed
}

/** The draft as the plan saves it (`storedSleep`). */
export function sleepPlanOf(draft: SleepDraft): SleepPlan | undefined {
  return storedSleep({
    enabled: draft.enabled,
    idleMinutes: term(draft.idleMinutes),
    wakeTimeoutSeconds: term(draft.wakeTimeoutSeconds),
  })
}

/**
 * Why the server would refuse the terms, naming the field, or nothing — said
 * under the field at save rather than clamped away.
 */
export function sleepRefusal(
  draft: SleepDraft,
): { field: keyof SleepDraft; message: string } | undefined {
  const whole = (
    field: "idleMinutes" | "wakeTimeoutSeconds",
    min: number,
    max: number,
    what: string,
  ) => {
    const value = term(draft[field])
    if (draft[field].trim() !== "" && value === undefined && Number(draft[field]) !== 0)
      return { field, message: `${what} is a number.` }
    if (value !== undefined && (!Number.isInteger(value) || value < min || value > max))
      return { field, message: `${what} is a whole number from ${min} to ${max}.` }
    return undefined
  }
  return (
    whole(
      "idleMinutes",
      SLEEP_LIMITS.minIdleMinutes,
      SLEEP_LIMITS.maxIdleMinutes,
      "The idle time",
    ) ??
    whole(
      "wakeTimeoutSeconds",
      SLEEP_LIMITS.minWakeTimeoutSeconds,
      SLEEP_LIMITS.maxWakeTimeoutSeconds,
      "The wake limit",
    )
  )
}

/** "15 min", "2 h", "1 h 30 min", "1 day". */
export function idleSpan(minutes: number) {
  if (minutes >= 1440 && minutes % 1440 === 0) return plural(minutes / 1440, "day")
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest ? `${hours} h ${rest} min` : `${hours} h`
}

/** How long a visitor waits at most before the waking page: the wake, up to the hold. */
export function holdSeconds(plan: SleepPlan | undefined) {
  return Math.min(sleepTerms(plan).wakeTimeoutSeconds, SLEEP_LIMITS.holdSeconds)
}

/** The settings head's line: what the next release does when nobody visits. */
export function sleepPolicyLine(plan: SleepPlan | undefined) {
  if (!plan?.enabled) return "Off · the container runs whether or not anyone visits"
  const terms = sleepTerms(plan)
  return `On · stopped after ${idleSpan(terms.idleMinutes)} without a request, woken within ${terms.wakeTimeoutSeconds} s`
}

/** "3 h 20 min", "45 s": a stretch's length. */
export function spanLength(seconds: number) {
  if (!Number.isFinite(seconds) || seconds < 0) return "—"
  if (seconds < 60) return `${Math.max(1, Math.round(seconds))} s`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  if (hours < 48) return rest ? `${hours} h ${rest} min` : `${hours} h`
  return plural(Math.round(hours / 24), "day")
}

const TRIGGERS: Record<string, string> = {
  request: "a request",
  manual: "Wake now",
  run: "a run",
  repair: "a start outside a wake",
}

/** What woke a stretch, in a few words: "a request to shop.example.com". */
export function wakeCause(span: SleepSpan) {
  const trigger = TRIGGERS[span.wakeTrigger ?? ""] ?? "the dashboard"
  if (span.wakeTrigger === "request" && span.wakeDetail) return `a request to ${span.wakeDetail}`
  if (span.wakeTrigger === "manual" && span.wakeDetail) return `Wake now, by ${span.wakeDetail}`
  if (span.wakeTrigger === "run" && span.wakeDetail) return span.wakeDetail
  return trigger
}

/** The last wake's line for the overview: by what, and how long it took. */
export function lastWakeLine(status: SleepStatus) {
  const woken = status.recent.find((span) => span.wokeAt)
  if (!woken) return undefined
  const took = woken.wakeSeconds ? ` in ${woken.wakeSeconds.toFixed(1)} s` : ""
  if (woken.wakeError) return `Last woken by ${wakeCause(woken)}${took}, ${woken.wakeError}`
  return `Last woken by ${wakeCause(woken)}${took}`
}

/**
 * The sleeping block's sentence and its detail, as the Overview's blocks open
 * (§16): that it is asleep on purpose and for how long, then why, what the
 * next visitor meets and what woke it last. The wake limit is the plan's own
 * and is said only in Runtime settings: here it would be a default repeated.
 */
export function sleepLead(status: SleepStatus, waking: boolean, now = Date.now()) {
  const since = status.since ? Date.parse(status.since) : NaN
  const title = waking
    ? "Waking up: its container is starting"
    : Number.isNaN(since)
      ? "Asleep by design"
      : `Asleep by design for ${spanLength((now - since) / 1000)}`
  const last = lastWakeLine(status)
  const hint = [
    status.reason,
    `The next request to its domain starts it and is answered by it, held for up to ${status.holdSeconds} s.`,
    last && `${last}.`,
  ]
    .filter(Boolean)
    .join(" ")
  return { title, hint }
}

const WEEK_MS = 7 * 24 * 3600 * 1000

/**
 * The last seven days as one strip: where each stretch asleep begins and how
 * wide it is, 0–1 of the week, oldest first. A stretch still open runs to now,
 * and one that began before the week is cut at its edge.
 */
export function sleepWeek(status: SleepStatus, now = Date.now()) {
  const from = now - WEEK_MS
  return status.recent
    .map((span) => {
      const slept = Math.max(from, Date.parse(span.sleptAt))
      const woke = Math.min(now, span.wokeAt ? Date.parse(span.wokeAt) : now)
      return { key: span.id, left: (slept - from) / WEEK_MS, width: (woke - slept) / WEEK_MS }
    })
    .filter((stretch) => Number.isFinite(stretch.left) && stretch.width > 0)
    .sort((a, b) => a.left - b.left)
}

/** Whether the overview draws the sleeping panel: asleep, or waking up. */
export function sleepShown(status: SleepStatus | undefined) {
  return status?.state === "asleep" || status?.state === "waking"
}

/** The share of the last seven days spent asleep, 0–1. */
export function asleepShare(status: SleepStatus) {
  return Math.min(1, Math.max(0, status.asleepSeconds / (7 * 24 * 3600)))
}
