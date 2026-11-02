import type {
  ImageScan,
  ImageScanBadge,
  ImageScanPolicy,
  ImageScanReading,
  VulnerabilityCounts,
  VulnerabilityFinding,
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

/** A severity's colour on the bar and its key: the worst red, down to a quiet blue. */
export const SEVERITY_COLOR: Record<Exclude<VulnerabilitySeverity, "UNKNOWN">, string> = {
  CRITICAL: "var(--destructive)",
  HIGH: "color-mix(in oklab, var(--destructive) 62%, var(--warning))",
  MEDIUM: "var(--warning)",
  LOW: "var(--chart-2)",
}

/** The scan's sentence: how many, in which release, and how many can be fixed. */
export function scanLead(scan: ImageScan) {
  const { counts, fixableCounts } = scan.summary
  const total = countAtLeast(counts, "LOW")
  const fixable = countAtLeast(fixableCounts, "LOW")
  if (total === 0)
    return { title: `No known vulnerabilities in release ${scan.releaseNumber}`, fix: undefined }
  const noun = total === 1 ? "vulnerability" : "vulnerabilities"
  return {
    title: `${total} known ${noun} in release ${scan.releaseNumber}`,
    fix:
      fixable === 0
        ? "None has a fix yet"
        : fixable === total
          ? total === 1
            ? "It has a fix"
            : `All ${total} have a fix`
          : `${fixable} of ${total} ${fixable === 1 ? "has" : "have"} a fix`,
  }
}

/**
 * Trivy repeats the package in front of its titles, sometimes twice
 * ("undici: undici: Denial of Service"), beside a name already on the row.
 */
export function cleanTitle(title: string | undefined, pkg: string) {
  if (!title) return undefined
  const prefix = `${pkg.toLowerCase()}:`
  let text = title.trim()
  while (text.toLowerCase().startsWith(prefix)) text = text.slice(prefix.length).trim()
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : undefined
}

const compareVersions = (a: string, b: string) =>
  a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" })

/**
 * The version to move to, from Trivy's list of every release line that has the
 * fix ("6.29.1, 7.30.1, 8.12.2"): the one on the line already installed, since
 * that is the upgrade nobody has to think about; the first listed otherwise.
 */
export function nearestFix(installed: string, fixed: string | undefined) {
  if (!fixed) return undefined
  const listed = fixed
    .split(",")
    .map((version) => version.trim())
    .filter(Boolean)
  const line = installed.split(".")[0]
  const sameLine = listed
    .filter((version) => version.split(".")[0] === line)
    .filter((version) => compareVersions(version, installed) >= 0)
    .sort(compareVersions)
  return sameLine[0] ?? listed[0]
}

export type FindingGroup = {
  package: string
  installed: string
  /** The one version that fixes every finding here. */
  fix?: string
  target: string
  class?: string
  fromBase: boolean
  findings: VulnerabilityFinding[]
  counts: VulnerabilityCounts
  worst: VulnerabilitySeverity
}

/**
 * The findings as what there is to do: one upgrade per package, however many
 * advisories it answers. Six rows naming undici and brace-expansion were two
 * upgrades. Worst first, keeping the server's order within a severity.
 */
export function groupFindings(findings: VulnerabilityFinding[]): FindingGroup[] {
  const groups = new Map<string, FindingGroup>()
  for (const finding of findings) {
    const key = `${finding.package}\u0000${finding.installed}\u0000${finding.target}`
    let group = groups.get(key)
    if (!group) {
      group = {
        package: finding.package,
        installed: finding.installed,
        target: finding.target,
        class: finding.class,
        fromBase: false,
        findings: [],
        counts: { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 },
        worst: finding.severity,
      }
      groups.set(key, group)
    }
    group.findings.push(finding)
    group.fromBase ||= Boolean(finding.fromBase)
    const fix = nearestFix(finding.installed, finding.fixed)
    if (fix && (!group.fix || compareVersions(fix, group.fix) > 0)) group.fix = fix
    if (finding.severity !== "UNKNOWN") group.counts[COUNT_KEY[finding.severity]]++
    if (RANK[finding.severity] > RANK[group.worst]) group.worst = finding.severity
  }
  return [...groups.values()].sort((a, b) => RANK[b.worst] - RANK[a.worst])
}

const LOCK_PRODUCTS: [RegExp, string][] = [
  [/package-lock\.json|node\.js|npm/i, "npm"],
  [/yarn\.lock/i, "yarn"],
  [/pnpm-lock/i, "pnpm"],
  [/bun\.lock/i, "bun"],
  [/go\.(mod|sum)|gobinary|\bgo\b/i, "go"],
  [/cargo|rust/i, "rust"],
  [/poetry\.lock|pipfile|requirements|uv\.lock|python/i, "python"],
  [/gemfile|ruby/i, "ruby"],
  [/composer|php/i, "php"],
  [/pom\.xml|\.jar|gradle|java/i, "java"],
  [/\.deps\.json|packages\.lock|dotnet|\.net/i, "dotnet"],
]

const OS_PRODUCTS = ["alpine", "debian", "ubuntu", "centos", "fedora", "rocky", "almalinux", "arch"]

/**
 * The mark a finding's package is drawn on: the distribution for an OS package,
 * the ecosystem for a library. A Compose service is in front of the target
 * ("web · Node.js"), so both read the whole string.
 */
export function findingProduct(group: Pick<FindingGroup, "class" | "target">) {
  const target = group.target.toLowerCase()
  const os = OS_PRODUCTS.find((name) => target.includes(name))
  if (group.class === "os-pkgs") return os ?? "linux"
  const lock = LOCK_PRODUCTS.find(([pattern]) => pattern.test(group.target))
  return lock?.[1] ?? os
}
