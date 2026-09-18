"use client"

import { useProject } from "@/components/deploy/project-context"
import { VariablesPanel } from "@/components/deploy/settings/variables"

export default function Page() {
  const { projectId, environmentId } = useProject()
  return <VariablesPanel projectId={projectId} environmentId={environmentId} />
}
