/**
 * The TLS report's picture, worked out apart from the page so each verdict
 * can be tested: the way a scan reached the name it was asked about — this
 * server as the visitor, the name and the address it resolved to, whatever
 * answered the handshake, the certificate it sent, the intermediates, and
 * the root a verifier ended on — each step with the verdict its colour says.
 *
 * A scan that never completed a handshake is the same row cut short: the
 * name, the port and the handshake, the step that failed in red and the ones
 * after it not reached (`failureSteps`).
 */

import type { FleetTarget } from "./proxy/types-tls-fleet"
import type { AddressKind, TLSOrigin, TLSScan } from "./proxy/types-tls"
import { failureSteps, STARTTLS_NAMES } from "./tls-report"
import { targetLabel } from "./scan-target"

/** A node's verdict, which is its colour. `neutral` is a step that was not judged. */
export type ChainTone = "success" | "warning" | "danger" | "neutral"

export type ChainKind =
  "visitor" | "domain" | "proxy" | "leaf" | "intermediate" | "root" | "name" | "port" | "handshake"

export type ChainNode = {
  key: string
  kind: ChainKind
  eyebrow: string
  title: string
  hint?: string
  tone: ChainTone
  /** The product drawn on the node's tile: the engine that answered, the authority that signed. */
  product?: string
  /** A name or an address, set in mono. */
  mono?: boolean
  /** The site whose file answered, which the proxy node opens. */
  site?: string
  /** The line into this node: dashed where nothing arrives at its end. */
  wire: { tone: "default" | "warning" | "danger"; dashed: boolean }
}

const WHOSE: Record<AddressKind, string> = {
  here: "this server",
  cloudflare: "Cloudflare",
  elsewhere: "another host",
  unknown: "cannot tell whose",
}

/** What the file behind the answer says, in a few words, and its verdict. */
const ORIGIN: Record<TLSOrigin["state"], { words: string; tone: ChainTone }> = {
  current: { words: "current file", tone: "success" },
  stale: { words: "stale, renewed on disk", tone: "warning" },
  other: { words: "another certificate here", tone: "warning" },
  foreign: { words: "not a file on this host", tone: "neutral" },
  unserved: { words: "no site serves the name", tone: "danger" },
  unknown: { words: "file not traced", tone: "neutral" },
}

/** The engine a `Server:` header names, drawn as itself where this product knows it. */
export function serverProduct(server: string | undefined): string | undefined {
  const name = (server ?? "").toLowerCase()
  if (name.startsWith("nginx") || name.startsWith("openresty")) return "nginx"
  if (name.startsWith("caddy")) return "caddy"
  if (name.startsWith("cloudflare")) return "cloudflare"
  if (name.startsWith("apache")) return "apache"
  return undefined
}

/** The IP of an ip:port, without the brackets an IPv6 one is written in. */
function addressIP(address: string): string {
  if (address.startsWith("[")) return address.slice(1, address.indexOf("]"))
  const colon = address.lastIndexOf(":")
  return colon < 0 ? address : address.slice(0, colon)
}

function isAddress(host: string) {
  return host.includes(":") || /^\d{1,3}(\.\d{1,3}){3}$/.test(host)
}

/**
 * How long a certificate has left, in the unit that reads true: days while
 * there are two or more, hours in its last day and a half.
 */
export function timeLeft(
  cert: { notAfter?: string; daysLeft: number; expired?: boolean },
  now: number,
) {
  if (cert.expired) return "expired"
  if (cert.daysLeft >= 2) return `${cert.daysLeft} days left`
  const hours = Math.floor((Date.parse(cert.notAfter ?? "") - now) / 3_600_000)
  return hours >= 1 ? `${hours} ${hours === 1 ? "hour" : "hours"} left` : "under an hour left"
}

