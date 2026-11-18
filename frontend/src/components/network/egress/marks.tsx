"use client"

import type { EgressGroup, EgressMemberView } from "@/lib/network/egress"
import type { NetworkLink } from "@/lib/types"
import { Lifebuoy, LockClosed, Router } from "@/components/icons"
import { ProductGlyph, StateMark } from "@/components/product-logo"
import { TEXT_TONE, TONE_DOT, type DotTone, type Tone } from "@/components/tone"
import { linkProduct } from "@/components/network/marks"
import { egressCarrying } from "@/components/network/egress/reading"

/** @deprecated use `TONE_DOT` from `components/tone`. */
export const DOT: Record<Tone, DotTone> = TONE_DOT

/**
 * A carrying state's word colour: the status dot's own, so a group that is
 * carrying is plain text, as every running thing is, and only the ones to act
 * on take a hue.
 */
export const TONE_TEXT: Record<Tone, string> = {
  default: TEXT_TONE[TONE_DOT.default],
  success: TEXT_TONE[TONE_DOT.success],
  warning: TEXT_TONE[TONE_DOT.warning],
  danger: TEXT_TONE[TONE_DOT.danger],
}

/**
 * A group as a tile: the section's own glyph, with its carrying state in the corner
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
  return (
    <StateMark
      glyph={Lifebuoy}
      tone={TONE_DOT[egressCarrying(group).tone]}
      size={size}
      className={className}
    />
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
