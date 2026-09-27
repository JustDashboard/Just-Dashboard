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
  reloaded: boolean
  /** Why nginx did not reload a configuration that tested clean. */
  reloadError?: string
  output?: string
}
