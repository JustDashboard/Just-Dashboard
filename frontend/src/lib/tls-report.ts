/**
 * The TLS report's readings, worked out apart from the page so each can be
 * tested: where a plain-HTTP visitor ends up, how far a failed scan got and
 * where to look next, and a certificate's term in the unit that reads true.
 */

import type { Verdict } from "@/components/status-dot"
import type { Tone } from "@/components/tone"
import type { HTTPScan, RedirectHop, TLSScan } from "./proxy/types-tls"
import { tlsReportHref } from "./scan-target"

const PLAIN_ERRORS: Record<NonNullable<HTTPScan["plainErrorKind"]>, string> = {
  refused: "port 80 refused the connection",
  timeout: "port 80 did not answer in time",
  dns: "the name did not resolve",
  other: "port 80 did not answer",
}

/** Where a hop's redirect points, resolved against the hop's own address. */
function destination(hop: RedirectHop): URL | undefined {
  if (!hop.location) return undefined
  try {
    return new URL(hop.location, hop.url)
  } catch {
    return undefined
  }
}

/**
 * Where a plain-HTTP visitor ends up, as a verdict and a label. Only a
 * refused connection is called refused, and HTTPS reached through another
 * host names the host: the header for the name asked about is never set on
 * that way in.
 */
export function plainVerdict(http: HTTPScan): { verdict: Verdict; label: string } {
  const hops = http.redirectChain
  const last = hops[hops.length - 1]
  if (http.plainError)
    return { verdict: "notice", label: PLAIN_ERRORS[http.plainErrorKind ?? "other"] }
  const count = hops.length > 1 ? ` in ${hops.length} hops` : ""
  switch (http.redirectVerdict) {
    case "same-host":
      return { verdict: "ok", label: `redirects to HTTPS${count}` }
    case "other-host":
      return {
        verdict: "ok",
        label: `reaches HTTPS on ${last && destination(last)?.host}${count}`,
      }
    case "internal":
      return { verdict: "notice", label: "not followed to an internal address" }
    case "loop":
      return { verdict: "critical", label: "redirects in a loop" }
    case "too-many":
      return { verdict: "critical", label: `${hops.length} redirects, none to HTTPS` }
    case "dead-end":
      return {
        verdict: "critical",
        label: last?.error ? "a redirect leads nowhere" : "redirects, never to HTTPS",
      }
  }
  if (hops.length > 1)
    return { verdict: "critical", label: `stays on HTTP, answering ${last?.status}` }
  return { verdict: "critical", label: `answers ${last?.status} without redirecting` }
}

/** One step of a failed scan's way in, as a reading. */
export type FailureStep = { label: string; value: string; hint: string; tone: Tone }

const CONNECT_REASONS: Record<string, string> = {
  refused: "refused",
  timeout: "timed out",
  unreachable: "no route",
}

const HANDSHAKE_REASONS: Record<string, string> = {
  alert: "refused",
  "plain-http": "plain HTTP",
  "not-tls": "not TLS",
  closed: "closed",
  timeout: "no answer",
}

const WHERE: Record<string, string> = {
  here: "this server",
  cloudflare: "Cloudflare",
  elsewhere: "not this server",
}

/** Whether a scanned host is an address rather than a name to resolve. */
function isAddress(host: string) {
  return host.includes(":") || /^\d{1,3}(\.\d{1,3}){3}$/.test(host)
}

/**
 * A failed scan as the three steps a connection takes — the name, the
 * connection, the handshake — each passed, failed or never reached, so the
 * step that failed is the one in red and the ones after it say they were not
 * reached rather than failing too.
 */
