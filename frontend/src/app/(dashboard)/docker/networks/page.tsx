"use client"

import { Suspense } from "react"
import { Networks } from "@/components/docker/networks-page"

// The open network lives in the query string, which the App Router only hands
// out inside a Suspense boundary.
export default function DockerNetworksPage() {
  return (
    <Suspense>
      <Networks />
    </Suspense>
  )
}
