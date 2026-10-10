import { literalAddress } from "../draft-input"

export function wireGuardIPv6Problem(raw: string) {
  const text = raw.trim()
  if (!text) return undefined
  const [address, bits, extra] = text.split("/")
  const parsed = literalAddress(address)
  if (extra !== undefined || bits !== "64" || parsed?.length !== 16 || (parsed[0] & 254) !== 252)
    return "Use a unique-local IPv6 /64, such as fd42:8::/64."
}

export function wireGuardIPv6Payload(enabled: boolean, subnet: string, exit: boolean) {
  return enabled ? { subnet: subnet.trim() || undefined, exitNode: exit } : undefined
}
