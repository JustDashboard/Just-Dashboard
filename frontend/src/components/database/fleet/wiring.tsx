"use client"

import { Box, CloudUpload, Globe } from "@/components/icons"
import type { DbTopoEdge, DbTopoNode } from "@/lib/types"
import { cn } from "@/lib/utils"
import { WireHost, WireMark } from "@/components/deploy/wire"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { ProjectFavicon } from "@/components/deploy/project-mark"
import type { Verdict } from "@/components/status-dot"
import { edgeRank } from "@/components/database/fleet/fleet"

/**
 * The words and marks a picture of what feeds what is drawn with: the legend
 * of its lines, a reader drawn as itself, and what a link's state is called.
 * A database's own page draws its readers with them (`home/picture.tsx`), and
 * the replication and performance pictures use the legend's line. The
 * section-wide picture of every database is the map's canvas (`map.tsx`).
 */

/** What the lines mean, in one line beside the map. */
export function WiringLegend({ className }: { className?: string }) {
  return (
    <ul
      aria-label="How to read the map"
      className={cn(
        "flex flex-wrap items-center gap-x-5 gap-y-1.5 text-hint text-muted-foreground",
        className,
      )}
    >
      <LegendLine kind="live">carrying sessions</LegendLine>
      <LegendLine kind="still">declared, idle</LegendLine>
      <LegendLine kind="dashed">could reach it</LegendLine>
      <LegendLine kind="warning">link stale</LegendLine>
      <LegendLine kind="danger">link broken</LegendLine>
    </ul>
  )
}

/** One line of a legend: how a wire is drawn, and what that says. */
export function LegendLine({
  kind,
  children,
}: {
  kind: "live" | "still" | "dashed" | "warning" | "danger"
  children: React.ReactNode
}) {
  return (
    <li className="flex items-center gap-2">
      <svg aria-hidden width="28" height="6" viewBox="0 0 28 6" className="shrink-0">
        <path
          d="M 1,3 H 27"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeDasharray={kind === "dashed" ? "2 5" : undefined}
          className={cn(
            kind === "live" && "stroke-signal",
            kind === "still" && "stroke-border-strong",
            kind === "dashed" && "stroke-border-strong",
            kind === "warning" && "stroke-warning",
            kind === "danger" && "stroke-destructive",
          )}
        />
      </svg>
      <span>{children}</span>
    </li>
  )
}

/** A reader drawn as itself: this server, the product it runs, or its kind. */
export function ConsumerMark({
  node,
  worst,
  size,
}: {
  node: DbTopoNode
  worst: string
  size: "lg" | "md" | "sm"
}) {
  if (node.kind === "host") return <WireHost size={size} />
  const wrong =
    edgeRank(worst) >= 3 || node.status === "exited" || node.status === "dead"
      ? "danger"
      : edgeRank(worst) === 2
        ? "warning"
        : undefined
  const product = node.product && hasProductLogo(node.product) ? node.product : undefined
  return (
    <WireMark
      tone={wrong ?? (product || node.projectId ? "logo" : "neutral")}
      size={size}
      shape="square"
    >
      <ProjectFavicon
        projectId={node.projectId}
        fallback={product ? <ProductGlyph id={product} /> : <KindGlyph node={node} />}
      />
    </WireMark>
  )
}

function KindGlyph({ node }: { node: DbTopoNode }) {
  switch (node.kind) {
    case "deployment":
      return <CloudUpload />
    case "container":
      return <Box />
    default:
      return <Globe />
  }
}

export function consumerKind(node: DbTopoNode) {
  switch (node.kind) {
    case "deployment":
      return "Deployment"
    case "container":
      return "Container"
    case "host":
      return "Processes here"
    case "remote":
      return "Another machine"
    default:
      return ""
  }
}

export function beamTone(edge: DbTopoEdge): "default" | "warning" | "danger" {
  if (edgeRank(edge.status) >= 3) return "danger"
  if (edgeRank(edge.status) >= 1) return "warning"
  return "default"
}

export function consumerVerdict(worst: string, status: string | undefined): Verdict {
  if (edgeRank(worst) >= 3 || status === "exited" || status === "dead") return "critical"
  if (edgeRank(worst) === 2 || status === "restarting" || status === "paused") return "warning"
  return "ok"
}

export function consumerWord(worst: string, status: string | undefined) {
  if (edgeRank(worst) >= 3) return "link broken"
  if (edgeRank(worst) === 2) return "link stale"
  return status ?? "linked"
}
