import type { VHost } from "@/lib/types"
import { byUrgency } from "@/components/proxy/site-order"
import { scanDomain } from "@/components/proxy/site-verbs"

/**
 * The overview's Routes: which sites it draws, in what order, as what, and
 * what pressing one does for the account looking at it.
 */

/** How many routes the overview draws; the Sites page lists every one. */
export const ROUTE_LIMIT = 8

/**
 * The routes worth the overview's space, worst first as the Sites page orders
 * them. It drew the first eight alphabetically and never said there were
 * more, so a plain-HTTP site named "zz" was never on it.
 */
export function overviewRoutes(hosts: VHost[]): { shown: VHost[]; total: number } {
  return { shown: [...hosts].sort(byUrgency).slice(0, ROUTE_LIMIT), total: hosts.length }
}

/** What serves a route, in the words the Sites cards use. */
export function routeKind(vhost: VHost): string {
  if (vhost.kind === "nginx") return "nginx site"
  return vhost.path ? "Caddyfile" : "Docker Caddy ingress"
}

/**
 * What pressing a route does.
 *
 * An administrator goes to the site on the Sites page, where its form, its
 * file and its verbs are. Anyone else reads the file where it is: the Sites
 * link opened the site form for them, whose preview and save they are not
 * allowed. A Docker Caddy route has no file on the host — its Caddyfile is the
 * container's own — so the one reading there is of it is the live TLS report,
 * which only an administrator may run; for anyone else, and for a route with
 * no domain to scan, there is nothing to open.
 */
export type RouteTarget =
  | { open: "page"; href: string; verb: string }
  | { open: "file"; verb: string }
  | { open: "none"; verb: string }

export function routeTarget(vhost: VHost, admin: boolean): RouteTarget {
  if (vhost.path) {
    return admin
      ? {
          open: "page",
          href: `/proxy/sites?site=${encodeURIComponent(vhost.name)}`,
          verb: `Open ${vhost.name}`,
        }
      : { open: "file", verb: `View ${vhost.name}` }
  }
  const domain = admin ? scanDomain(vhost) : undefined
  if (domain) {
    return {
      open: "page",
      href: `/proxy/tls?domain=${encodeURIComponent(domain)}`,
      verb: `TLS report for ${domain}`,
    }
  }
  return { open: "none", verb: vhost.name }
}
