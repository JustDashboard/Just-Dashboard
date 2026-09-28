import type { DotTone } from "@/components/status-dot"
import type { DeploymentPreview, GitPullRequest, ProjectPullRequests } from "@/lib/types"

/**
 * What a pull request and the preview built from it say about themselves, as
 * the words the Git page, the deploy Overview and the fleet card all use.
 *
 * Three surfaces draw the same two facts — how the pull request's checks
 * stand, and where its preview environment is — and each had the vocabulary
 * to invent its own. One place decides, so "Out of date" on the Git page is
 * "Out of date" on the project's Overview.
 */

/** The dot for a pull request's folded check state: gh's success/failure/pending. */
export function pullChecksTone(pull: Pick<GitPullRequest, "checks">): DotTone {
  switch (pull.checks) {
    case "success":
      return "running"
    case "failure":
      return "danger"
    case "pending":
      return "warning"
    default:
      return "unknown"
  }
}

// The engine's run states that have not ended yet. Listed here rather than
// imported from the deploy vocabulary because this file is plain data the unit
// tests load without a component tree behind it.
const RUN_IN_FLIGHT = new Set<string>([
  "requested",
  "validating",
  "queued",
  "preparing",
  "running",
  "verifying",
  "activating",
  "failed_activation",
  "restoring_previous",
  "cancelling",
])

function runFailed(state: string | undefined) {
  return state === "failed" || state === "failed_activation" || state === "rolled_back"
}

/**
 * New commits arrived on the pull request since the preview was built: the
 * reconciler records the newest head beside the approved revision, and the
 * two disagreeing is the whole reading.
 */
export function previewOutOfDate(preview: DeploymentPreview | null | undefined): boolean {
  return Boolean(
    preview &&
    preview.state === "open" &&
    preview.headRevision &&
    preview.revision &&
    preview.headRevision !== preview.revision,
  )
}

export type PreviewReading = { tone: DotTone; label: string }

/**
 * One dot and one word for a preview environment.
 *
 * Read in the order the facts arrive: a removal in flight or failed is said
 * first because a closed preview whose containers are still there is the
 * thing an operator has to act on; then a build in flight; then whether the
 * live release is reachable on its address, since a preview that built and
 * cannot be opened is not "Ready" by any reading; and a head that moved on
 * outranks "Ready" because the thing being tested is no longer the thing on
 * the pull request.
 */
export function previewStatus(
  preview: DeploymentPreview | null | undefined,
): PreviewReading | undefined {
  if (!preview) return undefined
  const run = preview.lastRun
  if (run?.operation === "preview_remove") {
    if (RUN_IN_FLIGHT.has(run.state)) return { tone: "warning", label: "Closing" }
    // Anything short of success — failed, rolled back, cancelled, superseded —
    // left the containers where they were, and the route refuses to build
    // over them until the removal is run again: the same rule the server's
    // listing applies, so a cancelled cleanup is not read as "Closed".
    if (run.state !== "succeeded") return { tone: "danger", label: "Cleanup failed" }
  }
  if (preview.state === "closed") return { tone: "stopped", label: "Closed" }
  if (run && RUN_IN_FLIGHT.has(run.state)) return { tone: "notice", label: "Building" }
  if (previewOutOfDate(preview)) return { tone: "warning", label: "Out of date" }
  if (preview.liveReleaseId) {
    return preview.address?.published
      ? { tone: "running", label: "Ready" }
      : { tone: "warning", label: "Not reachable" }
  }
  if (run && runFailed(run.state)) return { tone: "danger", label: "Failed" }
  // Approved and recorded, nothing built yet: a webhook preview waiting for
  // its first run, or one whose run was cancelled before it began.
  return { tone: "notice", label: "Pending" }
}

