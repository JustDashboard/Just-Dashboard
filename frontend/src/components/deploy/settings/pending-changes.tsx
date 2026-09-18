"use client"

import { useAuth } from "@/hooks/use-auth"
import type { DeploymentPendingState } from "@/lib/types"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { humanize } from "@/components/deploy/vocabulary"
import { useProject } from "@/components/deploy/project-context"

const SHOWN = 6

/**
 * Saved changes the live release does not have yet, named, with the one
 * button that applies them. Drawn once at the top of a settings section and
 * nowhere else: a dot on the Settings tab already says *that* something is
 * pending, and a warning on every card said it six times.
 */
export function PendingChanges({ pending }: { pending: DeploymentPendingState }) {
  const { can } = useAuth()
  const project = useProject()
  if (!pending.pending) return null
  const changes = pending.changes
  const shown = changes.slice(0, SHOWN)
  const busy = Boolean(project.detail.deployment.activeRun) || Boolean(project.starting)
  const count = changes.length
  return (
    <Notice
      tone="warning"
      title={
        count === 1
          ? "One saved change will apply on your next deployment"
          : `${count} saved changes will apply on your next deployment`
      }
    >
      {count > 0 && (
        <ul className="mt-1 space-y-0.5">
          {shown.map((change, index) => (
            <li key={`${change.kind}-${change.name}-${index}`} className="flex min-w-0 gap-2">
              <span className="min-w-0 truncate font-mono text-foreground/90">{change.name}</span>
              <span className="shrink-0">
                {humanize(change.kind)} · {change.change}
              </span>
            </li>
          ))}
          {count > SHOWN && <li>and {count - SHOWN} more</li>}
        </ul>
      )}
      {can("service.control") && project.normalized && !project.archived && (
        <Button
          size="xs"
          className="mt-2"
          disabled={busy}
          pending={project.starting === "deploy"}
          onClick={() => void project.start("deploy")}
        >
          {project.starting === "deploy" ? "Deploying…" : "Deploy changes"}
        </Button>
      )}
    </Notice>
  )
}
