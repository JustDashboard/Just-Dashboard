import type { Certificate } from "./types-certs"

/** The live TLS report: what a visitor actually gets, not what is on disk. */
export type ProtocolResult = {
  name: string
  status: "offered" | "refused" | "unknown"
  /** What the server said, or why nothing it said was heard. */
  detail?: string
}

export type ChainLink = {
  subject: string
  issuer: string
  notAfter: string
  isCa: boolean
  keyType?: string
  keyBits?: number
  selfIssued: boolean
}

export type HSTS = {
  maxAge: number
  includeSubDomains: boolean
  preload: boolean
  raw: string
}

export type HeaderCheck = {
  name: string
  value?: string
  present: boolean
  level: "important" | "optional"
  detail: string
}

/** One plain-HTTP request: its status and where it pointed, or why it did not answer. */
export type RedirectHop = {
  url: string
  status?: number
  location?: string
  error?: string
  /**
   * Not requested: a remote site's redirect pointed at this machine or its
   * private network, where the scan does not follow it.
   */
  internal?: boolean
}

export type HTTPScan = {
  statusCode: number
  server?: string
  /**
   * Why HTTPS gave no HTTP response — the service is not a website, or it is
   * failing. Nothing else on the HTTP side was measured then.
   */
  httpsError?: string
  /** Whether plain HTTP ends up at HTTPS, however many hops it takes. */
  plainRedirects: boolean
  /** The first hop's answer. */
  plainStatus?: number
  plainLocation?: string
  /** Why port 80 did not answer at all. */
  plainError?: string
  plainErrorKind?: "refused" | "timeout" | "dns" | "other"
  /** Every plain-HTTP request made, in order. */
  redirectChain: RedirectHop[]
  hsts?: HSTS
  headers: HeaderCheck[]
}

export type ScanFinding = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  advice?: string
}

export type TLSScan = {
  domain: string
  port: number
  checkedAt: string
  reachable: boolean
  error?: string
  grade: string
  summary: string
  negotiated?: string
  cipherSuite?: string
  protocols: ProtocolResult[]
  certificate?: Certificate
  chain: ChainLink[]
  chainComplete: boolean
  trusted: boolean
  trustError?: string
  nameMatches: boolean
  keyType?: string
  keyBits?: number
  signatureAlgorithm?: string
  fingerprint?: string
  serial?: string
  ocspStapled: boolean
  /** The OCSP responders the leaf names; with none there is nothing to staple. */
  ocspServers?: string[]
  http?: HTTPScan
  findings: ScanFinding[]
}

export type DomainCheck = {
  domain: string
  addresses: string[]
  hostAddresses: string[]
  /**
   * False on every VPS behind provider NAT — AWS, Google Cloud, Azure and
   * Oracle all give the instance a private address and map a public one in
   * front of it — where the comparison cannot be made at all. Rendered as
   * "cannot tell", never as a mismatch.
   */
  hostAddressesKnown: boolean
  pointsHere: boolean
  behindProxy: boolean
  summary: string
  error?: string
}
