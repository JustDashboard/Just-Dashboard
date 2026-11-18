"use client"

import { useState, useSyncExternalStore } from "react"
import { createPortal } from "react-dom"
import { CheckCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { STACK_EDGE, useBottomStack, useStackOccupant } from "@/components/dock"
import { StatusDot } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"

// The content area beside the rail, which a floating bar centres on. It is in
// the document for the whole life of the shell, so it never needs re-reading.
const subscribe = () => () => {}
const contentArea = () => document.querySelector<HTMLElement>('[data-slot="sidebar-inset"]')
const noContentArea = () => null

/** Where a floating bar is drawn: the content area, once in the browser. */
export function useFloatingBarHost() {
  return useSyncExternalStore(subscribe, contentArea, noContentArea)
}

/** The Save shortcut's keycap. Read once a bar is on screen, which is only ever in the browser. */
export const saveShortcut = () => (/Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘S" : "Ctrl S")

/** The keycap at the end of a bar's primary command; hidden on a phone and while it runs. */
export function SaveKey({ hidden }: { hidden?: boolean }) {
  return (
    <kbd
      aria-hidden
      className={cn(
        "ml-0.5 font-sans text-hint font-normal opacity-60 max-sm:hidden",
        hidden && "hidden",
      )}
    >
      {saveShortcut()}
    </kbd>
  )
}

const WIDTHS = { md: "max-w-xl", lg: "max-w-2xl" } as const

/**
 * The bar that floats at the foot of a settings page while there is something
 * to save: what is unsaved, what saving does, and the commands. It is the one
 * thing on such a page that floats, so it takes a popover's surface and
 * shadow (§2), and it sits in the bottom stack (`components/dock.tsx`): the
 * docks rise above it, and the page's scroller stops a focused field short of
 * it, where a field near the foot of a long form used to sit under the bar.
 *
 * The surface was written three times — the settings save bar, a proxy site's
 * bar with its two saves, a grants strip — and the copies had drifted on
 * width, wrapping and how the note was cut. The logic behind each stays its
 * own; this is what they look like.
 *
 * `position="viewport"` (the default) floats over the content area, centred on
 * it; `sticky` holds to the foot of the column it is in, for a column that
 * shares the area with something beside it (a rail of siblings).
 */
export function FloatingBar({
  headline,
  note,
  state = "dirty",
  actions,
  width = "md",
  position = "viewport",
  label = "Save changes",
}: {
  /** What there is to save: "3 unsaved changes". Shimmers while saving. */
  headline: string
  /** What saving does, or why it cannot be done yet, in one line. */
  note?: React.ReactNode
  state?: "dirty" | "saving" | "saved"
  actions?: React.ReactNode
  /** `lg` for a bar with three commands. */
  width?: keyof typeof WIDTHS
  position?: "viewport" | "sticky"
  /** The region's name. */
  label?: string
}) {
  const host = useFloatingBarHost()
  const [frame, setFrame] = useState<HTMLDivElement | null>(null)
  // Over a pane page's bottom bar (order 0) when both are up, never on it.
  const id = useStackOccupant(frame, "bar", { order: 1 })
  const lift = useBottomStack().bars[id] ?? 0
  const sticky = position === "sticky"
  if (!sticky && !host) return null

  const bar = (
    <div
      ref={setFrame}
      data-slot="floating-bar"
      style={lift > 0 ? { bottom: lift + STACK_EDGE } : undefined}
      className={cn(
        "pointer-events-none z-float flex justify-center",
        sticky ? "sticky bottom-4" : "absolute inset-x-0 bottom-4 px-3 md:bottom-6",
      )}
    >
      <div
        role="region"
        aria-label={label}
        aria-live="polite"
        className={cn(
          "pointer-events-auto flex w-full animate-rise flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-border-strong bg-popover py-2.5 pr-2.5 pl-4 text-popover-foreground shadow-lg",
          WIDTHS[width],
        )}
      >
        {/* Keyed on what it is saying, so each change of state arrives
            (§11) rather than being repainted under the reader's eye. */}
        <div key={state} className="min-w-0 flex-[1_1_10rem] animate-rise">
          <p className="flex min-w-0 items-center gap-2 text-body leading-snug font-medium">
            {state === "saved" ? (
              <CheckCircle aria-hidden className="size-4 shrink-0 text-success" />
            ) : (
              <StatusDot tone="warning" />
            )}
            {state === "saving" ? (
              <TextShimmer>{headline}</TextShimmer>
            ) : (
              <span className="numeric">{headline}</span>
            )}
          </p>
          {note && (
            <p className="mt-0.5 pl-4 text-xs text-muted-foreground max-sm:line-clamp-2 sm:truncate">
              {note}
            </p>
          )}
        </div>
        {actions && (
          <div className="ml-auto flex shrink-0 flex-wrap items-center justify-end gap-1.5">
            {actions}
          </div>
        )}
      </div>
    </div>
  )
  return sticky ? bar : createPortal(bar, host!)
}
