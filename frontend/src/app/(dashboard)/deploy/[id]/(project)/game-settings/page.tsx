"use client"

import { GameSettings } from "@/components/deploy/game/settings"
import { useProject } from "@/components/deploy/project-context"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"

export default function Page() {
  const project = useProject()
  return (
    <SaveBarProvider>
      <GameSettings projectId={project.projectId} />
    </SaveBarProvider>
  )
}
