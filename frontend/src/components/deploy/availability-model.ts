import type { AvailabilityDay, AvailabilityWindow } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"
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

/** One window as the readout says it: the figure, what is behind it, its tone. */
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

/** A window's name in the readout under the ribbon. */
export const WINDOW_SHORT: Record<string, string> = {
  "24h": "24 hours",
  "7d": "7 days",
  "30d": "30 days",
}

/** "the last 2 days": the window, or as much of it as the record reaches. */
function reachWords(window: AvailabilityWindow) {
  const seconds =
    window.coverageSeconds > 0 && window.coverageSeconds < window.seconds
      ? window.coverageSeconds
      : window.seconds
  if (seconds >= 2 * 86400) return `the last ${Math.round(seconds / 86400)} days`
  if (seconds >= 2 * 3600) return `the last ${Math.round(seconds / 3600)} hours`
  return "the last hour"
}

/**
 * The section's sentence, over the longest window with a figure. It names how
 * far back the record really reaches, so a steady two days never reads as a
 * steady month, and says whether the downtime is recent.
 */
export function availabilityLead(data: { down: boolean; windows: AvailabilityWindow[] }): {
  title: string
  hint?: string
  tone: DotTone
} {
  const figures = data.windows.filter((w) => w.ratio !== undefined && w.observedSeconds > 0)
  const widest = figures.at(-1)
  if (!widest && !data.down)
    return {
      title: "Nothing recorded yet",
      hint: "The record fills in while a release is live.",
      tone: "unknown",
    }
  const lost =
    widest && widest.downSeconds > 0
      ? `${downtimeLabel(widest.downSeconds)} down across ${widest.incidents} ${widest.incidents === 1 ? "incident" : "incidents"}`
      : undefined
  if (data.down) return { title: "The live release is down right now", hint: lost, tone: "danger" }
  const reach = reachWords(widest!)
  if (!lost) return { title: `No downtime in ${reach}`, tone: "running" }
  const day = data.windows.find((w) => w.window === "24h")
  const quiet = day && day.observedSeconds > 0 && day.downSeconds === 0
  const tone = availabilityTone(widest!.ratio, false)
  return {
    title: `Up ${availabilityPercent(widest!.ratio!)} of ${reach}`,
    hint: quiet ? `${lost}, none in the last 24 hours` : lost,
    tone: tone === "danger" ? "danger" : tone === "warning" ? "warning" : "running",
  }
}

/** A stretch's colour on the ribbon: any downtime shows, however little. */
export function dayTone(day: AvailabilityDay): "unknown" | "up" | "dented" | "down" {
  if (day.observedSeconds <= 0) return "unknown"
  if (day.downSeconds <= 0) return "up"
  return 1 - day.downSeconds / day.observedSeconds < DANGER_BELOW ? "down" : "dented"
}

/** What a stretch says on the readout while it is pointed at. */
export function dayWords(day: AvailabilityDay) {
  if (day.observedSeconds <= 0) return "nothing recorded"
  if (day.downSeconds <= 0) return "no downtime"
  return `${downtimeLabel(day.downSeconds)} down · ${day.incidents} ${day.incidents === 1 ? "incident" : "incidents"}`
}
