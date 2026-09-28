export type Certificate = {
  name: string
  path: string
  domains: string[]
  issuer: string
  notBefore: string
  notAfter: string
  daysLeft: number
  expired: boolean
  expiring: boolean
  selfSigned: boolean
  source: string
  error?: string
  /** The nginx sites whose ssl_certificate points at this file. */
  usedBy: string[]
  /** SHA-256 of the DER and the serial, in openssl's uppercase colon form. */
  fingerprint?: string
  serial?: string
  /** A test certificate: a staging authority signed it, so browsers refuse it whatever its days. */
  staging?: boolean
}

export type CertbotCert = {
  name: string
  domains: string[]
  expiry: string
  daysLeft: number
  valid: boolean
  certPath?: string
  keyPath?: string
  serial?: string
  /** A test certificate from a staging authority: browsers refuse it whatever its days. */
  staging?: boolean
  /** Why the lineage's certificate could not be read. */
  error?: string
}

export type CertbotState = {
  available: boolean
  version?: string
  certs: CertbotCert[]
  /** Whether anything is scheduled to renew these, and what. */
  autoRenew: boolean
  renewSource?: string
  /** A certbot timer systemd knows but is not running: the thing to turn on. */
  renewUnit?: string
  /** Why the lineages could not be read; the renewal fields are answered regardless. */
  error?: string
  /**
   * The ACME directory issuance orders from when it is not Let's Encrypt's: a
   * test run rehearses with that authority itself, under its own limits.
   */
  directory?: string
}

/** A certbot DNS plugin — the only way to a wildcard, or past a CDN. */
export type DNSProvider = {
  key: string
  name: string
  plugin: string
  installed: boolean
  credentials: string
  defaultWait: number
  /** A token is saved for this provider. The token itself is never read back. */
  hasCredentials: boolean
}

export type ImportResult = {
  name: string
  certPath: string
  keyPath: string
  certificate: Certificate
  chainComplete: boolean
  /** An import of the same name was overwritten; nginx serves the new one after a reload. */
  replaced: boolean
  warnings: string[]
}
