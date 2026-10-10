import { covers, parseAddress } from "../../lib/cidr"
import { calcSubnet } from "../network/tools/subnet-math"

export type NetworkDraft = {
  name: string
  driver: string
  internal: boolean
  attachable: boolean
  ipv6: boolean
  subnet: string
  gateway: string
  ipRange: string
  ipv6Subnet: string
  ipv6Gateway: string
  ipv6Range: string
  labels: string
  options: string
}

export const EMPTY_NETWORK: NetworkDraft = {
  name: "",
  driver: "bridge",
  internal: false,
  attachable: false,
  ipv6: false,
  subnet: "",
  gateway: "",
  ipRange: "",
  ipv6Subnet: "",
  ipv6Gateway: "",
  ipv6Range: "",
  labels: "",
  options: "",
}

type Pool = { subnet: string; gateway?: string; ipRange?: string }
export type NetworkCreation = {
  name: string
  internal: boolean
  driver?: string
  attachable?: boolean
  ipv6?: boolean
  subnet?: string
  gateway?: string
  ipRange?: string
  ipam?: Pool[]
  labels?: Record<string, string>
  options?: Record<string, string>
}

function metadata(input: string, label: string): Record<string, string> | undefined {
  const pairs = input.split("\n").filter((line) => line.trim())
  if (!pairs.length) return
  if (pairs.length > 32) throw new Error(`${label} can contain up to 32 entries.`)
  const entries = pairs.map((line) => {
    const separator = line.indexOf("=")
    if (separator < 1) throw new Error(`Write each ${label.toLowerCase()} entry as key=value.`)
    const key = line.slice(0, separator).trim()
    const value = line.slice(separator + 1)
    if (
      !key ||
      new TextEncoder().encode(key).length > 256 ||
      new TextEncoder().encode(value).length > 4096 ||
      `${key}${value}`.includes("\0")
    ) {
      throw new Error(`${label} need keys up to 256 bytes and values up to 4096 bytes.`)
    }
    if (label === "Labels" && /^(io\.just-dashboard\.|com\.docker\.compose\.)/.test(key)) {
      throw new Error("Compose and dashboard ownership labels are reserved.")
    }
    return [key, value] as const
  })
  if (new Set(entries.map(([key]) => key)).size !== entries.length) {
    throw new Error(`${label} contain a repeated key.`)
  }
  if (
    entries.reduce((size, [key, value]) => size + new TextEncoder().encode(key + value).length, 0) >
    16 * 1024
  ) {
    throw new Error(`${label} exceed 16 KiB.`)
  }
  return Object.fromEntries(entries)
}

function canonicalSubnet(input: string, family: number): string {
  const parts = input.trim().split("/")
  if (parts.length !== 2) throw new Error("Write the subnet with its prefix length.")
  const bytes = parseAddress(parts[0])
  if (!bytes || bytes.length !== family)
    throw new Error(`Use an IPv${family === 4 ? 4 : 6} subnet in this field.`)
  if (
    bytes.length === 16 &&
    bytes.slice(0, 10).every((byte) => byte === 0) &&
    bytes[10] === 255 &&
    bytes[11] === 255
  ) {
    throw new Error("Use IPv4 directly instead of an IPv4-mapped IPv6 subnet.")
  }
  const subnet = calcSubnet(input)
  if (parseAddress(subnet.network)?.some((byte, index) => byte !== bytes[index])) {
    throw new Error(`Use the network address ${subnet.cidr}, rather than an address inside it.`)
  }
  return subnet.cidr
}

function pool(subnet: string, gateway: string, range: string, family: number): Pool | undefined {
  if (!subnet.trim()) {
    if (gateway.trim() || range.trim())
      throw new Error("Set a subnet before its gateway or allocation range.")
    return
  }
  const cidr = canonicalSubnet(subnet, family)
  const result: Pool = { subnet: cidr }
  if (gateway.trim()) {
    const address = parseAddress(gateway.trim())
    if (!address || address.length !== family || !covers(cidr, gateway.trim())) {
      throw new Error("The gateway must be an address inside its subnet.")
    }
    result.gateway = calcSubnet(`${gateway.trim()}/${family * 8}`).network
  }
  if (range.trim()) {
    const allocation = canonicalSubnet(range, family)
    if (
      !covers(cidr, allocation.split("/")[0]) ||
      Number(allocation.split("/")[1]) < Number(cidr.split("/")[1])
    ) {
      throw new Error("The allocation range must fit inside its subnet.")
    }
    result.ipRange = allocation
  }
  return result
}

export function networkCreation(
  draft: NetworkDraft,
  networks: { name: string; subnets: string[] }[] = [],
): NetworkCreation {
  const name = draft.name.trim()
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(name))
    throw new Error("Give the network a name using letters, digits, dots, dashes or underscores.")
  const driver = draft.driver.trim()
  if (
    !driver ||
    /[\s\0]/.test(driver) ||
    new TextEncoder().encode(driver).length > 256 ||
    ["host", "none", "null"].includes(driver)
  ) {
    throw new Error("Choose a network driver; host and none are existing system networks.")
  }
  const v4 = pool(draft.subnet, draft.gateway, draft.ipRange, 4)
  const v6 = draft.ipv6 ? pool(draft.ipv6Subnet, draft.ipv6Gateway, draft.ipv6Range, 16) : undefined
  const pools = [v4, v6].filter((entry): entry is Pool => Boolean(entry))
  for (const candidate of pools) {
    for (const existing of networks) {
      if (
        existing.subnets.some(
          (subnet) =>
            covers(subnet, candidate.subnet.split("/")[0]) ||
            covers(candidate.subnet, subnet.split("/")[0]),
        )
      ) {
        throw new Error(`${candidate.subnet} overlaps ${existing.name}. Choose an unused range.`)
      }
    }
  }
  const spec: NetworkCreation = { name, internal: draft.internal }
  if (driver !== "bridge") spec.driver = driver
  if (draft.attachable) spec.attachable = true
  if (draft.ipv6) spec.ipv6 = true
  if (v6) spec.ipam = pools
  else if (v4) Object.assign(spec, v4)
  spec.labels = metadata(draft.labels, "Labels")
  spec.options = metadata(draft.options, "Driver options")
  return spec
}
