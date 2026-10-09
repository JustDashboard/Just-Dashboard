import type { DotTone } from "@/components/status-dot"
import type { AccessExplanation, AccessLayer } from "@/lib/proxy/types-route"

/**
 * Reading GET /proxy/resolve/access: the answer for one address, and each
 * layer's part in it. A layer that refuses is drawn as the refusal; one that
 * could not be judged is unknown, never a pass.
 */

const VERDICT: Record<AccessExplanation["verdict"], { label: string; tone: DotTone }> = {
  admitted: { label: "Let through", tone: "running" },
  credentials: { label: "Asked to sign in", tone: "warning" },
  refused: { label: "Refused", tone: "danger" },
  unknown: { label: "Not decided here", tone: "unknown" },
  "no-route": { label: "Nothing answers", tone: "danger" },
}

export function accessVerdict(explanation: AccessExplanation): { label: string; tone: DotTone } {
  return VERDICT[explanation.verdict]
}

const LAYER: Record<AccessLayer["verdict"], { label: string; tone: DotTone }> = {
  admits: { label: "lets through", tone: "running" },
  refuses: { label: "refuses", tone: "danger" },
  requires: { label: "asks for credentials", tone: "warning" },
  unknown: { label: "not judged", tone: "unknown" },
  skipped: { label: "not applied", tone: "unknown" },
}

export function layerVerdict(layer: AccessLayer): { label: string; tone: DotTone } {
  return LAYER[layer.verdict]
}

/** An address the server takes: one IPv4 or IPv6 address, no range, no zone. */
export function validSource(raw: string): boolean {
  const value = raw.trim()
  if (/^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/.test(value)) return true
  return /^[0-9a-fA-F:.]+$/.test(value) && value.includes(":") && !value.includes(":::")
}
