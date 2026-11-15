"use client"

import * as React from "react"
import { Check } from "@/components/icons"
import { Checkbox as CheckboxPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { hitAreaClasses } from "@/components/ui/button"

/**
 * A 16px box with a 24px press box under a mouse and 44px under a finger, so
 * a checkbox in a table's first column is a target rather than a speck. The
 * edge is the control edge (falling back to `--input`), and the radius is the
 * mark step, `rounded-sm`.
 */
function Checkbox({ className, ...props }: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "peer size-4 shrink-0 rounded-sm border border-[color:var(--control-edge,var(--input))] bg-control focus-ring transition-shadow disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/40 data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground",
        hitAreaClasses,
        className,
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator
        data-slot="checkbox-indicator"
        className="grid place-content-center text-current transition-none"
      >
        <Check className="size-3.5" />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
}

export { Checkbox }
