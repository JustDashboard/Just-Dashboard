import type { ScanFinding } from "./types-tls"

/**
 * The TLS report's deep scan: every suite each version accepts, the key
 * exchange groups, and the connection features a quick scan leaves out.
 */

/** One accepted cipher suite and how it rates. */
export type SuiteResult = {
  id: number
  /** The IANA name. */
  name: string
  /** The name ssl_ciphers takes. */
  openssl: string
  /** ECDHE, DHE, RSA, ECDH, or "any" in TLS 1.3, where the group decides. */
  kex: string
  auth: string
  cipher: string
  bits: number
  forwardSecrecy: boolean
  aead: boolean
  rating: "strong" | "weak" | "insecure"
  /** Why it is not strong. */
  reasons: string[]
}

export type VersionSuites = {
  name: string
  status: "accepted" | "refused" | "unknown"
  /** The server's refusal, why nothing could be read, or where a listing stopped. */
  detail?: string
  /** False when the listing stopped before the server refused. */
  complete: boolean
  /** Whose preference picks the suite, once two or more are accepted. */
  order?: "server" | "client" | "unclear"
  /** In the order the server chose them. */
  suites: SuiteResult[]
}

/** A TLS 1.3 key exchange group, asked for on its own. */
export type GroupResult = {
  id: number
  name: string
  postQuantum: boolean
  status: "accepted" | "refused" | "unknown"
  detail?: string
}

/** The site on this server that serves the name, as its form reads it. */
export type DeepSite = {
  name: string
  http2: boolean
}

export type ALPNResult = {
  offered: string[]
  /** Empty when the server chose no protocol. */
  negotiated: string
  /** Present only when the connection reached this server and a site serves the name. */
  site?: DeepSite
}

/** What a UDP port said to a QUIC packet. */
export type QUICProbe = {
  port: number
  answered: boolean
  versions?: string[]
  detail?: string
}

/** HTTP/3 as Alt-Svc advertises it, and whether QUIC answers there. */
export type HTTP3Result = {
  /** Whether the HTTPS request got an HTTP answer. */
  answered: boolean
  error?: string
  altSvc?: string
  advertised: boolean
  /** Set when the alternative names another host, which is not probed. */
  host?: string
  port?: number
  quic?: QUICProbe
}

export type ResumptionResult = {
  version: string
  status: "resumed" | "not-resumed" | "no-ticket" | "unknown"
  detail: string
}

/** A handshake naming no site, or a site nobody has. */
export type SNIProbe = {
  kind: "none" | "unknown"
  /** The name sent; absent for none. */
  sent?: string
  status: "refused" | "certificate" | "unknown"
  detail?: string
  subject?: string
  issuer?: string
  names?: string[]
  fingerprint?: string
  /** It is the certificate the scanned name gets. */
  sameAsNamed: boolean
}

export type DeepScan = {
  domain: string
  port: number
  checkedAt: string
  reachable: boolean
  error?: string
  /** Where every probe went, and whose address that is. */
  address?: string
  where?: "here" | "cloudflare" | "elsewhere" | "unknown"
  /** How many connections the deep scan opened. */
  connections: number
  versions: VersionSuites[]
  groups: GroupResult[]
  /** The group a current browser's offer gets, and in which version. */
  browserGroup?: string
  browserGroupVersion?: string
  /** The size of a DHE suite's group. */
  dhBits?: number
  alpn?: ALPNResult
  http3?: HTTP3Result
  resumption: ResumptionResult[]
  sni: SNIProbe[]
  findings: ScanFinding[]
}
