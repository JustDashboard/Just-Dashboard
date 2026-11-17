"use client"

import { useEffect, type RefObject } from "react"
import { useReactFlow, useStoreApi } from "@xyflow/react"

/**
 * How a canvas is moved, a whiteboard's (Excalidraw's): two fingers on a
 * trackpad slide it, a pinch zooms it about the fingers; a mouse drags it by
 * any open ground, or by its middle button anywhere, and its wheel slides it
 * too — Ctrl or ⌘ with the wheel zooms, Shift with it slides sideways. Space
 * held makes any drag a slide. Nothing on it changes size but by the zoom.
 *
 * Every `<ReactFlow>` of the product spreads this and calls `useCanvasPinch`,
 * so that one gesture does one thing on every canvas; `canvas-move.test.js`
 * fails for a canvas that does neither.
 */
export const CANVAS_MOVE = {
  panOnScroll: true,
  panOnScrollSpeed: 1,
  zoomOnScroll: false,
  zoomOnPinch: true,
  zoomOnDoubleClick: false,
  panOnDrag: [0, 1],
}

/** Whether a key press is one of a whiteboard's zoom keys: Ctrl or ⌘ with +, − or 0. */
export function zoomKey(e: {
  key: string
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
}): "in" | "out" | "real" | null {
  if (!(e.ctrlKey || e.metaKey) || e.altKey) return null
  if (e.key === "=" || e.key === "+") return "in"
  if (e.key === "-") return "out"
  if (e.key === "0") return "real"
  return null
}

/**
 * Where the view lands when it is zoomed to `next` about a point of the
 * canvas: what is under the point stays under it.
 */
export function zoomAbout(
  [x, y, k]: readonly [number, number, number],
  point: { x: number; y: number },
  next: number,
) {
  return {
    x: point.x - ((point.x - x) * next) / k,
    y: point.y - ((point.y - y) * next) / k,
    zoom: next,
  }
}

/**
 * Safari's pinch. It sends a trackpad's pinch as gesture events rather than
 * as the Ctrl-wheel every other browser sends, and zooms the page with it
 * unless told not to. Its gestures carry a scale and no point, so the canvas
 * zooms about where the pointer last was, and any Ctrl-wheel it also sends
 * while one runs is let go so a pinch zooms once.
 *
 * Called under a `ReactFlowProvider`, with the element the canvas fills.
 */
export function useCanvasPinch(
  ref: RefObject<HTMLElement | null>,
  zoom: { min: number; max: number },
) {
  const flow = useReactFlow()
  const store = useStoreApi()
  const { min, max } = zoom
  useEffect(() => {
    const el = ref.current
    if (!el) return
    let base: number | null = null
    let at = { x: 0, y: 0 }
    const track = (e: PointerEvent) => {
      at = { x: e.clientX, y: e.clientY }
    }
    const start = (e: Event) => {
      e.preventDefault()
      base = store.getState().transform[2]
    }
    const change = (e: Event) => {
      e.preventDefault()
      if (base === null) return
      const scale = (e as Event & { scale: number }).scale
      const next = Math.min(max, Math.max(min, base * scale))
      const rect = el.getBoundingClientRect()
      void flow.setViewport(
        zoomAbout(store.getState().transform, { x: at.x - rect.left, y: at.y - rect.top }, next),
      )
    }
    const end = (e: Event) => {
      e.preventDefault()
      base = null
    }
    const once = (e: WheelEvent) => {
      if (base !== null && e.ctrlKey) {
        e.preventDefault()
        e.stopPropagation()
      }
    }
    el.addEventListener("pointermove", track)
    el.addEventListener("gesturestart", start)
    el.addEventListener("gesturechange", change)
    el.addEventListener("gestureend", end)
    el.addEventListener("wheel", once, { capture: true, passive: false })
    return () => {
      el.removeEventListener("pointermove", track)
      el.removeEventListener("gesturestart", start)
      el.removeEventListener("gesturechange", change)
      el.removeEventListener("gestureend", end)
      el.removeEventListener("wheel", once, { capture: true })
    }
  }, [ref, flow, store, min, max])
}

/**
 * The arrow keys on a canvas: the server map, the databases map, a
 * database's diagram and a drawing (T10, WCAG 2.5.7). Each of them is moved
 * by a trackpad, a mouse and a finger (above); these keys do the same two
 * things without a drag.
 *
 * With nothing chosen an arrow slides the picture: the view goes the way the
 * arrow points, as a page scrolls, by a fixed distance on the screen whatever
 * the zoom. With a box or a frame chosen it moves that, by a distance on the
 * canvas, and the move is kept the way a drag is. Shift takes the larger
 * step. Ctrl, ⌘ and Alt are left to the browser and the canvas's own keys.
 *
 * Pure, so the steps and the bounds are tested without a browser
 * (`canvas-move.test.js`); the canvases decide what is chosen and where the
 * keyboard is (`isTypingTarget` from `lib/keys.ts`).
 */

