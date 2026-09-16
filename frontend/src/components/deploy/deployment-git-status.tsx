"use client"

import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { Status } from "@/components/status-dot"

type GitWatchStatus = {
  automatic: boolean
  branch?: string
  status: string
  checkedAt?: string
  intervalSeconds: number
}

export function DeploymentGitStatus({
  projectID,
  environmentID,
}: {
  projectID: number
  environmentID: number
}) {
  const watch = usePoll(
    (signal) =>
      get<GitWatchStatus>(
        `/deploy/${projectID}/environments/${environmentID}/git-watch`,
        undefined,
        signal,
      ),
    5000,
    [projectID, environmentID],
  )
  if (watch.error) {
    return <p className="text-xs text-warning">Automatic deployment status is unavailable.</p>
  }
  const status = watch.data
  if (!status?.automatic) return null
  const unavailable = status.status === "unavailable" || status.status === "stale"
  return (
    <div className="space-y-2" aria-label="Automatic deployments">
      <Status
        state={unavailable ? "warning" : "enabled"}
        label={unavailable ? "Automatic deployments need attention" : "Automatic deployments"}
      />
      <p className="text-xs text-muted-foreground">
        {unavailable
          ? "Could not check the production branch. Check repository access and credentials."
          : status.status === "awaiting_first_deployment"
            ? `After your first deployment, new commits to ${status.branch} deploy automatically.`
            : `New commits to ${status.branch} deploy automatically. Checked every ${status.intervalSeconds} seconds.`}
      </p>
      {status.checkedAt && (
        <p className="text-xs text-muted-foreground">
          Last checked {relativeTime(status.checkedAt)}
        </p>
      )}
    </div>
  )
}
