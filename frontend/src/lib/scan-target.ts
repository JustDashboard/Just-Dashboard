/**
 * What the operator typed into the scan or watch field, as a host and a port.
 *
 * Those fields are pasted into: a URL from the address bar, host:port from a
 * mail client's settings, an IPv6 literal, a name in its own script. Sent
 * verbatim, a URL was looked up in DNS whole, a pasted https://… was watched as
 * a host called "https", and host:port became "[mail.example.com:993]:443".
 * This is the backend's ParseScanTarget (proxysvc/scantarget.go), so the page
 * can say what is wrong before anything is sent; scan-target-cases.json is the
 * table both are tested against.
 */

import type { Certificate } from "./proxy/types-certs"
import type { VHost } from "./proxy/types-sites"

export type ScanTarget = { host: string; port: number }

export type ParsedTarget =
  { target: ScanTarget; error?: undefined } | { target?: undefined; error: string }

/**
 * Schemes whose port speaks TLS from the first byte, plus http and ws: a TLS
 * report of http://example.com means its HTTPS side.
 */
const SCHEME_PORTS: Record<string, number> = {
  https: 443,
  http: 443,
  wss: 443,
  ws: 443,
  imaps: 993,
  pop3s: 995,
  smtps: 465,
  submissions: 465,
  ldaps: 636,
  ftps: 990,
  ircs: 6697,
  mqtts: 8883,
  amqps: 5671,
}

const LABEL = "[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?"
const HOST = new RegExp(`^${LABEL}(\\.${LABEL})*$`)
const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/
const EMPTY = "enter a domain, for example app.example.com"

const fail = (error: string): ParsedTarget => ({ error })

/**
 * Reads `raw` as a scan target. `port` is a separately given port, 0 when
 * there is none; a port written into `raw` wins over a scheme's, and the two
 * given ports must agree.
 */
