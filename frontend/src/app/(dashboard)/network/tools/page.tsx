"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { ToolsPanel } from "@/components/network/tools-panel"

// The tool another page sends the reader to lives in the query string, which
// the App Router only hands out inside a Suspense boundary.
export default function NetworkToolsPage() {
  return (
    // A workbench held to the window, the rail and the pane each scrolling
    // inside it, running to the sheet's edges with no frame of its own. Below
    // `md` it shows one pane at a time (`phone-panes.tsx`).
    <Page rise fill="workspace">
      <Suspense>
        <ToolsPanel />
      </Suspense>
    </Page>
  )
}
