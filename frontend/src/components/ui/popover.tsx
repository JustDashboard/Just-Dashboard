"use client"

import * as React from "react"
import { Popover as PopoverPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { menuSurfaceClasses } from "@/components/ui/menu-styles"
import { usePortalContainer } from "@/lib/portal-container"

/**
 * Whether a closed popover may hand the focus back to its trigger. Radix does
 * that when the content unmounts, which is after its fade, and unconditionally:
 * Escape on one popover and a press on another within the fade, and the first
 * took the focus back out of the second as it left, which closed the second.
 * The focus goes back only when it has nowhere else to be — on the page's body,
 * because it was inside the content that just left. A reader who has moved on
 * keeps the focus where they put it.
 */
export function keepMovedFocus(event: Event) {
  const active = document.activeElement
  if (active && active !== document.body) event.preventDefault()
}

/** Whether the popover around this content is open, or null outside one of ours. */
const OpenContext = React.createContext<boolean | null>(null)

/**
 * Radix keeps a closed popover's content mounted while it fades out, with its
 * role and its words, so for a moment after Escape the page held two dialogs —
 * the one closing and the one just opened — and anything that asked for "the
 * dialog" found both. The content reads whether it is open from here and
 * leaves the accessibility tree the moment it closes; the fade is only paint.
 */
function Popover({
  open,
  defaultOpen = false,
  onOpenChange,
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Root>) {
  const [own, setOwn] = React.useState(defaultOpen)
  const shown = open ?? own
  return (
    <OpenContext.Provider value={shown}>
      <PopoverPrimitive.Root
        data-slot="popover"
        open={shown}
        onOpenChange={(next) => {
          if (open === undefined) setOwn(next)
          onOpenChange?.(next)
        }}
        {...props}
      />
    </OpenContext.Provider>
  )
}

function PopoverTrigger({ ...props }: React.ComponentProps<typeof PopoverPrimitive.Trigger>) {
  return <PopoverPrimitive.Trigger data-slot="popover-trigger" {...props} />
}

function PopoverContent({
  className,
  align = "center",
  sideOffset = 4,
  onCloseAutoFocus,
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Content>) {
  const open = React.useContext(OpenContext)
  return (
    <PopoverPrimitive.Portal container={usePortalContainer()}>
      <PopoverPrimitive.Content
        data-slot="popover-content"
        aria-hidden={open === false ? true : undefined}
        onCloseAutoFocus={(event) => {
          onCloseAutoFocus?.(event)
          if (!event.defaultPrevented) keepMovedFocus(event)
        }}
        align={align}
        sideOffset={sideOffset}
        className={cn(
          menuSurfaceClasses,
          "w-72 max-w-(--radix-popover-content-available-width) origin-(--radix-popover-content-transform-origin) p-4",
          className,
        )}
        {...props}
      />
    </PopoverPrimitive.Portal>
  )
}

function PopoverAnchor({ ...props }: React.ComponentProps<typeof PopoverPrimitive.Anchor>) {
  return <PopoverPrimitive.Anchor data-slot="popover-anchor" {...props} />
}

function PopoverHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="popover-header"
      className={cn("flex flex-col gap-1 text-sm", className)}
      {...props}
    />
  )
}

function PopoverTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return <div data-slot="popover-title" className={cn("font-medium", className)} {...props} />
}

function PopoverDescription({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="popover-description"
      className={cn("text-muted-foreground", className)}
      {...props}
    />
  )
}

export {
  Popover,
  PopoverTrigger,
  PopoverContent,
  PopoverAnchor,
  PopoverHeader,
  PopoverTitle,
  PopoverDescription,
}
