import type { DeploymentSummary } from "@/lib/types"
import type { WorkloadCandidate } from "@/lib/workload-import"

export function importedWorkloadOf(summary: DeploymentSummary) {
  return (summary as DeploymentSummary & { importedWorkload?: WorkloadCandidate }).importedWorkload
}

export function importedWorkloadState(summary: DeploymentSummary) {
  const workload = importedWorkloadOf(summary)
  if (!workload || workload.state === "unavailable" || workload.state === "missing")
    return "unavailable" as const
  if (workload.running === 0) return "stopped" as const
  if (workload.running < workload.total) return "partial" as const
  return "observed" as const
}
