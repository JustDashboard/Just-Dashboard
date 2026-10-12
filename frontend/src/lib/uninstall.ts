import type { DeploymentFootprint, UninstallRun } from "@/lib/types"
import { plural } from "@/lib/format"

/**
 * Reading an uninstall's record — the part of the page that has to keep
 * making sense after the API it reads from has gone.
 *
 * `lost` is the poll failing while the run is live, and it is read as the
 * stack having gone whichever phase this page last saw. A whole run can finish
 * between two polls — a keep takes seconds — so the last phase seen proves
 * nothing about how far it got. A backend that merely crashed comes back, and
 * its next answer (the run closed as failed) replaces this reading. From then
 * on nothing here can find out more, and the page says so rather than spinning.
 */

/**
 * Where the transcript continues once the stack is gone — on the host, outside
 * the data directory a wipe deletes. internal/uninstall.DurableLog.
 */
export const DURABLE_LOG = "/var/log/just-dashboard-uninstall.log"

/** Whether the stack is gone: contact lost while the run is live. */
export function stackGone(run: UninstallRun, lost: boolean): boolean {
  return lost && isLive(run)
}

export function isLive(run: UninstallRun): boolean {
  return run.status === "pending" || run.status === "running"
}

/** The run's stages, and where it is among them. */
export function uninstallStages(
  run: UninstallRun,
  lost: boolean,
): { labels: string[]; at: number; outcome: "running" | "success" | "failed" } {
  const labels = [
    "Pause deployments",
    ...(run.mode === "wipe" ? ["Remove the deployments"] : []),
    "Stop the dashboard",
    run.mode === "wipe" ? "Delete its data" : "Remove its images",
  ]
  const stop = labels.indexOf("Stop the dashboard")
  const at = (() => {
    // The page losing the stack is the stack coming down, which is that stage
    // done: what is left runs on the server, where nothing can watch it.
    if (stackGone(run, lost)) return stop + 1
    switch (run.phase) {
      case "queued":
      case "pausing":
        return 0
      case "deployments":
        return 1
      case "stack":
        return stop
      case "files":
        return stop + 1
      default:
        return labels.length
    }
  })()
  if (run.status === "success") return { labels, at: labels.length, outcome: "success" }
  if (run.status === "failed")
    return { labels, at: Math.min(at, labels.length - 1), outcome: "failed" }
  return { labels, at, outcome: "running" }
}

/** What the run is doing, as the headline says it. */
export function uninstallHeadline(run: UninstallRun, lost: boolean): string {
  if (run.status === "success") return "Uninstalled"
  if (run.status === "failed") return "Uninstall failed"
  if (stackGone(run, lost)) return "The dashboard has stopped"
  switch (run.phase) {
    case "pausing":
      return "Pausing deployments"
    case "deployments":
      return "Removing the deployments"
    case "stack":
      return "Stopping the dashboard"
    case "files":
      return "Removing what is left"
    default:
      return "Starting the uninstall"
  }
}

/** The deployments' footprint in one line: "6 containers · 3 volumes · 2 images". */
export function footprintLine(footprint: DeploymentFootprint): string {
  const parts = [
    footprint.containers.length > 0 && plural(footprint.containers.length, "container"),
    footprint.volumes.length > 0 && plural(footprint.volumes.length, "volume"),
    footprint.paths.length > 0 && plural(footprint.paths.length, "folder"),
    footprint.images.length > 0 && plural(footprint.images.length, "image"),
    footprint.networks.length > 0 && plural(footprint.networks.length, "network"),
    footprint.routes.length > 0 && plural(footprint.routes.length, "route"),
    footprint.ingress && "the public ingress",
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(" · ") : "nothing on this server"
}
