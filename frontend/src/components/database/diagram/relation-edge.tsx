"use client"

import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from "@xyflow/react"
import { cn } from "@/lib/utils"
import type { DbGraphEdge } from "@/lib/types"

/**
 * A foreign key, drawn between the two columns it relates.
 *
 * Smooth-step rather than a bezier: a bezier between two rows of a table looks
 * like a wire and gives no sense of direction, while an orthogonal path with
 * rounded-sm corners reads as a route from one row to another — which is what
 * schema tools have always drawn and what makes a dense diagram followable.
 *
 * The label is the cardinality, in crow's-foot terms rather than jargon, and it
 * appears only when the edge is involved in whatever is focused. A diagram with
 * a label on every line is a diagram nobody can read.
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
  const relation = (data as { relation?: DbGraphEdge; active?: boolean; dimmed?: boolean }) ?? {}
  const rel = relation.relation
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
        className={cn(
          "transition-opacity duration-200",
          relation.dimmed ? "opacity-10" : "opacity-100",
        )}
        style={{
          stroke: relation.active ? "var(--color-chart-1)" : "var(--color-muted-foreground)",
          strokeWidth: relation.active ? 2 : 1.25,
          strokeOpacity: relation.active ? 1 : 0.4,
        }}
      />
      {relation.active && rel && (
        <EdgeLabelRenderer>
          <div
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
            }}
            className="pointer-events-none absolute rounded-sm border bg-card px-1.5 py-0.5 font-mono text-micro text-muted-foreground shadow-sm"
          >
            {rel.cardinality === "one-to-one" ? "1 — 1" : "n — 1"}
            {rel.onDelete && rel.onDelete !== "NO ACTION" && (
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
