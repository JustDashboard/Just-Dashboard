"use client"

import { createContext, useContext } from "react"

export type ProxyStatus = {
  nginx: boolean
  caddy: boolean
  nginxVersion?: string
  caddyVersion?: string
  nginxDir: string
  caddyFile: string
  certbot: boolean
  /** The public Caddy container deployments share, where one owns ports 80 and 443. */
  ingressContainer?: string
}

/**
 * What reverse proxy this host runs, polled once by the layout.
 *
 * `nginx` gates the site builder — the form writes nginx config and there is
 * nowhere to put it otherwise — and both the Sites page and the Overview read
 * it. The certificate and port pages do not: certificates on disk and
 * listening ports exist whether or not a proxy is installed, which is why the
 * section is never gated as a whole.
 */
export type ProxyContextValue = {
  status: ProxyStatus | undefined
  loading: boolean
  hasNginx: boolean
  refresh: () => void
}

const ProxyContext = createContext<ProxyContextValue | null>(null)

export const ProxyProvider = ProxyContext.Provider

export function useProxy() {
  const value = useContext(ProxyContext)
  if (!value) throw new Error("useProxy must be used inside the proxy layout")
  return value
}

/**
 * The systemd unit behind the engine, where there is one. A Caddy that runs
 * as the shared Docker ingress has no unit on the host — its lifecycle is the
 * container's — so the overview offers no service controls for it.
 */
export function engineUnit(status: ProxyStatus | undefined): string | undefined {
  if (!status) return undefined
  if (status.nginx) return "nginx.service"
  if (status.caddy && !status.ingressContainer) return "caddy.service"
  return undefined
}
