"use client"

import { useProject } from "@/components/deploy/project-context"
import { GeneralSettings } from "@/components/deploy/settings/general"

export default function Page() {
  const { projectId, environmentId } = useProject()
  return <GeneralSettings projectId={projectId} environmentId={environmentId} />
}
