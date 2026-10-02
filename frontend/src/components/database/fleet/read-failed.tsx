"use client"

import { useState } from "react"
import { RefreshClockwise } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * A read that failed, with the way to ask again.
 *
 * `ErrorState` offers "Try again" only when the server marked the failure as
 * worth retrying, and most failures of these reads carry no such mark — a
 * proxy's 502, a backend that is restarting, a body that is not the shape.
 * The pages do ask again by themselves on their next poll, but nothing said
 * so, and a page that is one red line with no control on it reads as broken
 * for good. So where the server did not offer the retry, this does, and says
 * when the page will ask on its own.
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
  // The press is answered until the read it started settles: a new failure is
  // a new error object, and a success takes this block away.
  const [asked, setAsked] = useState<Error>()
  const offered = error instanceof ApiError && error.retryable
  return (
    <div className={className}>
      <ErrorState error={error} onRetry={onRetry} />
      {!offered && (
        <div className="mt-3 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
          <Button
            size="sm"
            variant="outline"
            pending={asked === error}
            onClick={() => {
              setAsked(error)
              onRetry()
            }}
          >
            <RefreshClockwise />
            Try again
          </Button>
          {every && (
            <span className="text-hint text-muted-foreground">
              It is asked again by itself every {every}.
            </span>
          )}
        </div>
      )}
    </div>
  )
}
