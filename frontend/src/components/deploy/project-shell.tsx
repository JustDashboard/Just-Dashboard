"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  Archive,
  ArrowLeft,
  ArrowRight,
  Box,
  CloudUpload,
  Copy,
  External,
  GitTag,
  Play,
  RefreshClockwise,
  RotateCounterClockwise,
  StopCircle,
  Trash,
} from "@/components/icons"
import { del, get } from "@/lib/api"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentGitWatch } from "@/lib/types"
import { Page, PageHeader } from "@/components/page"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { useProject, type ProjectOperation } from "@/components/deploy/project-context"
import { PROJECT_NAV, PROJECT_SETTINGS_NAV } from "@/components/nav"
import { useNavScope } from "@/components/nav-scope"
import { DeployVersionDialog } from "@/components/deploy/deploy-version-dialog"
import { DuplicateProjectDialog } from "@/components/deploy/duplicate-dialog"
import { ProjectMark } from "@/components/deploy/project-mark"
import {
  ProjectStatus,
  deploymentURL,
  hostOf,
  liveReleaseLine,
  projectState,
  sourceLine,
} from "@/components/deploy/vocabulary"

/**
 * The project's frame: its name and state, the one command, and the facts a
 * visitor asks first.
 *
 * Its pages are not here. A project is a place with fourteen destinations, and
 * a strip of tabs across the top of each of them could hold eight before it
 * scrolled sideways — so the rail drills into the project instead, which is
 * where the section above it already goes. What this registers is that list;
 * what it draws is the header over whichever of those pages you picked.
 *
 * Nothing here is framed. The name and the primary command sit in the page
 * header the way every page's do; what the project *is* — its address, its
 * branch, its live release, whether it deploys itself — is a row of facts
 * under the title, as the host Overview does with its platform and kernel.
 */

const PROGRESSIVE: Record<ProjectOperation, string> = {
  deploy: "Deploying…",
  redeploy: "Redeploying…",
  restart: "Restarting…",
  force_build: "Rebuilding…",
  stop: "Stopping…",
  start: "Starting…",
}

