"use client"

import { Suspense } from "react"
import { LiveProcesses } from "@/components/procs/process-table"

// The selected process lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function ProcessesPage() {
  return (
    <Suspense>
      <LiveProcesses />
    </Suspense>
  )
}
