/**
 * The limits and defaults the planner and the runtime owner apply to a
 * release's shutdown (`planning_model.go` validation, `runtime_owner.go`).
 * Mirrored here rather than guessed, so a number the server would refuse is
 * clamped at the field and the head names what an empty field means.
 */

/** The signals the planner accepts; anything else is refused at save. */
export const STOP_SIGNALS = ["SIGTERM", "SIGINT", "SIGQUIT", "SIGHUP"] as const

/** What an empty stop signal runs as: the container is always created with one. */
export const DEFAULT_STOP_SIGNAL = "SIGTERM"

/** What an empty grace period waits before the container is killed. */
export const DEFAULT_GRACE_SECONDS = 10

export const MAX_SHUTDOWN_SECONDS = 300

/**
 * A seconds field's text as the plan holds it: blank, zero and anything that
 * is not a number are the field left out — which the server reads as its
 * default — and the rest is a whole number inside the planner's range.
 */
export function shutdownSeconds(text: string): number | undefined {
  const seconds = Math.min(MAX_SHUTDOWN_SECONDS, Math.max(0, Math.trunc(Number(text) || 0)))
  return seconds || undefined
}

/**
 * The section head's line: the signal, how long the container has to answer
 * it, and how long the previous release is kept after a deployment. A plan
 * that sets none of them says it runs the defaults, and what those are.
 */
export function shutdownSummary(plan: {
  stopSignal?: string
  gracePeriodSeconds?: number
  drainSeconds?: number
}): string {
  const grace = plan.gracePeriodSeconds || DEFAULT_GRACE_SECONDS
  const parts = [plan.stopSignal || DEFAULT_STOP_SIGNAL, `${grace} s grace`]
  if (plan.drainSeconds) parts.push(`${plan.drainSeconds} s drain`)
  const set = Boolean(plan.stopSignal || plan.gracePeriodSeconds || plan.drainSeconds)
  return set ? parts.join(" · ") : `Defaults · ${parts.join(" · ")}`
}
