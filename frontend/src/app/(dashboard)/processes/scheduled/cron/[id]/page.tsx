"use client"

import { Suspense } from "react"
import { CronJobPage } from "@/components/procs/schedule-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function CronJobRoute() {
  return (
    <Suspense>
      <CronJobPage />
    </Suspense>
  )
}
