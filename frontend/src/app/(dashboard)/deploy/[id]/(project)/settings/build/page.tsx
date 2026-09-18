"use client"

import { useProject } from "@/components/deploy/project-context"
import { BuildSettings } from "@/components/deploy/settings/build"

export default function Page() {
  const { projectId, environmentId } = useProject()
  return <BuildSettings projectId={projectId} environmentId={environmentId} />
}
