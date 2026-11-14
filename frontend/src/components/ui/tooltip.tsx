"use client"

import * as React from "react"
import { Tooltip as TooltipPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { usePortalContainer } from "@/lib/portal-container"

function TooltipProvider({
  delayDuration = 0,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return (
    <TooltipPrimitive.Provider
      data-slot="tooltip-provider"
      delayDuration={delayDuration}
      {...props}
    />
  )
}

/**
 * The trigger a tooltip's own focus last landed on, and when: what `Tooltip`
 * needs to tell a scroll the focus caused from one the reader made.
 */
type Focused = { trigger: Element; at: number }
const FocusedContext = React.createContext<((trigger: Element) => void) | null>(null)

/** How long after a focus a scroll is still the page bringing the trigger into view. */
const FOCUS_SCROLL_MS = 800

/**
 * When anything last scrolled. One listener for every tooltip, in the capture
 * phase and registered when the first tooltip mounts, so it has run before
 * Radix's own listener for the same event (which a tooltip registers only
 * while it is open).
 */
let scrolledAt = -Infinity
let watching = false
function watchScrolls() {
  if (watching || typeof window === "undefined") return
  watching = true
  window.addEventListener("scroll", () => (scrolledAt = performance.now()), {
    capture: true,
    passive: true,
  })
}

/**
 * Radix closes a tooltip when anything around its trigger scrolls, since a
 * tooltip a pointer opened is then no longer under the pointer. A keyboard is
 * another matter: tabbing to a trigger below the fold opens its tooltip and
 * scrolls the page to it, and that scroll closed the tooltip a frame after it
 * opened, so a reader who tabbed to an info mark never read it. A close that
 * comes with a scroll, while the trigger still holds the focus that opened
 * it a moment ago, is that scroll and is not followed. Escape, a blur, a
 * pointer leaving and a scroll the reader makes later close it as before.
 */
function Tooltip({
  disableHoverableContent = true,
  open,
  defaultOpen = false,
  onOpenChange,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Root>) {
  const focused = React.useRef<Focused | null>(null)
  const noteFocus = React.useCallback((trigger: Element) => {
    focused.current = { trigger, at: performance.now() }
  }, [])
  const [shown, setShown] = React.useState(defaultOpen)
  React.useEffect(watchScrolls, [])
  const change = (next: boolean) => {
    if (!next) {
      const now = performance.now()
      const held = focused.current
      const byScroll = now - scrolledAt < 16
      if (
        byScroll &&
        held &&
        held.trigger === document.activeElement &&
        now - held.at < FOCUS_SCROLL_MS
      )
        return
    }
    if (open === undefined) setShown(next)
    onOpenChange?.(next)
  }
  return (
    <FocusedContext.Provider value={noteFocus}>
      <TooltipPrimitive.Root
        data-slot="tooltip"
        disableHoverableContent={disableHoverableContent}
        open={open ?? shown}
        onOpenChange={change}
        {...props}
      />
    </FocusedContext.Provider>
  )
}

function TooltipTrigger({
  onFocus,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Trigger>) {
  const noteFocus = React.useContext(FocusedContext)
  return (
    <TooltipPrimitive.Trigger
      data-slot="tooltip-trigger"
      onFocus={(event) => {
        onFocus?.(event)
        noteFocus?.(event.currentTarget)
      }}
      {...props}
    />
  )
}

function TooltipContent({
  className,
  sideOffset = 0,
  children,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal container={usePortalContainer()}>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        sideOffset={sideOffset}
        className={cn(
          "z-50 w-fit origin-(--radix-tooltip-content-transform-origin) animate-in rounded-md bg-foreground px-3 py-1.5 text-xs text-balance text-background fade-in-0 zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
          className,
        )}
        {...props}
      >
        {children}
        <TooltipPrimitive.Arrow className="z-50 size-2.5 translate-y-[calc(-50%_-_2px)] rotate-45 rounded-[2px] bg-foreground fill-foreground" />
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  )
}

export { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider }
