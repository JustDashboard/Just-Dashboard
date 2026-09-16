"use client"

import Link from "next/link"
import { ArrowRight, GitBranch, Globe, Logs, Monitoring } from "@/components/icons"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { EmptyNote, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { DeploymentSitePreview } from "@/components/deploy/deployment-site-preview"
import { DeploymentGitStatus } from "@/components/deploy/deployment-git-status"
import {
  DeploymentStatus,
  HealthStatus,
  deploymentURL,
  humanize,
  releaseLabel,
} from "@/components/deploy/deployment-ui"
import { LiveUsage } from "@/components/deploy/deployment-metrics"
import { relativeTime } from "@/lib/format"
import type {
  DeploymentEngineRun,
  DeploymentOperations,
  DeploymentRuntimeServices,
  DeploymentSummary,
  DeployProject,
} from "@/lib/types"

export function DeploymentOverview({
  deployment,
  project,
  runs,
  runtime,
  operations,
}: {
  deployment: DeploymentSummary
  project: DeployProject
  runs: DeploymentEngineRun[]
  runtime?: DeploymentRuntimeServices
  operations?: DeploymentOperations
}) {
  const url = deploymentURL(deployment.endpoint)
  const service = runtime?.services.find((item) => item.liveRelease) ?? runtime?.services[0]
  const findings = operations?.diagnosis.findings.length ?? 0
  const recent = runs.slice(0, 4)
  return (
    <div className="space-y-5">
      {deployment.activeRun && (
        <Notice title="A new deployment is in progress">
          <Link
            className="inline-flex items-center gap-2 rounded-sm underline focus-ring"
            href={`/deploy/${deployment.id}/runs/${deployment.activeRun.id}`}
          >
            Follow the build <ArrowRight className="size-3.5" />
          </Link>
        </Notice>
      )}
      <Panel>
        <PanelHeader
          title={`${humanize(deployment.environmentKind)} deployment`}
          actions={
            url && (
              <Button size="sm" asChild>
                <a href={url} target="_blank" rel="noopener noreferrer">
                  Visit <ArrowRight className="size-3.5" />
                </a>
              </Button>
            )
          }
        />
        <PanelBody className="grid items-start gap-6 p-5 lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]">
          <DeploymentSitePreview
            key={`${deployment.endpoint}:${deployment.liveReleaseId}`}
            deployment={deployment}
          />
          <div className="min-w-0 space-y-5 py-1">
            <div>
              <p className="mb-2 text-xs text-muted-foreground">{url ? "Domain" : "Access"}</p>
              {url ? (
                <a
                  className="inline-flex max-w-full items-center gap-2 rounded-sm text-sm font-medium break-all focus-ring hover:underline"
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <Globe className="size-4 shrink-0 text-muted-foreground" />
                  {new URL(url).host}
                </a>
              ) : (
                <p className="text-sm">
                  {deployment.liveReleaseId
                    ? "Private service on your server"
                    : "Your address appears after the first deployment"}
                </p>
              )}
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <p className="mb-2 text-xs text-muted-foreground">Runtime status</p>
                <HealthStatus health={deployment.health} />
              </div>
              <div>
                <p className="mb-2 text-xs text-muted-foreground">Release</p>
                <span className="font-mono text-xs">
                  {deployment.liveReleaseId ? releaseLabel(deployment) : "Not deployed yet"}
                </span>
              </div>
            </div>
            <div className="border-t border-hairline pt-4">
              <p className="mb-2 text-xs text-muted-foreground">Source</p>
              <p className="flex items-center gap-2 text-sm">
                <GitBranch className="size-4 shrink-0 text-muted-foreground" />
                <span className="truncate">
                  {deployment.sourceRef || project.branch || humanize(deployment.sourceKind)}
                </span>
              </p>
              <p
                className="mt-2 truncate font-mono text-xs text-muted-foreground"
                title={project.repoPath}
              >
                {project.repoPath || humanize(deployment.buildMethod)}
              </p>
              {deployment.buildMethod !== "legacy_compose" && (
                <div className="mt-3">
                  <DeploymentGitStatus
                    projectID={deployment.id}
                    environmentID={deployment.environmentId}
                  />
                </div>
              )}
            </div>
            {findings > 0 && (
              <Link
                href={`/deploy/${deployment.id}?tab=diagnostics`}
                className="inline-flex min-h-9 items-center gap-2 rounded-sm text-xs text-warning focus-ring"
              >
                {findings} {findings === 1 ? "item needs" : "items need"} attention{" "}
                <ArrowRight className="size-3.5" />
              </Link>
            )}
          </div>
        </PanelBody>
        <PanelFooter className="justify-between">
          <span className="text-xs text-muted-foreground">
            {deployment.pendingChanges
              ? "Saved changes are ready for your next deployment."
              : `Updated ${relativeTime(deployment.updatedAt)}`}
          </span>
          <Button size="xs" variant="ghost" asChild>
            <Link href={`/deploy/${deployment.id}?tab=configuration`}>
              Project settings <ArrowRight className="size-3" />
            </Link>
          </Button>
        </PanelFooter>
      </Panel>
      <div className="grid min-w-0 gap-5 xl:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
        <Panel>
          <PanelHeader
            title="Recent deployments"
            actions={
              <Button variant="ghost" size="xs" asChild>
                <Link href={`/deploy/${deployment.id}?tab=deployments`}>
                  View all <ArrowRight className="size-3" />
                </Link>
              </Button>
            }
          />
          <PanelBody flush>
            {recent.length ? (
              <ul className="divide-y divide-hairline">
                {recent.map((run) => (
                  <li key={run.id}>
                    <Link
                      href={`/deploy/${deployment.id}/runs/${run.id}`}
                      className="flex min-h-16 flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3 focus-ring-inset hover:bg-row-hover"
                    >
                      <span className="min-w-24 flex-1">
                        <span className="block text-sm font-medium">Run #{run.runNumber}</span>
                        <span className="text-xs text-muted-foreground">
                          {humanize(run.operation)}
                        </span>
                      </span>
                      <DeploymentStatus state={run.state} />
                      <span className="text-xs text-muted-foreground">
                        {relativeTime(run.requestedAt)}
                      </span>
                      <ArrowRight className="size-3.5 text-muted-foreground" />
                    </Link>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="p-5">
                <EmptyNote>
                  Start your first deployment to see its build and release here.
                </EmptyNote>
              </div>
            )}
          </PanelBody>
        </Panel>
        <Panel>
          <PanelHeader
            title="Resource usage"
            actions={
              <Button variant="ghost" size="xs" asChild>
                <Link href={`/deploy/${deployment.id}?tab=metrics`}>
                  <Monitoring className="size-3" /> Details
                </Link>
              </Button>
            }
          />
          <PanelBody className="p-5">
            {service && runtime?.status === "available" ? (
              <LiveUsage key={service.containerId} containerId={service.containerId} compact />
            ) : (
              <EmptyNote>
                {runtime?.reason || "Usage appears when your application starts."}
              </EmptyNote>
            )}
          </PanelBody>
          <PanelFooter className="justify-between">
            <span className="truncate text-xs text-muted-foreground">
              {service?.name || "Application runtime"}
            </span>
            <Button variant="ghost" size="xs" asChild>
              <Link href={`/deploy/${deployment.id}?tab=logs`}>
                <Logs className="size-3" /> Logs
              </Link>
            </Button>
          </PanelFooter>
        </Panel>
      </div>
      <Panel>
        <PanelHeader
          title="Production branch"
          actions={
            <Button variant="ghost" size="xs" asChild>
              <Link href={`/deploy/${deployment.id}?tab=automations`}>
                Manage automation <ArrowRight className="size-3" />
              </Link>
            </Button>
          }
        />
        <PanelBody className="p-5">
          <DetailList>
            <Detail label="Branch">
              <span className="inline-flex items-center gap-2">
                <GitBranch className="size-3.5" />
                {project.branch || deployment.sourceRef || "No Git branch"}
              </span>
            </Detail>
            <Detail label="Deploy on push">
              {project.enabled ? "Enabled" : "Manual deployments"}
            </Detail>
          </DetailList>
        </PanelBody>
      </Panel>
    </div>
  )
}
