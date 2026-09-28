import type {
  Certificate,
  SiteUpstreamHealth,
  SiteUpstreams,
  SitesTraffic,
  VHost,
} from "@/lib/types"
import { byUrgency, isBroken, isDisabled, isPlain } from "./site-order"
import { isDown, upstreamsOf } from "./upstream-health"

/**
 * The Sites list's chips, sorts and search, as pure functions of a site and
 * the readings other endpoints give about it: certificates, upstream health
 * and traffic. Each of those may be missing — the endpoint belongs to another
 * part of the proxy and may not answer on this host — and a missing reading
 * matches no chip and sorts nothing, rather than reading as "fine".
 */

export type SiteChip =
  | "all"
  | "broken"
  | "notlive"
  | "down"
  | "expiring"
  | "maintenance"
  | "restricted"
  | "password"
  | "static"
  | "redirect"
  | "tls"
  | "plain"
  | "disabled"

export const CHIP_LABEL: Record<SiteChip, string> = {
  all: "All",
  broken: "Broken",
  notlive: "Not live",
  down: "Upstream down",
  expiring: "Cert expiring",
  maintenance: "Maintenance",
  restricted: "IP-restricted",
  password: "Password",
  static: "Static",
  redirect: "Redirect",
  tls: "TLS",
  plain: "Plain HTTP",
  disabled: "Disabled",
}

export type SiteSort = "urgency" | "name" | "modified" | "traffic"

export const SORT_LABEL: Record<SiteSort, string> = {
  urgency: "Urgency",
  name: "Name",
  modified: "Last edited",
  traffic: "Traffic",
}

/** What the page knows about a site beyond its own listing entry. */
export type SiteReadings = {
  /** Its file changed since nginx loaded it; the page works this out from /proxy/pending. */
  notLive: (v: VHost) => boolean
  certs?: Certificate[]
  upstreams?: SiteUpstreams
  traffic?: SitesTraffic
}

/** The redirect every HTTPS site makes on port 80, which does not make it a redirect site. */
function isUpgrade(target: string): boolean {
  return /^https:\/\/\$(host|http_host|server_name)\b/.test(target)
}

/** Sends visitors somewhere else and proxies nothing. */
export function isRedirect(v: VHost): boolean {
  return v.upstreams.length === 0 && (v.redirects ?? []).some((t) => !isUpgrade(t))
}

/** Serves files from a directory and proxies nothing. */
export function isStatic(v: VHost): boolean {
  return v.upstreams.length === 0 && Boolean(v.roots?.length) && !isRedirect(v)
}

/**
 * The certificate the site serves that ends soonest — the one to act on —
 * matched by the file the site names or by the site the inventory says uses
 * it. Undefined when the site names none the inventory read.
 */
export function siteCert(v: VHost, certs: Certificate[] | undefined): Certificate | undefined {
  if (!certs || !v.tls) return undefined
  const paths = new Set(v.certPaths ?? (v.certPath ? [v.certPath] : []))
  const rank = (c: Certificate) => (c.error ? -2 : c.expired ? -1 : c.daysLeft)
  return certs
    .filter((c) => paths.has(c.path) || c.usedBy.includes(v.name))
    .sort((a, b) => rank(a) - rank(b))[0]
}

/** The site's upstreams as the health check last found them; empty where it has not checked. */
export function siteUpstreams(
  v: VHost,
  upstreams: SiteUpstreams | undefined,
): SiteUpstreamHealth[] {
  if (!Array.isArray(upstreams?.targets) || !v.path) return []
  return upstreamsOf(upstreams, v.path)
}

export { isDown }

/** Requests in the last hour, when the traffic summary has the site. */
export function siteRequests(v: VHost, traffic: SitesTraffic | undefined): number | undefined {
  if (!Array.isArray(traffic?.sites)) return undefined
  return traffic.sites.find((s) => s.site === v.name)?.requests
}

export function matchesChip(v: VHost, chip: SiteChip, r: SiteReadings): boolean {
  switch (chip) {
    case "all":
      return true
    case "broken":
      return isBroken(v)
    case "notlive":
      return r.notLive(v)
    case "down":
      return siteUpstreams(v, r.upstreams).some(isDown)
    case "expiring": {
      const cert = siteCert(v, r.certs)
      return Boolean(cert && (cert.expiring || cert.expired))
    }
    case "maintenance":
      return Boolean(v.features?.includes("maintenance"))
    case "restricted":
      return Boolean(v.features?.includes("allow"))
    case "password":
      return Boolean(v.features?.includes("auth"))
    case "static":
      return isStatic(v)
    case "redirect":
      return isRedirect(v)
    case "tls":
      return v.tls
    case "plain":
      return isPlain(v)
    case "disabled":
      return isDisabled(v)
  }
}

/**
 * The sites in the chosen order. Every order falls back to urgency, so two
 * sites equal on the chosen key keep the page's usual worst-first answer.
 */
export function sortSites(sites: VHost[], sort: SiteSort, traffic?: SitesTraffic): VHost[] {
  const out = [...sites]
  switch (sort) {
    case "urgency":
      return out.sort(byUrgency)
    case "name":
      return out.sort((a, b) => a.name.localeCompare(b.name) || byUrgency(a, b))
    case "modified":
      return out.sort((a, b) => b.modified.localeCompare(a.modified) || byUrgency(a, b))
    case "traffic":
      return out.sort(
        (a, b) =>
          (siteRequests(b, traffic) ?? -1) - (siteRequests(a, traffic) ?? -1) || byUrgency(a, b),
      )
  }
}

/** The key of the site j or k moves to, from the one focused now; the first when none is. */
export function stepSelection(
  keys: string[],
  current: string | null,
  delta: 1 | -1,
): string | null {
  if (keys.length === 0) return null
  const at = current === null ? -1 : keys.indexOf(current)
  if (at === -1) return delta === 1 ? keys[0] : keys[keys.length - 1]
  return keys[Math.min(keys.length - 1, Math.max(0, at + delta))]
}

/**
 * Several sites' files as one text file: each under a comment naming where
 * it came from, so the bundle reads back as the files it was made from.
 */
export function exportBundle(files: { path: string; content: string }[], at: Date): string {
  const head = `# ${files.length} nginx site file${files.length === 1 ? "" : "s"}, exported ${at.toISOString()}\n`
  return (
    head +
    files
      .map(
        ({ path, content }) =>
          `\n# ==== ${path} ====\n${content.endsWith("\n") ? content : `${content}\n`}`,
      )
      .join("")
  )
}
