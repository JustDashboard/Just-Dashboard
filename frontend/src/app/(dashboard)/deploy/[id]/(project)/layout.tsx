"use client"

import { useParams } from "next/navigation"
import { ProjectProvider } from "@/components/deploy/project-context"
import { ProjectShell } from "@/components/deploy/project-shell"

/**
 * Every page of a project shares one read of it and one frame around it.
 * The run page sits outside this group on purpose: a deployment is its own
 * destination with a breadcrumb back, not a tab.
 */
export default function ProjectLayout({ children }: { children: React.ReactNode }) {
  const { id } = useParams<{ id: string }>()
  return (
    <ProjectProvider projectId={Number(id)}>
      <ProjectShell>{children}</ProjectShell>
    </ProjectProvider>
  )
}
