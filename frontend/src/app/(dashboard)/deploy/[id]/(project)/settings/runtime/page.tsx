"use client"

import { useProject } from "@/components/deploy/project-context"
import { RuntimeSettings } from "@/components/deploy/settings/runtime"

export default function Page() {
  const { projectId, environmentId } = useProject()
  return <RuntimeSettings projectId={projectId} environmentId={environmentId} />
}
