"use client"

import { useId, useMemo } from "react"
import { cn } from "@/lib/utils"

/**
 * A trend in the width of a table cell.
 *
 * Drawn as a bare SVG path rather than through recharts: a chart library
 * mounts a responsive container, a resize observer and a tooltip layer per
 * instance, and a table of forty containers would pay for forty of those to
 * draw forty polylines. This is one path element.
 *
 * It carries no axis, no labels and no tooltip on purpose. A sparkline is not
 * a chart you read values off — it is there to answer "did anything happen
 * here", so that the one row that spiked can be clicked through to the real
 * charts. Giving it ticks would invite it to be read as something it is too
 * small to be.
 */
export function Sparkline({
  values,
  /** Fixes the vertical scale. Omit to scale each line to its own maximum. */
  max,
  color = "var(--chart-1)",
  width = 64,
  height = 20,
  className,
  label,
}: {
  values: number[]
  max?: number
  color?: string
  width?: number
  height?: number
  className?: string
  label?: string
}) {
  const gradientId = useId()

  const path = useMemo(() => buildPath(values, max, width, height), [values, max, width, height])

  if (!path) {
    return (
      <span
        className={cn("inline-block align-middle text-hint text-muted-foreground", className)}
        style={{ width, height }}
      />
    )
  }

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      width={width}
      height={height}
      preserveAspectRatio="none"
      className={cn("inline-block overflow-visible align-middle", className)}
      role="img"
      aria-label={label}
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity={0.35} />
          <stop offset="100%" stopColor={color} stopOpacity={0} />
        </linearGradient>
      </defs>
      <path d={path.fill} fill={`url(#${gradientId})`} />
      <path
        d={path.line}
        fill="none"
        stroke={color}
        strokeWidth={1.25}
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}

function buildPath(
  values: number[],
  max: number | undefined,
  width: number,
  height: number,
): { line: string; fill: string } | null {
  if (values.length === 0) return null
  if (values.length === 1) values = [values[0], values[0]]

  // A floor on the ceiling: without one, a container that idled between 0.1%
  // and 0.3% is drawn as a dramatic mountain range, and a table of those is
  // unreadable noise where every row looks like an incident.
  const ceiling = Math.max(max ?? Math.max(...values), 1)
  // One pixel of inset top and bottom so a flat line at zero is still visible
  // and a line at the maximum is not clipped by the viewbox edge.
  const usable = height - 2
  const step = width / (values.length - 1)

  const points = values.map((value, i) => {
    const x = i * step
    const y = 1 + usable - (Math.min(Math.max(value, 0), ceiling) / ceiling) * usable
    return `${x.toFixed(2)},${y.toFixed(2)}`
  })

  const line = `M${points.join("L")}`
  const fill = `${line}L${width.toFixed(2)},${height}L0,${height}Z`
  return { line, fill }
}
