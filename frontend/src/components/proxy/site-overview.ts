import type {
  Certificate,
  Listener,
  PolicyControl,
  RequestSummary,
  SiteSpec,
  UpstreamPool,
  VHost,
} from "@/lib/types"
import { agentProduct } from "@/lib/clients"
import { imageProduct, portProduct, processProduct, unitProduct } from "@/components/product-logo"
import type { DotTone } from "@/components/status-dot"
import { FEATURE_LABEL } from "@/components/proxy/site-details"

/**
 * A site's page at a glance, as pure reads: the verdict at the end of its
 * identity line, the words its engine node carries, and what answers at the
 * far end of its route. Kept apart from the drawing so each rule is one test.
 */

/** The verdict at the identity line's end, and the requests a press narrows to. */
export type SiteVerdict = {
  tone: DotTone
  label: string
  hint?: string
  /** The status family the verdict counts, which a press narrows Requests to. */
  narrows?: "5xx"
}

/**
 * What is wrong with the site, worst first: requests it failed in the last
 * hour, then servers behind it that will not answer, then its certificate.
 * A site that failed nothing says how much it answered, and one that was
 * asked nothing says that. A disabled site has no verdict: its state says it.
 */
export function siteVerdict({
  vhost,
  summary,
  pools,
  cert,
}: {
  vhost: Pick<VHost, "enabled">
  summary?: RequestSummary
  pools: UpstreamPool[]
  cert?: Certificate
}): SiteVerdict | undefined {
  if (!vhost.enabled) return undefined
  const failed = summary?.classes["5xx"] ?? 0
  if (summary && failed > 0) {
    return {
      tone: summary.errorRate >= 0.01 ? "danger" : "warning",
      label: `${failed.toLocaleString()} failed in the last hour`,
      hint: `${percent(summary.errorRate)} of ${summary.total.toLocaleString()} answered 5xx`,
      narrows: "5xx",
    }
  }
  const down = pools.find((pool) => pool.verdict === "down")
  if (down) return { tone: "danger", label: "No server behind it answers" }
  const failing = pools.find((pool) => pool.verdict === "degraded" || pool.verdict === "on-backup")
  if (failing) {
    const bad = failing.members.filter((m) => m.state && m.state !== "up").length
    return {
      tone: "warning",
      label:
        failing.verdict === "on-backup"
          ? "Serving from its backup"
          : `${bad} of ${failing.members.length} servers failing`,
    }
  }
  if (cert?.expired) return { tone: "danger", label: "Its certificate has expired" }
  if (cert?.expiring) {
    return { tone: "warning", label: `Its certificate ends in ${cert.daysLeft} days` }
  }
  if (!summary) return undefined
  if (summary.total === 0) return { tone: "unknown", label: "No requests in the last hour" }
  return {
    tone: "running",
    label: "Every request answered",
    hint: `${summary.total.toLocaleString()} in the last hour`,
  }
}

const percent = (rate: number) => `${(rate * 100).toFixed(rate < 0.1 ? 1 : 0)}%`

/** A word the engine node carries, in the hue of what kind of thing it is. */
export type SiteFeatureWord = { label: string; hue: string }

/** Security in green, protocols in blue, what saves work in cyan, a paused site in amber. */
const KIND_HUE = {
  guard: "var(--tag-green)",
  protocol: "var(--tag-blue)",
  speed: "var(--tag-cyan)",
  paused: "var(--tag-amber)",
} as const

const FEATURE_KIND: Record<string, keyof typeof KIND_HUE> = {
  "HTTPS only": "guard",
  HSTS: "guard",
  password: "guard",
  SSO: "guard",
  "IP-restricted": "guard",
  "exploit filter": "guard",
  "rate-limited": "guard",
  "HTTP/2": "protocol",
  "HTTP/3": "protocol",
  WebSockets: "protocol",
  gzip: "speed",
  cached: "speed",
  maintenance: "paused",
}

/**
 * What the site's file switches on, in the form's words: from the form's
 * fields where the file was read back, and from what the server found in the
 * file otherwise — a Caddy route, a site written by hand.
 */
export function siteFeatures(
  vhost: Pick<VHost, "features" | "tls">,
  spec: SiteSpec | undefined,
): SiteFeatureWord[] {
  const words: string[] = []
  if (spec) {
    if (spec.tls && spec.forceHttps) words.push("HTTPS only")
    if (spec.tls && spec.hsts) words.push("HSTS")
    if (spec.http2) words.push("HTTP/2")
    if (spec.webSockets) words.push("WebSockets")
    if (spec.gzip) words.push("gzip")
    if (spec.blockExploits) words.push("exploit filter")
  }
  for (const feature of vhost.features ?? []) words.push(FEATURE_LABEL[feature])
  return [...new Set(words)].map((label) => ({
    label,
    hue: KIND_HUE[FEATURE_KIND[label] ?? "protocol"],
  }))
}

