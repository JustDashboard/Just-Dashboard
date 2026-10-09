import { prefixRelation } from "@/components/network/tools/subnet-math"
import type { BlockEngine, BlockedEntry, BlocksView } from "@/lib/types"

/** An address as the single-host prefix it blocks; a range as written. */
export function asPrefix(value: string): string {
  const trimmed = value.trim()
  if (trimmed.includes("/")) return trimmed
  return `${trimmed}/${trimmed.includes(":") ? 128 : 32}`
}

/**
 * What already holds an address or a range someone is about to ban: the
 * entry for exactly it, and the broader blocks it sits inside. Input that is
 * not an address yet (half-typed) holds nothing.
 */
export function heldBy(
  value: string,
  view: BlocksView | undefined,
): { same?: BlockedEntry; inside: BlockedEntry[] } {
  const found: { same?: BlockedEntry; inside: BlockedEntry[] } = { inside: [] }
  if (!view || !value.trim()) return found
  const typed = asPrefix(value)
  for (const entry of view.entries) {
    let relation: string
    try {
      relation = prefixRelation(asPrefix(entry.value), typed).relation
    } catch {
      return { inside: [] }
    }
    if (relation === "same") found.same = entry
    else if (relation === "contains") found.inside.push(entry)
  }
  return found
}

const ENGINE_WORDS: Record<BlockEngine, string> = {
  fail2ban: "fail2ban",
  crowdsec: "CrowdSec",
  firewall: "the firewall",
}

/** One source as a short phrase: "fail2ban sshd", "CrowdSec #11", "firewall rule 3". */
export function sourceLabel(source: BlockedEntry["sources"][number]): string {
  switch (source.engine) {
    case "fail2ban":
      return `fail2ban ${source.ref}`
    case "crowdsec":
      return `CrowdSec #${source.ref}${source.community ? " (community)" : ""}`
    case "firewall":
      return `firewall rule ${source.ref}`
  }
}

/**
 * The sentence a ban dialog shows under its field: who already refuses this
 * address, so a second ban can be seen to add nothing — or nothing at all
 * when the address is new.
 */
export function heldSentence(value: string, view: BlocksView | undefined): string | undefined {
  const { same, inside } = heldBy(value, view)
  const parts: string[] = []
  if (same) parts.push(`already held by ${same.sources.map(sourceLabel).join(", ")}`)
  if (inside.length > 0)
    parts.push(
      `inside ${inside.map((entry) => `${entry.value} (${entry.engines.map((e) => ENGINE_WORDS[e]).join(", ")})`).join(", ")}`,
    )
  if (parts.length === 0) return undefined
  const sentence = parts.join(", and ")
  return `${value.trim()} is ${sentence}.`
}
