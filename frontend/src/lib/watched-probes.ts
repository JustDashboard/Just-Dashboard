import type { DotTone } from "@/components/status-dot"

/**
 * A network probe kept on the TLS watch list's own schedule, history and
 * alerts (watched_endpoints with kind "tcp"): it connects and nothing more.
 */

export type ProbeCheck = {
  ok: boolean
  address?: string
  ms?: number
  state: "connected" | "refused" | "timeout" | "unresolvable" | "error"
  error?: string
}

export type WatchedEndpoint = {
  id: number
  domain: string
  port: number
  ip?: string
  /** "tls", a handshake and its certificate, or "tcp", a probe that only connects. */
  kind?: "tls" | "tcp"
  createdAt?: string
  checkedAt?: string
  probe?: ProbeCheck
}

export type WatchedCheck = {
  checkedAt: string
  daysLeft?: number
  fingerprint?: string
  error?: string
  /** A probe's connect time. */
  ms?: number
}

export function isProbe(row: Pick<WatchedEndpoint, "kind">): boolean {
  return row.kind === "tcp"
}

const STATE: Record<ProbeCheck["state"], { label: string; tone: DotTone }> = {
  connected: { label: "connects", tone: "running" },
  refused: { label: "refused", tone: "danger" },
  timeout: { label: "no answer", tone: "warning" },
  unresolvable: { label: "does not resolve", tone: "warning" },
  error: { label: "unreachable", tone: "warning" },
}

/** "connects in 3 ms", "refused", or "not checked yet". */
export function probeStatus(probe: ProbeCheck | undefined): { label: string; tone: DotTone } {
  if (!probe) return { label: "not checked yet", tone: "unknown" }
  const state = STATE[probe.state] ?? STATE.error
  if (probe.ok) return { label: `${state.label} in ${probe.ms ?? 0} ms`, tone: state.tone }
  return state
}

/** The connect times of a probe's recent checks that connected, oldest first. */
export function connectTimes(checks: WatchedCheck[]): number[] {
  return checks.filter((check) => !check.error && check.ms !== undefined).map((check) => check.ms!)
}
