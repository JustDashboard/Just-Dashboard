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
export function upstreamLabel(target: Pick<UpstreamTarget, "state" | "ms">): string {
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

/** How bad a state is, for picking the worse of two: a refusal outranks a timeout. */
export function upstreamRank(state: UpstreamState): number {
  return RANK[state]
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
