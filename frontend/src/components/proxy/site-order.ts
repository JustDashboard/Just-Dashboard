import type { VHost } from "@/lib/types"
import { isLeftover, siteTitle } from "@/components/proxy/site-details"

/** Its name in sites-enabled does not serve its file: a link to nothing, or to something else. */
export function isBroken(v: VHost) {
  return Boolean(v.broken)
}
/** A site with something to act on: proxying an application in plain text, or on disk and not serving. */
export function isPlain(v: VHost) {
  return v.enabled && !v.tls && v.upstreams.length > 0
}
export function isDisabled(v: VHost) {
  return v.kind === "nginx" && !v.enabled && Boolean(v.enabledPath) && !v.broken
}
/**
 * Disabled, and something to decide about. The distribution's own default
 * site, still as its package installed it, is disabled on most hosts on
 * purpose: nobody made it and nobody has to act on it.
 */
export function isParked(v: VHost) {
  return isDisabled(v) && !v.package
}

/** By what the list calls each site, then by file where two share a domain. */
export function byTitle(a: VHost, b: VHost): number {
  return siteTitle(a).localeCompare(siteTitle(b)) || a.name.localeCompare(b.name)
}

/**
 * Worst first, then by title. The order *is* the page's answer to "which of
 * these needs me": a link to nothing stops every reload, so it leads. A
 * route its deployment left comes next: it answers a domain nobody deploys,
 * and stood among the serving ones under a green word.
 */
export function byUrgency(a: VHost, b: VHost): number {
  const rank = (v: VHost) =>
    v.broken === "dangling"
      ? 0
      : isBroken(v)
        ? 1
        : isLeftover(v)
          ? 2
          : isPlain(v)
            ? 3
            : isParked(v)
              ? 4
              : 5
  if (rank(a) !== rank(b)) return rank(a) - rank(b)
  return byTitle(a, b)
}

/**
 * The nginx names more than one entry answers to — conf.d/app.conf beside
 * sites-available/app.conf. The verbs that act by name alone cannot tell
 * those apart, so they are not offered on them.
 */
export function sharedNames(hosts: VHost[]): Set<string> {
  const seen = new Set<string>()
  const shared = new Set<string>()
  for (const v of hosts) {
    if (v.kind !== "nginx") continue
    if (seen.has(v.name)) shared.add(v.name)
    seen.add(v.name)
  }
  return shared
}
