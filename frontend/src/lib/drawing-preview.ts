import type { DrawingElement } from "@/lib/boards"

export type Box = { x: number; y: number; w: number; h: number }

const LINEAR = new Set<DrawingElement["type"]>(["line", "arrow", "freedraw"])

export function isLinear(el: DrawingElement) {
  return LINEAR.has(el.type) && (el.points?.length ?? 0) > 1
}

/**
 * Where an element actually draws. A linear element's x/y is its first point,
 * not its corner: an arrow drawn up and to the left has points at negative
 * offsets, so its width and height alone put it in the wrong place.
 */
export function extentOf(el: DrawingElement): Box {
  if (!isLinear(el)) return { x: el.x, y: el.y, w: Math.abs(el.w), h: Math.abs(el.h) }
  let x1 = Infinity
  let y1 = Infinity
  let x2 = -Infinity
  let y2 = -Infinity
  for (const [px, py] of el.points!) {
    x1 = Math.min(x1, px)
    y1 = Math.min(y1, py)
    x2 = Math.max(x2, px)
    y2 = Math.max(y2, py)
  }
  return { x: el.x + x1, y: el.y + y1, w: x2 - x1, h: y2 - y1 }
}

/**
 * The window a thumbnail shows: the drawing with room around it, and a floor
 * on its size so one small shape is a shape on a canvas rather than a slab
 * filling the card.
 */
export function frameOf(elements: readonly DrawingElement[]): Box | null {
  if (elements.length === 0) return null
  let x1 = Infinity
  let y1 = Infinity
  let x2 = -Infinity
  let y2 = -Infinity
  for (const el of elements) {
    const box = extentOf(el)
    x1 = Math.min(x1, box.x)
    y1 = Math.min(y1, box.y)
    x2 = Math.max(x2, box.x + box.w)
    y2 = Math.max(y2, box.y + box.h)
  }
  const pad = Math.max(x2 - x1, y2 - y1) * 0.06 + 24
  const w = Math.max(x2 - x1 + pad * 2, 480)
  const h = Math.max(y2 - y1 + pad * 2, 270)
  return { x: (x1 + x2) / 2 - w / 2, y: (y1 + y2) / 2 - h / 2, w, h }
}

/**
 * Excalidraw's own corner for a rounded rectangle: a quarter of the shorter
 * side, up to 32 units, so a large card does not turn into a capsule.
 */
export function cornerOf(el: DrawingElement) {
  if (!el.round) return 0
  const side = Math.min(Math.abs(el.w), Math.abs(el.h))
  return side <= 128 ? side * 0.25 : 32
}

const round = (n: number) => Math.round(n * 10) / 10

/**
 * A line through its points: straight segments, or a Catmull-Rom curve
 * through every point when the element is rounded — the curve Excalidraw
 * draws for a curved arrow passes through each point it was given.
 */
export function linePath(points: readonly [number, number][], curved: boolean) {
  const [first, ...rest] = points
  let d = `M${round(first[0])} ${round(first[1])}`
  if (!curved || points.length < 3) {
    for (const [x, y] of rest) d += `L${round(x)} ${round(y)}`
    return d
  }
  for (let i = 0; i < points.length - 1; i++) {
    const p0 = points[Math.max(i - 1, 0)]
    const p1 = points[i]
    const p2 = points[i + 1]
    const p3 = points[Math.min(i + 2, points.length - 1)]
    const c1x = p1[0] + (p2[0] - p0[0]) / 6
    const c1y = p1[1] + (p2[1] - p0[1]) / 6
    const c2x = p2[0] - (p3[0] - p1[0]) / 6
    const c2y = p2[1] - (p3[1] - p1[1]) / 6
    d += `C${round(c1x)} ${round(c1y)} ${round(c2x)} ${round(c2y)} ${round(p2[0])} ${round(p2[1])}`
  }
  return d
}

/**
 * The two strokes of an arrowhead at `tip`, pointing away from `from`, as a
 * path. Its length follows the stroke, as Excalidraw's does, and never takes
 * more than half of the segment it ends.
 */
export function arrowhead(
  tip: readonly [number, number],
  from: readonly [number, number],
  strokeWidth = 2,
) {
  const dx = tip[0] - from[0]
  const dy = tip[1] - from[1]
  const length = Math.hypot(dx, dy)
  if (length === 0) return ""
  const size = Math.min(18 + strokeWidth * 6, length / 2)
  const angle = Math.atan2(dy, dx)
  const spread = Math.PI / 7
  const wing = (side: number) => [
    tip[0] - size * Math.cos(angle + side * spread),
    tip[1] - size * Math.sin(angle + side * spread),
  ]
  const [ax, ay] = wing(1)
  const [bx, by] = wing(-1)
  return `M${round(ax)} ${round(ay)}L${round(tip[0])} ${round(tip[1])}L${round(bx)} ${round(by)}`
}

/** Excalidraw's font ids, by the face this product draws each in. */
export function faceOf(fontFamily: number | undefined): "hand" | "sans" | "mono" {
  if (fontFamily === 3 || fontFamily === 8) return "mono"
  if (fontFamily === undefined || fontFamily === 1 || fontFamily === 5) return "hand"
  return "sans"
}

/**
 * A closed shape as one path, so a rectangle, an ellipse and a diamond all
 * draw — and sketch themselves in — the same way a line does.
 */
export function shapePath(el: DrawingElement) {
  const x = Math.min(el.x, el.x + el.w)
  const y = Math.min(el.y, el.y + el.h)
  const w = Math.abs(el.w)
  const h = Math.abs(el.h)
  if (el.type === "ellipse") {
    const rx = w / 2
    const ry = h / 2
    const cy = round(y + ry)
    return `M${round(x)} ${cy}A${round(rx)} ${round(ry)} 0 1 0 ${round(x + w)} ${cy}A${round(rx)} ${round(ry)} 0 1 0 ${round(x)} ${cy}Z`
  }
  if (el.type === "diamond") {
    return `M${round(x + w / 2)} ${round(y)}L${round(x + w)} ${round(y + h / 2)}L${round(x + w / 2)} ${round(y + h)}L${round(x)} ${round(y + h / 2)}Z`
  }
  const r = cornerOf(el)
  if (r === 0) return `M${round(x)} ${round(y)}H${round(x + w)}V${round(y + h)}H${round(x)}Z`
  return (
    `M${round(x + r)} ${round(y)}H${round(x + w - r)}Q${round(x + w)} ${round(y)} ${round(x + w)} ${round(y + r)}` +
    `V${round(y + h - r)}Q${round(x + w)} ${round(y + h)} ${round(x + w - r)} ${round(y + h)}` +
    `H${round(x + r)}Q${round(x)} ${round(y + h)} ${round(x)} ${round(y + h - r)}` +
    `V${round(y + r)}Q${round(x)} ${round(y)} ${round(x + r)} ${round(y)}Z`
  )
}
