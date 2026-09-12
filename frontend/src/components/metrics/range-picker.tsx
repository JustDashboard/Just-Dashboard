"use client"

import { ChevronLeft, ChevronRight, MagnifyingGlassMinus } from "@/components/icons"
import { timestamp } from "@/lib/format"
import {
  RANGES,
  windowLabel,
  type MetricsWindow,
  type RangeKey,
  type RangeSpec,
} from "@/lib/metrics-range"
import type { WindowControls } from "@/hooks/use-metrics-window"
import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * Chooses the window the charts cover.
 *
 * "Live" is kept as an option rather than made the only one: at two seconds a
 * sample it shows detail no stored series can, and it is the right view when
 * you are watching something happen right now. It is simply the wrong default
 * for a page you open to find out what happened while you were not here.
 *
 * The pan and zoom-out controls appear only once a window has been dragged.
 * A named range is anchored to the present and panning it would silently turn
 * "1h" into a fixed hour that no longer follows the clock.
 */
export function RangePicker({
  controls,
  ranges = RANGES,
}: {
  controls: WindowControls
  /** Narrower list for charts with no live feed to offer. */
  ranges?: RangeSpec[]
}) {
  const { window: win, zoomed, setRange, zoomOut, pan } = controls

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {zoomed && (
        <>
          <span className="numeric rounded-md border border-rule-primary bg-wash-primary px-2 py-1 text-hint font-medium text-primary">
            {windowLabel(win)} window
          </span>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Earlier"
            className="text-muted-foreground"
            onClick={() => pan(-0.5)}
          >
            <ChevronLeft className="size-4" />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Later"
            className="text-muted-foreground"
            onClick={() => pan(0.5)}
          >
            <ChevronRight className="size-4" />
          </Button>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="Zoom out"
                className="text-muted-foreground"
                onClick={zoomOut}
              >
                <MagnifyingGlassMinus className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Back to the previous window</TooltipContent>
          </Tooltip>
        </>
      )}
      <ToggleGroup
        type="single"
        value={zoomed ? "" : win.key}
        // Radix reports "" when the active item is pressed again. Falling back
        // to the current value makes that a no-op instead of clearing the
        // chart — except while zoomed, where re-picking the underlying range
        // is the natural way to say "take me back out".
        onValueChange={(next) => setRange((next as RangeKey) || win.key)}
        variant="outline"
        size="sm"
        aria-label="Chart time range"
      >
        {ranges.map((option) => (
          <ToggleGroupItem key={option.key} value={option.key} className="px-2.5 text-hint">
            {option.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </div>
  )
}

/** The exact span on screen, for the caption under a zoomed set of charts. */
export function windowSpanNote(win: MetricsWindow): string | null {
  if (win.from === undefined || win.to === undefined) return null
  return `${timestamp(new Date(win.from).toISOString())} → ${timestamp(new Date(win.to).toISOString())}`
}
