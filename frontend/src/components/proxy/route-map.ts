import type {
  Listener,
  SiteTrafficReading,
  UpstreamReport,
  UpstreamState,
  UpstreamTarget,
  VHost,
} from "@/lib/types"
import {
  containerProduct,
  imageProduct,
  processProduct,
  unitProduct,
} from "@/components/product-logo"
import { certPathProduct } from "@/components/proxy/marks"
import { listenerProduct } from "@/components/proxy/ports"
import { isParked } from "@/components/proxy/site-order"
import { isDown, upstreamRank, upstreamsOf } from "@/components/proxy/upstream-health"

/**
 * What the overview's route map draws: the domains this proxy answers for,
 * and the applications behind them, each as the thing it is.
 *
 * A route's far end used to be an address — `http://127.0.0.1:3001` — under
 * the engine's own logo, so a host fronting Grafana, n8n and Gitea drew nine
 * identical nginx marks. Here the address is read against the sockets this
 * host holds: a loopback port is whoever listens on it (a container as its
 * image, a program as itself), a name a container answers to is that
 * container, and a unix socket is the program its file is named for.
 * Anything none of these names keeps a glyph.
 */

/** How many domains the picture draws; the Sites page lists every one. */
export const MAP_LIMIT = 8

/** Below this many requests an hour a wire is still: a health check is not traffic. */
export const CARRIES_PER_HOUR = 60

export type BackendKind = "app" | "files" | "redirect" | "config"

export type MapApp = {
  /** The upstream's address as nginx was given it, or what stands in for one. */
  id: string
  kind: BackendKind
  product?: string
  /** What answers there: a container's name, a program's, or the address. */
  name: string
  /** host:port, a socket path or a directory. */
  address: string
  /** The container's id, for its page. */
  container?: string
  /** The worst the upstream check found of it, where it was checked. */
  state?: UpstreamState
  ms?: number
  domains: string[]
  /** The hour's requests over every domain that reaches it. */
  requests: number
}

export type MapDomain = {
  /** The site's name, which is its page's address. */
  id: string
  vhost: VHost
  /** The first server name, else the site's own name. */
  label: string
  /** The other names it answers to. */
  aliases: number
  tls: boolean
  /** Who signed its certificate, where its path says. */
  issuer?: string
  traffic?: SiteTrafficReading
  apps: string[]
  /** Every upstream it sends to refuses or is gone: visitors get 502. */
  down: boolean
}

export type RouteMap = {
  domains: MapDomain[]
  apps: MapApp[]
  /** Enabled routes the picture left out, which the Sites page lists. */
  hidden: number
  /** Every enabled route, drawn or not. */
  total: number
}

const LOOPBACK = /^(127\.\d+\.\d+\.\d+|localhost|\[?::1\]?)$/i

/** host and port from `http://127.0.0.1:3000/`, `grafana:3000`, `[::1]:9000`; nothing for a socket. */
export function upstreamAddress(upstream: string): { host: string; port?: number } | undefined {
  const bare = upstream.trim().replace(/^[a-z]+:\/\//i, "")
  if (bare.startsWith("unix:")) return undefined
  const match = /^(\[[^\]]+\]|[^/:\s]+)(?::(\d+))?/.exec(bare)
  if (!match) return undefined
  return { host: match[1], port: match[2] ? Number(match[2]) : undefined }
}

function socketPath(upstream: string): string | undefined {
  const match = /unix:([^\s:]+)/.exec(upstream)
  return match?.[1]
}

/**
 * What answers on an upstream, as the product it is and a name for it.
 * `owner` is the process the upstream check found on a local port, for a
 * host whose port list could not be read.
 */