export function ProjectShell({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [versionOpen, setVersionOpen] = useState(false)
  const [duplicateOpen, setDuplicateOpen] = useState(false)
  const project = useProject()
  const { deployment, project: record, runtime } = project.detail
  const state = projectState(deployment, runtime, project.archived)
  const url = deploymentURL(deployment.endpoint)
  const source = sourceLine(deployment, record, project.liveRun ?? deployment.lastRun)
  const activeRun = deployment.activeRun
  const canRun = can("service.control") && project.normalized && !project.archived
  const busy = Boolean(activeRun) || Boolean(project.starting)
  const base = `/deploy/${project.projectId}`

  const watch = usePoll(
    (signal) =>
      get<DeploymentGitWatch>(
        `${base}/environments/${project.environmentId}/git-watch`,
        undefined,
        signal,
      ),
    15000,
    [project.projectId, project.environmentId],
    {
      enabled:
        project.normalized &&
        deployment.sourceKind === "git" &&
        project.environmentId > 0 &&
        !project.archived,
    },
  )

  // The rail's third level: inside Deployments, inside this project. It is
  // registered from here because the three things the rail cannot read off the
  // URL are all in this component's hands — what the project is called, that a
  // game server has two pages nothing else has, and that its configuration has
  // moved on from what is live.
  useNavScope({
    path: base,
    title: record.name,
    caption: url ? hostOf(url) : undefined,
    icon: CloudUpload,
    groups: [
      {
        items: PROJECT_NAV.filter((entry) => !entry.game || deployment.profile === "game").map(
          (entry) => ({
            title: entry.title,
            href: `${base}${entry.path}`,
            icon: entry.icon,
          }),
        ),
      },
      {
        label: "Settings",
        pending: deployment.pendingChanges && !project.archived,
        items: PROJECT_SETTINGS_NAV.map((entry) => ({
          title: entry.title,
          href: `${base}${entry.path}`,
          icon: entry.icon,
        })),
      },
    ],
  })

  const primary = (() => {
    if (project.archived) return null
    if (activeRun)
      return (
        <Button size="sm" asChild>
          <Link href={`${base}/runs/${activeRun.id}`}>
            View deployment <ArrowRight className="size-3.5" />
          </Link>
        </Button>
      )
    if (!can("service.control")) return null
    const operation: ProjectOperation =
      state === "stopped"
        ? "start"
        : !deployment.liveReleaseId || deployment.pendingChanges || !project.normalized
          ? "deploy"
          : "redeploy"
    const label =
      operation === "start"
        ? "Start"
        : !deployment.liveReleaseId
          ? "Deploy"
          : deployment.pendingChanges
            ? "Deploy changes"
            : "Redeploy"
    return (
      <Button
        size="sm"
        disabled={busy}
        pending={project.starting === operation}
        onClick={() => void project.start(operation)}
      >
        {operation === "start" ? (
          <Play className="size-3.5" />
        ) : deployment.liveReleaseId ? (
          <RefreshClockwise className="size-3.5" />
        ) : (
          <Play className="size-3.5" />
        )}
        {project.starting === operation ? PROGRESSIVE[operation] : label}
      </Button>
    )
  })()

  // Stopping or restarting takes the service down, so like the Docker
  // container verbs it needs the destructive capability; starting a stopped
  // deployment only needs service control.
  const canInterrupt = canRun && can("destructive")
  const verbs: Verb[] = []
  if (canRun && deployment.liveReleaseId) {
    if (state === "stopped") {
      verbs.push({
        key: "start",
        label: "Start",
        detail: "Start the stopped containers and check they answer.",
        icon: Play,
        disabled: busy,
        run: () => void project.start("start"),
      })
    } else if (canInterrupt) {
      verbs.push({
        key: "restart",
        label: "Restart",
        detail: "Stop and start the live release, then check it answers.",
        icon: RefreshClockwise,
        disabled: busy,
        run: () => void project.start("restart"),
      })
      verbs.push({
        key: "stop",
        label: "Stop",
        detail: "Stop the live containers. Visitors get an error until you start it again.",
        icon: StopCircle,
        disabled: busy,
        run: () => void project.start("stop"),
      })
    }
    if (deployment.pendingChanges) {
      verbs.push({
        key: "redeploy",
        label: "Redeploy live release",
        detail: "Run the live release again, without the pending changes.",
        icon: RotateCounterClockwise,
        disabled: busy,
        run: () => void project.start("redeploy"),
      })
    }
  }
  if (canRun) {
    verbs.push({
      key: "force_build",
      label: "Rebuild without cache",
      detail: "Build a fresh image from the saved plan, ignoring the cache.",
      icon: Box,
      disabled: busy,
      run: () => void project.start("force_build"),
    })
  }
  if (canRun && deployment.sourceKind === "git") {
    verbs.push({
      key: "version",
      label: "Deploy a specific version…",
      detail: "Build a branch, a tag or a commit instead of the configured branch.",
      icon: GitTag,
      disabled: busy,
      run: () => setVersionOpen(true),
    })
  }
  if (can("system.admin") && !project.archived) {
    verbs.push({
      key: "duplicate",
      label: "Duplicate project…",
      detail: "Copy its source, build and runtime settings into a new draft.",
      icon: Copy,
      run: () => setDuplicateOpen(true),
    })
  }
  if (runtime?.status === "available" && runtime.services.length > 0) {
    const live = runtime.services.find((service) => service.liveRelease) ?? runtime.services[0]
    verbs.push({
      key: "docker",
      label: "Open in Docker",
      detail: "The live containers, as Docker sees them.",
      icon: External,
      run: () =>
        router.push(`/docker/containers?${new URLSearchParams({ container: live.containerId })}`),
    })
  }
  if (can("destructive") && !project.archived) {
    verbs.push({
      key: "archive",
      label: "Archive deployment",
      detail: "Disable automation and keep runtime and history.",
      icon: Archive,
      danger: true,
      disabled: busy,
      run: () =>
        confirm({
          title: `Archive ${record.name}`,
          confirmLabel: "Archive deployment",
          description: (
            <>
              <p>
                Remove this project from active deployments and disable its automatic deployments?
                Its archived history is retained.
              </p>
              <p>
                Running containers, routes, and persistent data remain. To remove managed resources
                too, use Settings → Danger zone.
              </p>
            </>
          ),
          action: async () => {
            await del(`/deploy/${project.projectId}`)
          },
          onDone: () => router.push("/deploy"),
        }),
    })
  }
  if (can("destructive") && project.archived && record.archivedAt) {
    verbs.push({
      key: "purge",
      label: "Delete permanently",
      detail: "Forget this deployment's configuration, variables and history.",
      icon: Trash,
      danger: true,
      run: () =>
        confirm({
          title: `Permanently delete ${record.name}?`,
          confirmLabel: "Delete permanently",
          description: (
            <div className="space-y-3">
              <p>
                Delete this deployment’s saved configuration, variables, release history, and
                deployment logs. This cannot be undone.
              </p>
              <p>
                Running containers, routes, images, files, and persistent data remain on the server.
                The dashboard will forget their deployment ownership. Remove managed resources from
                Settings → Danger zone first if you want them removed too.
              </p>
            </div>
          ),
          action: async () => {
            await del(`/deploy/${project.projectId}/permanent`)
          },
          onDone: () => router.push("/deploy?view=archived"),
        }),
    })
  }

  return (
    <Page>
      {/* The header and the facts under it are one block: the facts are the
          title's second line, not a section of their own. */}
      <div className="space-y-3">
        <PageHeader
          eyebrow={
            <Link
              href="/deploy"
              className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
            >
              <ArrowLeft className="size-3" /> Deployments
            </Link>
          }
          title={
            <span className="inline-flex max-w-full min-w-0 items-center gap-3">
              <ProjectMark deployment={deployment} size="sm" />
              <span className="truncate">{record.name}</span>
              <ProjectStatus
                summary={deployment}
                runtime={runtime}
                archived={project.archived}
                live={Boolean(activeRun)}
                className="shrink-0"
              />
            </span>
          }
          actions={
            <>
              {project.archived && <Tag>Archived</Tag>}
              {url && (
                <Button variant="outline" size="sm" asChild>
                  <a href={url} target="_blank" rel="noopener noreferrer">
                    <External className="size-3.5" /> Visit
                  </a>
                </Button>
              )}
              {primary}
              {verbs.length > 0 && <VerbMenu verbs={verbs} label="Deployment actions" />}
            </>
          }
        />

        {/* What the project is: where it answers, what it was built from, what
          is live, and whether it deploys itself. A row of facts under the
          title, not a fourth panel. */}
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
          {url ? (
            <a
              href={url}
              target="_blank"
              rel="noopener noreferrer"
              className="truncate rounded-sm text-foreground focus-ring hover:underline"
            >
              {hostOf(url)}
            </a>
          ) : (
            <span>{deployment.liveReleaseId ? "Private service" : "No public address yet"}</span>
          )}
          <Dot />
          <span className="truncate">
            {source.primary}
            {source.secondary && (
              <span className="text-muted-foreground/80">
                {" · "}
                {source.secondary}
              </span>
            )}
          </span>
          <Dot />
          {project.liveRelease ? (
            <span className="truncate">
              <span className="numeric">Release #{project.liveRelease.number}</span>
              {project.liveRun && <> · {liveReleaseLine(project.liveRun)}</>}
            </span>
          ) : (
            <span>{deployment.liveReleaseId ? "Live release" : "Not deployed yet"}</span>
          )}
          {watch.data && watch.data.status !== "not_applicable" && (
            <>
              <Dot />
              <Link
                href={`${base}/settings/general`}
                className="rounded-sm focus-ring"
                aria-label="Automatic deployment settings"
              >
                <GitWatchStatus watch={watch.data} />
              </Link>
            </>
          )}
        </div>
      </div>

      {children}
      {dialog}
      <DeployVersionDialog
        open={versionOpen}
        onOpenChange={setVersionOpen}
        projectId={project.projectId}
        environmentId={project.environmentId}
        branch={deployment.sourceRef}
      />
      <DuplicateProjectDialog
        open={duplicateOpen}
        onOpenChange={setDuplicateOpen}
        projectId={project.projectId}
        name={record.name}
      />
    </Page>
  )
}

function Dot() {
  return (
    <span aria-hidden="true" className="text-muted-foreground/40">
      ·
    </span>
  )
}

/** Whether the branch deploys itself, as one status word in the facts row. */
function GitWatchStatus({ watch }: { watch: DeploymentGitWatch }) {
  const automatic = watch.policy?.automatic ?? watch.automatic
  if (["unavailable", "stale", "policy_conflict"].includes(watch.status))
    return <Status tone="warning" label="Auto-deploy needs attention" />
  if (!automatic) return <Status tone="stopped" label="Manual deployments" />
  if (watch.status === "awaiting_first_deployment")
    return <Status tone="stopped" label="Auto-deploy after first deployment" />
  return <Status tone="running" label="Auto-deploy on" />
}
