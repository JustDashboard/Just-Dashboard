import type { DriftReport, UpstreamReport, UpstreamTarget } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"
import { isDown, upstreamFailure, visitorOutcome } from "@/components/proxy/upstream-health"

function basename(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
}

function where(target: UpstreamTarget): string {
  return target.location ? `${target.site} ${target.location}` : target.site
}

export type InsightFindingInput = {
  /** What each TLS site hands out against the file it names. */
  drift?: DriftReport
  upstreams?: UpstreamReport
}

/** The id prefix of a served certificate that is not the file's, which a page answers with a reload. */
export const SERVED_STALE = "insight.served-stale."

function days(n: number): string {
  if (n < 0) return "has expired"
  return n === 1 ? "expires in 1 day" : `expires in ${n} days`
}

/**
 * Conditions read from what the proxy has been doing — its traffic, its
 * upstreams, its history — rather than from its files: among them a
 * certificate renewed on disk and never reloaded, and a name that reaches
 * another server block's certificate. A block that did not answer or could
 * not be asked is not one: the engine's own findings say when nginx is down.
 *
 * A route is judged as nginx serves it: a pass directive whose upstream
 * block still has a server that answers loses nothing but that server, so
 * it is a warning, and only a route with nothing left to send to is the 502
 * a visitor sees. A name that no longer resolves is not a 502 yet — nginx
 * resolved it when it loaded — but the next reload fails on it.
 */
export function insightFindings({ drift, upstreams }: InsightFindingInput): ProxyFinding[] {
  const routes = new Map<string, UpstreamTarget[]>()
  for (const target of upstreams?.targets ?? []) {
    if (target.state === "dynamic") continue
    const key = [target.file, target.line, target.upstream ?? target.address].join("\u0000")
    routes.set(key, [...(routes.get(key) ?? []), target])
  }

  const out: ProxyFinding[] = []
  for (const site of drift?.sites ?? []) {
    const href = `/proxy/sites?site=${encodeURIComponent(site.site)}`
    const name = site.serverName ?? site.site
    if (site.state === "stale" && site.served && site.disk) {
      out.push({
        id: `${SERVED_STALE}${site.path}:${site.line}`,
        level: site.served.expired || site.served.daysLeft <= 7 ? "critical" : "warning",
        title: `${name} still serves the certificate it had before the file changed`,
        detail: `Stale: nginx still serves the certificate that ${days(site.served.daysLeft)}; the file on disk, ${site.certPath}, is valid for ${Math.max(site.disk.daysLeft, 0)} days.`,
        advice:
          "nginx reads certificate files only when it starts or reloads. A reload serves the file on disk without dropping a connection.",
        meta: site.site,
        href,
      })
    } else if (site.state === "mismatch" && site.served) {
      out.push({
        id: `insight.served-mismatch.${site.path}:${site.line}`,
        level: "warning",
        title: `${name} gets another server block's certificate`,
        detail: site.servedBy
          ? `Mismatch: a request for ${name} was answered with the certificate ${site.servedBy} names, not ${site.certPath}. SNI fell through to another server block.`
          : `Mismatch: a request for ${name} was answered with a certificate for ${site.served.domains.join(", ") || "another name"}, not ${site.certPath}. SNI fell through to another server block.`,
        advice:
          "nginx picks the block by server_name. Check the spelling in this site's server_name, or whether another block claims the same name on this port.",
        meta: site.site,
        href,
      })
    }
  }
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

    if (down.length < targets.length) {
      out.push({
        id: `upstream.partial.${key}`,
        level: "warning",
        title: `${where(first)} → upstream ${first.upstream}: ${down.length} of ${targets.length} servers down`,
        detail,
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
      advice:
        "Start the service this route forwards to, or point the route at where it now listens.",
      meta,
      href,
    })
  }
  return out
}
