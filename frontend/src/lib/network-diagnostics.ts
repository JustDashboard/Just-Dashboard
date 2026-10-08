import type { DotTone } from "@/components/status-dot"
import type { PathRequest, PathResult } from "@/lib/network-investigator-types"
import type { ProbeResult } from "@/lib/types"

export type DiagnosticRequest = {
  tool: string
  target: string
  port?: number
  record?: string
  option?: string
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
  result?: ProbeResult
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