/** A control of the site's service policy in the hue its feature word has on the route. */
export function controlHue(id: PolicyControl["id"]): string {
  switch (id) {
    case "rate-limit":
    case "conn-limit":
      return KIND_HUE.guard
    case "proxy-cache":
    case "static-cache":
      return KIND_HUE.speed
    default:
      return KIND_HUE.protocol
  }
}

/**
 * A control's state before anything was measured: set, not set, or set on a
 * build that lacks what it needs — which is not in effect whatever the file
 * says.
 */
export function controlState(control: PolicyControl): { tone: DotTone; label: string } {
  if (!control.configured) return { tone: "unknown", label: "off" }
  if (control.support === "missing") return { tone: "warning", label: "no module" }
  return { tone: "running", label: "on" }
}

/** The host and port an upstream names: `http://127.0.0.1:3000`, `10.0.0.1:3000`, `app:80`. */
export function upstreamAddress(raw: string): { host: string; port?: number } | undefined {
  const match = /^(?:[a-z]+:\/\/)?(\[[^\]]+\]|[^:/]+)(?::(\d+))?/i.exec(raw.trim())
  if (!match || raw.startsWith("unix:")) return undefined
  const port = match[2] ? Number(match[2]) : raw.startsWith("https") ? 443 : undefined
  return { host: match[1].replace(/^\[|\]$/g, ""), port: port ?? 80 }
}

const LOOPBACK = new Set(["127.0.0.1", "localhost", "::1", "0.0.0.0"])

/** What answers at an upstream: the program or container holding its socket. */
export type UpstreamOwner = { product?: string; name: string; pid?: number }

/**
 * Who holds the socket an upstream names, from the host's listening ports:
 * the one the server already ties to this site, else one on this machine at
 * that port. Nothing where the address is another machine's — a socket list
 * of this host says nothing about it.
 */
export function upstreamOwner(
  raw: string,
  site: string,
  listeners: Listener[] | undefined,
): UpstreamOwner | undefined {
  const at = upstreamAddress(raw)
  if (!at || !listeners) return undefined
  // A socket on this machine answers a loopback address, or the address it
  // is bound to; 10.0.0.3:3000 is another machine's, whatever holds :3000 here.
  const tcp = listeners.filter(
    (l) =>
      l.protocol.startsWith("tcp") &&
      l.port === at.port &&
      (LOOPBACK.has(at.host) || l.address === at.host),
  )
  const listener = tcp.find((l) => l.routes?.some((route) => route.site === site)) ?? tcp[0]
  if (!listener) return undefined
  const unit = listener.activates || (listener.manager === "systemd" ? listener.managerName : "")
  const product = listener.container
    ? imageProduct(listener.container.image)
    : (processProduct(listener.displayName || listener.process || "") ??
      (unit ? unitProduct(unit) : undefined) ??
      portProduct(listener.port))
  return {
    product,
    name: listener.container?.name ?? listener.displayName ?? listener.process,
    pid: listener.container ? undefined : listener.pid || undefined,
  }
}

/**
 * Whether nothing at all listens where a loopback upstream points — the 502
 * the site is about to answer. Only claimed for this machine's own addresses,
 * and only once the socket list was read.
 */
export function upstreamUnheard(raw: string, listeners: Listener[] | undefined): boolean {
  const at = upstreamAddress(raw)
  if (!at || !listeners || !LOOPBACK.has(at.host)) return false
  return !listeners.some((l) => l.protocol.startsWith("tcp") && l.port === at.port)
}

/**
 * The browsers and bots that asked most in the hour, as the products they
 * are, each with its share of every request: Chrome 58%, Safari 30%. A family
 * no product names is left out rather than drawn as a guess.
 */
export function visitorShares(
  summary: RequestSummary | undefined,
  max = 3,
): { product: string; share: number }[] {
  if (!summary || summary.total === 0) return []
  const byProduct = new Map<string, number>()
  for (const facet of summary.agents) {
    const product = agentProduct(facet.value).product
    if (product) byProduct.set(product, (byProduct.get(product) ?? 0) + facet.count)
  }
  return [...byProduct]
    .sort((a, b) => b[1] - a[1])
    .slice(0, max)
    .map(([product, count]) => ({ product, share: count / summary.total }))
}

/** The refused probes for files that are not there — `/.env`, `/wp-login.php` — in the hour. */
export function probeCount(summary: RequestSummary | undefined): number {
  return (summary?.probes ?? []).reduce((n, facet) => n + facet.count, 0)
}

/**
 * How long a pulse takes down the route's wires: a busy site's run quicker.
 * Three seconds for a trickle, a little over one for a thousand a minute.
 */
export function beamDuration(perMinute: number | undefined): number {
  const busy = Math.log10(Math.max(0, perMinute ?? 0) + 1)
  return Math.max(1.2, Math.min(3, 3 - busy * 0.6))
}
