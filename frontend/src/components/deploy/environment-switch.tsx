"use client"

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useProject, type ProjectEnvironmentKind } from "@/components/deploy/project-context"

/**
 * Which of the deployment's environments every page under it reads and acts
 * on: production, or staging once there is one.
 *
 * It is a pair of toggles in the header rather than a page of its own,
 * because the two environments are the same project read twice — the same
 * pages, the same settings, the same verbs — and the one thing the reader
 * must never lose track of is which of the two a Deploy or a Save will touch.
 * The pressed half is the selected surface (§6), so the header says it on
 * every page without a word of prose. Nothing is drawn until staging exists:
 * a switch with one position is not a choice.
 */
export function EnvironmentSwitch({ className }: { className?: string }) {
  const project = useProject()
  if (!project.staging) return null
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={project.environment}
      onValueChange={(next) => next && project.selectEnvironment(next as ProjectEnvironmentKind)}
      aria-label="Environment"
      data-slot="environment-switch"
      className={className}
    >
      <ToggleGroupItem value="production" className="px-2.5 text-hint">
        Production
      </ToggleGroupItem>
      <ToggleGroupItem value="staging" className="px-2.5 text-hint">
        Staging
      </ToggleGroupItem>
    </ToggleGroup>
  )
}
