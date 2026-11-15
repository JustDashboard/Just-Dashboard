"use client"

import { Suspense } from "react"
import { Page } from "@/components/page"
import { ToolsPanel } from "@/components/network/tools-panel"

// The tool another page sends the reader to lives in the query string, which
// the App Router only hands out inside a Suspense boundary.
export default function NetworkToolsPage() {
  return (
    // A workbench: one frame held to the window, the rail and the pane each
    // scrolling inside it. Below `md` it runs to the window's edges, one pane
    // at a time (`phone-panes.tsx`).
    <Page fill className="animate-rise gap-3 p-0 md:px-3 md:py-3">
      <Suspense>
        <ToolsPanel />
      </Suspense>
    </Page>
  )
}
