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
  /** This server's local CA signed it: trusted only where its root is installed. */
  localCA?: boolean
  /** A Caddy certificate's release copies: what deployments of its domain were activated with. */
  evidence?: CertificateEvidence[]
}

/** One of Caddy's caddy-<hash> release copies, kept beside the imports. */
export type CertificateEvidence = {
  name: string
  /** The directory holding the copy. */
  path: string
  domains: string[]
  notAfter: string
}

/** What a prune removed, and the copies asked for that a route or release names again. */
export type EvidencePrune = {
  removed: string[]
  kept: string[]
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
  /** When the certificate was issued: the other end of the meter. */
  notBefore: string
  /** The plugin that proves control on renewal: "nginx", "webroot", "dns-cloudflare". */
  authenticator?: string
  /** The plugin that deploys the renewed certificate; "nginx" reloads nginx itself. */
  installer?: string
  /** The folders the webroot plugin writes challenges into. */
  webroots?: string[]
  /** The DNS provider by name, where the dashboard knows the plugin. */
  dnsProvider?: string
  /** The lineage has a deploy hook of its own; what it does is not read. */
  deployHook?: boolean
  /** The lineage has a post hook of its own (--post-hook); what it does is not read. */
  postHook?: boolean
  /** The enabled nginx sites whose certificate is this lineage's. */
  servedBy?: string[]
  /** What will make the next renewal fail, each certain in certbot's code. */
  willFail?: string[]
  /** Why the last renewal run failed on this lineage, when nothing renewed it since. */
  lastFailure?: RenewalFailure
}

/** One certificate a renewal run failed to renew, in certbot's words. */
export type RenewalFailure = {
  lineage: string
  reason: string
  /** Saved after the failed run: the failure no longer describes it. */
  renewedSince?: boolean
}

/** The renewal schedule's last run and its next. */
export type RenewalHealth = {
  /** The service the timer starts, or certbot's log on a host that renews from cron. */
  source: string
  /** That service: what "Run now" starts. Absent on a cron host. */
  service?: string
  state: "ok" | "failed" | "recovered" | "running" | "never" | "unknown"
  lastRun?: string
  nextRun?: string
  exitStatus?: number
  /** Why the run failed when no certificate's failure says it. */
  reason?: string
  failures: RenewalFailure[]
  /** The first of an unbroken streak of failed runs ending with the last. */
  failingSince?: string
  /** Hooks the last run ran that exited with an error; certbot only warns, so the run passes. */
  hookFailures?: HookFailure[]
  /** Why the record could not be read. */
  error?: string
}

/** A hook certbot ran that exited with an error. */
export type HookFailure = {
  /** certbot's name for it: "pre-hook", "deploy-hook", "post-hook". */
  kind: string
  /** What certbot ran, where its log says. */
  command?: string
  code: number
  /** What the hook wrote to its error output. */
  output?: string
}

/** A line a renewal run printed; systemd's own about the run are marked. */
export type RenewalLine = { time: string; text: string; error?: boolean; systemd?: boolean }

export type RenewalRun = {
  start: string
  result: "succeeded" | "failed" | ""
  lines: RenewalLine[]
}

/** The recent renewal runs, newest first. */
export type RenewalLog = { source: string; runs: RenewalRun[] }

/** The deploy hook that reloads nginx after every renewal, and what else runs beside it. */
export type RenewalHook = {
  path: string
  state: "installed" | "missing" | "modified" | "foreign"
  /**
   * The other hooks certbot runs after a renewal: a deploy hook by its name,
   * "post/<name>" for one in renewal-hooks/post, "cli.ini's post-hook" for
   * one cli.ini sets.
   */
  others: string[]
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
   * The ACME directory issuance orders from when it is not one of Let's
   * Encrypt's: a test run rehearses with that authority itself, under its own
   * limits.
   */
  directory?: string
  /**
   * The configured directory signs test certificates (Let's Encrypt's staging
   * one among them): a real issuance from here is refused by browsers too.
   */
  testAuthority?: boolean
  /** What the renewal schedule did last and does next, when there is one. */
  health?: RenewalHealth
  /** The deploy hook that reloads nginx after a renewal. */
  reloadHook?: RenewalHook
  /** certbot here reloads nginx itself for a lineage its nginx plugin installed. */
  nginxReloads?: boolean
}

/** What one enabled nginx site that names a certificate serves, asked over a handshake. */
export type ServedCertificate = {
  site: string
  /** The server name asked for. */
  name: string
  address?: string
  issuer?: string
  serial?: string
  /** A staging authority signed the served certificate. */
  staging?: boolean
  /** The served certificate is the one the file holds now. */
  current: boolean
  /** Why the site could not be asked. */
  error?: string
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
