import type { ProxyValidation } from "./types-engine"

/** A site as the dashboard describes it, not as nginx does. */
export type SiteLocation = {
  path: string
  upstream?: string
  root?: string
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
  /**
   * Where the site's access_log and error_log write, read back from its file
   * — never set by the form. Absent when it logs nowhere of its own: off,
   * syslog, or nginx's shared log.
   */
  accessLogPath?: string
  errorLogPath?: string
  locations: SiteLocation[]
  custom?: string
}

export type SiteResult = {
  name: string
  path: string
  content: string
  warnings: string[]
  validation?: ProxyValidation
  enabled: boolean
  reloaded: boolean
  output?: string
}
