"use client"

import { Suspense } from "react"
import { PM2AppPage } from "@/components/procs/pm2-app-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function PM2AppRoute() {
  return (
    <Suspense>
      <PM2AppPage />
    </Suspense>
  )
}
