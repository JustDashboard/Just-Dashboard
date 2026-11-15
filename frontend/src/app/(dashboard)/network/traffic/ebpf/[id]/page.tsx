"use client"

import { Suspense } from "react"
import { EBPFProgramPage } from "@/components/network/traffic/program-page"

// The type the list was narrowed to lives in the query string, which the App
// Router only hands out inside a Suspense boundary.
export default function EBPFProgramRoute() {
  return (
    <Suspense>
      <EBPFProgramPage />
    </Suspense>
  )
}
