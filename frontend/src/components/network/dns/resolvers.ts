import type { DotTone } from "@/components/status-dot"
import type {
  DNSClearableList,
  DNSPreset,
  DNSVerification,
  DNSVerificationCheck,
  DNSView,
  HostRecordIssue,
} from "@/lib/types"

/**
 * What the DNS page reads out of resolved's own words. Nothing here draws:
 * these are the rules the picture, the editor and the readings share, so a
 * server written as `1.1.1.1#cloudflare-dns.com` is one upstream everywhere.
 */

/** An upstream as resolved prints it: an address, a `#name` for TLS, a `%iface` on a link-local one. */
export function splitServer(server: string): { address: string; tlsName?: string } {
  const [head, tlsName] = server.split("#")
  return { address: head.replace(/%.*$/, ""), tlsName: tlsName || undefined }
}

/** The preset a server belongs to, by address: the name and port suffixes do not change who answers. */
export function presetFor(server: string, presets: DNSPreset[]): DNSPreset | undefined {
  const { address } = splitServer(server)
  return presets.find((p) => p.servers.includes(address))
}

/** Whether a draft's servers are exactly this preset's, in either spelling. */
export function presetChosen(preset: DNSPreset, servers: string[]): boolean {
  const addresses = new Set(servers.map((s) => splitServer(s).address))
  return preset.servers.every((s) => addresses.has(s)) && addresses.size === preset.servers.length
}

/** Servers typed one per line, or separated by commas or spaces, each once. */
export function parseList(text: string): string[] {
  return [...new Set(text.split(/[\s,]+/).filter(Boolean))]
}

/** The servers DNS over TLS would refuse: it validates a certificate, so each needs the name to check. */
export function missingTLSNames(servers: string[]): string[] {
  return servers.filter((s) => !s.includes("#"))
}

export type Encryption = "required" | "opportunistic" | "plain"

/** How a scope's DNS over TLS setting treats the servers under it. */
export function encryptionOf(setting: string | undefined): Encryption {
  if (setting === "yes") return "required"
  if (setting === "opportunistic") return "opportunistic"
  return "plain"
}

/**
 * Whether two addresses are one server, ignoring the name, zone and — for a
 * `currentServer` that resolved prints with the TLS name — everything after the
 * address.
 */
export function sameServer(a: string | undefined, b: string | undefined): boolean {
  if (!a || !b) return false
  return splitServer(a).address === splitServer(b).address
}

/** The product a listener or ad-blocker kind is drawn as; `undefined` has no mark of its own. */
export const KIND_PRODUCT: Partial<
  Record<DNSView["listeners"][number]["kind"] | DNSView["adblock"][number]["kind"], string>
> = {
  adguardhome: "adguard",
  pihole: "pihole",
  unbound: "unbound",
  "docker-proxy": "docker",
}

/** What a listener kind is called, where the process name alone would not say. */
export const KIND_NAME: Record<DNSView["listeners"][number]["kind"], string> = {
  "resolved-stub": "systemd-resolved",
  dnsmasq: "dnsmasq",
  unbound: "Unbound",
  named: "BIND",
  adguardhome: "AdGuard Home",
  pihole: "Pi-hole",
  "docker-proxy": "Docker's published port",
  other: "Another resolver",
}

/** A check's state, drawn as the dot it is: failed is the only danger. */
export function checkTone(state: DNSVerificationCheck["state"]): DotTone {
  switch (state) {
    case "passed":
      return "running"
    case "failed":
      return "danger"
    case "warning":
      return "warning"
    default:
      return "notice"
  }
}

export const CHECK_KIND_NAME: Record<DNSVerificationCheck["kind"], string> = {
  readback: "Read back",
  resolution: "Resolves",
  transport: "Transport",
  dnssec: "DNSSEC",
}

/** The lists a draft clears: a list can be cleared only while nothing is typed in it. */
export function clearedLists(
  lists: Record<DNSClearableList, string[]>,
  clear: Partial<Record<DNSClearableList, boolean>>,
): DNSClearableList[] {
  return (["servers", "fallback", "domains"] as const).filter(
    (list) => clear[list] && lists[list].length === 0,
  )
}

/** The verification a refused change carries beside its error, when it does. */
export function refusedVerification(body: unknown): DNSVerification | undefined {
  if (typeof body !== "object" || body === null) return undefined
  const value = (body as { verification?: unknown }).verification
  if (
    typeof value !== "object" ||
    value === null ||
    !Array.isArray((value as DNSVerification).checks) ||
    !Array.isArray((value as DNSVerification).unverified)
  )
    return undefined
  return value as DNSVerification
}

/** A preview's overlaps by the record they belong to, worst first within each. */
export function issuesByRecord(issues: HostRecordIssue[]): Map<number, HostRecordIssue[]> {
  const rank: Record<HostRecordIssue["kind"], number> = {
    conflict: 0,
    shadowed: 1,
    duplicate: 2,
    overrides: 3,
    repeated: 4,
  }
  const out = new Map<number, HostRecordIssue[]>()
  for (const issue of [...issues].sort((a, b) => rank[a.kind] - rank[b.kind])) {
    out.set(issue.record, [...(out.get(issue.record) ?? []), issue])
  }
  return out
}

/** Whether an overlap changes which address a program gets, rather than repeating one. */
export function issueMatters(issue: HostRecordIssue): boolean {
  return issue.kind === "conflict" || issue.kind === "shadowed"
}
