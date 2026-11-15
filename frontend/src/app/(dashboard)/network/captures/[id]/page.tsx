"use client"

import { Suspense } from "react"
import { CapturePage } from "@/components/network/capture-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function PacketCaptureRoute() {
  return (
    <Suspense>
      <CapturePage />
    </Suspense>
  )
}
