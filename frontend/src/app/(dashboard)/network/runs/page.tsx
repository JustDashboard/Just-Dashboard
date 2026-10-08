"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { SavedRuns } from "@/components/network/saved-runs"

export default function NetworkRunsPage() {
  return (
    <Page className="animate-rise">
      <Suspense>
        <SavedRuns />
      </Suspense>
    </Page>
  )
}
