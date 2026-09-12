"use client"

import { useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import {
  ArrowLeft,
  ArrowRight,
  Box,
  CloudUpload,
  Database,
  GitBranch,
  Globe,
  Key,
  Logs,
  Monitoring,
  Play,
  RefreshClockwise,
  RotateCounterClockwise,
  SettingsSliders,
  Trash,
} from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEngineRun,
  DeploymentRelease,
  DeploymentRunSnapshot,
  DeploymentSummary,
  DeploymentOperations,
  DeploymentRuntimeServices,
  DeployCommit,
  DeployProject,
  EnvVar,
} from "@/lib/types"
import { Page, PageHeader, Metric, MetricStrip } from "@/components/page"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import {
  DeploymentStatus,
  HealthStatus,
  ProjectTabs,
  ReleasePath,
  humanize,
  reachableAt,
} from "@/components/deploy/deployment-ui"
import {
  NormalizedConfigurationTab,
  NormalizedNetworkTab,
  NormalizedStorageTab,
  NormalizedVariablesTab,
} from "@/components/deploy/deployment-configuration"
import { DeploymentAutomation } from "@/components/deploy/deployment-automation"
import { DeploymentRuntime } from "@/components/deploy/deployment-runtime"
import {
  DeploymentDependencies,
  DeploymentDomains,
  DeploymentFindings,
  DeploymentStorage,
} from "@/components/deploy/deployment-operations"
import { DeploymentReleaseComparison } from "@/components/deploy/deployment-release-comparison"
import { DeploymentRunMetrics } from "@/components/deploy/deployment-run-metrics"
import {
  GameConsoleTab,
  GamePlayersTab,
  GameSettingsTab,
} from "@/components/deploy/deployment-game"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"

type DeploymentDetail = {
  project: DeployProject
  running: boolean
  deployment: DeploymentSummary
  runtime?: DeploymentRuntimeServices
}

type RunsResponse = { runs: DeploymentEngineRun[]; running: boolean }

const VALID_TABS = new Set([
  "overview",
  "deployments",
  "logs",
  "configuration",
  "variables",
  "network",
  "storage",
  "automations",
  "metrics",
  "console",
  "players",
  "settings",
])

