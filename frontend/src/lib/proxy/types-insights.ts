/*
 * The readings derived from the engine as a whole rather than from one site,
 * certificate or stream.
 */

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
