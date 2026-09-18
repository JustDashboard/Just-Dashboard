"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Archive, Play, StopCircle, Trash } from "@/components/icons"
import { del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import type { DeploymentRemovalPlan, DeploymentRemovalTarget } from "@/lib/types"
import { Well } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { humanize, projectState } from "@/components/deploy/vocabulary"
import { useProject } from "@/components/deploy/project-context"

/**
 * Danger zone — the four acts that change what this deployment is, not just
 * what it runs. Each is its own card so the sentence that explains it sits
 * right beside the one button that does it.
 */
export function DangerZoneSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  return (
    <ConfigurationState state={state}>
      {(configuration) => (
        <div className="space-y-6">
          <PendingChanges pending={configuration.pending} />
          <StopStartCard />
          <ArchiveCard />
          <RemovalCard />
          <PurgeCard />
        </div>
      )}
    </ConfigurationState>
  )
}

function StopStartCard() {
  const { can } = useAuth()
  const project = useProject()
  const { deployment, runtime } = project.detail
  if (
    !can("service.control") ||
    !project.normalized ||
    !deployment.liveReleaseId ||
    project.archived
  )
    return null
  // Agrees with the project header's own reading rather than the raw flag
  // alone: a container stopped from outside the dashboard leaves `stopped`
  // false, and the two cards would otherwise offer opposite verbs.
  const stopped = projectState(deployment, runtime) === "stopped"
  // Stopping takes the service down, so it carries the destructive
  // capability the Docker stop verb carries; starting it again does not.
  if (!stopped && !can("destructive")) return null
  const operation = stopped ? "start" : "stop"
  const busy = Boolean(deployment.activeRun) || Boolean(project.starting)
  return (
    <SettingCard
      tone="danger"
      title={stopped ? "Start the application" : "Stop the application"}
      action={
        <Button
          variant={stopped ? "outline" : "destructive"}
          disabled={busy}
          pending={project.starting === operation}
          onClick={() => void project.start(operation)}
        >
          {stopped ? <Play className="size-3.5" /> : <StopCircle className="size-3.5" />}
          {stopped ? "Start" : "Stop"}
        </Button>
      }
    >
      <p className="text-body text-muted-foreground">
        {stopped
          ? "Start the stopped containers and check they answer."
          : "Stop the live containers. Visitors get an error until you start it again."}
      </p>
    </SettingCard>
  )
}

function ArchiveCard() {
  const { can } = useAuth()
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  if (!can("destructive") || project.archived) return null
  return (
    <SettingCard
      tone="danger"
      title="Archive this deployment"
      action={
        <Button
          variant="destructive"
          onClick={() =>
            confirm({
              title: "Archive deployment",
              confirmLabel: "Archive deployment",
              description:
                "Triggers will be disabled. Runtime, routes, releases, linked resources, and " +
                "persistent data remain in place.",
              action: async () => {
                await post(`/deploy/${project.projectId}/archive`, {})
              },
              onDone: () => project.markArchived(),
            })
          }
        >
          <Archive className="size-3.5" /> Archive
        </Button>
      }
    >
      <p className="text-body text-muted-foreground">
        Disable automatic deployments and drop this project from the active list. Runtime, routes,
        releases and persistent data remain in place.
      </p>
      {dialog}
    </SettingCard>
  )
}

function RemovalCard() {
  const { can } = useAuth()
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  const [plan, setPlan] = useState<DeploymentRemovalPlan>()
  const [loading, setLoading] = useState(false)
  if (!can("destructive") || !project.archived) return null

  const loadPlan = async () => {
    setLoading(true)
    try {
      setPlan(await post<DeploymentRemovalPlan>(`/deploy/${project.projectId}/removal-plan`, {}))
    } catch (error) {
      notify.error("Could not read the removal plan", error)
    } finally {
      setLoading(false)
    }
  }

  const remove = (target: DeploymentRemovalTarget) => {
    if (!plan) return
    confirm({
      title: `Remove ${target.displayName}`,
      confirmLabel: "Remove managed resource",
      phrase: target.confirmationType === "typed" ? target.confirmationPhrase : undefined,
      description: (
        <div className="space-y-2">
          <p>Only this exact managed {humanize(target.kind)} target is removed.</p>
          <Well className="font-mono break-all">{target.resourceId}</Well>
          {target.data && (
            <p className="font-medium text-destructive">This target holds persistent data.</p>
          )}
        </div>
      ),
      action: async (confirmation) => {
        await post(
          `/deploy/${project.projectId}/remove-managed`,
          { planDigest: plan.digest, targetIds: [target.id] },
          { confirm: confirmation },
        )
        await loadPlan()
      },
    })
  }

  return (
    <SettingCard
      tone="danger"
      title="Remove managed resources"
      actions={
        <Button size="sm" variant="outline" onClick={() => void loadPlan()} pending={loading}>
          Preview targets
        </Button>
      }
    >
      <p className="text-body text-muted-foreground">
        Remove the containers, volumes, routes and other resources this deployment created. Linked
        and observed resources are never touched.
      </p>
      {plan &&
        (plan.targets.length === 0 ? (
          <EmptyNote>No managed resource is eligible for removal.</EmptyNote>
        ) : (
          <ul aria-label="Managed resources" className="divide-y divide-hairline">
            {plan.targets.map((target) => (
              <li
                key={target.id}
                className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-3 first:pt-0 last:pb-0"
              >
                <div className="min-w-0">
                  <p className="truncate text-body font-medium">{target.displayName}</p>
                  <p className="text-hint text-muted-foreground">
                    {humanize(target.kind)} · {target.owner} · {target.confirmationType}{" "}
                    confirmation
                    {target.data ? " · persistent data" : ""}
                  </p>
                </div>
                {plan.archived && (
                  <Button size="sm" variant="destructive" onClick={() => remove(target)}>
                    <Trash className="size-3.5" /> Remove
                  </Button>
                )}
              </li>
            ))}
          </ul>
        ))}
      {dialog}
    </SettingCard>
  )
}

function PurgeCard() {
  const { can } = useAuth()
  const router = useRouter()
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  const record = project.detail.project
  if (!can("destructive") || !project.archived || !record.archivedAt) return null
  return (
    <SettingCard
      tone="danger"
      title="Delete permanently"
      action={
        <Button
          variant="destructive"
          onClick={() =>
            confirm({
              title: `Delete ${record.name} permanently`,
              confirmLabel: "Delete permanently",
              description: (
                <div className="space-y-2">
                  <p>
                    Delete this deployment&rsquo;s saved configuration, variables, release history
                    and deployment logs. This cannot be undone.
                  </p>
                  <p>
                    Running containers, routes, images, files and persistent data on the server are
                    not touched. Remove managed resources above first if you want them removed too.
                  </p>
                </div>
              ),
              action: async () => {
                await del(`/deploy/${project.projectId}/permanent`)
              },
              onDone: () => router.push("/deploy?view=archived"),
            })
          }
        >
          <Trash className="size-3.5" /> Delete permanently
        </Button>
      }
    >
      <p className="text-body text-muted-foreground">
        Forget this deployment&rsquo;s configuration, variables and history. Host resources are left
        as they are.
      </p>
      {dialog}
    </SettingCard>
  )
}
