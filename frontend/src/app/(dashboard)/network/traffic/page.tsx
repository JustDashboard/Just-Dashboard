"use client"

import { Suspense } from "react"
import { TrafficPage } from "@/components/network/traffic/traffic-page"

// The view and the window live in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function NetworkTrafficRoute() {
  return (
    <Suspense>
      <TrafficPage />
    </Suspense>
  )
}
