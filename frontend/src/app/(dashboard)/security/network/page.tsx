"use client"

import { Page } from "@/components/page"
import { NetworkPanel } from "@/components/security/network-panel"

export default function SecurityNetworkPage() {
  return (
    <Page className="animate-rise">
      <NetworkPanel />
    </Page>
  )
}
