"use client"

import Link from "next/link"
import { ArrowRight, GitBranch, Globe, Logs, Monitoring } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
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

/**
 * The project's front page: what is live, where, from which source, and what
 * happened last.
 *
 * Nothing here is framed. It used to be four boxes — the production
 * deployment, recent deployments, resource usage and the production branch —
 * and the fourth repeated two facts the first already carried. The preview
 * and its facts now sit on the page; the branch and its automation are two
 * more facts beside the source; the lists underneath are titled, ruled and
 * otherwise bare.
 */
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
  const recent = runs.slice(0, 5)
  return (
    <div className="space-y-8">
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

      <Panel plain>
        <PanelHeader
          title={`${humanize(deployment.environmentKind)} deployment`}
          actions={
            url && (
              <Button size="sm" variant="outline" asChild>
                <a href={url} target="_blank" rel="noopener noreferrer">
                  Visit <ArrowRight className="size-3.5" />
                </a>
              </Button>
            )
          }
        />
        <PanelBody
          flush
          className="grid items-start gap-8 pt-5 lg:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)]"
        >
          <DeploymentSitePreview
            key={`${deployment.endpoint}:${deployment.liveReleaseId}`}
            deployment={deployment}
          />
          <dl className="min-w-0 space-y-5">
            <Fact label={url ? "Domain" : "Access"}>
              {url ? (
                <a
                  className="inline-flex max-w-full items-center gap-2 rounded-sm font-medium break-all focus-ring hover:underline"
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <Globe className="size-3.5 shrink-0 text-muted-foreground" />
                  {new URL(url).host}
                </a>
              ) : deployment.liveReleaseId ? (
                "Private service on your server"
              ) : (
                "Your address appears after the first deployment"
              )}
            </Fact>
            <div className="grid grid-cols-2 gap-5">
              <Fact label="Status">
                <HealthStatus health={deployment.health} />
              </Fact>
              <Fact label="Release">
                <span className="font-mono text-xs">
                  {deployment.liveReleaseId ? releaseLabel(deployment) : "Not deployed yet"}
                </span>
              </Fact>
            </div>
            <Fact label="Source">
              <span className="flex min-w-0 items-center gap-2">
                <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="truncate">
                  {deployment.sourceRef || project.branch || humanize(deployment.sourceKind)}
                </span>
              </span>
              <span
                className="mt-1 block truncate font-mono text-xs text-muted-foreground"
                title={project.repoPath}
              >
                {project.repoPath || humanize(deployment.buildMethod)}
              </span>
              {deployment.buildMethod !== "legacy_compose" && (
                <span className="mt-3 block">
                  <DeploymentGitStatus
                    projectID={deployment.id}
                    environmentID={deployment.environmentId}
                  />
                </span>
              )}
            </Fact>
            <Fact label="Deploy on push">
              <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span>{project.enabled ? "Enabled" : "Manual deployments"}</span>
                <Link
                  href={`/deploy/${deployment.id}?tab=automations`}
                  className="inline-flex items-center gap-1 rounded-sm text-xs text-muted-foreground focus-ring hover:text-foreground"
                >
                  Manage automation <ArrowRight className="size-3" />
                </Link>
              </span>
            </Fact>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-hairline pt-4 text-xs">
              {findings > 0 && (
                <Link
                  href={`/deploy/${deployment.id}?tab=diagnostics`}
                  className="inline-flex items-center gap-1.5 rounded-sm text-warning focus-ring"
                >
                  {findings} {findings === 1 ? "item needs" : "items need"} attention
                  <ArrowRight className="size-3" />
                </Link>
              )}
              <span className="text-muted-foreground">
                {deployment.pendingChanges
                  ? "Saved changes are ready for your next deployment."
                  : `Updated ${relativeTime(deployment.updatedAt)}`}
              </span>
              <Link
                href={`/deploy/${deployment.id}?tab=configuration`}
                className="ml-auto inline-flex items-center gap-1 rounded-sm text-muted-foreground focus-ring hover:text-foreground"
              >
                Project settings <ArrowRight className="size-3" />
              </Link>
            </div>
          </dl>
        </PanelBody>
      </Panel>

      <div className="grid min-w-0 gap-8 xl:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
        <Panel plain>
          <PanelHeader
            title="Recent deployments"
            actions={
              <Link
                href={`/deploy/${deployment.id}?tab=deployments`}
                className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
              >
                View all <ArrowRight className="size-3" />
              </Link>
            }
          />
          <PanelBody flush>
            {recent.length ? (
              <RowList>
                {recent.map((run) => (
                  <Row
                    key={run.id}
                    href={`/deploy/${deployment.id}/runs/${run.id}`}
                    title={`Run #${run.runNumber}`}
                    subtitle={humanize(run.operation)}
                    trailing={
                      <>
                        <DeploymentStatus state={run.state} />
                        <span className="numeric text-hint text-muted-foreground">
                          {relativeTime(run.requestedAt)}
                        </span>
                      </>
                    }
                  />
                ))}
              </RowList>
            ) : (
              <EmptyNote className="px-0 py-5 text-left">
                Start your first deployment to see its build and release here.
              </EmptyNote>
            )}
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader
            title="Resource usage"
            actions={
              <>
                <Link
                  href={`/deploy/${deployment.id}?tab=logs`}
                  className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
                >
                  <Logs className="size-3" /> Logs
                </Link>
                <Link
                  href={`/deploy/${deployment.id}?tab=metrics`}
                  className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
                >
                  <Monitoring className="size-3" /> Details
                </Link>
              </>
            }
          />
          <PanelBody flush className="pt-4">
            {service && runtime?.status === "available" ? (
              <LiveUsage key={service.containerId} containerId={service.containerId} compact />
            ) : (
              <EmptyNote className="px-0 py-5 text-left">
                {runtime?.reason || "Usage appears when your application starts."}
              </EmptyNote>
            )}
            <p className="mt-3 truncate text-hint text-muted-foreground">
              {service?.name || "Application runtime"}
            </p>
          </PanelBody>
        </Panel>
      </div>
    </div>
  )
}

/** One labelled fact in the overview's column: a small name over its value. */
function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="mb-1 text-hint text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-sm">{children}</dd>
    </div>
  )
}
