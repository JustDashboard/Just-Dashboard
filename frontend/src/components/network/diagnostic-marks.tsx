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
import { ProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
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

const SIZES = {
  // The identity line's tile, the row's and the rail's, with the state dot
  // scaled to each.
  lg: {
    tile: "size-12 rounded-xl [&_svg]:size-6",
    logo: "[&_img]:size-7",
    ring: "size-4",
    dot: undefined,
  },
  md: {
    tile: "size-9 rounded-lg [&_svg]:size-4",
    logo: "[&_img]:size-5",
    ring: "size-3",
    dot: "size-2",
  },
  sm: {
    tile: "size-7 rounded-md [&_svg]:size-3.5",
    logo: "[&_img]:size-4",
    ring: "size-2.5",
    dot: "size-1.5",
  },
} as const

/**
 * A thing's tile with its state in the corner, as a process's is: the glyph
 * says what kind of thing it is, the dot whether it is still working.
 */
export function StateTile({
  glyph,
  product,
  tone,
  size = "md",
  className,
}: {
  glyph: Icon
  /** The product the thing is, where one made it: its logo stands in the glyph's place. */
  product?: string
  tone: DotTone
  size?: keyof typeof SIZES
  className?: string
}) {
  const s = SIZES[size]
  return (
    <span className={cn("relative flex shrink-0", className)}>
      <ProductLogo id={product} fallback={glyph} className={cn(s.tile, s.logo)} />
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

const WORD_TONE: Record<DotTone, string> = {
  running: "text-foreground",
  stopped: "text-muted-foreground",
  warning: "text-warning",
  danger: "text-destructive",
  notice: "text-muted-foreground",
  unknown: "text-muted-foreground",
}

/**
 * A row's state as a word at the head of its second line, for a phone: the
 * row's end has no room for it there, and the state is what the list is read
 * for. From `sm` the word stands in its own column and this is not drawn.
 */
export function PhoneState({ tone, children }: { tone: DotTone; children: React.ReactNode }) {
  return (
    <span className={cn("font-sans sm:hidden", WORD_TONE[tone])}>
      {children}
      <span className="text-muted-foreground"> · </span>
    </span>
  )
}
