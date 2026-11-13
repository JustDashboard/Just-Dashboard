"use client"

import { Suspense } from "react"
import { ServicePage } from "@/components/procs/service-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function ServiceRoute() {
  return (
    <Suspense>
      <ServicePage />
    </Suspense>
  )
}
