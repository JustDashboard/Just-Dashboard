/**
 * The TLS report's readings, worked out apart from the page so each can be
 * tested: where a plain-HTTP visitor ends up, how far a failed scan got and
 * where to look next, and a certificate's term in the unit that reads true.
 */

import type { Verdict } from "@/components/status-dot"
import type { Tone } from "@/components/tone"
import type {
  AddressKind,
  AddressScan,
  GradeCheck,
  HTTPScan,
  RedirectHop,
  TLSScan,
} from "./proxy/types-tls"
import { targetLabel, tlsReportHref } from "./scan-target"

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

/**
 * A word as one POSIX shell argument: left bare when nothing in it is special,
 * single-quoted otherwise, since a name or a pin pasted into a terminal must
 * arrive as typed. Brackets are quoted too: an IPv6 address's are a glob to
 * zsh, which refuses a glob that matches nothing.
 */
export function shellQuote(word: string): string {
  if (/^[A-Za-z0-9_.,:/@%+=-]+$/.test(word)) return word
  return `'${word.replace(/'/g, `'\\''`)}'`
}

/** A command that shows for yourself what the scan saw. */
export type ReproduceCommand = { label: string; command: string }

/**
 * The commands that repeat the scan by hand. An address is sent no SNI, as the
 * scan sends none; an IPv6 one is bracketed, and curl is told with -g not to
 * read those brackets as a glob. curl is offered only where an HTTP answer was
 * read: the headers of a mail server are not a question.
 */
export function reproduceCommands(scan: TLSScan): ReproduceCommand[] {
  const host = scan.domain
  const ipv6 = host.includes(":")
  // A scan sent to another address dials it and still names the host, which
  // is -connect beside -servername for openssl and --resolve for curl.
  const dialled = scan.connectTo ?? host
  const connect = dialled.includes(":") ? `[${dialled}]:${scan.port}` : `${dialled}:${scan.port}`
  const sni = isAddress(host) ? "" : ` -servername ${shellQuote(host)}`
  const sClient = `openssl s_client -connect ${shellQuote(connect)}${sni}`
  const commands: ReproduceCommand[] = [
    { label: "Handshake and chain", command: `${sClient} -showcerts </dev/null` },
    {
      label: "Leaf certificate",
      command: `${sClient} </dev/null 2>/dev/null | openssl x509 -noout -text`,
    },
  ]
  if (scan.http?.service !== "http") return commands

  // A zone in an address is a percent sign, which a URL spells %25.
  const urlHost = ipv6 ? `[${host.replace(/%/g, "%25")}]` : host
  const url = `https://${urlHost}${scan.port === 443 ? "" : `:${scan.port}`}/`
  const resolve = (port: number) =>
    scan.connectTo
      ? ` --resolve ${shellQuote(`${host}:${port}:${scan.connectTo.includes(":") ? `[${scan.connectTo}]` : scan.connectTo}`)}`
      : ""
  const curl = `${ipv6 ? "curl -g" : "curl"}${resolve(scan.port)}`
  commands.push({ label: "HTTPS headers", command: `${curl} -sSI ${shellQuote(url)}` })
  commands.push({
    label: "Plain HTTP redirect",
    command: `${ipv6 ? "curl -g" : "curl"}${resolve(80)} -sSIL ${shellQuote(`http://${urlHost}/`)}`,
  })
  if (scan.spkiPin)
    commands.push({
      label: "Key pin",
      command: `${curl} -sS -o /dev/null --pinnedpubkey ${shellQuote(`sha256//${scan.spkiPin}`)} ${shellQuote(url)}`,
    })
  return commands
}

/** The chain as the server sent it, leaf first, as one PEM file. */
export function chainPem(scan: TLSScan): string {
  return scan.chain.map((link) => link.pem).join("")
}

/** A file name for something saved from a report: the target, with nothing a file system minds. */
export function reportFileName(scan: TLSScan, suffix: string): string {
  const name = targetLabel({ host: scan.domain, port: scan.port }).replace(/[^A-Za-z0-9.-]+/g, "_")
  return `${name}-${suffix}`
}

/** The element id a finding is linked to by, as #finding-… after the report's address. */
export function findingAnchor(id: string): string {
  return `finding-${id.replace(/[^A-Za-z0-9.-]+/g, "-")}`
}

/** A rule's outcome, as the checklist and the Markdown both word it. */
export function checkOutcome(check: GradeCheck): string {
  if (check.na) return "not judged"
  return check.passed ? "passed" : `failed, caps at ${check.cap}`
}

/** A table cell: one line, with its pipes escaped. */
function cell(value: string | number | undefined): string {
  if (value === undefined || value === "") return "—"
  return String(value).replace(/\s+/g, " ").replace(/\|/g, "\\|")
}

