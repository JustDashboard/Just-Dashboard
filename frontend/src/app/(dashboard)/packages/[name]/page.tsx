"use client"

import { Suspense } from "react"
import { PackagePage } from "@/components/packages/package-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function PackageRoute() {
  return (
    <Suspense>
      <PackagePage />
    </Suspense>
  )
}
