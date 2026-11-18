"use client"

import * as React from "react"
import { Check } from "@/components/icons"
import { Checkbox as CheckboxPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { useFieldControl } from "@/components/form"
import { hitAreaClasses } from "@/components/ui/button"

/**
 * A 16px box with a 24px press box under a mouse and 44px under a finger, so
 * a checkbox in a table's first column is a target rather than a speck. The
 * edge is the control edge, and the radius is the
 * mark step, `rounded-sm`.
 *
 * Inside a `Field` it is described by the field's hint and error and is
 * invalid with the error. It never takes the field's id: boxes come in runs —
 * the tables a dump holds, the methods a site allows — each named by its own
 * words, and the field's id on every one of them would be one id on many
 * elements and the field's label pointing at the first. A lone box names
 * itself with its own `id` and label.
 */
function Checkbox({ className, ...rest }: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  const props = { ...useFieldControl(rest), id: rest.id }
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "peer size-4 shrink-0 rounded-sm border border-control-edge bg-control focus-ring transition-shadow disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/40 data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground",
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
