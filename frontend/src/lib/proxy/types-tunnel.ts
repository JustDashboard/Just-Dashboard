/**
 * Cloudflare Tunnel as an ingress (`backend/internal/cftunnel`): the tunnel
 * this host runs a connector for, the connector's health as cloudflared
 * reports it, and the deployment domains routed through it.
 */

/** The tunnel the dashboard runs a connector for. Its token is never sent back. */
export type TunnelConnection = {
  /** "token" is a token pasted from Cloudflare's dashboard; "api" a tunnel created here. */
  mode: "token" | "api"
  tunnelId: string
  tunnelName?: string
  accountId: string
  createdBy?: string
  createdAt: string
  updatedAt: string
}

/** The `just-dashboard-cloudflared` container, as Docker reports it. */
export type TunnelConnector = {
  /** running, restarting, exited, created, or missing. */
  state: string
  id?: string
  image?: string
  /** The pinned cloudflared release the dashboard runs. */
  version: string
  startedAt?: string
  /** Its address on the tunnel's private network; it publishes no port. */
  address?: string
  restartCount?: number
  exitCode?: number
  /** A container of the name the dashboard did not start, which it leaves alone. */
  foreign?: boolean
  /** The proxy it points at. */
  origin?: string
}

/** Where the connector sends what it receives: this host's proxy, over HTTP. */
export type TunnelOrigin = {
  service: string
  network: string
  hostGateway?: boolean
  /** The proxy in words: "the Caddy ingress", "this host's nginx". */
  proxy: string
}

export type TunnelIngressRule = {
  /** Absent on the catch-all, which answers 404 to any other name. */
  hostname?: string
  service: string
  originRequest?: { noTLSVerify?: boolean; originServerName?: string }
}

/** One deployment domain marked to be served through the tunnel, and its record. */
export type TunnelHostname = {
  hostname: string
  projectId: number
  environmentId: number
  project: string
  /** The tunnel's routes at Cloudflare hold it. */
  routed: boolean
  /** created, present, updated, error, or manual when no credentials are saved to make it. */
  dns: "created" | "present" | "updated" | "error" | "manual" | ""
  error?: string
  checkedAt?: string
}

/** GET /proxy/tunnel, and the answer to every verb on it. */
export type TunnelStatus = {
  connected: boolean
  connection?: TunnelConnection
  connector: TunnelConnector
  /** cloudflared holds at least one connection to Cloudflare's edge. */
  ready: boolean
  readyConnections: number
  checkedAt?: string
  /** Why it is not ready, in cloudflared's own words when it said so. */
  error?: string
  /** Whether saved Cloudflare credentials let the dashboard set routes and records itself. */
  api: { available: boolean; reason?: string }
  origin?: TunnelOrigin
  hostnames: TunnelHostname[]
  /** The ingress rules last set at Cloudflare, or to set there by hand. */
  routes?: TunnelIngressRule[]
  syncedAt?: string
  syncError?: string
}
