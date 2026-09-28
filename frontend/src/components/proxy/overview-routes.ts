import type { VHost } from "@/lib/types"
import { byUrgency } from "@/components/proxy/site-order"
import { sitePath } from "@/components/proxy/site-verbs"

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
 * What pressing a route does: it opens the site's own page, for every route
 * and every reader. The page carries what each account may do there — the
 * form and the file for an administrator, the file read-only for anyone
 * else, and for a Docker Caddy route, which has no file on the host, its
 * requests and its TLS report.
 */
export function routeTarget(vhost: VHost): { href: string; verb: string } {
  return { href: sitePath(vhost.name), verb: `Open ${vhost.name}` }
}
