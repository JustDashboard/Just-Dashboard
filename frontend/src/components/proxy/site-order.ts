import type { VHost } from "@/lib/types"

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
export function waiting(v: VHost) {
  return isBroken(v) || isPlain(v) || isDisabled(v)
}

/**
 * Worst first, then by name. The order *is* the page's answer to "which of
 * these needs me": a link to nothing stops every reload, so it leads.
 */
export function byUrgency(a: VHost, b: VHost): number {
  const rank = (v: VHost) =>
    v.broken === "dangling" ? 0 : isBroken(v) ? 1 : isPlain(v) ? 2 : isDisabled(v) ? 3 : 4
  if (rank(a) !== rank(b)) return rank(a) - rank(b)
  return a.name.localeCompare(b.name)
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
