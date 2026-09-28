"use client"

import { useState } from "react"
import { API_BASE } from "@/lib/api"
import type { DeploymentSummary } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import {
  WORKLOAD_GLYPH,
  WorkloadMark,
  deploymentURL,
  projectProduct,
} from "@/components/deploy/vocabulary"

// ProductLogo's tile and artwork at each size, so the favicon and the product
// it falls back to are one shape.
const TILE = { sm: "size-8", md: "size-10", lg: "size-12 rounded-xl" } as const
const ART = { sm: "size-4.5", md: "size-6", lg: "size-7" } as const

/**
 * The project drawn as itself, the way a browser's tab would draw it: the icon
 * its website declares, else the product it is (`projectProduct` — the
 * template, the image, the framework detection found), else its workload's
 * glyph. All three stand on ProductLogo's tile, so a template and the project
 * it becomes are one mark and a card does not change shape when its favicon
 * arrives (§14).
 *
 * A repository's own site says what it is, and not what it runs on, so its
 * favicon carries the framework or language as a badge in the tile's corner —
 * the sessions list's browser-over-system shape. A template's or an image's
 * favicon is already its product's mark, and a badge would be the same logo
 * twice.
 *
 * The icon comes through the dashboard's own origin — the page's image policy
 * does not let the site be read directly — and only once there is a release to
 * serve it.
 *
 * `xs` is the mark bare inside a line — the rail's heading, the preview's
 * address strip — at 16px with no tile, as `ProductGlyph` draws a product,
 * and the workload's glyph in the muted ink a browser tab gives a site with
 * no icon.
 */
export function ProjectMark({
  deployment,
  product,
  size = "md",
  className,
}: {
  deployment: DeploymentSummary
  /** What the project is, when the caller has read more than the summary says. */
  product?: string
  size?: "xs" | "sm" | "md" | "lg"
  className?: string
}) {
  // Which icon failed rather than whether one did, as ProductLogo tracks its
  // file: a mark reused for another project must be allowed to load.
  const [failed, setFailed] = useState<string>()
  const url = deploymentURL(deployment.endpoint)
  const icon =
    url && deployment.liveReleaseId ? `${API_BASE}/deploy/${deployment.id}/favicon` : undefined
  const favicon = icon !== failed ? icon : undefined
  const named = product ?? projectProduct(deployment)

  if (size === "xs") {
    if (favicon)
      return (
        // Same-origin and not a Next-optimised asset: a plain element, as the avatar is.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={favicon}
          alt=""
          aria-hidden="true"
          loading="lazy"
          className={cn("size-4 shrink-0 object-contain", className)}
          onError={() => setFailed(favicon)}
        />
      )
    if (hasProductLogo(named))
      return <ProductGlyph id={named} className={cn("size-4", className)} />
    const Glyph = WORKLOAD_GLYPH[deployment.profile]
    return <Glyph aria-hidden className={cn("size-4 shrink-0 text-muted-foreground", className)} />
  }

  if (!favicon)
    return (
      <WorkloadMark
        profile={deployment.profile}
        product={named}
        size={size}
        className={className}
      />
    )

  const badge =
    (deployment.sourceKind === "git" || deployment.sourceKind === "local") && hasProductLogo(named)
      ? named
      : undefined
  const tile = (
    <span
      aria-hidden="true"
      className={cn(
        "flex shrink-0 items-center justify-center rounded-lg border border-hairline bg-background",
        TILE[size],
        !badge && className,
      )}
    >
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={favicon}
        alt=""
        loading="lazy"
        className={cn("object-contain", ART[size])}
        onError={() => setFailed(favicon)}
      />
    </span>
  )
  if (!badge) return tile
  return (
    <span aria-hidden="true" className={cn("relative flex shrink-0", className)}>
      {tile}
      <span
        className={cn(
          "absolute -right-1 -bottom-1 flex items-center justify-center rounded-sm border border-hairline bg-background",
          size === "lg" ? "size-5" : "size-4",
        )}
      >
        <ProductGlyph id={badge} className={size === "lg" ? "size-3.5" : "size-2.5"} />
      </span>
    </span>
  )
}
