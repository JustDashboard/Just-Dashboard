import type { DotTone } from "@/components/status-dot"
import type {
  ComposePreviewService,
  DeploymentPreview,
  DeploymentRuntimeService,
  GitPullRequest,
  ProjectPullRequests,
} from "@/lib/types"

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

/** Where an open preview can be visited — only while its address answers. */
export function previewURL(preview: DeploymentPreview | null | undefined): string | undefined {
  return preview?.state === "open" && preview.address?.published ? preview.address.url : undefined
}

/** A preview whose removal ended short of success keeps its containers: the thing to retry. */
export function cleanupFailed(preview: DeploymentPreview | null | undefined): boolean {
  return previewStatus(preview)?.label === "Cleanup failed"
}

/**
 * Why a project cannot have a preview at all, as the sentence the dialog
 * says in place of building; nothing when it can. The order is the route's:
 * the server refuses for the same reasons, in the same order, before
 * anything is written.
 */
export function previewBlocker(
  info:
    | Pick<
        ProjectPullRequests,
        "compose" | "composePreview" | "localCheckout" | "internalPort" | "tailnet"
      >
    | undefined,
): string | undefined {
  if (!info) return undefined
  if (info.localCheckout)
    return "This project deploys a local checkout; previews need a remote Git source."
  if (info.compose && !info.composePreview)
    return "A compatibility Compose deployment has no release engine to build a preview with. Deploy it from a Compose source first."
  const refusals = info.composePreview?.refusals ?? []
  if (refusals.length > 0)
    return `A preview isolates every service of the stack, and ${refusals.join("; ")}, which no preview gets.`
  if (info.internalPort === 0) return "The runtime plan has no port to publish."
  if (!info.tailnet.running) return info.tailnet.detail || "Tailscale is not running on this host."
  return undefined
}

/**
 * What one service of a Compose stack gets in its preview, as the short
 * sentences the dialog lists under its name: where it answers, the ports of
 * production's it does not publish, the storage it starts with and what of
 * this server it is not given.
 */
export function composeServiceLines(service: ComposePreviewService): string[] {
  const lines = [
    service.public
      ? "Answers the preview's route and tailnet address, on loopback only"
      : "Reached only by the stack's own services",
  ]
  if (service.ports.length > 0)
    lines.push(`Publishes none of production's ports (${service.ports.join(", ")})`)
  if (service.volumes.length > 0)
    lines.push(
      `${service.volumes.join(", ")} start${service.volumes.length === 1 ? "s" : ""} empty, on a volume of its own`,
    )
  if (service.checkout.length > 0)
    lines.push(`${service.checkout.join(", ")} read from this commit, read-only`)
  if (service.leftOut.length > 0)
    lines.push(`${service.leftOut.join(", ")} from this server left out`)
  return lines
}

/**
 * A preview service as one dot and one word: Docker's health where the
 * service has a check, otherwise whether it runs, and for one that stopped,
 * how it ended.
 */
export function previewServiceReading(
  service: Pick<DeploymentRuntimeService, "state" | "health" | "exitCode" | "oomKilled">,
): PreviewReading {
  if (service.state === "running") {
    if (service.health === "healthy") return { tone: "running", label: "Healthy" }
    if (service.health === "unhealthy") return { tone: "danger", label: "Unhealthy" }
    if (service.health === "starting") return { tone: "warning", label: "Starting" }
    return { tone: "running", label: "Running" }
  }
  if (service.state === "restarting") return { tone: "warning", label: "Restarting" }
  if (service.oomKilled) return { tone: "danger", label: "Out of memory" }
  if (service.state === "exited" || service.state === "dead")
    return {
      tone: "danger",
      label: service.exitCode === undefined ? "Exited" : `Exited ${service.exitCode}`,
    }
  return {
    tone: "stopped",
    label: service.state
      ? `${service.state[0].toUpperCase()}${service.state.slice(1)}`
      : "Not running",
  }
}

/**
 * The live release's services of a Compose preview, by their Compose name,
 * the public one first: what the preview's row lists under it.
 */
export function previewServices(
  preview: DeploymentPreview | null | undefined,
  publicService?: string,
): { name: string; public: boolean; reading: PreviewReading }[] {
  if (!preview?.services || preview.services.status !== "available") return []
  return preview.services.services
    .filter((service) => service.liveRelease)
    .map((service) => {
      const name = service.service || service.name
      return { name, public: name === publicService, reading: previewServiceReading(service) }
    })
    .sort((a, b) => (a.public === b.public ? a.name.localeCompare(b.name) : a.public ? -1 : 1))
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
