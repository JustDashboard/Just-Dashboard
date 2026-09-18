"use client"

import { Suspense } from "react"
import { ProjectLogs } from "@/components/deploy/project-logs"
import { LoadingPanel } from "@/components/state"

export default function Page() {
  return (
    <Suspense fallback={<LoadingPanel rows={4} />}>
      <ProjectLogs />
    </Suspense>
  )
}
