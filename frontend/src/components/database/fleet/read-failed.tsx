"use client"

import { ErrorState } from "@/components/state"

/**
 * A read that failed, with the way to ask again and how often the page asks
 * by itself.
 *
 * @deprecated use `ErrorState` with `retryable` and `every`, which is what
 * this is: it added its own Try again under `ErrorState` for the failures the
 * server did not mark worth retrying, and `ErrorState` offers it itself now.
 */
export function ReadFailed({
  error,
  onRetry,
  every,
  className,
}: {
  error: Error
  onRetry: () => void
  /** How often the page asks again by itself: "30 seconds". */
  every?: string
  className?: string
}) {
  return (
    <ErrorState error={error} onRetry={onRetry} retryable every={every} className={className} />
  )
}
