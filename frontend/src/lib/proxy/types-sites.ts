import type { ProxyValidation } from "./types-engine"

export type VHost = {
  name: string
  kind: "nginx" | "caddy"
  path: string
  enabledPath?: string
  enabled: boolean
  /** Where an nginx site was found; "sites-enabled" is a file or link that is only there. */
  layout?: "sites-available" | "conf.d" | "sites-enabled"
  /**
   * The site's name in sites-enabled does not serve its file: "dangling" is
   * a link to nothing, which makes nginx refuse every reload; "stale" is a
   * link to, or a copy of, some other file.
   */
  broken?: "dangling" | "stale"
  /** Where the link in sites-enabled points, for a broken site and a link-only one. */
  linkTarget?: string
  /**
   * A stale link's target is also read through another name in sites-enabled
   * or through conf.d. Otherwise Enable, which points the link at this file,
   * takes that target out of nginx.
   */
  targetServedElsewhere?: boolean
  /** Other names in sites-enabled that link to this file, each serving it; Disable takes them out. */
  linkedAs?: string[]
  /** Where the file really is, when that is outside the proxy's directories: the editor does not open it. */
  resolvesTo?: string
  /** The site form reads this file and saves it back to the same place. */
  formEditable: boolean
  serverNames: string[]
  listen: string[]
  upstreams: string[]
  tls: boolean
  certPath?: string
  /** Where an nginx site writes its requests and errors, as its own page reads them. */
  accessLogPath?: string
  errorLogPath?: string
  certPaths?: string[]
  /** The file the site's requests are logged to, its own or nginx.conf's; absent where it logs nowhere openable. */
  accessLog?: string
  errorLog?: string
  /** The upstream blocks the file declares. */
  pools?: SitePool[]
  features?: SiteFeature[]
  /** The directories the site serves files from. */
  roots?: string[]
  /** Where the site's return directives send visitors. */
  redirects?: string[]
  /** The package that installed this file, which it still matches byte for byte: the stock default site. */
  package?: string
  /** The deployment environment that writes this route. */
  owner?: VHostOwner
  modified: string
  size: number
  /** The site answers with its maintenance page now. */
  maintenance?: boolean
}

/** An upstream block and the servers in it. */
export type SitePool = { name: string; servers: string[] }

/**
 * What a site's server blocks do besides naming and listening: a password,
 * sign-in through another server, an address list, a rate limit, a cache,
 * WebSockets, HTTP/2, HTTP/3, and maintenance.
 */
export type SiteFeature =
  "auth" | "sso" | "allow" | "ratelimit" | "cache" | "ws" | "h2" | "h3" | "maintenance"

/** A deployment environment that writes a route; `archived` is one nothing deploys any more. */
export type VHostOwner = {
  projectId: number
  environmentId: number
  project: string
  environment: string
  archived?: boolean
}

/** An htpasswd file and who is in it. */
export type AuthFile = {
  name: string
  path: string
  users: string[]
}

/** nginx's test and reload, as POST /proxy/reload and the site verbs report them. */
export type ProxyReload = {
  validation: ProxyValidation
  reloaded: boolean
  output: string
}

/**
 * What a change to a link in sites-enabled did — the enable switch and the
 * removal of a link no site owns. The change passed `nginx -t` or it was
 * undone and refused with a 422; `reloadError` says why nginx is not running
 * it yet.
 */
export type VHostLinkResult = {
  name: string
  enabled: boolean
  reloaded: boolean
  reloadError?: string
  reload?: ProxyReload
}

/** DELETE /proxy/sites/{name}: the file is gone; the reload may not have happened. */
export type SiteDeleteResult = {
  name: string
  reload?: ProxyReload | null
  reloadError?: string
}

/**
 * GET /proxy/pending: what on disk the running nginx has not loaded. nginx
 * replaces its workers on every load and keeps them through a reload it
 * refuses, so `lastReload` is its oldest worker's start and `generation`
 * names that load, for `?after=` to wait for a newer one after a reload.
 * `running` is false, with `reason`, where no running nginx reads this
 * configuration, and then nothing is compared.
 */
export type ProxyPending = {
  running: boolean
  reason?: string
  lastReload?: string
  generation?: string
  /** nginx's first error in the configuration on disk: every reload is refused until it is fixed. */
  problem?: string
  files: PendingFile[]
}

/**
 * One change nginx has not loaded, by the path nginx reads it through — a
 * site's link in sites-enabled — with the Sites entry it belongs to, if any.
 * "changed" is an edit, "added" a link put into sites-enabled, "removed" a
 * file nginx loaded and no longer reads, which it serves until it reloads.
 */
export type PendingFile = {
  path: string
  site?: string
  layout?: VHost["layout"]
  change: "changed" | "added" | "removed"
  modified?: string
}

/** What the catch-all default site does with a request whose Host names no site. */
export type DefaultChoice = "close" | "not_found" | "redirect" | "page"

/** Who answers an unknown Host on one socket today: its default_server, or else the first server nginx read. */
export type DefaultListener = {
  listen: string
  file: string
  line: number
  serverNames: string[]
  claimed: boolean
  ours: boolean
}

/** Another file's default_server on a socket the catch-all would claim. */
export type DefaultClaim = { listen: string; file: string; line: number }

/** GET /proxy/default-site: the catch-all as it stands and what an Apply would write. */
export type DefaultSite = {
  installed: boolean
  path?: string
  content?: string
  /** Absent when the owned file was changed by hand into something the page cannot read back. */
  choice?: DefaultChoice
  redirectTo?: string
  pageDir: string
  /** The sockets an Apply would claim, e.g. "*:80", "[::]:443". */
  covers: string[]
  answering: DefaultListener[]
  others: DefaultClaim[]
  /** Sockets on a named address, which nginx matches before the catch-all's wildcard. */
  uncovered: string[]
  tlsSkipped?: string
  /** Why nginx's configuration could not be read; the plan fields are then empty. */
  error?: string
}

/** PUT and DELETE /proxy/default-site. */
export type DefaultSiteResult = VHostLinkResult & { path: string; content?: string }

/**
 * POST /proxy/sites/bulk: one nginx -t over every site's change and one
 * reload; a refusal changes nothing and comes back as a 422 or a 400.
 * `unchanged` are sites already as asked.
 */
export type SitesBulkResult = {
  action: "enable" | "disable" | "delete"
  changed: string[]
  unchanged: string[]
  reloaded: boolean
  reloadError?: string
  reload?: ProxyReload
}

/**
 * One route's upstream as GET /proxy/upstreams reports it. The endpoint is
 * the engine lane's; the list reads only the fields it draws, and draws
 * nothing where the endpoint does not answer.
 */
export type SiteUpstreamHealth = {
  site: string
  upstream: string
  address?: string
  state: "up" | "refused" | "timeout" | "unresolvable" | "dynamic"
  ms?: number
}

export type SiteUpstreams = { checkedAt: string; targets: SiteUpstreamHealth[] }

/** A site's last hour from GET /proxy/traffic, the engine lane's summary. */
export type SiteTraffic = { site: string; requests: number }

export type SitesTraffic = { sites: SiteTraffic[] }
