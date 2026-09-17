"use client"

import { Page } from "@/components/page"
import { ConnectionsPanel } from "@/components/security/connections-panel"

export default function SecurityConnectionsPage() {
  return (
    <Page className="animate-rise">
      <ConnectionsPanel />
    </Page>
  )
}