export function failureSteps(scan: TLSScan): FailureStep[] {
  const failure = scan.failure
  if (!failure) return []
  const at = [failure.address, failure.where && WHERE[failure.where]].filter(Boolean).join(" · ")
  const notReached = { value: "—", hint: "not reached", tone: "default" as Tone }

  const name: FailureStep =
    failure.stage === "dns"
      ? {
          label: "Name",
          value: failure.reason === "no-such-host" ? "no record" : "lookup failed",
          hint: scan.domain,
          tone: "danger",
        }
      : isAddress(scan.domain)
        ? { label: "Name", value: "address", hint: "nothing to resolve", tone: "default" }
        : {
            label: "Name",
            value: "resolves",
            hint: failure.dns?.addresses.join(", ") || scan.domain,
            tone: "default",
          }
  const connection: FailureStep =
    failure.stage === "dns"
      ? { label: `Port ${scan.port}`, ...notReached }
      : failure.stage === "connect"
        ? {
            label: `Port ${scan.port}`,
            value: CONNECT_REASONS[failure.reason] ?? "failed",
            hint: at,
            tone: "danger",
          }
        : { label: `Port ${scan.port}`, value: "open", hint: at, tone: "default" }
  const handshake: FailureStep =
    failure.stage !== "handshake"
      ? { label: "Handshake", ...notReached }
      : {
          label: "Handshake",
          value: HANDSHAKE_REASONS[failure.reason] ?? "failed",
          hint: failure.alert ?? (failure.answer ? `answered “${failure.answer}”` : ""),
          tone: "danger",
        }
  return [name, connection, handshake]
}

/**
 * Where to look next for a failed scan, only where the fault can be here: a
 * refusal from another host is not fixed on this server's ports page. Plain
 * HTTP on port 80 is no fault anywhere, so it is sent to the scan of 443.
 */
export function diagnosisLinks(scan: TLSScan): { label: string; href: string }[] {
  const failure = scan.failure
  if (!failure || failure.stage === "dns") return []
  if (scan.port === 80 && failure.reason === "plain-http")
    return [{ label: "Scan port 443", href: tlsReportHref({ host: scan.domain, port: 443 }) }]
  if (failure.where !== "here" && failure.where !== "unknown") return []
  if (failure.stage === "connect" && failure.reason === "unreachable") return []
  const links = [{ label: "Listening ports", href: `/proxy/ports?q=:${scan.port}` }]
  if (failure.stage === "connect" && failure.reason === "timeout")
    links.push({ label: "Firewall", href: "/security/firewall" })
  if (failure.stage === "handshake") links.push({ label: "Sites", href: "/proxy/sites" })
  return links
}

/**
 * A span of a certificate's term, in hours for a term under ten days: Let's
 * Encrypt's short-lived certificates run 160 hours, neither six days nor seven.
 */
function span(hours: number, termHours: number) {
  return termHours < 240 ? `${hours} hours` : `${Math.round(hours / 24)} days`
}

/** The term and its renewal window: "90 days, renewal due in the last 30 days". */
export function termText(scan: Pick<TLSScan, "lifetimeHours" | "renewalWindowHours">) {
  const term = scan.lifetimeHours
  if (!term) return undefined
  const window = scan.renewalWindowHours
  return window
    ? `${span(term, term)}, renewal due in the last ${span(window, term)}`
    : span(term, term)
}

/**
 * The figure for how long a certificate has left: whole days, as the
 * Certificates page counts them, and hours for its last two days, which "1d"
 * and "0d" would hide.
 */
export function certificateLeft(cert: { notAfter: string; daysLeft: number }, now: number): string {
  if (cert.daysLeft >= 2) return `${cert.daysLeft}d`
  const hours = Math.floor((Date.parse(cert.notAfter) - now) / 3_600_000)
  return hours >= 1 ? `${hours}h` : "<1h"
}

/** The share of a certificate's term still to run, 0–100, for the meter under the figure. */
export function termLeft(cert: { notBefore: string; notAfter: string }, now: number): number {
  const start = Date.parse(cert.notBefore)
  const end = Date.parse(cert.notAfter)
  if (!(end > start)) return 0
  return Math.max(0, Math.min(100, ((end - now) / (end - start)) * 100))
}
