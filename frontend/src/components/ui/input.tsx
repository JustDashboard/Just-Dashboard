"use client"

import * as React from "react"

import { cn } from "@/lib/utils"
import { useFieldControl } from "@/components/form"

/**
 * A field is 44px under a finger and 36px under a pointer.
 *
 * 44px is the smallest target a finger hits reliably, and a form of 32px inputs
 * is genuinely hard to use on a phone. The deployment wizard had worked this out
 * and written `h-11 sm:h-9` on 96 individual controls; nothing else in the app
 * had, so every other form shipped a 36px field. The rule belongs here, where a
 * new field gets it without being told. `pointer-coarse:h-11` carries it to a
 * tablet as well, and past a caller's `h-8`, which was a desktop toolbar's
 * size and made a 32px field on the phone.
 *
 * The 16px text is not a type decision: iOS Safari zooms the viewport when a
 * field with text under 16px takes focus, and the zoom does not come back out.
 * It is written on the `max-sm:` and `pointer-coarse:` variants and not only as
 * the bare base, because a caller's `font-mono text-xs` replaces a bare
 * `text-base` in the merge and 242 fields were 12 or 13px on a phone that way.
 * A variant is a different slot to tailwind-merge, so no caller class removes
 * it. Above `sm` under a mouse the field drops to the product's body size, and
 * a caller's own size applies there.
 *
 * No shadow: a field is a control on the page, and nothing on the page lifts.
 * The edge is the control edge, the one border that has to reach 3:1 against
 * the ground (WCAG 1.4.11); it falls back to `--input` until that token lands.
 */
function Input({ className, type, ...rest }: React.ComponentProps<"input">) {
  // Inside a `Field`: its id, its hint and error ids, and whether it is wrong.
  const props = useFieldControl(rest)
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "h-11 w-full min-w-0 rounded-md border border-[color:var(--control-edge,var(--input))] bg-input/30 px-3 py-1 text-base focus-ring transition-[color,box-shadow] selection:bg-primary selection:text-primary-foreground file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 sm:h-9 sm:text-body",
        "aria-invalid:border-destructive aria-invalid:ring-destructive/40 pointer-coarse:h-11",
        className,
        // The zoom guard goes after the caller's classes, so even a prefixed
        // size cannot take it away. The height above stays before them: an
        // `InputGroupInput` fills its group at every pointer.
        "max-sm:text-base pointer-coarse:text-base",
      )}
      {...props}
    />
  )
}

export { Input }
