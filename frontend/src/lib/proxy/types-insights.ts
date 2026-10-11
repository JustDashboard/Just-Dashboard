import type { DeploymentRequests, RequestEntry } from "../types"

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

/**
 * One server of an upstream pool: its options as written, the check's state
 * (absent for one marked down) and what nginx's error logs said of it over
 * the window, by kind.
 */
export type PoolMember = {
  address: string
  weight?: number
  maxFails?: number
  failTimeout?: string
  backup?: boolean
  down?: boolean
  state?: UpstreamState
  ms?: number
  status?: number
  owner?: string
  detail?: string
  /** Addresses a host name resolves to from here now. */
  resolved?: string[]
  /** refused, timeout, reset, closed, disabled (set aside after max_fails), other. */
  failures?: Record<string, number>
  lastFailure?: string
}

/**
 * How nginx spreads one route's requests: `native` across an upstream
 * block's servers, `native-dns` across the addresses one name resolved to,
 * or `single`, one address whose spreading — a provider's balancer, a
 * floating address — nginx cannot see.
 */
export type UpstreamPool = {
  /** The upstream block's name; absent for a single endpoint. */
  name?: string
  kind: "http" | "stream"
  sites: string[]
  files: string[]
  method?: string
  keepalive?: number
  balancing: "native" | "native-dns" | "single"
  /** The managed balancer a single endpoint's host name suggests. */
  provider?: string
  members: PoolMember[]
  verdict: "serving" | "degraded" | "on-backup" | "down" | "unknown"
  /** Requests nginx had nowhere to send, every server set aside. */
  noLive?: number
}

/** Which error logs the pools' failures were read from, and since when. */
export type PoolEvidence = { since: string; logs: string[]; complete: boolean; note?: string }

export type UpstreamReport = {
  checkedAt: string
  targets: UpstreamTarget[]
  /** Absent from a backend that predates pools. */
  pools?: UpstreamPool[]
  evidence?: PoolEvidence
}

/**
 * Where an nginx site writes, read from its own file. A log is empty when the
 * site names none the dashboard reads, and the note beside it says why.
 */
export type SiteLogs = {
  site: string
  file: string
  serverNames: string[]
  access?: string
  accessNote?: string
  error?: string
  errorNote?: string
}

/** GET /proxy/traffic/{name}: a deployment's request window, for a site. */
export type SiteTraffic = DeploymentRequests & { logs: SiteLogs }

/** GET /proxy/traffic/{name}/tail: what arrived after the cursor, oldest first. */
export type SiteTrafficTail = { entries: RequestEntry[]; cursor: number }

/** One site's last hour, as the overview reads it. */
/** One minute of a site's hour, by what went wrong in it. */
export type TrafficPoint = {
  start: string
  total: number
  refused: number
  failed: number
  bytes: number
}

export type SiteTrafficReading = {
  site: string
  file: string
  /** What answered: nginx, or the Docker Caddy ingress holding 80 and 443. */
  engine: "nginx" | "caddy-ingress"
  status: "available" | "unavailable"
  reason?: string
  /** The hour's count, which is its rate per hour. */
  requests: number
  errorRate: number
  bytes: number
  p95?: number
  complete: boolean
  /** The hour minute by minute. */
  points: TrafficPoint[]
}

export type SiteTrafficSummary = { observedAt: string; sites: SiteTrafficReading[] }

/** One kind of failure in nginx's error log, with what to do about it where that is known. */
export type ErrorGroup = {
  pattern: string
  level: string
  count: number
  first: string
  last: string
  sample: string
  upstream?: string
  server?: string
  title?: string
  advice?: string
}

export type ErrorReport = {
  site?: string
  /** The site's own log, nginx's narrowed to the site's names, or nginx's whole. */
  scope: "site" | "shared" | "main"
  path: string
  exists: boolean
  since: string
  /** The read's byte bound cut into the window, so each count is a floor. */
  complete: boolean
  lines: number
  ungrouped?: number
  groups: ErrorGroup[]
  note?: string
}

/**
 * A finding put aside on the overview, as GET /proxy/findings/snoozes lists
 * it. `fingerprint` is the finding as it read when it was snoozed; one that
 * reads differently now is shown again.
 */
export type ProxyFindingSnooze = {
  findingId: string
  fingerprint: string
  /** Absent for a snooze that lasts until the finding changes. */
  until?: string
  note?: string
  actor: string
  createdAt: string
}
