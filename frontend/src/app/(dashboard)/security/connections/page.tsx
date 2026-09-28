"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { LoadingPanel } from "@/components/state"
import { ConnectionsPanel } from "@/components/security/connections-panel"

export default function SecurityConnectionsPage() {
  return (
    <Page className="animate-rise">
      {/* The panel reads its search from the address bar, which the server does not have. */}
      <Suspense fallback={<LoadingPanel />}>
        <ConnectionsPanel />
      </Suspense>
    </Page>
  )
}
