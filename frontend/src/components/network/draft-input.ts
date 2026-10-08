import { parseAddress } from "@/lib/cidr"
import { portProblem } from "./tools/tool-input"

/** Match the backend's literal-address grammar while sharing the existing IPv6 parser. */
export function literalAddress(text: string): number[] | null {
  const address = text.trim()
  const dotted = address.slice(address.lastIndexOf(":") + 1)
  if (address.includes(".") && dotted.split(".").some((part) => !/^(0|[1-9]\d{0,2})$/.test(part)))
    return null
  const bytes = parseAddress(address)
  if (
    bytes?.length === 16 &&
    bytes.slice(0, 10).every((byte) => byte === 0) &&
    bytes[10] === 255 &&
    bytes[11] === 255
  )
    return bytes.slice(12)
  return bytes
}

export function preferredSourceProblem(source: string, destination: string, gateway: string) {
  if (!source.trim()) return undefined
  const address = literalAddress(source)
  if (!address) return "Use one IPv4 or IPv6 address, without a prefix or zone."
  const destinationAddress = literalAddress(destination.trim().split("/")[0])
  const gatewayAddress = literalAddress(gateway)
  if (
    (destinationAddress && destinationAddress.length !== address.length) ||
    (gatewayAddress && gatewayAddress.length !== address.length)
  )
    return "The preferred source must use the same address family as the destination and gateway."
}

export function noteProblem(text: string) {
  const note = text.trim()
  if (new TextEncoder().encode(note).length > 80) return "Keep the note within 80 bytes."
  if (/[\p{C}\p{Zl}\p{Zp}"\\#;{}`$]/u.test(note))
    return "Use a printable note without quotes, backslashes, braces, #, ;, ` or $."
}

/** DNS endpoints retain ports, interface scopes and TLS names rather than guessing a host name. */
export function dnsServersProblem(servers: string[]): string | undefined {
  if (servers.length > 8) return "Use at most eight servers."
  for (const server of servers) {
    const [head, tlsName, extraName] = server.split("#")
    if (extraName !== undefined) return `${server} has more than one TLS name.`
    if (tlsName !== undefined) {
      const name = tlsName.replace(/\.$/, "")
      if (
        !name ||
        name.length > 253 ||
        name.split(".").some((label) => !/^[a-z\d](?:[a-z\d-]{0,61}[a-z\d])?$/i.test(label))
      )
        return `${server} needs a valid DNS name after #.`
    }
    const scope = head.split("%")
    let endpoint = scope[0]
    let iface = scope[1]
    if (scope.length > 2) return `${server} has more than one interface scope.`
    if (iface !== undefined) {
      const bracketed = iface.match(/^(.+)\]:(\d+)$/)
      if (endpoint.startsWith("[") && bracketed) {
        iface = bracketed[1]
        endpoint += `]:${bracketed[2]}`
      }
      if (!/^[a-z\d._@-]{1,15}$/i.test(iface) || iface === "." || iface === "..")
        return `${server} needs a valid interface name after %.`
    }
    let address = endpoint
    let port: string | undefined
    if (endpoint.startsWith("[")) {
      const bracketed = endpoint.match(/^\[([^\]]+)\](?::(\d+))?$/)
      if (!bracketed) return `${server} needs a bracketed IPv6 address before its port.`
      address = bracketed[1]
      port = bracketed[2]
    } else if (endpoint.split(":").length === 2) {
      const parts = endpoint.split(":")
      address = parts[0]
      port = parts[1]
    }
    if (port !== undefined && portProblem(port)) return `${server} needs a port from 1 to 65535.`
    const bytes = literalAddress(address)
    if (!bytes) return `${server} needs a literal IPv4 or IPv6 address.`
    if (
      bytes.every((byte) => byte === 0) ||
      (bytes.length === 4 && bytes[0] >= 224 && bytes[0] <= 239) ||
      (bytes.length === 16 && bytes[0] === 255)
    )
      return `${server} cannot be an unspecified or multicast DNS server.`
    const stub = bytes.length === 4 && bytes.slice(0, 3).join(".") === "127.0.0"
    if (stub && (bytes[3] === 53 || bytes[3] === 54) && (!port || Number(port) === 53))
      return `${server} is systemd-resolved's own listener; choose an upstream server.`
    const linkLocal =
      bytes.length === 4
        ? bytes[0] === 169 && bytes[1] === 254
        : bytes[0] === 254 && (bytes[1] & 192) === 128
    if (linkLocal && !iface) return `${server} needs its interface after %, such as %eno1.`
  }
}
