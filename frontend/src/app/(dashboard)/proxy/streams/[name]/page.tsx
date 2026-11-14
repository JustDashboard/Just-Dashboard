"use client"

import { Suspense } from "react"
import { StreamPage } from "@/components/proxy/stream-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function ProxyStreamPage() {
  return (
    <Suspense>
      <StreamPage />
    </Suspense>
  )
}
