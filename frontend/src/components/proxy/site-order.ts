import type { VHost } from "@/lib/types"

/** A site with something to act on: proxying an application in plain text, or on disk and not serving. */
export function isPlain(v: VHost) {
  return v.enabled && !v.tls && v.upstreams.length > 0
}
export function isDisabled(v: VHost) {
  return v.kind === "nginx" && !v.enabled && Boolean(v.enabledPath)
}
export function waiting(v: VHost) {
  return isPlain(v) || isDisabled(v)
}

/** Worst first, then by name. The order *is* the page's answer to "which of these needs me". */
export function byUrgency(a: VHost, b: VHost): number {
  const rank = (v: VHost) => (isPlain(v) ? 0 : isDisabled(v) ? 1 : 2)
  if (rank(a) !== rank(b)) return rank(a) - rank(b)
  return a.name.localeCompare(b.name)
}