export function DeploymentWorkspace() {
  const route = useParams<{ id: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const { can } = useAuth()
  const deletion = useConfirm()
  const projectID = Number(route.id)
  const [archived, setArchived] = useState(false)
  const requestedTab = search.get("tab") ?? "overview"
  const activeTab = VALID_TABS.has(requestedTab) ? requestedTab : "overview"
  const detail = usePoll(
    (signal) => get<DeploymentDetail>(`/deploy/${projectID}`, undefined, signal),
    5000,
    [projectID],
    { enabled: Number.isInteger(projectID) && projectID > 0 && !archived },
  )
  const runs = usePoll(
    (signal) => get<RunsResponse>(`/deploy/${projectID}/runs`, { view: "engine" }, signal),
    5000,
    [projectID],
    { enabled: Number.isInteger(projectID) && projectID > 0 },
  )
  const environmentID = detail.data?.deployment.environmentId ?? 0
  const releases = usePoll(
    (signal) =>
      get<DeploymentRelease[]>(
        `/deploy/${projectID}/environments/${environmentID}/releases`,
        { limit: 30 },
        signal,
      ),
    5000,
    [projectID, environmentID],
    { enabled: projectID > 0 && environmentID > 0 },
  )
  // Operational evidence reads five feature owners, so it polls on its own
  // slower cadence rather than riding the five-second workspace poll.
  const operations = usePoll(
    (signal) => get<DeploymentOperations>(`/deploy/${projectID}/operations`, undefined, signal),
    20000,
    [projectID],
    { enabled: Number.isInteger(projectID) && projectID > 0 && !archived },
  )
  const [starting, setStarting] = useState(false)

  const start = async (operation: "deploy" | "redeploy" | "restart" | "force_build") => {
    const deployment = detail.data?.deployment
    if (!deployment) return
    setStarting(true)
    try {
      const run = await post<DeploymentEngineRun>(
        `/deploy/${projectID}/environments/${deployment.environmentId}/runs`,
        { operation },
      )
      router.push(`/deploy/${projectID}/runs/${run.id}`)
    } catch (error) {
      notify.error("Could not start deployment", error)
      setStarting(false)
    }
  }

  if (detail.loading && !detail.data) {
    return (
      <Page>
        <PageHeader eyebrow="Deployments" title="Loading deployment" />
        <LoadingPanel rows={6} />
      </Page>
    )
  }
  if (detail.error || !detail.data) {
    return (
      <Page>
        <PageHeader eyebrow="Deployments" title="Deployment unavailable" />
        {detail.error && <ErrorState error={detail.error} />}
        <Button variant="outline" size="sm" asChild>
          <Link href="/deploy">
            <ArrowLeft className="size-3.5" /> Back to deployments
          </Link>
        </Button>
      </Page>
    )
  }

  const { deployment, project } = detail.data
  const isArchived = archived || Boolean(project.archivedAt)
  const activeRun = deployment.activeRun
  const normalized = deployment.buildMethod !== "legacy_compose"
  const canStart = can("service.control")
  const primaryOperation =
    normalized && deployment.liveReleaseId && !deployment.pendingChanges ? "redeploy" : "deploy"
  const primaryLabel = deployment.liveReleaseId
    ? deployment.pendingChanges
      ? "Deploy changes"
      : "Redeploy"
    : "Deploy"

  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link href="/deploy" className="inline-flex items-center gap-1 hover:underline">
            <ArrowLeft className="size-3" /> Deployments
          </Link>
        }
        title={
          <span className="inline-flex min-w-0 items-center gap-2">
            <span className="truncate">{deployment.name}</span>
            <span className="text-muted-foreground">/</span>
            <span className="text-sm font-normal text-muted-foreground">
              {deployment.environmentName}
            </span>
          </span>
        }
        actions={
          <>
            {/* That the saved plan is ahead of what is live was the page
                header's description. It is a state, so it is a `Status` —
                the nav's per-tab dots say *which* tabs changed, and this says
                that anything did. */}
            {!isArchived && deployment.pendingChanges && (
              <Status tone="warning" label="Pending deployment" />
            )}
            <HealthStatus health={deployment.health} />
            {isArchived ? (
              <Tag>Archived</Tag>
            ) : activeRun ? (
              <Button size="sm" asChild>
                <Link href={`/deploy/${projectID}/runs/${activeRun.id}`}>
                  View deployment <ArrowRight className="size-3.5" />
                </Link>
              </Button>
            ) : (
              <Button
                size="sm"
                disabled={!canStart || starting}
                onClick={() => start(primaryOperation)}
              >
                {deployment.liveReleaseId ? (
                  <RefreshClockwise className="size-3.5" />
                ) : (
                  <Play className="size-3.5" />
                )}
                {starting ? "Starting…" : primaryLabel}
              </Button>
            )}
            {!isArchived &&
              !activeRun &&
              normalized &&
              deployment.liveReleaseId &&
              deployment.pendingChanges && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!canStart || starting}
                  onClick={() => start("redeploy")}
                >
                  <RefreshClockwise className="size-3.5" /> Redeploy live
                </Button>
              )}
            {!isArchived && !activeRun && normalized && deployment.liveReleaseId && (
              <Button
                size="sm"
                variant="outline"
                disabled={!canStart || starting}
                onClick={() => start("restart")}
              >
                <RefreshClockwise className="size-3.5" /> Restart
              </Button>
            )}
            {!isArchived && !activeRun && normalized && (
              <Button
                size="sm"
                variant="outline"
                disabled={!canStart || starting}
                onClick={() => start("force_build")}
              >
                <Box className="size-3.5" /> Force build
              </Button>
            )}
            {!isArchived && can("destructive") && (
              <Button
                size="sm"
                variant="outline"
                disabled={Boolean(activeRun) || starting}
                title={
                  activeRun
                    ? "Wait for the active deployment to finish or cancel it first"
                    : undefined
                }
                onClick={() =>
                  deletion.confirm({
                    title: `Delete ${project.name}`,
                    confirmLabel: "Delete project",
                    description: (
                      <>
                        <p>
                          Remove this project from active deployments and disable its automatic
                          deployments? Its archived history is retained.
                        </p>
                        <p>
                          Running containers, routes, and persistent data remain. To remove managed
                          resources too, use Configuration → Archive &amp; managed resources.
                        </p>
                      </>
                    ),
                    action: async () => {
                      await del(`/deploy/${projectID}`)
                    },
                    onDone: () => router.push("/deploy"),
                  })
                }
              >
                <Trash className="size-3.5" /> Delete project
              </Button>
            )}
          </>
        }
      />

      <MetricStrip className="rounded-lg border border-hairline bg-card px-4 py-3">
        <Metric label="Environment" value={humanize(deployment.environmentKind)} />
        <Metric label="Reachable at" value={reachableAt(deployment)} />
        <Metric label="Release strategy" value={humanize(deployment.strategy)} />
        <Metric
          label="Plan"
          value={`Revision ${deployment.desiredRevision}`}
          hint={deployment.pendingChanges ? "Not live yet" : "Live"}
        />
      </MetricStrip>

      <ProjectTabs
        projectId={projectID}
        active={activeTab}
        pending={deployment.pendingChanges}
        profile={deployment.profile}
      />

      {activeTab === "overview" && (
        <Overview
          deployment={deployment}
          project={project}
          runs={runs.data?.runs ?? []}
          runtime={detail.data.runtime}
          operations={operations.data}
          operationsLoading={operations.loading}
        />
      )}
      {activeTab === "deployments" && (
        <DeploymentsTab
          project={project}
          runs={runs.data?.runs ?? []}
          loading={runs.loading}
          legacy={deployment.buildMethod === "legacy_compose"}
          active={Boolean(deployment.activeRun)}
          deployment={deployment}
          releases={releases.data ?? []}
          releasesLoading={releases.loading}
        />
      )}
      {activeTab === "logs" && <DeploymentRuntime runtime={detail.data.runtime} />}
      {activeTab === "configuration" &&
        (normalized ? (
          <NormalizedConfigurationTab
            projectID={projectID}
            environmentID={environmentID}
            onArchived={() => setArchived(true)}
          />
        ) : (
          <LegacyConfigurationTab deployment={deployment} project={project} />
        ))}
      {activeTab === "variables" &&
        (normalized ? (
          <NormalizedVariablesTab projectID={projectID} environmentID={environmentID} />
        ) : (
          <LegacyVariablesTab projectID={projectID} />
        ))}
      {activeTab === "network" &&
        (normalized ? (
          <NormalizedNetworkTab projectID={projectID} environmentID={environmentID} />
        ) : (
          <OwnedFeatureTab kind="network" deployment={deployment} />
        ))}
      {activeTab === "storage" &&
        (normalized ? (
          <NormalizedStorageTab
            projectID={projectID}
            environmentID={environmentID}
            latestRunID={(runs.data?.runs[0] ?? deployment.lastRun)?.id}
          />
        ) : (
          <OwnedFeatureTab kind="storage" deployment={deployment} />
        ))}
      {activeTab === "automations" && (
        <DeploymentAutomation
          projectID={project.id}
          environmentID={environmentID}
          legacyHook={project.hookUrl}
          legacyEnabled={project.enabled}
          normalized={normalized}
        />
      )}
      {activeTab === "metrics" && (
        <DeploymentMetricsTab deployment={deployment} runs={runs.data?.runs ?? []} />
      )}
      {activeTab === "console" &&
        (deployment.profile === "game" ? (
          <GameConsoleTab projectID={projectID} />
        ) : (
          <OwnedFeatureTab kind="console" deployment={deployment} />
        ))}
      {activeTab === "players" && deployment.profile === "game" && (
        <GamePlayersTab projectID={projectID} />
      )}
      {activeTab === "settings" && deployment.profile === "game" && (
        <GameSettingsTab projectID={projectID} />
      )}
      {deletion.dialog}
    </Page>
  )
}

