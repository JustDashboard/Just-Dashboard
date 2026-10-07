"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { ToolsPanel } from "@/components/network/tools-panel"

// The tool another page sends the reader to lives in the query string, which
// the App Router only hands out inside a Suspense boundary.
export default function NetworkToolsPage() {
  return (
    <Page className="animate-rise">
      <Suspense>
        <ToolsPanel />
      </Suspense>
    </Page>
  )
}
