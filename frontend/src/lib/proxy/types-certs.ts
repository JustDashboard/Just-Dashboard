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
  /** The streams serving TLS with this file. */
  usedByStreams?: string[]
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
  /** The certbot everything above was read from and every job runs. */
  runtime: CertbotRuntime
  /** For each plugin the runtime lacks: the package that brings it, or why none can. */
  installs?: Record<string, CertbotInstall>
}

export type CertbotRuntime = {
  /** The host's certbot; false is the dashboard's own. */
  onHost: boolean
  /** The authenticators it lists: the methods it can prove control with. */
  plugins: string[]
  /** Why it could not list them. */
  pluginsError?: string
  /** The host's certbot is the snap, whose plugins no package manager installs. */
  snap?: boolean
}

export type CertbotInstall = {
  package?: string
  reason?: string
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
  /** The common name of each certificate as saved, leaf first, root left out. */
  chain: string[]
  /** Enabled sites serving a replaced import; each keeps the old pair until nginx reloads. */
  usedBy?: string[]
}

/** What an import would do, from POST /certificates/import/inspect. Nothing is written. */
export type ImportInspection = ImportResult & {
  suggestedName: string
  /** The import this one would replace, when the name is taken. */
  existing?: Certificate
  usedBy: string[]
  /** Where the certificate says its missing issuer is; fetched only on consent. */
  issuerURL?: string
}

/** A certificate covering every domain of a site, with the key that goes with it. */
export type SiteCertificate = {
  name: string
  path: string
  keyPath: string
  domains: string[]
  issuer: string
  daysLeft: number
  source: string
}

/** GET /certificates/covering: what the site form can switch onto, and how to issue one. */
export type SiteCertificates = {
  /** The contact email the last issuance used. */
  email: string
  certificates: SiteCertificate[]
  /** The folder a site saved with managedAcme serves its HTTP challenge from. */
  webRoot: string
}

/** One certificate, decoded from a file or a paste. */
export type CertificateFacts = {
  subject: string
  issuer: string
  dnsNames: string[]
  ipAddresses: string[]
  emails: string[]
  uris: string[]
  serial: string
  sha256: string
  sha1: string
  notBefore: string
  notAfter: string
  keyType: string
  keyBits: number
  signature: string
  ca: boolean
  selfSigned: boolean
  ocsp: string[]
  issuerUrls: string[]
  crl: string[]
  /** Signed certificate timestamps embedded in it. */
  scts: number
  /** It signs the certificate before it in the file; unset on the first. */
  signsPrevious?: boolean
}

export type ChainVerdict =
  "complete" | "wrong-order" | "missing-intermediate" | "private-ca" | "self-signed" | "invalid"

/** The certificates a file carries, in the order nginx sends them. */
export type CertificateChain = {
  verdict: ChainVerdict
  note: string
  certificates: CertificateFacts[]
}

/** What is known of the key beside a certificate: never the key itself. */
export type CertificateKey = {
  path: string
  from: string
  mode?: string
  owner?: string
  group?: string
  groupReadable?: boolean
  worldReadable?: boolean
  /** Null when the key could not be read or parsed; error says why. */
  matches: boolean | null
  error?: string
}

export type CertificateDetail = {
  path: string
  chain: CertificateChain
  key: CertificateKey | null
}

export type CertificateEvent = {
  time: string
  kind: "version" | "audit" | "failure"
  title: string
  detail?: string
  serial?: string
  sha256?: string
}

export type CertificateHistory = {
  events: CertificateEvent[]
  /** Why renewal failures are missing, when they could not be read. */
  note: string
}

export type DecodedCSR = {
  subject: string
  dnsNames: string[]
  ipAddresses: string[]
  emails: string[]
  keyType: string
  keyBits: number
  signature: string
  signatureValid: boolean
}

export type DecodedPEM = {
  chain?: CertificateChain
  requests: DecodedCSR[]
  /** Blocks left unread: a private key is never decoded. */
  refused: string[]
}

/** The ACME account a real issuance orders under, as certbot's show_account tells it. */
export type ACMEAccount = {
  /** The directory the account is with. */
  server: string
  exists: boolean
  /** The account's first contact; absent when it has none. */
  email?: string
  url?: string
  thumbprint?: string
  /** show_account failing for an account on disk: the authority unreachable, or the account gone. */
  error?: string
}