function Overview({
  deployment,
  project,
  runs,
  runtime,
  operations,
  operationsLoading,
}: {
  deployment: DeploymentSummary
  project: DeployProject
  runs: DeploymentEngineRun[]
  runtime?: DeploymentRuntimeServices
  operations?: DeploymentOperations
  operationsLoading: boolean
}) {
  const lastRun = runs[0] ?? deployment.lastRun
  return (
    <div className="grid min-w-0 gap-4 xl:grid-cols-2">
      <Panel className="xl:col-span-2">
        <PanelHeader
          icon={CloudUpload}
          title="Release path"
          actions={
            lastRun && (
              <Button variant="outline" size="xs" asChild>
                <Link href={`/deploy/${deployment.id}/runs/${lastRun.id}`}>Open run</Link>
              </Button>
            )
          }
        />
        <PanelBody>
          {lastRun ? (
            <LastReleasePath projectID={deployment.id} runID={lastRun.id} />
          ) : (
            <ReleasePath steps={[]} />
          )}
          {!lastRun && (
            <p className="mt-4 text-xs text-muted-foreground">
              The persisted release path will fill as the first deployment runs.
            </p>
          )}
        </PanelBody>
      </Panel>

      <DeploymentFindings diagnosis={operations?.diagnosis} loading={operationsLoading} />

      <DeploymentRuntime runtime={runtime} />
      <SummaryPanel
        icon={GitBranch}
        title="Source & automation"
        rows={[
          ["Source", deployment.sourceRef || project.repoPath || "Not recorded"],
          ["Revision", deployment.sourceRevision || project.branch || "Not recorded"],
          ["Build", humanize(deployment.buildMethod)],
          ["Deploy on push", project.enabled ? "Enabled" : "Disabled"],
        ]}
        href={`/deploy/${deployment.id}?tab=automations`}
      />
      <DeploymentDomains operations={operations} />
      <DeploymentStorage operations={operations} />
      <DeploymentDependencies operations={operations} />
      <SummaryPanel
        icon={Monitoring}
        title="Metrics since release"
        rows={[
          [
            "Attribution",
            lastRun
              ? "Recorded per container around activation"
              : "Available after the first deployment",
          ],
          ["Host history", "Retained by the Metrics module"],
        ]}
        href={`/deploy/${deployment.id}?tab=metrics`}
        actionLabel="Open release metrics"
      />
      <SummaryPanel
        icon={Logs}
        title="Runtime logs"
        rows={[
          [
            "Sources",
            runtime?.status === "available"
              ? `${runtime.services.length} managed runtime services`
              : "Docker evidence unavailable",
          ],
        ]}
        href={`/deploy/${deployment.id}?tab=logs`}
        actionLabel="Choose runtime logs"
      />
    </div>
  )
}

