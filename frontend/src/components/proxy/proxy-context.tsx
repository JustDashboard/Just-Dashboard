"use client"

import { createContext, useContext } from "react"
import type { Certificate, Listener, StreamStatus, VHost } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import type { Reading } from "@/components/proxy/freshness"
import type { ConfigTest } from "@/components/proxy/test-result"

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
  /** That container's ID, which Restart container and Container logs act on. */
  ingressId?: string
  /** When that container last started, as Docker reports it (RFC 3339). */
  ingressStartedAt?: string
  /**
   * Whether that ingress exists: `running`, or `provisionable` when none does
   * and the first deployment that routes a domain would start one — which is
   * neither Caddy nor a container yet, and names none.
   */
  ingressState?: "running" | "provisionable"
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
  /** When `status` was read, in epoch milliseconds, so a page can say how old it is. */
  updatedAt: number | undefined
  /**
   * Why the last read of the status failed; a page with no status says so
   * rather than rendering nothing. The status keeps its last answer beside
   * it, which a page must not present as current.
   */
  error: Error | undefined
  loading: boolean
  hasNginx: boolean
  refresh: () => void
  /**
   * The lists more than one page shows, polled once here: the overview's
   * figures, each page's own table and the rail's marks read the same
   * answer, and moving between pages does not ask for it again.
   */
  reads: ProxyReads
  /** The engine's config test, which `t` runs from any page of the section. */
  configTest: ConfigTest
}

export type ProxyReads = {
  vhosts: PollState<Reading<VHost[]>>
  certs: PollState<Reading<Certificate[]>>
  streams: PollState<Reading<StreamStatus>>
  ports: PollState<Reading<Listener[]>>
}

const ProxyContext = createContext<ProxyContextValue | null>(null)

export const ProxyProvider = ProxyContext.Provider

export function useProxy() {
  const value = useContext(ProxyContext)
  if (!value) throw new Error("useProxy must be used inside the proxy layout")
  return value
}

/**
 * One of the layout's shared reads as a page's own poll would have answered
 * it: the value without the time it was read.
 */
export function useProxyRead<K extends keyof ProxyReads>(
  key: K,
): PollState<NonNullable<ProxyReads[K]["data"]>["value"]> {
  const poll = useProxy().reads[key]
  return { data: poll.data?.value, error: poll.error, loading: poll.loading, refresh: poll.refresh }
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

/**
 * Which engine the config test and reload act on — the one the overview
 * draws. A Caddy that is the shared Docker ingress is tested and reloaded
 * inside its container; the host usually has no caddy of its own, and its
 * Caddyfile is not what the container serves.
 */
export function engineKind(status: ProxyStatus): "nginx" | "caddy" | "caddy-ingress" {
  if (status.nginx) return "nginx"
  return status.ingressContainer ? "caddy-ingress" : "caddy"
}
