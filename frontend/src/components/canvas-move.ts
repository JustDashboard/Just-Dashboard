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