/**
 * Release metrics are the run's own comparison around activation. The Metrics
 * module remains the owner of host history; this tab links into it rather than
 * charting a second copy.
 */
function DeploymentMetricsTab({
  deployment,
  runs,
}: {
  deployment: DeploymentSummary
  runs: DeploymentEngineRun[]
}) {
  const lastRun = runs[0] ?? deployment.lastRun
  if (!lastRun) {
    return (
      <Panel>
        <PanelHeader icon={Monitoring} title="Metrics around activation" />
        <PanelBody>
          <EmptyState
            icon={Monitoring}
            title="No deployment to attribute"
            description="Release metrics compare the ten minutes before an activation with the ten minutes after it. The first deployment records the first comparison."
            className="border-0 py-6"
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href="/metrics">Open Metrics</Link>
              </Button>
            }
          />
        </PanelBody>
      </Panel>
    )
  }
  return <DeploymentRunMetrics projectID={deployment.id} runID={lastRun.id} />
}

function LastReleasePath({ projectID, runID }: { projectID: number; runID: number }) {
  const snapshot = usePoll(
    (signal) => get<DeploymentRunSnapshot>(`/deploy/${projectID}/runs/${runID}`, undefined, signal),
    0,
    [projectID, runID],
  )
  if (snapshot.error) {
    return <p className="text-xs text-muted-foreground">Release evidence could not be loaded.</p>
  }
  return <ReleasePath steps={snapshot.data?.steps ?? []} />
}

