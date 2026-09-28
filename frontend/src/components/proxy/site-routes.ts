import type { LocationMatch, SiteLocation, SitePool, SiteSpec } from "@/lib/types"

/**
 * What each path of a proxy site does, in the order nginx decides it.
 *
 * The same arithmetic the renderer does (sites.go renderedPath and
 * renderedUpstream, sites_render.go proxyPassTarget), repeated here only to
 * draw an example: the file the preview shows is still the server's. nginx
 * does not read locations top to bottom — an exact match wins outright, then
 * the longest prefix, which a regex overrides unless the prefix is marked ^~ —
 * so a list in the order they were typed says little about which one answers.
 */
export type SiteRoute = {
  /** The location as nginx reads it, modifier included. */
  location: string
  /** How this one competes with the others. */
  rule: string
  /** Where a request ends up: an upstream or a folder. */
  target: string
  /** An example request and what the application or the disk sees of it. */
  example?: { request: string; reaches: string }
  /** Short facts: stripped, SPA, password, own address list, WebSockets. */
  notes: string[]
}

export const MATCH_LABEL: Record<LocationMatch, string> = {
  "": "Starts with",
  "^~": "Starts with, before regexes",
  "=": "Exactly",
  "~": "Regex",
  "~*": "Regex, any case",
}

const RULE: Record<LocationMatch, string> = {
  "=": "Exact match, checked first",
  "^~": "Longest prefix; regexes are skipped",
  "~": "Regex, tried in the order listed",
  "~*": "Regex, tried in the order listed",
  "": "Longest prefix, unless a regex matches",
}

const RANK: Record<LocationMatch, number> = { "=": 0, "^~": 1, "~": 2, "~*": 2, "": 3 }

const isPrefix = (loc: SiteLocation) => !loc.match || loc.match === "^~"
const servesFolder = (loc: SiteLocation) => !loc.upstream && Boolean(loc.root)

/** The path the block is written for: a folder or a stripped prefix ends in a slash. */
export function renderedPath(loc: SiteLocation): string {
  if (loc.path.endsWith("/") || !isPrefix(loc)) return loc.path
  const folder = servesFolder(loc)
  if ((folder && !loc.rootMode) || (!folder && loc.stripPrefix)) return `${loc.path}/`
  return loc.path
}

/** The upstream's address and the path after it, where nginx reads them apart. */
export function splitUpstream(upstream: string): [string, string] {
  if (upstream.startsWith("unix:")) {
    const socket = upstream.slice("unix:".length)
    const i = socket.indexOf(":")
    return i >= 0 ? [`unix:${socket.slice(0, i)}`, socket.slice(i + 1)] : [upstream, ""]
  }
  const scheme = upstream.indexOf("://")
  if (scheme < 0) return [upstream, ""]
  const host = scheme + 3
  const i = upstream.indexOf("/", host)
  return i >= 0 ? [upstream.slice(0, i), upstream.slice(i)] : [upstream, ""]
}

/** The upstream as the location forwards to it; stripping ends it in a slash. */
export function renderedUpstream(loc: SiteLocation): string {
  const upstream = loc.upstream ?? ""
  if (!loc.stripPrefix || servesFolder(loc)) return upstream
  const [address, uri] = splitUpstream(upstream)
  if (uri.endsWith("/")) return upstream
  if (!uri && address.startsWith("unix:")) return `${address}:/`
  return `${address}${uri}/`
}

function example(loc: SiteLocation): SiteRoute["example"] {
  const path = renderedPath(loc)
  if (!isPrefix(loc)) {
    if (loc.match !== "=") return undefined
    if (servesFolder(loc)) {
      return { request: path, reaches: `${loc.root?.replace(/\/$/, "")}${path}` }
    }
    const [, uri] = splitUpstream(loc.upstream ?? "")
    return { request: path, reaches: uri || path }
  }
  const request = `${path.replace(/\/$/, "")}/page`
  const rest = request.slice(path.length)
  if (servesFolder(loc)) {
    const root = loc.root?.replace(/\/$/, "") ?? ""
    return { request, reaches: loc.rootMode === "root" ? `${root}${request}` : `${root}/${rest}` }
  }
  const [, uri] = splitUpstream(renderedUpstream(loc))
  // A path that is the location's own is left off the proxy_pass, so the
  // request goes through as sent.
  return { request, reaches: uri && uri !== path ? `${uri}${rest}` : request }
}

function notes(loc: SiteLocation, spec: SiteSpec): string[] {
  const out: string[] = []
  if (loc.stripPrefix && !servesFolder(loc)) out.push("prefix stripped")
  if (servesFolder(loc) && loc.spa) out.push("falls back to index.html")
  if (!servesFolder(loc) && loc.webSockets) out.push("WebSockets")
  if (loc.basicAuthFile) out.push("own password")
  else if (spec.basicAuthFile) out.push("site password")
  if (loc.allowFrom?.length || loc.denyFrom?.length) out.push("own address list")
  if (loc.rateLimit) out.push("own rate")
  return out
}

/** The site's paths as rows, most decisive first, the catch-all last. */
export function siteRoutes(spec: SiteSpec): SiteRoute[] {
  const rows = spec.locations
    .map((loc, order) => ({ loc, order }))
    .filter(({ loc }) => loc.path && (loc.upstream || loc.root))
    .sort((a, b) => {
      const rank = RANK[a.loc.match ?? ""] - RANK[b.loc.match ?? ""]
      if (rank !== 0) return rank
      // Regexes go in the order written; prefixes longest first.
      if (a.loc.match === "~" || a.loc.match === "~*") return a.order - b.order
      return renderedPath(b.loc).length - renderedPath(a.loc).length
    })
    .map(({ loc }): SiteRoute => {
      const match = loc.match ?? ""
      return {
        location: match ? `${match} ${renderedPath(loc)}` : renderedPath(loc),
        rule: RULE[match],
        target: servesFolder(loc) ? (loc.root ?? "") : renderedUpstream(loc),
        example: example(loc),
        notes: notes(loc, spec),
      }
    })
  const upstream = spec.pool ? poolTarget(spec.pool) : spec.upstream
  const catchAll: SiteLocation = { path: "/", upstream, webSockets: spec.webSockets }
  rows.push({
    location: "/",
    rule: "Everything else",
    target: upstream ?? "",
    example: example(catchAll),
    notes: notes(catchAll, spec),
  })
  return rows
}

/** A pool as the routes table names it: how many servers share it, and over what. */
function poolTarget(pool: SitePool): string {
  const n = pool.servers.length
  return `pool of ${n} server${n === 1 ? "" : "s"} over ${pool.scheme || "http"}`
}
