"use client"

import { memo } from "react"
import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  type Edge,
  type EdgeProps,
} from "@xyflow/react"
import { cn } from "@/lib/utils"

export type WireData = {
  via: string
  also: string[]
  shape: "flow" | "sub" | "down"
  lit: boolean
  dimmed: boolean
  /** Both ends are up, so the line carries: a pulse travels it while it is lit. */
  carries: boolean
}

/**
 * A line between two things, the way a workflow editor draws one: out of the
 * right side of one box and into the left of the next, a curve that leaves and
 * arrives level, in the border's grey. What the chosen box is wired to is the
 * brand's blue with how the server knows it written on it, and where both
 * ends are up a pulse travels it — the wiring pictures' sign that a line
 * carries (design system §2). What hangs under a box hangs by a dashed line.
 */
export const Wire = memo(function Wire({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps<Edge<WireData>>) {
  const sub = data?.shape === "sub"
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    curvature: sub ? 0.2 : 0.32,
  })
  const lit = data?.lit ?? false
  const label = data ? (VIA_LABEL[data.via] ?? data.via) : ""
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        className={cn("transition-[stroke,opacity] duration-150", data?.dimmed && "opacity-25")}
        style={{
          stroke: lit
            ? "var(--color-brand)"
            : sub
              ? "var(--color-muted-foreground)"
              : "var(--color-border-strong)",
          strokeWidth: lit ? 2 : sub ? 1.25 : 1.75,
          strokeDasharray: sub ? "4 4" : undefined,
          strokeOpacity: sub && !lit ? 0.6 : 1,
        }}
      />
      {lit && data?.carries && !sub && (
        <circle r={3} fill="var(--color-signal)" className="map-pulse">
          <animateMotion dur="1.8s" repeatCount="indefinite" path={path} />
        </circle>
      )}
      {lit && !sub && label && (
        <EdgeLabelRenderer>
          <span
            className="nopan pointer-events-none absolute"
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              zIndex: 1001,
            }}
          >
            <span
              title={
                data?.also.length
                  ? `Also: ${data.also.map((v) => VIA_LABEL[v] ?? v).join(", ")}`
                  : undefined
              }
              className="block rounded-sm border border-hairline bg-popover px-1.5 py-0.5 text-micro whitespace-nowrap text-muted-foreground"
            >
              {label}
            </span>
          </span>
        </EdgeLabelRenderer>
      )}
    </>
  )
})

export const edgeTypes = { wire: Wire }

/** How a link is known, in the words the inspector and the line both use. */
export const VIA_LABEL: Record<string, string> = {
  route: "routes to",
  release: "runs",
  member: "in the stack",
  binding: "linked database",
  mount: "mounts",
  certificate: "certificate",
  backup: "backed up by",
  serves: "serves",
  reference: "refers to",
  env: "names in env",
  session: "connected to",
  dependency: "depends on",
  source: "built from",
  workdir: "runs from",
  pooler: "pools for",
  self: "runs as",
  tailnet: "reached over",
  logs: "logs",
}
