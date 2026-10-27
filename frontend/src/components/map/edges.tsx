"use client"

import { memo, useState } from "react"
import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  Position,
  type Edge,
  type EdgeProps,
} from "@xyflow/react"
import type { BranchKey } from "@/lib/server-map"
import { cn } from "@/lib/utils"

export type BranchEdgeData = {
  lit?: boolean
  dimmed?: boolean
  tier: "branch" | "shelf" | "away"
  /** The branch the line is on, which names its hue. */
  branch?: BranchKey
  /** The server's readings are streaming: a branch's line carries a pulse out from it. */
  live?: boolean
}

/**
 * Until when a line drawn for the first time draws itself out rather than
 * appearing: the map's arrival (`settleLines`). A line mounted after — panned
 * into view, which mounts what was off screen — is simply there, because it
 * was there all along.
 */
let drawingUntil = 0

/** The map has arrived: the lines it draws in the next moment draw themselves out. */
export function settleLines(ms = 1400) {
  drawingUntil = performance.now() + ms
}

const WIDTH = { branch: 2.25, shelf: 1.5, away: 1.5 }
const OPACITY = { branch: 0.9, shelf: 0.55, away: 0.8 }

/**
 * A line of the tree: the server to a branch, a branch to a shelf, a shelf
 * to a thing pulled off it, in the branch's hue — the line from the server
 * running out of the brand into it — so a branch is read as one colour from
 * the heart to its last shelf. It leaves level and arrives level, the node
 * editor's `shape="s"`, so a column of shelves reads as one fan rather than as
 * strings pulled tight. A thing off its shelf hangs from a dashed line: it is
 * where the reader put it, not where it belongs.
 *
 * The lines draw themselves out from the server as the map arrives, and
 * while the server's readings stream in, each branch's line carries a pulse
 * out from the heart (§11: *arrived* once, *live* while it is true).
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
  const tier = data?.tier ?? "shelf"
  const [drawing] = useState(() => tier !== "away" && performance.now() < drawingUntil)
  const [path] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    curvature: 0.35,
  })
  const gradient = tier === "branch" ? `map-line-${id}` : undefined
  const width = WIDTH[tier] + (data?.lit ? 0.75 : 0)
  return (
    <g
      data-branch={data?.branch}
      className={cn("transition-opacity", data?.dimmed && "opacity-30")}
    >
      {gradient && (
        <defs>
          <linearGradient
            id={gradient}
            gradientUnits="userSpaceOnUse"
            x1={sourceX}
            y1={sourceY}
            x2={targetX}
            y2={targetY}
          >
            <stop offset="0" style={{ stopColor: "var(--brand)" }} />
            <stop offset="0.55" style={{ stopColor: "var(--hue, var(--brand))" }} />
          </linearGradient>
        </defs>
      )}
      <BaseEdge
        id={id}
        path={path}
        pathLength={drawing ? 1 : undefined}
        className="transition-[stroke-opacity]"
        style={{
          stroke: gradient ? `url(#${gradient})` : "var(--hue, var(--color-border-strong))",
          strokeOpacity: data?.lit ? 1 : OPACITY[tier],
          strokeWidth: width,
          strokeLinecap: "round",
          strokeDasharray: tier === "away" ? "5 5" : drawing ? 1 : undefined,
          animation: drawing ? "jd-map-draw 0.9s cubic-bezier(0.16, 1, 0.3, 1) both" : undefined,
        }}
      />
      {tier === "branch" && data?.live && (
        <path
          d={path}
          pathLength={1}
          fill="none"
          aria-hidden
          data-slot="map-pulse"
          className="pointer-events-none"
          style={{
            stroke: "var(--hue, var(--brand))",
            strokeWidth: width + 1.5,
            strokeLinecap: "round",
            strokeDasharray: "0.16 1.2",
            strokeDashoffset: 0.16,
            animation: "jd-map-pulse 3.2s cubic-bezier(0.45, 0, 0.25, 1) 0.9s infinite",
          }}
        />
      )}
    </g>
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
            className="nopan pointer-events-none absolute"
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              // Above every box, chosen ones included: a label under a box
              // says nothing.
              zIndex: 1001,
            }}
          >
            {/* Held to the screen as the names are, from its middle on the line. */}
            <span className="block [transform:scale(max(1,calc(var(--map-unzoom,1)*0.8)))] rounded-sm border border-hairline bg-popover px-1.5 py-0.5 text-micro whitespace-nowrap text-muted-foreground">
              {data.label}
            </span>
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
