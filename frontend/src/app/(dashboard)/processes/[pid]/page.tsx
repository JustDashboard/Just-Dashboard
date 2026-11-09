"use client"

import { Suspense } from "react"
import { ProcessPage } from "@/components/procs/process-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function ProcessRoute() {
  return (
    <Suspense>
      <ProcessPage />
    </Suspense>
  )
}
