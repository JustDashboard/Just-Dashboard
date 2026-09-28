import { PRIVATE_NETWORKS } from "@/lib/stream-presets"

/**
 * Addresses and ranges as nginx's allow and deny read them, so a stream's
 * access list can say which rule a client lands on before anything is saved.
 * The server checks every source again; this only has to agree with it.
 */

/** A range a rule can be given in one press. */
export type QuickRange = {
  id: string
  label: string
  sources: string[]
}

export const QUICK_RANGES: QuickRange[] = [
  { id: "private", label: "Private networks", sources: PRIVATE_NETWORKS },
  // Tailscale hands out addresses from the carrier-grade NAT block and its
  // own IPv6 prefix; 100.64.0.0/10 is also what some ISPs use, which the
  // label cannot know.
  { id: "tailnet", label: "Tailnet", sources: ["100.64.0.0/10", "fd7a:115c:a1e0::/48"] },
  // Only this host itself: a client in a container reaches the stream from
  // its bridge address, not from loopback.
  { id: "loopback", label: "This host", sources: ["127.0.0.0/8", "::1"] },
]

function parseV4(text: string): number[] | null {
  if (!/^\d{1,3}(\.\d{1,3}){3}$/.test(text)) return null
  const bytes = text.split(".").map(Number)
  return bytes.every((b) => b <= 255) ? bytes : null
}

function parseV6(text: string): number[] | null {
  const halves = text.split("::")
  if (halves.length > 2) return null
  const words = (part: string, last: boolean): number[] | null => {
    if (part === "") return []
    const groups = part.split(":")
    const out: number[] = []
    for (const [i, group] of groups.entries()) {
      const v4 = last && i === groups.length - 1 && group.includes(".") ? parseV4(group) : null
      if (v4) out.push((v4[0] << 8) | v4[1], (v4[2] << 8) | v4[3])
      else if (/^[0-9a-f]{1,4}$/i.test(group)) out.push(parseInt(group, 16))
      else return null
    }
    return out
  }
  const head = words(halves[0], halves.length === 1)
  const tail = halves.length === 2 ? words(halves[1], true) : []
  if (!head || !tail) return null
  const missing = 8 - head.length - tail.length
  if (halves.length === 1 ? missing !== 0 : missing < 1) return null
  return [...head, ...Array<number>(missing).fill(0), ...tail].flatMap((w) => [w >> 8, w & 255])
}

/** An address as its bytes, four for IPv4 and sixteen for IPv6, or null. */
export function parseAddress(text: string): number[] | null {
  const trimmed = text.trim()
  return trimmed.includes(":") ? parseV6(trimmed) : parseV4(trimmed)
}

/** A source as its network bytes and prefix length: an address is a range of one. */
function parseSource(text: string): { bytes: number[]; prefix: number } | null {
  const [address, prefix, extra] = text.trim().split("/")
  const bytes = parseAddress(address)
  if (!bytes || extra !== undefined) return null
  if (prefix === undefined) return { bytes, prefix: bytes.length * 8 }
  const bits = Number(prefix)
  if (!/^\d{1,3}$/.test(prefix) || bits > bytes.length * 8) return null
  return { bytes, prefix: bits }
}

/** Why a rule's source would be refused, or "" when it is an address or a CIDR. */
export function sourceError(text: string): string {
  const trimmed = text.trim()
  if (trimmed === "") return "Give an address or a range."
  if (trimmed === "all") return "Everyone is the choice below the rules, not a rule."
  return parseSource(trimmed) ? "" : `${trimmed} is not an IP address or a CIDR range.`
}

/** Whether a source covers an address. An IPv4 range never covers an IPv6 client. */
export function covers(source: string, address: string): boolean {
  const range = parseSource(source)
  const ip = parseAddress(address)
  if (!range || !ip || range.bytes.length !== ip.length) return false
  for (let bit = 0; bit < range.prefix; bit++) {
    const byte = bit >> 3
    const mask = 0x80 >> (bit & 7)
    if ((range.bytes[byte] & mask) !== (ip[byte] & mask)) return false
  }
  return true
}

/** The first rule, by index, that an address matches, as nginx looks; -1 is none. */
export function firstMatch(rules: { source: string }[], address: string): number {
  return rules.findIndex((rule) => covers(rule.source, address))
}
