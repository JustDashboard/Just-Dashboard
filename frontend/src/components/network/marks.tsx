"use client"

import {
  Box,
  Bridge,
  Connection,
  Hash,
  Linked,
  LockClosed,
  NetworkDevice,
  RotateClockwise,
  Servers,
  type Icon,
} from "@/components/icons"
import type { NetworkLink } from "@/lib/types"
import { ProductLogo, hasProductLogo, imageProduct } from "@/components/product-logo"

/**
 * What the Network pages draw a device as (§14): the product that made it
 * where one did — Docker's bridge, Tailscale's tunnel, WireGuard's, and a
 * container's veth as the product the container runs — and otherwise a glyph
 * for its kind. The kernel's own word for the kind is the source, never the
 * name: `lan0` can be a bridge and `wg-office` is a WireGuard tunnel.
 */
export function linkProduct(link: Pick<NetworkLink, "kind" | "owner" | "containerImage">) {
  if (link.containerImage) {
    const product = imageProduct(link.containerImage)
    if (hasProductLogo(product)) return product
  }
  if (link.kind === "wireguard") return "wireguard"
  if (link.owner === "tailscale") return "tailscale"
  if (link.owner === "docker") return "docker"
  return undefined
}

/** A device's glyph where no product names it. A table, not a choice made in render. */
const KIND_GLYPH: Record<string, Icon> = {
  physical: NetworkDevice,
  bond: NetworkDevice,
  loopback: RotateClockwise,
  bridge: Bridge,
  vlan: Hash,
  macvlan: Hash,
  ipvlan: Hash,
  vxlan: Linked,
  gre: Linked,
  gretap: Linked,
  ip6gre: Linked,
  ip6gretap: Linked,
  ipip: Linked,
  sit: Linked,
  geneve: Linked,
  tun: LockClosed,
  tap: LockClosed,
  wireguard: LockClosed,
  veth: Box,
  dummy: Servers,
}

export function linkGlyph(kind: string): Icon {
  return KIND_GLYPH[kind] ?? Connection
}

/** A device's kind as a bare glyph, for inside a `WireMark` or a line of text. */
export function LinkGlyph({ kind, className }: { kind: string; className?: string }) {
  const Glyph = KIND_GLYPH[kind] ?? Connection
  return <Glyph aria-hidden className={className} />
}

/** A device as what made it, else as its kind, on the tile every product takes. */
export function LinkMark({
  link,
  size = "sm",
  className,
}: {
  link: Pick<NetworkLink, "kind" | "owner" | "containerImage">
  size?: "sm" | "md"
  className?: string
}) {
  return (
    <ProductLogo
      id={linkProduct(link)}
      size={size}
      fallback={linkGlyph(link.kind)}
      className={className}
    />
  )
}

/** What a device is for, as a word. */
export const ROLE_LABEL: Record<NetworkLink["role"], string> = {
  uplink: "Uplink",
  physical: "Network card",
  tunnel: "Tunnel",
  vlan: "VLAN",
  bridge: "Bridge",
  virtual: "Virtual",
  container: "Container",
  loopback: "Loopback",
}

/**
 * Each role's hue, wherever a device is keyed by what it is for: the topology's
 * lanes, a device row's edge, a traffic series. From the lane palette, so no
 * device reads as a state (§3) — an uplink in green would say "healthy".
 */
export const ROLE_HUE: Record<NetworkLink["role"], string> = {
  uplink: "var(--tag-blue)",
  physical: "var(--tag-slate)",
  tunnel: "var(--tag-violet)",
  vlan: "var(--tag-pink)",
  bridge: "var(--tag-cyan)",
  virtual: "var(--tag-slate)",
  container: "var(--tag-green)",
  loopback: "var(--tag-slate)",
}

/** Who manages a device, as the sentence a row's second line carries. */
export const OWNER_LABEL: Record<NetworkLink["owner"], string> = {
  kernel: "the kernel",
  system: "this system's network configuration",
  docker: "Docker",
  tailscale: "Tailscale",
  wireguard: "WireGuard",
  libvirt: "libvirt",
  lxd: "LXD",
  "just-dashboard": "Just Dashboard",
}

/** The kind as a person says it: "WireGuard", "VLAN 100", "VXLAN 42". */
export function kindLabel(link: Pick<NetworkLink, "kind" | "vlanId" | "vni">) {
  switch (link.kind) {
    case "physical":
      return "Ethernet"
    case "loopback":
      return "Loopback"
    case "wireguard":
      return "WireGuard"
    case "tun":
      return "TUN"
    case "vlan":
      return link.vlanId ? `VLAN ${link.vlanId}` : "VLAN"
    case "vxlan":
      return link.vni ? `VXLAN ${link.vni}` : "VXLAN"
    case "gre":
    case "gretap":
    case "ip6gre":
    case "ip6gretap":
      return link.kind.toUpperCase().replace("TAP", " tap").replace("IP6", "IPv6 ")
    case "veth":
      return "veth"
    default:
      return link.kind
  }
}

/** A device that is moving bytes, by the threshold every Network page uses: a kilobyte a second. */
export function carrying(link: Pick<NetworkLink, "rxRate" | "txRate">) {
  return link.rxRate + link.txRate >= 1024
}

/**
 * How long a pulse takes to cross a wire for a device's throughput: a second
 * and a half at a hundred megabytes a second, four seconds near idle, so a
 * busy link is seen to be busier before a figure is read. Logarithmic, since
 * the range is eight orders of magnitude and a linear one would leave every
 * wire but the uplink's looking idle.
 */
export function pulseDuration(bytesPerSecond: number) {
  if (bytesPerSecond <= 1024) return 4
  const scale = Math.min(Math.log10(bytesPerSecond / 1024) / 5, 1)
  return Number((4 - scale * 2.5).toFixed(2))
}
