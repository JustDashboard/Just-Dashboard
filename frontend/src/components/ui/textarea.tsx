import * as React from "react"

import { cn } from "@/lib/utils"

/**
 * `Input`'s rules for a field of several lines: 16px text under a finger or
 * below `sm`, written on variants after the caller's classes so a
 * `font-mono text-xs` cannot undo the iOS zoom guard; no shadow; the control
 * edge, falling back to `--input`.
 */
function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "flex field-sizing-content min-h-16 w-full rounded-md border border-[color:var(--control-edge,var(--input))] bg-input/30 px-3 py-2 text-base focus-ring transition-[color,box-shadow] placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/40 sm:text-body",
        className,
        "max-sm:text-base pointer-coarse:text-base",
      )}
      {...props}
    />
  )
}

export { Textarea }
