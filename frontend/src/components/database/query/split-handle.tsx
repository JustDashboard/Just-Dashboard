"use client"

import { useCallback, useRef, useState } from "react"
import { cn } from "@/lib/utils"

/**
 * The grab strip between the editor and what its statements returned.
 *
 * `ResizeHandle` is the same thing for a column's width; this is its twin for
 * a row's height, with the same four manners: the pointer is captured, so a
 * drag that leaves the strip keeps arriving; the arrows move it and Home
 * resets it; a double-click resets it; and the page, not the handle, decides
 * what a position means.
 *
 * The value is the lower pane's share of the two (0–1), not a number of
 * pixels: a share stays sensible when the window changes height, where a
 * stored 600px would swallow the editor on a laptop.
 */
export function SplitHandle({
  label,
  value,
  min,
  max,
  onChange,
  onReset,
  measure,
  className,
}: {
  label: string
  /** The lower pane's share of the height, 0–1. */
  value: number
  min: number
  max: number
  onChange: (share: number) => void
  onReset: () => void
  /** The height the two panes share, in pixels, read when a drag starts. */
  measure: () => number
  className?: string
}) {
  const [dragging, setDragging] = useState(false)
  const start = useRef({ y: 0, share: 0, height: 1 })
  const clamp = useCallback((share: number) => Math.min(max, Math.max(min, share)), [min, max])

  const move = (event: React.PointerEvent<HTMLDivElement>) => {
    // Dragging up grows the lower pane.
    const delta = (start.current.y - event.clientY) / start.current.height
    onChange(clamp(start.current.share + delta))
  }

  return (
    <div
      role="separator"
      aria-orientation="horizontal"
      aria-label={label}
      aria-valuenow={Math.round(value * 100)}
      aria-valuemin={Math.round(min * 100)}
      aria-valuemax={Math.round(max * 100)}
      tabIndex={0}
      title={`${label} — drag, or double-click to reset`}
      onPointerDown={(event) => {
        if (event.button !== 0) return
        event.preventDefault()
        start.current = { y: event.clientY, share: value, height: Math.max(1, measure()) }
        setDragging(true)
        event.currentTarget.setPointerCapture(event.pointerId)
      }}
      onPointerMove={(event) => dragging && move(event)}
      onPointerUp={(event) => {
        if (!dragging) return
        setDragging(false)
        if (event.currentTarget.hasPointerCapture(event.pointerId)) {
          event.currentTarget.releasePointerCapture(event.pointerId)
        }
        move(event)
      }}
      onPointerCancel={() => setDragging(false)}
      onDoubleClick={onReset}
      onKeyDown={(event) => {
        const step = event.shiftKey ? 0.1 : 0.04
        if (event.key === "ArrowUp") {
          event.preventDefault()
          onChange(clamp(value + step))
        } else if (event.key === "ArrowDown") {
          event.preventDefault()
          onChange(clamp(value - step))
        } else if (event.key === "Home") {
          event.preventDefault()
          onReset()
        }
      }}
      className={cn(
        "h-2 shrink-0 cursor-row-resize touch-none rounded-full focus-ring select-none",
        className,
      )}
    >
      {dragging && <span className="fixed inset-0 z-50 cursor-row-resize" aria-hidden />}
    </div>
  )
}
