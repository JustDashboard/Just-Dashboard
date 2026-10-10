import type { BoundaryImpact, BoundaryProposal, BoundaryState } from "@/lib/types"
import type { Verdict } from "@/components/status-dot"

/**
 * A proposal as the query `/security/boundary/check` reads: one parameter per
 * field, and `setting=key=value` for each SSH directive. Asked with a GET, so
 * judging a change is never itself recorded as one.
 */
export function boundaryQuery(proposal: BoundaryProposal): Record<string, string | string[]> {
  const query: Record<string, string | string[]> = { kind: proposal.kind }
  for (const key of ["target", "action", "port", "protocol", "policy"] as const) {
    const value = proposal[key]
    if (value) query[key] = value
  }
  const settings = Object.entries(proposal.settings ?? {})
  if (settings.length > 0) query.setting = settings.map(([key, value]) => `${key}=${value}`).sort()
  return query
}

/** Whether a proposal is complete enough to judge: a ban needs an address. */
export function judgeable(proposal: BoundaryProposal | undefined): proposal is BoundaryProposal {
  if (!proposal) return false
  if (proposal.kind === "ban") return /^[0-9a-fA-F:.]+(\/\d{1,3})?$/.test(proposal.target ?? "")
  if (proposal.kind === "ssh") return Object.keys(proposal.settings ?? {}).length > 0
  return true
}

/** A cut is refused by the server whatever is acknowledged; an effect is acknowledged. */
export function boundaryVerdict(impacts: BoundaryImpact[]): "clear" | "acknowledge" | "refused" {
  if (impacts.some((impact) => impact.level === "cuts")) return "refused"
  return impacts.length > 0 ? "acknowledge" : "clear"
}

export const BOUNDARY_STATE: Record<BoundaryState, { verdict: Verdict; label: string }> = {
  held: { verdict: "ok", label: "held" },
  broken: { verdict: "critical", label: "broken" },
  unknown: { verdict: "notice", label: "unknown" },
}
