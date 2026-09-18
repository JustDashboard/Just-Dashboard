"use client"

import { StorageSettings } from "@/components/deploy/settings/storage"
import { useProject } from "@/components/deploy/project-context"

export default function Page() {
  const project = useProject()
  return <StorageSettings projectId={project.projectId} environmentId={project.environmentId} />
}
