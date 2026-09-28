import type { UpstreamReport, UpstreamTarget } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"
import { isDown, upstreamFailure, visitorOutcome } from "@/components/proxy/upstream-health"

export type InsightFindingInput = { upstreams?: UpstreamReport }

function basename(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
}

function where(target: UpstreamTarget): string {
  return target.location ? `${target.site} ${target.location}` : target.site
}

/**
 * Conditions read from what the proxy has been doing — its traffic, its
 * upstreams, its history — rather than from its files.
 *
 * A route is judged as nginx serves it: a pass directive whose upstream
 * block still has a server that answers loses nothing but that server, so
 * it is a warning, and only a route with nothing left to send to is the 502
 * a visitor sees. A name that no longer resolves is not a 502 yet — nginx
 * resolved it when it loaded — but the next reload fails on it.
 */
export function insightFindings({ upstreams }: InsightFindingInput): ProxyFinding[] {
  const routes = new Map<string, UpstreamTarget[]>()
  for (const target of upstreams?.targets ?? []) {
    if (target.state === "dynamic") continue
    const key = [target.file, target.line, target.upstream ?? target.address].join("\u0000")
    routes.set(key, [...(routes.get(key) ?? []), target])
  }

  const out: ProxyFinding[] = []
  for (const [key, targets] of routes) {
    const down = targets.filter(isDown)
    if (down.length === 0) continue
    const first = down[0]
    const href =
      first.kind === "stream"
        ? "/proxy/streams"
        : `/proxy/sites?site=${encodeURIComponent(basename(first.file))}`
    const detail = down.map((t) => `${t.address}: ${t.detail ?? upstreamFailure(t)}`).join("\n")
    const meta = first.kind === "stream" ? "stream" : "upstream"
    // What failed, not how long the check waited or which errno it quoted.
    const fingerprint = down
      .map((t) => `${t.address} ${t.state}`)
      .sort()
      .join("\n")

    if (down.length < targets.length) {
      out.push({
        id: `upstream.partial.${key}`,
        level: "warning",
        title: `${where(first)} → upstream ${first.upstream}: ${down.length} of ${targets.length} servers down`,
        detail,
        fingerprint,
        advice:
          "nginx sends this route's requests to the servers that answer, so visitors are served while the rest carry the load. Start the stopped service or remove the server from the upstream.",
        meta,
        href,
      })
      continue
    }
    const target = targets.length === 1 ? first.address : `upstream ${first.upstream}`
    if (down.every((t) => t.state === "unresolvable")) {
      out.push({
        id: `upstream.unresolvable.${key}`,
        level: "warning",
        title: `${where(first)} → ${target} does not resolve`,
        detail,
        fingerprint,
        advice:
          "nginx resolved this name when it last loaded, so requests still go to that address, but the next reload will fail on it. Correct the name or the DNS record before reloading.",
        meta,
        href,
      })
      continue
    }
    const failure = targets.length === 1 ? upstreamFailure(first) : "has no server that answers"
    out.push({
      id: `upstream.down.${key}`,
      level: "critical",
      title: `${where(first)} → ${target} ${failure}; ${visitorOutcome(first)}`,
      detail,
      fingerprint,
      advice:
        "Start the service this route forwards to, or point the route at where it now listens.",
      meta,
      href,
    })
  }
  return out
}
