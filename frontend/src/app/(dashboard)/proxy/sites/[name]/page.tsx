"use client"

import { Suspense } from "react"
import { SitePage } from "@/components/proxy/site-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function ProxySitePage() {
  return (
    <Suspense>
      <SitePage />
    </Suspense>
  )
}
