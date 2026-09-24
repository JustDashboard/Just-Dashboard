"use client"

import {
  ArrowLeftRight,
  ArrowRight,
  External,
  Notes,
  Pin,
  RefreshClockwise,
  RotateCounterClockwise,
  StopCircle,
} from "@/components/icons"
import type { Capability, DeploymentEngineRun, DeploymentRelease } from "@/lib/types"
import type { Verb } from "@/components/verbs"
import { isCancellable, isRetryable } from "@/components/deploy/vocabulary"

/** What pressing each of a run's own verbs does, supplied by the surface that draws them. */
export type RunVerbHandlers = {
  open: () => void
  visit: () => void
  redeploy: () => void
  retry: () => void
  cancel: () => void
}

/** What pressing each of a release's verbs does. */
export type ReleaseVerbHandlers = {
  /** The release against the one before it — what this release changed. */
  changes: (release: DeploymentRelease) => void
  /** The release against the live one. */
  compare: (release: DeploymentRelease) => void
  rollback: (release: DeploymentRelease) => void
  pin: (release: DeploymentRelease) => void
}

/**
 * What can be done to one run, declared once (§13), and — in `releaseVerbs` —
 * to the release it made.
 *
 * The Deployments list draws both behind each row's menu and the run's own
 * page draws the release's behind its header's, its own verbs being the
 * header's buttons — two surfaces that had begun to disagree about which of
 * them a run offered, and under which capability. The release's verbs sit
 * under the release's own name, because "Pin" and "Roll back" act on release
 * #3 and not on run #7, and a menu that says so needs no sentence to explain
 * it.
 *
 * `working` is the verb whose request is in flight: it is disabled until the
 * answer lands, and the surface carries its present participle meanwhile.
 */
export function runVerbs({
  run,
  release,
  liveReleaseId,
  url,
  can,
  working,
  on,
}: {
  run: DeploymentEngineRun
  /** The release this run produced, if it produced one. */
  release?: DeploymentRelease
  liveReleaseId?: number
  /** The live site's address, for Visit on the live release's run. */
  url?: string
  can: (capability: Capability) => boolean
  working?: RunVerbKey
  on: RunVerbHandlers
}): Verb[] {
  const isLive = Boolean(release) && release!.id === liveReleaseId
  const verbs: Verb[] = [
    {
      key: "open",
      label: "Open deployment",
      detail: "The build log and release status for this run.",
      icon: ArrowRight,
      run: on.open,
    },
  ]
  if (isLive && url) {
    verbs.push({
      key: "visit",
      label: "Visit",
      detail: "Open the live site in a new tab.",
      icon: External,
      run: on.visit,
    })
  }
  if (isLive && can("service.control")) {
    verbs.push({
      key: "redeploy",
      label: "Redeploy",
      detail: "Run this release again, unchanged.",
      icon: RefreshClockwise,
      run: on.redeploy,
    })
  }
  if (isRetryable(run.state) && can("service.control")) {
    verbs.push({
      key: "retry",
      label: "Retry",
      detail: "Run this deployment again from the same source.",
      icon: RefreshClockwise,
      progressive: "Starting…",
      disabled: working === "retry",
      run: on.retry,
    })
  }
  if (isCancellable(run.state) && !run.cancelRequested && can("service.control")) {
    verbs.push({
      key: "cancel",
      label: "Cancel",
      detail: "Stop this deployment before it finishes; its cleanup still runs.",
      icon: StopCircle,
      progressive: "Cancelling…",
      disabled: working === "cancel",
      run: on.cancel,
    })
  }
  return verbs
}

/** The verbs of the release a run made, under that release's name. */
export function releaseVerbs({
  release,
  liveReleaseId,
  can,
  working,
  on,
}: {
  release?: DeploymentRelease
  liveReleaseId?: number
  can: (capability: Capability) => boolean
  working?: RunVerbKey
  on: ReleaseVerbHandlers
}): Verb[] {
  if (!release) return []
  const isLive = release.id === liveReleaseId
  const group = `Release #${release.number}`
  const verbs: Verb[] = []
  if (release.predecessorReleaseId) {
    verbs.push({
      key: "changes",
      label: "What changed in this release",
      detail: "Its source, variables and checks against the release before it.",
      icon: Notes,
      group,
      run: () => on.changes(release),
    })
  }
  if (!isLive && liveReleaseId) {
    verbs.push({
      key: "compare",
      label: "Compare with live",
      detail: "What changed between this release and the live one.",
      icon: ArrowLeftRight,
      group,
      run: () => on.compare(release),
    })
  }
  if (release.state === "retained" && !isLive && can("destructive")) {
    verbs.push({
      key: "rollback",
      label: "Roll back to this release",
      detail: "Make this retained release live again.",
      icon: RotateCounterClockwise,
      group,
      run: () => on.rollback(release),
    })
  }
  if (can("system.admin")) {
    verbs.push({
      key: "pin",
      label: release.pinned ? "Unpin release" : "Pin release",
      detail: release.pinned
        ? "Allow this release to be cleaned up automatically again."
        : "Keep this release from being cleaned up automatically.",
      icon: Pin,
      progressive: release.pinned ? "Unpinning…" : "Pinning…",
      disabled: working === "pin",
      group,
      run: () => on.pin(release),
    })
  }
  return verbs
}

export type RunVerbKey =
  "open" | "visit" | "redeploy" | "retry" | "cancel" | "changes" | "compare" | "rollback" | "pin"
