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
  modified: string
  size: number
  /** The site answers with its maintenance page now. */
  maintenance?: boolean
  /** A server of the site limits requests or connections. */
  rateLimited?: boolean
  /** The site keeps its application's responses in a proxy cache the dashboard can empty. */
  cached?: boolean
}

/** An htpasswd file and who is in it. */
export type AuthFile = {
  name: string
  path: string
  users: string[]
}
