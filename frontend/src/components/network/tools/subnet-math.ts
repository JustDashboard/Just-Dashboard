export type SubnetInfo = {
  cidr: string
  mask: string
  wildcard: string
  network: string
  broadcast: string
  first: string
  last: string
  hosts: string
  note: string
}

function parseOctets(s: string): number[] | null {
  const parts = s.trim().split(".")
  if (parts.length !== 4) return null
  const nums = parts.map((p) => {
    if (!/^(0|[1-9]\d{0,2})$/.test(p)) return -1
    return Number(p)
  })
  if (nums.some((n) => n < 0 || n > 255)) return null
  return nums
}

function toDotted(n: number): string {
  return [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join(".")
}

function toNum(octets: number[]): number {
  return ((octets[0] << 24) | (octets[1] << 16) | (octets[2] << 8) | octets[3]) >>> 0
}

/**
 * The subnet calculator: pure arithmetic in the browser, no probe — a mask
 * is math, not traffic, so it answers instantly and works offline.
 */
export function calcSubnet(input: string): SubnetInfo {
  const m = input.trim().match(/^(.+?)\s*\/\s*(\d{1,3})$/)
  if (!m) throw new Error('Write it as an address with a prefix, like "192.168.1.20/24".')
  const prefix = Number(m[2])
  if (m[1].includes(":")) return calcIPv6(m[1].trim(), prefix)
  const octets = parseOctets(m[1])
  if (!octets || prefix > 32) throw new Error("That is not an IPv4 address with a /0–/32 prefix.")
  const addr = toNum(octets)
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0
  const network = (addr & mask) >>> 0
  const broadcast = (network | ~mask) >>> 0
  const size = 2 ** (32 - prefix)
  let first: string
  let last: string
  let hosts: string
  if (prefix === 32) {
    first = toDotted(network)
    last = first
    hosts = "1 (a single host route)"
  } else if (prefix === 31) {
    // RFC 3021: a point-to-point link has no broadcast, both are usable.
    first = toDotted(network)
    last = toDotted(broadcast)
    hosts = "2 (point-to-point, both usable)"
  } else {
    first = toDotted(network + 1)
    last = toDotted(broadcast - 1)
    hosts = `${(size - 2).toLocaleString("en-US")} usable`
  }
  const firstOctet = octets[0]
  const secondOctet = octets[1]
  let note = "Public address space."
  if (
    firstOctet === 10 ||
    (firstOctet === 172 && secondOctet >= 16 && secondOctet <= 31) ||
    (firstOctet === 192 && secondOctet === 168)
  ) {
    note = "Private (RFC 1918) — not routable on the internet."
  } else if (firstOctet === 127) {
    note = "Loopback — this machine only."
  } else if (firstOctet === 169 && secondOctet === 254) {
    note = "Link-local — one broadcast domain, no router."
  } else if (firstOctet >= 224 && firstOctet <= 239) {
    note = "Multicast — not a host address."
  } else if (firstOctet === 100 && secondOctet >= 64 && secondOctet <= 127) {
    note = "Shared carrier NAT space (RFC 6598) — not publicly routable."
  } else if (
    (firstOctet === 192 && secondOctet === 0 && octets[2] === 2) ||
    (firstOctet === 198 && secondOctet === 51 && octets[2] === 100) ||
    (firstOctet === 203 && secondOctet === 0 && octets[2] === 113)
  ) {
    note = "Documentation address space — not for public hosts."
  } else if (firstOctet === 198 && (secondOctet === 18 || secondOctet === 19)) {
    note = "Benchmarking address space — not publicly routable."
  } else if (firstOctet === 0 || firstOctet >= 240) {
    note = "Reserved address space — not a public host address."
  }
  return {
    cidr: `${toDotted(network)}/${prefix}`,
    mask: toDotted(mask),
    wildcard: toDotted(~mask >>> 0),
    network: toDotted(network),
    broadcast: toDotted(broadcast),
    first,
    last,
    hosts,
    note,
  }
}

function parseIPv6(input: string): bigint {
  let address = input
  if (address.includes(".")) {
    const colon = address.lastIndexOf(":")
    const octets = parseOctets(address.slice(colon + 1))
    if (colon < 0 || !octets) throw new Error("That is not a valid IPv6 address.")
    address = `${address.slice(0, colon)}:${((octets[0] << 8) | octets[1]).toString(16)}:${((octets[2] << 8) | octets[3]).toString(16)}`
  }
  const halves = address.split("::")
  if (halves.length > 2) throw new Error("That is not a valid IPv6 address.")
  const left = halves[0] ? halves[0].split(":") : []
  const right = halves.length === 2 && halves[1] ? halves[1].split(":") : []
  const count = left.length + right.length
  if ((halves.length === 1 && count !== 8) || (halves.length === 2 && count >= 8)) {
    throw new Error("That is not a valid IPv6 address.")
  }
  if (![...left, ...right].every((part) => /^[a-fA-F0-9]{1,4}$/.test(part))) {
    throw new Error("That is not a valid IPv6 address.")
  }
  const groups = [...left, ...Array(8 - count).fill("0"), ...right]
  return groups.reduce((value, group) => (value << BigInt(16)) | BigInt(`0x${group}`), BigInt(0))
}

function formatIPv6(value: bigint): string {
  const groups = Array.from({ length: 8 }, (_, index) =>
    ((value >> BigInt((7 - index) * 16)) & BigInt(65535)).toString(16),
  )
  let start = -1
  let length = 1
  for (let index = 0; index < groups.length;) {
    if (groups[index] !== "0") {
      index++
      continue
    }
    let end = index
    while (end < groups.length && groups[end] === "0") end++
    if (end - index > length) {
      start = index
      length = end - index
    }
    index = end
  }
  if (start < 0) return groups.join(":")
  return `${groups.slice(0, start).join(":")}::${groups.slice(start + length).join(":")}`
}

function calcIPv6(address: string, prefix: number): SubnetInfo {
  if (prefix > 128) throw new Error("IPv6 needs a /0–/128 prefix.")
  const value = parseIPv6(address)
  const all = (BigInt(1) << BigInt(128)) - BigInt(1)
  const wildcard = (BigInt(1) << BigInt(128 - prefix)) - BigInt(1)
  const mask = all ^ wildcard
  const network = value & mask
  const last = network | wildcard
  const formatted = formatIPv6(network)
  const top = Number(value >> BigInt(112))
  let note = "IPv6 has no broadcast. Reserved assignments may reduce the usable range."
  if (value === BigInt(0)) note = "Unspecified IPv6 address."
  else if (value === BigInt(1)) note = "Loopback — this machine only."
  else if ((top & 0xff00) === 0xff00) note = "Multicast — not a host address."
  else if ((top & 0xffc0) === 0xfe80) note = "Link-local — one link, no router."
  else if ((top & 0xfe00) === 0xfc00) note = "Unique local IPv6 space — not publicly routable."
  else if (value >> BigInt(96) === BigInt(0x20010db8))
    note = "Documentation IPv6 space — not for public hosts."
  return {
    cidr: `${formatted}/${prefix}`,
    mask: formatIPv6(mask),
    wildcard: formatIPv6(wildcard),
    network: formatted,
    broadcast: "None (IPv6 has no broadcast)",
    first: formatted,
    last: formatIPv6(last),
    hosts:
      prefix === 128
        ? "1 (a single host route)"
        : `${(wildcard + BigInt(1)).toLocaleString("en-US")} addresses`,
    note,
  }
}
