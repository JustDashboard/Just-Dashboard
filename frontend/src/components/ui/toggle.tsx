"use client"

import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { Toggle as TogglePrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { hitAreaClasses } from "@/components/ui/button"

const toggleVariants = cva(
  // `Button`'s rules: a body-size label, and the height as `--control-h` with
  // a coarse step per size, so a caller's own `h-*` still wins at every
  // pointer. The compact size takes the hit area the compact button does.
  "inline-flex h-(--control-h) min-w-(--control-h) items-center justify-center gap-2 rounded-md text-body font-medium whitespace-nowrap focus-ring transition-[color,box-shadow] hover:bg-muted hover:text-muted-foreground disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/40 data-[state=on]:bg-accent data-[state=on]:text-accent-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-transparent",
        outline:
          "border border-transparent border-t-edge bg-control hover:bg-control-hover hover:text-accent-foreground active:bg-control-active",
      },
      size: {
        default: "px-2 [--control-h:2.25rem] pointer-coarse:[--control-h:2.75rem]",
        sm: `px-1.5 [--control-h:2rem] pointer-coarse:[--control-h:2.5rem] [&_svg:not([class*='size-'])]:size-3.5 ${hitAreaClasses}`,
        lg: "px-2.5 [--control-h:2.5rem] pointer-coarse:[--control-h:3rem]",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
)

function Toggle({
  className,
  variant,
  size,
  ...props
}: React.ComponentProps<typeof TogglePrimitive.Root> & VariantProps<typeof toggleVariants>) {
  return (
    <TogglePrimitive.Root
      data-slot="toggle"
      className={cn(toggleVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Toggle, toggleVariants }
