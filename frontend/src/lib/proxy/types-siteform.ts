import type { ProxyDiagnostic, ProxyValidation } from "./types-engine"

/** A site as the dashboard describes it, not as nginx does. */
export type SiteLocation = {
  path: string
  /** How nginx compares the path: a prefix (empty), exact, a prefix that beats regexes, or a regex. */
  match?: LocationMatch
  upstream?: string
  /** Forward /api/users as /users: the path and the upstream both end in a slash. */
  stripPrefix?: boolean
  /** A folder served at the path: /assets/app.css is <root>/app.css. */
  root?: string
  /**
   * "root" keeps nginx's own reading, read back from a file written with
   * `root`: the path is appended, so /assets/app.css is <root>/assets/app.css.
   */
  rootMode?: "root"
  /** A path under the folder with no file of its own gets the folder's index.html. */
  spa?: boolean
  webSockets: boolean
  /** In place of the site's on this path; empty keeps the site's. */
  bodyLimit?: string
  timeout?: number
  buffering?: "on" | "off"
  requestBuffering?: "on" | "off"
  /** In place of the site's on this path, as nginx reads them: the path's list is the whole list. */
  basicAuthFile?: string
  allowFrom?: string[]
  denyFrom?: string[]
  /** This path's own request rate, in place of the site's. */
  rateLimit?: RequestLimit
}

export type LocationMatch = "" | "=" | "^~" | "~" | "~*"

export type SiteSpec = {
  managedAcme?: boolean
  name: string
  domains: string[]
  kind: "proxy" | "static" | "redirect"
  upstream?: string
  /** Several servers in place of `upstream`. Proxy sites only. */
  pool?: SitePool
  root?: string
  /** A static site whose paths with no file of their own get index.html. */
  spa?: boolean
  redirectTo?: string
  permanent?: boolean
  tls: boolean
  certPath?: string
  keyPath?: string
  forceHttps: boolean
  hsts: boolean
  http2: boolean
  webSockets: boolean
  gzip: boolean
  blockExploits: boolean
  securityHeaders: boolean
  clientMaxBody?: string
  proxyTimeout?: number
  /** Let nginx hold a response until it has it; off streams it as produced. */
  buffering?: boolean
  /** Hand a request body to the application as it arrives. */
  streamUploads?: boolean
  /** The Host the application is sent: the visitor's (empty), the upstream's, or hostHeaderValue. */
  hostHeader?: "" | "upstream" | "custom"
  hostHeaderValue?: string
  /** Send the upstream's name in the TLS handshake. */
  upstreamSni?: boolean
  /** Check the upstream's certificate, against upstreamCa or the system's CAs. */
  upstreamVerify?: boolean
  upstreamCa?: string
  /** The name sent and checked in place of the upstream's host. */
  upstreamTlsName?: string
  allowFrom: string[]
  denyFrom: string[]
  basicAuthFile?: string
  basicAuthRealm?: string
  accessLog: boolean
  /**
   * Where the site's access_log and error_log write, read back from its file
   * — never set by the form. Absent when it logs nowhere of its own: off,
   * syslog, or nginx's shared log.
   */
  accessLogPath?: string
  errorLogPath?: string
  /** Timed adds the request time traffic analytics reads latency from; empty is timed. */
  logFormat?: "timed" | "combined"
  locations: SiteLocation[]
  /** The maintenance page and who gets past it; kept while off. */
  maintenance?: SiteMaintenance
  /** Codes answered with the site's own page: 404, 502, 503, 504. */
  errorPages?: ErrorPageCode[]
  /** Also replace the application's own responses with those codes. Proxy sites only. */
  interceptErrors?: boolean
  /** How much one client may ask of the site. Not for a redirect. */
  limits?: SiteLimits
  custom?: string
}

/**
 * The site's main upstream as an nginx upstream block. Health checking is
 * passive: a server is set aside after its real requests fail.
 */
export type SitePool = {
  /** Empty is round robin. */
  method?: PoolMethod
  /** How every server is spoken to; empty is http. */
  scheme?: "" | "http" | "https"
  servers: PoolServer[]
  /** Idle connections each worker keeps open to the servers; 0 keeps none. */
  keepalive?: number
  /** proxy_next_upstream's conditions; empty is nginx's error and timeout, ["off"] never retries. */
  retryOn?: RetryCondition[]
  /** Servers one request may try; 0 is no cap. */
  tries?: number
}

export type PoolMethod = "" | "least_conn" | "ip_hash" | "hash" | "random"

export type RetryCondition =
  | "error"
  | "timeout"
  | "invalid_header"
  | "http_500"
  | "http_502"
  | "http_503"
  | "http_504"
  | "http_403"
  | "http_404"
  | "http_429"
  | "non_idempotent"
  | "off"

/** One server: host:port, [IPv6]:port or unix:/path. Zero is nginx's default. */
export type PoolServer = {
  address: string
  weight?: number
  maxFails?: number
  /** Seconds. */
  failTimeout?: number
  /** Gets requests only while every other server is unavailable. */
  backup?: boolean
  /** Kept in the file and sent nothing. */
  down?: boolean
}

/** Counted per client address, as nginx sees it. */
export type SiteLimits = {
  /** The site-wide request rate; a path's own replaces it there. */
  request?: RequestLimit
  /** Connections one address may hold open at once; 0 sets no cap. */
  connPerIp?: number
  /** Addresses no limit counts. */
  exemptFrom: string[]
  /** Log what would be refused, as "dry run" in the error log, and refuse nothing. */
  dryRun?: boolean
}

