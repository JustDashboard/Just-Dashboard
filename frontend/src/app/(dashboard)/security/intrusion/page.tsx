"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { IntrusionPanels } from "@/components/security/intrusion-panels"

// The engine on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function SecurityIntrusionPage() {
  return (
    <Page className="animate-rise">
      <Suspense>
        <IntrusionPanels />
      </Suspense>
    </Page>
  )
}
