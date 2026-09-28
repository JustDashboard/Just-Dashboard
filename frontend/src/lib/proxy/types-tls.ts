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
  notBefore: string
  notAfter: string
  serial: string
  /** SHA-256 of the certificate, colon hex. */
  fingerprint: string
  signatureAlgorithm: string
  dnsNames?: string[]
  extKeyUsage?: string[]
  /** Whether the certificate sent after this one is its issuer. */
  issuedByNext: boolean
  isCa: boolean
  keyType?: string
  keyBits?: number
  selfIssued: boolean
  /** The certificate as the server sent it, PEM-encoded. */
  pem: string
}

/** One step of the path a verifier built from the leaf to a root in this machine's store. */
export type TrustLink = {
  subject: string
  notAfter: string
  /** Sent by the server, rather than supplied by the store. */
  sent: boolean
  root: boolean
}

/** What the chain as sent says beyond whether it is trusted. */
export type ChainAudit = {
  order: "ordered" | "out-of-order" | "extra"
  rootSent: boolean
  sha1?: string[]
  leafDays: number
  tooLong: boolean
  mustStaple: boolean
  scts: number
  sctSources?: ("certificate" | "tls" | "ocsp")[]
  serverAuth: boolean
}

/** The issuer's answer on whether the leaf is revoked, believed only when its signature checks. */
export type Revocation = {
  status: "good" | "revoked" | "unknown" | "unchecked"
  method?: "stapled" | "ocsp" | "crl"
  source?: string
  revokedAt?: string
  reason?: string
  thisUpdate?: string
  nextUpdate?: string
  detail?: string
  /** Reused from an earlier scan; it holds until the issuer's nextUpdate. */
  cached?: boolean
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

/**
 * Where the plain-HTTP chain ended: HTTPS on the host it started from or on
 * another one, an answer that is not a redirect, a loop, too many hops, a hop
 * that did not answer or pointed nowhere usable, or a hop into this machine's
 * network, which is not followed.
 */
export type RedirectVerdict =
  "same-host" | "other-host" | "stays-http" | "loop" | "too-many" | "dead-end" | "internal"

export type HTTPScan = {
  /**
   * "http" when HTTPS answered an HTTP request, "other" when the service is
   * known not to be a website (its port is registered to another protocol,
   * or it answered in one), "unknown" when the request got no answer that
   * says either.
   */
  service: "http" | "other" | "unknown"
  /** The protocol the port is registered to, when that is why no request was sent. */
  serviceName?: string
  /** The first line a service that is not HTTP answered with. */
  banner?: string
  statusCode: number
  server?: string
  /** Where the HTTPS answer redirects, when it does. */
  location?: string
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
  /** Every plain-HTTP request made, in order, and where that ended. */
  redirectChain: RedirectHop[]
  redirectVerdict?: RedirectVerdict
  hsts?: HSTS
  headers: HeaderCheck[]
  /** The HTTPS request as sent: the scan may ask for another method, path or Host. */
  method?: string
  path?: string
  /** The Host header sent, when it was not the scanned name. */
  host?: string
  /** Every header of the HTTPS answer, by name; Set-Cookie values are redacted by the server. */
  responseHeaders?: ResponseHeader[]
  /** The list was cut at the server's cap. */
  headersTruncated?: boolean
}

export type ResponseHeader = { name: string; value: string }

/**
 * One of the grade's rules as it applied to a scan. `cap` is the best letter
 * the scan can get while the rule fails; `na` is a rule the scan gave no means
 * to judge, which caps nothing.
 */
export type GradeCheck = {
  id: string
  category: "connection" | "protocol" | "certificate" | "http"
  title: string
  passed: boolean
  na: boolean
  cap: string
}

export type ScanFinding = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  advice?: string
  /** The remedy the page can carry out for this finding, as lib/tls-fixes.ts reads it. */
  fix?:
    | "renew"
    | "issue"
    | "force-https"
    | "hsts"
    | "security-headers"
    | "fullchain"
    | "protocols"
    | "server-tokens"
}

/** Where a directive is set in the configuration nginx loads. */
export type DirectiveUse = {
  path: string
  line: number
  value: string
  context: string
  /** The enclosing server block's server_name. */
  server?: string[]
}

/** One of hstspreload.org's submission rules and what the scan saw of it. */
export type PreloadRule = {
  id: string
  title: string
  passed: boolean
  detail: string
}

/**
 * A name on port 443 against the HSTS preload list's rules. For a subdomain,
 * `domain` is the registrable domain to scan instead and the one rule says so.
 */
export type PreloadCheck = {
  domain: string
  eligible: boolean
  rules: PreloadRule[]
}

/** How far a scan that never completed a handshake got, and why it stopped. */
export type ScanFailure = {
  stage: "dns" | "connect" | "handshake"
  /**
   * dns: no-such-host, timeout, error. connect: refused, timeout, unreachable,
   * error. handshake: alert, plain-http, not-tls, closed, timeout, error.
   */
  reason: string
  /** Where the failing connection went, once the name had resolved. */
  address?: string
  /** The TLS alert the server refused the handshake with. */
  alert?: string
  /** The first bytes a service that does not speak TLS sent back. */
  answer?: string
  /**
   * Whose answer it was: this server, Cloudflare's proxy, another host, or
   * unknown when the provider maps this server's public address in front of it.
   */
  where?: "here" | "cloudflare" | "elsewhere" | "unknown"
  /** The name resolved beside this server's own addresses. */
  dns?: DomainCheck
}

/**
 * A scanned name traced to this host's nginx. "stale" is a later renewal of
 * the answer on disk; "other" another certificate on this host answered;
 * "foreign" none on this host did.
 */
