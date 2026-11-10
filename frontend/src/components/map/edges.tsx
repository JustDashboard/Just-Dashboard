"use client"

import { memo } from "react"
import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  type Edge,
  type EdgeProps,
} from "@xyflow/react"
import type { MapTone } from "@/lib/types"
import { cn } from "@/lib/utils"

export type WireData = {
  via: string
  also: string[]
  shape: "flow" | "sub" | "down"
  lit: boolean
  dimmed: boolean
  /** Both ends are up, so the line carries: a pulse travels it while it is lit. */
  carries: boolean
  /** Sessions are open on it now: a pulse travels it whether it is lit or not. */
  live?: boolean
  /** Known only as a way it could be reached — a stack or a network shared — and drawn dashed. */
  maybe?: boolean
  /** A link that broke or went stale, in its colour while it is not lit. */
  tone?: MapTone
}

/**
 * A line between two things, the way a workflow editor draws one: out of the
 * right side of one box and into the left of the next, a curve that leaves and
 * arrives level, in the border's grey. What the chosen box is wired to is the
 * brand's blue with how the server knows it written on it, and where both
 * ends are up a pulse travels it — the wiring pictures' sign that a line
 * carries (design system §2). What hangs under a box hangs by a dashed line.
 *
 * The databases map knows three things more of a line, and draws them the
 * way its picture always did: a line with sessions open on it pulses lit or
 * not, one known only as a stack or a network shared is dashed, and one that
 * broke or went stale is in the danger or the warning colour.
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
  const label = data ? wireLabel(data.via, data.tone) : ""
  const quiet = sub
    ? "var(--color-muted-foreground)"
    : data?.tone === "danger"
      ? "var(--color-destructive)"
      : data?.tone === "warning"
        ? "var(--color-warning)"
        : "var(--color-border-strong)"
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        className={cn("transition-[stroke,opacity] duration-150", data?.dimmed && "opacity-25")}
        style={{
          stroke: lit ? "var(--color-brand)" : quiet,
          strokeWidth: lit ? 2 : sub ? 1.25 : 1.75,
          strokeDasharray: sub ? "4 4" : data?.maybe ? "3 6" : undefined,
          strokeOpacity: sub && !lit ? 0.6 : 1,
        }}
      />
      {((lit && data?.carries) || data?.live) && !sub && (
        <circle
          r={3}
          fill="var(--color-signal)"
          className={cn("map-pulse", data?.dimmed && "opacity-25")}
        >
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
              className={cn(
                "block rounded-sm border border-hairline bg-popover px-1.5 py-0.5 text-micro whitespace-nowrap",
                data?.tone === "danger"
                  ? "text-destructive"
                  : data?.tone === "warning"
                    ? "text-warning"
                    : "text-muted-foreground",
              )}
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

/** A line's words: how it is known, or that it broke or went stale. */
export function wireLabel(via: string, tone?: MapTone) {
  if (tone === "danger") return "link broken"
  if (tone === "warning") return "link stale"
  return VIA_LABEL[via] ?? via
}

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
  stack: "same stack",
  network: "shares a network",
  self: "runs as",
  tailnet: "reached over",
  logs: "logs",
}