/** The visitor: this server, and what it asked for. */
function visitorNode(scan: TLSScan): ChainNode {
  const asked = scan.starttls
    ? `${STARTTLS_NAMES[scan.starttls]} STARTTLS`
    : scan.http && scan.http.service !== "other"
      ? `${scan.http.method ?? "GET"} ${scan.http.path ?? "/"}`
      : "a handshake"
  return {
    key: "visitor",
    kind: "visitor",
    eyebrow: "Visitor",
    title: "This server",
    hint: asked,
    tone: "neutral",
    wire: { tone: "default", dashed: false },
  }
}

/** The name asked about, and the address the scan reached. */
function domainNode(scan: TLSScan): ChainNode {
  const others = (scan.addresses ?? []).filter(
    (address) =>
      !address.reachable ||
      (scan.fingerprint !== undefined &&
        address.fingerprint !== undefined &&
        address.fingerprint !== scan.fingerprint),
  )
  const reached = scan.address
    ? [addressIP(scan.address), scan.addressKind && WHOSE[scan.addressKind]]
        .filter(Boolean)
        .join(" · ")
    : undefined
  return {
    key: "domain",
    kind: "domain",
    eyebrow: isAddress(scan.domain) ? "Address" : "Domain",
    title: targetLabel({ host: scan.domain, port: scan.port }),
    hint:
      others.length > 0
        ? `${others.length} of ${scan.addresses?.length} addresses differ`
        : scan.connectTo
          ? `dialled ${scan.connectTo}`
          : reached,
    tone: others.length > 0 ? "warning" : "success",
    mono: true,
    wire: { tone: "default", dashed: false },
  }
}

/** Whatever answered the handshake: the nginx site traced on this host, or the engine that said so. */
function proxyNode(scan: TLSScan): ChainNode {
  const protocol = scan.legacyOnly
    ? `${scan.negotiated ?? "TLS"}, legacy offer only`
    : scan.negotiated
  const protocolTone: ChainTone = scan.legacyOnly
    ? "danger"
    : scan.negotiated === "TLS 1.3"
      ? "success"
      : scan.negotiated === "TLS 1.2"
        ? "success"
        : "warning"
  const origin = scan.origin
  if (origin) {
    const state = ORIGIN[origin.state]
    const tone = worst(state.tone === "neutral" ? "success" : state.tone, protocolTone)
    return {
      key: "proxy",
      kind: "proxy",
      eyebrow: "Served by",
      title:
        origin.site ??
        (origin.state === "foreign"
          ? (scan.http?.server ?? "another server")
          : origin.state === "unserved"
            ? "no site"
            : "nginx"),
      hint: [protocol, origin.state === "current" ? undefined : state.words]
        .filter(Boolean)
        .join(" · "),
      tone,
      product: origin.state === "foreign" ? serverProduct(scan.http?.server) : "nginx",
      mono: Boolean(origin.site),
      site: origin.site,
      wire: { tone: "default", dashed: false },
    }
  }
  return {
    key: "proxy",
    kind: "proxy",
    eyebrow: "Served by",
    title: scan.http?.server || (scan.addressKind === "cloudflare" ? "Cloudflare" : "the server"),
    hint: protocol,
    tone: protocolTone,
    product:
      serverProduct(scan.http?.server) ??
      (scan.addressKind === "cloudflare" ? "cloudflare" : undefined),
    wire: { tone: "default", dashed: false },
  }
}

/** The worse of two verdicts. */
function worst(a: ChainTone, b: ChainTone): ChainTone {
  const rank: ChainTone[] = ["neutral", "success", "warning", "danger"]
  return rank.indexOf(a) >= rank.indexOf(b) ? a : b
}

/**
 * The leaf's verdict, by the Certificates page's own rule: amber inside its
 * renewal window, red once expired, red too when it is not for the name.
 */
function leafTone(scan: TLSScan): ChainTone {
  const cert = scan.certificate
  if (!cert || cert.expired || !scan.nameMatches) return "danger"
  return cert.expiring ? "warning" : "success"
}

