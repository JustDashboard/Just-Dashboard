"use client"

import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

/** Retained state is useful during a failed poll, provided it is not presented as a fresh read. */
export function NetworkReadWarning({ error, refresh }: { error?: Error; refresh: () => void }) {
  if (!error) return null
  return (
    <Notice tone="warning" title="Showing the last known network state">
      <p>The latest refresh failed. These readings may be out of date.</p>
      <p className="mt-1">{error.message}</p>
      <Button size="xs" variant="outline" className="mt-2" onClick={refresh}>
        Refresh
      </Button>
    </Notice>
  )
}