/**
 * The readings under which "Test this pull request" has nothing to do, each
 * with the sentence the dialog says instead of building: a preview ready at
 * this commit, one still building or closing, and one whose removal failed —
 * the route refuses that last one until the cleanup is retried. Every other
 * reading — none yet, failed, pending, closed, unreachable, out of date — is
 * one press from a fresh build of the head. Three surfaces offer the verb,
 * and this table is what keeps them holding it for the same reasons.
 */
const HELD: Record<string, string> = {
  Ready: "This pull request already has a preview at this commit.",
  Building: "A preview of this pull request is already building.",
  Closing: "Its preview is still closing. Test it again once the removal has finished.",
  "Cleanup failed": "Its last preview's cleanup failed. Retry the cleanup before testing it again.",
}

/** Whether the preview's reading holds "Test this pull request" back. */
export function previewHeld(preview: DeploymentPreview | null | undefined): boolean {
  return heldReason(preview) !== undefined
}

/** Why a held pull request is not built, as the sentence that says what happens instead. */
export function heldReason(preview: DeploymentPreview | null | undefined): string | undefined {
  const reading = previewStatus(preview)
  return reading ? HELD[reading.label] : undefined
}

/** A preview whose removal ended short of success keeps its containers: the thing to retry. */
export function cleanupFailed(preview: DeploymentPreview | null | undefined): boolean {
  return previewStatus(preview)?.label === "Cleanup failed"
}

/**
 * Whether "Test this pull request" has an exact commit to build. The route
 * refuses anything but the head it was told, so a listing without the sha
 * cannot offer the verb — and a closed pull request has nothing to test.
 */
export function canTest(pull: Pick<GitPullRequest, "state" | "headSha">): boolean {
  return pull.state === "open" && Boolean(pull.headSha)
}

/**
 * Why a project's pull requests could not be read, as the sentence that
 * says what to do about it. The Overview's panel and the header's picker
 * both answer an unavailable listing with it, so the two cannot disagree
 * about where to sign in. `app_not_installed` is only ever said when the
 * GitHub CLI is missing too — with it, a signed-out CLI reads
 * `sign_in_required` — so the App is the one fix on offer.
 */
export function unavailableReason(info: Pick<ProjectPullRequests, "reason" | "repository">) {
  switch (info.reason) {
    case "sign_in_required":
      return "Sign in to GitHub on the Git page to read this repository's pull requests."
    case "app_not_installed":
      return `Install the GitHub App on ${info.repository} to read its pull requests; the GitHub CLI is not installed on this host.`
    case "not_installed":
      return "The GitHub CLI is not installed on this host, so pull requests cannot be read."
    case "not_github":
      return "Pull requests are read from GitHub, and this project's source is not a GitHub repository."
    default:
      return "This repository's pull requests could not be read."
  }
}

/**
 * A preview's pull request as the row draws one, from what the dashboard
 * recorded of it: its title, author, branch and head at the time it was
 * approved. Closed when the listing is trusted and does not have it — the
 * reconciler will close the preview on its next pass — and unknown while
 * the listing cannot be read at all, so nothing offers to test or merge a
 * pull request nobody here can see. A listing that names no repository has
 * no page on GitHub to open and nothing to call the head a fork of.
 */
export function pullOfPreview(
  preview: DeploymentPreview,
  info: Pick<ProjectPullRequests, "available" | "host" | "repository">,
): GitPullRequest {
  const repository = info.repository
  return {
    number: preview.number,
    title: preview.title || `Pull request #${preview.number}`,
    url: repository
      ? `https://${info.host || "github.com"}/${repository}/pull/${preview.number}`
      : "",
    state: info.available ? "closed" : "unknown",
    draft: false,
    head: preview.headRef ?? "",
    base: "",
    author: preview.author,
    updatedAt: preview.updatedAt,
    comments: 0,
    headSha: preview.revision,
    headRepository: preview.headRepository,
    fork: Boolean(
      repository &&
      preview.headRepository &&
      preview.headRepository.toLowerCase() !== repository.toLowerCase(),
    ),
  }
}
