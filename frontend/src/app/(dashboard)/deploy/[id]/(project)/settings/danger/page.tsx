"use client"

import { DangerZoneSettings } from "@/components/deploy/settings/danger"
import { useProject } from "@/components/deploy/project-context"

export default function Page() {
  const project = useProject()
  return <DangerZoneSettings projectId={project.projectId} environmentId={project.environmentId} />
}
