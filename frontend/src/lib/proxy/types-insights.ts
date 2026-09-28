import type { DeploymentRequests, RequestEntry } from "../types"

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
export type SiteTrafficReading = {
  site: string
  file: string
  status: "available" | "unavailable"
  reason?: string
  /** The hour's count, which is its rate per hour. */
  requests: number
  errorRate: number
  bytes: number
  p95?: number
  complete: boolean
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
