import type { BackupJob, BackupResourceKind } from "@/lib/types"
import { describeCron } from "@/lib/cron"
import { plural } from "@/lib/format"

/** Where a job's archives go, as the row says it. */
export function targetLabel(job: BackupJob): string {
  switch (job.targetKind) {
    case "s3":
      return `S3 · ${job.target.bucket ?? ""}`
    case "b2":
      return `Backblaze B2 · ${job.target.bucket ?? ""}`
    default:
      return job.target.path ?? "Local directory"
  }
}

/** The schedule as a sentence, or the word for a job that only runs by hand. */
export function scheduleLabel(schedule: string): string {
  if (!schedule.trim()) return "Manual only"
  return describeCron(schedule)
}

/** What a job captures, in one line under its name. */
export function contentsLabel(job: BackupJob): string {
  // A job that only dumps databases has no paths, and "0 paths" led its line.
  const parts = job.sources.length > 0 ? [plural(job.sources.length, "path")] : []
  if (job.databaseDumps?.length) parts.push(plural(job.databaseDumps.length, "database dump"))
  if (job.sqlitePaths?.length) parts.push(plural(job.sqlitePaths.length, "SQLite snapshot"))
  if (job.pauseContainers?.length)
    parts.push(`pauses ${plural(job.pauseContainers.length, "container")}`)
  return parts.join(" · ")
}

export function retentionLabel(job: BackupJob): string {
  const parts: string[] = []
  if (job.retention > 0) parts.push(`last ${plural(job.retention, "archive")}`)
  if (job.retentionDays > 0) parts.push(`${plural(job.retentionDays, "day")}`)
  return parts.length ? `Keeps ${parts.join(" and ")}` : "Keeps everything"
}

export const RESOURCE_KIND_LABEL: Record<BackupResourceKind, string> = {
  dashboard: "Dashboard",
  proxy: "Proxy",
  volume: "Docker volume",
  stack: "Compose stack",
  deployment: "Deployment",
  repository: "Repository",
  database: "Database",
}

/** The lines of a textarea, trimmed and without blanks. */
export function lines(value: string): string[] {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}
