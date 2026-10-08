import { literalAddress } from "../draft-input"

type Draft = {
  vni: string
  remote: string
  group: string
  local: string
  parent: string
  ttl: string
  key: string
}

export type TunnelProblems = Partial<Record<keyof Draft, string>>

function numberProblem(value: string, label: string, max: number, min = 0) {
  const text = value.trim()
  if (text && (!/^\d+$/.test(text) || Number(text) < min || Number(text) > max))
    return `Use a whole ${label} from ${min} to ${max}.`
}

function multicast(address: number[]) {
  return address.length === 4 ? address[0] >= 224 && address[0] <= 239 : address[0] === 255
}

/** Keep the form's optional numbers and endpoint family aligned with LinkRequest. */
export function tunnelProblems(kind: string, draft: Draft): TunnelProblems {
  if (kind !== "vxlan" && !kind.includes("gre")) return {}
  const errors: TunnelProblems = {}
  const remote = draft.remote.trim()
  const group = draft.group.trim()
  const local = draft.local.trim()
  const remoteAddress = literalAddress(remote)
  const groupAddress = literalAddress(group)
  const localAddress = literalAddress(local)
  if (remote && !remoteAddress) errors.remote = "Use one IP address, without a prefix or zone."
  if (local && !localAddress) errors.local = "Use one IP address, without a prefix or zone."

  if (kind === "vxlan") {
    errors.vni = numberProblem(draft.vni, "VNI", 16777215, 1)
    if (remote && group) errors.group = "Use a remote end or a multicast group, not both."
    if (remoteAddress && (multicast(remoteAddress) || remoteAddress.every((byte) => byte === 0)))
      errors.remote = "The remote end must be a unicast address."
    if (group && (!groupAddress || !multicast(groupAddress)))
      errors.group = "Use an IPv4 or IPv6 multicast group address."
    if (group && !draft.parent) errors.parent = "Choose the network card that sends to this group."
  } else {
    errors.ttl = numberProblem(draft.ttl, "TTL or hop limit", 255)
    errors.key = numberProblem(draft.key, "key", 4294967295)
    const ipv6 = kind.startsWith("ip6")
    if (remoteAddress && (remoteAddress.length === 16) !== ipv6)
      errors.remote = `This tunnel needs IPv${ipv6 ? "6" : "4"} endpoints.`
  }

  const destination = kind === "vxlan" && group ? groupAddress : remoteAddress
  if (destination && localAddress && destination.length !== localAddress.length)
    errors.local = "The local and destination addresses must use the same address family."
  return errors
}
