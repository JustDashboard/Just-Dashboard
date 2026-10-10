"use client"

import { Suspense } from "react"
import { Services } from "@/components/procs/services"

// The selected unit and a `?state=failed` link live in the query string,
// which the App Router only hands out inside a Suspense boundary.
export default function ServicesPage() {
  return (
    <Suspense>
      <Services />
    </Suspense>
  )
}
