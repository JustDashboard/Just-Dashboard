import type { ProxyDiagnostic, ProxyValidation } from "./types-engine"

/** A site as the dashboard describes it, not as nginx does. */
export type SiteLocation = {
  path: string
  upstream?: string
  /** A folder served at the path: /assets/app.css is <root>/app.css. */
  root?: string
  /**
   * "root" keeps nginx's own reading, read back from a file written with
   * `root`: the path is appended, so /assets/app.css is <root>/assets/app.css.
   */
  rootMode?: "root"
  webSockets: boolean
}

export type SiteSpec = {
  managedAcme?: boolean
  name: string
  domains: string[]
  kind: "proxy" | "static" | "redirect"
  upstream?: string
  root?: string
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
  allowFrom: string[]
  denyFrom: string[]
  basicAuthFile?: string
  basicAuthRealm?: string
  accessLog: boolean
  locations: SiteLocation[]
  custom?: string
}

/** One of a site's names another server block already answers on the same address. */
export type ServerNameConflict = {
  domain: string
  /** The address as nginx names it: 0.0.0.0:80, [::]:443. */
  listen: string
  /** The other enabled site serving it, when one of the listed sites does. */
  site?: string
}

export type SiteResult = {
  name: string
  path: string
  content: string
  warnings: string[]
  validation?: ProxyValidation
  /** Names saved anyway although another server block answers them. */
  conflicts?: ServerNameConflict[]
  /** nginx's test warnings placed in this site's own file. */
  testWarnings?: ProxyDiagnostic[]
  enabled: boolean
  reloaded: boolean
  /** Why nginx did not reload a configuration that tested clean. */
  reloadError?: string
  output?: string
}
