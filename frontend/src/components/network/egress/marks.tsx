"use client"

import type { EgressGroup, EgressMemberView } from "@/lib/network-egress"
import type { NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"
import { LockClosed, Route, Router } from "@/components/icons"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import type { Tone } from "@/components/tone"
import { linkProduct } from "@/components/network/marks"
import { egressCarrying } from "@/components/network/egress/reading"

export const DOT: Record<Tone, DotTone> = {
  default: "stopped",
  success: "running",
  warning: "warning",
  danger: "danger",
}

export const TONE_TEXT: Record<Tone, string> = {
  default: "text-muted-foreground",
  success: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
}

/**
 * A group as a tile: the route glyph, with its carrying state in the corner
 * the way a unit's is in the services rail, so a list of groups reads green,
 * grey and red before a name is read.
 */
export function EgressMark({
  group,
  size = "md",
  className,
}: {
  group: EgressGroup
  size?: "sm" | "md" | "lg"
  className?: string
}) {
  const carrying = egressCarrying(group)
  return (
    <span className={cn("relative flex shrink-0", className)}>
      <ProductLogo
        fallback={Route}
        className={cn(
          size === "lg" && "size-10 rounded-lg [&_svg]:size-5",
          size === "md" && "size-8 rounded-lg [&_svg]:size-4",
          size === "sm" && "size-7 rounded-md [&_svg]:size-3.5",
        )}
      />
      <span
        aria-hidden
        className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
      >
        <StatusDot tone={DOT[carrying.tone]} className="size-1.5" />
      </span>
    </span>
  )
}

/**
 * A member as what carries it: the tunnel's product where the device is one
 * this host knows (WireGuard, Tailscale), else a router for a gateway and a
 * lock for a tunnel.
 */
export function MemberGlyph({
  member,
  links,
  className,
}: {
  member: Pick<EgressMemberView, "kind" | "device">
  links: NetworkLink[]
  className?: string
}) {
  const link = links.find((l) => l.name === member.device)
  const product = link ? linkProduct(link) : undefined
  if (product) return <ProductGlyph id={product} className={className} />
  const Glyph = member.kind === "tunnel" ? LockClosed : Router
  return <Glyph aria-hidden className={className} />
}
