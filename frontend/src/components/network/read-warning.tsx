"use client"

import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

/** Retained state is useful during a failed poll, provided it is not presented as a fresh read. */
export function NetworkReadWarning({
  error,
  refresh,
  lastSuccess,
  reading = "network state",
}: {
  error?: Error
  refresh: () => void
  lastSuccess?: number
  reading?: string
}) {
  if (!error) return null
  return (
    <div role="alert">
      <Notice tone="warning" title={`Showing the last known ${reading}`}>
        <p>The latest refresh failed. These readings may be out of date.</p>
        {lastSuccess && (
          <p>
            Last successful read:{" "}
            <time dateTime={new Date(lastSuccess).toISOString()}>
              {new Date(lastSuccess).toLocaleString()}
            </time>
          </p>
        )}
        <p className="mt-1">{error.message}</p>
        <Button size="xs" variant="outline" className="mt-2" onClick={refresh}>
          Refresh
        </Button>
      </Notice>
    </div>
  )
}
