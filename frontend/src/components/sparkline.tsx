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
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity={0.35} />
          <stop offset="100%" stopColor={color} stopOpacity={0} />
        </linearGradient>
      </defs>
      {path.fill && <path d={path.fill} fill={`url(#${gradientId})`} />}
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

/**
 * A reading's recent shape, in a `StatTile`'s `trend` slot or at a
 * `ReadingLine`'s end.
 *
 * The deploy tiles put 72px sparklines inside their hints or beside their
 * figures — one idea at three sizes in three places. This is the one shape:
 * the slot's full width, 36px tall, rising once when its points land
 * (§11 *arrived*). Nothing is drawn below two points, which is not yet a
 * shape.
 *
 * A series that never moves is drawn as what it is, a level line with no
 * area under it: at its level against a fixed `max`, at mid-height on a scale
 * of its own, where it has no level to sit at (on the floor when it is
 * nothing at all). It used to draw nothing, which
 * on an idle server left three of four lines with an empty slot and read as
 * broken, and against `max` it drew the area under the line, so a cache
 * steady at 99% was a grey block pinned to the top of the slot.
 *
 * `label` is the whole accessible name, window included — "CPU over the last
 * hour" — because only the caller knows the window. Colour is a series
 * colour, never a status one: a failing share is `--chart-3`, because
 * `--destructive` on a line says the line itself is an error (§3).
 */
export function TileTrend({
  values,
  label,
  color = "var(--chart-1)",
  max,
}: {
  values: number[]
  label: string
  color?: string
  /** Fixes the vertical scale — 100 for a percentage. */
  max?: number
}) {
  if (values.length < 2) return null
  return (
    <div className="h-full animate-rise">
      <Sparkline
        values={values}
        max={max}
        color={color}
        width={240}
        height={36}
        className="h-9 w-full"
        label={label}
      />
    </div>
  )
}

/** Whether every value is the same: a level line rather than a shape. */
export function isSteady(values: number[]) {
  return values.length > 0 && values.every((value) => value === values[0])
}

export function buildPath(
  values: number[],
  max: number | undefined,
  width: number,
  height: number,
): { line: string; fill?: string } | null {
  if (values.length === 0) return null
  if (values.length === 1) values = [values[0], values[0]]

  // One pixel of inset top and bottom so a flat line at zero is still visible
  // and a line at the maximum is not clipped by the viewbox edge.
  const usable = height - 2

  // A level line has no shape for an area to fill: under it, the area reads
  // as a meter at that level. On its own scale it has no level either, so it
  // runs through the middle, which says "steady" and nothing more; nothing at
  // all is a level on any scale, and stays on the floor.
  if (isSteady(values)) {
    const ceiling = max === undefined ? undefined : Math.max(max, 1)
    const level = Math.max(values[0], 0)
    const y =
      ceiling !== undefined
        ? 1 + usable - (Math.min(level, ceiling) / ceiling) * usable
        : level === 0
          ? 1 + usable
          : height / 2
    return { line: `M0,${y.toFixed(2)}L${width.toFixed(2)},${y.toFixed(2)}` }
  }

  // A floor on the ceiling: without one, a container that idled between 0.1%
  // and 0.3% is drawn as a dramatic mountain range, and a table of those is
  // unreadable noise where every row looks like an incident.
  const ceiling = Math.max(max ?? Math.max(...values), 1)
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
