import type {
  ImageScan,
  ImageScanBadge,
  ImageScanPolicy,
  ImageScanReading,
  VulnerabilityCounts,
  VulnerabilitySeverity,
} from "@/lib/types"
import type { DotTone } from "@/components/status-dot"

/*
 * The base-image vulnerability watch's words (`deploy/image_scan.go`), kept
 * free of React so `bun test` reads them: what a scan's counts say, what a
 * release's row says, and when a rebuild is worth offering.
 */

export const SEVERITIES: Exclude<VulnerabilitySeverity, "UNKNOWN">[] = [
  "CRITICAL",
  "HIGH",
  "MEDIUM",
  "LOW",
]

export const DEFAULT_IMAGE_SCAN_POLICY: ImageScanPolicy = { enabled: true, severityFloor: "HIGH" }

const RANK: Record<VulnerabilitySeverity, number> = {
  CRITICAL: 4,
  HIGH: 3,
  MEDIUM: 2,
  LOW: 1,
  UNKNOWN: 0,
}

export function atLeast(severity: VulnerabilitySeverity, floor: VulnerabilitySeverity) {
  return RANK[severity] > 0 && RANK[severity] >= RANK[floor]
}

const COUNT_KEY: Record<Exclude<VulnerabilitySeverity, "UNKNOWN">, keyof VulnerabilityCounts> = {
  CRITICAL: "critical",
  HIGH: "high",
  MEDIUM: "medium",
  LOW: "low",
}

/** How many findings are at or above the floor. */
export function countAtLeast(counts: VulnerabilityCounts, floor: VulnerabilitySeverity) {
  return SEVERITIES.filter((severity) => atLeast(severity, floor)).reduce(
    (total, severity) => total + counts[COUNT_KEY[severity]],
    0,
  )
}

/** A severity as a reading's tone: the worst two are danger, medium a warning. */
export function severityTone(severity: VulnerabilitySeverity): DotTone {
  if (severity === "CRITICAL" || severity === "HIGH") return "danger"
  if (severity === "MEDIUM") return "warning"
  return "notice"
}

/** "high or worse", the floor as a sentence says it. */
export function floorWords(floor: VulnerabilitySeverity) {
  return floor === "CRITICAL" ? "critical" : `${floor.toLowerCase()} or worse`
}

/** "2 critical and 1 high", the non-zero counts at or above the floor. */
export function countsLine(counts: VulnerabilityCounts, floor: VulnerabilitySeverity = "LOW") {
  const parts = SEVERITIES.filter(
    (severity) => atLeast(severity, floor) && counts[COUNT_KEY[severity]] > 0,
  ).map((severity) => `${counts[COUNT_KEY[severity]]} ${severity.toLowerCase()}`)
  if (parts.length === 0) return ""
  if (parts.length === 1) return parts[0]
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`
}

/** A database's age in the words a footer says it in. */
export function ageWords(hours: number | undefined) {
  if (hours === undefined || !Number.isFinite(hours)) return ""
  if (hours < 1) return "under an hour old"
  if (hours < 48) {
    const whole = Math.round(hours)
    return whole === 1 ? "1 hour old" : `${whole} hours old`
  }
  return `${Math.round(hours / 24)} days old`
}

/**
 * The alert's condition, as the page reads it: a fixable finding at or above
 * the floor, and a base whose tag names a newer image.
 */
export function vulnerable(scan: ImageScan | null | undefined) {
  if (!scan || scan.state !== "scanned" || !scan.newerBase) return false
  return scan.summary.fixable.some((finding) => atLeast(finding.severity, scan.floor))
}

/** Whether a scan is waiting or running, which the panel polls faster for. */
export function inFlight(scan: ImageScan | null | undefined) {
  return scan?.state === "queued" || scan?.state === "scanning"
}

/**
 * What the panel's header line says of the latest scan: the verdict a
 * reader asks first, in one sentence.
 */
export function scanVerdict(reading: ImageScanReading): { tone: DotTone; line: string } {
  const { scan, policy } = reading
  if (!policy.enabled) return { tone: "stopped", line: "Scanning is off for this environment" }
  if (!reading.available) return { tone: "unknown", line: reading.reason ?? "No scanner here" }
  if (!scan) return { tone: "unknown", line: "Not scanned yet" }
  if (scan.state === "queued" && !scan.finishedAt)
    return { tone: "unknown", line: "Waiting to scan" }
  if (scan.state === "scanning" && !scan.finishedAt) return { tone: "unknown", line: "Scanning" }
  if (scan.state === "failed" && !scan.finishedAt)
    return { tone: "warning", line: "Could not scan" }
  const atFloor = countAtLeast(scan.summary.counts, scan.floor)
  const fixable = countAtLeast(scan.summary.fixableCounts, scan.floor)
  if (atFloor === 0)
    return { tone: "running", line: `No ${floorWords(scan.floor)} vulnerabilities` }
  if (vulnerable(scan))
    return {
      tone: "danger",
      line: `${fixable} fixable by a newer base`,
    }
  if (fixable > 0)
    return { tone: "danger", line: `${fixable} ${fixable === 1 ? "has" : "have"} a fix` }
  return { tone: "warning", line: `${atFloor} with no fix yet` }
}

/**
 * A release's mark in the releases list: drawn only when its scan found
 * something at or above the floor. The words say what can be done about it.
 */
export function badgeOf(
  badge: ImageScanBadge | undefined,
): { label: string; tone: DotTone; title: string } | undefined {
  if (!badge || badge.atFloor === 0) return undefined
  const counted = `${countsLine(badge.counts, badge.floor)} at ${floorWords(badge.floor)}`
  if (badge.fixable > 0) {
    const base = badge.newerBase ? "; a newer base image is out, so a rebuild may fix them" : ""
    return {
      label: `${badge.fixable} fixable`,
      tone: "danger",
      title: `${counted}, ${badge.fixable} with a published fix${base}.`,
    }
  }
  return {
    label: `${badge.atFloor} unfixed`,
    tone: "warning",
    title: `${counted}, none with a published fix yet.`,
  }
}

/** The short form of a digest, as the release comparison shows one. */
export function shortDigest(digest: string | undefined) {
  if (!digest) return ""
  const [, hex = digest] = digest.split(":")
  return hex.slice(0, 12)
}
