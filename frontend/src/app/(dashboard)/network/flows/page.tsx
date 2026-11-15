"use client"

import { Suspense } from "react"
import { SocketHistoryPage } from "@/components/network/flows/flows-page"

// The view on screen lives in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function Page() {
  return (
    <Suspense>
      <SocketHistoryPage />
    </Suspense>
  )
}
