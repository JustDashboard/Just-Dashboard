"use client"

import { Suspense } from "react"
import { StreamsPage } from "@/components/proxy/streams-panel"

// The page reads ?stream= and ?new= links, and useSearchParams needs a
// Suspense boundary above it.
export default function ProxyStreamsPage() {
  return (
    <Suspense>
      <StreamsPage />
    </Suspense>
  )
}
