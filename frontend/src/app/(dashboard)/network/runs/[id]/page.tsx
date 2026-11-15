"use client"

import { Suspense } from "react"
import { RunPage } from "@/components/network/run-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function SavedRunRoute() {
  return (
    <Suspense>
      <RunPage />
    </Suspense>
  )
}
