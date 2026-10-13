"use client"

import { WireMark } from "@/components/deploy/wire"
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
      {reach && (
        <span className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-full bg-background">
          <StatusDot tone={reachTone(reach)} live={reach.kind === "live"} className="size-2" />
        </span>
      )}
    </span>
  )
}

/** The key that finds a computer on a bar or a list, in the hue its card carries. */
export function HueKey({ color }: { color: string }) {
  return (
    <span
      aria-hidden
      data-slot="hue-key"
      className="h-2.5 w-0.75 shrink-0 rounded-full"
      style={{ background: color }}
    />
  )
}
