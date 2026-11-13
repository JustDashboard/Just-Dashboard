import type { AuditEntry, NetworkLink } from "@/lib/types"

/**
 * What the Interfaces list, a device's page and its rail all say about a
 * device, kept apart from React so it is tested in `bun test src`: where a
 * device's page is, what state it is in, which group it is listed under,
 * which views its page has, what it is wired to and what was done to it.
 */

/** A device's views, in the order its tab strip draws them. */
export const DEVICE_TABS = ["traffic", "addresses", "ports", "settings", "history"] as const
export type DeviceTab = (typeof DEVICE_TABS)[number]

/** A device's page, on a view. Traffic is the page itself, so it is not said. */
export function deviceHref(name: string, tab?: string) {
  const path = `/network/interfaces/${encodeURIComponent(name)}`
  return tab && tab !== "traffic" ? `${path}?tab=${encodeURIComponent(tab)}` : path
}

/** The `[name]` segment as the kernel spells the device. */
export function deviceName(segment: string | undefined) {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

type Stateful = Pick<NetworkLink, "adminUp" | "carrier" | "state">

/**
 * Up, set up with nothing on the wire, or set down. A tunnel has no carrier
 * to lose and reads "unknown" while it works, which is up.
 */
export function deviceState(link: Stateful): {
  up: boolean
  tone: "running" | "warning" | "stopped"
  label: "Up" | "No carrier" | "Down"
} {
  const up = link.adminUp && (link.carrier || link.state === "unknown")
  if (up) return { up, tone: "running", label: "Up" }
  return link.adminUp
    ? { up, tone: "warning", label: "No carrier" }
    : { up, tone: "stopped", label: "Down" }
}

/** The groups a device list is read in, top to bottom, and which of them hide by default. */
export const GROUPS: {
  key: string
  title: string
  match: (l: NetworkLink) => boolean
  everythingOnly?: boolean
}[] = [
  { key: "uplink", title: "Uplink", match: (l) => l.role === "uplink" },
  { key: "physical", title: "Network cards", match: (l) => l.role === "physical" },
  {
    key: "tunnel",
    title: "Tunnels",
    match: (l) => l.role === "tunnel" && l.owner !== "kernel",
  },
  { key: "bridge", title: "Bridges", match: (l) => l.role === "bridge" },
  {
    key: "vlan",
    title: "VLANs and virtual devices",
    match: (l) => (l.role === "vlan" || l.role === "virtual") && l.owner !== "kernel",
  },
  {
    key: "container",
    title: "Containers",
    match: (l) => l.role === "container",
    everythingOnly: true,
  },
  {
    key: "kernel",
    title: "The kernel's own",
    match: (l) => l.owner === "kernel",
    everythingOnly: true,
  },
]

/** Whether a device is one of those Everything adds: a container's veth, the kernel's own. */
export function behindEverything(link: NetworkLink) {
  return GROUPS.some((g) => g.everythingOnly && g.match(link))
}

function matcher(query: string) {
  const needle = query.trim().toLowerCase()
  return (l: NetworkLink) =>
    !needle ||
    l.name.toLowerCase().includes(needle) ||
    (l.container ?? "").toLowerCase().includes(needle) ||
    (l.dockerNetwork ?? "").toLowerCase().includes(needle) ||
    l.addresses.some((a) => a.cidr.includes(needle))
}

/**
 * The devices as the list draws them: each in the first group that takes it,
 * the hidden groups only under Everything or a search, empty groups left out.
 * The rail beside a device walks the same order.
 */
export function groupDevices(
  links: NetworkLink[],
  { everything, query }: { everything: boolean; query: string },
) {
  const matches = matcher(query)
  const searching = query.trim() !== ""
  const seen = new Set<string>()
  return GROUPS.filter((g) => everything || !g.everythingOnly || searching)
    .map((g) => {
      const members = links.filter((l) => !seen.has(l.name) && g.match(l) && matches(l))
      members.forEach((l) => seen.add(l.name))
      return { key: g.key, title: g.title, members }
    })
    .filter((g) => g.members.length > 0)
}

/** What a device is attached to, and who keeps it: the second half of its line. */
export function describeDevice(link: NetworkLink, ownerLabel: Record<string, string>) {
  if (link.dockerJoin === "unresolved")
    return `container not joined · on ${link.master ?? "no bridge"}`
  if (link.dockerJoin === "unknown") return "Docker network unknown"
  if (link.container) return `${link.container} · on ${link.master ?? "no bridge"}`
  if (link.dockerNetwork) return `Docker network ${link.dockerNetwork}`
  if (link.role === "bridge" && link.members?.length)
    return `${link.members.length} port${link.members.length === 1 ? "" : "s"}: ${link.members.slice(0, 3).join(", ")}`
  if (link.parent) return `on ${link.parent}${link.master ? ` · port of ${link.master}` : ""}`
  if (link.remote) return `to ${link.remote}`
  if (link.master) return `port of ${link.master}`
  return ownerLabel[link.owner] ?? link.owner
}

/**
 * The list page's one line: how many devices a person would count (not the
 * veths and the kernel's own), which carry the default route, how many the
 * dashboard made, and whether any of them is set up with nothing on the wire.
 */
export function listFacts(links: NetworkLink[]) {
  const real = links.filter((l) => !behindEverything(l))
  return {
    devices: real.length,
    everything: links.length,
    uplinks: links.filter((l) => l.role === "uplink").map((l) => l.name),
    made: links.filter((l) => l.managed).length,
    up: real.filter((l) => deviceState(l).up).length,
    noCarrier: real.filter((l) => deviceState(l).label === "No carrier").length,
  }
}

/**
 * The views a device's page has. A bridge has ports; only an administrator
 * can read what was done to a device, because the audit trail is theirs.
 */
export function deviceViews(link: Pick<NetworkLink, "kind" | "role">, admin: boolean): DeviceTab[] {
  return DEVICE_TABS.filter((tab) => {
    if (tab === "ports") return link.kind === "bridge" || link.role === "bridge"
    if (tab === "history") return admin
    return true
  })
}

/** One end of a device's picture: a thing it leads to, or a thing on this server that uses it. */
export type PictureNode = {
  id: string
  kind: "device" | "internet" | "wire" | "self" | "peers" | "remote" | "namespace" | "address"
  eyebrow: string
  title: string
  hint?: string
  /** The words are a literal from the host: a name, an address. */
  mono?: boolean
  /** Another device, whose page the node opens. */
  device?: string
  /** A page elsewhere. */
  href?: string
  /** A namespace the node opens in its sheet. */
  namespace?: { kind: "named" | "container"; name: string }
  /** One of this device's own views. */
  tab?: DeviceTab
}

const unicast = (address: string) => {
  const first = Number(address.split(".")[0])
  return address.includes(":")
    ? !address.toLowerCase().startsWith("ff")
    : !(first >= 224 && first <= 239)
}

/**
 * What a device is wired to, as the two sides of its picture. `away` is the
 * far side of the wire: the internet behind an uplink, a tunnel's other end,
 * the namespace a veth leads into, the card a VLAN rides on, a bridge's
 * ports. `here` is what this server does with it: the bridge it is a port
 * of, the devices that ride on it, the address the server answers at.
 */
export function devicePicture(
  link: NetworkLink,
  links: NetworkLink[],
): { away: PictureNode[]; here: PictureNode[] } {
  const away: PictureNode[] = []
  const here: PictureNode[] = []
  const speed = link.speedMbps ? `${link.speedMbps} Mb/s link` : undefined

  if (link.role === "bridge" || link.kind === "bridge") {
    for (const port of links.filter((l) => l.master === link.name)) {
      away.push({
        id: `port:${port.name}`,
        kind: "device",
        eyebrow: port.container ? "Container" : "Port",
        title: port.name,
        hint: port.container ?? undefined,
        mono: true,
        device: port.name,
      })
    }
  } else if (link.container) {
    away.push({
      id: "container",
      kind: "namespace",
      eyebrow: "Container",
      title: link.container,
      hint: link.dockerNetwork ? `on ${link.dockerNetwork}` : undefined,
      namespace: { kind: "container", name: link.container },
    })
  } else if (link.peer || link.peerNamespace) {
    away.push({
      id: "peer",
      kind: "namespace",
      eyebrow: "Other end",
      title: link.peer ?? link.peerNamespace ?? "",
      hint: link.peerNamespace ? `inside ${link.peerNamespace}` : undefined,
      mono: true,
      namespace: link.peerNamespace ? { kind: "named", name: link.peerNamespace } : undefined,
    })
  } else if (link.kind === "wireguard" || link.owner === "tailscale") {
    away.push({
      id: "peers",
      kind: "peers",
      eyebrow: link.owner === "tailscale" ? "Tailscale" : "WireGuard",
      title: link.owner === "tailscale" ? "The tailnet" : "Its peers",
      hint: "on the VPN page",
      href: "/network/vpn",
    })
  } else if (link.remote || link.local) {
    away.push({
      id: "remote",
      kind: "remote",
      eyebrow: link.remote && !unicast(link.remote) ? "Multicast group" : "Remote end",
      title: `${link.remote ?? "any address"}${link.remote && link.port ? `:${link.port}` : ""}`,
      hint: `from ${link.local ?? "any address"}`,
      mono: true,
    })
    for (const end of link.remotes ?? []) {
      away.push({
        id: `end:${end}`,
        kind: "remote",
        eyebrow: "Also floods to",
        title: end,
        mono: true,
      })
    }
  } else if (link.parent) {
    away.push({
      id: "parent",
      kind: "device",
      eyebrow: link.vlanId ? `Tag ${link.vlanId} on` : "Rides on",
      title: link.parent,
      mono: true,
      device: links.some((l) => l.name === link.parent) ? link.parent : undefined,
    })
  } else if (link.role === "uplink") {
    away.push({
      id: "internet",
      kind: "internet",
      eyebrow: "Default route",
      title: "The internet",
      hint: speed,
    })
  } else if (link.role === "loopback") {
    away.push({
      id: "self",
      kind: "self",
      eyebrow: "Loopback",
      title: "This machine",
      hint: "its own traffic",
    })
  } else if (link.role === "physical") {
    away.push({
      id: "wire",
      kind: "wire",
      eyebrow: "Cable",
      title: link.carrier ? "Its network" : "Nothing plugged in",
      hint: speed,
    })
  }

  if (link.master && link.role !== "bridge") {
    here.push({
      id: "master",
      kind: "device",
      eyebrow: "Port of",
      title: link.master,
      hint: link.dockerNetwork ? `Docker network ${link.dockerNetwork}` : undefined,
      mono: true,
      device: links.some((l) => l.name === link.master) ? link.master : undefined,
    })
  }
  for (const child of links.filter((l) => l.parent === link.name)) {
    here.push({
      id: `child:${child.name}`,
      kind: "device",
      eyebrow: child.vlanId ? `VLAN ${child.vlanId}` : "Rides on it",
      title: child.name,
      mono: true,
      device: child.name,
    })
  }
  const routable = link.addresses.filter((a) => a.scope !== "link")
  const first = routable[0] ?? link.addresses[0]
  if (first) {
    const rest = link.addresses.length - 1
    here.push({
      id: "address",
      kind: "address",
      eyebrow: "Answers at",
      title: first.cidr,
      hint: rest > 0 ? `and ${rest} more` : first.public ? "public" : undefined,
      mono: true,
      tab: "addresses",
    })
  }
  return { away, here }
}

/** What each audited change to a device is called in its history. */
const CHANGE_WORDS: Record<string, string> = {
  "network.link.create": "Made",
  "network.link.delete": "Deleted",
  "network.link.up": "Set up",
  "network.link.down": "Set down",
  "network.link.mtu": "MTU changed",
  "network.link.master": "Bridge membership changed",
  "network.link.vlans": "VLANs changed",
  "network.link.remotes": "Flood ends changed",
  "network.address.add": "Address added",
  "network.address.remove": "Address removed",
  "network.native.edit": "Native profile applied",
}

/** The audit families a device's history is read from. */
export const CHANGE_FAMILIES = ["network.link.", "network.address.", "network.native."]

function detailOf(entry: Pick<AuditEntry, "detail">): Record<string, unknown> {
  try {
    const parsed: unknown = JSON.parse(entry.detail || "{}")
    return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : {}
  } catch {
    return {}
  }
}

/** One change in words, with what it was changed to where the trail kept it. */
export function changeWords(entry: Pick<AuditEntry, "action" | "detail">): {
  title: string
  value?: string
} {
  const title = CHANGE_WORDS[entry.action] ?? entry.action.replace(/^network\./, "")
  const detail = detailOf(entry)
  const text = (key: string) =>
    typeof detail[key] === "string" || typeof detail[key] === "number"
      ? String(detail[key])
      : undefined
  switch (entry.action) {
    case "network.link.create":
      return { title, value: text("kind") }
    case "network.link.mtu":
      return { title, value: text("mtu") }
    case "network.link.master":
      return { title, value: text("master") || "no bridge" }
    case "network.address.add":
    case "network.address.remove":
      return { title, value: text("cidr") }
    case "network.link.remotes":
      return {
        title,
        value: Array.isArray(detail.remotes) ? detail.remotes.join(", ") || "none" : undefined,
      }
    default:
      return { title }
  }
}

/** The changes made to one device, newest first, from the families' entries. */
export function deviceChanges<T extends Pick<AuditEntry, "action" | "target" | "ts">>(
  entries: T[] | undefined,
  name: string,
): T[] {
  return (Array.isArray(entries) ? entries : [])
    .filter(
      (entry) =>
        entry.target === name && CHANGE_FAMILIES.some((family) => entry.action.startsWith(family)),
    )
    .sort((a, b) => new Date(b.ts).getTime() - new Date(a.ts).getTime())
}