export function parseScanTarget(raw: string, port = 0): ParsedTarget {
  let rest = raw.trim()
  if (!rest) return fail(EMPTY)
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    return fail(`port ${port} is outside 1–65535`)
  }
  let schemePort = 0
  const scheme = rest.indexOf("://")
  if (scheme >= 0) {
    const name = rest.slice(0, scheme)
    const known = SCHEME_PORTS[name.toLowerCase()]
    if (!known)
      return fail(`${name}:// does not start with TLS; use https://, imaps:// or the name alone`)
    schemePort = known
    rest = rest.slice(scheme + 3)
  }
  // A path, a query or a fragment names a page on the server, not the server.
  const page = rest.search(/[/?#]/)
  if (page >= 0) rest = rest.slice(0, page)
  // user@host lets the text before the @ read as the destination to anyone
  // skimming the field, while the scan goes somewhere else.
  if (rest.includes("@"))
    return fail("a user name is not part of the address; enter the host alone")

  const split = splitHostPort(rest)
  if ("error" in split) return fail(split.error)
  const written = split.port

  if (written && port && written !== port) {
    return fail(`the address says port ${written} and the port field says ${port}`)
  }
  const chosen = written || port || schemePort || 443

  // The complaint quotes the host as it was typed, not the whole field: a
  // pasted URL's path and query are not what is wrong with it.
  const typed = split.host
  let host = typed.toLowerCase().replace(/\.$/, "")
  if (!host) return fail(EMPTY)
  const ip = canonicalIP(host)
  if (ip) return { target: { host: ip, port: chosen } }
  if (host.startsWith("*."))
    return fail("a wildcard is not an address; scan one of the names it covers")
  if (!/^[\x00-\x7f]*$/.test(host)) {
    const ascii = toASCII(host)
    if (!ascii) return fail(`"${typed}" is not a domain name`)
    host = ascii
  }
  if (host.length > 253 || !HOST.test(host)) return fail(`"${typed}" is not a domain name`)
  return { target: { host, port: chosen } }
}

/**
 * A target given as two texts: ?domain= and ?port= in a link, or the report's
 * name and port fields. An empty port is no port; one that is given must be a
 * port, so "0" and "+993" are refused rather than read as 443 and 993. The
 * backend's ParseScanQuery.
 */
export function parseScanQuery(domain: string, port: string | null): ParsedTarget {
  if (!port) return parseScanTarget(domain, 0)
  if (!/^\d+$/.test(port)) return fail(`port "${port}" is not a number`)
  const n = Number(port)
  if (n < 1 || n > 65535) return fail(`port ${port} is outside 1–65535`)
  return parseScanTarget(domain, n)
}

/** What a scan will reach, said while it is being typed: "mail.example.com, port 993". */
export function targetHint({ host, port }: ScanTarget): string {
  return `${host}, port ${port}`
}

/**
 * The target as one string, the way it is typed and put in a link: the host
 * alone on 443, host:port otherwise, an IPv6 address bracketed when it has one.
 */
export function targetLabel({ host, port }: ScanTarget): string {
  if (port === 443) return host
  return `${host.includes(":") ? `[${host}]` : host}:${port}`
}

/** The TLS report for a target. */
export function tlsReportHref(target: ScanTarget): string {
  return `/proxy/tls?domain=${encodeURIComponent(targetLabel(target))}`
}

/** A target the report's field offers, and where it was found. */
export type ScanSuggestion = { value: string; source: string }

/** How many scanned targets the field remembers. */
export const RECENT_TARGETS = 8

/** The remembered targets with `label` first. */
export function withRecent(recent: string[], label: string): string[] {
  return [label, ...recent.filter((entry) => entry !== label)].slice(0, RECENT_TARGETS)
}

/**
 * The targets the report's field offers: what was scanned recently, then the
 * names this server's sites answer, the endpoints on the watch list and the
 * names on its certificates. Each target once, under the first place it was
 * found, and only what can be dialled — a wildcard, a regex server_name, a
 * variable or the catch-all "_" names no host.
 */
export function scanSuggestions({
  recent,
  sites = [],
  certificates = [],
  watched = [],
}: {
  /** Targets scanned before, newest first, as `targetLabel` wrote them. */
  recent: string[]
  sites?: Pick<VHost, "name" | "kind" | "enabled" | "serverNames" | "listen">[]
  certificates?: Pick<Certificate, "name" | "domains">[]
  watched?: { domain: string; port: number }[]
}): ScanSuggestion[] {
  const out: ScanSuggestion[] = []
  const seen = new Set<string>()
  const offer = (group: ScanSuggestion[], raw: string, port: number, source: string) => {
    if (raw === "_") return
    const target = parseScanTarget(raw, port).target
    if (!target) return
    const value = targetLabel(target)
    if (seen.has(value)) return
    seen.add(value)
    group.push({ value, source })
  }
  // Recent keeps its order; every other source is one alphabetical group.
  const add = (fill: (group: ScanSuggestion[]) => void) => {
    const group: ScanSuggestion[] = []
    fill(group)
    out.push(...group.sort((a, b) => a.value.localeCompare(b.value)))
  }
  for (const label of recent) offer(out, label, 0, "Scanned recently")
  add((group) => {
    for (const site of sites) {
      if (!site.enabled) continue
      // A Caddy site address carries its own scheme and port; an nginx
      // server_name never does, and its port is in the listen lines.
      const ports = site.kind === "caddy" ? [] : tlsListenPorts(site.listen)
      for (const name of site.serverNames) {
        // `.example.com` is nginx for the name and every name under it.
        const host = site.kind === "caddy" ? name : name.replace(/^\./, "")
        for (const port of ports.length ? ports : [0]) offer(group, host, port, `Site ${site.name}`)
      }
    }
  })
  add((group) => {
    for (const row of watched) offer(group, row.domain, row.port, "Watched")
  })
  add((group) => {
    for (const cert of certificates) {
      // A certificate's own name is often a hash (Caddy's are); the name it
      // covers is the part worth reading.
      for (const name of cert.domains) offer(group, name, 0, "Certificate")
    }
  })
  return out
}

/**
 * The ports an nginx site's listen lines serve TLS on: those with `ssl` or
 * `quic`, where a bare address listens on 80 as nginx reads it.
 */
export function tlsListenPorts(listen: string[]): number[] {
  const ports: number[] = []
  for (const line of listen) {
    const [address = "", ...params] = line.trim().split(/\s+/)
    if (!params.includes("ssl") && !params.includes("quic")) continue
    if (address.startsWith("unix:")) continue
    const written = /^\d+$/.test(address) ? address : /:(\d+)$/.exec(address)?.[1]
    const port = written ? Number(written) : 80
    if (!ports.includes(port)) ports.push(port)
  }
  return ports
}

function splitHostPort(s: string): { host: string; port: number } | { error: string } {
  let host: string
  let rest = ""
  const colons = s.split(":").length - 1
  if (s.startsWith("[")) {
    const end = s.indexOf("]")
    if (end < 0) return { error: "an IPv6 address opened with [ is not closed with ]" }
    host = s.slice(1, end)
    rest = s.slice(end + 1)
    if (!host.includes(":") || !canonicalIP(host)) {
      return { error: `"${host}" inside [ ] is not an IPv6 address` }
    }
    if (rest && !rest.startsWith(":")) return { error: `unexpected "${rest}" after the address` }
    rest = rest.slice(1)
  } else if (colons === 1) {
    const colon = s.indexOf(":")
    host = s.slice(0, colon)
    rest = s.slice(colon + 1)
  } else if (colons > 1) {
    if (!canonicalIP(s)) {
      return { error: `"${s}" is not an IPv6 address; write one with a port as [2001:db8::1]:443` }
    }
    return { host: s, port: 0 }
  } else {
    return { host: s, port: 0 }
  }
  if (!rest) return { host, port: 0 }
  if (!/^\d+$/.test(rest)) return { error: `port "${rest}" is not a number` }
  const port = Number(rest)
  if (port < 1 || port > 65535) return { error: `port ${port} is outside 1–65535` }
  return { host, port }
}

/** An IP address in its canonical form, or undefined for anything else. */
function canonicalIP(host: string): string | undefined {
  if (IPV4.test(host)) return host
  if (!host.includes(":")) return undefined
  try {
    const parsed = new URL(`http://[${host}]/`).hostname
    return parsed.slice(1, -1)
  } catch {
    return undefined
  }
}

/** A name in its own script, in the punycode DNS carries. */
function toASCII(host: string): string | undefined {
  try {
    return new URL(`http://${host}/`).hostname
  } catch {
    return undefined
  }
}
