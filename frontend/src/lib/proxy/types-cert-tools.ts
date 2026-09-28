import type { Certificate } from "./types-certs"

/** The kind of key a certificate made on this server is given. */
export type KeyType = "ecdsa-p256" | "ecdsa-p384" | "rsa-2048" | "rsa-3072" | "rsa-4096"

/** What an organisation-validated certificate says about its owner; a domain-validated one ignores it. */
export type CSRSubject = {
  organization?: string
  locality?: string
  province?: string
  country?: string
}

/** A signing request whose certificate has not arrived yet. Its key never leaves the server. */
export type SigningRequest = {
  name: string
  domains: string[]
  subject: CSRSubject
  keyType?: KeyType
  created: string
  /** The request itself, which is what the authority is sent. */
  csr: string
  /** Where its key waits, readable by root only. */
  keyPath: string
  /** The certificate kept under the same name now, which completing the request replaces. */
  replaces?: Certificate
  /** Why the request cannot be completed as it stands. */
  error?: string
}

/** A certificate the local CA signed, kept with the imports and renewed here. */
export type LocalCALeaf = {
  name: string
  path: string
  domains: string[]
  notBefore: string
  notAfter: string
  daysLeft: number
  /** The first daily check that finds it due. */
  renewsAt: string
  usedBy: string[]
  /** Why it cannot be renewed here. */
  error?: string
}

/** What one pass of the daily renewal did. */
export type LocalCACheck = {
  at: string
  renewed: string[]
  /** The enabled sites nginx was reloaded for. */
  reloaded: string[]
  /** "name: reason" for each certificate due that could not be renewed. */
  failed: string[]
  /** Why the pass could not finish: the CA's key unreadable, or nginx refusing the reload. */
  error?: string
}

/** The local CA: a root of this server's own, trusted only on devices where it is installed. */
export type LocalCA = {
  exists: boolean
  /** The root's common name, which a device's trust settings show. */
  name?: string
  notBefore?: string
  notAfter?: string
  fingerprint?: string
  /** Why it can issue nothing now. */
  error?: string
  leaves: LocalCALeaf[]
  /** How many days before expiry the daily check renews. */
  renewBefore: number
  lastCheck?: LocalCACheck
  /** When the daily check next runs; absent until the loop has started. */
  nextCheck?: string
}

/** A certificate the public Certificate Transparency logs hold, as crt.sh lists it. */
export type CTCertificate = {
  id: number
  /** Its page on crt.sh. */
  url: string
  /** The issuing CA's common name (R11). */
  issuer: string
  /** Its organisation (Let's Encrypt): what a certificate is judged by. */
  issuerOrg: string
  names: string[]
  serial: string
  notBefore: string
  notAfter: string
  logged: string
  /** Its serial is one this host holds, live or in certbot's archive. */
  ours: boolean
  /** Not held here, and from an authority that signed none of this host's certificates. */
  unexpectedIssuer: boolean
}

/** What the logs hold for one registered domain and its subdomains. */
export type CTDomain = {
  domain: string
  certificates: CTCertificate[]
  checked?: string
  error?: string
}

/** The transparency monitor: off, or its report. */
export type CTReport = {
  enabled: boolean
  source?: string
  domains?: CTDomain[]
  /** Registered domains past the per-report limit, left unasked. */
  skipped?: string[]
  /** Organisations that signed this host's own public certificates. */
  expectedIssuers?: string[]
}
