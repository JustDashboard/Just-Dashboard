export type VHost = {
  name: string
  kind: "nginx" | "caddy"
  path: string
  enabledPath?: string
  enabled: boolean
  serverNames: string[]
  listen: string[]
  upstreams: string[]
  tls: boolean
  certPath?: string
  /** Where an nginx site writes its requests and errors, as its own page reads them. */
  accessLogPath?: string
  errorLogPath?: string
  modified: string
  size: number
}

/** An htpasswd file and who is in it. */
export type AuthFile = {
  name: string
  path: string
  users: string[]
}
