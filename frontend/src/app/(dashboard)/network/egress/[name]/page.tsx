"use client"

import { Suspense } from "react"
import { EgressGroupPage } from "@/components/network/egress/egress-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function EgressGroupRoute() {
  return (
    <Suspense>
      <EgressGroupPage />
    </Suspense>
  )
}