export type TLSOrigin = {
  state: "current" | "stale" | "other" | "foreign" | "unserved" | "unknown"
  site?: string
  match?: "exact" | "wildcard" | "regex" | "default"
  defaultFlag?: boolean
  file?: Certificate
  served?: Certificate
  detail: string
}

/** Whose an address is: this server, Cloudflare's proxy, another host, or cannot tell. */
export type AddressKind = "here" | "cloudflare" | "elsewhere" | "unknown"

/** One of a name's A or AAAA records, handshaken with on its own. */
export type AddressScan = {
  /** ip:port */
  address: string
  family: "IPv4" | "IPv6"
  kind: AddressKind
  reachable: boolean
  error?: string
  legacyOnly?: boolean
  negotiated?: string
  subject?: string
  issuer?: string
  notAfter?: string
  fingerprint?: string
}

export type TLSScan = {
  domain: string
  port: number
  checkedAt: string
  reachable: boolean
  error?: string
  failure?: ScanFailure
  /** The IP dialled in place of the name's records, as curl --resolve does. */
  connectTo?: string
  /** The ip:port the handshake reached, and whose it is. */
  address?: string
  addressKind?: AddressKind
  /** Every A and AAAA record, when the scan was asked to check each. */
  addresses?: AddressScan[]
  /** Why the name's addresses could not be listed. */
  addressesError?: string
  grade: string
  summary: string
  negotiated?: string
  cipherSuite?: string
  /**
   * The server refused the handshake a current client makes and took one
   * offering older versions and cipher suites too; negotiated and
   * cipherSuite are what it took then.
   */
  legacyOnly: boolean
  protocols: ProtocolResult[]
  certificate?: Certificate
  chain: ChainLink[]
  chainComplete: boolean
  trusted: boolean
  trustError?: string
  nameMatches: boolean
  trustPath?: TrustLink[]
  chainAudit?: ChainAudit
  revocation?: Revocation
  keyType?: string
  keyBits?: number
  signatureAlgorithm?: string
  fingerprint?: string
  serial?: string
  ocspStapled: boolean
  /** The OCSP responders the leaf names; with none there is nothing to staple. */
  ocspServers?: string[]
  crlUrls?: string[]
  /** Base64 SHA-256 of the leaf's public key: the pin curl's --pinnedpubkey takes. */
  spkiPin?: string
  /**
   * The leaf's whole term and the end of it in which renewal is due, in hours:
   * a short-lived certificate's 160 is no whole number of days.
   */
  lifetimeHours?: number
  renewalWindowHours?: number
  http?: HTTPScan
  preload?: PreloadCheck
  /** The nginx site and file behind the answer; absent for an address or another engine. */
  origin?: TLSOrigin
  findings: ScanFinding[]
  /** Every rule the grade applies, passed or not. */
  checks: GradeCheck[]
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

/** One answer as a resolver gave it, TTL included. */
export type DNSRecord = {
  name: string
  type: "A" | "AAAA" | "CNAME" | "TXT" | "CAA"
  ttl: number
  value: string
  /** CAA only: the issuer-critical flag and the property. */
  critical?: boolean
  tag?: string
}

export type DNSAddress = {
  type: "A" | "AAAA"
  address: string
  ttl: number
  /** "unknown" when this machine has no public address to compare with. */
  owner: "this-server" | "cloudflare" | "other" | "unknown"
}

/** RFC 8659's walk up the tree and what it means for issuance. */
export type CAAReport = {
  checked: string[]
  foundAt?: string
  records: DNSRecord[]
  issuers: string[]
  wildcardIssuers: string[]
  letsEncrypt: boolean
  letsEncryptWildcard: boolean
  verdict: string
  error?: string
}

export type ResolverView = {
  /** Empty for a system resolver resolv.conf does not name. */
  resolver: string
  label: string
  system?: boolean
  rcode?: string
  addresses: string[]
  error?: string
  /** Same addresses as the first resolver that answered. */
  agrees: boolean
}

/** GET /certificates/dns?deep=1: the TLS page's DNS panel. */
export type DNSReport = {
  domain: string
  checkedAt: string
  /** The resolver the records were read from. */
  source: string
  rcode?: string
  error?: string
  addresses: DNSAddress[]
  cnameChain: DNSRecord[]
  hostAddresses: string[]
  hostAddressesKnown: boolean
  pointsHere: boolean
  behindProxy: boolean
  summary: string
  caa: CAAReport
  acmeChallenge: DNSRecord[]
  acmeError?: string
  propagation: ResolverView[]
  consistent: boolean
  /** The saved comparison list. */
  resolvers: string[]
}

/** The request tester: one request to this machine's nginx, bypassing DNS. */
export type RequestHeader = { name: string; value: string }

export type RequestTest = {
  url: string
  method: string
  headers: RequestHeader[]
  connectTo?: "127.0.0.1" | "::1"
}

export type RequestHop = {
  url: string
  method: string
  /** The nginx site whose server_name takes the host on this port. */
  site: string
  status?: number
  proto?: string
  headers: RequestHeader[]
  location?: string
  /** At most the first 64 KiB; empty when the body is not text. */
  body: string
  bodyBytes: number
  truncated?: boolean
  binary?: boolean
  /** Milliseconds. */
  timings: { connect: number; tls?: number; firstByte?: number; total: number }
  tls?: {
    version: string
    cipherSuite: string
    alpn?: string
    certificate?: Certificate
    origin?: TLSOrigin
  }
  error?: string
}

export type RequestResult = {
  connectTo: string
  hops: RequestHop[]
  /** Why a redirect was not followed. */
  stopped?: "limit" | "loop" | "elsewhere"
}
