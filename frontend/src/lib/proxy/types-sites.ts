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
  certPaths?: string[]
  modified: string
  size: number
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
