import type { DotTone } from "@/components/status-dot"
import type { PathRequest, PathResult } from "@/lib/network-investigator-types"
import type { ProbeResult } from "@/lib/types"

export type DiagnosticRequest = {
  tool: string
  target: string
  port?: number
  record?: string
  option?: string
  verify?: string
}

/** How a fact was obtained; an inference is never presented as a measurement. */
export type ProbeFact = { label: string; value: string; basis?: string }
export type ProbeStage = {
  id: string
  label: string
  status: "passed" | "warning" | "failed" | "skipped" | "unknown"
  detail?: string
  duration?: string
}
export type ProbeTable = {
  id: string
  title: string
  columns: string[]
  rows: string[][]
  rowLinks?: string[]
  note?: string
}
export type ProbeFinding = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  owner?: string
  action?: string
  href?: string
}
export type ProbeLink = { label: string; href: string }
export type ProbeMetric = { key: string; label: string; value: number; unit?: string }

/** A probe result with the structured evidence every server tool now reports. */
export type DiagnosticResult = ProbeResult & {
  verdict?: "ok" | "findings" | "unknown" | "failed"
  summary?: string
  facts?: ProbeFact[]
  stages?: ProbeStage[]
  tables?: ProbeTable[]
  findings?: ProbeFinding[]
  links?: ProbeLink[]
  metrics?: ProbeMetric[]
  limitations?: string[]
  /** A quick result the server holds briefly so it can be saved without rerunning. */
  resultId?: string
}

export type DiagnosticHistoryPoint = {
  id: string
  name: string
  createdAt: string
  status: DiagnosticStatus
  outcome?: string
  verdict?: string
  summary?: string
  metrics: ProbeMetric[]
}
export type DiagnosticHistory = {
  request: DiagnosticRequest
  points: DiagnosticHistoryPoint[]
  limitations: string[]
}
export type MetricChange = {
  key: string
  label: string
  unit?: string
  before?: number
  after?: number
}

export type SSHTrustedKey = { type: string; fingerprint: string }
export type SSHTrust = {
  target: string
  keys: SSHTrustedKey[]
  source: "observed" | "entered"
  savedAt: string
  savedBy: string
}
export type WakeDevice = {
  id: string
  name: string
  mac: string
  interface: string
  verify?: string
  port?: number
  createdAt: string
  updatedAt: string
  createdBy: string
}

export type DiagnosticStatus =
  "queued" | "running" | "cancelling" | "completed" | "failed" | "cancelled" | "interrupted"

export type DiagnosticRun = {
  id: string
  kind?: "investigation"
  investigationRequest?: PathRequest
  investigation?: PathResult
  name: string
  request: DiagnosticRequest
  scope: {
    vantage: string
    source?: string
    sourceAddress?: string
    address?: string
    mark?: string
    target?: string
    family: string
    protocol?: string
    port?: number
    interface?: string
    limitations: string[]
  }
  status: DiagnosticStatus
  outcome?: string
  outcomeSource?: "context" | "tool_result" | "error_text" | "recovery" | "path_evidence"
  createdAt: string
  startedAt?: string
  endedAt?: string
  updatedAt: string
  createdBy: string
  rerunOf?: string
  jobId?: string
  stages: {
    id: string
    status: string
    startedAt?: string
    endedAt?: string
    outcome?: string
  }[]
  result?: DiagnosticResult
  hasResult: boolean
  resultTruncated: boolean
  error?: string
}

export type DiagnosticRetention = { maxRuns: number; maxAgeHours: number }
export type DiagnosticDifference = {
  added: string[]
  removed: string[]
  unchanged: number
  truncated: boolean
}
export type DiagnosticComparison = {
  kind?: "investigation"
  investigationRequest?: PathRequest
  beforeId: string
  afterId: string
  request: DiagnosticRequest
  status: { before: string; after: string }
  outcome: { before: string; after: string }
  duration: { before: string; after: string }
  durationDeltaMs?: number
  records: DiagnosticDifference
  output: DiagnosticDifference
  metrics?: MetricChange[]
  partial: boolean
  limitations: string[]
}

export const diagnosticFinished = (run: Pick<DiagnosticRun, "status">) =>
  ["completed", "failed", "cancelled", "interrupted"].includes(run.status)

