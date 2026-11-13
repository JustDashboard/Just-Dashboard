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
import { ProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
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

const SIZES = {
  // The identity line's tile, the row's and the rail's, with the state dot
  // scaled to each.
  lg: { tile: "size-12 rounded-xl [&_svg]:size-6", ring: "size-4", dot: undefined },
  md: { tile: "size-9 rounded-lg [&_svg]:size-4", ring: "size-3", dot: "size-2" },
  sm: { tile: "size-7 rounded-md [&_svg]:size-3.5", ring: "size-2.5", dot: "size-1.5" },
} as const

/**
 * A thing's tile with its state in the corner, as a process's is: the glyph
 * says what kind of thing it is, the dot whether it is still working.
 */
export function StateTile({
  glyph,
  tone,
  size = "md",
  className,
}: {
  glyph: Icon
  tone: DotTone
  size?: keyof typeof SIZES
  className?: string
}) {
  const s = SIZES[size]
  return (
    <span className={cn("relative flex shrink-0", className)}>
      <ProductLogo fallback={glyph} className={s.tile} />
      <span
        aria-hidden
        className={cn(
          "absolute -right-0.5 -bottom-0.5 flex items-center justify-center rounded-full bg-background",
          s.ring,
        )}
      >
        <StatusDot tone={tone} className={s.dot} />
      </span>
    </span>
  )
}
