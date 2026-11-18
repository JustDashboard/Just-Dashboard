"use client"

import {
  ArrowUpRight,
  Connection,
  Cpu,
  Crosshair,
  Envelope,
  Fingerprint,
  Globe,
  Inspect,
  Key,
  Lightning,
  LockClosed,
  NetworkDevice,
  Route,
  Router,
  Shield,
  Wrench,
  type Icon,
} from "@/components/icons"
import { linkGlyph, linkProduct } from "@/components/network/marks"
import { TEXT_TONE, type DotTone } from "@/components/tone"
import type { NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * What a saved run is drawn as: the glyph of the question its tool asks. A
 * run is no product, so it takes a tile with a glyph (§14), and the same
 * glyph wherever the run appears — its row, the rail, its page's header.
 */
const TOOL_GLYPHS: Record<string, Icon> = {
  dns: Globe,
  dnsauth: Globe,
  ping: Route,
  traceroute: Route,
  route: Route,
  mtu: Route,
  port: Connection,
  scan: Connection,
  banner: Connection,
  ssh: Key,
  http: Globe,
  httpsec: Shield,
  tls: LockClosed,
  tlssurvey: LockClosed,
  siteaudit: Globe,
  mx: Envelope,
  starttls: LockClosed,
  dnsbl: Shield,
  asn: Fingerprint,
  whois: Fingerprint,
  capabilities: Cpu,
  capture: Crosshair,
  listeners: Router,
  egress: ArrowUpRight,
  neigh: NetworkDevice,
  wol: Lightning,
}

export function toolGlyph(run: { kind?: string; request: { tool: string } }): Icon {
  if (run.kind === "investigation") return Inspect
  return TOOL_GLYPHS[run.request.tool] ?? Wrench
}

/**
 * What a capture is drawn as: the interface it listens on, as the Network
 * pages draw a device (`network/marks.tsx`) — Docker's bridge, a WireGuard or
 * Tailscale tunnel as the product that made it, anything else as its kind. A
 * device that has gone since, or one not read yet, keeps the plain glyph.
 */
export function interfaceMark(name: string, links: NetworkLink[] | undefined) {
  const link = links?.find((row) => row.name === name)
  return {
    product: link ? linkProduct(link) : undefined,
    glyph: link ? linkGlyph(link.kind) : NetworkDevice,
  }
}

/**
 * A row's state as a word at the head of its second line, for a phone: the
 * row's end has no room for it there, and the state is what the list is read
 * for. From `sm` the word stands in its own column and this is not drawn.
 */
export function PhoneState({ tone, children }: { tone: DotTone; children: React.ReactNode }) {
  return (
    <span className={cn("font-sans sm:hidden", TEXT_TONE[tone])}>
      {children}
      <span className="text-muted-foreground"> · </span>
    </span>
  )
}