export function diagnosticCompatible(a: DiagnosticRun, b: DiagnosticRun) {
  const key = (request: DiagnosticRequest) =>
    JSON.stringify([
      request.tool,
      request.target,
      request.port ?? 0,
      request.record ?? "",
      request.option ?? "",
      request.verify ?? "",
    ])
  const pathKey = (request?: PathRequest) =>
    request &&
    JSON.stringify([
      request.sourceKind,
      request.containerId ?? "",
      request.sourceAddress ?? "",
      request.target,
      request.address ?? "",
      request.family,
      request.protocol,
      request.port,
      request.mark ?? "",
      request.measure,
    ])
  const same =
    a.kind === "investigation" || b.kind === "investigation"
      ? a.kind === b.kind &&
        Boolean(a.investigationRequest && b.investigationRequest) &&
        pathKey(a.investigationRequest) === pathKey(b.investigationRequest)
      : key(a.request) === key(b.request)
  return (
    a.id !== b.id &&
    diagnosticFinished(a) &&
    diagnosticFinished(b) &&
    a.hasResult &&
    b.hasResult &&
    same
  )
}

export function diagnosticDuration(run: Pick<DiagnosticRun, "kind" | "result" | "investigation">) {
  if (run.kind !== "investigation") return run.result?.duration || "Not recorded"
  const report = run.investigation
  if (!report) return "Not recorded"
  const elapsed = Date.parse(report.endedAt) - Date.parse(report.startedAt)
  if (!Number.isFinite(elapsed) || elapsed < 0) return "Not recorded"
  return `${elapsed} ms to collect the report`
}

export function diagnosticNameProblem(name: string) {
  const clean = name.trim()
  if (!clean) return "Give this run a name."
  if (new TextEncoder().encode(clean).length > 100) return "Use a name of at most 100 UTF-8 bytes."
  if (/\p{Cc}/u.test(clean)) return "Remove control characters from the name."
}

export function diagnosticRetentionProblem(runs: string, hours: string) {
  if (!/^\d+$/.test(runs) || Number(runs) < 1 || Number(runs) > 256)
    return "Retain between 1 and 256 finished runs."
  if (!/^\d+$/.test(hours) || Number(hours) < 1 || Number(hours) > 2160)
    return "Retain runs for between 1 and 2160 hours."
}

const OUTCOMES: Record<string, { label: string; tone: DotTone }> = {
  completed_with_unknowns: { label: "Report completed with unknowns", tone: "warning" },
  completed: { label: "Completed", tone: "notice" },
  completed_with_findings: { label: "Completed with findings", tone: "warning" },
  unsupported: { label: "Unsupported on this host", tone: "notice" },
  permission_denied: { label: "Permission denied", tone: "warning" },
  dns_failure: { label: "DNS failure", tone: "warning" },
  refused: { label: "Connection refused", tone: "warning" },
  timed_out: { label: "Timed out", tone: "warning" },
  invalid_certificate: { label: "Invalid certificate", tone: "warning" },
  failed: { label: "Probe failed", tone: "warning" },
  cancelled: { label: "Cancelled", tone: "stopped" },
  interrupted: { label: "Interrupted", tone: "warning" },
}

export function diagnosticReading(run: Pick<DiagnosticRun, "status" | "outcome">) {
  if (run.status === "queued") return { label: "Queued", tone: "warning" as const }
  if (run.status === "running") return { label: "Running", tone: "running" as const }
  if (run.status === "cancelling") return { label: "Stopping", tone: "warning" as const }
  return OUTCOMES[run.outcome ?? run.status] ?? { label: "No answer recorded", tone: "unknown" }
}

/** The verdict a result is read with. An unanswered probe is inconclusive, not down. */
export function resultReading(
  result: Pick<DiagnosticResult, "ok" | "verdict">,
  successLabel = "answered",
): { label: string; tone: DotTone } {
  switch (result.verdict) {
    case "ok":
      return { label: successLabel, tone: "running" }
    case "findings":
      return { label: "answered with findings", tone: "warning" }
    case "unknown":
      return { label: "inconclusive", tone: "unknown" }
    case "failed":
      return { label: "no answer", tone: "danger" }
  }
  return result.ok
    ? { label: successLabel, tone: "running" }
    : { label: "no answer", tone: "danger" }
}

