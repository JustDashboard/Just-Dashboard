import { dnsLiteralAddress } from "@/lib/network-dns-service-policy"
import type { NativeFamily, NativeIntent } from "@/lib/network-native-profile"

/**
 * A link's split DNS, as the native profile carries it. resolved keeps one
 * domain list per link, and networkd insists both enabled families carry the
 * same one, so the draft is link-level: servers split by family on the way
 * out, the same routing and search domains on every enabled family.
 */
export type SplitDNSDraft = {
  servers: string
  /** Suffixes whose names go only to this link's servers, written without the `~`. */
  routing: string
  /** Suffixes appended to single-label names, which also route to this link. */
  search: string
  /** Use only these servers on the link, ignoring the ones DHCP or RA hand out. */
  exclusive: boolean
}

const words = (text: string) => [...new Set(text.split(/[\s,]+/).filter(Boolean))]
const enabled = (family: NativeFamily) => family.method !== "disabled"

const label = /^(?!-)[a-z0-9-]{1,63}(?<!-)$/

/** A DNS suffix: letters, digits and hyphens in labels of at most 63, 253 in all. */
export function splitDNSSuffix(value: string) {
  const name = value.toLowerCase().replace(/\.$/, "")
  return name.length > 0 && name.length <= 253 && name.split(".").every((part) => label.test(part))
}

export function splitDNSDraft(intent: NativeIntent): SplitDNSDraft {
  const families = [intent.ipv4, intent.ipv6].filter(enabled)
  const domains = families[0]?.domains ?? []
  return {
    servers: [...intent.ipv4.dns, ...intent.ipv6.dns].join("\n"),
    routing: domains
      .filter((d) => d.startsWith("~"))
      .map((d) => (d === "~." ? "." : d.slice(1)))
      .join("\n"),
    search: domains.filter((d) => !d.startsWith("~")).join("\n"),
    exclusive: families.length > 0 && families.every((f) => f.ignoreAutoDns),
  }
}

/**
 * The full native intent with only DNS changed. Every other field — addresses,
 * methods, routes — is the profile's own, so the write cannot carry anything
 * this form did not show.
 */
export function splitDNSIntent(
  intent: NativeIntent,
  draft: SplitDNSDraft,
): { intent?: NativeIntent; error?: string } {
  const v4: string[] = []
  const v6: string[] = []
  for (const server of words(draft.servers)) {
    const ip = dnsLiteralAddress(server)
    if (!ip || ip.mapped)
      return {
        error: `${server} is not an IP address; a native profile takes servers as literals.`,
      }
    ;(ip.bytes.length === 4 ? v4 : v6).push(ip.canonical)
  }
  if (v4.length > 0 && !enabled(intent.ipv4))
    return { error: "IPv4 is disabled on this profile, so it cannot carry an IPv4 DNS server." }
  if (v6.length > 0 && !enabled(intent.ipv6))
    return { error: "IPv6 is disabled on this profile, so it cannot carry an IPv6 DNS server." }
  if (v4.length > 8 || v6.length > 8)
    return { error: "A native profile takes at most eight DNS servers per family." }
  const domains: string[] = []
  for (const suffix of words(draft.routing)) {
    if (suffix === "." || suffix === "~.") {
      domains.push("~.")
      continue
    }
    const bare = suffix.replace(/^~/, "").toLowerCase().replace(/\.$/, "")
    if (!splitDNSSuffix(bare)) return { error: `${suffix} is not a DNS suffix.` }
    domains.push(`~${bare}`)
  }
  for (const suffix of words(draft.search)) {
    const bare = suffix.toLowerCase().replace(/\.$/, "")
    if (!splitDNSSuffix(bare)) return { error: `${suffix} is not a DNS suffix.` }
    domains.push(bare)
  }
  const unique = [...new Set(domains)]
  if (unique.length > 16) return { error: "A native profile takes at most sixteen domains." }
  const family = (current: NativeFamily, dns: string[]): NativeFamily =>
    enabled(current)
      ? { ...current, dns, domains: unique, ignoreAutoDns: draft.exclusive }
      : { ...current, dns: [], domains: [] }
  return { intent: { ipv4: family(intent.ipv4, v4), ipv6: family(intent.ipv6, v6) } }
}

/**
 * What resolved will do with the link once the profile carries the draft, in
 * the order an operator asks it: which names go here, and does this link still
 * answer everything else.
 */
export function splitDNSEffect(draft: SplitDNSDraft, device: string): string[] {
  const routing = words(draft.routing).map((d) => d.replace(/^~/, ""))
  const search = words(draft.search)
  const servers = words(draft.servers)
  const out: string[] = []
  const everything = routing.includes(".")
  const named = routing.filter((d) => d !== ".")
  if (servers.length === 0 && !draft.exclusive)
    out.push(`No server is set here, so ${device} keeps the servers DHCP or RA hand out.`)
  if (servers.length === 0 && draft.exclusive)
    out.push(`${device} will have no DNS server: names routed to it go unanswered.`)
  if (named.length > 0)
    out.push(
      `Names under ${named.join(", ")} go only to ${device}'s servers, never to the global upstreams.`,
    )
  if (search.length > 0)
    out.push(
      `Single-label names are tried under ${search.join(", ")}, and names under them also route to ${device}.`,
    )
  if (everything) out.push(`~. makes ${device} a default route for every name nothing else claims.`)
  else if (named.length > 0)
    out.push(
      `With routing-only domains and no ~., resolved's automatic rule stops using ${device} for names nothing claims, unless its manager sets the default route itself.`,
    )
  else out.push(`${device} stays a default route: names nothing claims may be asked here too.`)
  return out
}
