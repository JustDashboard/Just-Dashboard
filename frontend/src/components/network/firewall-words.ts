import type { FirewallStatus } from "@/lib/types"
import { isAnywhere, isInbound } from "@/components/network/firewall-reading"

/**
 * What the Firewall page says about a backend, a rule and a recorded change
 * in a few words or a mark, apart from React so it is tested in `bun test
 * src`. `firewall-reading.ts` reads the rules; this names what was read.
 */

/**
 * The firewall as the product it is. firewalld has a mark of its own; ufw,
 * iptables and nftables have none, and each is a front end to netfilter, the
 * kernel's packet filter, so they are drawn as the engine they drive, the way
 * the Drift page draws the nftables files. Nothing is drawn for a backend
 * this does not know.
 */
export function firewallProduct(backend: string | undefined) {
  switch (backend) {
    case "firewalld":
      return "firewalld"
    case "ufw":
    case "iptables":
    case "nftables":
      return "netfilter"
  }
  return undefined
}

/**
 * The ports a rule rate-limits, under the key `openings` folds them by: ufw's
 * LIMIT is the one action of the three backends that admits and counts.
 */
export function limitedKeys(status: Pick<FirewallStatus, "rules">) {
  const keys = new Set<string>()
  for (const rule of status.rules) {
    if (rule.ipv6 || !isInbound(rule) || rule.action.toUpperCase() !== "LIMIT") continue
    keys.add(rule.port || rule.service || rule.to || "any")
  }
  return keys
}

/** A recorded rule as the few words a line has room for: "allow 51820/udp from 10.0.0.0/8". */
export function ruleWords(rule: Record<string, unknown> | undefined) {
  if (!rule) return ""
  const word = (key: string) => (typeof rule[key] === "string" ? (rule[key] as string) : "")
  const port = word("port")
  const from = word("from")
  return [
    word("action"),
    port ? `${port}${word("protocol") ? `/${word("protocol")}` : ""}` : word("to"),
    from && !isAnywhere(from) ? `from ${from}` : "",
  ]
    .filter(Boolean)
    .join(" ")
}
