"use client"

import { Suspense } from "react"
import { ContainerTrafficPage } from "@/components/network/traffic/container-page"

// The window lives in the query string, which the App Router only hands out
// inside a Suspense boundary.
export default function ContainerTrafficRoute() {
  return (
    <Suspense>
      <ContainerTrafficPage />
    </Suspense>
  )
}
