/*
 * Host alerts: rules over the machine's own readings — CPU and memory held
 * high, a filesystem filling, a container gone down, a backup job failed,
 * the Cloudflare Tunnel's connector cut off from the edge —
 * that tell the deployments' notification channels once when a subject starts
 * firing and once when it comes back. The server's half is
 * `api/host_alerts.go`; this is the page's vocabulary for it, kept free of
 * React so `bun test` reads it.
 */

export type HostAlertKind =
  "cpu_high" | "memory_high" | "disk_filling" | "container_down" | "backup_failed" | "tunnel_down"

/** Each kind reads its own; the server clears the rest. */
export type HostAlertParams = {
  /** The line, as a percentage. */
  threshold?: number
  /** CPU's and memory's window, or how long a container stays down before it is told. */
  minutes?: number
}

export type HostAlertRule = {
  id: number
  kind: HostAlertKind
  params: HostAlertParams
  /** The channels it tells; empty tells every enabled one. */
  channels: number[]
  enabled: boolean
  createdAt: string
  updatedAt: string
  /** Why the last pass could not judge it at all, while that holds. */
  unjudged?: string
}

/** One subject a rule follows that is firing, waiting to hold, or muted. */
export type HostAlertSubject = {
  ruleId: number
  kind: HostAlertKind
  /** "cpu", "memory", a mountpoint, a container's name, or "job:<id>". */
  subject: string
  label: string
  detail: string
  state: "pending" | "firing" | "quiet"
  since?: string
  notifiedAt?: string
  muted: boolean
}

export type HostAlertEvent = {
  id: number
  ruleId: number
  kind: HostAlertKind
  subject: string
  label: string
  event: "host.alert.firing" | "host.alert.recovered"
  detail: string
  delivered: number
  failed: number
  muted: boolean
  /** Sent from a rule's "Send test", not by a pass. */
  test: boolean
  createdAt: string
}

/** GET /system/alerts, and the answer to POST /system/alerts/evaluate. */
export type HostAlerts = {
  rules: HostAlertRule[]
  subjects: HostAlertSubject[]
  /** The newest 100 transitions and tests, newest first. */
  history: HostAlertEvent[]
  /** Absent before the first pass since the dashboard started. */
  lastPass?: string
  intervalSeconds: number
  /** Whether the metrics history CPU, memory and disk rules read is kept. */
  metrics: boolean
}

export const HOST_ALERT_KINDS: HostAlertKind[] = [
  "cpu_high",
  "memory_high",
  "disk_filling",
  "container_down",
  "backup_failed",
  "tunnel_down",
]

/** The server's defaults (`hostAlertSpecs`), so a new form opens on them. */
export const HOST_ALERT_DEFAULTS: Record<HostAlertKind, HostAlertParams> = {
  cpu_high: { threshold: 90, minutes: 10 },
  memory_high: { threshold: 90, minutes: 5 },
  disk_filling: { threshold: 90 },
  container_down: { minutes: 2 },
  backup_failed: {},
  tunnel_down: { minutes: 2 },
}

/** The ranges the server accepts, so the form says so before it is sent. */
const LIMITS: Record<HostAlertKind, { threshold?: [number, number]; minutes?: [number, number] }> =
  {
    cpu_high: { threshold: [10, 100], minutes: [1, 1440] },
    memory_high: { threshold: [10, 100], minutes: [1, 1440] },
    disk_filling: { threshold: [10, 99] },
    container_down: { minutes: [1, 1440] },
    backup_failed: {},
    tunnel_down: { minutes: [1, 1440] },
  }

/** Whether a kind has a line, and whether it has minutes. */
export function hostAlertFields(kind: HostAlertKind) {
  return { threshold: Boolean(LIMITS[kind].threshold), minutes: Boolean(LIMITS[kind].minutes) }
}

/** Whether a kind reads the metrics recorder's history. */
export function readsMetrics(kind: HostAlertKind) {
  return kind === "cpu_high" || kind === "memory_high" || kind === "disk_filling"
}

