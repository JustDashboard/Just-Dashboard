"use client"

import { Suspense } from "react"
import { ProjectOverview } from "@/components/deploy/project-overview"
import { useLegacyTabRedirect, useProject } from "@/components/deploy/project-context"
import { LoadingPanel } from "@/components/state"

// Legacy `?tab=` links redirect first; only once there is nothing to redirect
// does the overview itself render.
function OverviewPage() {
  const project = useProject()
  const redirecting = useLegacyTabRedirect(project.projectId)
  if (redirecting) return <LoadingPanel rows={4} />
  return <ProjectOverview />
}

export default function Page() {
  return (
    <Suspense fallback={<LoadingPanel rows={4} />}>
      <OverviewPage />
    </Suspense>
  )
}
