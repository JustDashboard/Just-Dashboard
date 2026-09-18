import type { BackupJob, BackupResourceKind } from "@/lib/types"
import { describeCron, isValidCron, nextCronRun } from "@/lib/cron"
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
  const parts = [plural(job.sources.length, "path")]
  if (job.databaseDumps?.length) parts.push(plural(job.databaseDumps.length, "database dump"))
  if (job.sqlitePaths?.length) parts.push(plural(job.sqlitePaths.length, "SQLite snapshot"))
  if (job.pauseContainers?.length) parts.push(`pauses ${plural(job.pauseContainers.length, "container")}`)
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

/**
 * The presets the job form offers. Backups are not cron jobs: nothing here
 * runs every minute, and there is no "when the server boots" — a backup that
 * fires on every reboot is a surprise, not a policy.
 */
export const SCHEDULE_PRESETS = [
  { key: "manual", label: "Only when I run it" },
  { key: "hourly", label: "Every hour" },
  { key: "daily", label: "Every day" },
  { key: "weekly", label: "Every week" },
  { key: "monthly", label: "Every month" },
  { key: "custom", label: "Custom cron expression" },
] as const

export type SchedulePreset = (typeof SCHEDULE_PRESETS)[number]["key"]

export type ScheduleFields = {
  preset: SchedulePreset
  time: string
  weekday: string
  monthDay: string
  custom: string
}

/** What an existing expression looks like in the builder's fields. */
export function scheduleFields(schedule: string): ScheduleFields {
  const out: ScheduleFields = { preset: "daily", time: "03:00", weekday: "1", monthDay: "1", custom: "" }
  const trimmed = schedule.trim()
  if (!trimmed) return { ...out, preset: "manual" }
  const fields = trimmed.split(/\s+/)
  if (fields.length !== 5) return { ...out, preset: "custom", custom: trimmed }
  const [m, h, dom, month, dow] = fields
  const plainMinute = /^\d{1,2}$/.test(m)
  const plainHour = /^\d{1,2}$/.test(h)
  if (plainMinute && plainHour) out.time = `${h.padStart(2, "0")}:${m.padStart(2, "0")}`
  if (month !== "*") return { ...out, preset: "custom", custom: trimmed }
  if (plainMinute && h === "*" && dom === "*" && dow === "*") return { ...out, preset: "hourly" }
  if (plainMinute && plainHour && dom === "*" && dow === "*") return { ...out, preset: "daily" }
  if (plainMinute && plainHour && dom === "*" && /^[0-7]$/.test(dow)) {
    return { ...out, preset: "weekly", weekday: dow === "7" ? "0" : dow }
  }
  if (plainMinute && plainHour && /^\d{1,2}$/.test(dom) && dow === "*") {
    return { ...out, preset: "monthly", monthDay: dom }
  }
  return { ...out, preset: "custom", custom: trimmed }
}

/** The expression the builder's fields describe; empty for a manual job. */
export function scheduleExpression(fields: ScheduleFields): string {
  const [hh, mm] = fields.time.split(":").map((v) => Number(v))
  const minute = Number.isFinite(mm) ? mm : 0
  const hour = Number.isFinite(hh) ? hh : 0
  switch (fields.preset) {
    case "manual":
      return ""
    case "hourly":
      return `${minute} * * * *`
    case "daily":
      return `${minute} ${hour} * * *`
    case "weekly":
      return `${minute} ${hour} * * ${fields.weekday}`
    case "monthly":
      return `${minute} ${hour} ${fields.monthDay} * *`
    case "custom":
      return fields.custom.trim()
  }
}

export function scheduleValid(expression: string): boolean {
  return expression === "" || isValidCron(expression)
}

/** The next three moments a schedule fires, in this browser's clock. */
export function schedulePreview(expression: string, count = 3): Date[] {
  if (!expression || !isValidCron(expression)) return []
  const out: Date[] = []
  let from = new Date()
  for (let i = 0; i < count; i++) {
    const next = nextCronRun(expression, from)
    if (!next) break
    out.push(next)
    from = next
  }
  return out
}

export const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]

/** The lines of a textarea, trimmed and without blanks. */
export function lines(value: string): string[] {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}
