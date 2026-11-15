"use client"

import * as React from "react"

import { cn } from "@/lib/utils"
import { useFieldControl } from "@/components/form"

/**
 * `Input`'s rules for a field of several lines: 16px text under a finger or
 * below `sm`, written on variants after the caller's classes so a
 * `font-mono text-xs` cannot undo the iOS zoom guard; no shadow; the control
 * edge.
 */
function Textarea({ className, ...rest }: React.ComponentProps<"textarea">) {
  // Inside a `Field`: its id, its hint and error ids, and whether it is wrong.
  const props = useFieldControl(rest)
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "flex field-sizing-content min-h-16 w-full rounded-md border border-control-edge bg-input/30 px-3 py-2 text-base focus-ring transition-[color,box-shadow] placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/40 sm:text-body",
        className,
        "max-sm:text-base pointer-coarse:text-base",
      )}
      {...props}
    />
  )
}

export { Textarea }
