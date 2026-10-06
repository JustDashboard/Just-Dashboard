import type { FirewallRule, FirewallStatus } from "@/lib/types"

/**
 * What a firewall's rules say about inbound traffic, read the way the backend
 * reads them for the ports page (`netsec/reach.go`): the three backends spell
 * an action and a direction three ways, ufw lists every rule twice on a
 * dual-stack host, and "Anywhere" has five spellings. The pictures on the
 * Security pages draw from these rather than from the raw rows.
 */

const ADMITS = new Set(["ALLOW", "LIMIT", "ACCEPT"])
const REFUSES = new Set(["DENY", "REJECT", "DROP"])

/** The source every address matches, in each backend's spelling. */
export function isAnywhere(from: string | undefined) {
  switch ((from ?? "").trim().toLowerCase()) {
    case "":
    case "anywhere":
    case "anywhere (v6)":
    case "0.0.0.0/0":
    case "::/0":
    case "any":
      return true
  }
  return false
}

/** A rule about traffic arriving: ufw's IN, iptables' INPUT chain, firewalld's every rule. */
export function isInbound(rule: FirewallRule) {
  const direction = (rule.direction ?? "").toUpperCase()
  return direction === "" || direction === "IN" || direction === "INPUT"
}

export function admits(rule: FirewallRule) {
  return ADMITS.has(rule.action.toUpperCase())
}

export function refuses(rule: FirewallRule) {
  return REFUSES.has(rule.action.toUpperCase())
}

/** Whether the default for a connection no rule matched turns it away. */
export function refusesByDefault(policy: string | undefined) {
  const p = (policy ?? "").toLowerCase()
  return p.startsWith("deny") || p.startsWith("reject") || p.startsWith("drop")
}

/** One place inbound traffic is let through to, and who may reach it. */
export type Opening = {
  /** The port or the profile, as the rule names it — "22", "443", "OpenSSH". */
  key: string
  port?: string
  protocol?: string
  /** The service the rule names, or ufw's profile. */
  service?: string
  /** A rule admits every address to it. */
  anyone: boolean
  /** The sources admitted by name, when nobody else is. */
  sources: string[]
  /** Why opening this to everyone is a mistake, from the backend's own list. */
  danger?: string
  rules: number[]
}

/**
 * The places inbound traffic is admitted to, one per port, each with the
 * sources the rules admit — so three rules opening 22 to three networks read
 * as one opening restricted to three networks, and a rule for everyone makes
 * it everyone's whatever else names it (a source admitted first and everyone
 * after it is everyone, as the backend's reach reading says).
 */
export function openings(status: FirewallStatus | undefined): Opening[] {
  const byKey = new Map<string, Opening>()
  for (const rule of status?.rules ?? []) {
    if (rule.ipv6 || !isInbound(rule) || !admits(rule)) continue
    const key = rule.port || rule.service || rule.to || "any"
    const opening = byKey.get(key) ?? {
      key,
      port: rule.port,
      protocol: rule.protocol,
      service: rule.service,
      anyone: false,
      sources: [],
      rules: [],
    }
    if (rule.number !== undefined) opening.rules.push(rule.number)
    if (isAnywhere(rule.from)) {
      opening.anyone = true
      opening.danger ??= rule.danger
    } else if (!opening.sources.includes(rule.from)) {
      opening.sources.push(rule.from)
    }
    byKey.set(key, opening)
  }
  return [...byKey.values()]
}

/** The addresses a rule turns away by name: a blocklist, beside the fence. */
export function deniedSources(status: FirewallStatus | undefined) {
  return [
    ...new Set(
      (status?.rules ?? [])
        .filter((r) => !r.ipv6 && isInbound(r) && refuses(r) && !isAnywhere(r.from))
        .map((r) => r.from),
    ),
  ]
}
