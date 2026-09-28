import type { DotTone } from "@/components/status-dot"
import type { UpstreamReport, UpstreamState, UpstreamTarget } from "@/lib/types"

/**
 * Reading GET /proxy/upstreams: whether what each route forwards to answers.
 *
 * Contract for the Sites and Streams pages: a site's targets are the ones
 * whose `file` is its `path`; a failed read (404 on an older backend, 503 on
 * a host without nginx) shows nothing rather than a guess.
 */

const WORD: Record<UpstreamState, string> = {
  up: "up",
  refused: "refused",
  timeout: "timeout",
  unresolvable: "unresolvable",
  missing: "no socket",
  error: "unreachable",
  dynamic: "dynamic, not checked",
}

/** "up 2 ms", "refused", "dynamic, not checked". */
export function upstreamLabel(target: UpstreamTarget): string {
  if (target.state !== "up") return WORD[target.state]
  return `up ${target.ms ?? 0} ms`
}

const TONE: Record<UpstreamState, DotTone> = {
  up: "running",
  refused: "danger",
  missing: "danger",
  timeout: "warning",
  unresolvable: "warning",
  error: "warning",
  dynamic: "unknown",
}

export function upstreamTone(state: UpstreamState): DotTone {
  return TONE[state]
}

const RANK: Record<UpstreamState, number> = {
  refused: 6,
  missing: 6,
  timeout: 5,
  unresolvable: 4,
  error: 3,
  dynamic: 1,
  up: 0,
}

/** The targets of one site file, worst first. */
export function upstreamsOf(report: UpstreamReport | undefined, path: string): UpstreamTarget[] {
  return (report?.targets ?? [])
    .filter((t) => t.file === path)
    .sort((a, b) => RANK[b.state] - RANK[a.state])
}

/** An address that does not take a connection: every request sent only there fails. */
export function isDown(target: UpstreamTarget): boolean {
  return target.state !== "up" && target.state !== "dynamic"
}

/** What a visitor gets from a route whose every upstream is in this state. */
export function visitorOutcome(target: UpstreamTarget): string {
  if (target.kind === "stream") return "visitors' connections are closed"
  switch (target.state) {
    case "timeout":
      return "visitors wait, then get 504"
    default:
      return "visitors get 502"
  }
}

/** "refuses connections", "does not answer within 1.5 s". */
export function upstreamFailure(target: UpstreamTarget): string {
  switch (target.state) {
    case "refused":
      return "refuses connections"
    case "missing":
      return "has no socket at that path"
    case "timeout":
      return "does not answer a connection within 1.5 s"
    case "unresolvable":
      return "does not resolve"
    default:
      return "cannot be reached"
  }
}
