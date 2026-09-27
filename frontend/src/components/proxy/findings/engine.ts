import type { ProxyFinding } from "@/components/proxy/findings/shared"

/**
 * What is wrong with the engine itself rather than with something it serves.
 * The overview reads the engine's state from its identity line; nothing here
 * is folded into the list yet.
 */
export function engineFindings(): ProxyFinding[] {
  return []
}