function leafNode(scan: TLSScan, now: number): ChainNode {
  const cert = scan.certificate
  const first = scan.chain[0]
  const tone = leafTone(scan)
  const key = scan.keyType ? `${scan.keyType}${scan.keyBits ? ` ${scan.keyBits}` : ""}` : undefined
  return {
    key: "leaf",
    kind: "leaf",
    eyebrow: "Certificate",
    title: cert?.name || first?.subject || "none sent",
    hint: !cert
      ? "the handshake completed without one"
      : !scan.nameMatches
        ? "not for this name"
        : [timeLeft(cert, now), key].filter(Boolean).join(" · "),
    tone,
    mono: true,
    wire: {
      tone: tone === "danger" ? "danger" : tone === "warning" ? "warning" : "default",
      dashed: !cert,
    },
  }
}

/**
 * The certificates sent after the leaf that are not a root: the first drawn,
 * the rest counted. An incomplete chain draws the intermediate it should
 * have sent, missing, on a dashed line.
 */
function intermediateNode(scan: TLSScan): ChainNode | undefined {
  const sent = scan.chain.slice(1).filter((link) => !link.selfIssued)
  if (!scan.chainComplete && scan.certificate) {
    return {
      key: "intermediate",
      kind: "intermediate",
      eyebrow: "Intermediate",
      title: "not sent",
      hint: "browsers may fetch it, other clients fail",
      tone: "warning",
      wire: { tone: "warning", dashed: true },
    }
  }
  const first = sent[0]
  if (!first) return undefined
  const sha1 = /^SHA1-|-SHA1$/.test(first.signatureAlgorithm ?? "")
  const outOfOrder = scan.chainAudit?.order === "out-of-order"
  const tone: ChainTone = sha1 ? "danger" : outOfOrder ? "warning" : "success"
  return {
    key: "intermediate",
    kind: "intermediate",
    eyebrow: sent.length > 1 ? `Intermediates · ${sent.length}` : "Intermediate",
    title: first.subject,
    hint: sha1
      ? "signed with SHA-1"
      : outOfOrder
        ? "sent out of order"
        : sent.length > 1
          ? `then ${sent
              .slice(1)
              .map((link) => link.subject)
              .join(", ")}`
          : `issued by ${first.issuer}`,
    tone,
    wire: {
      tone: tone === "danger" ? "danger" : tone === "warning" ? "warning" : "default",
      dashed: false,
    },
  }
}

/** The root a verifier ended on, from the store or as sent, or that none was reached. */
function rootNode(scan: TLSScan): ChainNode {
  const fromPath = scan.trustPath?.find((step) => step.root)
  const last = scan.chain[scan.chain.length - 1]
  const name = fromPath?.subject ?? (last?.selfIssued ? last.subject : last?.issuer) ?? "—"
  return {
    key: "root",
    kind: "root",
    eyebrow: "Root",
    title: name,
    hint: scan.trusted
      ? fromPath && !fromPath.sent
        ? "trusted, from the store"
        : "trusted"
      : "not trusted",
    tone: scan.trusted ? "success" : "danger",
    wire: { tone: scan.trusted ? "default" : "danger", dashed: !scan.trusted },
  }
}

/**
 * Every step of a scan that completed a handshake: visitor, domain, what
 * answered, the leaf, the intermediate (or the one missing), the root.
 */
export function chainNodes(scan: TLSScan, now: number): ChainNode[] {
  if (!scan.reachable) return failureNodes(scan)
  const nodes = [visitorNode(scan), domainNode(scan), proxyNode(scan), leafNode(scan, now)]
  const intermediate = intermediateNode(scan)
  if (intermediate) nodes.push(intermediate)
  nodes.push(rootNode(scan))
  return nodes
}

const STEP_KIND: ChainKind[] = ["name", "port", "handshake"]

/**
 * A scan that never completed a handshake: this server, then the three steps
 * a connection takes, each passed, failed or never reached.
 */
