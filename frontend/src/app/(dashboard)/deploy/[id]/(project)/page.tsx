"use client"

import { Suspense } from "react"
import { ProjectOverview } from "@/components/deploy/project-overview"
import { OverviewSkeleton } from "@/components/deploy/overview-skeleton"
import { useLegacyTabRedirect, useProject } from "@/components/deploy/project-context"

// Legacy `?tab=` links redirect first; only once there is nothing to redirect
// does the overview itself render.
function OverviewPage() {
  const project = useProject()
  const redirecting = useLegacyTabRedirect(project.projectId)
  if (redirecting) return <OverviewSkeleton />
  return <ProjectOverview />
}

export default function Page() {
  return (
    <Suspense fallback={<OverviewSkeleton />}>
      <OverviewPage />
    </Suspense>
  )
}
