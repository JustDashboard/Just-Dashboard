"use client"

import { useSyncExternalStore, type ReactNode } from "react"
import { createPortal } from "react-dom"
import { cn } from "@/lib/utils"

const subscribe = () => () => {}

/** Working feedback floats above its owner instead of resizing the workspace. */
export function OperationOverlay({
  children,
  local = false,
  className,
}: {
  children: ReactNode
  local?: boolean
  className?: string
}) {
  const mounted = useSyncExternalStore(
    subscribe,
    () => true,
    () => false,
  )
  const panel = (
    <div
      data-slot="operation-overlay"
      className={cn(
        "pointer-events-auto animate-rise space-y-3 rounded-xl border border-hairline bg-popover p-4 shadow-md",
        local
          ? "absolute inset-x-3 bottom-full z-10 mb-3"
          : "fixed top-16 right-4 z-40 max-h-[calc(100svh-5rem)] w-[min(24rem,calc(100vw-2rem))] overflow-y-auto",
        className,
      )}
    >
      {children}
    </div>
  )
  // A dialog keeps its status in its own accessibility and focus scope.
  if (local) return panel
  return mounted ? createPortal(panel, document.body) : null
}
