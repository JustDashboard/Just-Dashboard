"use client"

import { Page } from "@/components/page"
import { IntrusionPanels } from "@/components/security/intrusion-panels"

export default function SecurityIntrusionPage() {
  return (
    <Page className="animate-rise">
      <IntrusionPanels />
    </Page>
  )
}
