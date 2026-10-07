import type { DNSPreset, DNSView } from "@/lib/types"

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
export const KIND_PRODUCT: Partial<Record<DNSView["listeners"][number]["kind"], string>> = {
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
