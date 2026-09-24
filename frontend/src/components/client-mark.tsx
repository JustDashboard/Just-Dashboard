"use client"

import {
  Connection,
  DesktopDevice,
  Globe,
  Router,
  Servers,
  Terminal,
  type Icon,
} from "@/components/icons"
import { networkOf, parseAgent, type NetworkKind } from "@/lib/clients"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"

/**
 * What signed this session in, as the product it is: the browser's own mark
 * on the tile, with the system it runs on as a badge in the corner — the way
 * a status sits on an avatar — so Safari on an iPhone reads as Safari first
 * and Apple second. A program is drawn as itself; a client the parser cannot
 * name keeps a glyph for its kind on the same tile, so its title lines up
 * with the rest.
 *
 * Shared by every page that describes a client — the account's sessions and
 * a deployment's requests — so the same browser is drawn the same way on both.
 */
export function ClientMark({ userAgent }: { userAgent: string }) {
  const agent = parseAgent(userAgent)
  const badge = agent.product && agent.osProduct !== agent.product ? agent.osProduct : undefined
  return (
    <BadgedLogo
      id={agent.product ?? agent.osProduct}
      badge={badge}
      fallback={agent.device === "program" ? Terminal : DesktopDevice}
    />
  )
}

/**
 * A product's tile with a second product in its corner, the way a status sits
 * on an avatar: a browser over the system it runs on, a terminal over the
 * forge it signs in to. One shape, so every page that draws "this, on that"
 * draws it the same.
 */
export function BadgedLogo({
  id,
  badge,
  fallback,
}: {
  id?: string
  badge?: string
  fallback?: Icon
}) {
  return (
    <span className="relative flex shrink-0">
      <ProductLogo id={id} size="sm" fallback={fallback} />
      {badge && (
        <span
          aria-hidden
          className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background"
        >
          <ProductGlyph id={badge} className="size-2.5" />
        </span>
      )}
    </span>
  )
}

/**
 * The drawing for where an address sits, when no product names it. Tailscale
 * has its own mark; the other three are places rather than products, so they
 * take a glyph for the place.
 */
export const NETWORK_GLYPH: Record<NetworkKind, Icon> = {
  tailscale: Connection,
  server: Servers,
  local: Router,
  internet: Globe,
}

/**
 * Where the address is — the tailnet drawn as Tailscale — and the address
 * itself. `glyph` draws the other kinds too, for a page where the network is
 * the point (a request log's visitors) rather than a detail of a session the
 * reader already recognises. `address` draws the address the way the page
 * around it does — a request log's in the address hue — so one address does
 * not change colour between a row and its own disclosure.
 */
export function NetworkFact({
  ip,
  glyph,
  address,
}: {
  ip: string
  glyph?: boolean
  address?: React.ReactNode
}) {
  const network = networkOf(ip)
  const Glyph = NETWORK_GLYPH[network.kind]
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      {network.product ? (
        <ProductGlyph id={network.product} />
      ) : (
        glyph && <Glyph aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
      )}
      <span className="shrink-0">{network.label}</span>
      {address ?? <span className="truncate font-mono text-muted-foreground/80">{ip}</span>}
    </span>
  )
}
