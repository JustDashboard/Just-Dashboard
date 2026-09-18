"use client"

import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from "@xyflow/react"
import { cn } from "@/lib/utils"
import type { DbGraphEdge } from "@/lib/types"

export type RelationEdgeData = {
  relation: DbGraphEdge
  /** Part of the focused neighbourhood, or the edge the operator picked. */
  active?: boolean
  dimmed?: boolean
  selected?: boolean
  hovered?: boolean
  /** Labels on every edge, an option for a small schema. */
  labelled?: boolean
}

/**
 * A foreign key, drawn between the two columns it relates.
 *
 * Smooth-step rather than a bezier: a bezier between two rows of a table looks
 * like a wire and gives no sense of direction, while an orthogonal path with
 * rounded corners reads as a route from one row to another — which is what
 * schema tools have always drawn and what makes a dense diagram followable.
 *
 * The label is the cardinality, in crow's-foot terms rather than jargon, and it
 * appears when the edge is involved in whatever is focused, under the pointer,
 * or picked. A diagram with a label on every line is a diagram nobody can read
 * — unless the schema is small enough that the operator asks for them.
 */
export function RelationEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  markerEnd,
}: EdgeProps) {
  const d = (data ?? {}) as RelationEdgeData
  const rel = d.relation
  const lit = Boolean(d.active || d.selected || d.hovered)
  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 12,
  })

  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        interactionWidth={18}
        className={cn("transition-opacity duration-200", d.dimmed ? "opacity-10" : "opacity-100")}
        style={{
          stroke: lit ? "var(--color-chart-1)" : "var(--color-muted-foreground)",
          strokeWidth: d.selected ? 2.5 : lit ? 2 : 1.25,
          strokeOpacity: lit ? 1 : 0.45,
          strokeDasharray: rel?.cardinality === "one-to-one" ? "6 4" : undefined,
        }}
      />
      {(lit || d.labelled) && rel && !d.dimmed && (
        <EdgeLabelRenderer>
          <div
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
            className={cn(
              "pointer-events-none absolute rounded-sm border bg-card px-1.5 py-0.5 font-mono text-micro",
              lit
                ? "border-border-strong text-foreground"
                : "border-hairline text-muted-foreground",
            )}
          >
            {rel.cardinality === "one-to-one" ? "1 — 1" : "n — 1"}
            {lit && rel.onDelete && rel.onDelete !== "NO ACTION" && (
              <span className="ml-1 text-destructive/80">
                on delete {rel.onDelete.toLowerCase()}
              </span>
            )}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
}