/** How far one press slides the picture, in screen pixels; with Shift, the larger. */
export const PAN_STEP = { step: 48, large: 240 } as const

/** How far one press moves what is chosen, in canvas units; with Shift, the larger. */
export const MOVE_STEP = { step: 8, large: 40 } as const

/** The legend's caps for the four arrows, and what they do, as `Keycaps` draws them. */
export const ARROW_CAPS = "←+↑+↓+→"

export type Point = { x: number; y: number }

/** A view of a canvas: what is at canvas point `p` is drawn at `p * zoom + (x, y)`. */
export type View = { x: number; y: number; zoom: number }

/** A rectangle on the canvas. */
export type Rect = { x: number; y: number; width: number; height: number }

/** Which way an arrow points: one of x and y is 0, the other 1 or -1. */
export type Direction = { x: -1 | 0 | 1; y: -1 | 0 | 1 }

const ARROWS: Record<string, Direction> = {
  ArrowUp: { x: 0, y: -1 },
  ArrowDown: { x: 0, y: 1 },
  ArrowLeft: { x: -1, y: 0 },
  ArrowRight: { x: 1, y: 0 },
}

type KeyLike = { key: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean }

/**
 * The direction of an arrow pressed alone or with Shift, or null for any
 * other key and for an arrow with Ctrl, ⌘ or Alt, which mean something else
 * (the browser's history, a word's end, a system's own shortcut).
 */
export function arrowOf(event: KeyLike): Direction | null {
  if (event.ctrlKey || event.metaKey || event.altKey) return null
  return ARROWS[event.key] ?? null
}

/** One press's distance: the step, or the larger one with Shift. */
export function stepOf(steps: { step: number; large: number }, shift: boolean) {
  return shift ? steps.large : steps.step
}

/**
 * Keeps one axis of a pan inside its bounds without jumping: a view already
 * outside them (put there by a trackpad) is not pulled back, it only stops
 * going further out.
 */
function clampAxis(current: number, next: number, low: number, high: number) {
  if (next < low && next < current) return Math.min(current, Math.max(low, next))
  if (next > high && next > current) return Math.max(current, Math.min(high, next))
  return next
}

/**
 * The view after one press of an arrow: the picture slides the other way by
 * the step, in screen pixels, at any zoom.
 *
 * With `bounds` (what is drawn, on the canvas) and `size` (the canvas on the
 * screen), the picture is not slid out of sight: at least `margin` pixels of
 * it stay on the screen, so a held key cannot lose it.
 */
export function panned(
  view: View,
  direction: Direction,
  shift: boolean,
  fence?: { bounds: Rect; size: { width: number; height: number }; margin?: number },
): View {
  const step = stepOf(PAN_STEP, shift)
  let x = view.x - direction.x * step
  let y = view.y - direction.y * step
  if (fence) {
    const { bounds, size, margin = PAN_STEP.step } = fence
    const z = view.zoom
    x = clampAxis(
      view.x,
      x,
      margin - (bounds.x + bounds.width) * z,
      size.width - margin - bounds.x * z,
    )
    y = clampAxis(
      view.y,
      y,
      margin - (bounds.y + bounds.height) * z,
      size.height - margin - bounds.y * z,
    )
  }
  return { x, y, zoom: view.zoom }
}

/**
 * Where a chosen box or frame lands after one press, on the canvas.
 *
 * On a canvas that snaps to a grid the step is the grid's (the larger step a
 * whole number of cells) and the place is put on it, so a press never leaves
 * a box between two lines. `bounds` holds the box's corner inside a rectangle
 * of the canvas, where a canvas has one.
 */
export function moved(
  at: Point,
  direction: Direction,
  shift: boolean,
  options: {
    grid?: number
    bounds?: { minX: number; minY: number; maxX: number; maxY: number }
  } = {},
): Point {
  const { grid, bounds } = options
  let step = stepOf(MOVE_STEP, shift)
  if (grid && grid > 0) step = Math.max(1, Math.ceil(step / grid)) * grid
  let x = at.x + direction.x * step
  let y = at.y + direction.y * step
  if (grid && grid > 0) {
    // Off the grid to start with (placed before the grid was on): the first
    // press lands on the next line that way, not a full step past it.
    x = direction.x === 0 ? at.x : snapToward(at.x, direction.x, grid, step)
    y = direction.y === 0 ? at.y : snapToward(at.y, direction.y, grid, step)
  }
  if (bounds) {
    x = Math.min(bounds.maxX, Math.max(bounds.minX, x))
    y = Math.min(bounds.maxY, Math.max(bounds.minY, y))
  }
  return { x, y }
}

function snapToward(value: number, sign: -1 | 1, grid: number, step: number) {
  const on = Math.round(value / grid) * grid
  if (Math.abs(on - value) > 1e-9) {
    // The nearest line the way the arrow points, then whatever is left of the step.
    const line = sign > 0 ? Math.ceil(value / grid) * grid : Math.floor(value / grid) * grid
    return line + sign * (step - grid)
  }
  return on + sign * step
}
