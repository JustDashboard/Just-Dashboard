"use client"

import * as React from "react"
import { Switch as SwitchPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { hitAreaClasses } from "@/components/ui/button"

/**
 * A switch is an 18px track, and the finger that presses it is not: the
 * press box around it is 24px under a mouse and 44px under a finger, so a
 * switch standing alone (the Users page's "can sign in", a gateway card's
 * toggle) is as easy to hit as one inside a row that takes the tap.
 *
 * No shadow, and the track's edge is the control edge, so an unchecked switch
 * reads as a control on the card rather than a dent in it. It falls back to
 * `--input` until the token lands.
 */
function Switch({
  className,
  size = "default",
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root> & {
  size?: "sm" | "default"
}) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      data-size={size}
      className={cn(
        "peer group/switch inline-flex shrink-0 items-center rounded-full border border-[color:var(--control-edge,var(--input))] focus-ring transition-all disabled:cursor-not-allowed disabled:opacity-50 data-[size=default]:h-[1.15rem] data-[size=default]:w-8 data-[size=sm]:h-3.5 data-[size=sm]:w-6 data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=unchecked]:bg-input/80",
        hitAreaClasses,
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className={cn(
          "pointer-events-none block rounded-full ring-0 transition-transform group-data-[size=default]/switch:size-4 group-data-[size=sm]/switch:size-3 data-[state=checked]:translate-x-[calc(100%-2px)] data-[state=checked]:bg-primary-foreground data-[state=unchecked]:translate-x-0 data-[state=unchecked]:bg-foreground",
        )}
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
