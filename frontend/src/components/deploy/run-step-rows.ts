import type { DeploymentStep } from "@/lib/types"

/*
 * Which of a run's steps are rows on its page (`run-steps.tsx`). Only steps
 * that did something are: a step the plan skipped records its reason and
 * nothing else, so it is a line under the rows, and one the run has not
 * reached records nothing at all.
 */

/**
 * What a skipped step can record having done anyway. Preview cleanup skips a
 * preview with no live release, but only after removing its route and
 * withdrawing its address: a record of that is evidence, not a reason.
 */
const SKIPPED_EFFECTS = ["routeRemoved", "addressWithdrawn"] as const

/** A skipped step whose evidence says it changed something, so it has a record to read. */
export function skippedWithEffect(step: DeploymentStep) {
  return step.state === "skipped" && SKIPPED_EFFECTS.some((key) => step.evidence?.[key] === true)
}

/**
 * A step with something of its own to read: it ran, it is running, or it was
 * skipped after doing something anyway. A bare skip has a reason and nothing
 * else, and one the run has not reached has nothing at all.
 */
export function ranStep(step: DeploymentStep) {
  if (step.state === "pending") return false
  return step.state !== "skipped" || skippedWithEffect(step)
}
