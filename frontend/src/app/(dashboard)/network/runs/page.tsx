"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { SavedRuns } from "@/components/network/saved-runs"
import { WatchedProbes } from "@/components/network/watched-probes"
import { useAuth } from "@/hooks/use-auth"

// Saved runs are evidence kept on request; watched probes are the same
// question asked on the watch list's schedule.
export default function NetworkRunsPage() {
  const { can } = useAuth()
  return (
    <Page className="animate-rise">
      <Suspense>
        <SavedRuns />
      </Suspense>
      <WatchedProbes admin={can("system.admin")} />
    </Page>
  )
}
