import type { AvailabilityWindow } from "@/lib/types"
import type { Tone } from "@/components/tone"

/**
 * The availability figure as the page says it.
 *
 * The server computes the share (`deploy/availability.go`); this is the
 * wording — how many decimals a percentage earns, how downtime reads beside
 * it, and when a short record has to be said out loud so three steady days do
 * not pass for a steady month. Pure, so `bun test` holds it.
 */

/** Below this the figure is a warning; below the second it is the bad one. */
const WARNING_BELOW = 0.999
const DANGER_BELOW = 0.99

export const WINDOW_LABELS: Record<string, string> = {
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  "30d": "Last 30 days",
}

/**
 * "99.93%". Two decimals, because the difference between 99.9 and 99.99 is
 * the whole point of the figure — and "100%" only for no downtime at all:
 * 99.996% must never round up to a claim the record does not make.
 */
export function availabilityPercent(ratio: number): string {
  if (ratio >= 1) return "100%"
  const text = (Math.max(0, ratio) * 100).toFixed(2)
  return `${text === "100.00" ? "99.99" : text}%`
}

/** How long something was down, in the two largest units that say it: "3 min", "1 h 12 min", "2 d 3 h". */
export function downtimeLabel(seconds: number): string {
  if (seconds <= 0) return "none"
  if (seconds < 60) return "< 1 min"
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) {
    const rest = minutes % 60
    return rest ? `${hours} h ${rest} min` : `${hours} h`
  }
  const days = Math.floor(hours / 24)
  const restHours = hours % 24
  return restHours ? `${days} d ${restHours} h` : `${days} d`
}

/** The figure's tone: quiet while it is steady, loud as it falls, and the bad one while the release is down now. */
export function availabilityTone(ratio: number | undefined, downNow: boolean): Tone {
  if (downNow) return "danger"
  if (ratio === undefined) return "default"
  if (ratio < DANGER_BELOW) return "danger"
  if (ratio < WARNING_BELOW) return "warning"
  return "default"
}

/**
 * The sentence for a record younger than its window: "record covers 9 of 30
 * days". Nothing while the record reaches the whole window.
 */
export function coverageNote(window: AvailabilityWindow): string | undefined {
  if (window.coverageSeconds <= 0 || window.coverageSeconds >= window.seconds) return undefined
  if (window.seconds >= 2 * 86400) {
    const covered = Math.max(1, Math.round(window.coverageSeconds / 86400))
    return `record covers ${covered} of ${Math.round(window.seconds / 86400)} days`
  }
  const covered = Math.max(1, Math.round(window.coverageSeconds / 3600))
  return `record covers ${covered} of ${Math.round(window.seconds / 3600)} hours`
}

export type AvailabilityReading = {
  value: string
  hint: string
  tone: Tone
}

/** One window as a tile reads it: the figure, the line under it, its tone. */
export function availabilityReading(
  window: AvailabilityWindow,
  downNow: boolean,
): AvailabilityReading {
  const coverage = coverageNote(window)
  if (window.ratio === undefined || window.observedSeconds <= 0) {
    return {
      value: "—",
      hint: window.coverageSeconds > 0 ? "no release was live" : "nothing recorded yet",
      tone: "default",
    }
  }
  const parts: string[] = []
  if (window.downSeconds > 0) {
    parts.push(`${downtimeLabel(window.downSeconds)} down`)
    parts.push(`${window.incidents} incident${window.incidents === 1 ? "" : "s"}`)
  } else parts.push("no downtime recorded")
  if (downNow && window.window === "24h") parts.push("down now")
  if (coverage) parts.push(coverage)
  return {
    value: availabilityPercent(window.ratio),
    hint: parts.join(" · "),
    tone: availabilityTone(window.ratio, downNow),
  }
}