/** Whom an issuance orders from. */
export type IssueAuthority = {
  name: string
  /** The directory the run talks to: Let's Encrypt's staging one for its test runs. */
  server: string
  letsEncrypt: boolean
  /** The directory signs only test certificates. */
  staging: boolean
  eabRequired: boolean
  /** certbot has an account there: no email and no EAB are needed. */
  account: boolean
}

/** A certificate authority the issue form offers by name. */
export type ACMEAuthorityOption = {
  key: string
  name: string
  directory: string
  /** It registers no account without External Account Binding. */
  eabRequired: boolean
  /** An EAB is saved, sealed, for its directory; the key itself is never sent. */
  eabSaved: boolean
  account: boolean
}

/** One account certbot keeps under /etc/letsencrypt/accounts. */
export type ACMEAccountEntry = {
  id: string
  server: string
  authority: string
  staging: boolean
  url?: string
  email?: string
  /** The authority answered: an empty email is then an account with no contact. */
  contacted: boolean
  created?: string
  /** Why the contact could not be asked. */
  error?: string
}

export type ACMEAccounts = {
  accounts: ACMEAccountEntry[]
  authorities: ACMEAuthorityOption[]
  /** Whom an issuance orders from when the form names nobody. */
  default: IssueAuthority
  error?: string
}

/** The command an issuance would run, and what it would replace. */
export type IssuePreview = {
  /** certbot's argv, "certbot" first. DNS tokens and EAB keys appear only as their file's path. */
  args: string[]
  replacesTestCertificate: boolean
  /** The lineage whose key is replaced: a forced renewal, which counts as a duplicate. */
  replacesKeyOf?: string
  authority: IssueAuthority
}

/** One preflight finding; "ok" is a check that passed. */
export type PreflightCheck = {
  id: string
  /** The name it is about, when it is about one. */
  name?: string
  check: "dns" | "caa" | "http" | "lineage" | "rate"
  level: "ok" | "notice" | "warning" | "critical"
  title: string
  detail: string
  advice?: string
}

export type RateCount = {
  used: number
  limit: number
  /** When the limit is reached: the moment a slot comes back. */
  frees?: string
}

export type CertificateRateLimits = {
  /** False when certbot orders from an authority these limits are not. */
  applies: boolean
  /** Certificates for exactly this set of names in seven days. */
  duplicates: RateCount
  /** New certificates per registered domain in seven days, from this host. */
  registered: (RateCount & { domain: string })[]
  /** Failed real runs per name in the last hour. */
  failures: (RateCount & { domain: string })[]
  scope: string
}

/** What would fail an issuance, asked before it runs. */
export type IssuePreflight = {
  checks: PreflightCheck[]
  relation: {
    kind: "new" | "identical" | "replaces" | "expands"
    lineage?: string
    added?: string[]
    removed?: string[]
  }
  limits: CertificateRateLimits
  /** A critical check: the real run is expected to fail. */
  blocking: boolean
  checkedAt: string
}

export type CertificateFindingKind =
  "key-readable" | "key-mismatch" | "key-reused" | "weak" | "uncovered" | "stale"

export type CertificateHygieneFinding = {
  id: string
  kind: CertificateFindingKind
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  advice: string
  /** A listed file the finding is about, which opens its sheet. */
  certificate?: string
  /** A certbot lineage the finding suggests deleting. */
  lineage?: string
  /** Server names the block's certificate does not cover. */
  names?: string[]
}

export type CertificateHygiene = {
  findings: CertificateHygieneFinding[]
  /** "nginx -T", or "site files" when nginx could not print its configuration. */
  config: string
  configNote?: string
}

export type CoveredName = {
  name: string
  site: string
  file: string
  line: number
  tls: boolean
  state: "served" | "wrong" | "unknown" | "available" | "uncovered"
  certificate?: string
  certificateName?: string
}

export type UnusedCertificate = {
  path: string
  name: string
  source: string
  domains: string[]
  expired: boolean
  disabledSites?: string[]
}

export type CertificateCoverage = {
  names: CoveredName[]
  /** Null when only the site files could be read, so nothing can be called unused. */
  unused: UnusedCertificate[] | null
  config: string
  configNote?: string
}