/**
 * The report as Markdown, for a ticket or a chat: the grade and why, the
 * findings with their advice, and the certificate's facts. Dates are ISO, since
 * the reader is not in this browser's locale.
 */
export function reportToMarkdown(scan: TLSScan): string {
  const lines = [
    `# TLS report: ${targetLabel({ host: scan.domain, port: scan.port })}`,
    "",
    `**Grade ${scan.grade}**: ${scan.summary}`,
    "",
    `Checked ${scan.checkedAt}`,
  ]
  const connected = connectedTo(scan)
  if (connected) lines.push("", connected)
  lines.push("", "## Findings", "")
  if (scan.findings.length === 0) lines.push("None.")
  for (const finding of scan.findings) {
    lines.push(`- **${finding.level}**: ${finding.title}. ${finding.detail}`)
    if (finding.advice) lines.push(`  ${finding.advice}`)
  }
  if (!scan.reachable) return `${lines.join("\n")}\n`

  if (scan.checks.length) {
    lines.push("", "## How the grade was reached", "", "| Rule | Result |", "| --- | --- |")
    for (const check of scan.checks) lines.push(`| ${cell(check.title)} | ${checkOutcome(check)} |`)
  }

  const cert = scan.certificate
  const facts: [string, string | number | undefined][] = [
    ["Subject", cert?.name],
    ["Names", cert?.domains.join(", ")],
    ["Issuer", cert?.issuer],
    ["Valid from", cert?.notBefore],
    ["Valid until", cert?.notAfter],
    ["Lifetime", termText(scan)],
    ["Key", [scan.keyType, scan.keyBits && `${scan.keyBits} bits`].filter(Boolean).join(" ")],
    ["Signature", scan.signatureAlgorithm],
    ["Serial", scan.serial],
    ["SHA-256", scan.fingerprint],
    ["SPKI pin", scan.spkiPin],
    ["Negotiated", [scan.negotiated, scan.cipherSuite].filter(Boolean).join(" ")],
    ["OCSP stapled", scan.ocspStapled ? "yes" : "no"],
  ]
  lines.push("", "## Certificate", "", "| | |", "| --- | --- |")
  for (const [label, value] of facts) lines.push(`| ${label} | ${cell(value)} |`)

  lines.push("", "## Protocol versions", "")
  for (const protocol of scan.protocols)
    lines.push(
      `- ${protocol.name}: ${protocol.status}${protocol.detail ? ` (${protocol.detail})` : ""}`,
    )

  if (scan.addresses?.length) {
    lines.push("", "## Addresses", "", "| Address | Whose | Answer |", "| --- | --- | --- |")
    for (const address of scan.addresses)
      lines.push(
        `| ${address.address} | ${ADDRESS_KINDS[address.kind]} | ${cell(addressAnswer(address))} |`,
      )
  }

  lines.push("", "## Chain as presented", "")
  scan.chain.forEach((link, index) =>
    lines.push(`${index + 1}. ${link.subject}, issued by ${link.issuer}, until ${link.notAfter}`),
  )
  return `${lines.join("\n")}\n`
}

const ADDRESS_KINDS: Record<AddressKind, string> = {
  here: "this server",
  cloudflare: "Cloudflare edge",
  elsewhere: "another host",
  unknown: "cannot tell whose",
}

/** Whose an address is, in words. */
export function addressKindLabel(kind: AddressKind): string {
  return ADDRESS_KINDS[kind]
}

/** The IP of an ip:port, without the brackets an IPv6 one is written in. */
function addressIP(address: string): string {
  if (address.startsWith("[")) return address.slice(1, address.indexOf("]"))
  return address.slice(0, address.lastIndexOf(":"))
}

/**
 * Which address the scan reached and whose it is, the one line that says
 * whether the grade is the CDN's or the origin's.
 */
export function connectedTo(scan: TLSScan): string | undefined {
  if (!scan.address) return undefined
  const kind = scan.addressKind ? ` — ${ADDRESS_KINDS[scan.addressKind]}` : ""
  const asked = scan.connectTo ? ", as asked in place of its DNS records" : ""
  return `Connected to ${addressIP(scan.address)}${kind}${asked}`
}

/** What one address served, in a line: the certificate, or why nothing. */
export function addressAnswer(address: AddressScan): string {
  if (!address.reachable) return address.error ?? "no handshake"
  const parts = [address.negotiated, address.subject && `${address.subject} from ${address.issuer}`]
  if (address.notAfter) parts.push(`until ${address.notAfter.slice(0, 10)}`)
  if (address.legacyOnly) parts.push("legacy offer only")
  return parts.filter(Boolean).join(" · ")
}