export function backendOf(
  upstream: string,
  listeners: Listener[],
  owner?: string,
): Pick<MapApp, "product" | "name" | "address" | "container"> {
  const path = socketPath(upstream)
  if (path) {
    const file =
      path
        .split("/")
        .pop()
        ?.replace(/\.sock$/, "") ?? path
    return { product: unitProduct(file) ?? processProduct(file), name: file, address: path }
  }
  const at = upstreamAddress(upstream)
  if (!at) return { name: upstream, address: upstream }
  const address = at.port ? `${at.host}:${at.port}` : at.host
  const local = LOOPBACK.test(at.host)
  const holder = local
    ? listeners.find((l) => l.port === at.port && l.protocol.startsWith("tcp"))
    : listeners.find((l) => l.container?.name === at.host)
  if (holder?.container) {
    return {
      product: containerProduct(holder.container),
      name: holder.container.name,
      address,
      container: holder.container.id,
    }
  }
  if (holder) {
    return {
      product: listenerProduct(holder),
      name: holder.displayName || holder.process || address,
      address,
    }
  }
  if (local && owner) return { product: processProduct(owner), name: owner, address }
  // A name on Docker's network is usually the service it runs: `grafana`,
  // `n8n`. An image that names nothing is a guess, and is left out.
  if (!local && !/^[\d.[\]:]+$/.test(at.host)) {
    const named = imageProduct(at.host)
    if (named !== "docker") return { product: named, name: at.host, address }
  }
  return { name: address, address }
}

/** The address the upstream check names a target by: host:port, or a socket's path. */
function checkedAddress(upstream: string): string {
  const path = socketPath(upstream)
  if (path) return path
  const at = upstreamAddress(upstream)
  if (!at) return upstream
  return at.port ? `${at.host}:${at.port}` : at.host
}

function sameAddress(target: UpstreamTarget, address: string) {
  return target.address === address || target.address === `unix:${address}`
}

/**
 * What a site sends its visitors to, for a mark of one: its first upstream
 * as `backendOf` reads it, else the files it serves or where it redirects.
 */
export function siteBackend(
  vhost: VHost,
  listeners: Listener[],
  upstreams?: UpstreamReport,
): Pick<MapApp, "kind" | "product" | "name" | "address" | "container"> {
  const upstream = vhost.upstreams[0]
  if (upstream) {
    const address = checkedAddress(upstream)
    const targets = upstreamsOf(upstreams, vhost.path)
    const owner = (targets.find((t) => sameAddress(t, address)) ?? targets[0])?.owner
    return { kind: "app", ...backendOf(upstream, listeners, owner) }
  }
  const root = vhost.roots?.[0]
  if (root) return { kind: "files", name: root, address: root }
  const redirect = vhost.redirects?.[0]
  if (redirect) return { kind: "redirect", name: redirect, address: redirect }
  return { kind: "config", name: "Served by configuration", address: "" }
}

/**
 * The routes worth the picture's space, the applications behind them and
 * how they join. A route that is down comes first, then the busiest, so
 * the picture leads with what is failing and then with what is carrying;
 * a disabled site serves nothing and is not drawn.
 */
