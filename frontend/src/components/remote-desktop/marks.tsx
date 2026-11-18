"use client"

import { WireMark } from "@/components/wire/wire"
import { ProductGlyph } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { osProduct, type Reachability, type RdOS } from "@/lib/remote-desktop"
import { cn } from "@/lib/utils"

/** The dot a computer's state is drawn with, in the status vocabulary the rest of the app reads. */
export function reachTone(reach: Reachability): DotTone {
  if (reach.tone === "success") return "running"
  if (reach.tone === "warning") return "warning"
  if (reach.tone === "danger") return "danger"
  return "stopped"
}

/**
 * A computer drawn as its system: the Windows, Apple or Tux mark on the
 * picture's own tile, with its state in the corner the way an engine's sits
 * on the proxy's picture.
 */
export function ComputerMark({
  os,
  reach,
  size = "md",
  className,
}: {
  os: RdOS
  reach?: Reachability
  size?: "lg" | "md" | "sm"
  className?: string
}) {
  return (
    <span className={cn("relative flex shrink-0", className)}>
      <WireMark tone="logo" size={size}>
        <ProductGlyph id={osProduct(os)} />
      </WireMark>
      {reach && <StatusDot corner="lg" tone={reachTone(reach)} live={reach.kind === "live"} />}
    </span>
  )
}
