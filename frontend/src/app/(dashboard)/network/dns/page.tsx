"use client"

import { Suspense } from "react"
import { DNSPage } from "@/components/network/dns/dns-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function NetworkDNSRoute() {
  return (
    <Suspense>
      <DNSPage />
    </Suspense>
  )
}
