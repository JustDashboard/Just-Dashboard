"use client"

import { Sparkles } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { BorderBeam } from "@/components/ui/border-beam"
import { cn } from "@/lib/utils"

/**
 * Who is answering, drawn as itself: the provider's own logo on its tile, with the
 * light that says work is happening in this frame (`BorderBeam`, §11) going round it
 * while it works. It is the one thing on the page that moves on its own, so a glance
 * says whether it is still going.
 */
export function AssistantMark({
  product,
  working,
  size = "md",
  className,
}: {
  product?: string
  working?: boolean
  size?: "sm" | "md"
  className?: string
}) {
  return (
    <span
      className={cn("relative inline-flex shrink-0 rounded-[0.6rem]", className)}
      data-working={working ? "true" : undefined}
    >
      <ProductLogo id={product || undefined} size={size} fallback={Sparkles} />
      {working && <BorderBeam size={28} duration={2} borderWidth={1.5} />}
    </span>
  )
}
