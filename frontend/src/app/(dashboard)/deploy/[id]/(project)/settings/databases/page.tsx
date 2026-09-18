"use client"

import { DatabasesSettings } from "@/components/deploy/settings/databases"
import { useProject } from "@/components/deploy/project-context"

export default function Page() {
  const project = useProject()
  return <DatabasesSettings projectId={project.projectId} environmentId={project.environmentId} />
}
