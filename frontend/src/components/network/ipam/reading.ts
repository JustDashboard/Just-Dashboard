import type { IPAMOwner, IPAMPool, IPAMView } from "@/lib/network-ipam"

/**
 * A prefix as the first address and the count of addresses it covers, exact
 * in either family. Undefined for anything that is not a canonical-looking
 * prefix: the planner's own records always parse, and an observation that
 * does not is left out of a pool's owners rather than guessed at.
 */
export function parsePrefix(
  prefix: string,
): { v6: boolean; start: bigint; end: bigint } | undefined {
  const [address, length] = prefix.split("/")
  if (address === undefined || length === undefined || !/^\d{1,3}$/.test(length)) return
  const bits = Number(length)
  const v6 = address.includes(":")
  const width = v6 ? 128 : 32
  if (bits > width) return
  const value = v6 ? v6Value(address) : v4Value(address)
  if (value === undefined) return
  const span = BigInt(1) << BigInt(width - bits)
  const start = value - (value % span)
  return { v6, start, end: start + span - BigInt(1) }
}

function v4Value(address: string) {
  const parts = address.split(".")
  if (parts.length !== 4 || parts.some((p) => !/^\d{1,3}$/.test(p) || Number(p) > 255)) return
  return parts.reduce((sum, part) => (sum << BigInt(8)) + BigInt(Number(part)), BigInt(0))
}

function v6Value(address: string) {
  const halves = address.split("::")
  if (halves.length > 2) return
  const groups = (half: string) => (half === "" ? [] : half.split(":"))
  const head = groups(halves[0])
  const tail = halves.length === 2 ? groups(halves[1]) : []
  const missing = 8 - head.length - tail.length
  if (missing < 0 || (halves.length === 1 && missing !== 0)) return
  const all = [...head, ...Array(missing).fill("0"), ...tail]
  if (all.some((g) => !/^[0-9a-f]{1,4}$/i.test(g))) return
  return all.reduce((sum, group) => (sum << BigInt(16)) + BigInt(parseInt(group, 16)), BigInt(0))
}

/** Two prefixes of one family share at least one address. */
export function overlaps(a: string, b: string) {
  const x = parsePrefix(a)
  const y = parsePrefix(b)
  if (!x || !y || x.v6 !== y.v6) return false
  return x.start <= y.end && y.start <= x.end
}

/**
 * The product an owner word is, for its logo: the planner's own owners and
 * the native inventory's ("docker", "tailscale_offer", "wireguard_peer").
 * Interfaces, routes and namespaces have no product and take a glyph.
 */
export function ownerProduct(owner: string): string | undefined {
  if (owner.startsWith("docker")) return "docker"
  if (owner.startsWith("wireguard")) return "wireguard"
  if (owner.startsWith("tailscale")) return "tailscale"
}

const RESERVATION_OWNER: Record<IPAMOwner, string> = {
  docker_network: "docker",
  wireguard_server: "wireguard",
  interface_address: "interface",
  namespace_address: "namespace",
  provider_range: "provider",
}

/**
 * Who holds space in a pool: the owners of its held reservations and the
 * native owners observed inside its network, each once, products first.
 */
export function poolOwners(pool: IPAMPool, view: IPAMView): string[] {
  const owners = new Set<string>()
  for (const row of view.reservations)
    if (row.poolId === pool.id && row.state !== "released") owners.add(RESERVATION_OWNER[row.owner])
  for (const seen of view.inventory.observations)
    if (overlaps(pool.prefix, seen.prefix)) owners.add(ownerProduct(seen.owner) ?? seen.owner)
  return [...owners].sort((a, b) => Number(!ownerProduct(a)) - Number(!ownerProduct(b)))
}

/**
 * A pool's band, in blocks: what the planner holds, what native owners hold
 * outside the planner's holdings (the union less the planned, so a block both
 * hold is counted once), and what is free. The counts are exact strings; the
 * band's spans need only their shares, which a float keeps.
 */
export function poolBand(use: {
  totalBlocks: string
  reservedBlocks: string
  unavailableBlocks: string
  candidateBlocks: string
}) {
  const total = BigInt(use.totalBlocks)
  const planned = BigInt(use.reservedBlocks)
  const unavailable = BigInt(use.unavailableBlocks)
  const observedOnly = unavailable > planned ? unavailable - planned : BigInt(0)
  return {
    total,
    planned,
    observedOnly,
    free: BigInt(use.candidateBlocks),
    share: (part: bigint) =>
      total === BigInt(0) ? 0 : Number((part * BigInt(1_000_000)) / total) / 1_000_000,
  }
}

/** An exact count with its thousands marked: "65,536". */
export const exact = (value: bigint | string) => BigInt(value).toLocaleString("en-US")
