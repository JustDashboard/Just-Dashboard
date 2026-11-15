"use client"

import { Suspense } from "react"
import { DNSEnginePage } from "@/components/network/dns/engine-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function NetworkDNSEngineRoute() {
  return (
    <Suspense>
      <DNSEnginePage />
    </Suspense>
  )
}
