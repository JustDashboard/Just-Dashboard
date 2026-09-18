"use client"

import { Suspense } from "react"
import { ProjectConsole } from "@/components/deploy/project-console"
import { LoadingPanel } from "@/components/state"

export default function Page() {
  return (
    <Suspense fallback={<LoadingPanel rows={4} />}>
      <ProjectConsole />
    </Suspense>
  )
}
