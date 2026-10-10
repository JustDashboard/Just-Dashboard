"use client"

import { Suspense } from "react"
import { StacksTab } from "@/components/docker/stacks-tab"

// A `?stack=` link from before stacks had pages lives in the query string,
// which the App Router only hands out inside a Suspense boundary.
export default function DockerStacksPage() {
  return (
    <Suspense>
      <StacksTab />
    </Suspense>
  )
}
