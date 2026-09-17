"use client"

import { Check } from "@/components/icons"
import { StatusDot } from "@/components/status-dot"
import { cn } from "@/lib/utils"

/**
 * The mark beside a window tab or a session row that says what is happening
 * in it: a breathing dot while something is working — the product's one mark
 * for "this is live", quiet enough to sit on a strip of five tabs — and a
 * check once it has finished, until that has been seen. Nothing at all
 * otherwise, including for a program that is merely open: an editor, or an
 * agent waiting for its next message, holds the terminal and does nothing.
 */
export function ActivityMark({
  working,
  finished,
  className,
}: {
  working: boolean
  finished: boolean
  className?: string
}) {
  if (working) {
    return (
      <span role="img" aria-label="Working" className={cn("flex shrink-0 items-center", className)}>
        <StatusDot tone="running" live />
      </span>
    )
  }
  if (finished) {
    return (
      <span
        role="img"
        aria-label="Finished"
        className={cn("flex shrink-0 items-center", className)}
      >
        <Check className="size-3 text-success" />
      </span>
    )
  }
  return null
}
