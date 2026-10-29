import { bytes } from "@/lib/format"
import type { Tone } from "@/components/tone"

/**
 * The capacity forecast as `GET /system/metrics/capacity` answers it
 * (`api/handlers_capacity.go`, the line fitting in `metrics/forecast.go`),
 * and the words the Metrics page says it in.
 */

export type ForecastStatus = "filling" | "steady" | "shrinking" | "insufficient_history"
export type ForecastConfidence = "high" | "medium" | "low"

export type MountForecast = {
  mountpoint: string
  usedBytes: number
  totalBytes: number
  usedPercent: number
  status: ForecastStatus
  growthBytesPerDay: number | null
  daysToFull: number | null
  fullAt: string | null
  r2: number | null
  confidence?: ForecastConfidence
  from: string | null
  to: string | null
  points: number
}

export type VolumeGrowth = {
  name: string
  sizeBytes: number
  status: ForecastStatus
  growthBytesPerDay: number | null
  r2: number | null
  confidence?: ForecastConfidence
  from: string | null
  to: string | null
  points: number
  measured: boolean
}

export type AppGrowth = {
  projectId: number
  projectName: string
  environmentId: number
  environmentName: string
  sizeBytes: number
  growthBytesPerDay: number | null
  status: ForecastStatus
  volumes: VolumeGrowth[]
}

export type CapacityForecast = {
  status: "available" | "unavailable"
  reason?: string
  retentionSeconds: number
  volumeIntervalSeconds: number
  mounts: MountForecast[]
  apps: AppGrowth[]
}

/** "in 5 days", "in 3 weeks", "in 4 months", "in more than a year". */
export function daysLabel(days: number) {
  if (days < 1) return "within a day"
  if (days < 14) return `in ${Math.round(days)} ${Math.round(days) === 1 ? "day" : "days"}`
  if (days < 60) return `in ${Math.round(days / 7)} weeks`
  if (days < 365) return `in ${Math.round(days / 30)} months`
  return "in more than a year"
}

/** The verdict on one filesystem, as its row says it. */
export function mountVerdict(mount: MountForecast) {
  switch (mount.status) {
    case "filling":
      return mount.daysToFull !== null ? `Full ${daysLabel(mount.daysToFull)}` : "Filling"
    case "shrinking":
      return "Shrinking"
    case "steady":
      return "Not filling"
    default:
      return "Gathering history"
  }
}

/**
 * Under a week is the red one, under a month worth a look; further than that
 * a forecast is a trend to know about rather than a date to act by.
 */
export function mountTone(mount: MountForecast): Tone {
  if (mount.daysToFull === null) return "default"
  if (mount.daysToFull < 7) return "danger"
  if (mount.daysToFull < 30) return "warning"
  return "default"
}

/** "+2.0 GB a day", "−512.0 MB a day", "steady". */
export function growthText(bytesPerDay: number | null) {
  if (bytesPerDay === null) return "—"
  if (Math.abs(bytesPerDay) < 1 << 20) return "steady"
  const sign = bytesPerDay > 0 ? "+" : "−"
  return `${sign}${bytes(Math.abs(bytesPerDay))} a day`
}

/** "high confidence (R² 0.98)". */
export function confidenceText(confidence: ForecastConfidence | undefined, r2: number | null) {
  if (!confidence) return ""
  return r2 === null ? `${confidence} confidence` : `${confidence} confidence (R² ${r2.toFixed(2)})`
}

/** "fitted to 96 hourly means over 4 days". */
export function basisText(points: number, from: string | null, to: string | null) {
  if (!from || !to || points === 0) return "no samples yet"
  const days = (Date.parse(to) - Date.parse(from)) / 86_400_000
  const over =
    days >= 2 ? `${Math.floor(days)} days` : `${Math.max(1, Math.round(days * 24))} hours`
  return `fitted to ${points} hourly ${points === 1 ? "mean" : "means"} over ${over}`
}

/** The apps the list draws: those whose volumes have a line, fastest first. */
export function growingApps(apps: AppGrowth[], limit = 5) {
  return apps.slice(0, limit)
}

export const FORECAST_RULE =
  "Each line is a least-squares fit through hourly means of the recorded samples, so it says when a disk fills at the rate it has been growing — not what next month's traffic will do. R² is how well the samples agree with the line: high needs 0.8 over three days, medium 0.5 over one."
