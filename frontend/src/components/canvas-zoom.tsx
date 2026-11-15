"use client"

import { useReactFlow, useStore } from "@xyflow/react"
import { Minus, Plus, RotateCounterClockwise } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"

/**
 * The ground of a control that floats on a canvas: the zoom bar, the
 * inspector and full-screen toggles in the corner opposite. A popover's
 * surface without its shadow — it sits on the canvas, not over the page.
 */
export const FLOAT =
  "absolute flex items-center gap-0.5 rounded-md border border-hairline bg-popover p-0.5"

const ZOOM_STEP = { duration: 150 }

/**
 * The bar at the foot of a canvas — the server's map, a database's diagram:
 * out, how far in the picture is drawn (pressed, the real size), in, Fit,
 * whatever the canvas adds (`extra`: the map's All), and — while something has
 * been moved — Tidy, putting everything back where the layout would.
 *
 * A whiteboard in this product is zoomed and fitted from this one place, and
 * where the reader is in the picture is said by the percentage rather than
 * by a second, smaller picture (`canvas-move.ts` has how it moves). The two
 * canvases drew it line for line apart; only the diagram named the
 * percentage to a screen reader.
 */
export function CanvasZoom({
  onFit,
  onTidy,
  tidyLabel = "Tidy: lay everything out again",
  extra,
  slot,
  className,
}: {
  onFit: () => void
  /** Only while something has been moved: with nothing to put back it would be noise. */
  onTidy?: () => void
  tidyLabel?: string
  extra?: React.ReactNode
  slot?: string
  className?: string
}) {
  const flow = useReactFlow()
  const percent = useStore((state) => Math.round(state.transform[2] * 100))
  return (
    <div data-slot={slot} className={cn(FLOAT, "bottom-3 left-3", className)}>
      <IconAction label="Zoom out" className="size-7" onClick={() => void flow.zoomOut(ZOOM_STEP)}>
        <Minus />
      </IconAction>
      <Button
        size="xs"
        variant="ghost"
        title="Back to the real size (Ctrl 0)"
        aria-label={`Zoom ${percent}%: back to the real size`}
        className="numeric h-7 min-w-12 px-1.5 text-hint"
        onClick={() => void flow.zoomTo(1, ZOOM_STEP)}
      >
        {percent}%
      </Button>
      <IconAction label="Zoom in" className="size-7" onClick={() => void flow.zoomIn(ZOOM_STEP)}>
        <Plus />
      </IconAction>
      <Button
        size="xs"
        variant="ghost"
        title="Fit the picture to the canvas (F)"
        className="h-7 px-2 text-hint"
        onClick={onFit}
      >
        Fit
      </Button>
      {extra}
      {onTidy && (
        <IconAction label={tidyLabel} className="size-7" onClick={onTidy}>
          <RotateCounterClockwise />
        </IconAction>
      )}
    </div>
  )
}

/** A word on the zoom bar beside Fit: the map's All. */
export function CanvasZoomWord({
  onClick,
  title,
  children,
}: {
  onClick: () => void
  title?: string
  children: React.ReactNode
}) {
  return (
    <Button
      size="xs"
      variant="ghost"
      title={title}
      className="h-7 px-2 text-hint"
      onClick={onClick}
    >
      {children}
    </Button>
  )
}
