import type { Job } from "@/lib/types"

export type InstallJob = Pick<Job, "id" | "status">
export type InstallJobs = Record<string, InstallJob>

/** A response from an older attempt must not replace a newer retry for the same package. */
export function reconcileInstall(jobs: InstallJobs, name: string, job: InstallJob): InstallJobs {
  const current = jobs[name]
  if (!current || current.id !== job.id || current.status === job.status) return jobs
  return { ...jobs, [name]: { id: job.id, status: job.status } }
}
