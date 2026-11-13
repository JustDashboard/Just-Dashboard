"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { ToolsPanel } from "@/components/network/tools-panel"

// The tool another page sends the reader to lives in the query string, which
// the App Router only hands out inside a Suspense boundary.
export default function NetworkToolsPage() {
  return (
    // Below `md` the workbench is one pane at a time, the height of the
    // window (`phone-panes.tsx`), so there the page is held to it.
    <Page className="animate-rise max-md:h-full max-md:min-h-0 max-md:overflow-hidden">
      <Suspense>
        <ToolsPanel />
      </Suspense>
    </Page>
  )
}
