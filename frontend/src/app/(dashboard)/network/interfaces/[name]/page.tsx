"use client"

import { Suspense } from "react"
import { DevicePage } from "@/components/network/interfaces/device-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function DeviceRoute() {
  return (
    <Suspense>
      <DevicePage />
    </Suspense>
  )
}