export type RequestLimit = {
  /** nginx's spelling: 10r/s or 60r/m. */
  rate: string
  /** Requests past the rate that are queued rather than refused. */
  burst?: number
  /** Answer the burst at once rather than spacing it out at the rate. */
  noDelay?: boolean
  /** One allowance per address, or per address and path; empty is per address. */
  key?: "ip_path"
}

export type SiteMaintenance = {
  on: boolean
  /** Seconds the 503 asks clients to wait; 0 sends no Retry-After. */
  retryAfter?: number
  /** Addresses that reach the site as usual while it is on. */
  bypassFrom: string[]
}

export type ErrorPageCode = 404 | 502 | 503 | 504

/** A page file a site serves: its maintenance page or one of its error pages. */
export type SitePageName = "maintenance" | "404" | "502" | "503" | "504"

/** GET/PUT /proxy/sites/{name}/pages/{page}. */
export type SitePage = {
  site: string
  page: SitePageName
  content: string
  /** The site has its own file; otherwise this is the shipped default a save writes. */
  custom: boolean
}

/**
 * A statement of a site file that saving the form in its place does not
 * write back: a line added by hand the form has no field for.
 */
export type DroppedLine = {
  /** Where it starts in the file, and how many lines `text` takes. */
  line: number
  lines: number
  /** Its own lines of the file, dedented, or the statement on one line where it shares one. */
  text: string
  /** Where it sits: "server", "location /api/", "server on port 80", "outside any server". */
  context: string
  /**
   * It sits directly in the server block the form writes, and the extra
   * configuration, which goes there, keeps it as it is.
   */
  movable: boolean
  /** Why a line of that server block cannot move. */
  reason?: string
}

/** What POST /proxy/sites/preview answers: the file a spec renders to, and where it goes. */
export type SitePreview = {
  content: string
  warnings: string[]
  /** The file a save writes; absent when the host has no site directory. */
  path?: string
  /** Whether a file is already there. */
  exists?: boolean
  /**
   * What holds the name's sites-enabled link when it is another file — a
   * site a new one of this name would unlink. A sentence.
   */
  enabledElsewhere?: string
  /**
   * Whether nginx reads the file: linked into sites-enabled under its own
   * name or another, or in conf.d with a name ending in .conf.
   */
  enabled?: boolean
  /**
   * sites-enabled/<name> is a file of its own, not a link: nginx serves that
   * file under the name, and a save of this one does not reach it.
   */
  servedCopy?: boolean
  /**
   * The host keeps its sites in conf.d: a file is on while its name ends in
   * .conf, and enabling one that does not is renaming it, which a save does
   * not do.
   */
  confd?: boolean
  /** The version of the file there now, when there is one. */
  digest?: string
  /**
   * What saving this spec over that file drops of what the form cannot
   * hold, less what the spec now writes itself; absent when the file is not
   * one the form can read.
   */
  dropped?: DroppedLine[]
}

/** What GET /proxy/sites/{name} answers: a site read back into the form. */
export type SiteRead = {
  spec: SiteSpec
  managed: boolean
  content: string
  warnings: string[]
  /** Whether nginx reads the site; absent when its name is not one the form saves. */
  enabled?: boolean
  /** The site is in conf.d, where it is enabled by renaming it. */
  confd?: boolean
  /** nginx serves sites-enabled/<name>, a file of its own, instead of this one. */
  servedCopy?: boolean
  /** The version of the file read, which a save sends back to be refused if it changed. */
  digest: string
  /** Whether a save of the file as read keeps every statement of it; absent when unknown. */
  lossless?: boolean
  /** What a save of the file as read drops; absent when the form cannot write it at all. */
  dropped?: DroppedLine[]
}

/** One of a site's names another server block also claims on the same address. */
export type ServerNameConflict = {
  domain: string
  /** The address as nginx names it: 0.0.0.0:80, [::]:443. */
  listen: string
  /** The listed site behind the other claim; absent for a block that is not a listed site. */
  site?: string
  /**
   * Which claim nginx answers from — it keeps the first block it reads:
   * `ignored`, the other site keeps the name; `takes`, this site took it;
   * `keeps`, this site already answered it. Absent when the order could not
   * be read.
   */
  effect?: "ignored" | "takes" | "keeps"
}

export type SiteResult = {
  name: string
  path: string
  content: string
  warnings: string[]
  validation?: ProxyValidation
  /** Names another server block also claims, saved anyway or kept. */
  conflicts?: ServerNameConflict[]
  /** nginx's test warnings placed in this site's own file. */
  testWarnings?: ProxyDiagnostic[]
  enabled: boolean
  /**
   * A site saved disabled, tested with its link in place for the test only:
   * `validation`, `conflicts` and `testWarnings` are what enabling it would
   * meet, and none of them refused the save.
   */
  testedAsEnabled?: boolean
  /**
   * nginx serves sites-enabled/<name>, a file of its own, and not this one:
   * the save changed nothing it serves, and was not tested.
   */
  servedCopy?: boolean
  /** Where the hand-written file the save replaced was kept. */
  backup?: string
  reloaded: boolean
  /** Why nginx did not reload a configuration that tested clean. */
  reloadError?: string
  output?: string
}