export const HOST_ALERT_WORDS: Record<HostAlertKind, { label: string; hint: string }> = {
  cpu_high: {
    label: "CPU high",
    hint: "Averaged over a window of the recorded history, so one spike is not an alert.",
  },
  memory_high: {
    label: "Memory high",
    hint: "Memory in use, averaged the same way, before the kernel starts killing processes.",
  },
  disk_filling: {
    label: "Disk filling up",
    hint: "Any mounted filesystem, by its space or by its inodes.",
  },
  container_down: {
    label: "Container down",
    hint: "One that crashes, keeps restarting or turns unhealthy. A container you stop is not down.",
  },
  backup_failed: {
    label: "Backup failed",
    hint: "The latest run of any backup job: files, volumes and database dumps alike.",
  },
  tunnel_down: {
    label: "Tunnel down",
    hint: "The Cloudflare Tunnel's connector stopped, or holding no connection to Cloudflare's edge, in cloudflared's own words.",
  },
}

/** A minute count in words: "1 min", "90 min", "2 h", "1 day". */
export function minutesWords(minutes: number) {
  if (minutes >= 1440 && minutes % 1440 === 0) {
    const days = minutes / 1440
    return days === 1 ? "1 day" : `${days} days`
  }
  if (minutes >= 120 && minutes % 60 === 0) return `${minutes / 60} h`
  return `${minutes} min`
}

/** What a rule watches for, in the words a row and a toast say it in. */
export function describeHostAlert(kind: HostAlertKind, params: HostAlertParams) {
  const p = { ...HOST_ALERT_DEFAULTS[kind], ...params }
  switch (kind) {
    case "cpu_high":
    case "memory_high":
      return `averages ${p.threshold}% or more over ${minutesWords(p.minutes ?? 1)}`
    case "disk_filling":
      return `a filesystem reaches ${p.threshold}% of its space or inodes`
    case "container_down":
      return `exits, keeps restarting or turns unhealthy for ${minutesWords(p.minutes ?? 1)}`
    case "backup_failed":
      return "the latest run of a backup job fails"
    case "tunnel_down":
      return `holds no connection to Cloudflare's edge for ${minutesWords(p.minutes ?? 1)}`
  }
}

/**
 * The parameters a form sends for a kind, from what was typed, with what is
 * wrong with them — the server's own ranges, said before the round trip.
 */
export function hostAlertDraft(
  kind: HostAlertKind,
  typed: { threshold: string; minutes: string },
): { params: HostAlertParams; problem?: string } {
  const limits = LIMITS[kind]
  const params: HostAlertParams = {}
  if (limits.threshold) {
    const [low, high] = limits.threshold
    const value = Number(typed.threshold.trim())
    if (typed.threshold.trim() === "" || !Number.isFinite(value) || value < low || value > high)
      return { params, problem: `The line is a percentage between ${low} and ${high}.` }
    params.threshold = value
  }
  if (limits.minutes) {
    const [low, high] = limits.minutes
    const value = Number(typed.minutes.trim())
    if (typed.minutes.trim() === "" || !Number.isInteger(value) || value < low || value > high)
      return { params, problem: `The minutes are a whole number between ${low} and ${high}.` }
    params.minutes = value
  }
  return { params }
}

/** How often the rules are checked, as the page says it. */
export function cadenceWords(intervalSeconds: number) {
  const minutes = Math.max(1, Math.round(intervalSeconds / 60))
  return minutes === 1 ? "every minute" : `every ${minutes} minutes`
}

/** What is firing and what is holding, not counting muted subjects. */
export function hostAlertCounts(subjects: HostAlertSubject[]) {
  let firing = 0
  let holding = 0
  for (const subject of subjects) {
    if (subject.muted) continue
    if (subject.state === "firing") firing++
    else if (subject.state === "pending") holding++
  }
  return { firing, holding }
}
