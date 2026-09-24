"use client"

import { Suspense } from "react"
import { LogsSkeleton, ProjectLogs } from "@/components/deploy/project-logs"

export default function Page() {
  return (
    <Suspense fallback={<LogsSkeleton />}>
      <ProjectLogs />
    </Suspense>
  )
}
