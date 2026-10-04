import type { DeploymentBackupJob } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"

/** The part of the Backups coverage report one volume's row needs. */
type Coverage = {
  protected: boolean
  lastBackupAt?: string
  coveredBy: { jobId: number; enabled: boolean }[]
}

/**
 * Where a protected volume's backups stand, as a word and its tone, or
 * nothing for a volume no job copies — that one has its own row to say so.
 *
 * A job the release itself declares as its backup policy knows more than the
 * coverage list does — whether the last run failed and whether it is inside
 * the policy's maximum age — so that job's reading wins. Without one, all
 * the page has is the time the last run ended. `at` is that time, for the
 * caller to put in words, so this stays a function of its arguments.
 */
export function volumeBackup(
  resource: Coverage | undefined,
  declared: DeploymentBackupJob[] | undefined,
): { tone: DotTone; word: string; at?: string } | undefined {
  if (!resource?.protected) return undefined
  const covering = resource.coveredBy.filter((job) => job.enabled).map((job) => String(job.jobId))
  const policy = declared?.find(
    (job) => job.status === "present" && covering.includes(job.resourceId),
  )
  const at = resource.lastBackupAt
  if (policy?.lastStatus === "failed") return { tone: "danger", word: "Last backup failed", at }
  if (policy?.lastStatus === "success" && !policy.fresh)
    return { tone: "warning", word: "Backup is stale", at }
  if (policy?.lastStatus === "never run") return { tone: "warning", word: "Never backed up" }
  if (at) return { tone: "running", word: "Backed up", at }
  return { tone: "notice", word: "Protected · not run yet" }
}
