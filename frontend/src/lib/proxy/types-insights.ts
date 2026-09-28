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
