/** Match the backend's IPv4 containment check for the SSH tunnel address. */
export function coversLoopback(entry: string): boolean {
  const parts = entry.trim().split("/")
  if (parts.length > 2) return false
  const octets = parts[0].split(".")
  if (octets.length !== 4 || octets.some((part) => !/^(0|[1-9]\d{0,2})$/.test(part))) return false
  const numbers = octets.map(Number)
  if (numbers.some((part) => part > 255)) return false
  const prefix = parts.length === 1 ? 32 : Number(parts[1])
  if (parts.length === 2 && !/^(0|[1-9]\d?)$/.test(parts[1])) return false
  if (!Number.isInteger(prefix) || prefix < 0 || prefix > 32) return false
  const address = numbers.reduce((value, part) => value * 256 + part, 0)
  const block = 2 ** (32 - prefix)
  return Math.floor(address / block) === Math.floor(0x7f000001 / block)
}
