import type {
  ImageScan,
  ImageScanBadge,
  ImageScanPolicy,
  ImageScanReading,
  VulnerabilityCounts,
  VulnerabilityFinding,
  VulnerabilityOrigin,
  VulnerabilitySeverity,
} from "@/lib/types"
import type { DotTone } from "@/components/status-dot"

/*
 * The base-image vulnerability watch's words (`deploy/image_scan.go`), kept
 * free of React so `bun test` reads them: what a scan's counts say, what a
 * release's row says, and when a rebuild is worth offering.
 *
 * A scan counts everything Trivy finds, and most of it is nobody's to fix
 * from here: a development tool's binary shipped with the application
 * (esbuild's Go was 148 of one release's 183), or a base image that is already
 * the newest under its tag. So what the overview and a release's row say is
 * only what the reader can act on (`action` on a finding): an upgrade of the
 * application's own package, or a rebuild that picks up a fix. The rest is in
 * the report, under whose it is.
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

/** Whose a finding is; the server says, and a finding it has not marked reads from its marks. */
export function originOf(finding: VulnerabilityFinding): VulnerabilityOrigin {
  if (finding.origin) return finding.origin
  if (finding.fromBase) return "base"
  if (finding.dependency === "development") return "development"
  return "app"
}

/** The report's sections, in the order a reader owns them. */
export const ORIGINS: VulnerabilityOrigin[] = ["app", "development", "base"]

/** The fixable findings at or above the floor that an upgrade or a rebuild fixes. */
export function actionableFindings(scan: ImageScan) {
  return scan.summary.fixable.filter(
    (finding) => Boolean(finding.action) && atLeast(finding.severity, scan.floor),
  )
}

/**
 * How many findings at or above the floor there are to fix. The server counts
 * them before it cuts the listed findings at their bound, so a list filled by a
 * development tool's findings never makes this read as nothing.
 */
export function toFixCount(scan: ImageScan) {
  return scan.actionable
    ? countAtLeast(scan.actionable, scan.floor)
    : actionableFindings(scan).length
}

/** "1 critical, 2 high", or nothing: how many of each, every severity. */
const totalOf = (counts: VulnerabilityCounts | undefined) =>
  counts ? countAtLeast(counts, "LOW") : 0

/** The base images a scan read, by the reference the recipe names. */
export function baseNames(scan: ImageScan) {
  return scan.targets.filter((target) => target.kind === "base").map((target) => target.reference)
}

/** Where a scan's findings are, as a sentence's parts: "149 in development tools shipped with it". */
export function originParts(scan: ImageScan) {
  const origins = scan.origins
  if (!origins) return []
  const bases = baseNames(scan)
  const base = bases.length === 1 ? `the ${bases[0]} base image` : "the base images"
  const words: Record<VulnerabilityOrigin, string> = {
    app: "your app's packages",
    development: "development tools shipped with it",
    base,
  }
  return ORIGINS.filter((origin) => totalOf(origins[origin]) > 0).map(
    (origin) => `${totalOf(origins[origin]).toLocaleString("en-US")} in ${words[origin]}`,
  )
}

/** "a, b and c". */
export function joinAnd(parts: string[]) {
  if (parts.length <= 1) return parts.join("")
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`
}

/**
 * The overview's sentence: what there is to fix at the floor, or where what
 * was found is when there is nothing. Never a bare total: 183 read as 183
 * things wrong with the application when none of them were.
 */
export function overviewLead(scan: ImageScan) {
  const total = totalOf(scan.summary.counts)
  const release = `release ${scan.releaseNumber}`
  const toFix = toFixCount(scan)
  if (toFix > 0)
    return { title: `${toFix} ${floorWords(scan.floor)} to fix in ${release}`, where: "" }
  if (total === 0) return { title: `No known vulnerabilities in ${release}`, where: "" }
  const parts = originParts(scan)
  const unfixedOwn = scan.summary.unfixed.filter((finding) => originOf(finding) === "app").length
  const where = [
    parts.length > 0 && `${total.toLocaleString("en-US")} known: ${joinAnd(parts)}.`,
    unfixedOwn > 0 &&
      `${unfixedOwn} ${floorWords(scan.floor)} in your app's packages ${unfixedOwn === 1 ? "has" : "have"} no fix yet.`,
  ]
    .filter(Boolean)
    .join(" ")
  return { title: `Nothing ${floorWords(scan.floor)} to fix in ${release}`, where }
}

