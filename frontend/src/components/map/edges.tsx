"use client"

import { memo } from "react"
import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  Position,
  type Edge,
  type EdgeProps,
} from "@xyflow/react"
import { cn } from "@/lib/utils"

export type BranchEdgeData = { lit?: boolean; dimmed?: boolean; tier: "branch" | "shelf" | "away" }

/**
 * A line of the tree: the server to a branch, a branch to a shelf, a shelf
 * to a thing pulled off it. It is still — nothing travels along a branch of
 * a mind map — and it leaves level and arrives level, the node editor's
 * `shape="s"`, so a column of shelves reads as one fan rather than as strings
 * pulled tight. A thing off its shelf hangs from a dashed line: it is where
 * the reader put it, not where it belongs.
 */
export const BranchEdge = memo(function BranchEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps<Edge<BranchEdgeData>>) {
  const [path] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    curvature: 0.35,
  })
  return (
    <BaseEdge
      id={id}
      path={path}
      className={cn("transition-[stroke,opacity]", data?.dimmed && "opacity-30")}
      style={{
        // A thing off its shelf hangs by a line that has to be seen: it is
        // the only thing saying which shelf the box is still on.
        stroke: data?.lit
          ? "var(--color-chart-1)"
          : data?.tier === "away"
            ? "var(--color-muted-foreground)"
            : "var(--color-border-strong)",
        strokeWidth: data?.tier === "branch" ? 1.75 : data?.tier === "away" ? 1.5 : 1.25,
        strokeDasharray: data?.tier === "away" ? "5 5" : undefined,
      }}
    />
  )
})

export type RelationEdgeData = { via: string; label: string }

/**
 * What the chosen thing is wired to: a domain to its project, a project to its
 * database, a container to its volume. Drawn only for the chosen box — every
 * link at once is the hairball the tree exists to avoid — in the brand's
 * blue, with how the link is known written on it.
 */
export const RelationEdge = memo(function RelationEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps<Edge<RelationEdgeData>>) {
  const [path, labelX, labelY] =
    sourcePosition === Position.Left && targetPosition === Position.Left
      ? aroundTheLeft(sourceX, sourceY, targetX, targetY)
      : getBezierPath({
          sourceX,
          sourceY,
          targetX,
          targetY,
          sourcePosition,
          targetPosition,
          curvature: 0.25,
        })
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        className="animate-rise"
        style={{ stroke: "var(--color-chart-1)", strokeWidth: 1.5 }}
      />
      {data?.label && (
        <EdgeLabelRenderer>
          <span
            className="nopan pointer-events-none absolute rounded-sm border border-hairline bg-popover px-1.5 py-0.5 text-micro text-muted-foreground group-data-[zoom=far]/map:hidden"
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              // Above every box, chosen ones included: a label under a box
              // says nothing.
              zIndex: 1001,
            }}
          >
            {data.label}
          </span>
        </EdgeLabelRenderer>
      )}
    </>
  )
})

/**
 * Two boxes stacked in one column, joined round the column's left: a curve
 * that bows out further the further apart they are. A bezier between two
 * left-facing ends has no width to bend with and draws a straight line down
 * the column's edge, through every box between them.
 */
function aroundTheLeft(sx: number, sy: number, tx: number, ty: number): [string, number, number] {
  const bow = Math.min(160, 36 + Math.abs(ty - sy) * 0.18)
  const left = Math.min(sx, tx) - bow
  return [
    `M ${sx},${sy} C ${left},${sy} ${left},${ty} ${tx},${ty}`,
    left + bow * 0.25,
    (sy + ty) / 2,
  ]
}

export const edgeTypes = { branch: BranchEdge, relation: RelationEdge }

/** How a link is known, in the words the inspector and the line both use. */
export const VIA_LABEL: Record<string, string> = {
  route: "routes to",
  release: "release of",
  member: "in the stack",
  binding: "linked database",
  mount: "mounts",
  network: "on the network",
  certificate: "certificate for",
  backup: "backed up by",
  image: "runs the image",
  serves: "serves",
  reference: "refers to",
  env: "names in env",
  session: "connected to",
  dependency: "depends on",
  source: "built from",
  workdir: "runs from",
  starts: "starts",
  user: "runs as",
  notifies: "notifies",
  owner: "belongs to",
  access: "reaches",
  pooler: "pools for",
  self: "runs in",
}
