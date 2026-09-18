"use client"

import { GameSettings } from "@/components/deploy/game/settings"
import { useProject } from "@/components/deploy/project-context"

export default function Page() {
  const project = useProject()
  return <GameSettings projectId={project.projectId} />
}
