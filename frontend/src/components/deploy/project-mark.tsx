"use client"

import { useState } from "react"
import { API_BASE } from "@/lib/api"
import type { DeploymentSummary } from "@/lib/types"
import { cn } from "@/lib/utils"
import { WorkloadMark, deploymentURL } from "@/components/deploy/vocabulary"

/**
 * The project's own mark: the icon its website declares, in the box the
 * workload glyph would otherwise fill, so a fleet of cards reads by the same
 * marks the browser's tabs do. The icon comes through the dashboard's own
 * origin — the page's image policy does not let the site be read directly —
 * and a project that has no website, no release yet or no icon keeps the
 * workload glyph.
 */
export function ProjectMark({
  deployment,
  size = "md",
  className,
}: {
  deployment: DeploymentSummary
  size?: "sm" | "md"
  className?: string
}) {
  const [missing, setMissing] = useState(false)
  const url = deploymentURL(deployment.endpoint)
  if (!url || !deployment.liveReleaseId || missing) {
    return <WorkloadMark profile={deployment.profile} size={size} className={className} />
  }
  return (
    <span
      aria-hidden="true"
      className={cn(
        "flex shrink-0 items-center justify-center overflow-hidden rounded-md border border-hairline bg-control",
        size === "sm" ? "size-8" : "size-10",
        className,
      )}
    >
      {/* Same-origin and not a Next-optimised asset: a plain element, as the avatar is. */}
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={`${API_BASE}/deploy/${deployment.id}/favicon`}
        alt=""
        loading="lazy"
        className={cn("object-contain", size === "sm" ? "size-4" : "size-5")}
        onError={() => setMissing(true)}
      />
    </span>
  )
}
