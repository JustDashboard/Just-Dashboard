import type { DotTone } from "@/components/status-dot"
import type { MetricEvent } from "@/lib/types"

const ACTION_LABELS: Record<string, string> = {
  "terminal.kill": "Close terminal",
  "terminal.window.kill": "Close terminal window",
  "deploy.variable.delete": "Delete deployment variable",
  "dashboard.restart": "Restart dashboard",
  "docker.container.restart": "Restart container",
  "docker.container.start": "Start container",
  "docker.container.stop": "Stop container",
  "docker.container.remove": "Remove container",
  "systemd.restart": "Restart service",
  "systemd.start": "Start service",
  "systemd.stop": "Stop service",
}

/** Keep the host's target intact, including spaces; prose titles remain prose. */
export function activityAction(event: MetricEvent) {
  if (event.kind !== "action") return undefined
  const match = /^((?:[a-z]+:)?[a-z][\w-]*(?:\.[\w-]+)+)(?:\s+([\s\S]*))?$/.exec(event.title)
  if (!match) return undefined
  const action = match[1]
  const bare = action.replace(/^[a-z]+:/, "")
  return {
    action,
    target: match[2] ?? "",
    label: Object.hasOwn(ACTION_LABELS, bare) ? ACTION_LABELS[bare] : undefined,
  }
}

/** An informational marker is not proof that a deploy or backup finished. */
export function activityStatus(event: MetricEvent): { label: string; tone: DotTone } {
  if (event.severity === "error") return { label: "Failed", tone: "danger" }
  if (event.kind === "reboot") return { label: "Restarted", tone: "warning" }
  if (event.severity === "warning") return { label: "Warning", tone: "warning" }
  if (event.kind === "action") return { label: "Accepted", tone: "running" }

  // These are the recorder's run details, not words guessed from a job name.
  const run = /^(deploy|backup) (\w+)\b/.exec(event.detail ?? "")
  const state = run?.[1] === event.kind ? run[2] : undefined
  switch (state) {
    case "success":
      return { label: "Succeeded", tone: "running" }
    case "running":
      return { label: "Running", tone: "notice" }
    case "pending":
      return { label: "Pending", tone: "notice" }
    case "cancelled":
      return { label: "Cancelled", tone: "stopped" }
    default:
      return { label: "Recorded", tone: "notice" }
  }
}
