import { hueFor, LANES } from "@/lib/hue"
import { containerProduct, processProduct, unitProduct } from "@/components/product-logo"
import type {
  Certificate,
  Listener,
  SiteTrafficReading,
  SiteUpstreamHealth,
  SitesTraffic,
  VHost,
} from "@/lib/types"
import { siteCert, type SiteChip } from "./site-filters"
import { isPlain } from "./site-order"

/**
 * The Sites page's readings of the whole host, as pure functions of the
 * listing and the reads other parts of the proxy own: what each site hands
 * its requests to, drawn as the product it is; who carries the last hour's
 * traffic; how long each certificate has left; and the one verdict the
 * identity line ends on. Each read may be missing — its endpoint belongs to
 * another part of the proxy — and a missing read draws nothing rather than
 * a reading of zero.
 */

/**
 * The lanes a site can take: no red or amber, which are states, and no
 * slate, which is the muted "everything else" span on the traffic bar.
 */
const SITE_HUES = LANES.filter((hue) => hue !== "var(--tag-slate)")

/** The colour a site has everywhere on the page: its hour bar, its span, its dot. */
export function siteHue(name: string): string {
  return hueFor(name.toLowerCase(), SITE_HUES)
}

/** Where nginx connects for an upstream URL, the port filled in from its scheme. */
export function upstreamEndpoint(url: string): { host: string; port: number } | undefined {
  const match = /^(?:([a-z][a-z0-9+.-]*):\/\/)?(\[[^\]]+\]|[^/:?#\s]+)(?::(\d+))?/i.exec(url.trim())
  if (!match || match[1]?.toLowerCase() === "unix" || match[2].toLowerCase() === "unix") {
    return undefined
  }
  const port = match[3] ? Number(match[3]) : match[1]?.toLowerCase() === "https" ? 443 : 80
  return { host: match[2].toLowerCase(), port }
}

/** A host nginx dials on this machine: loopback, or every interface. */
function isLocal(host: string) {
  return (
    host === "localhost" ||
    host === "[::1]" ||
    host === "0.0.0.0" ||
    host === "[::]" ||
    /^127\.\d+\.\d+\.\d+$/.test(host)
  )
}

/** A socket that answers a connection to loopback on its port. */
function answersLoopback(address: string) {
  return (
    address === "" ||
    address === "*" ||
    address === "0.0.0.0" ||
    address === "::" ||
    address === "::1" ||
    /^127\.\d+\.\d+\.\d+$/.test(address)
  )
}

/**
 * The socket a local upstream reaches, from the Ports page's list: the same
 * port, bound where loopback gets to it. A container's published port is
 * preferred over the bare docker-proxy, which says less about what answers.
 */
export function listenerAt(url: string, listeners: Listener[] | undefined): Listener | undefined {
  const endpoint = upstreamEndpoint(url)
  if (!endpoint || !listeners || !isLocal(endpoint.host)) return undefined
  const found = listeners.filter(
    (l) => l.protocol === "tcp" && l.port === endpoint.port && answersLoopback(l.address),
  )
  return found.find((l) => l.container) ?? found.find((l) => l.pid > 0) ?? found[0]
}

/**
 * The owner of a socket as the product it is: the container's image first,
 * then the program, then the unit that runs it. Never the port alone — a
 * site's upstream on 8080 is whatever listens there, and a guessed logo
 * would be the card lying about it.
 */
export function listenerProduct(listener: Listener): string | undefined {
  if (listener.container) {
    const product = containerProduct({ image: listener.container.image })
    return product === "docker" ? undefined : product
  }
  const unit = listener.activates || (listener.manager === "systemd" ? listener.managerName : "")
  const product =
    processProduct(listener.displayName || listener.process || "") ??
    (unit ? unitProduct(unit) : undefined)
  // docker-proxy with no container on the listing names Docker, not the app.
  return product === "docker" ? undefined : product
}

/** What a site hands its requests to, as far as this host can say. */
export type SiteApp = {
  /** host:port, as nginx dials it. */
  address: string
  /** A product-logo id, only where the socket's owner names one. */
  product?: string
  /** The container or program that answers there. */
  name?: string
}

/**
 * The application behind a site: its first upstream, resolved through the
 * upstream block it names, then to the socket that answers there. A site
 * that proxies nothing has none; an upstream on another machine is its
 * address alone.
 */
export function siteApp(
  v: VHost,
  listeners: Listener[] | undefined,
  health: SiteUpstreamHealth[],
): SiteApp | undefined {
  const first = v.upstreams[0]
  if (!first) return undefined
  const endpoint = upstreamEndpoint(first)
  const pool = endpoint && v.pools?.find((p) => p.name.toLowerCase() === endpoint.host)
  const url = pool?.servers[0] ?? first
  const at = upstreamEndpoint(url)
  if (!at) return { address: url }
  const address = `${at.host}:${at.port}`
  const listener = listenerAt(url, listeners)
  if (listener) {
    return {
      address,
      product: listenerProduct(listener),
      name: listener.container?.name ?? (listener.displayName || listener.process || undefined),
    }
  }
  // The health check names the process on a local port where /ports did not.
  const owner = health.find((t) => t.address === address)?.owner
  const product = owner ? processProduct(owner) : undefined
  return { address, product: product === "docker" ? undefined : product, name: owner }
}

/** A site's last hour, when the traffic summary could read its log. */
export function siteTraffic(
  v: VHost,
  traffic: SitesTraffic | undefined,
): SiteTrafficReading | undefined {
  if (!Array.isArray(traffic?.sites)) return undefined
  const reading = traffic.sites.find((s) => s.site === v.name)
  return reading?.status === "available" ? reading : undefined
}

export type TrafficShare = { vhost: VHost; reading: SiteTrafficReading }

/**
 * Who carried the last hour, busiest first, and what is left over: the sites
 * that served nothing, and the enabled ones whose log could not be read —
 * which are neither quiet nor busy, and are counted apart.
 */
export function trafficShares(
  hosts: VHost[],
  traffic: SitesTraffic | undefined,
): { busy: TrafficShare[]; total: number; quiet: number; unread: number } | undefined {
  if (!Array.isArray(traffic?.sites)) return undefined
  const busy: TrafficShare[] = []
  let quiet = 0
  let unread = 0
  for (const vhost of hosts) {
    const reading = siteTraffic(vhost, traffic)
    if (!reading) {
      if (vhost.enabled && vhost.kind === "nginx") unread++
    } else if (reading.requests > 0) {
      busy.push({ vhost, reading })
    } else {
      quiet++
    }
  }
  busy.sort(
    (a, b) => b.reading.requests - a.reading.requests || a.vhost.name.localeCompare(b.vhost.name),
  )
  return { busy, total: busy.reduce((sum, s) => sum + s.reading.requests, 0), quiet, unread }
}

/** How loud a site's share of failed requests is: 5% of them is an outage for someone. */
export function errorTone(rate: number): "danger" | "warning" | undefined {
  if (rate >= 0.05) return "danger"
  if (rate >= 0.01) return "warning"
  return undefined
}

export type CertRow = { vhost: VHost; cert: Certificate }

/**
 * Every enabled site on TLS whose certificate the inventory read, soonest to
 * end first; the sites proxying an application in plain text; and those on
 * TLS whose certificate the inventory does not have — Caddy's own, or one
 * outside the directories it reads — which have no expiry to show.
 */
export function certRows(
  hosts: VHost[],
  certs: Certificate[] | undefined,
): { rows: CertRow[]; plain: VHost[]; unknown: number; tls: number } {
  const rows: CertRow[] = []
  let unknown = 0
  const tls = hosts.filter((v) => v.tls && v.enabled)
  for (const vhost of tls) {
    const cert = siteCert(vhost, certs)
    if (cert && !cert.error) rows.push({ vhost, cert })
    else unknown++
  }
  rows.sort((a, b) => a.cert.daysLeft - b.cert.daysLeft || a.vhost.name.localeCompare(b.vhost.name))
  return { rows, plain: hosts.filter(isPlain), unknown, tls: tls.length }
}

/** How a certificate's days read: red once it has ended, amber in its last fortnight. */
export function certTone(cert: Certificate): "danger" | "warning" | undefined {
  if (cert.expired || cert.daysLeft < 0) return "danger"
  if (cert.expiring || cert.daysLeft < 14) return "warning"
  return undefined
}

/** How many certificates are valid, due for renewal or expired: the spans of the block's bar. */
export function certCounts(rows: readonly CertRow[]): {
  valid: number
  due: number
  expired: number
} {
  const counts = { valid: 0, due: 0, expired: 0 }
  for (const { cert } of rows) {
    const tone = certTone(cert)
    counts[tone === "danger" ? "expired" : tone === "warning" ? "due" : "valid"] += 1
  }
  return counts
}

export type SitesVerdict = {
  tone: "danger" | "warning" | "running" | "stopped"
  label: string
  /** What the count is made of, one phrase per cause. */
  detail?: string
  /** The chip a press narrows the list to, which holds exactly what is counted. */
  chip?: SiteChip
}

/**
 * The identity line's verdict: the sites in the attention group, said by what
 * is wrong with them — a link to nothing and an application that refuses
 * connections, whose visitors get a 502, are red — then an engine that does
 * not run, and only then that nothing needs anyone.
 */
export function sitesVerdict({
  stopped,
  engine,
  total,
}: {
  stopped: boolean
  engine: string
  total: number
}): SitesVerdict {
  if (stopped) return { tone: "stopped", label: `No ${engine} site is served` }
  if (total === 0) return { tone: "stopped", label: "No sites yet" }
  return { tone: "running", label: `${total} sites` }
}
