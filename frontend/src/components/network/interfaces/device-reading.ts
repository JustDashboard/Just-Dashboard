import type { NetworkAddress, NetworkLinkDetail, NetworkPortVLAN } from "@/lib/types"
import { literalAddress } from "../draft-input"

/** Where an address came from, in the words the address list uses. */
export const ORIGIN_WORD: Record<NonNullable<NetworkAddress["origin"]>, string> = {
  static: "static",
  dhcp: "from DHCP",
  slaac: "from router advertisements",
  temporary: "temporary privacy address",
  dynamic: "dynamic (DHCPv6 or an advertisement)",
  "link-local": "link-local",
}

/** A lifetime in words, "forever" where there is none. */
export function lifetimeWords(seconds: number | undefined): string {
  if (seconds === undefined) return "forever"
  if (seconds < 60) return `${seconds} s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} min`
  if (seconds < 86400) {
    const hours = Math.floor(seconds / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    return minutes ? `${hours} h ${minutes} min` : `${hours} h`
  }
  return `${Math.floor(seconds / 86400)} d`
}

/** One address's origin and remaining life, for its row. */
export function addressProvenance(a: NetworkAddress): string {
  const origin = a.origin ? ORIGIN_WORD[a.origin] : a.dynamic ? "dynamic" : "static"
  if (a.validSeconds === undefined) return origin
  return `${origin}, valid ${lifetimeWords(a.validSeconds)}`
}

/**
 * A port's VLAN policy as the access/trunk model says it: one native VLAN,
 * untagged and the VLAN untagged frames arriving belong to, and the VLANs
 * carried tagged beside it.
 */
export function vlanPolicy(
  native: string,
  tagged: string,
): { vlans: NetworkPortVLAN[]; error?: string } {
  const vid = (text: string) => (/^\d+$/.test(text) ? Number(text) : NaN)
  const vlans: NetworkPortVLAN[] = []
  const seen = new Set<number>()
  const nativeText = native.trim()
  if (nativeText) {
    const n = vid(nativeText)
    if (!(n >= 1 && n <= 4094))
      return { vlans: [], error: "A VLAN id is a whole number 1 to 4094." }
    vlans.push({ vid: n, pvid: true, untagged: true })
    seen.add(n)
  }
  for (const part of tagged.split(/[\s,]+/).filter(Boolean)) {
    const [startText, endText] = part.split("-")
    const start = vid(startText)
    const end = endText === undefined ? start : vid(endText)
    if (!(start >= 1 && end <= 4094 && start <= end))
      return { vlans: [], error: `“${part}” is not a VLAN id or a range of them, 1 to 4094.` }
    for (let n = start; n <= end; n++) {
      if (seen.has(n)) return { vlans: [], error: `VLAN ${n} is listed twice.` }
      seen.add(n)
      vlans.push({ vid: n })
    }
  }
  if (vlans.length > 64) return { vlans: [], error: "A port carries at most 64 VLANs here." }
  return { vlans: vlans.sort((a, b) => a.vid - b.vid) }
}

/** The native and tagged fields that describe a membership list. */
export function vlanFields(vlans: NetworkPortVLAN[]): { native: string; tagged: string } {
  const native = vlans.find((v) => v.pvid)
  return {
    native: native ? String(native.vid) : "",
    tagged: vlans
      .filter((v) => v !== native)
      .map((v) => v.vid)
      .join(", "),
  }
}

/** A membership list in words: "native 10 · tagged 20, 30". */
export function describeVlans(vlans: NetworkPortVLAN[]): string {
  if (vlans.length === 0) return "no VLANs"
  const native = vlans.find((v) => v.pvid)
  const untagged = vlans.filter((v) => v.untagged && v !== native).map((v) => v.vid)
  const tagged = vlans.filter((v) => !v.untagged && v !== native).map((v) => v.vid)
  const parts: string[] = []
  if (native) parts.push(`native ${native.vid}${native.untagged ? "" : " (tagged)"}`)
  if (untagged.length) parts.push(`untagged ${untagged.join(", ")}`)
  if (tagged.length) parts.push(`tagged ${tagged.join(", ")}`)
  return parts.join(" · ")
}

/** Whether the kernel holds exactly the memberships the spec wants. */
export function sameVlans(a: NetworkPortVLAN[], b: NetworkPortVLAN[]): boolean {
  const key = (v: NetworkPortVLAN) => `${v.vid}:${v.pvid ? 1 : 0}:${v.untagged ? 1 : 0}`
  return a.map(key).sort().join() === b.map(key).sort().join()
}

/** A VXLAN's further flood ends from a free-text list, in the remote's family. */
export function parseRemotes(
  text: string,
  primary: string | undefined,
): { remotes: string[]; error?: string } {
  const family = primary ? literalAddress(primary)?.length : undefined
  const remotes: string[] = []
  for (const part of text.split(/[\s,]+/).filter(Boolean)) {
    const bytes = literalAddress(part)
    if (!bytes) return { remotes: [], error: `“${part}” is not one IP address.` }
    if (family && bytes.length !== family)
      return { remotes: [], error: `Every end is IPv${family === 4 ? "4" : "6"}, like the remote.` }
    const multicast = bytes.length === 4 ? bytes[0] >= 224 && bytes[0] <= 239 : bytes[0] === 255
    if (multicast || bytes.every((b) => b === 0))
      return { remotes: [], error: `${part} is not another end's unicast address.` }
    if (part !== primary && !remotes.includes(part)) remotes.push(part)
  }
  if (remotes.length > 64) return { remotes: [], error: "At most 64 further ends here." }
  return { remotes }
}

/** The error counters worth a row, by name, with their values; zeros left out. */
export function errorRows(errors: NetworkLinkDetail["errors"]): { label: string; value: number }[] {
  if (!errors) return []
  const labels: [keyof NonNullable<NetworkLinkDetail["errors"]>, string][] = [
    ["rxCrcErrors", "CRC errors (received)"],
    ["rxFrameErrors", "Frame errors (received)"],
    ["rxLengthErrors", "Length errors (received)"],
    ["rxMissedErrors", "Missed by the NIC (received)"],
    ["rxFifoErrors", "FIFO overruns (received)"],
    ["rxOverErrors", "Ring overflows (received)"],
    ["rxDropped", "Dropped (received)"],
    ["txCarrierErrors", "Carrier errors (sent)"],
    ["txCollisions", "Collisions (sent)"],
    ["txAbortedErrors", "Aborted (sent)"],
    ["txFifoErrors", "FIFO underruns (sent)"],
    ["txWindowErrors", "Window errors (sent)"],
    ["txHeartbeatErrors", "Heartbeat errors (sent)"],
    ["txDropped", "Dropped (sent)"],
    ["carrierChanges", "Carrier changes"],
  ]
  return labels
    .filter(([key]) => errors[key] > 0)
    .map(([key, label]) => ({ label, value: errors[key] }))
}
