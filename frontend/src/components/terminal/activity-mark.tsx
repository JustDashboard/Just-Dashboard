"use client"

import { Check } from "@/components/icons"
import { StatusDot } from "@/components/status-dot"
import { cn } from "@/lib/utils"

/**
 * The mark beside a window tab or a session row that says what is happening
 * in it: a breathing dot while something is working — the product's one mark
 * for "this is live", quiet enough to sit on a strip of five tabs — and a
 * check once it has finished, until that has been seen. Nothing otherwise,
 * including for a program that is merely open: an editor, or an agent waiting
 * for its next message, holds the terminal and does nothing.
 *
 * The box is the same size whatever is in it, and it is there even when it is
 * empty. The dot is six pixels and the check twelve, and a tab that is sized
 * to its contents grew and shrank as one turned into the other, which read as
 * the whole strip twitching every time an agent stopped.
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
  const box = cn("flex size-3 shrink-0 items-center justify-center", className)
  if (working) {
    return (
      <span role="img" aria-label="Working" className={box}>
        <StatusDot tone="running" live />
      </span>
    )
  }
  if (finished) {
    return (
      <span role="img" aria-label="Finished" className={box}>
        <Check className="size-3 text-success" />
      </span>
    )
  }
  return <span aria-hidden className={box} />
}