export function routeMap({
  vhosts,
  listeners,
  upstreams,
  traffic,
  limit = MAP_LIMIT,
}: {
  vhosts: VHost[]
  listeners: Listener[]
  upstreams?: UpstreamReport
  traffic?: SiteTrafficReading[]
  limit?: number
}): RouteMap {
  const hour = new Map(
    (traffic ?? []).filter((t) => t.status === "available").map((t) => [t.site, t]),
  )
  const live = vhosts.filter((v) => v.enabled && !isParked(v) && !v.broken)
  const read = live.map((vhost) => {
    const targets = upstreamsOf(upstreams, vhost.path)
    return {
      vhost,
      targets,
      down: targets.length > 0 && targets.every(isDown),
      requests: (vhost.kind === "nginx" ? hour.get(vhost.name)?.requests : undefined) ?? 0,
    }
  })
  read.sort(
    (a, b) =>
      Number(b.down) - Number(a.down) ||
      b.requests - a.requests ||
      a.vhost.name.localeCompare(b.vhost.name),
  )
  const shown = read.slice(0, limit)

  const apps = new Map<string, MapApp>()
  const domains: MapDomain[] = shown.map(({ vhost, targets, down, requests }) => {
    const ids: string[] = []
    const add = (id: string, app: Omit<MapApp, "id" | "domains" | "requests">) => {
      const held = apps.get(id)
      if (held) {
        if (!held.domains.includes(vhost.name)) held.domains.push(vhost.name)
        held.requests += requests
        if (app.state && (!held.state || upstreamRank(app.state) > upstreamRank(held.state))) {
          held.state = app.state
          held.ms = app.ms
        }
      } else {
        apps.set(id, { ...app, id, domains: [vhost.name], requests })
      }
      if (!ids.includes(id)) ids.push(id)
    }
    for (const upstream of vhost.upstreams) {
      // Each address is checked on its own; a site with one upstream whose
      // check names it differently (through an upstream block) takes the
      // worst of the site's. The targets come worst first.
      const address = checkedAddress(upstream)
      const mine = targets.filter((t) => sameAddress(t, address))
      const judged = mine[0] ?? (vhost.upstreams.length === 1 ? targets[0] : undefined)
      const backend = backendOf(upstream, listeners, judged?.owner)
      add(`app:${backend.address}`, {
        kind: "app",
        ...backend,
        state: judged?.state,
        ms: judged?.ms,
      })
    }
    if (vhost.upstreams.length === 0) {
      const served = siteBackend(vhost, listeners)
      add(`${served.kind}:${served.address}`, served)
    }
    const names = vhost.serverNames.filter((n) => n && n !== "_")
    return {
      id: vhost.name,
      vhost,
      label: names[0] ?? vhost.name,
      aliases: Math.max(names.length - 1, 0),
      tls: vhost.tls,
      issuer: vhost.tls ? certPathProduct(vhost.certPath) : undefined,
      traffic: vhost.kind === "nginx" ? hour.get(vhost.name) : undefined,
      apps: ids,
      down,
    }
  })

  return {
    domains,
    apps: [...apps.values()],
    hidden: read.length - shown.length,
    total: read.length,
  }
}

/**
 * How long a pulse takes to run a wire, from the hour's requests: four
 * seconds at a request a minute, a second and a half at a thousand a minute.
 * The Network section's `pulseDuration` is the same curve over bytes.
 */
export function requestPulse(requestsPerHour: number): number {
  if (requestsPerHour <= CARRIES_PER_HOUR) return 4
  const scale = Math.min(Math.log10(requestsPerHour / CARRIES_PER_HOUR) / 3, 1)
  return Number((4 - scale * 2.5).toFixed(2))
}

/**
 * The verdict at the end of the engine's line: how many routes reach what
 * they forward to. Nothing while the upstream check has not answered, since
 * a route that was never checked is not "answering".
 */
export function routeVerdict(
  vhosts: VHost[],
  upstreams: UpstreamReport | undefined,
): { tone: "ok" | "warning" | "critical"; label: string } | undefined {
  if (!upstreams) return undefined
  const checked = vhosts
    .filter((v) => v.enabled && !isParked(v))
    .map((v) => upstreamsOf(upstreams, v.path))
    .filter((targets) => targets.some((t) => t.state !== "dynamic"))
  if (checked.length === 0) return undefined
  const routes = (n: number) => `${n} ${n === 1 ? "route" : "routes"}`
  const down = checked.filter((targets) => targets.every(isDown)).length
  if (down > 0) return { tone: "critical", label: `${routes(down)} down` }
  const partly = checked.filter((targets) => targets.some(isDown)).length
  if (partly > 0) return { tone: "warning", label: `${routes(partly)} partly down` }
  return {
    tone: "ok",
    label: checked.length === 1 ? "1 route answering" : `All ${checked.length} routes answering`,
  }
}
