export type ExternalScope = {
  id: string
  target: string
  addresses: string[]
  ports: number[]
  families: ("inet" | "inet6")[]
}
export type ExternalVantage = {
  id: string
  name: string
  location: string
  placement: "external_host" | "controlled_fixture"
  scopes: ExternalScope[]
  createdAt: string
  enrolledAt?: string
  lastSeen?: string
  lastIp?: string
  revokedAt?: string
}
export type ExternalEnrollment = {
  vantage: ExternalVantage
  token: string
  expiresAt: string
  serverKey: string
}
export type ExternalRequest = {
  vantageId: string
  scopeId: string
  family: "inet" | "inet6"
  port: number
  tls: boolean
}
export type ExternalStage = {
  name: "dns" | "tcp" | "tls"
  basis: "observed" | "measured" | "unknown"
  state: string
  startedAt: string
  endedAt: string
  detail: string
  durationMs: number
}
export type ExternalResult = {
  checkId: string
  vantageId: string
  request: ExternalRequest
  target: string
  addresses: string[]
  address?: string
  sourceAddress?: string
  startedAt: string
  endedAt: string
  stages: ExternalStage[]
  certificate?: { sha256: string; subject: string; issuer: string; expiresAt: string }
  limitations: string[]
}
export type ExternalCheck = {
  id: string
  vantageId: string
  request: ExternalRequest
  status: "queued" | "running" | "completed" | "failed" | "cancelled" | "expired"
  createdAt: string
  expiresAt: string
  leasedAt?: string
  completedAt?: string
  startedBy: string
  result?: ExternalResult
}
export type EnrollmentDraft = {
  name: string
  location: string
  placement: ExternalVantage["placement"]
  target: string
  addresses: string
  ports: string
  family: "inet" | "inet6" | "both"
}
export const newEnrollmentDraft = (): EnrollmentDraft => ({
  name: "",
  location: "",
  placement: "external_host",
  target: "",
  addresses: "",
  ports: "443",
  family: "inet",
})
export function enrollmentRequest(draft: EnrollmentDraft) {
  const addresses = draft.addresses.split(/[\s,]+/).filter(Boolean)
  const ports = draft.ports.split(/[\s,]+/).filter(Boolean)
  if (
    !draft.name.trim() ||
    !draft.target.trim() ||
    addresses.length < 1 ||
    addresses.length > 16 ||
    ports.length < 1 ||
    ports.length > 8 ||
    ports.some((port) => !/^\d{1,5}$/.test(port) || Number(port) < 1 || Number(port) > 65535)
  )
    return undefined
  return {
    name: draft.name.trim(),
    location: draft.location.trim(),
    placement: draft.placement,
    scopes: [
      {
        id: "service",
        target: draft.target.trim(),
        addresses,
        ports: ports.map(Number),
        families: draft.family === "both" ? ["inet", "inet6"] : [draft.family],
      },
    ],
  }
}
export const activeCheck = (check: ExternalCheck) =>
  check.status === "queued" || check.status === "running"
export function stageReading(check: ExternalCheck, name: ExternalStage["name"]) {
  const stage = check.result?.stages.find((item) => item.name === name)
  if (!stage) return { label: "Unknown", detail: "No accepted agent measurement", good: false }
  const labels: Record<string, string> = {
    resolved: "Resolved",
    connected: "Connected",
    verified: "Verified",
    failed: "Failed",
    unavailable: "Unavailable",
    refused: "Refused",
    skipped: "Skipped",
    not_applicable: "Literal target",
    not_requested: "Not requested",
  }
  return {
    label: labels[stage.state] ?? "Unknown",
    detail: `${stage.basis} · ${stage.detail}`,
    good: stage.basis === "measured" && ["resolved", "connected", "verified"].includes(stage.state),
  }
}
export function checkCounts(vantages: ExternalVantage[], checks: ExternalCheck[]) {
  return {
    enrolled: vantages.filter((v) => v.enrolledAt && !v.revokedAt).length,
    pending: checks.filter(activeCheck).length,
    retained: checks.filter((check) => check.result).length,
    unknown: checks.filter((check) => !activeCheck(check) && !check.result).length,
  }
}
