import type { ProxyFinding } from "@/components/proxy/findings/shared"

/**
 * Conditions read from what the proxy has been doing — its traffic, its
 * upstreams, its history — rather than from its files. Nothing here is folded
 * into the list yet.
 */
export function insightFindings(): ProxyFinding[] {
  return []
}