const STAGE_TONES: Record<ProbeStage["status"], DotTone> = {
  passed: "running",
  warning: "warning",
  failed: "danger",
  skipped: "stopped",
  unknown: "unknown",
}

export const stageTone = (status: ProbeStage["status"]): DotTone => STAGE_TONES[status] ?? "unknown"

const BASES: Record<string, string> = {
  observed: "observed",
  configured: "configured",
  self_reported: "self-reported",
  inferred: "inferred",
  registry: "registry",
  unknown: "unknown",
}

export const basisLabel = (basis?: string) => (basis ? (BASES[basis] ?? basis) : "")

export function formatMetric(metric: Pick<ProbeMetric, "value" | "unit">) {
  const value = Number.isInteger(metric.value)
    ? String(metric.value)
    : String(Math.round(metric.value * 1000) / 1000)
  if (!metric.unit) return value
  return metric.unit === "%" ? `${value}%` : `${value} ${metric.unit}`
}

/** True when a result carries structured evidence beyond its records and text. */
export function hasEvidence(result: DiagnosticResult) {
  return Boolean(
    result.summary ||
    result.facts?.length ||
    result.stages?.length ||
    result.tables?.length ||
    result.findings?.length ||
    result.metrics?.length,
  )
}

/** One metric across a request's retained runs, oldest first, gaps kept as null. */
export function historySeries(history: DiagnosticHistory, key: string) {
  return history.points.map((point) => ({
    id: point.id,
    at: point.createdAt,
    value: point.metrics.find((metric) => metric.key === key)?.value ?? null,
  }))
}

/** The metric keys that appear in any retained run, in first-seen order. */
export function historyKeys(history: DiagnosticHistory) {
  const seen = new Map<string, { key: string; label: string; unit?: string }>()
  for (const point of history.points)
    for (const metric of point.metrics)
      if (!seen.has(metric.key))
        seen.set(metric.key, { key: metric.key, label: metric.label, unit: metric.unit })
  return [...seen.values()]
}

const SSH_TYPES = new Set([
  "ssh-ed25519",
  "ssh-rsa",
  "ecdsa-sha2-nistp256",
  "ecdsa-sha2-nistp384",
  "ecdsa-sha2-nistp521",
  "sk-ssh-ed25519@openssh.com",
  "sk-ecdsa-sha2-nistp256@openssh.com",
  "ssh-dss",
])

export const sshKeyTypes = [...SSH_TYPES]

/** The host keys an SSH scan recorded as `type SHA256:…`. */
export function observedSSHKeys(result: Pick<ProbeResult, "records">): SSHTrustedKey[] {
  return (result.records ?? []).flatMap((record) => {
    const [type, fingerprint] = record.split(" ")
    return SSH_TYPES.has(type) && sshFingerprintProblem(fingerprint ?? "") === undefined
      ? [{ type, fingerprint }]
      : []
  })
}

export function sshFingerprintProblem(fingerprint: string) {
  if (!/^SHA256:[A-Za-z0-9+/]{43}$/.test(fingerprint.trim()))
    return "Paste the SHA256 fingerprint as ssh-keygen -l prints it: SHA256: and 43 characters."
}

/** The key a trust entry is saved under, matching the backend's canonical host:port. */
export function sshTrustTarget(target: string, port: number) {
  let host = target.trim().replace(/\.$/, "").toLowerCase()
  if (host.includes(":")) host = `[${host}]`
  return `${host}:${port || 22}`
}

export function wakeDeviceProblem(device: Pick<WakeDevice, "name" | "mac" | "interface">) {
  if (!device.name.trim()) return "Give the device a name."
  const mac = device.mac.trim()
  if (!/^([0-9a-f]{2}[:-]){5}[0-9a-f]{2}$/i.test(mac)) return "Use a MAC like 00:11:22:33:44:55."
  if (parseInt(mac.slice(0, 2), 16) & 1) return "A multicast or broadcast MAC cannot be woken."
  if (!/^[A-Za-z0-9._@-]{1,15}$/.test(device.interface.trim()))
    return "Give the LAN interface name, such as eno1."
}
