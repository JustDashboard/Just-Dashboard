"use client"

import { DomainsSettings } from "@/components/deploy/settings/domains"
import { useProject } from "@/components/deploy/project-context"

export default function Page() {
  const project = useProject()
  return <DomainsSettings projectId={project.projectId} environmentId={project.environmentId} />
}