/** One thing the overview offers: an upgrade of a package, or a rebuild. */
export type ScanAction =
  | { kind: "upgrade"; group: FindingGroup }
  | {
      kind: "rebuild"
      counts: VulnerabilityCounts
      packages: string[]
      worst: VulnerabilitySeverity
      /** A base's tag names a newer image; otherwise the build's own packages. */
      newerBase: boolean
    }

/**
 * What the reader can do, one row each: a rebuild first, since one run takes
 * every fix it brings, then one upgrade per package, worst first.
 */
export function scanActions(scan: ImageScan): ScanAction[] {
  const findings = actionableFindings(scan)
  const actions: ScanAction[] = []
  const rebuild = findings.filter((finding) => finding.action === "rebuild")
  if (rebuild.length > 0) {
    const counts: VulnerabilityCounts = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 }
    for (const finding of rebuild)
      if (finding.severity !== "UNKNOWN") counts[COUNT_KEY[finding.severity]]++
    actions.push({
      kind: "rebuild",
      counts,
      packages: [...new Set(rebuild.map((finding) => finding.package))],
      worst: rebuild.reduce<VulnerabilitySeverity>(
        (worst, finding) => (RANK[finding.severity] > RANK[worst] ? finding.severity : worst),
        "UNKNOWN",
      ),
      newerBase: rebuild.some((finding) => originOf(finding) === "base") && scan.newerBase,
    })
  }
  for (const group of groupFindings(findings.filter((finding) => finding.action === "upgrade")))
    actions.push({ kind: "upgrade", group })
  return actions
}

/**
 * An upgrade as the command it is: "Upgrade next to 16.4.7". Go's standard
 * library is not a package anybody upgrades; it is the Go a binary was built
 * with.
 */
export function upgradeTitle(group: FindingGroup) {
  if (group.package === "stdlib" && group.class === "lang-pkgs")
    return `Build with Go ${group.fix?.replace(/^v/, "") ?? "a newer release"}`
  return group.fix ? `Upgrade ${group.package} to ${group.fix}` : `Upgrade ${group.package}`
}