function SummaryPanel({
  icon,
  title,
  rows,
  href,
  actionLabel = "Open section",
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  rows: [string, string][]
  href: string
  actionLabel?: string
}) {
  return (
    <Panel>
      <PanelHeader
        icon={icon}
        title={title}
        actions={
          <Button variant="ghost" size="xs" asChild>
            <Link href={href}>
              {actionLabel} <ArrowRight className="size-3" />
            </Link>
          </Button>
        }
      />
      <PanelBody className="space-y-2.5">
        {rows.map(([label, value]) => (
          <div key={label} className="grid min-w-0 grid-cols-[8rem_minmax(0,1fr)] gap-3 text-xs">
            <span className="text-muted-foreground">{label}</span>
            <span className="min-w-0 text-right font-medium break-words" title={value}>
              {value}
            </span>
          </div>
        ))}
      </PanelBody>
    </Panel>
  )
}

function DeploymentsTab({
  project,
  runs,
  loading,
  legacy,
  active,
  deployment,
  releases,
  releasesLoading,
}: {
  project: DeployProject
  runs: DeploymentEngineRun[]
  loading: boolean
  legacy: boolean
  active: boolean
  deployment: DeploymentSummary
  releases: DeploymentRelease[]
  releasesLoading: boolean
}) {
  if (loading && runs.length === 0) return <LoadingPanel rows={5} />
  return (
    <div className="space-y-4">
      <Panel>
        <PanelHeader
          icon={CloudUpload}
          title="Deployment history"
        />
        <PanelBody flush>
          {runs.length === 0 ? (
            <EmptyState
              icon={CloudUpload}
              title="No deployments yet"
              description="The first deployment will appear here as soon as its request is accepted."
              className="m-4"
            />
          ) : (
            <ul className="divide-y divide-hairline">
              {runs.map((run) => (
                <li key={run.id}>
                  <Link
                    href={`/deploy/${project.id}/runs/${run.id}`}
                    className="grid min-h-14 min-w-0 gap-1 px-4 py-3 hover:bg-row-hover sm:grid-cols-[8rem_minmax(0,1fr)_8rem_auto] sm:items-center sm:gap-4"
                  >
                    <span className="font-mono text-xs font-medium">Run #{run.id}</span>
                    <span className="min-w-0 truncate text-xs text-muted-foreground">
                      {humanize(run.operation)} · {run.trigger} · {run.actor}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {relativeTime(run.requestedAt)}
                    </span>
                    <DeploymentStatus state={run.state} />
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
      {legacy && <RollbackPanel project={project} active={active} />}
      {!legacy && deployment.liveReleaseId && deployment.environmentId > 0 && (
        <DeploymentReleaseComparison
          projectID={project.id}
          environmentID={deployment.environmentId}
          releaseID={deployment.liveReleaseId}
        />
      )}
      {!legacy && (
        <ReleaseRecoveryPanel
          project={project}
          deployment={deployment}
          releases={releases}
          loading={releasesLoading}
          active={active}
        />
      )}
    </div>
  )
}

function ReleaseRecoveryPanel({
  project,
  deployment,
  releases,
  loading,
  active,
}: {
  project: DeployProject
  deployment: DeploymentSummary
  releases: DeploymentRelease[]
  loading: boolean
  active: boolean
}) {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  return (
    <>
      <Panel>
        <PanelHeader
          icon={RotateCounterClockwise}
          title="Immutable releases"
        />
        <PanelBody className="space-y-2">
          {loading && releases.length === 0 && (
            <p className="text-xs text-muted-foreground">Loading retained releases…</p>
          )}
          {releases.map((release) => (
            <Group
              key={release.id}
              className="flex min-w-0 flex-wrap items-center justify-between gap-3"
            >
              <div className="min-w-0 flex-1">
                <p className="text-body font-medium">Release #{release.number}</p>
                <p className="truncate font-mono text-hint text-muted-foreground">
                  {release.sourceRevision?.slice(0, 12) ||
                    release.imageDigest?.slice(0, 19) ||
                    release.configDigest.slice(0, 19)}{" "}
                  · plan {release.planRevision} · {relativeTime(release.createdAt)}
                </p>
              </div>
              {release.id === deployment.liveReleaseId ? (
                <Status verdict="ok" label="Live" />
              ) : release.state === "retained" && can("destructive") ? (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={active}
                  onClick={() =>
                    confirm({
                      title: `Roll back to release #${release.number}`,
                      confirmLabel: "Roll back",
                      description: (
                        <p>
                          The retained artifact will become a new candidate. The current release
                          remains the recovery point until all required checks pass.
                        </p>
                      ),
                      action: async () => {
                        const run = await post<DeploymentEngineRun>(
                          `/deploy/${project.id}/environments/${deployment.environmentId}/rollback`,
                          { releaseId: release.id },
                        )
                        router.push(`/deploy/${project.id}/runs/${run.id}`)
                      },
                    })
                  }
                >
                  <RotateCounterClockwise className="size-3.5" /> Roll back
                </Button>
              ) : (
                <span className="text-xs text-muted-foreground">{humanize(release.state)}</span>
              )}
            </Group>
          ))}
          {!loading && releases.length === 0 && (
            <p className="text-xs text-muted-foreground">
              No immutable releases have been recorded yet.
            </p>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </>
  )
}

function RollbackPanel({ project, active }: { project: DeployProject; active: boolean }) {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const commits = usePoll(
    (signal) => get<DeployCommit[]>(`/deploy/${project.id}/commits`, { limit: 25 }, signal),
    0,
    [project.id],
  )
  return (
    <>
      <Panel>
        <PanelHeader
          icon={RotateCounterClockwise}
          title="Recovery"
        />
        <PanelBody className="space-y-2">
          {commits.loading && !commits.data && (
            <p className="text-xs text-muted-foreground">Loading recoverable revisions…</p>
          )}
          {commits.error && <ErrorState error={commits.error} />}
          {commits.data?.map((commit) => (
            <Group
              key={commit.sha}
              className="flex min-w-0 flex-wrap items-center justify-between gap-3"
            >
              <div className="min-w-0 flex-1">
                <p className="truncate text-body">{commit.subject}</p>
                <p className="truncate font-mono text-hint text-muted-foreground">
                  {commit.short} · {commit.author} · {relativeTime(commit.date)}
                </p>
              </div>
              {commit.sha === project.currentSha ? (
                <Status verdict="ok" label="Current" />
              ) : (
                can("destructive") && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={active}
                    title={
                      active
                        ? "Wait for the active deployment to finish or cancel it first."
                        : undefined
                    }
                    onClick={() =>
                      confirm({
                        title: "Roll back",
                        confirmLabel: "Roll back",
                        description: (
                          <>
                            <p>
                              <b>{project.name}</b> will rebuild {commit.short}. The current
                              workload remains the recovery point until activation.
                            </p>
                            <p className="text-xs text-muted-foreground">{commit.subject}</p>
                          </>
                        ),
                        action: async (confirmation) => {
                          const result = await post<{ runId: number }>(
                            `/deploy/${project.id}/rollback`,
                            { commit: commit.sha },
                            { confirm: confirmation },
                          )
                          router.push(`/deploy/${project.id}/runs/${result.runId}`)
                        },
                      })
                    }
                  >
                    <RotateCounterClockwise className="size-3.5" /> Roll back
                  </Button>
                )
              )}
            </Group>
          ))}
          {commits.data?.length === 0 && <EmptyNote>No recoverable commits were found.</EmptyNote>}
        </PanelBody>
      </Panel>
      {dialog}
    </>
  )
}

function LegacyConfigurationTab({
  deployment,
  project,
}: {
  deployment: DeploymentSummary
  project: DeployProject
}) {
  return (
    <div className="grid min-w-0 gap-4 xl:grid-cols-2">
      <SummaryPanel
        icon={SettingsSliders}
        title="Normalized plan"
        rows={[
          ["Profile", humanize(deployment.profile)],
          ["Build method", humanize(deployment.buildMethod)],
          ["Source kind", humanize(deployment.sourceKind)],
          ["Desired revision", String(deployment.desiredRevision)],
          ["Release strategy", humanize(deployment.strategy)],
        ]}
        href={`/deploy/${deployment.id}?tab=deployments`}
        actionLabel="View deployments"
      />
      <SummaryPanel
        icon={GitBranch}
        title="Legacy Compose compatibility"
        rows={[
          ["Checkout", project.repoPath || "Not used"],
          ["Branch", project.branch || "Not used"],
          ["Compose file", project.composeFile || "Not used"],
          ["Pre-command", project.preCommand || "None"],
          ["Post-command", project.postCommand || "None"],
        ]}
        href={`/files?path=${encodeURIComponent(project.repoPath || "/")}`}
        actionLabel="Open checkout"
      />
    </div>
  )
}

function LegacyVariablesTab({ projectID }: { projectID: number }) {
  const variables = usePoll(
    (signal) => get<EnvVar[]>(`/deploy/${projectID}/env`, undefined, signal),
    0,
    [projectID],
  )
  return (
    <Panel>
      <PanelHeader
        icon={Key}
        title="Variables"
      />
      <PanelBody flush>
        {variables.loading && !variables.data ? (
          <div className="p-4">
            <LoadingPanel rows={3} />
          </div>
        ) : variables.error ? (
          <ErrorState className="m-4" error={variables.error} />
        ) : (variables.data?.length ?? 0) === 0 ? (
          <EmptyState
            icon={Key}
            title="No legacy environment variables"
            description="Normalized scoped variables are added in the configuration checkpoint."
            className="m-4"
          />
        ) : (
          <ul className="divide-y divide-hairline">
            {variables.data?.map((variable) => (
              <li
                key={variable.key}
                className="grid min-h-14 grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-4 py-2.5"
              >
                <div className="min-w-0">
                  <p className="truncate font-mono text-xs font-medium">{variable.key}</p>
                  <p className="text-hint text-muted-foreground">
                    Updated {relativeTime(variable.updatedAt)}
                  </p>
                </div>
                <span className="font-mono text-xs text-muted-foreground">{variable.masked}</span>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

function OwnedFeatureTab({
  kind,
  deployment,
}: {
  kind: "logs" | "network" | "storage" | "metrics" | "console" | "players"
  deployment: DeploymentSummary
}) {
  const content = {
    logs: {
      icon: Logs,
      title: "Runtime logs",
      description:
        "Per-deployment runtime log attribution is not available yet. Deployment transcripts remain on each run page.",
      href: "/logs",
      label: "Open system logs",
    },
    network: {
      icon: Globe,
      title: "Domains & ports",
      description: deployment.endpoint
        ? `${deployment.endpoint} is recorded for this plan. Proxy and certificate health are not observed yet.`
        : "No public route is recorded. Runtime and proxy observation are not available yet.",
      href: "/proxy",
      label: "Open proxy",
    },
    storage: {
      icon: Database,
      title: "Storage & backups",
      description:
        "Storage ownership and backup freshness are not observed for this deployment yet. Existing backup tools remain available.",
      href: "/backups",
      label: "Open backups",
    },
    metrics: {
      icon: Monitoring,
      title: "Deployment metrics",
      description:
        "CPU, memory, disk, and network measurements are not attributed to this deployment yet. Server-wide history remains available.",
      href: "/metrics",
      label: "Open server metrics",
    },
    console: {
      icon: Logs,
      title: "Console & files",
      description:
        "A deployment-scoped console and file root require managed runtime ownership. Existing terminal and file tools remain available.",
      href: "/terminal",
      label: "Open terminal",
    },
    players: {
      icon: Box,
      title: "Players",
      description:
        "Game-protocol player observation arrives with the reviewed game-server integration. No player count is inferred from container state.",
      href: `/deploy/${deployment.id}?tab=overview`,
      label: "Back to overview",
    },
  }[kind]
  const Icon = content.icon
  return (
    <Panel>
      <PanelHeader icon={Icon} title={content.title} />
      <PanelBody>
        <Notice title="Integration not available" icon={Icon}>
          {content.description}
        </Notice>
        <Button variant="outline" size="sm" asChild className="mt-4">
          <Link href={content.href}>
            {content.label} <ArrowRight className="size-3.5" />
          </Link>
        </Button>
      </PanelBody>
    </Panel>
  )
}