export function failureNodes(scan: TLSScan): ChainNode[] {
  const steps = failureSteps(scan)
  let failed = false
  return [
    {
      ...visitorNode(scan),
      hint: scan.starttls ? `${STARTTLS_NAMES[scan.starttls]} STARTTLS` : "a handshake",
    },
    ...steps.map((step, index): ChainNode => {
      const reached = !failed
      const tone: ChainTone = step.tone === "danger" ? "danger" : reached ? "success" : "neutral"
      if (step.tone === "danger") failed = true
      return {
        key: STEP_KIND[index],
        kind: STEP_KIND[index],
        eyebrow: step.label,
        title: step.value,
        hint: step.hint || undefined,
        tone: step.value === "—" ? "neutral" : tone,
        mono: index === 0 && step.value === "address",
        wire: {
          tone: tone === "danger" ? "danger" : "default",
          dashed: !reached || tone === "danger",
        },
      }
    }),
  ]
}

/**
 * The same row before anything is asked, or while the first answer is on its
 * way: the name being asked about where there is one, every other step
 * unjudged.
 */
export function pendingNodes(target: string | undefined): ChainNode[] {
  const wire = { tone: "default" as const, dashed: true }
  return [
    {
      key: "visitor",
      kind: "visitor",
      eyebrow: "Visitor",
      title: "This server",
      tone: "neutral",
      wire,
    },
    {
      key: "domain",
      kind: "domain",
      eyebrow: "Domain",
      title: target ?? "a name",
      tone: "neutral",
      mono: Boolean(target),
      wire,
    },
    { key: "proxy", kind: "proxy", eyebrow: "Served by", title: "—", tone: "neutral", wire },
    { key: "leaf", kind: "leaf", eyebrow: "Certificate", title: "—", tone: "neutral", wire },
    {
      key: "intermediate",
      kind: "intermediate",
      eyebrow: "Intermediate",
      title: "—",
      tone: "neutral",
      wire,
    },
    { key: "root", kind: "root", eyebrow: "Root", title: "—", tone: "neutral", wire },
  ]
}

/** Worst to best, so a higher rank is a better grade. */
export const GRADES = ["F", "E", "D", "C", "B", "A-", "A", "A+"]

/** A grade's verdict, the colour its word is drawn in. */
export function gradeVerdict(grade: string | undefined): ChainTone {
  if (!grade) return "neutral"
  if (grade === "A+" || grade === "A") return "success"
  if (grade === "A-" || grade === "B" || grade === "C") return "warning"
  return "danger"
}

/** Worst first, and a target never scanned after every graded one. */
export function fleetRank(target: FleetTarget) {
  return target.scan ? Math.max(0, GRADES.indexOf(target.scan.grade)) : GRADES.length
}

/** What a fleet row says of its certificate, and how loudly. */
export function fleetLife(target: FleetTarget): { words: string; tone: ChainTone } {
  const scan = target.scan
  if (!scan) return { words: "not scanned", tone: "neutral" }
  if (!scan.reachable) return { words: "unreachable", tone: "danger" }
  if (scan.expired) return { words: "expired", tone: "danger" }
  if (scan.daysLeft === undefined) return { words: "no certificate", tone: "neutral" }
  return {
    words: `${scan.daysLeft} ${scan.daysLeft === 1 ? "day" : "days"} left`,
    tone: scan.expiring ? "warning" : "success",
  }
}

/** A fleet's figures in words, for the identity line: only what is not zero is said. */
export function fleetFacts(targets: FleetTarget[]) {
  const scanned = targets.filter((t) => t.scan)
  return {
    total: targets.length,
    scanned: scanned.length,
    belowA: scanned.filter((t) => gradeVerdict(t.scan?.grade) !== "success").length,
    expiring: targets.filter((t) => t.scan?.expiring || t.scan?.expired).length,
    unreachable: scanned.filter((t) => !t.scan?.reachable).length,
    /** The newest stored report, which says how old the picture is. */
    newest: scanned.reduce<string | undefined>(
      (at, t) => (!at || (t.scan && t.scan.checkedAt > at) ? t.scan?.checkedAt : at),
      undefined,
    ),
  }
}
