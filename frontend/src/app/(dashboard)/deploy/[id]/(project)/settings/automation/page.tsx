"use client"

import { AutomationSettings } from "@/components/deploy/settings/automation"
import { useProject } from "@/components/deploy/project-context"

export default function Page() {
  const project = useProject()
  return <AutomationSettings projectId={project.projectId} environmentId={project.environmentId} />
}
