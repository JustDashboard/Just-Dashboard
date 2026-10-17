import type { DbAlertKind } from "@/lib/types"

/**
 * What the alert routes answer, as the server writes them
 * (`api/database_alerts.go`). A rule watches one database, or every database
 * when `connectionId` is null, and tells the deployments' notification
 * channels it names — every enabled one when it names none.
 */

export type DbAlertRule = {
  id: number
  connectionId: number | null
  kind: DbAlertKind
  /** In the kind's unit: a percentage, or seconds of lag; 0 for a kind with no line. */
  threshold: number
  /** How long it must hold before it is told; a missed backup's grace. */
  minutes: number
  cooldownMinutes: number
  notifyRecovery: boolean
  channels: number[]
  enabled: boolean
  createdAt: string
  updatedAt: string
}

/** What a form sends; an absent figure takes the kind's default. */
export type DbAlertWrite = {
  connectionId: number | null
  kind: DbAlertKind
  threshold?: number
  minutes?: number
  cooldownMinutes?: number
  notifyRecovery?: boolean
  channels: number[]
  enabled?: boolean
}

/**
 * Where a subject stands: holding before it is told (`pending`), told
 * (`firing`), kept back by the cooldown (`held`), not judged on the last pass
 * (`unknown`, with the reason), or fine.
 */
export type DbAlertState = "quiet" | "pending" | "firing" | "held" | "unknown"

/** One database, or one backup job that dumps it, as a rule last read it. */
export type DbAlertSubject = {
  ruleId: number
  subject: string
  connectionId?: number
  jobId?: number
  /** The database's name, or the backup job's. */
  label: string
  state: DbAlertState
  /** In the kind's unit; -1 for replication that has stopped. */
  reading: number
  /** The reading in words, with the line. */
  detail: string
  unknown?: string
  since?: string
  firedAt?: string
  notifiedAt?: string
  checkedAt?: string
}

export type DbAlertEvent = {
  id: number
  ruleId: number
  connectionId?: number
  jobId?: number
  kind: DbAlertKind
  subject: string
  label: string
  event: "fired" | "recovered"
  detail: string
  delivered: number
  failed: number
  /** Why nobody was told: the cooldown, or a rule that tells only firings. */
  kept?: "cooldown" | "recovery"
  createdAt: string
}

/** `GET /databases/alerts?connection=`. */
export type DbAlerts = {
  rules: DbAlertRule[]
  subjects: DbAlertSubject[]
  /** The newest hundred, newest first. */
  events: DbAlertEvent[]
  lastPass?: string
  everySeconds: number
}

/** `POST /databases/alerts/rules/{id}/test`. */
export type DbAlertTest = {
  delivered: number
  failed: number
  paused: number
  channels: number
}
