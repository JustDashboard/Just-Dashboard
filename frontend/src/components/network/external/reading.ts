import type { DotTone } from "@/components/status-dot"
import type { WireLine } from "@/components/wire-flow-model"
import {
  activeCheck,
  type ExternalCheck,
  type ExternalStage,
  type ExternalVantage,
} from "@/lib/network-external"

/**
 * What one check says about a path, in the words the picture, the identity
 * line and the table share. Only a stage the agent *measured* can call a path
 * broken: a check that expired, was cancelled or never reported is "no
 * measurement", drawn dashed and still, never red — a missing agent is not a
 * failed service.
 */
export type CheckOutcome = {
  kind: "reached" | "failed" | "unclear" | "pending" | "none"
  word: string
  tone: DotTone
  line: WireLine
}

const GOOD = ["resolved", "connected", "verified", "not_applicable", "not_requested"]
const BAD = ["failed", "refused", "unavailable"]
const STAGE_WORD: Record<ExternalStage["name"], string> = { dns: "DNS", tcp: "TCP", tls: "TLS" }
const STATE_WORD: Record<string, string> = {
  failed: "failed",
  refused: "refused",
  unavailable: "unavailable",
  skipped: "skipped",
}

export function checkOutcome(check: ExternalCheck | undefined): CheckOutcome {
  if (!check)
    return { kind: "none", word: "not checked yet", tone: "unknown", line: { state: "idle" } }
  if (activeCheck(check))
    return {
      kind: "pending",
      word: check.status === "running" ? "measuring" : "queued",
      tone: "notice",
      line: { state: "up", moving: true, dashed: true },
    }
  const stages = check.result?.stages ?? []
  if (!check.result || stages.length === 0)
    return { kind: "none", word: "no measurement", tone: "unknown", line: { state: "idle" } }
  const broken = stages.find((stage) => stage.basis === "measured" && BAD.includes(stage.state))
  if (broken)
    return {
      kind: "failed",
      word: `${STAGE_WORD[broken.name]} ${STATE_WORD[broken.state] ?? broken.state}`,
      tone: "danger",
      line: { state: "danger" },
    }
  const measured = stages.every((stage) => stage.basis === "measured" && GOOD.includes(stage.state))
  if (measured) return { kind: "reached", word: "reached", tone: "running", line: { state: "up" } }
  return { kind: "unclear", word: "partly unknown", tone: "warning", line: { state: "warning" } }
}

/** When a check last changed: its result, else when it was asked for. */
export const checkTime = (check: ExternalCheck) =>
  check.result?.endedAt ?? check.completedAt ?? check.createdAt

/** Newest first, so the first match of a filter is the last word on it. */
export function newestFirst(checks: ExternalCheck[]) {
  return [...checks].sort((a, b) => checkTime(b).localeCompare(checkTime(a)))
}

/** The scope's target a check asked about, or what its result says it reached. */
export function checkTarget(check: ExternalCheck, vantages: ExternalVantage[]) {
  return (
    check.result?.target ??
    vantages
      .find((v) => v.id === check.vantageId)
      ?.scopes.find((scope) => scope.id === check.request.scopeId)?.target
  )
}

export type ApprovedService = {
  target: string
  ports: number[]
  families: ("inet" | "inet6")[]
  addresses: string[]
}

/**
 * This server's approved services, once each however many sources may check
 * them: the targets of every live source's scopes, with their ports, families
 * and addresses joined.
 */
export function approvedServices(vantages: ExternalVantage[]): ApprovedService[] {
  const byTarget = new Map<string, ApprovedService>()
  for (const vantage of vantages) {
    if (vantage.revokedAt) continue
    for (const scope of vantage.scopes) {
      const known = byTarget.get(scope.target)
      byTarget.set(scope.target, {
        target: scope.target,
        ports: [...new Set([...(known?.ports ?? []), ...scope.ports])].sort((a, b) => a - b),
        families: [...new Set([...(known?.families ?? []), ...scope.families])],
        addresses: [...new Set([...(known?.addresses ?? []), ...scope.addresses])],
      })
    }
  }
  return [...byTarget.values()]
}

export const familyWord = (value: string) => (value === "inet" ? "IPv4" : "IPv6")

/** A source's standing: whether it can be asked for a check at all. */
export function sourceState(vantage: ExternalVantage): { word: string; tone: DotTone } {
  if (vantage.revokedAt) return { word: "Revoked", tone: "stopped" }
  if (vantage.enrolledAt) return { word: "Enrolled", tone: "running" }
  return { word: "Awaiting enrollment", tone: "notice" }
}

/**
 * The page's verdict over the enrolled sources: from how many of them the
 * last word was that a service was reached.
 */
export function reachVerdict(
  vantages: ExternalVantage[],
  checks: ExternalCheck[],
): { word: string; tone: DotTone } {
  const enrolled = vantages.filter((v) => v.enrolledAt && !v.revokedAt)
  if (enrolled.length === 0) return { word: "No sources enrolled", tone: "unknown" }
  const sorted = newestFirst(checks)
  const outcomes = enrolled.map((v) =>
    checkOutcome(sorted.find((check) => check.vantageId === v.id && !activeCheck(check))),
  )
  const reached = outcomes.filter((o) => o.kind === "reached").length
  const failed = outcomes.filter((o) => o.kind === "failed").length
  const measured = outcomes.filter((o) => o.kind !== "none").length
  if (measured === 0) return { word: "No measurement yet", tone: "unknown" }
  const of = `${reached} of ${enrolled.length} source${enrolled.length === 1 ? "" : "s"}`
  if (failed > 0) return { word: `Reached from ${of}`, tone: reached > 0 ? "warning" : "danger" }
  if (reached < enrolled.length) return { word: `Reached from ${of}`, tone: "warning" }
  return { word: `Reached from ${of}`, tone: "running" }
}
