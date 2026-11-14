"use client"

import { Suspense } from "react"
import { TunnelPage } from "@/components/network/vpn/tunnel-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function TunnelRoute() {
  return (
    <Suspense>
      <TunnelPage />
    </Suspense>
  )
}
