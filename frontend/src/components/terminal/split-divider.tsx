"use client"

import { useRef, useState } from "react"
import type { Divider } from "@/lib/terminal-layout"
import { SPLIT_GAP } from "@/lib/terminal-layout"
import { cn } from "@/lib/utils"

export function SplitDivider({
  divider,
  onChange,
}: {
  divider: Divider
  onChange: (ratio: number, commit: boolean) => void
}) {
  const [dragging, setDragging] = useState(false)
  const captured = useRef(false)
  const start = useRef({ pointer: 0, ratio: 0.5 })
  const horizontal = divider.axis === "x"
  const available = (horizontal ? divider.parent.width : divider.parent.height) - SPLIT_GAP
  const coordinate = (event: React.PointerEvent) => (horizontal ? event.clientX : event.clientY)
  const clamp = (ratio: number) => Math.max(divider.min, Math.min(divider.max, ratio))
  const move = (event: React.PointerEvent) =>
    clamp(
      start.current.ratio + (coordinate(event) - start.current.pointer) / Math.max(1, available),
    )

  return (
    <div
      role="separator"
      tabIndex={0}
      aria-label={horizontal ? "Terminal split width" : "Terminal split height"}
      aria-orientation={horizontal ? "vertical" : "horizontal"}
      aria-valuenow={Math.round(divider.ratio * 100)}
      aria-valuemin={Math.round(divider.min * 100)}
      aria-valuemax={Math.round(divider.max * 100)}
      title="Drag to resize · Arrow keys to resize · Home or double-click to balance"
      style={{ left: divider.x, top: divider.y, width: divider.width, height: divider.height }}
      className={cn(
        "absolute z-10 touch-none bg-border/50 focus-ring-inset select-none hover:bg-primary/50 focus-visible:bg-primary",
        horizontal ? "cursor-col-resize" : "cursor-row-resize",
      )}
      onPointerDown={(event) => {
        if (event.button !== 0) return
        event.preventDefault()
        start.current = { pointer: coordinate(event), ratio: divider.ratio }
        captured.current = true
        setDragging(true)
        event.currentTarget.setPointerCapture(event.pointerId)
      }}
      onPointerMove={(event) => {
        if (captured.current) onChange(move(event), false)
      }}
      onPointerUp={(event) => {
        if (!captured.current) return
        captured.current = false
        onChange(move(event), true)
        setDragging(false)
        event.currentTarget.releasePointerCapture(event.pointerId)
      }}
      onPointerCancel={() => {
        captured.current = false
        onChange(divider.ratio, true)
        setDragging(false)
      }}
      onLostPointerCapture={() => {
        if (captured.current) {
          captured.current = false
          onChange(divider.ratio, true)
          setDragging(false)
        }
      }}
      onDoubleClick={() => onChange(0.5, true)}
      onKeyDown={(event) => {
        const before = horizontal ? "ArrowLeft" : "ArrowUp"
        const after = horizontal ? "ArrowRight" : "ArrowDown"
        if (event.key === "Home") {
          event.preventDefault()
          onChange(0.5, true)
        }
        if (event.key === before || event.key === after) {
          event.preventDefault()
          onChange(
            clamp(divider.ratio + (event.key === before ? -1 : 1) * (event.shiftKey ? 0.1 : 0.025)),
            true,
          )
        }
      }}
    >
      {dragging && (
        <span
          aria-hidden
          className={cn(
            "fixed inset-0 z-50",
            horizontal ? "cursor-col-resize" : "cursor-row-resize",
          )}
        />
      )}
    </div>
  )
}
