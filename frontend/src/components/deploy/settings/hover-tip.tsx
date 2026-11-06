"use client"

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * A tooltip around a control that is already a control: it renders no element
 * of its own, so a segment stays the direct child its group's rounding reads.
 *
 * Focus that came from a press does not open it. A select hands focus back to
 * its trigger when its list closes, and the tip would otherwise sit open over
 * the row until the reader clicked somewhere else; the keyboard still gets it,
 * because that focus is `:focus-visible`.
 */
export function HoverTip({ tip, children }: { tip: React.ReactNode; children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger
        asChild
        onFocus={(event) => {
          if (!event.target.matches(":focus-visible")) event.preventDefault()
        }}
      >
        {children}
      </TooltipTrigger>
      <TooltipContent className="max-w-xs leading-relaxed">{tip}</TooltipContent>
    </Tooltip>
  )
}
