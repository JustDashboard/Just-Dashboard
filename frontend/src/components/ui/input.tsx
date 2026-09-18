import * as React from "react"

import { cn } from "@/lib/utils"

/**
 * A field is 44px on a touch screen and 36px under a pointer.
 *
 * 44px is the smallest target a finger hits reliably, and a form of 32px inputs
 * is genuinely hard to use on a phone. The deployment wizard had worked this out
 * and written `h-11 sm:h-9` on 96 individual controls; nothing else in the app
 * had, so every other form shipped a 36px field. The rule belongs here, where a
 * new field gets it without being told.
 *
 * `text-base` on the narrow end is not a type decision: iOS Safari zooms the
 * viewport when a field with text under 16px takes focus, and the zoom does not
 * come back out. Above `sm` it drops to the product's own body size.
 */
function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "h-11 w-full min-w-0 rounded-md border border-input bg-transparent px-3 py-1 text-base shadow-xs focus-ring transition-[color,box-shadow] selection:bg-primary selection:text-primary-foreground file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 sm:h-9 sm:text-body dark:bg-input/30",
        "aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40",
        className,
      )}
      {...props}
    />
  )
}

export { Input }
