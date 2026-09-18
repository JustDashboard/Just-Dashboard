"use client"

import { GamePlayers } from "@/components/deploy/game/players"
import { useProject } from "@/components/deploy/project-context"

export default function Page() {
  const project = useProject()
  return <GamePlayers projectId={project.projectId} />
}