/** Where a package came from, in words: "a direct dependency", "comes with next". */
export function dependencyWords(finding: Pick<FindingGroup, "dependency" | "via" | "package">) {
  if (finding.dependency === "direct" && finding.via === finding.package) return "in package.json"
  if (finding.dependency === "direct") return finding.via ? `installed as ${finding.via}` : ""
  if (finding.via && finding.via !== finding.package) return `comes with ${finding.via}`
  return ""
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
 * the floor that a base carries, and a newer image under that base's tag,
 * which the server marks as the finding's rebuild. Another base moving is
 * not it: a newer Bun does not fix what Node carries.
 */
export function vulnerable(scan: ImageScan | null | undefined) {
  if (!scan || scan.state !== "scanned" || !scan.newerBase) return false
  return scan.summary.fixable.some(
    (finding) =>
      originOf(finding) === "base" &&
      finding.action === "rebuild" &&
      atLeast(finding.severity, scan.floor),
  )
}

/**
 * Whether the overview links to the report: as soon as there is a live release
 * that can be scanned, scanned yet or not, since Scan now is the report's verb
 * and the first scan is asked for there. A scan already read keeps its link.
 */
export function reportReachable(reading: ImageScanReading | undefined) {
  if (!reading) return false
  if (reading.scan?.finishedAt) return true
  return Boolean(reading.releaseId && reading.policy.enabled && reading.available)
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
  const toFix = toFixCount(scan)
  if (toFix > 0) return { tone: "danger", line: `${toFix} to fix` }
  return { tone: "running", line: `Nothing ${floorWords(scan.floor)} to fix` }
}

/**
 * A release's mark in the releases list: drawn only when its scan found
 * something at or above the floor that an upgrade or a rebuild fixes. A
 * development tool's finding, or a base's with no newer image, is nothing the
 * reader can do anything about from a row.
 */
export function badgeOf(
  badge: ImageScanBadge | undefined,
): { label: string; tone: DotTone; title: string } | undefined {
  if (!badge || !badge.actionable) return undefined
  const how = badge.newerBase
    ? "an upgrade, or a rebuild on the newer base image, fixes"
    : "an upgrade or a rebuild fixes"
  return {
    label: `${badge.actionable} to fix`,
    tone: "danger",
    title: `${badge.actionable} ${floorWords(badge.floor)} that ${how}.`,
  }
}

/** A severity's colour on the bar and its key: the worst red, down to a quiet blue. */
export const SEVERITY_COLOR: Record<Exclude<VulnerabilitySeverity, "UNKNOWN">, string> = {
  CRITICAL: "var(--destructive)",
  HIGH: "color-mix(in oklab, var(--destructive) 62%, var(--warning))",
  MEDIUM: "var(--warning)",
  LOW: "var(--chart-2)",
}

/** The report's sentence: how many, in which release, and whose they are. */
export function scanLead(scan: ImageScan) {
  const total = totalOf(scan.summary.counts)
  if (total === 0)
    return { title: `No known vulnerabilities in release ${scan.releaseNumber}`, where: undefined }
  const noun = total === 1 ? "vulnerability" : "vulnerabilities"
  const parts = originParts(scan)
  return {
    title: `${total.toLocaleString("en-US")} known ${noun} in release ${scan.releaseNumber}`,
    where: parts.length > 0 ? `${joinAnd(parts)}` : undefined,
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
  /** Where this copy is in the image, when the scan kept it. */
  path?: string
  class?: string
  dependency?: VulnerabilityFinding["dependency"]
  via?: string
  origin: VulnerabilityOrigin
  fromBase: boolean
  findings: VulnerabilityFinding[]
  counts: VulnerabilityCounts
  worst: VulnerabilitySeverity
}

/**
 * The findings as what there is to do: one upgrade per package, however many
 * advisories it answers. Six rows naming undici and brace-expansion were two
 * upgrades. Two copies of a package at two paths (npm's own glob and the
 * application's) are two. Worst first, keeping the server's order within a
 * severity.
 */
export function groupFindings(findings: VulnerabilityFinding[]): FindingGroup[] {
  const groups = new Map<string, FindingGroup>()
  for (const finding of findings) {
    const key = `${finding.package}\u0000${finding.installed}\u0000${finding.path ?? finding.target}`
    let group = groups.get(key)
    if (!group) {
      group = {
        package: finding.package,
        installed: finding.installed,
        target: finding.target,
        path: finding.path,
        class: finding.class,
        dependency: finding.dependency,
        via: finding.via,
        origin: originOf(finding),
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
export function findingProduct(
  group: Pick<FindingGroup, "class" | "target"> & { package?: string },
) {
  // Go's standard library inside a binary (esbuild's, a Go server's).
  if (group.package === "stdlib" && group.class === "lang-pkgs") return "go"
  const target = group.target.toLowerCase()
  const os = OS_PRODUCTS.find((name) => target.includes(name))
  if (group.class === "os-pkgs") return os ?? "linux"
  const lock = LOCK_PRODUCTS.find(([pattern]) => pattern.test(group.target))
  return lock?.[1] ?? os
}
