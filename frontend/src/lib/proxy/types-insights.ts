/*
 * The readings derived from the engine as a whole rather than from one site,
 * certificate or stream.
 */
import type { Certificate } from "./types-certs"

/**
 * How the certificate a TLS server block hands out compares with the file it
 * names: `stale` is another certificate for the same name, a renewal nginx
 * has not reloaded; `mismatch` is one that is not this block's, the name
 * falling through to another server block.
 */
export type DriftState = "ok" | "stale" | "mismatch" | "unreachable" | "skipped"

/** One TLS server block of an enabled nginx site, as GET /proxy/tls/drift reports it. */
export type DriftSite = {
  site: string
  path: string
  line: number
  serverName?: string
  address?: string
  certPath?: string
  state: DriftState
  reason?: string
  served?: Certificate
  disk?: Certificate
  /** The site whose own file holds the certificate that was served, when it is another. */
  servedBy?: string
}

export type DriftReport = { checkedAt: string; sites: DriftSite[] }

/** What connecting to one upstream address found. */
export type UpstreamState =
  "up" | "refused" | "timeout" | "unresolvable" | "missing" | "error" | "dynamic"

/**
 * One destination nginx forwards to, as GET /proxy/upstreams reports it: a
 * pass directive resolved through its upstream block, one entry per server.
 */
export type UpstreamTarget = {
  /** The server block's first server_name, a stream's first listen, or its file name. */
  site: string
  kind: "http" | "stream"
  location?: string
  directive: string
  /** The upstream block the address came from, if any. */
  upstream?: string
  /** host:port, unix:/path, or a dynamic target's argument as written. */
  address: string
  /** The file behind the site, as the sites list names it by path. */
  file: string
  line: number
  state: UpstreamState
  ms?: number
  /** The answer to HEAD / of an http or https upstream. */
  status?: number
  /** The process listening on a local TCP port. */
  owner?: string
  detail?: string
}

export type UpstreamReport = { checkedAt: string; targets: UpstreamTarget[] }
